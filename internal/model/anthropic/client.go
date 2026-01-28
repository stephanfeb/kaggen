// Package anthropic implements the Anthropic Claude API client.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/yourusername/kaggen/pkg/protocol"
)

const (
	defaultAPIURL = "https://api.anthropic.com/v1/messages"
	apiVersion    = "2023-06-01"
)

// Client implements the Model interface for Anthropic's Claude API.
type Client struct {
	apiKey     string
	apiURL     string
	httpClient *http.Client
	model      string
}

// NewClient creates a new Anthropic API client.
func NewClient(apiKey, model string) *Client {
	return &Client{
		apiKey:     apiKey,
		apiURL:     defaultAPIURL,
		httpClient: &http.Client{},
		model:      model,
	}
}

// apiMessage represents a message in the Anthropic API format.
type apiMessage struct {
	Role    string       `json:"role"`
	Content []apiContent `json:"content"`
}

// apiContent represents content in the Anthropic API format.
type apiContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     map[string]any  `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *apiSource      `json:"source,omitempty"`
}

// MarshalJSON implements custom JSON marshaling to ensure tool_use blocks always
// include the input field, even when empty (Anthropic API requires this).
func (c apiContent) MarshalJSON() ([]byte, error) {
	type plain apiContent // avoid recursion
	if c.Type == "tool_use" {
		// Build a map manually to ensure input is always present
		m := map[string]any{
			"type": c.Type,
			"id":   c.ID,
			"name": c.Name,
		}
		if c.Input != nil {
			m["input"] = c.Input
		} else {
			m["input"] = map[string]any{}
		}
		return json.Marshal(m)
	}
	return json.Marshal(plain(c))
}

// ContentString returns the Content field as a string.
// For tool_result blocks, Content is a JSON string; for web_search_tool_result
// blocks it's an array. This method handles both cases.
func (c *apiContent) ContentString() string {
	if len(c.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(c.Content, &s); err == nil {
		return s
	}
	// Not a string (e.g. array from web_search_tool_result); return raw JSON.
	return string(c.Content)
}

// apiSource represents an image source for the Anthropic Vision API.
type apiSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// apiRequest represents a request to the Anthropic API.
type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []apiMessage `json:"messages"`
	Tools     []any        `json:"tools,omitempty"`
}

// apiTool represents a function tool definition in the Anthropic API format.
type apiTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// apiServerTool represents a server-managed tool (e.g. web_search_20250305).
type apiServerTool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses,omitempty"`
}

// apiResponse represents a response from the Anthropic API.
type apiResponse struct {
	ID           string       `json:"id"`
	Type         string       `json:"type"`
	Role         string       `json:"role"`
	Content      []apiContent `json:"content"`
	Model        string       `json:"model"`
	StopReason   string       `json:"stop_reason"`
	StopSequence string       `json:"stop_sequence"`
	Usage        apiUsage     `json:"usage"`
}

// apiUsage represents token usage information.
type apiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// apiError represents an error response from the API.
type apiError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Generate sends messages to Claude and returns a response.
func (c *Client) Generate(ctx context.Context, messages []protocol.Message, tools []protocol.ToolDef) (*protocol.Response, error) {
	// Extract system message and convert messages to API format
	systemPrompt, apiMessages := c.convertMessages(messages)

	// Convert tools to API format
	var apiTools []any
	for _, t := range tools {
		apiTools = append(apiTools, apiTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	// Build request
	req := &apiRequest{
		Model:     c.model,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages:  apiMessages,
	}
	if len(apiTools) > 0 {
		req.Tools = apiTools
	}

	// Send request using shared method
	apiResp, err := c.sendAPIRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert response to protocol format
	return c.convertResponse(apiResp), nil
}

// convertMessages converts protocol messages to API format.
// Returns the system prompt (if any) and the converted messages.
func (c *Client) convertMessages(messages []protocol.Message) (string, []apiMessage) {
	var systemPrompt string
	var apiMessages []apiMessage

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemPrompt = msg.Content

		case "user":
			apiMessages = append(apiMessages, apiMessage{
				Role: "user",
				Content: []apiContent{
					{Type: "text", Text: msg.Content},
				},
			})

		case "assistant":
			content := []apiContent{}
			if msg.Content != "" {
				content = append(content, apiContent{Type: "text", Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				input := tc.Input
				if input == nil {
					input = map[string]any{}
				}
				content = append(content, apiContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			apiMessages = append(apiMessages, apiMessage{
				Role:    "assistant",
				Content: content,
			})

		case "tool":
			if msg.ToolResult != nil {
				rawContent, _ := json.Marshal(msg.ToolResult.Output)
				apiMessages = append(apiMessages, apiMessage{
					Role: "user",
					Content: []apiContent{
						{
							Type:      "tool_result",
							ToolUseID: msg.ToolResult.ToolCallID,
							Content:   rawContent,
							IsError:   msg.ToolResult.IsError,
						},
					},
				})
			}
		}
	}

	return systemPrompt, apiMessages
}

// convertResponse converts an API response to protocol format.
func (c *Client) convertResponse(apiResp *apiResponse) *protocol.Response {
	resp := &protocol.Response{
		StopReason: apiResp.StopReason,
	}

	for _, content := range apiResp.Content {
		switch content.Type {
		case "text":
			resp.Content = content.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, protocol.ToolCall{
				ID:    content.ID,
				Name:  content.Name,
				Input: content.Input,
			})
		}
	}

	return resp
}

// sendAPIRequest sends a request to the Anthropic API and returns the raw response.
// This is used by both the legacy Generate method and the new trpc adapter.
func (c *Client) sendAPIRequest(ctx context.Context, req *apiRequest) (*apiResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	// Send request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Handle error responses
	if resp.StatusCode != http.StatusOK {
		var apiErr apiError
		if err := json.Unmarshal(body, &apiErr); err != nil {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("API error: %s - %s", apiErr.Error.Type, apiErr.Error.Message)
	}

	// Parse response
	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &apiResp, nil
}
