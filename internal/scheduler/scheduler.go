// Package scheduler 实现死亡开关的核心状态机。
//
// 设计原则（计划中的两块基石）：
//  1. 时间戳纯函数调度：Tick 的所有决策都基于「当前时间 now + 持久化的绝对时间戳」，
//     不依赖内存计数器或进程不间断运行。VPS 宕机数天恢复后，单次 Tick 即可算出
//     真实应处的阶段并补做动作。
//  2. 幂等投递：每阶段发送前查 deliveries 表，已发则跳过，重启重放绝不重复。
package scheduler

import (
	"fmt"
	"math"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

// Notifier 抽象对外通知（Telegram + Email），便于测试注入假实现。
type Notifier interface {
	// SendCheckin 发送带确认按钮的 Telegram 消息（携带一次性 token）。
	SendCheckin(token string) error
	// SendFinalWarning 发送高优先级的最后强提醒（Telegram）。
	SendFinalWarning() error
	// SendHeartbeat 发送服务巡检心跳（Telegram）。
	SendHeartbeat() error
	// SendMessageSafe 发送一条任意文本 Telegram 消息（如取消通知）。
	SendMessageSafe(text string) error
	// SendOwnerAlert 在进入触发预备时给用户本人发邮件（多通道兜底）。
	SendOwnerAlert() error
	// DeliverWarn/Password/File 分别给单个受益人投递三阶段邮件。
	DeliverWarn(b config.Beneficiary) error
	DeliverPassword(b config.Beneficiary, password string) error
	DeliverFile(b config.Beneficiary, archivePath, password string) error
}

// Packer 抽象打包，便于测试。
type Packer interface {
	// Pack 打包源目录，返回 (路径, sha256, 密码)。
	Pack(now time.Time) (path, sha256, password string, err error)
}

// TokenGen 生成一次性确认 token。
type TokenGen func() (string, error)

// Scheduler 持有依赖。它本身无状态，状态都在 store 里。
type Scheduler struct {
	cfg      *config.Config
	store    *state.Store
	notifier Notifier
	packer   Packer
	tokenGen TokenGen
	logf     func(format string, args ...any)
}

// New 创建调度器。logf 可为 nil。
func New(cfg *config.Config, store *state.Store, n Notifier, p Packer, tg TokenGen, logf func(string, ...any)) *Scheduler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Scheduler{cfg: cfg, store: store, notifier: n, packer: p, tokenGen: tg, logf: logf}
}

// Confirm 处理用户的一次确认。返回是否被接受（token 匹配）。
// 这是「用户还活着」的唯一入口：在任何阶段都立即把状态重置回 ALIVE，
// 清空触发流程的时间戳与投递记录。
func (s *Scheduler) Confirm(now time.Time, token string) (accepted bool, err error) {
	st, err := s.store.Load()
	if err != nil {
		return false, err
	}
	// token 必须匹配当前 pending_token，防重放旧确认。
	if st.PendingToken == "" || token != st.PendingToken {
		return false, nil
	}

	wasTriggering := st.Phase != state.PhaseAlive && st.Phase != state.PhaseGrace

	st.Phase = state.PhaseAlive
	st.LastConfirmedAt = ptr(now)
	st.MissCount = 0
	st.PendingToken = ""
	st.FinalWarningAt = nil
	st.WarnedAt = nil
	st.PasswordSentAt = nil
	st.FileSentAt = nil

	if err := s.store.Save(st); err != nil {
		return false, err
	}
	// 取消触发流程后清空投递记录，下次真触发重新走完整流程。
	if wasTriggering {
		if err := s.store.ClearDeliveries(); err != nil {
			return false, err
		}
		_ = s.notifier.SendMessageSafe("已收到本轮确认，后续流程已暂停。")
	}
	_ = s.store.Audit("user_confirmed", fmt.Sprintf("from_phase_triggering=%v", wasTriggering), now)
	s.logf("用户已确认，状态重置为 ALIVE（之前处于触发流程=%v）", wasTriggering)
	return true, nil
}

