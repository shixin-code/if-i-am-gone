package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ofilm/if-i-am-gone/internal/config"
	"github.com/ofilm/if-i-am-gone/internal/state"
)

// --- 测试替身 ---

// fakeNotifier 记录每种通知/投递发生了多少次。
type fakeNotifier struct {
	checkins             int
	dailyReminders       []int
	lastReminderWasFinal bool
	stageReminders       map[state.Stage]int
	heartbeats           int
	messages             int
	warnSent             map[string]int
	passwordSent         map[string]int
	fileSent             map[string]int
	failWarn             map[string]bool
	failPassword         map[string]bool
	failFile             map[string]bool
	lastPassword         string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{
		warnSent:       map[string]int{},
		passwordSent:   map[string]int{},
		fileSent:       map[string]int{},
		stageReminders: map[state.Stage]int{},
		failWarn:       map[string]bool{},
		failPassword:   map[string]bool{},
		failFile:       map[string]bool{},
	}
}

func (f *fakeNotifier) SendCheckin(token string) error { f.checkins++; return nil }
func (f *fakeNotifier) SendReminder(n int, isLast bool) error {
	f.dailyReminders = append(f.dailyReminders, n)
	f.lastReminderWasFinal = isLast
	return nil
}
func (f *fakeNotifier) SendStageReminder(stage state.Stage) error {
	f.stageReminders[stage]++
	return nil
}
func (f *fakeNotifier) SendHeartbeat() error              { f.heartbeats++; return nil }
func (f *fakeNotifier) SendMessageSafe(text string) error { f.messages++; return nil }
func (f *fakeNotifier) DeliverWarn(b config.Beneficiary, passwordSendDate, fileLinkSendDate time.Time) error {
	f.warnSent[b.Email]++
	if f.failWarn[b.Email] {
		return errors.New("warn delivery failed")
	}
	return nil
}
func (f *fakeNotifier) DeliverPassword(b config.Beneficiary, password string, fileLinkSendDate time.Time) error {
	f.passwordSent[b.Email]++
	if f.failPassword[b.Email] {
		return errors.New("password delivery failed")
	}
	f.lastPassword = password
	return nil
}
func (f *fakeNotifier) DeliverFile(b config.Beneficiary, archivePath, password string) error {
	f.fileSent[b.Email]++
	if f.failFile[b.Email] {
		return errors.New("file delivery failed")
	}
	return nil
}

// fakePacker 只计数，不真打包。
type fakePacker struct{ packs int }

func (p *fakePacker) Pack(now time.Time) (string, string, string, error) {
	p.packs++
	return "/tmp/archive.zip", "deadbeef", "secret-password", nil
}

// --- 测试脚手架 ---

func newTestScheduler(t *testing.T) (*Scheduler, *state.Store, *fakeNotifier, *fakePacker) {
	s, store, n, p, _ := newTestSchedulerCfg(t)
	return s, store, n, p
}

func newTestSchedulerCfg(t *testing.T) (*Scheduler, *state.Store, *fakeNotifier, *fakePacker, *config.Config) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.Config{
		SourceDir: "/tmp",
		TargetFlow: config.TargetFlow{
			CheckinDayOfMonth:      1,
			ReminderCount:          7,
			ReminderInterval:       config.Duration(24 * time.Hour),
			PasswordDelayAfterWarn: config.Duration(72 * time.Hour),
			FileDelayAfterPassword: config.Duration(96 * time.Hour),
			Timezone:               "UTC",
		},
		Archive: config.Archive{KeepArchives: 3, PasswordLength: 32, LargeFileThreshold: config.Bytes(20 << 20)},
		Beneficiaries: []config.Beneficiary{
			{Name: "A", Email: "a@example.com", Lang: "zh"},
			{Name: "B", Email: "b@example.com", Lang: "zh"},
		},
		Reliability: config.Reliability{HeartbeatEnabled: true, HeartbeatInterval: config.Duration(168 * time.Hour)},
	}
	n := newFakeNotifier()
	p := &fakePacker{}
	tok := func() (string, error) { return "tok-fixed", nil }
	s := New(cfg, store, n, p, tok, nil)
	return s, store, n, p, cfg
}

