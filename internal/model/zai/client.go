// Package zai implements the model adapter for Z.AI's GLM models.
package zai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.z.ai/api/paas/v4"

// Client is an HTTP client for the Z.AI chat completions API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	model      string
}

// NewClient creates a new Z.AI API client.
func NewClient(apiKey, model string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		model: model,
	}
}

// --- API request types ---

type apiRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Tools       []apiTool    `json:"tools,omitempty"`
	ToolChoice  string       `json:"tool_choice,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	Stream      bool         `json:"stream"`
}

type apiMessage struct {
	Role         string        `json:"role"` // system, user, assistant, tool
	Content      string        `json:"content,omitempty"`
	ToolCalls    []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"` // for role=tool
}

type apiTool struct {
	Type     string      `json:"type"` // "function"
	Function apiFunction `json:"function"`
}

type apiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type apiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // "function"
	Function apiToolCallFunc `json:"function"`
}

type apiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// --- API response types ---

type apiResponse struct {
	ID      string     `json:"id"`
	Created int64      `json:"created"`
	Model   string     `json:"model"`
	Choices []apiChoice `json:"choices"`
	Usage   *apiUsage  `json:"usage,omitempty"`
}

type apiChoice struct {
	Index        int              `json:"index"`
	Message      apiChoiceMessage `json:"message"`
	FinishReason string           `json:"finish_reason"`
}

type apiChoiceMessage struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []apiToolCall `json:"tool_calls,omitempty"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// sendAPIRequest sends a chat completion request and returns the parsed response.
func (c *Client) sendAPIRequest(ctx context.Context, req *apiRequest) (*apiResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr apiErrorResponse
		if err := json.Unmarshal(body, &apiErr); err != nil {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("API error %d: %s", apiErr.Code, apiErr.Message)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &apiResp, nil
}