// Tick 是周期性主循环的一拍。读取状态 + now，推导并执行该做的动作。
func (s *Scheduler) Tick(now time.Time) error {
	st, err := s.store.Load()
	if err != nil {
		return err
	}

	// --- 公共：到打包周期就重新打包（任何活跃态都保持最新加密包+密码） ---
	if s.due(st.LastPackAt, s.cfg.Intervals.PackInterval.Std(), now) && !isTerminal(st.Phase) {
		if err := s.doPack(st, now); err != nil {
			// 打包失败不阻断后续判定，记录后继续。
			s.logf("打包失败: %v", err)
			_ = s.store.Audit("pack_failed", err.Error(), now)
		}
	}

	switch st.Phase {
	case state.PhaseAlive, state.PhaseGrace:
		return s.tickAlive(st, now)
	case state.PhasePendingTrigger:
		return s.tickPendingTrigger(st, now)
	case state.PhaseWarned:
		return s.tickWarned(st, now)
	case state.PhasePasswordSent:
		return s.tickPasswordSent(st, now)
	case state.PhaseFileSent, state.PhaseCompleted:
		return s.tickTerminal(st, now)
	}
	return nil
}

func (s *Scheduler) tickAlive(st *state.State, now time.Time) error {
	ci := s.cfg.Intervals.CheckinInterval.Std()

	// 1) 该不该发确认消息
	if s.due(st.LastCheckinSentAt, ci, now) {
		token, err := s.tokenGen()
		if err != nil {
			return err
		}
		if err := s.notifier.SendCheckin(token); err != nil {
			s.logf("发送确认消息失败: %v", err)
			_ = s.store.Audit("checkin_send_failed", err.Error(), now)
			// 发送失败则不更新时间戳，下拍重试。
		} else {
			st.PendingToken = token
			st.LastCheckinSentAt = ptr(now)
			_ = s.store.Audit("checkin_sent", "", now)
		}
	}

	// 2) 漏回合数 = 纯函数（抗宕机关键）
	missed := missedRounds(st.LastConfirmedAt, ci, now)
	st.MissCount = missed
	if missed >= 1 && st.Phase == state.PhaseAlive {
		st.Phase = state.PhaseGrace
		s.logf("进入 GRACE：已漏 %d 回合", missed)
	}

	// 3) 达阈值 → 最后强提醒 + 进入触发预备
	if missed >= s.cfg.Intervals.MissThreshold {
		if err := s.notifier.SendFinalWarning(); err != nil {
			s.logf("发送最后强提醒失败: %v", err)
		}
		_ = s.notifier.SendOwnerAlert() // 多通道兜底，失败忽略
		st.Phase = state.PhasePendingTrigger
		st.FinalWarningAt = ptr(now)
		_ = s.store.Audit("entered_pending_trigger", fmt.Sprintf("missed=%d", missed), now)
		s.logf("漏回合达阈值 %d，进入 PENDING_TRIGGER", s.cfg.Intervals.MissThreshold)
	}

	s.maybeHeartbeat(st, now)
	return s.store.Save(st)
}

func (s *Scheduler) tickPendingTrigger(st *state.State, now time.Time) error {
	if s.due(st.FinalWarningAt, s.cfg.Intervals.FinalGrace.Std(), now) {
		allDelivered := true
		for _, b := range s.cfg.Beneficiaries {
			if !s.deliver(b.Email, state.StageWarn, now, func() error {
				return s.notifier.DeliverWarn(b)
			}) {
				allDelivered = false
			}
		}
		if !allDelivered {
			_ = s.store.Audit("warn_delivery_waiting_retry", "部分预警邮件投递失败，暂不推进阶段", now)
			s.logf("部分预警邮件投递失败，保持 PENDING_TRIGGER 等待重试")
			return s.store.Save(st)
		}
		st.Phase = state.PhaseWarned
		st.WarnedAt = ptr(now)
		_ = s.store.Audit("entered_warned", "预警邮件已发", now)
		s.logf("进入 WARNED：预警邮件已发给受益人")
	}
	return s.store.Save(st)
}

func (s *Scheduler) tickWarned(st *state.State, now time.Time) error {
	if s.due(st.WarnedAt, s.cfg.Intervals.PasswordDelay.Std(), now) {
		password := st.CurrentArchivePassword // 迭代2 起这里可能是加密的，由 notifier 层解密
		allDelivered := true
		for _, b := range s.cfg.Beneficiaries {
			if !s.deliver(b.Email, state.StagePassword, now, func() error {
				return s.notifier.DeliverPassword(b, password)
			}) {
				allDelivered = false
			}
		}
		if !allDelivered {
			_ = s.store.Audit("password_delivery_waiting_retry", "部分密码邮件投递失败，暂不推进阶段", now)
			s.logf("部分密码邮件投递失败，保持 WARNED 等待重试")
			return s.store.Save(st)
		}
		st.Phase = state.PhasePasswordSent
		st.PasswordSentAt = ptr(now)
		_ = s.store.Audit("entered_password_sent", "解压密码已发", now)
		s.logf("进入 PASSWORD_SENT：解压密码已发给受益人")
	}
	return s.store.Save(st)
}