// base 是测试用的固定起点时间（避免不确定性）。
var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func setConfirmed(t *testing.T, store *state.Store, at time.Time) {
	t.Helper()
	st, _ := store.Load()
	tt := at.UTC()
	st.LastConfirmedAt = &tt
	st.Phase = state.PhaseAlive
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
}

// --- 测试用例 ---

// 正常确认应重置回 ALIVE 并清零 miss_count。
func TestConfirmResetsToAlive(t *testing.T) {
	s, store, _, _ := newTestScheduler(t)
	setConfirmed(t, store, base)

	// 月度确认日 tick 设置 pending_token。
	if err := s.Tick(base); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	if st.PendingToken == "" {
		t.Fatal("期望 tick 设置 pending_token")
	}

	accepted, err := s.Confirm(base.Add(time.Hour), st.PendingToken)
	if err != nil || !accepted {
		t.Fatalf("确认应被接受, accepted=%v err=%v", accepted, err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseAlive || st.MissCount != 0 || st.PendingToken != "" {
		t.Fatalf("确认后状态不对: %+v", st)
	}
}

func TestConfirmDoesNotRepeatMonthlyCheckinInSameMonth(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))

	if err := s.Tick(base); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	accepted, err := s.Confirm(base.Add(time.Hour), st.PendingToken)
	if err != nil || !accepted {
		t.Fatalf("确认应被接受, accepted=%v err=%v", accepted, err)
	}

	if err := s.Tick(base.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n.checkins != 1 {
		t.Fatalf("同月确认后不应重复发送确认消息，实际 %d", n.checkins)
	}
}

// 错误 token 不应被接受（防重放）。
func TestConfirmWrongTokenRejected(t *testing.T) {
	s, store, _, _ := newTestScheduler(t)
	setConfirmed(t, store, base)
	_ = s.Tick(base)

	accepted, err := s.Confirm(base.Add(26*time.Hour), "wrong-token")
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("错误 token 不应被接受")
	}
}

// 月度确认未点击时，应先进入连续提醒，再在提醒期结束后进入 PENDING_TRIGGER。
func TestMonthlyReminderProgression(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))

	_ = s.Tick(base)
	if n.checkins != 1 {
		t.Fatalf("确认日应发送本月确认，实际 %d", n.checkins)
	}

	// D1 → GRACE + 第 1 天提醒
	_ = s.Tick(base.Add(24 * time.Hour))
	st, _ := store.Load()
	if st.Phase != state.PhaseGrace {
		t.Fatalf("D1 应为 GRACE，实际 %s", st.Phase)
	}
	if len(n.dailyReminders) != 1 || n.dailyReminders[0] != 1 {
		t.Fatalf("D1 应发送第 1 天提醒，实际 %+v", n.dailyReminders)
	}

	// D7 → 最后一天提醒
	_ = s.Tick(base.Add(7 * 24 * time.Hour))
	if !n.lastReminderWasFinal {
		t.Fatal("D7 应发送最后连续提醒文案")
	}

	// D8 → PENDING_TRIGGER
	_ = s.Tick(base.Add(8*24*time.Hour + time.Minute))
	st, _ = store.Load()
	if st.Phase != state.PhasePendingTrigger {
		t.Fatalf("连续提醒结束后应为 PENDING_TRIGGER，实际 %s", st.Phase)
	}
}

