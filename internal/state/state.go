// Package state 用 SQLite（WAL 模式）持久化死亡开关的全部状态。
//
// 设计要点：
//   - 整个系统的「真相」只在磁盘上。进程是无状态执行器，重启后从这里恢复。
//   - 所有计时基于绝对时间戳（UTC ISO8601 字符串），不依赖内存计数器。
//   - deliveries 表保证幂等：每个受益人每个阶段只投递一次，重启重放不重复。
package state

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，无需 CGO
)

// Phase 是状态机的阶段。
type Phase string

const (
	PhaseAlive          Phase = "ALIVE"           // 正常态
	PhaseGrace          Phase = "GRACE"           // 漏了回合但未达阈值
	PhasePendingTrigger Phase = "PENDING_TRIGGER" // 达阈值，最后强提醒窗口
	PhaseWarned         Phase = "WARNED"          // 已发预警邮件
	PhasePasswordSent   Phase = "PASSWORD_SENT"   // 已发解压密码
	PhaseFileSent       Phase = "FILE_SENT"       // 已发文件
	PhaseCompleted      Phase = "COMPLETED"       // 终态
)

// Stage 是投递阶段，用于 deliveries 幂等表。
type Stage string

const (
	StageWarn             Stage = "WARN"
	StagePassword         Stage = "PASSWORD"
	StageFile             Stage = "FILE"
	StageWarnTelegram     Stage = "WARN_TELEGRAM"
	StagePasswordTelegram Stage = "PASSWORD_TELEGRAM"
	StageFileTelegram     Stage = "FILE_TELEGRAM"
)

// State 是单行状态快照。时间字段用指针以区分「零值」与「未设置(NULL)」。
type State struct {
	Phase                  Phase
	LastConfirmedAt        *time.Time
	LastCheckinSentAt      *time.Time
	LastPackAt             *time.Time
	LastHeartbeatAt        *time.Time
	MissCount              int
	FinalWarningAt         *time.Time
	WarnedAt               *time.Time
	PasswordSentAt         *time.Time
	FileSentAt             *time.Time
	PendingToken           string
	CurrentArchivePath     string
	CurrentArchivePassword string // 明文或加密（取决于 state_protection 配置）
	CurrentArchiveSHA256   string
}

// Store 封装 SQLite 连接。
type Store struct {
	db *sql.DB
}

// MigrationResult 描述一次启动时状态归一化结果。
type MigrationResult struct {
	Changed bool
	From    Phase
	To      Phase
	Reason  string
}

// Open 打开（或创建）数据库，启用 WAL，建表，并确保存在单行状态。
func Open(path string) (*Store, error) {
	// _pragma 通过 DSN 设置：WAL 提升并发与崩溃安全；busy_timeout 避免锁冲突直接失败。
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 单写者：限制连接数避免写锁竞争。
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureInitialRow(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  phase TEXT NOT NULL,
  last_confirmed_at TEXT,
  last_checkin_sent_at TEXT,
  last_pack_at TEXT,
  last_heartbeat_at TEXT,
  miss_count INTEGER NOT NULL DEFAULT 0,
  final_warning_at TEXT,
  warned_at TEXT,
  password_sent_at TEXT,
  file_sent_at TEXT,
  pending_token TEXT NOT NULL DEFAULT '',
  current_archive_path TEXT NOT NULL DEFAULT '',
  current_archive_password TEXT NOT NULL DEFAULT '',
  current_archive_sha256 TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS deliveries (
  beneficiary_email TEXT NOT NULL,
  stage TEXT NOT NULL,
  sent_at TEXT NOT NULL,
  status TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (beneficiary_email, stage)
);
CREATE TABLE IF NOT EXISTS download_tokens (
  token TEXT PRIMARY KEY,
  archive_path TEXT NOT NULL,
  beneficiary_email TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  download_count INTEGER NOT NULL DEFAULT 0,
  max_downloads INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS audit (
  ts TEXT NOT NULL,
  event TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("建表失败: %w", err)
	}
	return nil
}

// ensureInitialRow 确保 state 表有且仅有 id=1 的一行。首次运行时
// last_confirmed_at 设为当前时间——视为「用户此刻是活着的」。
func (s *Store) ensureInitialRow() error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM state WHERE id = 1`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO state (id, phase, last_confirmed_at) VALUES (1, ?, ?)`,
		string(PhaseAlive), now,
	)
	return err
}

