package state

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustTime(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 1, 2, 3, 0, time.UTC)
}

func TestOpenCreatesInitialRow(t *testing.T) {
	store := openTestStore(t)
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseAlive {
		t.Fatalf("初始 phase=%s", st.Phase)
	}
	if st.LastConfirmedAt == nil {
		t.Fatal("初始 last_confirmed_at 不应为空")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	store := openTestStore(t)
	confirmed := mustTime(2026, 1, 1)
	checkin := mustTime(2026, 1, 2)
	pack := mustTime(2026, 1, 3)
	heartbeat := mustTime(2026, 1, 4)
	finalWarning := mustTime(2026, 1, 5)
	warned := mustTime(2026, 1, 6)
	passwordSent := mustTime(2026, 1, 7)
	fileSent := mustTime(2026, 1, 8)

	st := &State{
		Phase:                  PhasePasswordSent,
		LastConfirmedAt:        &confirmed,
		LastCheckinSentAt:      &checkin,
		LastPackAt:             &pack,
		LastHeartbeatAt:        &heartbeat,
		MissCount:              8,
		FinalWarningAt:         &finalWarning,
		WarnedAt:               &warned,
		PasswordSentAt:         &passwordSent,
		FileSentAt:             &fileSent,
		PendingToken:           "pending",
		CurrentArchivePath:     "/tmp/archive.zip",
		CurrentArchivePassword: "encrypted-password",
		CurrentArchiveSHA256:   "sha",
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != st.Phase || got.MissCount != st.MissCount || got.PendingToken != st.PendingToken {
		t.Fatalf("基础字段不一致: %+v", got)
	}
	if got.CurrentArchivePath != st.CurrentArchivePath || got.CurrentArchivePassword != st.CurrentArchivePassword || got.CurrentArchiveSHA256 != st.CurrentArchiveSHA256 {
		t.Fatalf("归档字段不一致: %+v", got)
	}
	for name, pair := range map[string][2]*time.Time{
		"confirmed":    {got.LastConfirmedAt, st.LastConfirmedAt},
		"checkin":      {got.LastCheckinSentAt, st.LastCheckinSentAt},
		"pack":         {got.LastPackAt, st.LastPackAt},
		"heartbeat":    {got.LastHeartbeatAt, st.LastHeartbeatAt},
		"finalWarning": {got.FinalWarningAt, st.FinalWarningAt},
		"warned":       {got.WarnedAt, st.WarnedAt},
		"passwordSent": {got.PasswordSentAt, st.PasswordSentAt},
		"fileSent":     {got.FileSentAt, st.FileSentAt},
	} {
		if pair[0] == nil || !pair[0].Equal(*pair[1]) {
			t.Fatalf("%s 时间不一致: got=%v want=%v", name, pair[0], pair[1])
		}
	}
}

func TestDeliveriesIdempotencyAndClear(t *testing.T) {
	store := openTestStore(t)
	now := mustTime(2026, 1, 1)

	done, err := store.AlreadyDelivered("a@example.com", StageWarn)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("未记录前不应已投递")
	}
	if err := store.RecordDelivery("a@example.com", StageWarn, "FAILED", "smtp", now); err != nil {
		t.Fatal(err)
	}
	done, _ = store.AlreadyDelivered("a@example.com", StageWarn)
	if done {
		t.Fatal("FAILED 不应视为已投递")
	}
	if err := store.RecordDelivery("a@example.com", StageWarn, "OK", "", now); err != nil {
		t.Fatal(err)
	}
	done, _ = store.AlreadyDelivered("a@example.com", StageWarn)
	if !done {
		t.Fatal("OK 应视为已投递")
	}
	if err := store.ClearDeliveries(); err != nil {
		t.Fatal(err)
	}
	done, _ = store.AlreadyDelivered("a@example.com", StageWarn)
	if done {
		t.Fatal("清空后不应已投递")
	}
}

func TestDownloadTokenLifecycle(t *testing.T) {
	store := openTestStore(t)
	expires := mustTime(2026, 1, 1)
	if err := store.CreateDownloadToken(DownloadToken{
		Token:        "tok",
		ArchivePath:  "/tmp/archive.zip",
		Beneficiary:  "a@example.com",
		ExpiresAt:    expires,
		MaxDownloads: 2,
	}); err != nil {
		t.Fatal(err)
	}

	tok, err := store.GetDownloadToken("tok")
	if err != nil {
		t.Fatal(err)
	}
	if tok == nil || tok.ArchivePath != "/tmp/archive.zip" || tok.Beneficiary != "a@example.com" || !tok.ExpiresAt.Equal(expires) || tok.MaxDownloads != 2 {
		t.Fatalf("token 不一致: %+v", tok)
	}
	if err := store.IncrementDownloadCount("tok"); err != nil {
		t.Fatal(err)
	}
	tok, _ = store.GetDownloadToken("tok")
	if tok.DownloadCount != 1 {
		t.Fatalf("download_count=%d", tok.DownloadCount)
	}
	if err := store.CleanupExpiredTokens(expires.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	tok, _ = store.GetDownloadToken("tok")
	if tok != nil {
		t.Fatalf("过期 token 应被清理: %+v", tok)
	}
}

func TestLoadReportsTimeParseError(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`UPDATE state SET last_confirmed_at = ? WHERE id = 1`, "not-a-time"); err != nil {
		t.Fatal(err)
	}
	_, err := store.Load()
	if err == nil {
		t.Fatal("期望时间解析失败")
	}
	if !strings.Contains(err.Error(), "解析时间字段失败") {
		t.Fatalf("错误信息不对: %v", err)
	}
}

func TestAuditWritesRecord(t *testing.T) {
	store := openTestStore(t)
	now := mustTime(2026, 1, 1)
	if err := store.Audit("event", "detail", now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE event = ? AND detail = ?`, "event", "detail").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit count=%d", count)
	}
}

func TestNormalizeForTargetFlowResetsUnsafeLegacyTriggerState(t *testing.T) {
	store := openTestStore(t)
	now := mustTime(2026, 6, 1)
	oldWarned := mustTime(2026, 5, 20)
	st := &State{
		Phase:              PhaseWarned,
		LastConfirmedAt:    &oldWarned,
		LastCheckinSentAt:  nil,
		WarnedAt:           &oldWarned,
		MissCount:          9,
		CurrentArchivePath: "/tmp/old.zip",
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDelivery("a@example.com", StageWarn, "OK", "", oldWarned); err != nil {
		t.Fatal(err)
	}

	result, err := store.NormalizeForTargetFlow(now)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.From != PhaseWarned || result.To != PhaseAlive || result.Reason != "missing_last_checkin_sent_at" {
		t.Fatalf("迁移结果不符合预期: %+v", result)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseAlive || got.PendingToken != "" || got.WarnedAt != nil || got.CurrentArchivePath != "" {
		t.Fatalf("旧触发状态未安全重置: %+v", got)
	}
	done, err := store.AlreadyDelivered("a@example.com", StageWarn)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("迁移重置后应清空旧投递记录")
	}
	var auditCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE event = ? AND detail LIKE ?`, "target_flow_state_normalized", "%missing_last_checkin_sent_at%").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("迁移审计数量不对: %d", auditCount)
	}
}

