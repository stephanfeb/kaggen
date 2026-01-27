package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// initEntityTables creates the entity graph schema (idempotent).
func initEntityTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS entities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			aliases TEXT DEFAULT '[]',
			summary TEXT DEFAULT '',
			summary_updated_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memory_entities (
			memory_id TEXT NOT NULL,
			entity_id INTEGER NOT NULL,
			PRIMARY KEY (memory_id, entity_id),
			FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE,
			FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS entity_relations (
			entity_a INTEGER NOT NULL,
			entity_b INTEGER NOT NULL,
			relation_type TEXT NOT NULL DEFAULT 'cooccurs',
			weight REAL DEFAULT 1.0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (entity_a, entity_b, relation_type)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("init entity tables: %w", err)
		}
	}
	return nil
}

// resolveEntity finds an entity by name (case-insensitive) or creates it.
func resolveEntity(ctx context.Context, db *sql.DB, name string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM entities WHERE LOWER(name) = LOWER(?)`, name,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("lookup entity %q: %w", name, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.ExecContext(ctx,
		`INSERT INTO entities (name, created_at, updated_at) VALUES (?, ?, ?)`,
		name, now, now,
	)
	if err != nil {
		// Race condition: another goroutine may have inserted concurrently.
		if strings.Contains(err.Error(), "UNIQUE") {
			err2 := db.QueryRowContext(ctx,
				`SELECT id FROM entities WHERE LOWER(name) = LOWER(?)`, name,
			).Scan(&id)
			if err2 == nil {
				return id, nil
			}
		}
		return 0, fmt.Errorf("insert entity %q: %w", name, err)
	}
	return res.LastInsertId()
}

// resolveEntities resolves a batch of entity names, returning their IDs.
func resolveEntities(ctx context.Context, db *sql.DB, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		id, err := resolveEntity(ctx, db, name)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// linkMemoryEntities creates junction rows between a memory and its entities.
// It replaces existing links for the memory.
func linkMemoryEntities(ctx context.Context, db *sql.DB, memoryID string, entityIDs []int64) error {
	// Remove old links first (handles updates cleanly)
	if _, err := db.ExecContext(ctx, `DELETE FROM memory_entities WHERE memory_id = ?`, memoryID); err != nil {
		return fmt.Errorf("clear memory_entities: %w", err)
	}
	for _, eid := range entityIDs {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO memory_entities (memory_id, entity_id) VALUES (?, ?)`,
			memoryID, eid,
		); err != nil {
			return fmt.Errorf("link memory %s to entity %d: %w", memoryID, eid, err)
		}
	}
	return nil
}

// updateCooccurrences strengthens co-occurrence edges between all pairs of entities.
func updateCooccurrences(ctx context.Context, db *sql.DB, entityIDs []int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < len(entityIDs); i++ {
		for j := i + 1; j < len(entityIDs); j++ {
			a, b := entityIDs[i], entityIDs[j]
			if a > b {
				a, b = b, a // canonical order
			}
			_, err := db.ExecContext(ctx,
				`INSERT INTO entity_relations (entity_a, entity_b, relation_type, weight, updated_at)
				 VALUES (?, ?, 'cooccurs', 1.0, ?)
				 ON CONFLICT(entity_a, entity_b, relation_type)
				 DO UPDATE SET weight = weight + 1.0, updated_at = ?`,
				a, b, now, now,
			)
			if err != nil {
				return fmt.Errorf("upsert cooccurrence (%d,%d): %w", a, b, err)
			}
		}
	}
	return nil
}

// getEntityIDsByName looks up entity IDs for a list of names (case-insensitive).
// Returns only those that exist.
func getEntityIDsByName(ctx context.Context, db *sql.DB, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for i, n := range names {
		placeholders[i] = "LOWER(?)"
		args[i] = strings.TrimSpace(n)
	}
	query := `SELECT id FROM entities WHERE LOWER(name) IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup entities by name: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
