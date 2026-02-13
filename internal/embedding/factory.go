package embedding

import (
	"fmt"

	"github.com/yourusername/kaggen/internal/config"
)

// NewEmbedder creates an Embedder based on the configuration.
// Supported providers: "ollama" (default), "gemini", "voyage".
func NewEmbedder(cfg *config.EmbeddingConfig) (Embedder, error) {
	switch cfg.Provider {
	case "ollama", "":
		return NewOllamaEmbedder(cfg.BaseURL, cfg.Model), nil

	case "gemini":
		apiKey := config.GeminiAPIKey()
		if apiKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY required for gemini embedding provider")
		}
		model := cfg.Model
		if model == "" {
			model = "text-embedding-004"
		}
		return NewGeminiEmbedder(apiKey, model), nil

	case "voyage":
		apiKey := config.VoyageAPIKey()
		if apiKey == "" {
			return nil, fmt.Errorf("VOYAGE_API_KEY required for voyage embedding provider")
		}
		model := cfg.Model
		if model == "" {
			model = "voyage-3"
		}
		return NewVoyageEmbedder(apiKey, model), nil

	default:
		return nil, fmt.Errorf("unknown embedding provider %q (supported: ollama, gemini, voyage)", cfg.Provider)
	}
}
