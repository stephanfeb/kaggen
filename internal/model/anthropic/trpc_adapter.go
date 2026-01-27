// Package anthropic implements the Anthropic Claude API client and trpc-agent-go adapter.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Adapter implements trpc-agent-go's model.Model interface for Anthropic Claude.
type Adapter struct {
	client *Client
}

// NewAdapter creates a new Anthropic model adapter.
func NewAdapter(apiKey, modelName string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, modelName),
	}
}

// GenerateContent implements model.Model interface.
// It generates content from the given request and returns a channel of responses.
func (a *Adapter) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	responseChan := make(chan *model.Response, 1)

	go func() {
		defer close(responseChan)

		// Convert trpc model.Request to our API format
		apiReq := a.convertRequest(req)

		// Send request to Anthropic API
		resp, err := a.sendRequest(ctx, apiReq)
		if err != nil {
			responseChan <- &model.Response{
				ID:        uuid.New().String(),
				Object:    model.ObjectTypeError,
				Created:   time.Now().Unix(),
				Done:      true,
				Timestamp: time.Now(),
				Error: &model.ResponseError{
					Type:    model.ErrorTypeAPIError,
					Message: err.Error(),
				},
			}
			return
		}

		// Convert API response to model.Response
		modelResp := a.convertResponse(resp)
		responseChan <- modelResp
	}()

	return responseChan, nil
}

// Info returns basic information about the model.
func (a *Adapter) Info() model.Info {
	return model.Info{
		Name: a.client.model,
	}
}

// convertRequest converts a trpc model.Request to our internal API request format.
func (a *Adapter) convertRequest(req *model.Request) *apiRequest {
	apiReq := &apiRequest{
		Model:     a.client.model,
		MaxTokens: 4096,
	}

	// Set max tokens if specified
	if req.MaxTokens != nil {
		apiReq.MaxTokens = *req.MaxTokens
	}

	// Convert messages
	var systemPrompt string
	var apiMessages []apiMessage

	for _, msg := range req.Messages {
		switch msg.Role {
		case model.RoleSystem:
			systemPrompt = msg.Content

		case model.RoleUser:
			content := []apiContent{}
			if msg.Content != "" {
				content = append(content, apiContent{Type: "text", Text: msg.Content})
			}
			// Handle multimodal content parts
			for _, part := range msg.ContentParts {
				switch part.Type {
				case model.ContentTypeText:
					if part.Text != nil {
						content = append(content, apiContent{Type: "text", Text: *part.Text})
					}
				case model.ContentTypeImage:
					if part.Image != nil && len(part.Image.Data) > 0 {
						mediaType := imageFormatToMIME(part.Image.Format)
						content = append(content, apiContent{
							Type: "image",
							Source: &apiSource{
								Type:      "base64",
								MediaType: mediaType,
								Data:      base64.StdEncoding.EncodeToString(part.Image.Data),
							},
						})
					} else if part.Image != nil && part.Image.URL != "" {
						content = append(content, apiContent{
							Type: "image",
							Source: &apiSource{
								Type: "url",
								URL:  part.Image.URL,
							},
						})
					}
				}
			}
			if len(content) > 0 {
				apiMessages = append(apiMessages, apiMessage{
					Role:    "user",
					Content: content,
				})
			}

		case model.RoleAssistant:
			content := []apiContent{}
			if msg.Content != "" {
				content = append(content, apiContent{Type: "text", Text: msg.Content})
			}
			// Handle tool calls in assistant messages
			for _, tc := range msg.ToolCalls {
				content = append(content, apiContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: argsToMap(tc.Function.Arguments),
				})
			}
			if len(content) > 0 {
				apiMessages = append(apiMessages, apiMessage{
					Role:    "assistant",
					Content: content,
				})
			}

		case model.RoleTool:
			// Tool results are sent as user messages with tool_result type
			apiMessages = append(apiMessages, apiMessage{
				Role: "user",
				Content: []apiContent{
					{
						Type:      "tool_result",
						ToolUseID: msg.ToolID,
						Content:   msg.Content,
					},
				},
			})
		}
	}

	apiReq.System = systemPrompt
	apiReq.Messages = apiMessages

	// Convert tools if present
	if len(req.Tools) > 0 {
		apiReq.Tools = make([]apiTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			decl := t.Declaration()
			apiReq.Tools = append(apiReq.Tools, apiTool{
				Name:        decl.Name,
				Description: decl.Description,
				InputSchema: schemaToMap(decl.InputSchema),
			})
		}
	}

	return apiReq
}

// sendRequest sends the API request and returns the response.
func (a *Adapter) sendRequest(ctx context.Context, req *apiRequest) (*apiResponse, error) {
	// Use the existing client's HTTP machinery
	return a.client.sendAPIRequest(ctx, req)
}

// convertResponse converts an API response to a trpc model.Response.
func (a *Adapter) convertResponse(resp *apiResponse) *model.Response {
	modelResp := &model.Response{
		ID:        resp.ID,
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Model:     resp.Model,
		Done:      true,
		Timestamp: time.Now(),
	}

	// Convert content to choices
	if len(resp.Content) > 0 {
		choice := model.Choice{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
			},
		}

		var textContent string
		var toolCalls []model.ToolCall

		for _, content := range resp.Content {
			switch content.Type {
			case "text":
				textContent = content.Text
			case "tool_use":
				args, _ := json.Marshal(content.Input)
				toolCalls = append(toolCalls, model.ToolCall{
					ID:   content.ID,
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      content.Name,
						Arguments: args,
					},
				})
			}
		}

		choice.Message.Content = textContent
		choice.Message.ToolCalls = toolCalls

		// Set finish reason
		finishReason := mapStopReason(resp.StopReason)
		choice.FinishReason = &finishReason

		modelResp.Choices = []model.Choice{choice}
	}

	// Convert usage
	modelResp.Usage = &model.Usage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}

	return modelResp
}

// mapStopReason maps Anthropic stop reasons to OpenAI-compatible ones.
func mapStopReason(stopReason string) string {
	switch stopReason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return stopReason
	}
}

// argsToMap converts JSON arguments bytes to map.
func argsToMap(args []byte) map[string]any {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	return m
}

// imageFormatToMIME maps image format strings to MIME types.
func imageFormatToMIME(format string) string {
	switch format {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

// schemaToMap converts a tool.Schema to a map for the API.
func schemaToMap(schema *tool.Schema) map[string]any {
	if schema == nil {
		return map[string]any{
			"type": "object",
		}
	}

	result := make(map[string]any)

	if schema.Type != "" {
		result["type"] = schema.Type
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}
	if len(schema.Properties) > 0 {
		props := make(map[string]any)
		for name, prop := range schema.Properties {
			props[name] = schemaToMap(prop)
		}
		result["properties"] = props
	}
	if schema.Items != nil {
		result["items"] = schemaToMap(schema.Items)
	}
	if schema.AdditionalProperties != nil {
		result["additionalProperties"] = schema.AdditionalProperties
	}
	if schema.Default != nil {
		result["default"] = schema.Default
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}

	return result
}
