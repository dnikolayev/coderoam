package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dnikolayev/coderoam/internal/types"
)

type Store struct {
	db *sql.DB
}

type MessageRecord struct {
	ID                int64
	ProfileID         string
	ChatID            string
	SenderID          string
	ExternalMessageID string
	Direction         string
	Text              string
	TextHash          string
	Status            string
	ReceivedAt        time.Time
	ProcessedAt       *time.Time
}

type InvocationRecord struct {
	ProfileID    string
	ChatID       string
	RunnerID     string
	MessageID    int64
	RequestJSON  []byte
	ResponseJSON []byte
	ExitCode     int
	Duration     time.Duration
	Status       string
}

type ActiveInboxRecord struct {
	ID                 int64
	ProfileID          string
	ChatID             string
	ChatAlias          string
	SessionID          string
	SenderID           string
	SenderName         string
	ExternalMessageID  string
	Text               string
	RawText            string
	RawJSON            string
	Media              []types.MediaAttachment
	Status             string
	ReceivedAt         time.Time
	ClaimedAt          *time.Time
	ClaimedBySessionID string
	DoneAt             *time.Time
}

type ActiveOutboxRecord struct {
	ID        int64
	ProfileID string
	ChatID    string
	Text      string
	Important bool
	Status    string
	Attempts  int
	LastError string
	CreatedAt time.Time
	SentAt    *time.Time
}

type ActiveReadReceiptRecord struct {
	ID                int64
	ProfileID         string
	ChatID            string
	SenderID          string
	ExternalMessageID string
	Status            string
	Attempts          int
	LastError         string
	CreatedAt         time.Time
	SentAt            *time.Time
}

type ActiveWatcherRecord struct {
	ProfileID   string
	SessionID   string
	ConsumerID  string
	PID         int
	Status      string
	StartedAt   time.Time
	HeartbeatAt time.Time
}

type PendingInteractionRecord struct {
	ID              int64
	ProfileID       string
	ChatID          string
	SenderID        string
	RunnerID        string
	SourceMessageID int64
	Prompt          string
	Options         []string
	Status          string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	AnsweredAt      *time.Time
	SelectedIndex   int
	SelectedText    string
}

type AuditEventRecord struct {
	ID          int64
	ProfileID   string
	EventType   string
	Actor       string
	Target      string
	DetailsJSON string
	CreatedAt   time.Time
}

