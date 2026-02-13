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
	geminiDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
	geminiDefaultModel   = "text-embedding-004"
	geminiHTTPTimeout    = 120 * time.Second
	geminiMaxRetries     = 5
	geminiMinBackoff     = 1 * time.Second
	geminiMaxBackoff     = 60 * time.Second
)

// GeminiEmbedder generates embeddings via Google's Gemini API.
type GeminiEmbedder struct {
	apiKey    string
	model     string
	baseURL   string
	client    *http.Client
	dimension int
	dimOnce   sync.Once
}

// NewGeminiEmbedder creates an embedder that calls the Gemini embedding API.
func NewGeminiEmbedder(apiKey, model string) *GeminiEmbedder {
	if model == "" {
		model = geminiDefaultModel
	}
	return &GeminiEmbedder{
		apiKey:  apiKey,
		model:   model,
		baseURL: geminiDefaultBaseURL,
		client: &http.Client{
			Timeout: geminiHTTPTimeout,
		},
	}
}

// geminiEmbedRequest is the request body for single embedding.
type geminiEmbedRequest struct {
	Content geminiContent `json:"content"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

// geminiEmbedResponse is the response from embedContent endpoint.
type geminiEmbedResponse struct {
	Embedding struct {
		Values []float64 `json:"values"`
	} `json:"embedding"`
	Error *geminiAPIError `json:"error,omitempty"`
}

// geminiBatchEmbedRequest is the request body for batch embedding.
type geminiBatchEmbedRequest struct {
	Requests []geminiBatchEmbedPart `json:"requests"`
}

type geminiBatchEmbedPart struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

// geminiBatchEmbedResponse is the response from batchEmbedContents endpoint.
type geminiBatchEmbedResponse struct {
	Embeddings []struct {
		Values []float64 `json:"values"`
	} `json:"embeddings"`
	Error *geminiAPIError `json:"error,omitempty"`
}

type geminiAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *GeminiEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("gemini: empty embedding response")
	}
	return results[0], nil
}

func (e *GeminiEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Use batch endpoint for multiple texts
	if len(texts) > 1 {
		return e.embedBatch(ctx, texts)
	}

	// Single text uses simple endpoint
	return e.embedSingle(ctx, texts[0])
}

func (e *GeminiEmbedder) embedSingle(ctx context.Context, text string) ([][]float32, error) {
	reqBody := geminiEmbedRequest{
		Content: geminiContent{
			Parts: []geminiPart{{Text: text}},
		},
	}

	url := fmt.Sprintf("%s/%s:embedContent?key=%s", e.baseURL, e.model, e.apiKey)
	respBody, err := e.doRequestWithRetry(ctx, url, reqBody)
	if err != nil {
		return nil, err
	}

	var result geminiEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("gemini: API error %d: %s", result.Error.Code, result.Error.Message)
	}

	return [][]float32{float64ToFloat32(result.Embedding.Values)}, nil
}

func (e *GeminiEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	requests := make([]geminiBatchEmbedPart, len(texts))
	for i, text := range texts {
		requests[i] = geminiBatchEmbedPart{
			Model: fmt.Sprintf("models/%s", e.model),
			Content: geminiContent{
				Parts: []geminiPart{{Text: text}},
			},
		}
	}

	reqBody := geminiBatchEmbedRequest{Requests: requests}
	url := fmt.Sprintf("%s/%s:batchEmbedContents?key=%s", e.baseURL, e.model, e.apiKey)

	respBody, err := e.doRequestWithRetry(ctx, url, reqBody)
	if err != nil {
		return nil, err
	}

	var result geminiBatchEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("gemini: decode batch response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("gemini: API error %d: %s", result.Error.Code, result.Error.Message)
	}

	embeddings := make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		embeddings[i] = float64ToFloat32(emb.Values)
	}

	return embeddings, nil
}

func (e *GeminiEmbedder) doRequestWithRetry(ctx context.Context, url string, reqBody any) ([]byte, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	var lastErr error
	backoff := geminiMinBackoff

	for attempt := 0; attempt <= geminiMaxRetries; attempt++ {
		if attempt > 0 {
			// Add jitter to backoff
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff + jitter):
			}
			backoff *= 2
			if backoff > geminiMaxBackoff {
				backoff = geminiMaxBackoff
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gemini: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gemini: request failed: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("gemini: read response: %w", err)
			continue
		}

		// Retry on rate limit or server errors
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("gemini: status %d: %s", resp.StatusCode, string(respBody))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gemini: status %d: %s", resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("gemini: max retries exceeded: %w", lastErr)
}

func (e *GeminiEmbedder) Dimension() int {
	e.dimOnce.Do(func() {
		emb, err := e.Embed(context.Background(), "dimension probe")
		if err == nil && len(emb) > 0 {
			e.dimension = len(emb)
		}
	})
	return e.dimension
}

// float64ToFloat32 converts a slice of float64 to float32.
func float64ToFloat32(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}