// 分钟级 reminder_interval 应让连续提醒期按分钟推进，无需等待整天。
func TestMinuteLevelReminderInterval(t *testing.T) {
	s, store, n, _, cfg := newTestSchedulerCfg(t)
	cfg.TargetFlow.ReminderCount = 2
	cfg.TargetFlow.ReminderInterval = config.Duration(time.Minute)
	setConfirmed(t, store, base.Add(-time.Hour))

	_ = s.Tick(base) // D0 发本月确认
	if n.checkins != 1 {
		t.Fatalf("确认日应发送本月确认，实际 %d", n.checkins)
	}

	// 第 1 分钟 → GRACE + 第 1 次提醒
	_ = s.Tick(base.Add(time.Minute))
	st, _ := store.Load()
	if st.Phase != state.PhaseGrace {
		t.Fatalf("第 1 分钟应为 GRACE，实际 %s", st.Phase)
	}
	if len(n.dailyReminders) != 1 || n.dailyReminders[0] != 1 {
		t.Fatalf("第 1 分钟应发送第 1 次提醒，实际 %+v", n.dailyReminders)
	}

	// 第 2 分钟 → 最后一次提醒
	_ = s.Tick(base.Add(2 * time.Minute))
	if !n.lastReminderWasFinal {
		t.Fatal("第 2 分钟（reminder_count=2）应为最后一次提醒")
	}

	// 第 3 分钟 → PENDING_TRIGGER
	_ = s.Tick(base.Add(3 * time.Minute))
	st, _ = store.Load()
	if st.Phase != state.PhasePendingTrigger {
		t.Fatalf("提醒期结束后应为 PENDING_TRIGGER，实际 %s", st.Phase)
	}
}

func TestDowntimeDoesNotOverwriteOutstandingCheckin(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))

	_ = s.Tick(base)
	st, _ := store.Load()
	firstToken := st.PendingToken
	if firstToken == "" {
		t.Fatal("D0 应生成 pending token")
	}

	// 模拟错过整段连续提醒甚至跨到下个月：不能覆盖旧 token 重新开始一轮。
	_ = s.Tick(base.Add(40 * 24 * time.Hour))
	st, _ = store.Load()
	if st.PendingToken != firstToken {
		t.Fatal("未确认的旧 token 不应被新月确认覆盖")
	}
	if st.Phase != state.PhasePendingTrigger {
		t.Fatalf("跨月恢复时应按旧确认补推进到 PENDING_TRIGGER，实际 %s", st.Phase)
	}
	if n.checkins != 1 {
		t.Fatalf("不应发送新的月度确认覆盖旧流程，实际 %d", n.checkins)
	}
}

// 宕机重放：长时间不 tick 后，单次 tick 应立即把「漏回合」算清并进入 PENDING_TRIGGER。
// 后续每个阶段的延迟窗口是从「该阶段开始那一刻」
// 起算的真实缓冲——这是死亡开关的正确语义：不能因为宕机就跳过给用户的最后机会窗口。
// 所以推进各阶段时需让时钟越过对应窗口。最后验证投递幂等。
func TestDowntimeReplay(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))

	tNow := base
	// D0：发送本月确认。
	_ = s.Tick(tNow)
	// D8：连续提醒期已过，进入 PENDING_TRIGGER。
	tNow = tNow.Add(8*24*time.Hour + time.Minute)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhasePendingTrigger {
		t.Fatalf("应到 PENDING_TRIGGER，实际 %s", st.Phase)
	}

	// PENDING_TRIGGER → WARNED，先发 Telegram 阶段提醒，再发受益人预提醒邮件。
	tNow = tNow.Add(time.Minute)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhaseWarned {
		t.Fatalf("应到 WARNED，实际 %s", st.Phase)
	}
	if n.stageReminders[state.StageWarn] != 1 {
		t.Fatalf("应发送预提醒阶段 Telegram 提醒，实际 %d", n.stageReminders[state.StageWarn])
	}

	// 越过 password_delay_after_warn(72h)：WARNED → PASSWORD_SENT，并在此刻才打包。
	tNow = tNow.Add(73 * time.Hour)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhasePasswordSent {
		t.Fatalf("越过 password_delay_after_warn 后应到 PASSWORD_SENT，实际 %s", st.Phase)
	}

	// 越过 file_delay_after_password(96h)：PASSWORD_SENT → FILE_SENT
	tNow = tNow.Add(97 * time.Hour)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhaseFileSent {
		t.Fatalf("越过 file_delay_after_password 后应到 FILE_SENT，实际 %s", st.Phase)
	}

	// 每个受益人每阶段应恰好投递一次。
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if n.warnSent[email] != 1 || n.passwordSent[email] != 1 || n.fileSent[email] != 1 {
			t.Fatalf("%s 投递次数不对: warn=%d pwd=%d file=%d",
				email, n.warnSent[email], n.passwordSent[email], n.fileSent[email])
		}
	}

	// 再多跑几拍（模拟重启重放），投递次数不应增加（幂等）。
	for i := 0; i < 3; i++ {
		_ = s.Tick(tNow.Add(time.Duration(i) * time.Hour))
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if n.warnSent[email] != 1 || n.passwordSent[email] != 1 || n.fileSent[email] != 1 {
			t.Fatalf("幂等性破坏，%s 被重复投递: warn=%d pwd=%d file=%d",
				email, n.warnSent[email], n.passwordSent[email], n.fileSent[email])
		}
	}
}

