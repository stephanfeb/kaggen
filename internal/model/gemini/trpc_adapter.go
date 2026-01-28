package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Adapter implements trpc-agent-go's model.Model interface for Google Gemini.
type Adapter struct {
	client *Client
}

// NewAdapter creates a new Gemini model adapter.
func NewAdapter(apiKey, modelName string) *Adapter {
	return &Adapter{
		client: NewClient(apiKey, modelName),
	}
}

// GenerateContent implements model.Model interface.
func (a *Adapter) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	responseChan := make(chan *model.Response, 1)

	go func() {
		defer close(responseChan)

		// Convert trpc model.Request to our API format
		apiReq := a.convertRequest(req)

		// Send request to Gemini API
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
		GenerationConfig: &apiGenerationConfig{
            MaxOutputTokens: 8192,
        },
	}

	// Set max tokens if specified
	if req.MaxTokens != nil {
		apiReq.GenerationConfig.MaxOutputTokens = *req.MaxTokens
	}

	// Build map of ToolID -> FunctionName from assistant messages
	toolIDToName := make(map[string]string)
	for _, msg := range req.Messages {
		if msg.Role == model.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				toolIDToName[tc.ID] = tc.Function.Name
			}
		}
	}

	var apiContents []apiContent
	var systemPrompt string

	for _, msg := range req.Messages {
		switch msg.Role {
		case model.RoleSystem:
			systemPrompt = msg.Content

		case model.RoleUser:
			parts := []apiPart{}
			if msg.Content != "" {
				parts = append(parts, apiPart{Text: msg.Content})
			}
			// Handle multimodal content parts
			for _, part := range msg.ContentParts {
				switch part.Type {
				case model.ContentTypeText:
					if part.Text != nil {
						parts = append(parts, apiPart{Text: *part.Text})
					}
				case model.ContentTypeImage:
					if part.Image != nil && len(part.Image.Data) > 0 {
						mediaType := imageFormatToMIME(part.Image.Format)
						parts = append(parts, apiPart{
							InlineData: &apiBlob{
								MimeType: mediaType,
								Data:     base64.StdEncoding.EncodeToString(part.Image.Data),
							},
						})
					}
				}
			}
			
			if len(parts) > 0 {
				apiContents = append(apiContents, apiContent{
					Role:  "user",
					Parts: parts,
				})
			}

		case model.RoleAssistant:
			parts := []apiPart{}
			if msg.Content != "" {
				parts = append(parts, apiPart{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				// Safely split ID to recover signature
				idParts := strings.SplitN(tc.ID, ":::", 2)
				var thoughtSig string
				if len(idParts) > 1 {
					thoughtSig = idParts[1]
				}

				// Thought signature is a sibling of functionCall, not inside it
				p := apiPart{
					FunctionCall: &apiFunctionCall{
						Name: tc.Function.Name,
						Args: json.RawMessage(tc.Function.Arguments),
					},
				}
				if thoughtSig != "" {
					p.ThoughtSignature = thoughtSig
				}
				parts = append(parts, p)
			}
			if len(parts) > 0 {
				apiContents = append(apiContents, apiContent{
					Role:  "model",
					Parts: parts,
				})
			}

		case model.RoleTool:
			// Tool results are sent as function parts
			// We need to resolve the function name using the tool ID
			funcName := toolIDToName[msg.ToolID]
			// If we can't find it, we might defaults to empty or log error. 
			// For robustness, if missing, we use "unknown" or similar, but Gemini might reject it.
			if funcName == "" {
				funcName = msg.ToolID // Fallback
			}

			// Parse content if it's JSON, otherwise treat as string
			var contentObj map[string]any
			if err := json.Unmarshal([]byte(msg.Content), &contentObj); err != nil {
				// Not a JSON object, wrap it
				contentObj = map[string]any{"content": string(msg.Content)}
			}
			
			// If msg.Content itself was a primitive (string/int) wrapped in json.RawMessage,
			// Unmarshal might behave differently. 
            // simpler approach:
            var finalResponse map[string]any
            err := json.Unmarshal([]byte(msg.Content), &finalResponse)
            if err != nil {
                 finalResponse = map[string]any{"result": string(msg.Content)}
            }


			apiContents = append(apiContents, apiContent{
				Role: "function",
				Parts: []apiPart{
					{
						FunctionResponse: &apiFunctionResponse{
							Name:     funcName,
							Response: finalResponse,
						},
					},
				},
			})
		}
	}

	if systemPrompt != "" {
		apiReq.SystemInstruction = &apiContent{
			Parts: []apiPart{{Text: systemPrompt}},
		}
	}
	
	apiReq.Contents = apiContents

	// Convert tools if present
	if len(req.Tools) > 0 {
		var funcDecls []apiFunctionDeclaration
		for _, t := range req.Tools {
			decl := t.Declaration()
			funcDecls = append(funcDecls, apiFunctionDeclaration{
				Name:        decl.Name,
				Description: decl.Description,
				Parameters:  schemaToMap(decl.InputSchema),
			})
		}
		apiReq.Tools = []apiTool{{FunctionDeclarations: funcDecls}}
	}

	return apiReq
}

// convertResponse converts an API response to a trpc model.Response.
func (a *Adapter) convertResponse(resp *apiResponse) *model.Response {
	modelResp := &model.Response{
		ID:        uuid.New().String(), // Gemini doesn't provide a single response ID
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Done:      true,
		Timestamp: time.Now(),
	}
	
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		
		choice := model.Choice{
			Index: candidate.Index,
			Message: model.Message{
				Role: model.RoleAssistant,
			},
		}

		var textContent string
		var toolCalls []model.ToolCall

		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				textContent += part.Text
			}
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)

				// Build tool call ID - embed thought signature if present
				tcID := uuid.New().String()
				if part.ThoughtSignature != "" {
					tcID = tcID + ":::" + part.ThoughtSignature
				}

				toolCalls = append(toolCalls, model.ToolCall{
					ID:   tcID,
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      part.FunctionCall.Name,
						Arguments: args,
					},
				})
			}
		}

		choice.Message.Content = textContent
		choice.Message.ToolCalls = toolCalls
		
		finishReason := mapFinishReason(candidate.FinishReason)
		choice.FinishReason = &finishReason
		
		modelResp.Choices = []model.Choice{choice}
	}

	// Usage
	if resp.UsageMetadata != nil {
		modelResp.Usage = &model.Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		}
	}

	return modelResp
}

func mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	case "OTHER":
		return "stop"
	default:
		return reason
	}
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
	// NOTE: Gemini does NOT support additionalProperties in tool schemas.
	// Omitting it entirely to avoid "Unknown name" errors.
    
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}

	return result
}
