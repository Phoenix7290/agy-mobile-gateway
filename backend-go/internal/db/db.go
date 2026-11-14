package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// GatewayDB provides SQLite persistence with WAL mode and FTS5, 1:1 compatible with Python GatewayDB.
type GatewayDB struct {
	sql *sql.DB
	mu  sync.Mutex
}

// Job represents an asynchronous AGY or Ollama execution record.
type Job struct {
	ID             string  `json:"id"`
	ConversationID *string `json:"conversation_id"`
	Engine         string  `json:"engine"`
	Model          string  `json:"model"`
	PermissionMode string  `json:"permission_mode"`
	Prompt         string  `json:"prompt"`
	Status         string  `json:"status"`
	Error          *string `json:"error"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	LastSeq        int     `json:"last_seq,omitempty"`
}

// JobEvent represents a resumable SSE event stored for a job.
type JobEvent struct {
	Seq       int            `json:"seq"`
	EventType string         `json:"event_type"`
	Payload   map[string]any `json:"payload"`
	CreatedAt string         `json:"created_at"`
}

// LocalMessage represents a single chat message stored locally (e.g. Ollama).
type LocalMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LocalConversationSummary mirrors the list item JSON returned by /api/conversations.
type LocalConversationSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Model        string `json:"model"`
	Engine       string `json:"engine"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
}