const activeInboxColumns = `id, profile_id, chat_id, chat_alias, session_id, sender_id, sender_name, external_message_id, text, raw_text, raw_json, status, received_at, claimed_at, claimed_by_session_id, done_at`
const activeWatcherColumns = `profile_id, session_id, consumer_id, pid, status, started_at, heartbeat_at`
const pendingInteractionColumns = `id, profile_id, chat_id, sender_id, runner_id, source_message_id, prompt, options_json, status, created_at, expires_at, answered_at, selected_index, selected_text`
const auditEventColumns = `id, profile_id, event_type, actor, target, details_json, created_at`

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.ExecContext(context.Background(), `PRAGMA busy_timeout = 5000`); err != nil {
		conn.Close()
		return nil, err
	}
	store := &Store{db: conn}
	if err := store.Migrate(context.Background()); err != nil {
		conn.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS profiles (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chats (
			id TEXT NOT NULL,
			profile_id TEXT NOT NULL,
			type TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			alias TEXT NOT NULL DEFAULT '',
			is_allowed INTEGER NOT NULL DEFAULT 0,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			PRIMARY KEY (profile_id, id)
		)`,
		`CREATE TABLE IF NOT EXISTS senders (
			id TEXT NOT NULL,
			profile_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (profile_id, id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			external_message_id TEXT NOT NULL,
			direction TEXT NOT NULL,
			text TEXT NOT NULL DEFAULT '',
			text_hash TEXT NOT NULL,
			raw_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			received_at TEXT NOT NULL,
			processed_at TEXT,
			UNIQUE(profile_id, external_message_id, direction)
		)`,
		`CREATE TABLE IF NOT EXISTS runner_invocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			runner_id TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			request_json TEXT NOT NULL DEFAULT '',
			response_json TEXT NOT NULL DEFAULT '',
			exit_code INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			text TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			sent_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			details_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS active_inbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			chat_alias TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			sender_id TEXT NOT NULL,
			sender_name TEXT NOT NULL DEFAULT '',
			external_message_id TEXT NOT NULL,
			text TEXT NOT NULL DEFAULT '',
			raw_text TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			received_at TEXT NOT NULL,
			claimed_at TEXT,
			claimed_by_session_id TEXT NOT NULL DEFAULT '',
			done_at TEXT,
			UNIQUE(profile_id, chat_id, external_message_id)
		)`,
		`CREATE TABLE IF NOT EXISTS active_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			text TEXT NOT NULL,
			important INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			sent_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS active_read_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			external_message_id TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			sent_at TEXT,
			UNIQUE(profile_id, chat_id, external_message_id)
		)`,
		`CREATE TABLE IF NOT EXISTS active_watchers (
			profile_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			consumer_id TEXT NOT NULL DEFAULT '',
			pid INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			heartbeat_at TEXT NOT NULL,
			PRIMARY KEY(profile_id, session_id)
		)`,
		`CREATE TABLE IF NOT EXISTS pending_interactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			runner_id TEXT NOT NULL,
			source_message_id INTEGER NOT NULL DEFAULT 0,
			prompt TEXT NOT NULL DEFAULT '',
			options_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			answered_at TEXT,
			selected_index INTEGER NOT NULL DEFAULT 0,
			selected_text TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "active_inbox", "session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "active_inbox", "claimed_by_session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	if table != "active_inbox" {
		return fmt.Errorf("unsupported migration table %q", table)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}

func (s *Store) EnsureProfile(ctx context.Context, id string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO profiles (id, display_name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at`, id, id, now, now)
	return err
}

func (s *Store) UpsertChat(ctx context.Context, profileID string, chat types.Chat) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats
		(id, profile_id, type, display_name, alias, is_allowed, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, id) DO UPDATE SET
			type = excluded.type,
			display_name = excluded.display_name,
			alias = CASE WHEN excluded.alias != '' THEN excluded.alias ELSE chats.alias END,
			is_allowed = CASE WHEN excluded.is_allowed = 1 THEN 1 ELSE chats.is_allowed END,
			last_seen_at = excluded.last_seen_at`,
		chat.ID, profileID, string(chat.Type), chat.DisplayName, chat.Alias, boolInt(chat.Allowed), now, now)
	return err
}

func (s *Store) UpsertSender(ctx context.Context, profileID, senderID, displayName string, isAdmin bool) error {
	if senderID == "" {
		return nil
	}
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO senders
		(id, profile_id, display_name, first_seen_at, last_seen_at, is_admin)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, id) DO UPDATE SET
			display_name = CASE WHEN excluded.display_name != '' THEN excluded.display_name ELSE senders.display_name END,
			last_seen_at = excluded.last_seen_at,
			is_admin = CASE WHEN excluded.is_admin = 1 THEN 1 ELSE senders.is_admin END`,
		senderID, profileID, displayName, now, now, boolInt(isAdmin))
	return err
}

func (s *Store) RecordIncomingMessage(ctx context.Context, profileID string, msg types.IncomingMessage, storeText bool) (MessageRecord, bool, error) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	text := msg.Text
	rawMsg := msg
	if !storeText {
		text = ""
		rawMsg.Text = ""
		rawMsg.RawText = ""
	}
	raw, _ := json.Marshal(rawMsg)
	hash := TextHash(msg.Text)
	result, err := s.db.ExecContext(ctx, `INSERT INTO messages
		(profile_id, chat_id, sender_id, external_message_id, direction, text, text_hash, raw_json, status, received_at)
		VALUES (?, ?, ?, ?, 'incoming', ?, ?, ?, 'received', ?)`,
		profileID, msg.ChatID, msg.SenderID, msg.ID, text, hash, string(raw), formatTime(msg.Timestamp))
	if err != nil {
		if isUniqueErr(err) {
			record, getErr := s.GetMessageByExternalID(ctx, profileID, msg.ID, "incoming")
			return record, false, getErr
		}
		return MessageRecord{}, false, err
	}
	id, _ := result.LastInsertId()
	return MessageRecord{
		ID:                id,
		ProfileID:         profileID,
		ChatID:            msg.ChatID,
		SenderID:          msg.SenderID,
		ExternalMessageID: msg.ID,
		Direction:         "incoming",
		Text:              text,
		TextHash:          hash,
		Status:            "received",
		ReceivedAt:        msg.Timestamp,
	}, true, nil
}

func (s *Store) GetMessageByExternalID(ctx context.Context, profileID, externalID, direction string) (MessageRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, profile_id, chat_id, sender_id, external_message_id, direction, text, text_hash, status, received_at, processed_at
		FROM messages WHERE profile_id = ? AND external_message_id = ? AND direction = ?`, profileID, externalID, direction)
	return scanMessage(row)
}