// 阶段内有受益人投递失败时，状态不能推进；下一拍只重试失败项，全部成功后再推进。
func TestStageWaitsForFailedDeliveriesBeforeProgressing(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))

	tNow := base
	_ = s.Tick(tNow)
	tNow = tNow.Add(8*24*time.Hour + time.Minute)
	_ = s.Tick(tNow) // → PENDING_TRIGGER
	tNow = tNow.Add(time.Minute)

	n.failWarn["b@example.com"] = true
	if err := s.Tick(tNow); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	if st.Phase != state.PhasePendingTrigger {
		t.Fatalf("预警投递部分失败时应保持 PENDING_TRIGGER，实际 %s", st.Phase)
	}
	if n.warnSent["a@example.com"] != 1 || n.warnSent["b@example.com"] != 1 {
		t.Fatalf("首次投递次数不对: a=%d b=%d", n.warnSent["a@example.com"], n.warnSent["b@example.com"])
	}

	n.failWarn["b@example.com"] = false
	if err := s.Tick(tNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseWarned {
		t.Fatalf("失败项重试成功后应进入 WARNED，实际 %s", st.Phase)
	}
	if n.warnSent["a@example.com"] != 1 {
		t.Fatalf("已成功投递的受益人不应重复发送，a=%d", n.warnSent["a@example.com"])
	}
	if n.warnSent["b@example.com"] != 2 {
		t.Fatalf("失败受益人应重试一次，b=%d", n.warnSent["b@example.com"])
	}
}

