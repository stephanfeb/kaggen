package memory

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"

	"github.com/yourusername/kaggen/internal/embedding"
)

// newTestService creates a FileMemoryService backed by a temp SQLite DB.
func newTestService(t *testing.T) *FileMemoryService {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dim := 4 // small dimension for tests

	vecIndex, err := NewVectorIndex(dbPath, dim)
	if err != nil {
		t.Fatalf("NewVectorIndex: %v", err)
	}
	t.Cleanup(func() { vecIndex.Close() })

	embedder := &fakeEmbedder{dim: dim}
	logger := slog.Default()

	svc, err := NewFileMemoryService(vecIndex.DB(), vecIndex, embedder, dir, logger)
	if err != nil {
		t.Fatalf("NewFileMemoryService: %v", err)
	}
	t.Cleanup(func() { svc.Close() })
	return svc
}

type fakeEmbedder struct {
	dim int
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	emb := make([]float32, f.dim)
	for i := range emb {
		emb[i] = float32(i+1) * 0.1
	}
	return emb, nil
}

func (f *fakeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, t := range texts {
		e, err := f.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		results[i] = e
	}
	return results, nil
}

func (f *fakeEmbedder) Dimension() int { return f.dim }

var _ embedding.Embedder = (*fakeEmbedder)(nil)

var testUserKey = memory.UserKey{AppName: "kaggen", UserID: "test-user"}

func TestAddAndReadMemories(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.AddMemory(ctx, testUserKey, "User likes Go", []string{"preferences"}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if err := svc.AddMemory(ctx, testUserKey, "User lives in Berlin", []string{"location"}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	entries, err := svc.ReadMemories(ctx, testUserKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestAddMemoryIdempotent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.AddMemory(ctx, testUserKey, "User likes Go", []string{"preferences"}); err != nil {
			t.Fatalf("AddMemory attempt %d: %v", i, err)
		}
	}

	entries, err := svc.ReadMemories(ctx, testUserKey, 10)
	if err != nil {
		t.Fatalf("ReadMemories: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (idempotent), got %d", len(entries))
	}
}

func TestUpdateMemory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.AddMemory(ctx, testUserKey, "User likes Go", []string{"preferences"}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	entries, _ := svc.ReadMemories(ctx, testUserKey, 10)
	if len(entries) == 0 {
		t.Fatal("no entries after add")
	}

	memKey := memory.Key{AppName: testUserKey.AppName, UserID: testUserKey.UserID, MemoryID: entries[0].ID}
	if err := svc.UpdateMemory(ctx, memKey, "User loves Rust now", []string{"preferences"}); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	updated, _ := svc.ReadMemories(ctx, testUserKey, 10)
	if len(updated) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(updated))
	}
	if updated[0].Memory.Memory != "User loves Rust now" {
		t.Fatalf("content not updated: %s", updated[0].Memory.Memory)
	}
}

func TestDeleteMemory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.AddMemory(ctx, testUserKey, "User likes Go", nil)
	entries, _ := svc.ReadMemories(ctx, testUserKey, 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	memKey := memory.Key{AppName: testUserKey.AppName, UserID: testUserKey.UserID, MemoryID: entries[0].ID}
	if err := svc.DeleteMemory(ctx, memKey); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	entries, _ = svc.ReadMemories(ctx, testUserKey, 10)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after delete, got %d", len(entries))
	}
}

func TestClearMemories(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.AddMemory(ctx, testUserKey, "memory one", nil)
	svc.AddMemory(ctx, testUserKey, "memory two", nil)

	if err := svc.ClearMemories(ctx, testUserKey); err != nil {
		t.Fatalf("ClearMemories: %v", err)
	}

	entries, _ := svc.ReadMemories(ctx, testUserKey, 10)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", len(entries))
	}
}

func TestSearchMemories(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.AddMemory(ctx, testUserKey, "User enjoys hiking on weekends", []string{"hobbies"})
	svc.AddMemory(ctx, testUserKey, "User works as a software engineer", []string{"work"})

	results, err := svc.SearchMemories(ctx, testUserKey, "hiking")
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	// With fake embedder all vectors are identical, so hybrid search returns both.
	// Just verify we get results without error.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
}

func TestMemoryFileAppend(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.AddMemory(ctx, testUserKey, "User likes tea", []string{"preferences"})

	path := filepath.Join(svc.workspace, "MEMORY.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("MEMORY.md should not be empty")
	}
}

func TestToolsReturned(t *testing.T) {
	svc := newTestService(t)
	tools := svc.Tools()
	// Without extractor: default enabled tools are add, update, search, load
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
}

func TestGenerateMemoryIDConsistent(t *testing.T) {
	id1 := generateMemoryID("hello", []string{"b", "a"}, "app", "user")
	id2 := generateMemoryID("hello", []string{"a", "b"}, "app", "user")
	if id1 != id2 {
		t.Fatal("IDs should be equal regardless of topic order")
	}

	id3 := generateMemoryID("hello", []string{"a", "b"}, "app", "other-user")
	if id1 == id3 {
		t.Fatal("different users should produce different IDs")
	}
}
