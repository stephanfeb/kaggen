// Package gemini implements the Google Gemini API client.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/yourusername/kaggen/pkg/protocol"
)

const (
	defaultBaseURL     = "https://generativelanguage.googleapis.com/v1beta/models"
	defaultHTTPTimeout = 120 * time.Second
	defaultMaxRetries  = 5
	defaultMinBackoff  = 1 * time.Second
	defaultMaxBackoff  = 60 * time.Second
)

// apiErrorResponse represents an error response from the Gemini API.
type apiErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Type       string `json:"@type"`
			RetryDelay string `json:"retryDelay,omitempty"`
		} `json:"details"`
	} `json:"error"`
}

// Client implements the Model interface for Google's Gemini API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	model      string
}

// NewClient creates a new Gemini API client.
func NewClient(apiKey, model string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		model: model,
	}
}

// apiPart represents a content part in the Gemini API format.
type apiPart struct {
	Text             string               `json:"text,omitempty"`
	InlineData       *apiBlob             `json:"inlineData,omitempty"`
	FunctionCall     *apiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *apiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string               `json:"thoughtSignature,omitempty"` // Sibling of functionCall, not inside it
}

type apiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

type apiFunctionCall struct {
	Name             string          `json:"name"`
	Args             json.RawMessage `json:"args,omitempty"`
	Thought          string          `json:"thought_signature,omitempty"` // Per docs: thought_signature
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`  // Keep as fallback just in case
}

type apiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// apiContent represents content in the Gemini API format.
type apiContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []apiPart `json:"parts"`
}

// apiTool represents a function tool definition in the Gemini API format.
type apiTool struct {
	FunctionDeclarations []apiFunctionDeclaration `json:"function_declarations,omitempty"`
}

type apiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// apiRequest represents a request to the Gemini API.
type apiRequest struct {
	Contents          []apiContent         `json:"contents"`
	Tools             []apiTool            `json:"tools,omitempty"`
	SystemInstruction *apiContent          `json:"system_instruction,omitempty"`
	GenerationConfig  *apiGenerationConfig `json:"generationConfig,omitempty"`
}

type apiGenerationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
}

// apiResponse represents a response from the Gemini API.
type apiResponse struct {
	Candidates    []apiCandidate    `json:"candidates"`
	UsageMetadata *apiUsageMetadata `json:"usageMetadata,omitempty"`
}

type apiCandidate struct {
	Content      apiContent `json:"content"`
	FinishReason string     `json:"finishReason"`
	Index        int        `json:"index"`
}

type apiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// Generate sends messages to Gemini and returns a response.
func (c *Client) Generate(ctx context.Context, messages []protocol.Message, tools []protocol.ToolDef) (*protocol.Response, error) {
	// Extract system message and convert messages to API format
	systemPrompt, apiContents := c.convertMessages(messages)

	// Build request
	temp := 0.7
	req := &apiRequest{
		Contents: apiContents,
		GenerationConfig: &apiGenerationConfig{
			MaxOutputTokens: 8192,
			Temperature:     &temp,
		},
	}

	if systemPrompt != "" {
		req.SystemInstruction = &apiContent{
			Parts: []apiPart{{Text: systemPrompt}},
		}
	}

	// Convert tools to API format
	if len(tools) > 0 {
		var funcDecls []apiFunctionDeclaration
		for _, t := range tools {
			funcDecls = append(funcDecls, apiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		req.Tools = []apiTool{{FunctionDeclarations: funcDecls}}
	}

	// Send request
	apiResp, err := c.sendAPIRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert response to protocol format
	return c.convertResponse(apiResp), nil
}

// mapToRawMessage helper for json.RawMessage
func mapToRawMessage(args map[string]any) json.RawMessage {
	b, _ := json.Marshal(args)
	return json.RawMessage(b)
}

// convertMessages converts protocol messages to Gemini API format.
func (c *Client) convertMessages(messages []protocol.Message) (string, []apiContent) {
	var systemPrompt string
	var apiContents []apiContent

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemPrompt = msg.Content

		case "user":
			content := apiContent{Role: "user", Parts: []apiPart{{Text: msg.Content}}}
			apiContents = append(apiContents, content)

		case "assistant":
			var parts []apiPart
			if msg.Content != "" {
				parts = append(parts, apiPart{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				p := apiPart{
					FunctionCall: &apiFunctionCall{
						Name: tc.Name,
						Args: mapToRawMessage(tc.Input),
					},
				}
				// Thought signature is a sibling of functionCall, not inside it
				if tc.ThoughtSignature != "" {
					p.ThoughtSignature = tc.ThoughtSignature
				}
				parts = append(parts, p)
			}
			apiContents = append(apiContents, apiContent{Role: "model", Parts: parts})

		case "tool":
			if msg.ToolResult != nil {
				// Find function name from previous tool calls
				var funcName string
				for _, m := range messages {
					for _, tc := range m.ToolCalls {
						if tc.ID == msg.ToolResult.ToolCallID {
							funcName = tc.Name
							break
						}
					}
					if funcName != "" {
						break
					}
				}
				if funcName == "" {
					funcName = msg.ToolResult.ToolCallID // Fallback
				}

				apiContents = append(apiContents, apiContent{
					Role: "function",
					Parts: []apiPart{
						{
							FunctionResponse: &apiFunctionResponse{
								Name:     funcName,
								Response: map[string]any{"content": msg.ToolResult.Output},
							},
						},
					},
				})
			}
		}
	}

	return systemPrompt, apiContents
}

// convertResponse converts an API response to protocol format.
func (c *Client) convertResponse(apiResp *apiResponse) *protocol.Response {
	resp := &protocol.Response{}

	if len(apiResp.Candidates) == 0 {
		return resp
	}

	candidate := apiResp.Candidates[0]
	resp.StopReason = candidate.FinishReason

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			resp.Content += part.Text
		}
		if part.FunctionCall != nil {
			var input map[string]any
			if len(part.FunctionCall.Args) > 0 {
				_ = json.Unmarshal(part.FunctionCall.Args, &input)
			}

			// Thought signature is a sibling of functionCall in the part, not inside it
			resp.ToolCalls = append(resp.ToolCalls, protocol.ToolCall{
				ID:               part.FunctionCall.Name,
				Name:             part.FunctionCall.Name,
				Input:            input,
				ThoughtSignature: part.ThoughtSignature,
			})
		}
	}

	return resp
}

// sendAPIRequest sends a request to the Gemini API and returns the raw response.
// It automatically retries on 429 rate limit errors with exponential backoff.
func (c *Client) sendAPIRequest(ctx context.Context, req *apiRequest) (*apiResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)

	var lastErr error
	for attempt := 0; attempt <= defaultMaxRetries; attempt++ {
		// Check context before each attempt
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Create HTTP request
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		// Send request
		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		// Success
		if resp.StatusCode == http.StatusOK {
			var apiResp apiResponse
			if err := json.Unmarshal(body, &apiResp); err != nil {
				return nil, fmt.Errorf("unmarshal response: %w", err)
			}
			return &apiResp, nil
		}

		// Rate limit - retry with backoff
		if resp.StatusCode == http.StatusTooManyRequests {
			backoff := calculateBackoff(body, attempt)
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))

			if attempt < defaultMaxRetries {
				slog.Info("gemini rate limit hit, retrying",
					"attempt", attempt+1,
					"max_retries", defaultMaxRetries,
					"backoff", backoff.String(),
					"model", c.model)

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
		}

		// Non-retryable error
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("exceeded max retries (%d): %w", defaultMaxRetries, lastErr)
}

// calculateBackoff determines the backoff duration for a retry attempt.
// It first tries to parse the retryDelay from Gemini's error response,
// falling back to exponential backoff if not available.
func calculateBackoff(body []byte, attempt int) time.Duration {
	// Try to parse Gemini's suggested retry delay
	var errResp apiErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil {
		for _, detail := range errResp.Error.Details {
			if detail.RetryDelay != "" {
				if d := parseRetryDelay(detail.RetryDelay); d > 0 {
					// Add a small buffer to the suggested delay
					return d + 500*time.Millisecond
				}
			}
		}
	}

	// Fallback to exponential backoff: 1s, 2s, 4s, 8s, 16s, capped at 60s
	backoff := defaultMinBackoff * time.Duration(1<<uint(attempt))
	if backoff > defaultMaxBackoff {
		backoff = defaultMaxBackoff
	}
	return backoff
}

// parseRetryDelay parses a duration string like "3s" or "3.475708169s".
func parseRetryDelay(s string) time.Duration {
	// Try standard duration format first
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	// Try to extract seconds as a float (e.g., "3.475708169s")
	re := regexp.MustCompile(`^(\d+\.?\d*)s$`)
	if matches := re.FindStringSubmatch(s); len(matches) == 2 {
		if secs, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return time.Duration(secs * float64(time.Second))
		}
	}

	return 0
}
