package agent

import (
	"testing"
	"time"
)

func TestAuditStoreRoundTrip(t *testing.T) {
	store, err := NewAuditStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()

	// Record a request.
	if err := store.RecordRequest("a1", "Bash", "deploy", `{"command":"kubectl apply"}`, "Run command: kubectl apply", "sess1", "user1", now); err != nil {
		t.Fatal(err)
	}

	// Query before resolution.
	entries, err := store.Query("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Resolution != "" {
		t.Errorf("expected empty resolution, got %q", entries[0].Resolution)
	}

	// Resolve.
	if err := store.RecordResolution("a1", "approved", "user1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Query after resolution.
	entries, err = store.Query("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Resolution != "approved" {
		t.Errorf("expected approved, got %q", entries[0].Resolution)
	}
	if entries[0].ResolvedAt == nil {
		t.Error("expected resolved_at to be set")
	}
}

func TestAuditStoreFilterBySkill(t *testing.T) {
	store, err := NewAuditStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	store.RecordRequest("a1", "Bash", "deploy", "", "", "", "", now)
	store.RecordRequest("a2", "Bash", "db-admin", "", "", "", "", now)

	entries, err := store.Query("deploy", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].SkillName != "deploy" {
		t.Errorf("expected deploy, got %q", entries[0].SkillName)
	}
}
