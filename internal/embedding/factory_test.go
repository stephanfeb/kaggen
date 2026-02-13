package embedding

import (
	"os"
	"testing"

	"github.com/yourusername/kaggen/internal/config"
)

func TestNewEmbedder(t *testing.T) {
	t.Run("ollama default", func(t *testing.T) {
		cfg := &config.EmbeddingConfig{
			Provider: "ollama",
			Model:    "nomic-embed-text",
			BaseURL:  "http://localhost:11434",
		}
		embedder, err := NewEmbedder(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := embedder.(*OllamaEmbedder); !ok {
			t.Errorf("expected OllamaEmbedder, got %T", embedder)
		}
	})

	t.Run("empty provider defaults to ollama", func(t *testing.T) {
		cfg := &config.EmbeddingConfig{
			Provider: "",
		}
		embedder, err := NewEmbedder(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := embedder.(*OllamaEmbedder); !ok {
			t.Errorf("expected OllamaEmbedder, got %T", embedder)
		}
	})

	t.Run("gemini requires API key", func(t *testing.T) {
		// Ensure no API key is set
		os.Unsetenv("GEMINI_API_KEY")

		cfg := &config.EmbeddingConfig{
			Provider: "gemini",
		}
		_, err := NewEmbedder(cfg)
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})

	t.Run("gemini with API key", func(t *testing.T) {
		os.Setenv("GEMINI_API_KEY", "test-key")
		defer os.Unsetenv("GEMINI_API_KEY")

		cfg := &config.EmbeddingConfig{
			Provider: "gemini",
		}
		embedder, err := NewEmbedder(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := embedder.(*GeminiEmbedder); !ok {
			t.Errorf("expected GeminiEmbedder, got %T", embedder)
		}
	})

	t.Run("voyage requires API key", func(t *testing.T) {
		os.Unsetenv("VOYAGE_API_KEY")

		cfg := &config.EmbeddingConfig{
			Provider: "voyage",
		}
		_, err := NewEmbedder(cfg)
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})

	t.Run("voyage with API key", func(t *testing.T) {
		os.Setenv("VOYAGE_API_KEY", "test-key")
		defer os.Unsetenv("VOYAGE_API_KEY")

		cfg := &config.EmbeddingConfig{
			Provider: "voyage",
		}
		embedder, err := NewEmbedder(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := embedder.(*VoyageEmbedder); !ok {
			t.Errorf("expected VoyageEmbedder, got %T", embedder)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		cfg := &config.EmbeddingConfig{
			Provider: "unknown",
		}
		_, err := NewEmbedder(cfg)
		if err == nil {
			t.Fatal("expected error for unknown provider")
		}
	})
}
