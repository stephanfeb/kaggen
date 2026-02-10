package agent

import (
	"log/slog"
	"os"
	"strings"
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

func TestContextManager_TaskPreservation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create a budget with a very small limit to force emergency pruning
	budget := kaggenmodel.ProviderBudget{
		MaxInputTokens:  300, // Very small to force emergency pruning
		MaxOutputTokens: 100,
		SafetyMargin:    0.1, // Effective limit = 270
	}

	cm := NewContextManager(budget, 50, logger)

	// Set the original task
	originalTask := "Create a REST API endpoint for user authentication with JWT tokens"
	cm.SetOriginalTask(originalTask)

	// Verify task is stored
	if cm.OriginalTask() != originalTask {
		t.Errorf("Expected original task to be stored, got %q", cm.OriginalTask())
	}

	// Create messages that will trigger emergency pruning (90%+ = 243+ tokens)
	// With 3 chars per token, we need about 800+ chars
	messages := []model.Message{
		{Role: model.RoleSystem, Content: "You are helpful."},
		{Role: model.RoleUser, Content: "Build me something great"},
		{Role: model.RoleAssistant, Content: "I'll help you. " + string(make([]byte, 400))},
		{Role: model.RoleTool, Content: string(make([]byte, 400))},
		{Role: model.RoleAssistant, Content: "I completed the task successfully."},
	}

	pruned, didPrune := cm.CheckAndPrune(messages)
	if !didPrune {
		t.Error("Expected emergency pruning to occur")
	}

	// Check that the original task is preserved in the pruned output
	found := false
	for _, msg := range pruned {
		if strings.Contains(msg.Content, originalTask) {
			found = true
			break
		}
	}

	if !found {
		t.Error("Original task should be preserved in pruned messages")
		for i, msg := range pruned {
			t.Logf("Message %d [%s]: %s", i, msg.Role, msg.Content[:min(100, len(msg.Content))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

// TestEmergencyPrune_AlwaysProducesUserMessage verifies that emergencyPrune
// always produces at least one user message, even when originalTask and
// summary are empty. This prevents "no valid messages after sanitization"
// errors in the Gemini adapter.
func TestEmergencyPrune_AlwaysProducesUserMessage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	budget := kaggenmodel.ProviderBudget{
		MaxInputTokens:  300,
		MaxOutputTokens: 100,
		SafetyMargin:    0.1,
	}

	cm := NewContextManager(budget, 50, logger)
	// Intentionally NOT setting originalTask to test edge case

	// Create messages with only assistant/tool roles (no user messages after system)
	messages := []model.Message{
		{Role: model.RoleSystem, Content: "You are helpful."},
		{Role: model.RoleAssistant, Content: "I'll help you. " + string(make([]byte, 400))},
		{Role: model.RoleTool, Content: string(make([]byte, 400))},
		{Role: model.RoleAssistant, Content: "Done."},
		{Role: model.RoleTool, Content: string(make([]byte, 100))},
	}

	pruned, didPrune := cm.CheckAndPrune(messages)
	if !didPrune {
		t.Error("Expected emergency pruning to occur")
	}

	// Verify at least one user message exists in the pruned output
	hasUserMessage := false
	for _, msg := range pruned {
		if msg.Role == model.RoleUser {
			hasUserMessage = true
			// Also verify it has the context emergency marker
			if !strings.Contains(msg.Content, "CONTEXT EMERGENCY") {
				t.Error("User message should contain CONTEXT EMERGENCY marker")
			}
			break
		}
	}

	if !hasUserMessage {
		t.Error("Emergency prune should always produce at least one user message")
		t.Log("Pruned messages:")
		for i, msg := range pruned {
			t.Logf("  %d [%s]: %s", i, msg.Role, msg.Content[:min(80, len(msg.Content))])
		}
	}
}

// TestConsolidateMessages_PreservesOriginalTask verifies that consolidateMessages
// includes the original task in the consolidation marker to prevent agents from
// going off-task when the task message is dropped during Level 2 pruning.
func TestConsolidateMessages_PreservesOriginalTask(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Budget that triggers Level 2 pruning (75-90% threshold)
	// Effective limit = 900 (1000 * 0.9)
	// Level 2 range: 675-810 tokens (75-90% of 900)
	budget := kaggenmodel.ProviderBudget{
		MaxInputTokens:  1000,
		MaxOutputTokens: 100,
		SafetyMargin:    0.1,
	}

	cm := NewContextManager(budget, 8000, logger)

	// Set original task
	originalTask := "Update the team member profile page with new bio"
	cm.SetOriginalTask(originalTask)

	// Create messages where the task is in the middle (will be dropped)
	// Need enough messages to trigger consolidation (>10 non-system)
	// Budget: 1000 tokens, effective limit = 900
	// Level 2 range: 75-90% of 900 = 675-810 tokens (~2025-2430 chars)
	// With 200 bytes per message × 16 messages = 3200+ chars → Level 2 pruning
	messages := []model.Message{
		{Role: model.RoleSystem, Content: "You are helpful."},
		// First 2 kept (might be from old conversation)
		{Role: model.RoleUser, Content: "Help me with recipe ideas " + string(make([]byte, 200))},
		{Role: model.RoleAssistant, Content: "Sure, I can help with recipes " + string(make([]byte, 200))},
		// These messages will be dropped during consolidation
		{Role: model.RoleUser, Content: "Actually, " + originalTask},
		{Role: model.RoleAssistant, Content: "I'll update the profile. " + string(make([]byte, 150))},
		{Role: model.RoleUser, Content: "Great, show me the current bio " + string(make([]byte, 150))},
		{Role: model.RoleAssistant, Content: "Here's the current bio... " + string(make([]byte, 150))},
		{Role: model.RoleUser, Content: "Now update it " + string(make([]byte, 150))},
		{Role: model.RoleAssistant, Content: "Updating... " + string(make([]byte, 150))},
		// Last 8 kept (but don't mention the original task explicitly)
		{Role: model.RoleUser, Content: "What's the status?"},
		{Role: model.RoleAssistant, Content: "Working on it " + string(make([]byte, 100))},
		{Role: model.RoleUser, Content: "Any issues?"},
		{Role: model.RoleAssistant, Content: "No issues " + string(make([]byte, 100))},
		{Role: model.RoleUser, Content: "Continue"},
		{Role: model.RoleAssistant, Content: "Continuing " + string(make([]byte, 100))},
		{Role: model.RoleUser, Content: "Almost done?"},
		{Role: model.RoleAssistant, Content: "Almost " + string(make([]byte, 100))},
	}

	pruned, didPrune := cm.CheckAndPrune(messages)
	if !didPrune {
		t.Error("Expected Level 2 pruning to occur")
	}

	// Verify the original task is preserved in the consolidation marker
	foundTask := false
	for _, msg := range pruned {
		if msg.Role == model.RoleUser && strings.Contains(msg.Content, "messages were consolidated") {
			// This is the consolidation marker - check if it has the task
			if strings.Contains(msg.Content, originalTask) {
				foundTask = true
				break
			}
		}
	}

	if !foundTask {
		t.Error("Original task should be preserved in consolidation marker")
		t.Log("Pruned messages:")
		for i, msg := range pruned {
			preview := msg.Content
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			t.Logf("  %d [%s]: %s", i, msg.Role, preview)
		}
	}
}
