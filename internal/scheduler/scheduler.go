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
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/secretbox"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

// Notifier 抽象对外通知（Telegram + Email），便于测试注入假实现。
type Notifier interface {
	// SendCheckin 发送带确认按钮的 Telegram 消息（携带一次性 token）。
	SendCheckin(token string) error
	// SendReminder 发送连续提醒阶段的第 n 次 Telegram 提醒。
	SendReminder(n int, isLast bool) error
	// SendStageReminder 在每个受益人邮件阶段前提醒用户本人。
	SendStageReminder(stage state.Stage) error
	// SendHeartbeat 发送服务巡检心跳（Telegram）。
	SendHeartbeat() error
	// SendMessageSafe 发送一条任意文本 Telegram 消息（如取消通知）。
	SendMessageSafe(text string) error
	// DeliverWarn/Password/File 分别给单个受益人投递三阶段邮件。
	DeliverWarn(b config.Beneficiary, passwordSendDate, fileLinkSendDate time.Time) error
	DeliverPassword(b config.Beneficiary, password string, fileLinkSendDate time.Time) error
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
		st.CurrentArchivePath = ""
		st.CurrentArchivePassword = ""
		st.CurrentArchiveSHA256 = ""
		st.LastPackAt = nil
		if err := s.store.Save(st); err != nil {
			return false, err
		}
		if err := s.store.ClearDeliveries(); err != nil {
			return false, err
		}
		_ = s.notifier.SendMessageSafe("")
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
	if hasOutstandingCheckin(st) {
		return s.tickOutstandingCheckin(st, now)
	}

	if shouldSendMonthlyCheckin(st.LastCheckinSentAt, s.cfg.TargetFlow, now) {
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
			st.MissCount = 0
			_ = s.store.Audit("checkin_sent", "", now)
		}
	}

	s.maybeHeartbeat(st, now)
	return s.store.Save(st)
}

func (s *Scheduler) tickOutstandingCheckin(st *state.State, now time.Time) error {
	n := remindersSent(*st.LastCheckinSentAt, now, s.cfg.TargetFlow.ReminderInterval.Std())
	if n < 1 {
		s.maybeHeartbeat(st, now)
		return s.store.Save(st)
	}

	if st.Phase == state.PhaseAlive {
		st.Phase = state.PhaseGrace
		s.logf("进入 GRACE：本月确认已漏第 %d 次提醒", n)
	}

	if n <= s.cfg.TargetFlow.ReminderCount {
		if st.MissCount < n {
			isLast := n == s.cfg.TargetFlow.ReminderCount
			if err := s.notifier.SendReminder(n, isLast); err != nil {
				s.logf("发送连续提醒失败: %v", err)
				_ = s.store.Audit("reminder_failed", err.Error(), now)
			} else {
				st.MissCount = n
				_ = s.store.Audit("reminder_sent", fmt.Sprintf("n=%d,last=%v", n, isLast), now)
			}
		}
	} else {
		st.Phase = state.PhasePendingTrigger
		st.MissCount = n
		st.FinalWarningAt = ptr(now)
		_ = s.store.Audit("entered_pending_trigger", fmt.Sprintf("reminder_count=%d", n), now)
		s.logf("连续提醒期已结束，进入 PENDING_TRIGGER")
	}

	s.maybeHeartbeat(st, now)
	return s.store.Save(st)
}

func (s *Scheduler) tickPendingTrigger(st *state.State, now time.Time) error {
	if st.FinalWarningAt == nil {
		st.FinalWarningAt = ptr(now)
	}
	if !s.deliverOwnerStageReminder(state.StageWarnTelegram, state.StageWarn, now) {
		return s.store.Save(st)
	}

	passwordSendDate := now.Add(s.cfg.TargetFlow.PasswordDelayAfterWarn.Std())
	fileLinkSendDate := passwordSendDate.Add(s.cfg.TargetFlow.FileDelayAfterPassword.Std())
	allDelivered := true
	for _, b := range s.cfg.Beneficiaries {
		if !s.deliver(b.Email, state.StageWarn, now, func() error {
			return s.notifier.DeliverWarn(b, passwordSendDate, fileLinkSendDate)
		}) {
			allDelivered = false
		}
	}
	if !allDelivered {
		_ = s.store.Audit("warn_delivery_waiting_retry", "部分预提醒邮件投递失败，暂不推进阶段", now)
		s.logf("部分预提醒邮件投递失败，保持 PENDING_TRIGGER 等待重试")
		return s.store.Save(st)
	}
	st.Phase = state.PhaseWarned
	st.WarnedAt = ptr(now)
	_ = s.store.Audit("entered_warned", "预提醒邮件已发", now)
	s.logf("进入 WARNED：预提醒邮件已发给受益人")
	return s.store.Save(st)
}

