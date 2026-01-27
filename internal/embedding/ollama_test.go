package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedder_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if req.Model != "nomic-embed-text" {
			t.Errorf("unexpected model: %s", req.Model)
		}

		resp := ollamaEmbedResponse{
			Embeddings: make([][]float32, len(req.Input)),
		}
		for i := range req.Input {
			resp.Embeddings[i] = []float32{0.1, 0.2, 0.3}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewOllamaEmbedder(server.URL, "nomic-embed-text")

	t.Run("single embed", func(t *testing.T) {
		emb, err := embedder.Embed(context.Background(), "hello world")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(emb) != 3 {
			t.Fatalf("expected 3 dimensions, got %d", len(emb))
		}
		if emb[0] != 0.1 {
			t.Errorf("expected 0.1, got %f", emb[0])
		}
	})

	t.Run("batch embed", func(t *testing.T) {
		results, err := embedder.EmbedBatch(context.Background(), []string{"hello", "world"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("empty batch", func(t *testing.T) {
		results, err := embedder.EmbedBatch(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results != nil {
			t.Errorf("expected nil for empty input")
		}
	})

	t.Run("dimension", func(t *testing.T) {
		dim := embedder.Dimension()
		if dim != 3 {
			t.Errorf("expected dimension 3, got %d", dim)
		}
	})
}

func TestOllamaEmbedder_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not found"))
	}))
	defer server.Close()

	embedder := NewOllamaEmbedder(server.URL, "bad-model")
	_, err := embedder.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}