func (s *Store) PendingIncomingMessages(ctx context.Context, profileID string, limit int) ([]types.IncomingMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT external_message_id, chat_id, sender_id, text, raw_json, received_at
		FROM messages
		WHERE profile_id = ? AND direction = 'incoming' AND status = 'received'
		ORDER BY id
		LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []types.IncomingMessage{}
	for rows.Next() {
		var externalID, chatID, senderID, text, rawJSON, receivedAt string
		if err := rows.Scan(&externalID, &chatID, &senderID, &text, &rawJSON, &receivedAt); err != nil {
			return nil, err
		}
		var msg types.IncomingMessage
		if rawJSON != "" {
			_ = json.Unmarshal([]byte(rawJSON), &msg)
		}
		if msg.ID == "" {
			msg.ID = externalID
		}
		if msg.ChatID == "" {
			msg.ChatID = chatID
		}
		if msg.SenderID == "" {
			msg.SenderID = senderID
		}
		if msg.Text == "" {
			msg.Text = text
		}
		if msg.RawText == "" {
			msg.RawText = msg.Text
		}
		if msg.Timestamp.IsZero() {
			if parsed, err := time.Parse(time.RFC3339Nano, receivedAt); err == nil {
				msg.Timestamp = parsed
			}
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *Store) MarkMessageProcessing(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'processing' WHERE id = ? AND status = 'received'`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) MarkMessageProcessed(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET status = ?, processed_at = ? WHERE id = ?`, status, formatTime(time.Now()), id)
	return err
}

func (s *Store) StoreActiveInboxMessage(ctx context.Context, profileID, chatAlias, sessionID string, msg types.IncomingMessage) (ActiveInboxRecord, bool, error) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(chatAlias)
	}
	if sessionID == "" {
		sessionID = msg.ChatID
	}
	raw, _ := json.Marshal(msg)
	text := msg.Text
	rawText := msg.RawText
	if rawText == "" {
		rawText = text
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO active_inbox
		(profile_id, chat_id, chat_alias, session_id, sender_id, sender_name, external_message_id, text, raw_text, raw_json, status, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread', ?)`,
		profileID, msg.ChatID, chatAlias, sessionID, msg.SenderID, msg.SenderName, msg.ID, text, rawText, string(raw), formatTime(msg.Timestamp))
	if err != nil {
		if isUniqueErr(err) {
			record, getErr := s.GetActiveInboxByExternalID(ctx, profileID, msg.ChatID, msg.ID)
			return record, false, getErr
		}
		return ActiveInboxRecord{}, false, err
	}
	id, _ := result.LastInsertId()
	return ActiveInboxRecord{
		ID:                id,
		ProfileID:         profileID,
		ChatID:            msg.ChatID,
		ChatAlias:         chatAlias,
		SessionID:         sessionID,
		SenderID:          msg.SenderID,
		SenderName:        msg.SenderName,
		ExternalMessageID: msg.ID,
		Text:              text,
		RawText:           rawText,
		RawJSON:           string(raw),
		Media:             msg.Media,
		Status:            "unread",
		ReceivedAt:        msg.Timestamp,
	}, true, nil
}

func (s *Store) GetActiveInboxByExternalID(ctx context.Context, profileID, chatID, externalID string) (ActiveInboxRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+activeInboxColumns+`
		FROM active_inbox WHERE profile_id = ? AND chat_id = ? AND external_message_id = ?`, profileID, chatID, externalID)
	return scanActiveInbox(row)
}

func (s *Store) ListActiveInbox(ctx context.Context, profileID, status string, limit int) ([]ActiveInboxRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT ` + activeInboxColumns + `
		FROM active_inbox WHERE profile_id = ?`
	args := []any{profileID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ActiveInboxRecord{}
	for rows.Next() {
		record, err := scanActiveInbox(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) ClaimNextActiveInbox(ctx context.Context, profileID string) (ActiveInboxRecord, bool, error) {
	return s.ClaimNextActiveInboxForSession(ctx, profileID, "")
}

func (s *Store) ClaimNextActiveInboxForSession(ctx context.Context, profileID, sessionID string) (ActiveInboxRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActiveInboxRecord{}, false, err
	}
	defer tx.Rollback()

	sessionID = strings.TrimSpace(sessionID)
	query := `SELECT ` + activeInboxColumns + `
		FROM active_inbox WHERE profile_id = ? AND status = 'unread'`
	args := []any{profileID}
	if sessionID != "" {
		query += ` AND (session_id = ? OR session_id = '')`
		args = append(args, sessionID)
	}
	query += ` ORDER BY id LIMIT 1`
	row := tx.QueryRowContext(ctx, query, args...)
	record, err := scanActiveInbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveInboxRecord{}, false, nil
	}
	if err != nil {
		return ActiveInboxRecord{}, false, err
	}
	now := formatTime(time.Now())
	result, err := tx.ExecContext(ctx, `UPDATE active_inbox SET status = 'claimed', claimed_at = ?, claimed_by_session_id = ? WHERE id = ? AND status = 'unread'`, now, sessionID, record.ID)
	if err != nil {
		return ActiveInboxRecord{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ActiveInboxRecord{}, false, err
	}
	if affected == 0 {
		return ActiveInboxRecord{}, false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO active_read_receipts
		(profile_id, chat_id, sender_id, external_message_id, status, attempts, created_at)
		VALUES (?, ?, ?, ?, 'pending', 0, ?)`,
		record.ProfileID, record.ChatID, record.SenderID, record.ExternalMessageID, now)
	if err != nil {
		return ActiveInboxRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ActiveInboxRecord{}, false, err
	}
	claimedAt, _ := time.Parse(time.RFC3339Nano, now)
	record.Status = "claimed"
	record.ClaimedAt = &claimedAt
	record.ClaimedBySessionID = sessionID
	return record, true, nil
}

