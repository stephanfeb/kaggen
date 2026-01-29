package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAdapterConvertRequest_BasicMessage(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "You are helpful."},
			{Role: model.RoleUser, Content: "Hello"},
		},
	}

	apiReq := adapter.convertRequest(req)

	if apiReq.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("expected model claude-3-5-sonnet-20241022, got %s", apiReq.Model)
	}
	if apiReq.System != "You are helpful." {
		t.Errorf("expected system prompt, got %q", apiReq.System)
	}
	if apiReq.MaxTokens != 16384 {
		t.Errorf("expected default max tokens 16384, got %d", apiReq.MaxTokens)
	}
	if len(apiReq.Messages) != 1 {
		t.Fatalf("expected 1 message (user only), got %d", len(apiReq.Messages))
	}
	if apiReq.Messages[0].Role != "user" {
		t.Errorf("expected user role, got %s", apiReq.Messages[0].Role)
	}
}

func TestAdapterConvertRequest_MaxTokens(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")

	maxTokens := 8192
	req := &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, Content: "Hello"}},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
		},
	}

	apiReq := adapter.convertRequest(req)
	if apiReq.MaxTokens != 8192 {
		t.Errorf("expected max tokens 8192, got %d", apiReq.MaxTokens)
	}
}

func TestAdapterConvertRequest_ToolCalls(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")

	args, _ := json.Marshal(map[string]any{"path": "/tmp/test.txt"})
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Read that file"},
			{
				Role:    model.RoleAssistant,
				Content: "",
				ToolCalls: []model.ToolCall{
					{
						ID:   "tc_123",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "read_file",
							Arguments: args,
						},
					},
				},
			},
			{
				Role:    model.RoleTool,
				ToolID:  "tc_123",
				Content: "file contents here",
			},
		},
	}

	apiReq := adapter.convertRequest(req)

	if len(apiReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(apiReq.Messages))
	}

	// Assistant message should have tool_use content
	assistantMsg := apiReq.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected assistant role, got %s", assistantMsg.Role)
	}
	if len(assistantMsg.Content) != 1 || assistantMsg.Content[0].Type != "tool_use" {
		t.Errorf("expected tool_use content in assistant message")
	}
	if assistantMsg.Content[0].ID != "tc_123" {
		t.Errorf("expected tool call ID tc_123, got %s", assistantMsg.Content[0].ID)
	}

	// Tool result should be user message with tool_result content
	toolMsg := apiReq.Messages[2]
	if toolMsg.Role != "user" {
		t.Errorf("expected user role for tool result, got %s", toolMsg.Role)
	}
	if len(toolMsg.Content) != 1 || toolMsg.Content[0].Type != "tool_result" {
		t.Errorf("expected tool_result content")
	}
	if toolMsg.Content[0].ToolUseID != "tc_123" {
		t.Errorf("expected tool_use_id tc_123, got %s", toolMsg.Content[0].ToolUseID)
	}
}

