package agent

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// AuditEntry represents a single approval audit record.
type AuditEntry struct {
	ID          int64
	ApprovalID  string
	ToolName    string
	SkillName   string
	Arguments   string
	Description string
	SessionID   string
	UserID      string
	RequestedAt time.Time
	ResolvedAt  *time.Time
	Resolution  string // approved, rejected, timed_out, auto_approved, notified
	ResolvedBy  string
}

// AuditStore persists approval lifecycle events in SQLite.
type AuditStore struct {
	db *sql.DB
}

// NewAuditStore opens or creates the audit database.
func NewAuditStore(dbPath string) (*AuditStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS approval_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			approval_id TEXT NOT NULL UNIQUE,
			tool_name TEXT NOT NULL,
			skill_name TEXT NOT NULL,
			arguments TEXT,
			description TEXT,
			session_id TEXT,
			user_id TEXT,
			requested_at TEXT NOT NULL,
			resolved_at TEXT,
			resolution TEXT,
			resolved_by TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_audit_approval_id ON approval_audit(approval_id);
		CREATE INDEX IF NOT EXISTS idx_audit_requested ON approval_audit(requested_at);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &AuditStore{db: db}, nil
}

// RecordRequest logs a new approval request.
func (a *AuditStore) RecordRequest(approvalID, toolName, skillName, args, desc, sessionID, userID string, requestedAt time.Time) error {
	_, err := a.db.Exec(
		`INSERT INTO approval_audit (approval_id, tool_name, skill_name, arguments, description, session_id, user_id, requested_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		approvalID, toolName, skillName, args, desc, sessionID, userID, requestedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// RecordResolution updates an existing audit entry with the resolution outcome.
func (a *AuditStore) RecordResolution(approvalID, resolution, resolvedBy string, resolvedAt time.Time) error {
	_, err := a.db.Exec(
		`UPDATE approval_audit SET resolved_at = ?, resolution = ?, resolved_by = ? WHERE approval_id = ?`,
		resolvedAt.UTC().Format(time.RFC3339), resolution, resolvedBy, approvalID,
	)
	return err
}

// Query returns recent audit entries, optionally filtered by skill name.
func (a *AuditStore) Query(skillName string, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error
	if skillName != "" {
		rows, err = a.db.Query(
			`SELECT id, approval_id, tool_name, skill_name, COALESCE(arguments,''), COALESCE(description,''),
			        COALESCE(session_id,''), COALESCE(user_id,''), requested_at,
			        resolved_at, COALESCE(resolution,''), COALESCE(resolved_by,'')
			 FROM approval_audit WHERE skill_name = ? ORDER BY id DESC LIMIT ?`, skillName, limit)
	} else {
		rows, err = a.db.Query(
			`SELECT id, approval_id, tool_name, skill_name, COALESCE(arguments,''), COALESCE(description,''),
			        COALESCE(session_id,''), COALESCE(user_id,''), requested_at,
			        resolved_at, COALESCE(resolution,''), COALESCE(resolved_by,'')
			 FROM approval_audit ORDER BY id DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var reqStr, resStr sql.NullString
		if err := rows.Scan(&e.ID, &e.ApprovalID, &e.ToolName, &e.SkillName,
			&e.Arguments, &e.Description, &e.SessionID, &e.UserID,
			&reqStr, &resStr, &e.Resolution, &e.ResolvedBy); err != nil {
			return nil, err
		}
		if reqStr.Valid {
			e.RequestedAt, _ = time.Parse(time.RFC3339, reqStr.String)
		}
		if resStr.Valid {
			t, _ := time.Parse(time.RFC3339, resStr.String)
			e.ResolvedAt = &t
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Close closes the database.
func (a *AuditStore) Close() error {
	return a.db.Close()
}
