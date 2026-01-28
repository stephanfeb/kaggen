// Package protocol defines shared types for agent communication.
package protocol

// Message represents a conversation message.
type Message struct {
	Role       string       `json:"role"` // user, assistant, tool
	Content    string       `json:"content"`
	ToolCalls  []ToolCall   `json:"tool_calls,omitempty"`
	ToolResult *ToolResult  `json:"tool_result,omitempty"`
}

// ToolCall represents a request to execute a tool.
type ToolCall struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Input          map[string]any `json:"input"`
	ThoughtSignature string       `json:"thought_signature,omitempty"` // Gemini thought signature for function calls
}

// ToolResult represents the output of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
	IsError    bool   `json:"is_error,omitempty"`
}

// ToolDef defines a tool's schema for the model.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"input_schema"`
}

// Response represents a model's response.
type Response struct {
	Content    string
	ToolCalls  []ToolCall
	StopReason string // "end_turn", "tool_use"
}
