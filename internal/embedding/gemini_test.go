package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiEmbedder_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify API key in query
		if !strings.Contains(r.URL.RawQuery, "key=test-api-key") {
			t.Errorf("missing API key in query: %s", r.URL.RawQuery)
		}

		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, ":batchEmbedContents") {
			// Batch response
			var req geminiBatchEmbedRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode batch request: %v", err)
			}

			resp := geminiBatchEmbedResponse{
				Embeddings: make([]struct {
					Values []float64 `json:"values"`
				}, len(req.Requests)),
			}
			for i := range req.Requests {
				resp.Embeddings[i].Values = []float64{0.1, 0.2, 0.3}
			}
			json.NewEncoder(w).Encode(resp)
		} else if strings.Contains(r.URL.Path, ":embedContent") {
			// Single response
			resp := geminiEmbedResponse{}
			resp.Embedding.Values = []float64{0.1, 0.2, 0.3}
			json.NewEncoder(w).Encode(resp)
		} else {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	embedder := NewGeminiEmbedder("test-api-key", "text-embedding-004")
	embedder.baseURL = server.URL + "/models"

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

func TestGeminiEmbedder_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    400,
				"message": "Invalid API key",
			},
		})
	}))
	defer server.Close()

	embedder := NewGeminiEmbedder("bad-key", "text-embedding-004")
	embedder.baseURL = server.URL + "/models"

	_, err := embedder.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected status 400 in error, got: %v", err)
	}
}

func TestGeminiEmbedder_Retry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		// Success on second attempt
		resp := geminiEmbedResponse{}
		resp.Embedding.Values = []float64{0.1, 0.2, 0.3}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	embedder := NewGeminiEmbedder("test-key", "text-embedding-004")
	embedder.baseURL = server.URL + "/models"

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