func (s *Store) ClaimActiveInboxBatchForSession(ctx context.Context, profileID, chatID, sessionID string, limit int) ([]ActiveInboxRecord, error) {
	if limit <= 0 {
		limit = 8
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	sessionID = strings.TrimSpace(sessionID)
	query := `SELECT ` + activeInboxColumns + `
		FROM active_inbox WHERE profile_id = ? AND chat_id = ? AND status = 'unread'`
	args := []any{profileID, chatID}
	if sessionID != "" {
		query += ` AND (session_id = ? OR session_id = '')`
		args = append(args, sessionID)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	records := []ActiveInboxRecord{}
	for rows.Next() {
		record, err := scanActiveInbox(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return records, nil
	}

	now := formatTime(time.Now())
	claimedAt, _ := time.Parse(time.RFC3339Nano, now)
	claimed := []ActiveInboxRecord{}
	for _, record := range records {
		result, err := tx.ExecContext(ctx, `UPDATE active_inbox SET status = 'claimed', claimed_at = ?, claimed_by_session_id = ? WHERE id = ? AND status = 'unread'`, now, sessionID, record.ID)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			continue
		}
		record.Status = "claimed"
		record.ClaimedAt = &claimedAt
		record.ClaimedBySessionID = sessionID
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO active_read_receipts
			(profile_id, chat_id, sender_id, external_message_id, status, attempts, created_at)
			VALUES (?, ?, ?, ?, 'pending', 0, ?)`,
			record.ProfileID, record.ChatID, record.SenderID, record.ExternalMessageID, now); err != nil {
			return nil, err
		}
		claimed = append(claimed, record)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) MarkActiveInboxDone(ctx context.Context, profileID string, id int64, status string) error {
	if status == "" {
		status = "done"
	}
	if status != "done" && status != "ignored" {
		return fmt.Errorf("unsupported active inbox status %q", status)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE active_inbox SET status = ?, done_at = ? WHERE profile_id = ? AND id = ?`, status, formatTime(time.Now()), profileID, id)
	return err
}

func (s *Store) RequeueActiveInbox(ctx context.Context, profileID string, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE active_inbox
		SET status = 'unread', claimed_at = NULL, claimed_by_session_id = '', done_at = NULL
		WHERE profile_id = ? AND id = ? AND status = 'claimed'`, profileID, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) RecoverAbandonedActiveInbox(ctx context.Context, profileID, sessionID string, staleAfter time.Duration) (int, error) {
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	cutoff := formatTime(time.Now().Add(-staleAfter))
	sessionID = strings.TrimSpace(sessionID)
	query := `UPDATE active_inbox
		SET status = 'unread', claimed_at = NULL, claimed_by_session_id = '', done_at = NULL
		WHERE profile_id = ?
			AND status = 'claimed'
			AND (claimed_at IS NULL OR claimed_at <= ?)
			AND (
				claimed_by_session_id = ''
				OR NOT EXISTS (
					SELECT 1 FROM active_watchers
					WHERE active_watchers.profile_id = active_inbox.profile_id
						AND active_watchers.session_id = active_inbox.claimed_by_session_id
						AND active_watchers.status = 'active'
						AND active_watchers.heartbeat_at > ?
				)
			)`
	args := []any{profileID, cutoff, cutoff}
	if sessionID != "" {
		query += ` AND (session_id = ? OR claimed_by_session_id = ?)`
		args = append(args, sessionID, sessionID)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *Store) CreatePendingInteraction(ctx context.Context, record PendingInteractionRecord) (int64, error) {
	now := time.Now()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = now.Add(15 * time.Minute)
	}
	optionsJSON, err := json.Marshal(record.Options)
	if err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO pending_interactions
		(profile_id, chat_id, sender_id, runner_id, source_message_id, prompt, options_json, status, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		record.ProfileID, record.ChatID, record.SenderID, record.RunnerID, record.SourceMessageID,
		record.Prompt, string(optionsJSON), formatTime(record.CreatedAt), formatTime(record.ExpiresAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) FindPendingInteraction(ctx context.Context, profileID, chatID, senderID string) (PendingInteractionRecord, bool, error) {
	now := formatTime(time.Now())
	row := s.db.QueryRowContext(ctx, `SELECT `+pendingInteractionColumns+`
		FROM pending_interactions
		WHERE profile_id = ? AND chat_id = ? AND sender_id = ? AND status = 'pending' AND expires_at > ?
		ORDER BY id LIMIT 1`, profileID, chatID, senderID, now)
	record, err := scanPendingInteraction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingInteractionRecord{}, false, nil
	}
	if err != nil {
		return PendingInteractionRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) MarkPendingInteractionAnswered(ctx context.Context, profileID string, id int64, selectedIndex int, selectedText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pending_interactions
		SET status = 'answered', answered_at = ?, selected_index = ?, selected_text = ?
		WHERE profile_id = ? AND id = ? AND status = 'pending'`,
		formatTime(time.Now()), selectedIndex, selectedText, profileID, id)
	return err
}

func (s *Store) ExpirePendingInteractions(ctx context.Context, profileID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pending_interactions
		SET status = 'expired'
		WHERE profile_id = ? AND status = 'pending' AND expires_at <= ?`, profileID, formatTime(time.Now()))
	return err
}

func (s *Store) ActiveInboxCounts(ctx context.Context, profileID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM active_inbox WHERE profile_id = ? GROUP BY status`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (s *Store) AcquireActiveWatcher(ctx context.Context, profileID, sessionID, consumerID string, pid int, staleAfter time.Duration, takeover bool) (ActiveWatcherRecord, bool, error) {
	if strings.TrimSpace(profileID) == "" {
		return ActiveWatcherRecord{}, false, fmt.Errorf("profile id is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return ActiveWatcherRecord{}, false, fmt.Errorf("session id is required")
	}
	if strings.TrimSpace(consumerID) == "" {
		return ActiveWatcherRecord{}, false, fmt.Errorf("consumer id is required")
	}
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActiveWatcherRecord{}, false, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `SELECT `+activeWatcherColumns+` FROM active_watchers WHERE profile_id = ? AND session_id = ?`, profileID, sessionID)
	existing, err := scanActiveWatcher(row)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ActiveWatcherRecord{}, false, err
	}
	now := time.Now()
	if err == nil {
		fresh := existing.Status == "active" && existing.HeartbeatAt.After(now.Add(-staleAfter))
		if fresh && !takeover {
			return existing, false, nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE active_watchers
			SET consumer_id = ?, pid = ?, status = 'active', started_at = ?, heartbeat_at = ?
			WHERE profile_id = ? AND session_id = ?`,
			consumerID, pid, formatTime(now), formatTime(now), profileID, sessionID)
		if err != nil {
			return ActiveWatcherRecord{}, false, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO active_watchers
			(profile_id, session_id, consumer_id, pid, status, started_at, heartbeat_at)
			VALUES (?, ?, ?, ?, 'active', ?, ?)`,
			profileID, sessionID, consumerID, pid, formatTime(now), formatTime(now))
		if err != nil {
			if isUniqueErr(err) {
				record, getErr := scanActiveWatcher(tx.QueryRowContext(ctx, `SELECT `+activeWatcherColumns+` FROM active_watchers WHERE profile_id = ? AND session_id = ?`, profileID, sessionID))
				return record, false, getErr
			}
			return ActiveWatcherRecord{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ActiveWatcherRecord{}, false, err
	}
	return ActiveWatcherRecord{
		ProfileID:   profileID,
		SessionID:   sessionID,
		ConsumerID:  consumerID,
		PID:         pid,
		Status:      "active",
		StartedAt:   now,
		HeartbeatAt: now,
	}, true, nil
}

func (s *Store) GetActiveWatcher(ctx context.Context, profileID, sessionID string) (ActiveWatcherRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+activeWatcherColumns+` FROM active_watchers WHERE profile_id = ? AND session_id = ?`, profileID, sessionID)
	return scanActiveWatcher(row)
}

func (s *Store) ActiveWatcherFresh(ctx context.Context, profileID, sessionID string, staleAfter time.Duration) (ActiveWatcherRecord, bool, error) {
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	record, err := s.GetActiveWatcher(ctx, profileID, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveWatcherRecord{}, false, nil
	}
	if err != nil {
		return ActiveWatcherRecord{}, false, err
	}
	if record.Status != "active" {
		return record, false, nil
	}
	if record.HeartbeatAt.After(time.Now().Add(-staleAfter)) {
		return record, true, nil
	}
	return record, false, nil
}

func (s *Store) ExpireActiveWatchers(ctx context.Context, profileID string, staleAfter time.Duration) (int64, error) {
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	result, err := s.db.ExecContext(ctx, `UPDATE active_watchers
		SET status = 'stale'
		WHERE profile_id = ? AND status = 'active' AND heartbeat_at <= ?`,
		profileID, formatTime(time.Now().Add(-staleAfter)))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) ListActiveWatchers(ctx context.Context, profileID string) ([]ActiveWatcherRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+activeWatcherColumns+`
		FROM active_watchers WHERE profile_id = ? ORDER BY session_id`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ActiveWatcherRecord{}
	for rows.Next() {
		record, err := scanActiveWatcher(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) HeartbeatActiveWatcher(ctx context.Context, profileID, sessionID, consumerID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE active_watchers
		SET heartbeat_at = ?, status = 'active'
		WHERE profile_id = ? AND session_id = ? AND consumer_id = ? AND status = 'active'`,
		formatTime(time.Now()), profileID, sessionID, consumerID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) ReleaseActiveWatcher(ctx context.Context, profileID, sessionID, consumerID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_watchers
		SET status = 'stopped', heartbeat_at = ?
		WHERE profile_id = ? AND session_id = ? AND consumer_id = ?`,
		formatTime(time.Now()), profileID, sessionID, consumerID)
	return err
}

func (s *Store) QueueActiveOutbox(ctx context.Context, profileID, chatID, text string, important bool) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO active_outbox
		(profile_id, chat_id, text, important, status, attempts, created_at)
		VALUES (?, ?, ?, ?, 'pending', 0, ?)`, profileID, chatID, text, boolInt(important), formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) PendingActiveOutbox(ctx context.Context, profileID string, limit int) ([]ActiveOutboxRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, profile_id, chat_id, text, important, status, attempts, last_error, created_at, sent_at
		FROM active_outbox
		WHERE profile_id = ? AND status IN ('pending', 'failed_retryable')
		ORDER BY id
		LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ActiveOutboxRecord{}
	for rows.Next() {
		record, err := scanActiveOutbox(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) MarkActiveOutboxSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_outbox SET status = 'sent', attempts = attempts + 1, sent_at = ?, last_error = '' WHERE id = ?`, formatTime(time.Now()), id)
	return err
}

func (s *Store) MarkActiveOutboxFailed(ctx context.Context, id int64, sendErr error) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_outbox SET status = 'failed_retryable', attempts = attempts + 1, last_error = ? WHERE id = ?`, sendErr.Error(), id)
	return err
}

func (s *Store) ActiveOutboxPendingCount(ctx context.Context, profileID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM active_outbox WHERE profile_id = ? AND status IN ('pending', 'failed_retryable')`, profileID).Scan(&count)
	return count, err
}

