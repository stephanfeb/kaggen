package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoyageEmbedder_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		// Verify Bearer token
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-api-key" {
			t.Errorf("unexpected auth header: %s", auth)
		}

		var req voyageEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if req.Model != "voyage-3" {
			t.Errorf("unexpected model: %s", req.Model)
		}

		resp := voyageEmbedResponse{
			Data: make([]struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}, len(req.Input)),
		}
		for i := range req.Input {
			resp.Data[i].Embedding = []float32{0.1, 0.2, 0.3}
			resp.Data[i].Index = i
		}
		resp.Usage.TotalTokens = 10

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewVoyageEmbedder("test-api-key", "voyage-3")
	embedder.baseURL = server.URL

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

func TestVoyageEmbedder_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":    "authentication_error",
				"message": "Invalid API key",
			},
		})
	}))
	defer server.Close()

	embedder := NewVoyageEmbedder("bad-key", "voyage-3")
	embedder.baseURL = server.URL

	_, err := embedder.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected status 401 in error, got: %v", err)
	}
}

func TestVoyageEmbedder_Retry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		// Success on second attempt
		resp := voyageEmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2, 0.3}, Index: 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewVoyageEmbedder("test-key", "voyage-3")
	embedder.baseURL = server.URL

	emb, err := embedder.Embed(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if len(emb) != 3 {
		t.Errorf("expected 3 dimensions, got %d", len(emb))
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}