// fmtTime / parseTime 在 *time.Time 与可空 TEXT 列之间转换。
func fmtTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
}

func parseTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

// Load 读取单行状态。
func (s *Store) Load() (*State, error) {
	var (
		st                                                  State
		phase                                               string
		lastConfirmed, lastCheckin, lastPack, lastHeartbeat sql.NullString
		finalWarning, warned, passwordSent, fileSent        sql.NullString
	)
	row := s.db.QueryRow(`
SELECT phase, last_confirmed_at, last_checkin_sent_at, last_pack_at, last_heartbeat_at,
       miss_count, final_warning_at, warned_at, password_sent_at, file_sent_at,
       pending_token, current_archive_path, current_archive_password, current_archive_sha256
FROM state WHERE id = 1`)
	err := row.Scan(
		&phase, &lastConfirmed, &lastCheckin, &lastPack, &lastHeartbeat,
		&st.MissCount, &finalWarning, &warned, &passwordSent, &fileSent,
		&st.PendingToken, &st.CurrentArchivePath, &st.CurrentArchivePassword, &st.CurrentArchiveSHA256,
	)
	if err != nil {
		return nil, fmt.Errorf("读取状态失败: %w", err)
	}
	st.Phase = Phase(phase)
	for dst, src := range map[**time.Time]sql.NullString{
		&st.LastConfirmedAt: lastConfirmed, &st.LastCheckinSentAt: lastCheckin,
		&st.LastPackAt: lastPack, &st.LastHeartbeatAt: lastHeartbeat,
		&st.FinalWarningAt: finalWarning, &st.WarnedAt: warned,
		&st.PasswordSentAt: passwordSent, &st.FileSentAt: fileSent,
	} {
		t, err := parseTime(src)
		if err != nil {
			return nil, fmt.Errorf("解析时间字段失败: %w", err)
		}
		*dst = t
	}
	return &st, nil
}

// Save 在单事务里写回整行状态（崩溃安全）。
func (s *Store) Save(st *State) error {
	_, err := s.db.Exec(`
UPDATE state SET
  phase = ?, last_confirmed_at = ?, last_checkin_sent_at = ?, last_pack_at = ?, last_heartbeat_at = ?,
  miss_count = ?, final_warning_at = ?, warned_at = ?, password_sent_at = ?, file_sent_at = ?,
  pending_token = ?, current_archive_path = ?, current_archive_password = ?, current_archive_sha256 = ?
WHERE id = 1`,
		string(st.Phase), fmtTime(st.LastConfirmedAt), fmtTime(st.LastCheckinSentAt),
		fmtTime(st.LastPackAt), fmtTime(st.LastHeartbeatAt), st.MissCount,
		fmtTime(st.FinalWarningAt), fmtTime(st.WarnedAt), fmtTime(st.PasswordSentAt), fmtTime(st.FileSentAt),
		st.PendingToken, st.CurrentArchivePath, st.CurrentArchivePassword, st.CurrentArchiveSHA256,
	)
	if err != nil {
		return fmt.Errorf("保存状态失败: %w", err)
	}
	return nil
}

// NormalizeForTargetFlow 在启动时把明显不符合目标流程前置条件的旧状态归一化。
//
// 保守原则：
//   - 不迁移看起来仍是有效目标流程的状态，避免打断正在进行的真实投递。
//   - 对缺少 pending token / last_checkin_sent_at 的触发阶段重置为 ALIVE，
//     因为用户已经无法通过 Telegram 按钮取消，继续推进可能误通知受益人。
//   - 对未知 phase 重置为 ALIVE，避免调度器静默卡死。
func (s *Store) NormalizeForTargetFlow(now time.Time) (*MigrationResult, error) {
	st, err := s.Load()
	if err != nil {
		return nil, err
	}
	reason := targetFlowNormalizationReason(st)
	if reason == "" {
		return &MigrationResult{Changed: false, From: st.Phase, To: st.Phase}, nil
	}

	from := st.Phase
	resetToAlive(st, now)
	if err := s.Save(st); err != nil {
		return nil, err
	}
	if err := s.ClearDeliveries(); err != nil {
		return nil, err
	}
	detail := fmt.Sprintf("from=%s,to=%s,reason=%s", from, st.Phase, reason)
	if err := s.Audit("target_flow_state_normalized", detail, now); err != nil {
		return nil, err
	}
	return &MigrationResult{Changed: true, From: from, To: st.Phase, Reason: reason}, nil
}