func (s *Store) PendingActiveReadReceipts(ctx context.Context, profileID string, limit int) ([]ActiveReadReceiptRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, profile_id, chat_id, sender_id, external_message_id, status, attempts, last_error, created_at, sent_at
		FROM active_read_receipts
		WHERE profile_id = ? AND status IN ('pending', 'failed_retryable')
		ORDER BY id
		LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ActiveReadReceiptRecord{}
	for rows.Next() {
		record, err := scanActiveReadReceipt(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) MarkActiveReadReceiptSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_read_receipts SET status = 'sent', attempts = attempts + 1, sent_at = ?, last_error = '' WHERE id = ?`, formatTime(time.Now()), id)
	return err
}

func (s *Store) MarkActiveReadReceiptFailed(ctx context.Context, id int64, receiptErr error) error {
	_, err := s.db.ExecContext(ctx, `UPDATE active_read_receipts SET status = 'failed_retryable', attempts = attempts + 1, last_error = ? WHERE id = ?`, receiptErr.Error(), id)
	return err
}

func (s *Store) ActiveReadReceiptPendingCount(ctx context.Context, profileID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM active_read_receipts WHERE profile_id = ? AND status IN ('pending', 'failed_retryable')`, profileID).Scan(&count)
	return count, err
}

func (s *Store) MigrateMessagesToActiveInbox(ctx context.Context, profileID, chatID, chatAlias, sessionID string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, external_message_id, sender_id, text, raw_json, received_at
		FROM messages
		WHERE profile_id = ? AND chat_id = ? AND direction = 'incoming' AND status IN ('received', 'processing')
		ORDER BY id`, profileID, chatID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type legacyMessage struct {
		id         int64
		externalID string
		senderID   string
		text       string
		rawJSON    string
		receivedAt string
	}
	legacy := []legacyMessage{}
	for rows.Next() {
		var item legacyMessage
		if err := rows.Scan(&item.id, &item.externalID, &item.senderID, &item.text, &item.rawJSON, &item.receivedAt); err != nil {
			return 0, err
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	migrated := 0
	for _, item := range legacy {
		var msg types.IncomingMessage
		if item.rawJSON != "" {
			_ = json.Unmarshal([]byte(item.rawJSON), &msg)
		}
		if msg.ID == "" {
			msg.ID = item.externalID
		}
		if msg.ChatID == "" {
			msg.ChatID = chatID
		}
		if msg.SenderID == "" {
			msg.SenderID = item.senderID
		}
		if msg.Text == "" {
			msg.Text = item.text
		}
		if msg.RawText == "" {
			msg.RawText = msg.Text
		}
		if msg.Timestamp.IsZero() {
			if parsed, err := time.Parse(time.RFC3339Nano, item.receivedAt); err == nil {
				msg.Timestamp = parsed
			}
		}
		_, fresh, err := s.StoreActiveInboxMessage(ctx, profileID, chatAlias, sessionID, msg)
		if err != nil {
			return migrated, err
		}
		if fresh {
			migrated++
		}
		if err := s.MarkMessageProcessed(ctx, item.id, "active_inbox"); err != nil {
			return migrated, err
		}
	}
	return migrated, nil
}

func (s *Store) RecordInvocation(ctx context.Context, record InvocationRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runner_invocations
		(profile_id, chat_id, runner_id, message_id, request_json, response_json, exit_code, duration_ms, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ProfileID, record.ChatID, record.RunnerID, record.MessageID, string(record.RequestJSON),
		string(record.ResponseJSON), record.ExitCode, record.Duration.Milliseconds(), record.Status, formatTime(time.Now()))
	return err
}

func (s *Store) RecordOutboxSent(ctx context.Context, profileID, chatID string, messageID int64, text string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO outbox
		(profile_id, chat_id, message_id, text, status, attempts, created_at, sent_at)
		VALUES (?, ?, ?, ?, 'sent', 1, ?, ?)`, profileID, chatID, messageID, text, now, now)
	return err
}