func TestNormalizeForTargetFlowKeepsValidTargetFlowState(t *testing.T) {
	store := openTestStore(t)
	now := mustTime(2026, 6, 1)
	checkin := mustTime(2026, 6, 1)
	warned := mustTime(2026, 6, 9)
	st := &State{
		Phase:             PhaseWarned,
		LastConfirmedAt:   &checkin,
		LastCheckinSentAt: &checkin,
		WarnedAt:          &warned,
		MissCount:         8,
		PendingToken:      "valid-token",
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	result, err := store.NormalizeForTargetFlow(now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("有效目标流程状态不应被迁移: %+v", result)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseWarned || got.PendingToken != "valid-token" || got.WarnedAt == nil {
		t.Fatalf("有效状态被意外修改: %+v", got)
	}
}

func TestNormalizeForTargetFlowResetsUnknownPhase(t *testing.T) {
	store := openTestStore(t)
	now := mustTime(2026, 6, 1)
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	st.Phase = Phase("OLD_UNKNOWN")
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	result, err := store.NormalizeForTargetFlow(now)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Reason != "unknown_phase" {
		t.Fatalf("未知 phase 应被重置: %+v", result)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseAlive {
		t.Fatalf("未知 phase 未重置为 ALIVE: %+v", got)
	}
}
