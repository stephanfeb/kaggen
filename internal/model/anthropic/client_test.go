package anthropic

import (
	"testing"

	"github.com/yourusername/kaggen/pkg/protocol"
)

func TestConvertMessages(t *testing.T) {
	client := NewClient("test-key", "claude-3-sonnet")

	messages := []protocol.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
	}

	systemPrompt, apiMessages := client.convertMessages(messages)

	if systemPrompt != "You are a helpful assistant." {
		t.Errorf("expected system prompt %q, got %q", "You are a helpful assistant.", systemPrompt)
	}

	if len(apiMessages) != 3 {
		t.Fatalf("expected 3 API messages, got %d", len(apiMessages))
	}

	// Verify first message (user)
	if apiMessages[0].Role != "user" {
		t.Errorf("expected first message role 'user', got %q", apiMessages[0].Role)
	}
	if len(apiMessages[0].Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(apiMessages[0].Content))
	}
	if apiMessages[0].Content[0].Text != "Hello" {
		t.Errorf("expected text %q, got %q", "Hello", apiMessages[0].Content[0].Text)
	}
}

func TestConvertMessages_ToolCalls(t *testing.T) {
	client := NewClient("test-key", "claude-3-sonnet")

	messages := []protocol.Message{
		{Role: "user", Content: "Read the file"},
		{
			Role:    "assistant",
			Content: "I'll read that for you.",
			ToolCalls: []protocol.ToolCall{
				{
					ID:    "call_123",
					Name:  "read",
					Input: map[string]any{"path": "/tmp/test.txt"},
				},
			},
		},
		{
			Role: "tool",
			ToolResult: &protocol.ToolResult{
				ToolCallID: "call_123",
				Output:     "File contents here",
				IsError:    false,
			},
		},
	}

	_, apiMessages := client.convertMessages(messages)

	if len(apiMessages) != 3 {
		t.Fatalf("expected 3 API messages, got %d", len(apiMessages))
	}

	// Check assistant message with tool call
	assistantMsg := apiMessages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", assistantMsg.Role)
	}
	if len(assistantMsg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(assistantMsg.Content))
	}
	if assistantMsg.Content[1].Type != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", assistantMsg.Content[1].Type)
	}
	if assistantMsg.Content[1].ID != "call_123" {
		t.Errorf("expected ID 'call_123', got %q", assistantMsg.Content[1].ID)
	}

	// Check tool result
	toolMsg := apiMessages[2]
	if toolMsg.Role != "user" {
		t.Errorf("expected role 'user' for tool result, got %q", toolMsg.Role)
	}
	if len(toolMsg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(toolMsg.Content))
	}
	if toolMsg.Content[0].Type != "tool_result" {
		t.Errorf("expected type 'tool_result', got %q", toolMsg.Content[0].Type)
	}
}

func TestConvertResponse(t *testing.T) {
	client := NewClient("test-key", "claude-3-sonnet")

	apiResp := &apiResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "end_turn",
		Content: []apiContent{
			{Type: "text", Text: "Hello! How can I help you?"},
		},
	}

	resp := client.convertResponse(apiResp)

	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("expected content %q, got %q", "Hello! How can I help you?", resp.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", resp.StopReason)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestConvertResponse_ToolUse(t *testing.T) {
	client := NewClient("test-key", "claude-3-sonnet")

	apiResp := &apiResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []apiContent{
			{Type: "text", Text: "Let me check that file."},
			{
				Type:  "tool_use",
				ID:    "call_456",
				Name:  "read",
				Input: map[string]any{"path": "/tmp/file.txt"},
			},
		},
	}

	resp := client.convertResponse(apiResp)

	if resp.Content != "Let me check that file." {
		t.Errorf("expected content %q, got %q", "Let me check that file.", resp.Content)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %q", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}

	tc := resp.ToolCalls[0]
	if tc.ID != "call_456" {
		t.Errorf("expected ID 'call_456', got %q", tc.ID)
	}
	if tc.Name != "read" {
		t.Errorf("expected name 'read', got %q", tc.Name)
	}
}
