package proactive

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// JobRun represents a single execution of a proactive job.
type JobRun struct {
	ID        int64
	JobName   string
	JobType   string // "cron", "webhook", "heartbeat"
	StartedAt time.Time
	Duration  time.Duration
	Status    string // "success", "failure", "timeout"
	Error     string
	Attempt   int
}

// HistoryStore persists proactive job execution history in SQLite.
type HistoryStore struct {
	db *sql.DB
}

// NewHistoryStore opens or creates the history database.
func NewHistoryStore(dbPath string) (*HistoryStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS job_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_name TEXT NOT NULL,
			job_type TEXT NOT NULL,
			started_at TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			status TEXT NOT NULL,
			error_message TEXT,
			attempt INTEGER DEFAULT 1
		);
		CREATE INDEX IF NOT EXISTS idx_job_runs_name ON job_runs(job_name);
		CREATE INDEX IF NOT EXISTS idx_job_runs_started ON job_runs(started_at);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &HistoryStore{db: db}, nil
}

// Record saves a job execution to history.
func (h *HistoryStore) Record(name, jobType string, startedAt time.Time, duration time.Duration, status, errMsg string, attempt int) error {
	_, err := h.db.Exec(
		`INSERT INTO job_runs (job_name, job_type, started_at, duration_ms, status, error_message, attempt)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, jobType, startedAt.UTC().Format(time.RFC3339), duration.Milliseconds(), status, errMsg, attempt,
	)
	return err
}

// Query returns recent job runs, optionally filtered by name.
func (h *HistoryStore) Query(name string, limit int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows *sql.Rows
	var err error
	if name != "" {
		rows, err = h.db.Query(
			`SELECT id, job_name, job_type, started_at, duration_ms, status, COALESCE(error_message,''), attempt
			 FROM job_runs WHERE job_name = ? ORDER BY id DESC LIMIT ?`, name, limit)
	} else {
		rows, err = h.db.Query(
			`SELECT id, job_name, job_type, started_at, duration_ms, status, COALESCE(error_message,''), attempt
			 FROM job_runs ORDER BY id DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []JobRun
	for rows.Next() {
		var r JobRun
		var startedStr string
		var durationMs int64
		if err := rows.Scan(&r.ID, &r.JobName, &r.JobType, &startedStr, &durationMs, &r.Status, &r.Error, &r.Attempt); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339, startedStr)
		r.Duration = time.Duration(durationMs) * time.Millisecond
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Close closes the database.
func (h *HistoryStore) Close() error {
	return h.db.Close()
}