// 触发流程中任意阶段确认应立即取消，回到 ALIVE，后续不再投递。
func TestConfirmDuringTriggerCancels(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))
	tNow := base

	// 推进到 WARNED：先发送确认，再进入 PENDING_TRIGGER，随后发预提醒。
	_ = s.Tick(tNow)
	tNow = tNow.Add(8*24*time.Hour + time.Minute)
	_ = s.Tick(tNow) // → PENDING_TRIGGER
	tNow = tNow.Add(time.Minute)
	_ = s.Tick(tNow) // → WARNED
	st, _ := store.Load()
	if st.Phase != state.PhaseWarned {
		t.Fatalf("应处于 WARNED，实际 %s", st.Phase)
	}

	// 此时 pending_token 是进入触发前最后一次 checkin 设置的。
	if st.PendingToken == "" {
		t.Fatal("期望仍有 pending_token 可供确认")
	}
	accepted, err := s.Confirm(tNow.Add(time.Hour), st.PendingToken)
	if err != nil || !accepted {
		t.Fatalf("WARNED 阶段确认应被接受, accepted=%v err=%v", accepted, err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseAlive {
		t.Fatalf("确认后应回 ALIVE，实际 %s", st.Phase)
	}

	// 确认后继续 tick（now 仍很晚），但因 last_confirmed_at 已更新，不应再推进触发。
	_ = s.Tick(tNow.Add(2 * time.Hour))
	st, _ = store.Load()
	if st.Phase == state.PhaseWarned || st.Phase == state.PhasePasswordSent {
		t.Fatalf("确认取消后不应再进入触发流程，实际 %s", st.Phase)
	}
	if n.passwordSent["a@example.com"] != 0 {
		t.Fatal("确认取消后不应发送密码")
	}
}

func TestConfirmDuringTriggerClearsArchiveForNextTrigger(t *testing.T) {
	s, store, _, _ := newTestScheduler(t)
	setConfirmed(t, store, base.Add(-time.Hour))

	tNow := base
	_ = s.Tick(tNow)
	tNow = tNow.Add(8*24*time.Hour + time.Minute)
	_ = s.Tick(tNow) // → PENDING_TRIGGER
	tNow = tNow.Add(time.Minute)
	_ = s.Tick(tNow) // → WARNED
	tNow = tNow.Add(73 * time.Hour)
	_ = s.Tick(tNow) // → PASSWORD_SENT，已打包

	st, _ := store.Load()
	if st.CurrentArchivePath == "" {
		t.Fatal("密码阶段应已生成归档")
	}
	accepted, err := s.Confirm(tNow.Add(time.Minute), st.PendingToken)
	if err != nil || !accepted {
		t.Fatalf("确认应被接受, accepted=%v err=%v", accepted, err)
	}
	st, _ = store.Load()
	if st.CurrentArchivePath != "" || st.CurrentArchivePassword != "" || st.CurrentArchiveSHA256 != "" || st.LastPackAt != nil {
		t.Fatalf("取消触发后应清理本轮归档状态: %+v", st)
	}
}

func TestEncryptedArchivePasswordStoredButDeliveredPlaintext(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	s.cfg.StateProtection.EncryptPasswordField = true
	s.cfg.StateProtection.MasterPassphrase = "master-passphrase"
	setConfirmed(t, store, base.Add(-time.Hour))

	tNow := base
	_ = s.Tick(tNow)
	tNow = tNow.Add(8*24*time.Hour + time.Minute)
	_ = s.Tick(tNow) // → PENDING_TRIGGER
	tNow = tNow.Add(time.Minute)
	_ = s.Tick(tNow) // → WARNED
	tNow = tNow.Add(73 * time.Hour)
	_ = s.Tick(tNow) // → PASSWORD_SENT

	st, _ := store.Load()
	if st.CurrentArchivePassword == "" || st.CurrentArchivePassword == "secret-password" {
		t.Fatalf("state 中不应明文保存密码: %q", st.CurrentArchivePassword)
	}
	if n.lastPassword != "secret-password" {
		t.Fatalf("密码邮件应收到明文密码，实际 %q", n.lastPassword)
	}
}

func TestEncryptedArchivePasswordReadsLegacyPlaintext(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	s.cfg.StateProtection.EncryptPasswordField = true
	s.cfg.StateProtection.MasterPassphrase = "master-passphrase"
	st, _ := store.Load()
	st.Phase = state.PhaseWarned
	st.WarnedAt = ptr(base)
	st.PendingToken = "tok-fixed"
	st.CurrentArchivePath = "/tmp/archive.zip"
	st.CurrentArchivePassword = "legacy-password"
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	if err := s.Tick(base.Add(73 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n.lastPassword != "legacy-password" {
		t.Fatalf("旧明文密码应兼容投递，实际 %q", n.lastPassword)
	}
}

// 非密码阶段不应提前打包。
func TestNoPackBeforePasswordStage(t *testing.T) {
	s, store, _, p := newTestScheduler(t)
	setConfirmed(t, store, base)

	packsBefore := p.packs
	_ = s.Tick(base)
	_ = s.Tick(base.Add(8*24*time.Hour + time.Minute))
	if p.packs != packsBefore {
		t.Fatal("密码阶段前不应打包")
	}
}

func TestMonthlyCheckinTimeClampsMonthEnd(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 2, 28, 1, 0, 0, 0, time.UTC)
	got := monthlyCheckinTime(now, 31, loc)
	want := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