func targetFlowNormalizationReason(st *State) string {
	switch st.Phase {
	case PhaseAlive, PhaseGrace, PhasePendingTrigger, PhaseWarned, PhasePasswordSent, PhaseFileSent, PhaseCompleted:
	default:
		return "unknown_phase"
	}

	if st.Phase == PhaseAlive || st.Phase == PhaseCompleted || st.Phase == PhaseFileSent {
		return ""
	}
	if st.LastCheckinSentAt == nil {
		return "missing_last_checkin_sent_at"
	}
	if st.PendingToken == "" {
		return "missing_pending_token"
	}
	if st.Phase == PhasePasswordSent && (st.CurrentArchivePath == "" || st.CurrentArchivePassword == "") {
		return "missing_archive_after_password_sent"
	}
	return ""
}

func resetToAlive(st *State, now time.Time) {
	st.Phase = PhaseAlive
	st.LastConfirmedAt = ptr(now)
	st.MissCount = 0
	st.PendingToken = ""
	st.FinalWarningAt = nil
	st.WarnedAt = nil
	st.PasswordSentAt = nil
	st.FileSentAt = nil
	st.CurrentArchivePath = ""
	st.CurrentArchivePassword = ""
	st.CurrentArchiveSHA256 = ""
	st.LastPackAt = nil
}

func ptr(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}

// --- deliveries 幂等表 ---

// AlreadyDelivered 返回某受益人某阶段是否已成功投递。
func (s *Store) AlreadyDelivered(email string, stage Stage) (bool, error) {
	var status string
	err := s.db.QueryRow(
		`SELECT status FROM deliveries WHERE beneficiary_email = ? AND stage = ?`,
		email, string(stage),
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "OK", nil
}

// RecordDelivery 记录一次投递结果（OK 或 FAILED）。用 REPLACE 以便失败后可重试覆盖。
func (s *Store) RecordDelivery(email string, stage Stage, status, detail string, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO deliveries (beneficiary_email, stage, sent_at, status, detail) VALUES (?, ?, ?, ?, ?)`,
		email, string(stage), at.UTC().Format(time.RFC3339), status, detail,
	)
	return err
}

// ClearDeliveries 在用户确认（取消触发流程）后清空投递记录，
// 以便下次真触发时重新走完整流程。
func (s *Store) ClearDeliveries() error {
	_, err := s.db.Exec(`DELETE FROM deliveries`)
	return err
}

// --- download_tokens ---

type DownloadToken struct {
	Token         string
	ArchivePath   string
	Beneficiary   string
	ExpiresAt     time.Time
	DownloadCount int
	MaxDownloads  int
}

func (s *Store) CreateDownloadToken(t DownloadToken) error {
	_, err := s.db.Exec(
		`INSERT INTO download_tokens (token, archive_path, beneficiary_email, expires_at, download_count, max_downloads)
		 VALUES (?, ?, ?, ?, 0, ?)`,
		t.Token, t.ArchivePath, t.Beneficiary, t.ExpiresAt.UTC().Format(time.RFC3339), t.MaxDownloads,
	)
	return err
}

// GetDownloadToken 取出 token；调用方负责校验过期与下载次数。
func (s *Store) GetDownloadToken(token string) (*DownloadToken, error) {
	var (
		t       DownloadToken
		expires string
	)
	err := s.db.QueryRow(
		`SELECT token, archive_path, beneficiary_email, expires_at, download_count, max_downloads
		 FROM download_tokens WHERE token = ?`, token,
	).Scan(&t.Token, &t.ArchivePath, &t.Beneficiary, &expires, &t.DownloadCount, &t.MaxDownloads)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return nil, err
	}
	t.ExpiresAt = exp.UTC()
	return &t, nil
}

func (s *Store) IncrementDownloadCount(token string) error {
	_, err := s.db.Exec(`UPDATE download_tokens SET download_count = download_count + 1 WHERE token = ?`, token)
	return err
}

func (s *Store) CleanupExpiredTokens(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM download_tokens WHERE expires_at < ?`, now.UTC().Format(time.RFC3339))
	return err
}

// --- audit ---

// Audit 追加一条审计记录。失败仅返回错误，不影响主流程。
func (s *Store) Audit(event, detail string, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO audit (ts, event, detail) VALUES (?, ?, ?)`,
		at.UTC().Format(time.RFC3339), event, detail,
	)
	return err
}