func (s *Scheduler) tickPasswordSent(st *state.State, now time.Time) error {
	if s.due(st.PasswordSentAt, s.cfg.Intervals.FileDelay.Std(), now) {
		allDelivered := true
		for _, b := range s.cfg.Beneficiaries {
			if !s.deliver(b.Email, state.StageFile, now, func() error {
				return s.notifier.DeliverFile(b, st.CurrentArchivePath, st.CurrentArchivePassword)
			}) {
				allDelivered = false
			}
		}
		if !allDelivered {
			_ = s.store.Audit("file_delivery_waiting_retry", "部分文件邮件投递失败，暂不推进阶段", now)
			s.logf("部分文件邮件投递失败，保持 PASSWORD_SENT 等待重试")
			return s.store.Save(st)
		}
		st.Phase = state.PhaseFileSent
		st.FileSentAt = ptr(now)
		_ = s.store.Audit("entered_file_sent", "压缩文件已发", now)
		s.logf("进入 FILE_SENT：压缩文件已发给受益人")
	}
	return s.store.Save(st)
}

func (s *Scheduler) tickTerminal(st *state.State, now time.Time) error {
	_ = s.store.CleanupExpiredTokens(now)
	if st.Phase == state.PhaseFileSent {
		st.Phase = state.PhaseCompleted
		_ = s.store.Audit("completed", "", now)
		return s.store.Save(st)
	}
	return nil
}

// deliver 执行一次幂等投递：已成功则跳过，否则调用 fn 并记录结果。
// 返回 true 表示该受益人的该阶段已成功完成，可用于判断阶段是否能推进。
func (s *Scheduler) deliver(email string, stage state.Stage, now time.Time, fn func() error) bool {
	done, err := s.store.AlreadyDelivered(email, stage)
	if err != nil {
		s.logf("查询投递记录失败 (%s/%s): %v", email, stage, err)
		return false
	}
	if done {
		return true
	}
	if err := fn(); err != nil {
		s.logf("投递失败 (%s/%s): %v", email, stage, err)
		if recordErr := s.store.RecordDelivery(email, stage, "FAILED", err.Error(), now); recordErr != nil {
			s.logf("记录投递失败状态失败 (%s/%s): %v", email, stage, recordErr)
		}
		return false
	}
	if err := s.store.RecordDelivery(email, stage, "OK", "", now); err != nil {
		s.logf("记录投递成功状态失败 (%s/%s): %v", email, stage, err)
		return false
	}
	s.logf("投递成功 (%s/%s)", email, stage)
	return true
}

func (s *Scheduler) doPack(st *state.State, now time.Time) error {
	path, sha, pwd, err := s.packer.Pack(now)
	if err != nil {
		return err
	}
	st.CurrentArchivePath = path
	st.CurrentArchiveSHA256 = sha
	st.CurrentArchivePassword = pwd
	st.LastPackAt = ptr(now)
	_ = s.store.Audit("packed", sha, now)
	s.logf("已打包: %s (sha256=%s)", path, sha)
	return nil
}

func (s *Scheduler) maybeHeartbeat(st *state.State, now time.Time) {
	if !s.cfg.Reliability.HeartbeatEnabled {
		return
	}
	if s.due(st.LastHeartbeatAt, s.cfg.Reliability.HeartbeatInterval.Std(), now) {
		if err := s.notifier.SendHeartbeat(); err != nil {
			s.logf("发送心跳失败: %v", err)
			return
		}
		st.LastHeartbeatAt = ptr(now)
	}
}

// --- 纯函数辅助 ---

// due 判断距上次动作是否已超过 interval。t 为 nil（从未做过）时视为「到期」。
func (s *Scheduler) due(t *time.Time, interval time.Duration, now time.Time) bool {
	if t == nil {
		return true
	}
	return now.Sub(*t) >= interval
}

// missedRounds = floor((now - lastConfirmed) / checkinInterval)。
// 这是抗宕机的核心：纯由时间戳推导，与进程是否连续运行无关。
func missedRounds(lastConfirmed *time.Time, interval time.Duration, now time.Time) int {
	if lastConfirmed == nil || interval <= 0 {
		return 0
	}
	elapsed := now.Sub(*lastConfirmed)
	if elapsed < interval {
		return 0
	}
	return int(math.Floor(float64(elapsed) / float64(interval)))
}

func isTerminal(p state.Phase) bool {
	return p == state.PhaseFileSent || p == state.PhaseCompleted
}

func ptr(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}
