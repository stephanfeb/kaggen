package proactive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryRecordAndQuery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewHistoryStore(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	now := time.Now()

	// Record some runs
	store.Record("job-a", "cron", now.Add(-2*time.Hour), 150*time.Millisecond, "success", "", 1)
	store.Record("job-b", "webhook", now.Add(-1*time.Hour), 300*time.Millisecond, "failure", "connection refused", 1)
	store.Record("job-a", "cron", now, 200*time.Millisecond, "timeout", "context deadline exceeded", 2)

	// Query all
	runs, err := store.Query("", 10)
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(runs))
	}
	// Results should be in reverse order (most recent first)
	if runs[0].JobName != "job-a" || runs[0].Status != "timeout" {
		t.Errorf("expected most recent first, got %s/%s", runs[0].JobName, runs[0].Status)
	}

	// Query by name
	runs, err = store.Query("job-b", 10)
	if err != nil {
		t.Fatalf("query by name: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run for job-b, got %d", len(runs))
	}
	if runs[0].Error != "connection refused" {
		t.Errorf("expected error message, got %q", runs[0].Error)
	}

	// Query with limit
	runs, err = store.Query("", 1)
	if err != nil {
		t.Fatalf("query with limit: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run with limit, got %d", len(runs))
	}
}

func TestHistoryStoreCreatesDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "test.db")

	// Parent dir doesn't exist — sqlite3 should fail
	_, err := NewHistoryStore(dbPath)
	if err == nil {
		t.Error("expected error for non-existent parent dir")
	}

	// Create parent and try again
	os.MkdirAll(filepath.Dir(dbPath), 0755)
	store, err := NewHistoryStore(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	store.Close()
}

func TestHistoryEmptyQuery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewHistoryStore(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	runs, err := store.Query("", 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}
