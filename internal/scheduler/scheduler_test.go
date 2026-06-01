package scheduler

import (
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
	checkins      int
	finalWarnings int
	heartbeats    int
	messages      int
	ownerAlerts   int
	warnSent      map[string]int
	passwordSent  map[string]int
	fileSent      map[string]int
	lastPassword  string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{
		warnSent:     map[string]int{},
		passwordSent: map[string]int{},
		fileSent:     map[string]int{},
	}
}

func (f *fakeNotifier) SendCheckin(token string) error          { f.checkins++; return nil }
func (f *fakeNotifier) SendFinalWarning() error                 { f.finalWarnings++; return nil }
func (f *fakeNotifier) SendHeartbeat() error                    { f.heartbeats++; return nil }
func (f *fakeNotifier) SendMessageSafe(text string) error       { f.messages++; return nil }
func (f *fakeNotifier) SendOwnerAlert() error                   { f.ownerAlerts++; return nil }
func (f *fakeNotifier) DeliverWarn(b config.Beneficiary) error  { f.warnSent[b.Email]++; return nil }
func (f *fakeNotifier) DeliverPassword(b config.Beneficiary, password string) error {
	f.passwordSent[b.Email]++
	f.lastPassword = password
	return nil
}
func (f *fakeNotifier) DeliverFile(b config.Beneficiary, archivePath, password string) error {
	f.fileSent[b.Email]++
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
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.Config{
		SourceDir: "/tmp",
		Intervals: config.Intervals{
			PackInterval:    config.Duration(24 * time.Hour),
			CheckinInterval: config.Duration(24 * time.Hour),
			MissThreshold:   5,
			FinalGrace:      config.Duration(48 * time.Hour),
			PasswordDelay:   config.Duration(72 * time.Hour),
			FileDelay:       config.Duration(96 * time.Hour),
		},
		Archive: config.Archive{KeepArchives: 3, PasswordLength: 32, LargeFileThreshold: config.Bytes(20 << 20)},
		Beneficiaries: []config.Beneficiary{
			{Name: "A", Email: "a@example.com", Lang: "zh"},
			{Name: "B", Email: "b@example.com", Lang: "en"},
		},
		Reliability: config.Reliability{HeartbeatEnabled: true, HeartbeatInterval: config.Duration(168 * time.Hour)},
	}
	n := newFakeNotifier()
	p := &fakePacker{}
	tok := func() (string, error) { return "tok-fixed", nil }
	s := New(cfg, store, n, p, tok, nil)
	return s, store, n, p
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

	// 推进到该发确认消息，tick 设置 pending_token。
	if err := s.Tick(base.Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	if st.PendingToken == "" {
		t.Fatal("期望 tick 设置 pending_token")
	}

	accepted, err := s.Confirm(base.Add(26*time.Hour), st.PendingToken)
	if err != nil || !accepted {
		t.Fatalf("确认应被接受, accepted=%v err=%v", accepted, err)
	}
	st, _ = store.Load()
	if st.Phase != state.PhaseAlive || st.MissCount != 0 || st.PendingToken != "" {
		t.Fatalf("确认后状态不对: %+v", st)
	}
}

// 错误 token 不应被接受（防重放）。
func TestConfirmWrongTokenRejected(t *testing.T) {
	s, store, _, _ := newTestScheduler(t)
	setConfirmed(t, store, base)
	_ = s.Tick(base.Add(25 * time.Hour))

	accepted, err := s.Confirm(base.Add(26*time.Hour), "wrong-token")
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("错误 token 不应被接受")
	}
}

// 连续漏回合应进入 GRACE，再达阈值进入 PENDING_TRIGGER。
func TestMissedRoundsProgression(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base)

	// 漏 1 回合 → GRACE
	_ = s.Tick(base.Add(25 * time.Hour))
	st, _ := store.Load()
	if st.Phase != state.PhaseGrace {
		t.Fatalf("漏 1 回合应为 GRACE，实际 %s", st.Phase)
	}

	// 漏 5 回合 → PENDING_TRIGGER + 最后强提醒
	_ = s.Tick(base.Add(5*24*time.Hour + time.Hour))
	st, _ = store.Load()
	if st.Phase != state.PhasePendingTrigger {
		t.Fatalf("漏 5 回合应为 PENDING_TRIGGER，实际 %s", st.Phase)
	}
	if n.finalWarnings == 0 {
		t.Fatal("应发送最后强提醒")
	}
	if n.ownerAlerts == 0 {
		t.Fatal("应给用户本人发兜底邮件")
	}
}