func TestAdapterConvertResponse_TextOnly(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")

	apiResp := &apiResponse{
		ID:         "msg_123",
		Model:      "claude-3-5-sonnet-20241022",
		StopReason: "end_turn",
		Content: []apiContent{
			{Type: "text", Text: "Hello! How can I help?"},
		},
		Usage: apiUsage{InputTokens: 10, OutputTokens: 8},
	}

	resp := adapter.convertResponse(apiResp)

	if resp.ID != "msg_123" {
		t.Errorf("expected ID msg_123, got %s", resp.ID)
	}
	if !resp.Done {
		t.Error("expected Done to be true")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello! How can I help?" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Error("expected finish reason 'stop'")
	}
	if resp.Usage.TotalTokens != 18 {
		t.Errorf("expected 18 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestAdapterConvertResponse_ToolUse(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")

	apiResp := &apiResponse{
		ID:         "msg_456",
		Model:      "claude-3-5-sonnet-20241022",
		StopReason: "tool_use",
		Content: []apiContent{
			{Type: "text", Text: "Let me read that file."},
			{Type: "tool_use", ID: "tc_789", Name: "read_file", Input: map[string]any{"path": "/tmp/test.txt"}},
		},
		Usage: apiUsage{InputTokens: 20, OutputTokens: 15},
	}

	resp := adapter.convertResponse(apiResp)

	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.Message.Content != "Let me read that file." {
		t.Errorf("unexpected content: %s", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "tc_789" || tc.Function.Name != "read_file" {
		t.Errorf("unexpected tool call: %+v", tc)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "tool_calls" {
		t.Error("expected finish reason 'tool_calls'")
	}
}

func TestAdapterGenerateContent_Integration(t *testing.T) {
	// Create a test server that returns a mock Anthropic response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header")
		}
		if r.Header.Get("anthropic-version") != apiVersion {
			t.Errorf("expected anthropic-version header")
		}

		resp := apiResponse{
			ID:         "msg_test",
			Model:      "claude-3-5-sonnet-20241022",
			StopReason: "end_turn",
			Content:    []apiContent{{Type: "text", Text: "Test response"}},
			Usage:      apiUsage{InputTokens: 5, OutputTokens: 3},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")
	adapter.client.apiURL = server.URL

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "Hello"},
		},
	}

	ch, err := adapter.GenerateContent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := <-ch
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %s", resp.Error.Message)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Test response" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
}

func TestAdapterGenerateContent_NilRequest(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")

	_, err := adapter.GenerateContent(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil request")
	}
}

func TestAdapterInfo(t *testing.T) {
	adapter := NewAdapter("test-key", "claude-3-5-sonnet-20241022")
	info := adapter.Info()
	if info.Name != "claude-3-5-sonnet-20241022" {
		t.Errorf("expected model name in info, got %s", info.Name)
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"end_turn", "stop"},
		{"tool_use", "tool_calls"},
		{"max_tokens", "length"},
		{"stop_sequence", "stop"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		got := mapStopReason(tt.input)
		if got != tt.expected {
			t.Errorf("mapStopReason(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeMessages_NormalConversation(t *testing.T) {
	msgs := []apiMessage{
		{Role: "user", Content: []apiContent{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []apiContent{
			{Type: "tool_use", ID: "t1", Name: "foo", Input: map[string]any{}},
		}},
		{Role: "user", Content: []apiContent{
			{Type: "tool_result", ToolUseID: "t1", Content: []byte(`"ok"`)},
		}},
	}
	result := sanitizeMessages(msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
}

func TestSanitizeMessages_OrphanedToolUse(t *testing.T) {
	msgs := []apiMessage{
		{Role: "user", Content: []apiContent{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []apiContent{
			{Type: "text", Text: "thinking"},
			{Type: "tool_use", ID: "t1", Name: "foo", Input: map[string]any{}},
		}},
		// no tool_result for t1
	}
	result := sanitizeMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	// assistant message should only have text, no tool_use
	if len(result[1].Content) != 1 || result[1].Content[0].Type != "text" {
		t.Errorf("expected only text content in assistant message, got %v", result[1].Content)
	}
}

func TestSanitizeMessages_OrphanedToolResult(t *testing.T) {
	msgs := []apiMessage{
		{Role: "user", Content: []apiContent{
			{Type: "tool_result", ToolUseID: "t_missing", Content: []byte(`"ok"`)},
		}},
		{Role: "assistant", Content: []apiContent{{Type: "text", Text: "done"}}},
	}
	result := sanitizeMessages(msgs)
	// orphaned tool_result message gets dropped entirely, leaving assistant
	// but assistant is not user role, so it also gets dropped
	// only messages starting with user role survive
	if len(result) != 0 {
		// The tool_result message is dropped (orphaned), leaving only assistant,
		// which is then dropped because first message must be user role.
		t.Fatalf("expected 0 messages, got %d", len(result))
	}
}

func TestSanitizeMessages_LeadingAssistant(t *testing.T) {
	msgs := []apiMessage{
		{Role: "assistant", Content: []apiContent{{Type: "text", Text: "hi"}}},
		{Role: "user", Content: []apiContent{{Type: "text", Text: "hello"}}},
	}
	result := sanitizeMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected first message to be user, got %s", result[0].Role)
	}
}

func TestSanitizeMessages_ToolResultAfterTruncatedToolUse(t *testing.T) {
	// Reproduces: messages.0 has tool_result but the tool_use was in a
	// preceding assistant message that got truncated away.
	msgs := []apiMessage{
		{Role: "user", Content: []apiContent{
			{Type: "tool_result", ToolUseID: "toolu_01HUTv2NiU4rfzYYNz94XpHL", Content: []byte(`"done"`)},
		}},
		{Role: "assistant", Content: []apiContent{{Type: "text", Text: "great"}}},
		{Role: "user", Content: []apiContent{{Type: "text", Text: "thanks"}}},
	}
	result := sanitizeMessages(msgs)
	// The tool_result is orphaned (no preceding tool_use). After stripping it,
	// the assistant message becomes first and gets dropped, leaving just "thanks".
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Content[0].Text != "thanks" {
		t.Errorf("expected 'thanks', got %s", result[0].Content[0].Text)
	}
}

func TestSanitizeMessages_NonAdjacentPair(t *testing.T) {
	// tool_use and tool_result both exist but are not adjacent (another message between them).
	msgs := []apiMessage{
		{Role: "user", Content: []apiContent{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []apiContent{
			{Type: "tool_use", ID: "t1", Name: "foo", Input: map[string]any{}},
		}},
		{Role: "user", Content: []apiContent{{Type: "text", Text: "interrupt"}}},
		{Role: "user", Content: []apiContent{
			{Type: "tool_result", ToolUseID: "t1", Content: []byte(`"ok"`)},
		}},
	}
	result := sanitizeMessages(msgs)
	// tool_use at [1] expects result at [2], but [2] is text. Both get stripped.
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Content[0].Text != "hello" {
		t.Errorf("expected 'hello', got %s", result[0].Content[0].Text)
	}
	if result[1].Content[0].Text != "interrupt" {
		t.Errorf("expected 'interrupt', got %s", result[1].Content[0].Text)
	}
}

func TestArgsToMap(t *testing.T) {
	// Valid JSON
	m := argsToMap([]byte(`{"key": "value"}`))
	if m["key"] != "value" {
		t.Errorf("expected key=value, got %v", m)
	}

	// Empty - should return empty map (not nil) for Anthropic API compatibility
	m = argsToMap(nil)
	if m == nil || len(m) != 0 {
		t.Errorf("expected empty map for empty args, got %v", m)
	}

	// Invalid JSON - should return empty map (not nil) for Anthropic API compatibility
	m = argsToMap([]byte(`not json`))
	if m == nil || len(m) != 0 {
		t.Errorf("expected empty map for invalid JSON, got %v", m)
	}
}
