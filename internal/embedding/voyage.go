package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

const (
	voyageDefaultBaseURL = "https://api.voyageai.com/v1"
	voyageDefaultModel   = "voyage-3"
	voyageHTTPTimeout    = 120 * time.Second
	voyageMaxRetries     = 5
	voyageMinBackoff     = 1 * time.Second
	voyageMaxBackoff     = 60 * time.Second
)

// VoyageEmbedder generates embeddings via Voyage AI's API.
type VoyageEmbedder struct {
	apiKey    string
	model     string
	baseURL   string
	client    *http.Client
	dimension int
	dimOnce   sync.Once
}

// NewVoyageEmbedder creates an embedder that calls the Voyage AI API.
func NewVoyageEmbedder(apiKey, model string) *VoyageEmbedder {
	if model == "" {
		model = voyageDefaultModel
	}
	return &VoyageEmbedder{
		apiKey:  apiKey,
		model:   model,
		baseURL: voyageDefaultBaseURL,
		client: &http.Client{
			Timeout: voyageHTTPTimeout,
		},
	}
}

// voyageEmbedRequest is the request body for Voyage embeddings.
type voyageEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// voyageEmbedResponse is the response from Voyage embeddings endpoint.
type voyageEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Error *voyageAPIError `json:"error,omitempty"`
}

type voyageAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (e *VoyageEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("voyage: empty embedding response")
	}
	return results[0], nil
}

func (e *VoyageEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := voyageEmbedRequest{
		Model: e.model,
		Input: texts,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("voyage: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings", e.baseURL)
	respBody, err := e.doRequestWithRetry(ctx, url, body)
	if err != nil {
		return nil, err
	}

	var result voyageEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("voyage: decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("voyage: API error (%s): %s", result.Error.Type, result.Error.Message)
	}

	// Voyage returns embeddings in order, but we'll use index to be safe
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	return embeddings, nil
}

func (e *VoyageEmbedder) doRequestWithRetry(ctx context.Context, url string, body []byte) ([]byte, error) {
	var lastErr error
	backoff := voyageMinBackoff

	for attempt := 0; attempt <= voyageMaxRetries; attempt++ {
		if attempt > 0 {
			// Add jitter to backoff
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff + jitter):
			}
			backoff *= 2
			if backoff > voyageMaxBackoff {
				backoff = voyageMaxBackoff
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("voyage: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.apiKey))

		resp, err := e.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("voyage: request failed: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("voyage: read response: %w", err)
			continue
		}

		// Retry on rate limit or server errors
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("voyage: status %d: %s", resp.StatusCode, string(respBody))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("voyage: status %d: %s", resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("voyage: max retries exceeded: %w", lastErr)
}

func (e *VoyageEmbedder) Dimension() int {
	e.dimOnce.Do(func() {
		emb, err := e.Embed(context.Background(), "dimension probe")
		if err == nil && len(emb) > 0 {
			e.dimension = len(emb)
		}
	})
	return e.dimension
}