// 宕机重放：长时间不 tick 后，单次 tick 应立即把「漏回合」算清并进入 PENDING_TRIGGER。
// 后续每个阶段的延迟窗口（final_grace/password_delay/file_delay）是从「该阶段开始那一刻」
// 起算的真实缓冲——这是死亡开关的正确语义：不能因为宕机就跳过给用户的最后机会窗口。
// 所以推进各阶段时需让时钟越过对应窗口。最后验证投递幂等。
func TestDowntimeReplay(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base)

	// 模拟 VPS 宕机很久：直接跳到 base + 30 天后才跑第一拍。
	// 漏回合远超阈值 5 → 单拍即进入 PENDING_TRIGGER，并记 final_warning_at=now。
	tNow := base.Add(30 * 24 * time.Hour)

	// 第1拍：ALIVE → PENDING_TRIGGER
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhasePendingTrigger {
		t.Fatalf("第1拍应到 PENDING_TRIGGER，实际 %s", st.Phase)
	}

	// 越过 final_grace(48h)：PENDING_TRIGGER → WARNED
	tNow = tNow.Add(49 * time.Hour)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhaseWarned {
		t.Fatalf("越过 final_grace 后应到 WARNED，实际 %s", st.Phase)
	}

	// 越过 password_delay(72h)：WARNED → PASSWORD_SENT
	tNow = tNow.Add(73 * time.Hour)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhasePasswordSent {
		t.Fatalf("越过 password_delay 后应到 PASSWORD_SENT，实际 %s", st.Phase)
	}

	// 越过 file_delay(96h)：PASSWORD_SENT → FILE_SENT
	tNow = tNow.Add(97 * time.Hour)
	_ = s.Tick(tNow)
	if st, _ := store.Load(); st.Phase != state.PhaseFileSent {
		t.Fatalf("越过 file_delay 后应到 FILE_SENT，实际 %s", st.Phase)
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

// 触发流程中任意阶段确认应立即取消，回到 ALIVE，后续不再投递。
func TestConfirmDuringTriggerCancels(t *testing.T) {
	s, store, n, _ := newTestScheduler(t)
	setConfirmed(t, store, base)
	tNow := base.Add(30 * 24 * time.Hour)

	// 推进到 WARNED：先进 PENDING_TRIGGER，再越过 final_grace。
	_ = s.Tick(tNow)                    // → PENDING_TRIGGER（记 final_warning_at）
	tNow = tNow.Add(49 * time.Hour)     // 越过 final_grace(48h)
	_ = s.Tick(tNow)                    // → WARNED
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

// 终态机器人不应再打包。
func TestNoPackInTerminal(t *testing.T) {
	s, store, _, p := newTestScheduler(t)
	st, _ := store.Load()
	st.Phase = state.PhaseCompleted
	_ = store.Save(st)

	packsBefore := p.packs
	_ = s.Tick(base.Add(48 * time.Hour))
	if p.packs != packsBefore {
		t.Fatal("终态不应打包")
	}
}

// missedRounds 纯函数边界。
func TestMissedRounds(t *testing.T) {
	last := base
	ci := 24 * time.Hour
	cases := []struct {
		elapsed time.Duration
		want    int
	}{
		{0, 0},
		{23 * time.Hour, 0},
		{24 * time.Hour, 1},
		{25 * time.Hour, 1},
		{48 * time.Hour, 2},
		{5 * 24 * time.Hour, 5},
		{30 * 24 * time.Hour, 30},
	}
	for _, c := range cases {
		got := missedRounds(&last, ci, last.Add(c.elapsed))
		if got != c.want {
			t.Errorf("elapsed=%v: got %d want %d", c.elapsed, got, c.want)
		}
	}
	if missedRounds(nil, ci, base) != 0 {
		t.Error("nil lastConfirmed 应返回 0")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
