package zai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Adapter implements trpc-agent-go's model.Model interface for Z.AI GLM models.
type Adapter struct {
	client *Client
}

// NewAdapter creates a new Z.AI model adapter.
func NewAdapter(apiKey, modelName string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, modelName),
	}
}

// GenerateContent implements model.Model.
func (a *Adapter) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	responseChan := make(chan *model.Response, 1)

	go func() {
		defer close(responseChan)

		apiReq := a.convertRequest(req)

		resp, err := a.client.sendAPIRequest(ctx, apiReq)
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

		responseChan <- a.convertResponse(resp)
	}()

	return responseChan, nil
}

// Info implements model.Model.
func (a *Adapter) Info() model.Info {
	return model.Info{Name: a.client.model}
}

// convertRequest converts a trpc model.Request to a Z.AI API request.
func (a *Adapter) convertRequest(req *model.Request) *apiRequest {
	apiReq := &apiRequest{
		Model:     a.client.model,
		MaxTokens: 4096,
		Stream:    false,
	}

	if req.MaxTokens != nil {
		apiReq.MaxTokens = *req.MaxTokens
	}

	// Convert messages — ZAI uses standard OpenAI roles directly.
	var messages []apiMessage
	for _, msg := range req.Messages {
		switch msg.Role {
		case model.RoleSystem:
			messages = append(messages, apiMessage{
				Role:    "system",
				Content: msg.Content,
			})

		case model.RoleUser:
			content := msg.Content
			// For multimodal, fall back to text parts only.
			if content == "" {
				for _, part := range msg.ContentParts {
					if part.Type == model.ContentTypeText && part.Text != nil {
						content += *part.Text
					}
				}
			}
			if content != "" {
				messages = append(messages, apiMessage{
					Role:    "user",
					Content: content,
				})
			}

		case model.RoleAssistant:
			am := apiMessage{
				Role:    "assistant",
				Content: msg.Content,
			}
			for _, tc := range msg.ToolCalls {
				am.ToolCalls = append(am.ToolCalls, apiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: apiToolCallFunc{
						Name:      tc.Function.Name,
						Arguments: string(tc.Function.Arguments),
					},
				})
			}
			messages = append(messages, am)

		case model.RoleTool:
			messages = append(messages, apiMessage{
				Role:       "tool",
				Content:    msg.Content,
				ToolCallID: msg.ToolID,
			})
		}
	}
	apiReq.Messages = messages

	// Convert tools.
	if len(req.Tools) > 0 {
		apiReq.ToolChoice = "auto"
		for _, t := range req.Tools {
			decl := t.Declaration()
			apiReq.Tools = append(apiReq.Tools, apiTool{
				Type: "function",
				Function: apiFunction{
					Name:        decl.Name,
					Description: decl.Description,
					Parameters:  schemaToMap(decl.InputSchema),
				},
			})
		}
	}

	return apiReq
}

// convertResponse converts a Z.AI API response to a trpc model.Response.
func (a *Adapter) convertResponse(resp *apiResponse) *model.Response {
	modelResp := &model.Response{
		ID:        resp.ID,
		Object:    model.ObjectTypeChatCompletion,
		Created:   resp.Created,
		Model:     resp.Model,
		Done:      true,
		Timestamp: time.Now(),
	}

	if len(resp.Choices) > 0 {
		c := resp.Choices[0]

		choice := model.Choice{
			Index: c.Index,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: c.Message.Content,
			},
		}

		// Map tool calls.
		for _, tc := range c.Message.ToolCalls {
			// Arguments come as a JSON string; convert to []byte.
			args := []byte(tc.Function.Arguments)
			// Validate it's valid JSON; if not, wrap as string.
			if !json.Valid(args) {
				args, _ = json.Marshal(tc.Function.Arguments)
			}
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, model.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      tc.Function.Name,
					Arguments: args,
				},
			})
		}

		finishReason := mapFinishReason(c.FinishReason)
		choice.FinishReason = &finishReason

		modelResp.Choices = []model.Choice{choice}
	}

	if resp.Usage != nil {
		modelResp.Usage = &model.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return modelResp
}

func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "tool_calls":
		return "tool_calls"
	case "length":
		return "length"
	case "sensitive":
		return "content_filter"
	case "network_error":
		return "stop"
	default:
		return reason
	}
}

func schemaToMap(schema *tool.Schema) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object"}
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
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}

	return result
}