// LocalConversationDetail mirrors the detail JSON returned by /api/conversations/{id}.
type LocalConversationDetail struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Model     string         `json:"model"`
	Engine    string         `json:"engine"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	Messages  []LocalMessage `json:"messages"`
}

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// Open initializes the SQLite database file with WAL mode, foreign keys, and schema parity.
func Open(dbPath string) (*GatewayDB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(10000)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer connection keeps SQLite lock handling deterministic like Python's self._lock.
	sqlDB.SetMaxOpenConns(1)

	g := &GatewayDB{sql: sqlDB}
	if err := g.initSchema(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return g, nil
}

// Close releases the underlying SQLite connection.
func (g *GatewayDB) Close() error {
	return g.sql.Close()
}

func (g *GatewayDB) initSchema() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	const schema = `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		conversation_id TEXT,
		engine TEXT NOT NULL,
		model TEXT NOT NULL,
		permission_mode TEXT NOT NULL,
		prompt TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'running',
		error TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS job_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		event_type TEXT NOT NULL,
		payload TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_job_events_job_seq
		ON job_events(job_id, seq);

	CREATE TABLE IF NOT EXISTS local_conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		model TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS local_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY (conversation_id) REFERENCES local_conversations(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_local_messages_conv
		ON local_messages(conversation_id, id);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS push_subscriptions (
		endpoint TEXT PRIMARY KEY,
		subscription_json TEXT NOT NULL,
		created_at TEXT NOT NULL
	);

	CREATE VIRTUAL TABLE IF NOT EXISTS conversations_fts
		USING fts5(conv_id UNINDEXED, title, content, tokenize='unicode61');
	`
	if _, err := g.sql.Exec(schema); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	if _, err := g.sql.Exec(
		"UPDATE jobs SET status = 'error', error = 'Gateway reiniciado durante execução', updated_at = ? WHERE status = 'running'",
		utcNow(),
	); err != nil {
		return fmt.Errorf("cleanup orphaned jobs: %w", err)
	}
	return nil
}

// CreateJob inserts a new running job and returns the persisted record.
func (g *GatewayDB) CreateJob(jobID string, convID *string, engine, model, permMode, prompt string) (*Job, error) {
	now := utcNow()
	g.mu.Lock()
	_, err := g.sql.Exec(
		`INSERT INTO jobs (id, conversation_id, engine, model, permission_mode, prompt, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'running', ?, ?)`,
		jobID, convID, engine, model, permMode, prompt, now, now,
	)
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return g.GetJob(jobID)
}

// UpdateJob updates optional fields (conversation_id, status, error) on an existing job.
func (g *GatewayDB) UpdateJob(jobID string, convID, status, errMsg *string) error {
	fields := []string{"updated_at = ?"}
	args := []any{utcNow()}
	if convID != nil {
		fields = append(fields, "conversation_id = ?")
		args = append(args, *convID)
	}
	if status != nil {
		fields = append(fields, "status = ?")
		args = append(args, *status)
	}
	if errMsg != nil {
		fields = append(fields, "error = ?")
		args = append(args, *errMsg)
	}
	args = append(args, jobID)

	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := g.sql.Exec(fmt.Sprintf("UPDATE jobs SET %s WHERE id = ?", strings.Join(fields, ", ")), args...)
	return err
}

// GetJob fetches a single job along with its latest event sequence number.
func (g *GatewayDB) GetJob(jobID string) (*Job, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	var j Job
	var convID, errText sql.NullString
	err := g.sql.QueryRow(
		`SELECT id, conversation_id, engine, model, permission_mode, prompt, status, error, created_at, updated_at
		 FROM jobs WHERE id = ?`, jobID,
	).Scan(&j.ID, &convID, &j.Engine, &j.Model, &j.PermissionMode, &j.Prompt, &j.Status, &errText, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if convID.Valid {
		j.ConversationID = &convID.String
	}
	if errText.Valid {
		j.Error = &errText.String
	}
	_ = g.sql.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM job_events WHERE job_id = ?", jobID).Scan(&j.LastSeq)
	return &j, nil
}

// GetActiveJobs returns up to 10 currently running jobs ordered newest first.
func (g *GatewayDB) GetActiveJobs() ([]Job, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rows, err := g.sql.Query(
		`SELECT id, conversation_id, engine, model, permission_mode, prompt, status, error, created_at, updated_at
		 FROM jobs WHERE status = 'running' ORDER BY created_at DESC LIMIT 10`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var convID, errText sql.NullString
		if err := rows.Scan(&j.ID, &convID, &j.Engine, &j.Model, &j.PermissionMode, &j.Prompt, &j.Status, &errText, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		if convID.Valid {
			j.ConversationID = &convID.String
		}
		if errText.Valid {
			j.Error = &errText.String
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// AppendJobEvent stores a sequenced event for SSE replay and returns its sequence number.
func (g *GatewayDB) AppendJobEvent(jobID, eventType string, payload map[string]any) (int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	var seq int
	if err := g.sql.QueryRow("SELECT COALESCE(MAX(seq), 0) + 1 FROM job_events WHERE job_id = ?", jobID).Scan(&seq); err != nil {
		return 0, err
	}
	if _, err := g.sql.Exec(
		"INSERT INTO job_events (job_id, seq, event_type, payload, created_at) VALUES (?, ?, ?, ?, ?)",
		jobID, seq, eventType, string(raw), utcNow(),
	); err != nil {
		return 0, err
	}
	return seq, nil
}

// GetJobEventsAfter fetches ordered SSE events strictly newer than afterSeq.
func (g *GatewayDB) GetJobEventsAfter(jobID string, afterSeq int) ([]JobEvent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rows, err := g.sql.Query(
		"SELECT seq, event_type, payload, created_at FROM job_events WHERE job_id = ? AND seq > ? ORDER BY seq ASC",
		jobID, afterSeq,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []JobEvent
	for rows.Next() {
		var ev JobEvent
		var payloadStr string
		if err := rows.Scan(&ev.Seq, &ev.EventType, &payloadStr, &ev.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payloadStr), &ev.Payload); err != nil {
			ev.Payload = map[string]any{}
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// EnsureLocalConversation inserts or updates metadata for a local Ollama conversation.
func (g *GatewayDB) EnsureLocalConversation(convID, title, model string) error {
	now := utcNow()
	g.mu.Lock()
	defer g.mu.Unlock()

	var existing string
	err := g.sql.QueryRow("SELECT id FROM local_conversations WHERE id = ?", convID).Scan(&existing)
	if err == sql.ErrNoRows {
		_, err = g.sql.Exec(
			"INSERT INTO local_conversations (id, title, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			convID, title, model, now, now,
		)
		return err
	}
	if err != nil {
		return err
	}
	_, err = g.sql.Exec("UPDATE local_conversations SET model = ?, updated_at = ? WHERE id = ?", model, now, convID)
	return err
}

// AppendLocalMessage saves a message and updates the FTS5 search index for that conversation.
func (g *GatewayDB) AppendLocalMessage(convID, role, content string) error {
	now := utcNow()
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := g.sql.Exec(
		"INSERT INTO local_messages (conversation_id, role, content, created_at) VALUES (?, ?, ?, ?)",
		convID, role, content, now,
	); err != nil {
		return err
	}
	if _, err := g.sql.Exec("UPDATE local_conversations SET updated_at = ? WHERE id = ?", now, convID); err != nil {
		return err
	}

	var title string
	_ = g.sql.QueryRow("SELECT title FROM local_conversations WHERE id = ?", convID).Scan(&title)

	rows, err := g.sql.Query("SELECT content FROM local_messages WHERE conversation_id = ? ORDER BY id ASC", convID)
	if err != nil {
		return err
	}
	var parts []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err == nil {
			parts = append(parts, c)
		}
	}
	_ = rows.Close()

	_, _ = g.sql.Exec("DELETE FROM conversations_fts WHERE conv_id = ?", convID)
	_, err = g.sql.Exec(
		"INSERT INTO conversations_fts (conv_id, title, content) VALUES (?, ?, ?)",
		convID, title, strings.Join(parts, "\n"),
	)
	return err
}

// GetLocalMessages returns raw role/content history (used when building Ollama chat payloads).
func (g *GatewayDB) GetLocalMessages(convID string) ([]LocalMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rows, err := g.sql.Query("SELECT role, content FROM local_messages WHERE conversation_id = ? ORDER BY id ASC", convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []LocalMessage
	for rows.Next() {
		var m LocalMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetLocalConversation returns full conversation details, mapping assistant -> agent for UI parity.
func (g *GatewayDB) GetLocalConversation(convID string) (*LocalConversationDetail, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	var detail LocalConversationDetail
	err := g.sql.QueryRow(
		"SELECT id, title, model, created_at, updated_at FROM local_conversations WHERE id = ?", convID,
	).Scan(&detail.ID, &detail.Title, &detail.Model, &detail.CreatedAt, &detail.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	detail.Engine = "ollama"

	rows, err := g.sql.Query("SELECT role, content FROM local_messages WHERE conversation_id = ? ORDER BY id ASC", convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	detail.Messages = []LocalMessage{}
	for rows.Next() {
		var m LocalMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		if m.Role == "assistant" {
			m.Role = "agent"
		}
		detail.Messages = append(detail.Messages, m)
	}
	return &detail, rows.Err()
}

// ListLocalConversations returns all local Ollama conversations sorted by most recent update.
func (g *GatewayDB) ListLocalConversations() ([]LocalConversationSummary, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rows, err := g.sql.Query(`
		SELECT c.id, c.title, c.model, c.created_at, c.updated_at, COUNT(m.id) AS message_count
		FROM local_conversations c
		LEFT JOIN local_messages m ON m.conversation_id = c.id
		GROUP BY c.id
		ORDER BY c.updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LocalConversationSummary{}
	for rows.Next() {
		var s LocalConversationSummary
		s.Engine = "ollama"
		if err := rows.Scan(&s.ID, &s.Title, &s.Model, &s.CreatedAt, &s.UpdatedAt, &s.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteLocalConversation removes a local conversation and its FTS5 index row.
func (g *GatewayDB) DeleteLocalConversation(convID string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, _ = g.sql.Exec("DELETE FROM conversations_fts WHERE conv_id = ?", convID)
	res, err := g.sql.Exec("DELETE FROM local_conversations WHERE id = ?", convID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// GetSettings returns persisted gateway preferences merged onto defaults.
func (g *GatewayDB) GetSettings() (map[string]any, error) {
	out := map[string]any{
		"default_permission_mode": "copilot",
		"default_model":           "gemini-4-hydrogen-high",
		"default_effort":          "",
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	rows, err := g.sql.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var k, rawVal string
		if err := rows.Scan(&k, &rawVal); err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal([]byte(rawVal), &decoded); err != nil {
			out[k] = rawVal
		} else {
			out[k] = decoded
		}
	}
	return out, rows.Err()
}

// UpdateSettings upserts key/value preference pairs and returns the updated map.
func (g *GatewayDB) UpdateSettings(patch map[string]any) (map[string]any, error) {
	now := utcNow()
	g.mu.Lock()
	for k, v := range patch {
		encoded, err := json.Marshal(v)
		if err != nil {
			g.mu.Unlock()
			return nil, err
		}
		if _, err := g.sql.Exec(
			`INSERT INTO settings (key, value, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			k, string(encoded), now,
		); err != nil {
			g.mu.Unlock()
			return nil, err
		}
	}
	g.mu.Unlock()
	return g.GetSettings()
}

// UpsertFTS refreshes the full-text index entry for a conversation.
func (g *GatewayDB) UpsertFTS(convID, title, content string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := g.sql.Exec("DELETE FROM conversations_fts WHERE conv_id = ?", convID); err != nil {
		return err
	}
	_, err := g.sql.Exec(
		"INSERT INTO conversations_fts (conv_id, title, content) VALUES (?, ?, ?)",
		convID, title, content,
	)
	return err
}

// SearchFTS performs a prefix-token FTS5 query and returns matching conversation IDs by rank.
func (g *GatewayDB) SearchFTS(query string, limit int) ([]string, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 40
	}

	var tokens []string
	for _, tok := range strings.Fields(q) {
		clean := strings.ReplaceAll(tok, `"`, "")
		if clean != "" {
			tokens = append(tokens, `"`+clean+`"*`)
		}
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	rows, err := g.sql.Query(
		"SELECT conv_id FROM conversations_fts WHERE conversations_fts MATCH ? ORDER BY rank LIMIT ?",
		strings.Join(tokens, " AND "), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
