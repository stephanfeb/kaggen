package memory

import (
	"testing"
)

func TestVectorIndex_InsertAndSearch(t *testing.T) {
	// Use in-memory SQLite
	idx, err := NewVectorIndex(":memory:", 3)
	if err != nil {
		t.Fatalf("NewVectorIndex: %v", err)
	}
	defer idx.Close()

	chunks := []MemoryChunk{
		{FilePath: "test.md", LineStart: 1, LineEnd: 3, Content: "The user prefers dark mode", Embedding: []float32{0.1, 0.2, 0.3}},
		{FilePath: "test.md", LineStart: 4, LineEnd: 6, Content: "The user likes Go programming", Embedding: []float32{0.4, 0.5, 0.6}},
		{FilePath: "notes.md", LineStart: 1, LineEnd: 2, Content: "Meeting notes from Monday", Embedding: []float32{0.7, 0.8, 0.9}},
	}

	for _, c := range chunks {
		if err := idx.Insert(c); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	t.Run("vector search", func(t *testing.T) {
		results, err := idx.Search([]float32{0.1, 0.2, 0.3}, 2)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		// First result should be closest to query
		if results[0].Content != "The user prefers dark mode" {
			t.Errorf("expected dark mode chunk first, got: %s", results[0].Content)
		}
	})

	t.Run("keyword search", func(t *testing.T) {
		results, err := idx.KeywordSearch("dark mode", 5)
		if err != nil {
			t.Fatalf("KeywordSearch: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		if results[0].Content != "The user prefers dark mode" {
			t.Errorf("expected dark mode chunk, got: %s", results[0].Content)
		}
	})

	t.Run("hybrid search", func(t *testing.T) {
		results, err := idx.HybridSearch([]float32{0.1, 0.2, 0.3}, "dark mode", 5)
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
	})

	t.Run("delete by file", func(t *testing.T) {
		if err := idx.DeleteByFile("test.md"); err != nil {
			t.Fatalf("DeleteByFile: %v", err)
		}
		results, err := idx.Search([]float32{0.1, 0.2, 0.3}, 10)
		if err != nil {
			t.Fatalf("Search after delete: %v", err)
		}
		for _, r := range results {
			if r.FilePath == "test.md" {
				t.Error("found test.md chunk after deletion")
			}
		}
	})
}
