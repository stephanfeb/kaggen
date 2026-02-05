// Package gemini implements the Google Gemini API client.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yourusername/kaggen/pkg/protocol"
)

const (
	defaultBaseURL     = "https://generativelanguage.googleapis.com/v1beta/models"
	defaultHTTPTimeout = 120 * time.Second
)

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
func (c *Client) sendAPIRequest(ctx context.Context, req *apiRequest) (*apiResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)

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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &apiResp, nil
}