func (s *Store) RecordOutboxFailure(ctx context.Context, profileID, chatID string, messageID int64, text string, sendErr error) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO outbox
		(profile_id, chat_id, message_id, text, status, attempts, last_error, created_at)
		VALUES (?, ?, ?, ?, 'failed_retryable', 1, ?, ?)`, profileID, chatID, messageID, text, sendErr.Error(), now)
	return err
}

func (s *Store) RecentlySentText(ctx context.Context, profileID, chatID, text string, since time.Time) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, nil
	}
	sinceText := formatTime(since)
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM outbox WHERE profile_id = ? AND chat_id = ? AND status = 'sent' AND sent_at >= ? AND TRIM(text) = ?)
		+
		(SELECT COUNT(*) FROM active_outbox WHERE profile_id = ? AND chat_id = ? AND status = 'sent' AND sent_at >= ? AND TRIM(text) = ?)`,
		profileID, chatID, sinceText, text,
		profileID, chatID, sinceText, text).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) DeleteChatData(ctx context.Context, profileID, chatID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	total := 0
	statements := []string{
		`DELETE FROM active_inbox WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM active_outbox WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM active_read_receipts WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM pending_interactions WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM outbox WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM runner_invocations WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM messages WHERE profile_id = ? AND chat_id = ?`,
		`DELETE FROM chats WHERE profile_id = ? AND id = ?`,
	}
	for _, statement := range statements {
		result, err := tx.ExecContext(ctx, statement, profileID, chatID)
		if err != nil {
			return 0, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		total += int(affected)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) PendingOutboxCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE status IN ('pending', 'failed_retryable')`).Scan(&count)
	return count, err
}

func (s *Store) Audit(ctx context.Context, profileID, eventType, actor, target string, details any) error {
	raw, _ := json.Marshal(details)
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events
		(profile_id, event_type, actor, target, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, profileID, eventType, actor, target, string(raw), formatTime(time.Now()))
	return err
}

func (s *Store) LatestAuditEvent(ctx context.Context, profileID, eventType, target string) (AuditEventRecord, bool, error) {
	query := `SELECT ` + auditEventColumns + ` FROM audit_events WHERE profile_id = ?`
	args := []any{profileID}
	if eventType != "" {
		query += ` AND event_type = ?`
		args = append(args, eventType)
	}
	if target != "" {
		query += ` AND target = ?`
		args = append(args, target)
	}
	query += ` ORDER BY id DESC LIMIT 1`
	record, err := scanAuditEvent(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return AuditEventRecord{}, false, nil
	}
	if err != nil {
		return AuditEventRecord{}, false, err
	}
	return record, true, nil
}

func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMessage(row scanner) (MessageRecord, error) {
	var record MessageRecord
	var receivedAt string
	var processedAt sql.NullString
	err := row.Scan(&record.ID, &record.ProfileID, &record.ChatID, &record.SenderID, &record.ExternalMessageID,
		&record.Direction, &record.Text, &record.TextHash, &record.Status, &receivedAt, &processedAt)
	if err != nil {
		return MessageRecord{}, err
	}
	parsedReceived, err := time.Parse(time.RFC3339Nano, receivedAt)
	if err == nil {
		record.ReceivedAt = parsedReceived
	}
	if processedAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, processedAt.String); err == nil {
			record.ProcessedAt = &parsed
		}
	}
	return record, nil
}

func scanActiveInbox(row scanner) (ActiveInboxRecord, error) {
	var record ActiveInboxRecord
	var receivedAt string
	var claimedAt sql.NullString
	var doneAt sql.NullString
	err := row.Scan(&record.ID, &record.ProfileID, &record.ChatID, &record.ChatAlias, &record.SessionID, &record.SenderID, &record.SenderName,
		&record.ExternalMessageID, &record.Text, &record.RawText, &record.RawJSON, &record.Status, &receivedAt, &claimedAt, &record.ClaimedBySessionID, &doneAt)
	if err != nil {
		return ActiveInboxRecord{}, err
	}
	if record.RawJSON != "" {
		var msg types.IncomingMessage
		if err := json.Unmarshal([]byte(record.RawJSON), &msg); err == nil {
			record.Media = msg.Media
		}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, receivedAt); err == nil {
		record.ReceivedAt = parsed
	}
	if claimedAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, claimedAt.String); err == nil {
			record.ClaimedAt = &parsed
		}
	}
	if doneAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, doneAt.String); err == nil {
			record.DoneAt = &parsed
		}
	}
	return record, nil
}

func scanActiveWatcher(row scanner) (ActiveWatcherRecord, error) {
	var record ActiveWatcherRecord
	var startedAt string
	var heartbeatAt string
	err := row.Scan(&record.ProfileID, &record.SessionID, &record.ConsumerID, &record.PID, &record.Status, &startedAt, &heartbeatAt)
	if err != nil {
		return ActiveWatcherRecord{}, err
	}
	if parsed, err := time.Parse(time.RFC3339Nano, startedAt); err == nil {
		record.StartedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, heartbeatAt); err == nil {
		record.HeartbeatAt = parsed
	}
	return record, nil
}

func scanActiveOutbox(row scanner) (ActiveOutboxRecord, error) {
	var record ActiveOutboxRecord
	var important int
	var createdAt string
	var sentAt sql.NullString
	err := row.Scan(&record.ID, &record.ProfileID, &record.ChatID, &record.Text, &important, &record.Status,
		&record.Attempts, &record.LastError, &createdAt, &sentAt)
	if err != nil {
		return ActiveOutboxRecord{}, err
	}
	record.Important = important == 1
	if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		record.CreatedAt = parsed
	}
	if sentAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, sentAt.String); err == nil {
			record.SentAt = &parsed
		}
	}
	return record, nil
}

func scanActiveReadReceipt(row scanner) (ActiveReadReceiptRecord, error) {
	var record ActiveReadReceiptRecord
	var createdAt string
	var sentAt sql.NullString
	err := row.Scan(&record.ID, &record.ProfileID, &record.ChatID, &record.SenderID, &record.ExternalMessageID,
		&record.Status, &record.Attempts, &record.LastError, &createdAt, &sentAt)
	if err != nil {
		return ActiveReadReceiptRecord{}, err
	}
	if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		record.CreatedAt = parsed
	}
	if sentAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, sentAt.String); err == nil {
			record.SentAt = &parsed
		}
	}
	return record, nil
}

func scanPendingInteraction(row scanner) (PendingInteractionRecord, error) {
	var record PendingInteractionRecord
	var optionsJSON string
	var createdAt string
	var expiresAt string
	var answeredAt sql.NullString
	err := row.Scan(&record.ID, &record.ProfileID, &record.ChatID, &record.SenderID, &record.RunnerID,
		&record.SourceMessageID, &record.Prompt, &optionsJSON, &record.Status, &createdAt, &expiresAt,
		&answeredAt, &record.SelectedIndex, &record.SelectedText)
	if err != nil {
		return PendingInteractionRecord{}, err
	}
	_ = json.Unmarshal([]byte(optionsJSON), &record.Options)
	if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		record.CreatedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339Nano, expiresAt); err == nil {
		record.ExpiresAt = parsed
	}
	if answeredAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, answeredAt.String); err == nil {
			record.AnsweredAt = &parsed
		}
	}
	return record, nil
}

func scanAuditEvent(row scanner) (AuditEventRecord, error) {
	var record AuditEventRecord
	var createdAt string
	err := row.Scan(&record.ID, &record.ProfileID, &record.EventType, &record.Actor, &record.Target, &record.DetailsJSON, &createdAt)
	if err != nil {
		return AuditEventRecord{}, err
	}
	if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		record.CreatedAt = parsed
	}
	return record, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") || strings.Contains(message, "constraint failed")
}

func (s *Store) String() string {
	if s == nil || s.db == nil {
		return "db:closed"
	}
	return fmt.Sprintf("db:%p", s.db)
}