func (s *Scheduler) tickWarned(st *state.State, now time.Time) error {
	if s.due(st.WarnedAt, s.cfg.TargetFlow.PasswordDelayAfterWarn.Std(), now) {
		if !s.deliverOwnerStageReminder(state.StagePasswordTelegram, state.StagePassword, now) {
			return s.store.Save(st)
		}
		if st.CurrentArchivePath == "" || st.CurrentArchivePassword == "" {
			if err := s.doPack(st, now); err != nil {
				s.logf("密码阶段打包失败: %v", err)
				_ = s.store.Audit("pack_failed", err.Error(), now)
				return s.store.Save(st)
			}
		}
		password, err := s.archivePassword(st)
		if err != nil {
			s.logf("读取归档密码失败: %v", err)
			_ = s.store.Audit("archive_password_read_failed", err.Error(), now)
			return s.store.Save(st)
		}
		fileLinkSendDate := now.Add(s.cfg.TargetFlow.FileDelayAfterPassword.Std())
		allDelivered := true
		for _, b := range s.cfg.Beneficiaries {
			if !s.deliver(b.Email, state.StagePassword, now, func() error {
				return s.notifier.DeliverPassword(b, password, fileLinkSendDate)
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
	if s.due(st.PasswordSentAt, s.cfg.TargetFlow.FileDelayAfterPassword.Std(), now) {
		if !s.deliverOwnerStageReminder(state.StageFileTelegram, state.StageFile, now) {
			return s.store.Save(st)
		}
		allDelivered := true
		for _, b := range s.cfg.Beneficiaries {
			if !s.deliver(b.Email, state.StageFile, now, func() error {
				password, err := s.archivePassword(st)
				if err != nil {
					return err
				}
				return s.notifier.DeliverFile(b, st.CurrentArchivePath, password)
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
		_ = s.store.Audit("entered_file_sent", "下载链接已发", now)
		s.logf("进入 FILE_SENT：下载链接已发给受益人")
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

func (s *Scheduler) deliverOwnerStageReminder(recordStage, flowStage state.Stage, now time.Time) bool {
	return s.deliver("__owner_telegram__", recordStage, now, func() error {
		return s.notifier.SendStageReminder(flowStage)
	})
}

func (s *Scheduler) doPack(st *state.State, now time.Time) error {
	path, sha, pwd, err := s.packer.Pack(now)
	if err != nil {
		return err
	}
	st.CurrentArchivePath = path
	st.CurrentArchiveSHA256 = sha
	st.CurrentArchivePassword = pwd
	if s.cfg.StateProtection.EncryptPasswordField {
		encrypted, err := secretbox.Encrypt(pwd, s.cfg.StateProtection.MasterPassphrase)
		if err != nil {
			return err
		}
		st.CurrentArchivePassword = encrypted
	}
	st.LastPackAt = ptr(now)
	_ = s.store.Audit("packed", sha, now)
	s.logf("已打包: %s (sha256=%s)", path, sha)
	return nil
}

func (s *Scheduler) archivePassword(st *state.State) (string, error) {
	if st.CurrentArchivePassword == "" {
		return "", fmt.Errorf("当前归档密码为空")
	}
	if !s.cfg.StateProtection.EncryptPasswordField {
		return st.CurrentArchivePassword, nil
	}
	return secretbox.Decrypt(st.CurrentArchivePassword, s.cfg.StateProtection.MasterPassphrase)
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

func isTerminal(p state.Phase) bool {
	return p == state.PhaseFileSent || p == state.PhaseCompleted
}

func ptr(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}

func shouldSendMonthlyCheckin(lastSent *time.Time, flow config.TargetFlow, now time.Time) bool {
	loc := loadLocation(flow.Timezone)
	current := monthlyCheckinTime(now, flow.CheckinDayOfMonth, loc)
	if now.Before(current) {
		return false
	}
	if lastSent == nil {
		return true
	}
	return lastSent.Before(current)
}

func hasOutstandingCheckin(st *state.State) bool {
	if st.LastCheckinSentAt == nil || st.PendingToken == "" {
		return false
	}
	return st.LastConfirmedAt == nil || st.LastConfirmedAt.Before(*st.LastCheckinSentAt)
}

func monthlyCheckinTime(now time.Time, day int, loc *time.Location) time.Time {
	local := now.In(loc)
	y, m, _ := local.Date()
	maxDay := daysInMonth(y, m)
	if day > maxDay {
		day = maxDay
	}
	return time.Date(y, m, day, 0, 0, 0, 0, loc).UTC()
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func remindersSent(checkinSentAt time.Time, now time.Time, interval time.Duration) int {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if now.Before(checkinSentAt.Add(interval)) {
		return 0
	}
	return int(now.Sub(checkinSentAt) / interval)
}

func loadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}
