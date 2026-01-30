package backlog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// Store is a SQLite-backed persistent backlog.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) a SQLite backlog database at dbPath.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open backlog db: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate backlog db: %w", err)
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS backlog (
			id          TEXT PRIMARY KEY,
			title       TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			priority    TEXT NOT NULL DEFAULT 'normal',
			status      TEXT NOT NULL DEFAULT 'pending',
			source      TEXT NOT NULL DEFAULT 'user',
			parent_id   TEXT NOT NULL DEFAULT '',
			context     TEXT NOT NULL DEFAULT '{}',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_backlog_status ON backlog(status);
		CREATE INDEX IF NOT EXISTS idx_backlog_priority ON backlog(priority);
		CREATE INDEX IF NOT EXISTS idx_backlog_parent ON backlog(parent_id);
	`)
	if err != nil {
		return err
	}
	// Additive migration: add parent_id column if missing (existing databases).
	_, _ = db.Exec(`ALTER TABLE backlog ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_backlog_parent ON backlog(parent_id)`)
	return nil
}

// Add inserts a new item into the backlog. ID and timestamps are auto-set.
func (s *Store) Add(item *Item) error {
	if item.ID == "" {
		item.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now

	if item.Priority == "" {
		item.Priority = "normal"
	}
	if item.Status == "" {
		item.Status = "pending"
	}
	if item.Source == "" {
		item.Source = "user"
	}

	ctxJSON, err := json.Marshal(item.Context)
	if err != nil {
		ctxJSON = []byte("{}")
	}

	_, err = s.db.Exec(`
		INSERT INTO backlog (id, title, description, priority, status, source, parent_id, context, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Title, item.Description, item.Priority, item.Status, item.Source, item.ParentID,
		string(ctxJSON), now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	return err
}

// Get returns a single backlog item by ID.
func (s *Store) Get(id string) (*Item, error) {
	row := s.db.QueryRow(`SELECT id, title, description, priority, status, source, parent_id, context, created_at, updated_at FROM backlog WHERE id = ?`, id)
	item, err := scanItem(row)
	if err != nil {
		return nil, err
	}
	// Populate child counts for parent items.
	s.populateChildCounts(item)
	return item, nil
}

// GetWithChildren returns a parent item and all its children.
func (s *Store) GetWithChildren(id string) (*Item, []*Item, error) {
	parent, err := s.Get(id)
	if err != nil {
		return nil, nil, err
	}
	children, err := s.List(Filter{ParentID: id, Limit: 200})
	if err != nil {
		return nil, nil, err
	}
	return parent, children, nil
}

// CheckParentCompletion returns true if all children of a parent are completed or failed.
func (s *Store) CheckParentCompletion(parentID string) (bool, error) {
	if parentID == "" {
		return false, nil
	}
	var total, done int
	err := s.db.QueryRow(`SELECT COUNT(*), COUNT(CASE WHEN status IN ('completed','failed') THEN 1 END) FROM backlog WHERE parent_id = ?`, parentID).Scan(&total, &done)
	if err != nil {
		return false, err
	}
	return total > 0 && total == done, nil
}

// List returns backlog items matching the filter.
func (s *Store) List(f Filter) ([]*Item, error) {
	query := `SELECT id, title, description, priority, status, source, parent_id, context, created_at, updated_at FROM backlog WHERE 1=1`
	var args []any

	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Priority != "" {
		query += ` AND priority = ?`
		args = append(args, f.Priority)
	}
	if f.Source != "" {
		query += ` AND source = ?`
		args = append(args, f.Source)
	}
	if f.ParentID != "" {
		query += ` AND parent_id = ?`
		args = append(args, f.ParentID)
	}
	if f.TopLevel {
		query += ` AND parent_id = ''`
	}

	query += ` ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 WHEN 'low' THEN 2 ELSE 3 END, created_at ASC`

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*Item
	for rows.Next() {
		item, err := scanItemRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Populate child counts for items that might be parents.
	for _, item := range items {
		s.populateChildCounts(item)
	}
	return items, nil
}

// Update applies partial updates to a backlog item.
func (s *Store) Update(id string, u Update) error {
	sets := []string{"updated_at = ?"}
	args := []any{time.Now().UTC().Format(time.RFC3339)}

	if u.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *u.Title)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.Priority != nil {
		sets = append(sets, "priority = ?")
		args = append(args, *u.Priority)
	}
	if u.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *u.Status)
	}
	if u.ParentID != nil {
		sets = append(sets, "parent_id = ?")
		args = append(args, *u.ParentID)
	}

	args = append(args, id)

	query := "UPDATE backlog SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += " WHERE id = ?"

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("backlog item %q not found", id)
	}
	return nil
}

// Complete marks an item as completed with a summary appended to its description.
func (s *Store) Complete(id, summary string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE backlog SET status = 'completed', description = description || char(10) || char(10) || 'Result: ' || ?, updated_at = ?
		WHERE id = ?`,
		summary, now, id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("backlog item %q not found", id)
	}
	return nil
}

// Delete removes a backlog item.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM backlog WHERE id = ?`, id)
	return err
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// populateChildCounts fills ChildCount and DoneCount for a given item.
func (s *Store) populateChildCounts(item *Item) {
	var total, done int
	_ = s.db.QueryRow(
		`SELECT COUNT(*), COUNT(CASE WHEN status IN ('completed','failed') THEN 1 END) FROM backlog WHERE parent_id = ?`,
		item.ID,
	).Scan(&total, &done)
	item.ChildCount = total
	item.DoneCount = done
}

// scanItem scans a single row into an Item.
func scanItem(row *sql.Row) (*Item, error) {
	var item Item
	var ctxJSON, createdAt, updatedAt string
	err := row.Scan(&item.ID, &item.Title, &item.Description, &item.Priority,
		&item.Status, &item.Source, &item.ParentID, &ctxJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(ctxJSON), &item.Context)
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &item, nil
}

func scanItemRows(rows *sql.Rows) (*Item, error) {
	var item Item
	var ctxJSON, createdAt, updatedAt string
	err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.Priority,
		&item.Status, &item.Source, &item.ParentID, &ctxJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(ctxJSON), &item.Context)
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &item, nil
}
