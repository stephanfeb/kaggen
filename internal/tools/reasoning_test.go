package tools

import (
	"context"
	"testing"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/pkg/protocol"
)

// mockModel implements model.Model for testing.
type mockModel struct {
	response *protocol.Response
	err      error
}

func (m *mockModel) Generate(ctx context.Context, messages []protocol.Message, tools []protocol.ToolDef) (*protocol.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func TestReasoningTool_NilModel(t *testing.T) {
	tool := NewReasoningTool(nil, nil, config.ReasoningConfig{}, nil)
	if tool != nil {
		t.Error("expected nil tool when model is nil")
	}
}

func TestReasoningTool_ParseResponse_ValidJSON(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	jsonContent := `{
		"analysis": "This is a test analysis",
		"approaches": [
			{
				"name": "Approach A",
				"strategy": "Do A",
				"pros": ["fast"],
				"cons": ["risky"],
				"skills_required": ["coder"],
				"effort": "low"
			}
		],
		"selected_plan": "Approach A",
		"confidence": 0.85,
		"next_steps": ["Step 1", "Step 2"]
	}`

	result, err := rt.parseResponse(jsonContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Analysis != "This is a test analysis" {
		t.Errorf("unexpected analysis: %s", result.Analysis)
	}
	if result.SelectedPlan != "Approach A" {
		t.Errorf("unexpected selected_plan: %s", result.SelectedPlan)
	}
	if result.Confidence != 0.85 {
		t.Errorf("unexpected confidence: %f", result.Confidence)
	}
	if len(result.Approaches) != 1 {
		t.Errorf("expected 1 approach, got %d", len(result.Approaches))
	}
	if len(result.NextSteps) != 2 {
		t.Errorf("expected 2 next steps, got %d", len(result.NextSteps))
	}
}

func TestReasoningTool_ParseResponse_JSONInCodeBlock(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	content := "Here is my analysis:\n\n```json\n" + `{
		"analysis": "Wrapped in code block",
		"approaches": [],
		"selected_plan": "None",
		"confidence": 0.5,
		"next_steps": []
	}` + "\n```\n\nHope this helps!"

	result, err := rt.parseResponse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Analysis != "Wrapped in code block" {
		t.Errorf("unexpected analysis: %s", result.Analysis)
	}
}

func TestReasoningTool_ParseResponse_EmptyContent(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	_, err := rt.parseResponse("")
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestReasoningTool_ParseResponse_InvalidJSON(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	_, err := rt.parseResponse("This is not valid JSON at all")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReasoningTool_BuildPrompt_Basic(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	args := reasoningEscalateArgs{
		Task:   "Design a caching layer",
		Reason: "Multiple approaches possible",
	}

	prompt := rt.buildPrompt(args, "")
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if len(prompt) < 50 {
		t.Error("expected substantial prompt")
	}
	// Check key sections are included
	if !contains(prompt, "Design a caching layer") {
		t.Error("expected task in prompt")
	}
	if !contains(prompt, "Multiple approaches possible") {
		t.Error("expected reason in prompt")
	}
}

func TestReasoningTool_BuildPrompt_WithContext(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	args := reasoningEscalateArgs{
		Task:    "Design a caching layer",
		Reason:  "Architectural decision",
		Context: "We're using Redis in production",
	}

	prompt := rt.buildPrompt(args, "")
	if !contains(prompt, "We're using Redis in production") {
		t.Error("expected context in prompt")
	}
}

func TestReasoningTool_BuildPrompt_WithWorldState(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	args := reasoningEscalateArgs{
		Task:   "Optimize database queries",
		Reason: "Performance issues",
		WorldState: map[string]string{
			"current_db":    "PostgreSQL",
			"query_count":   "1000/sec",
			"response_time": "500ms",
		},
	}

	prompt := rt.buildPrompt(args, "")
	if !contains(prompt, "PostgreSQL") {
		t.Error("expected world state values in prompt")
	}
	if !contains(prompt, "1000/sec") {
		t.Error("expected world state values in prompt")
	}
}

func TestReasoningTool_BuildPrompt_WithWorldContext(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	args := reasoningEscalateArgs{
		Task:   "Fix the failing tests",
		Reason: "Multiple failures",
	}

	worldContext := "Session running for 5m\n- 10 tool calls (60% success rate)\n- 2 files modified"

	prompt := rt.buildPrompt(args, worldContext)
	if !contains(prompt, "Session running for 5m") {
		t.Error("expected world context in prompt")
	}
}

func TestReasoningTool_SystemPrompt(t *testing.T) {
	rt := &ReasoningTool{
		cfg: config.ReasoningConfig{Tier2Model: "test-model"},
	}

	sysPrompt := rt.systemPrompt()
	if sysPrompt == "" {
		t.Error("expected non-empty system prompt")
	}
	// Check it contains key instructions
	if !contains(sysPrompt, "JSON") {
		t.Error("expected JSON format instruction")
	}
	if !contains(sysPrompt, "approaches") {
		t.Error("expected approaches instruction")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is a ..."},
		{"exactly10!", 10, "exactly10!"},
		{"", 10, ""},
	}

	for _, tc := range tests {
		result := truncate(tc.input, tc.maxLen)
		if result != tc.expected {
			t.Errorf("truncate(%q, %d) = %q, expected %q", tc.input, tc.maxLen, result, tc.expected)
		}
	}
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
