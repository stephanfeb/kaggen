package agent

import (
	"log/slog"
	"os"
	"testing"

	kaggenmodel "github.com/yourusername/kaggen/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantMin int
		wantMax int
	}{
		{"empty", "", 0, 0},
		{"short", "hello", 1, 3},
		{"medium", "this is a medium length string for testing", 10, 20},
		{"long", string(make([]byte, 3000)), 900, 1100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.content)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("EstimateTokens() = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestContextManager_CheckAndPrune(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create a budget with a small limit for testing
	budget := kaggenmodel.ProviderBudget{
		MaxInputTokens:  1000, // Small limit for testing
		MaxOutputTokens: 500,
		SafetyMargin:    0.1,
	}

	cm := NewContextManager(budget, 100, logger) // 100 char tool output limit

	t.Run("no pruning needed for small context", func(t *testing.T) {
		messages := []model.Message{
			{Role: model.RoleSystem, Content: "You are helpful."},
			{Role: model.RoleUser, Content: "Hello"},
		}

		pruned, didPrune := cm.CheckAndPrune(messages)
		if didPrune {
			t.Error("Expected no pruning for small context")
		}
		if len(pruned) != len(messages) {
			t.Errorf("Expected %d messages, got %d", len(messages), len(pruned))
		}
	})

	t.Run("truncates tool outputs at level 1", func(t *testing.T) {
		// Create messages that exceed 60% of effective limit (900 * 0.6 = 540 tokens)
		// With 3 chars per token estimate, we need about 1620 chars to reach 540 tokens
		longToolOutput := string(make([]byte, 2000)) // 2000 chars = ~666 tokens
		messages := []model.Message{
			{Role: model.RoleSystem, Content: "You are helpful."},
			{Role: model.RoleUser, Content: "Read the file"},
			{Role: model.RoleAssistant, Content: "I'll read it."},
			{Role: model.RoleTool, Content: longToolOutput},
		}

		pruned, didPrune := cm.CheckAndPrune(messages)
		if !didPrune {
			t.Error("Expected pruning for large context")
		}

		// Check tool output was truncated (100 char limit + truncation message)
		for _, msg := range pruned {
			if msg.Role == model.RoleTool && len(msg.Content) > 200 {
				t.Errorf("Tool output should be truncated, got length %d", len(msg.Content))
			}
		}
	})
}

func TestContextManager_RecordActualUsage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	budget := kaggenmodel.ProviderBudget{
		MaxInputTokens:  1000,
		MaxOutputTokens: 500,
		SafetyMargin:    0.1,
	}

	cm := NewContextManager(budget, 8000, logger)

	// Initially 0
	if cm.EstimatedTokens() != 0 {
		t.Errorf("Expected 0 initial tokens, got %d", cm.EstimatedTokens())
	}

	// Record usage
	cm.RecordActualUsage(500)
	if cm.EstimatedTokens() != 500 {
		t.Errorf("Expected 500 tokens after recording, got %d", cm.EstimatedTokens())
	}
}

func TestContextManager_NeedsIntervention(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	budget := kaggenmodel.ProviderBudget{
		MaxInputTokens:  1000,
		MaxOutputTokens: 500,
		SafetyMargin:    0.1, // Effective limit = 900
	}

	cm := NewContextManager(budget, 8000, logger)

	// Record usage below 90% threshold (90% of 900 = 810)
	cm.RecordActualUsage(800)
	if cm.NeedsIntervention() {
		t.Error("Should not need intervention at 800 tokens (below 90% of 900)")
	}

	// Record usage above 90% threshold
	cm.RecordActualUsage(850)
	if !cm.NeedsIntervention() {
		t.Error("Should need intervention at 850 tokens (above 90% of 900)")
	}
}

func TestIsTokenOverflowError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"token count exceeds", errFromMsg("The input token count exceeds the maximum"), true},
		{"maximum number of tokens", errFromMsg("maximum number of tokens allowed"), true},
		{"context_length_exceeded", errFromMsg("context_length_exceeded"), true},
		{"random error", errFromMsg("something went wrong"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTokenOverflowError(tt.err); got != tt.expected {
				t.Errorf("IsTokenOverflowError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

type simpleError struct{ msg string }

func (e simpleError) Error() string { return e.msg }
func errFromMsg(msg string) error  { return simpleError{msg} }
