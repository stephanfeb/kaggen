// Package agent provides the core agent implementation.
package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	kaggenmodel "github.com/yourusername/kaggen/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// contextManagerKey is the context key for ContextManager.
type contextManagerKey struct{}

// ContextManager tracks token usage and triggers pruning to prevent context overflow.
type ContextManager struct {
	mu              sync.Mutex
	budget          kaggenmodel.ProviderBudget
	estimatedTokens int
	lastActualInput int // Last actual input tokens from API response
	toolOutputLimit int // Max chars for tool outputs before truncation
	logger          *slog.Logger
	pruneCount      int  // Number of times pruning has been triggered
	enabled         bool // Whether context pruning is enabled
}

// NewContextManager creates a new context manager for token tracking.
func NewContextManager(budget kaggenmodel.ProviderBudget, toolOutputLimit int, logger *slog.Logger) *ContextManager {
	if toolOutputLimit <= 0 {
		toolOutputLimit = 8000 // Default: 8000 chars
	}
	return &ContextManager{
		budget:          budget,
		toolOutputLimit: toolOutputLimit,
		logger:          logger,
		enabled:         true,
	}
}

// WithContextManager adds a ContextManager to the context.
// It also adds the manager as a ContextPruner so adapters can use it.
func WithContextManager(ctx context.Context, cm *ContextManager) context.Context {
	ctx = context.WithValue(ctx, contextManagerKey{}, cm)
	// Also add as ContextPruner for adapter access (avoids circular deps)
	ctx = kaggenmodel.WithContextPruner(ctx, cm)
	return ctx
}

// ContextManagerFromContext retrieves a ContextManager from the context.
func ContextManagerFromContext(ctx context.Context) *ContextManager {
	if cm, ok := ctx.Value(contextManagerKey{}).(*ContextManager); ok {
		return cm
	}
	return nil
}

// EstimateTokens estimates the token count for a string.
// Uses a conservative estimate of ~3 characters per token.
func EstimateTokens(content string) int {
	if content == "" {
		return 0
	}
	// Conservative: 3 chars per token (JSON/code tends to be more)
	return len(content) / 3
}

// EstimateMessageTokens estimates tokens for a single message.
func EstimateMessageTokens(msg model.Message) int {
	tokens := 4 // Base overhead for role and message structure

	// Content tokens
	tokens += EstimateTokens(msg.Content)

	// Tool calls tokens
	for _, tc := range msg.ToolCalls {
		tokens += 10 // Tool call overhead
		tokens += EstimateTokens(tc.Function.Name)
		tokens += EstimateTokens(string(tc.Function.Arguments))
	}

	// Content parts (multimodal)
	for _, part := range msg.ContentParts {
		if part.Text != nil {
			tokens += EstimateTokens(*part.Text)
		}
		if part.Image != nil {
			// Images are typically 85-1000 tokens depending on size
			tokens += 500 // Conservative estimate
		}
	}

	return tokens
}

// EstimateMessagesTokens estimates total tokens for a message slice.
func EstimateMessagesTokens(messages []model.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}

// RecordActualUsage updates the manager with actual token usage from API response.
func (cm *ContextManager) RecordActualUsage(inputTokens int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastActualInput = inputTokens
	cm.estimatedTokens = inputTokens // Calibrate estimate with actual
}

// EstimatedTokens returns the current estimated token count.
func (cm *ContextManager) EstimatedTokens() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.estimatedTokens
}

// Limit returns the effective token limit.
func (cm *ContextManager) Limit() int {
	return cm.budget.EffectiveLimit()
}

// PruneCount returns how many times pruning has been triggered.
func (cm *ContextManager) PruneCount() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.pruneCount
}

// NeedsIntervention returns true if token usage is approaching the limit.
func (cm *ContextManager) NeedsIntervention() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if !cm.enabled {
		return false
	}
	return cm.estimatedTokens >= int(float64(cm.budget.EffectiveLimit())*0.9)
}

// CheckAndPrune evaluates messages and applies pruning if needed.
// Returns the pruned messages and whether pruning occurred.
func (cm *ContextManager) CheckAndPrune(messages []model.Message) ([]model.Message, bool) {
	if !cm.enabled {
		return messages, false
	}

	cm.mu.Lock()
	estimated := EstimateMessagesTokens(messages)
	cm.estimatedTokens = estimated
	limit := cm.budget.EffectiveLimit()
	cm.mu.Unlock()

	// No pruning needed
	if estimated < int(float64(limit)*0.6) {
		return messages, false
	}

	cm.mu.Lock()
	cm.pruneCount++
	cm.mu.Unlock()

	// Level 1: Truncate tool outputs (60-75% threshold)
	if estimated < int(float64(limit)*0.75) {
		cm.logger.Info("context manager: level 1 pruning (tool output truncation)",
			"estimated_tokens", estimated,
			"limit", limit,
			"threshold", "60%")
		return cm.truncateToolOutputs(messages), true
	}

	// Level 2: Consolidate messages (75-90% threshold)
	if estimated < int(float64(limit)*0.9) {
		cm.logger.Info("context manager: level 2 pruning (message consolidation)",
			"estimated_tokens", estimated,
			"limit", limit,
			"threshold", "75%")
		pruned := cm.truncateToolOutputs(messages)
		return cm.consolidateMessages(pruned), true
	}

	// Level 3: Emergency pruning (90%+ threshold)
	cm.logger.Warn("context manager: level 3 emergency pruning",
		"estimated_tokens", estimated,
		"limit", limit,
		"threshold", "90%")
	pruned := cm.truncateToolOutputs(messages)
	pruned = cm.consolidateMessages(pruned)
	return cm.emergencyPrune(pruned), true
}

// truncateToolOutputs truncates large tool result contents.
func (cm *ContextManager) truncateToolOutputs(messages []model.Message) []model.Message {
	result := make([]model.Message, len(messages))
	for i, msg := range messages {
		result[i] = msg
		if msg.Role == model.RoleTool && len(msg.Content) > cm.toolOutputLimit {
			// Truncate and add indicator
			truncated := msg.Content[:cm.toolOutputLimit]
			truncated += "\n\n[... output truncated due to context limits ...]"
			result[i].Content = truncated
		}
	}
	return result
}

// consolidateMessages merges consecutive same-role messages and removes old context.
func (cm *ContextManager) consolidateMessages(messages []model.Message) []model.Message {
	if len(messages) <= 4 {
		return messages
	}

	// Keep system prompt, first few exchanges, and last few exchanges
	var result []model.Message
	var systemMsg *model.Message

	// Extract system message
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			systemMsg = &msg
			break
		}
	}

	// Non-system messages
	nonSystem := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != model.RoleSystem {
			nonSystem = append(nonSystem, msg)
		}
	}

	// If still small, return as-is
	if len(nonSystem) <= 10 {
		return messages
	}

	// Keep first 2 messages (initial context) and last 8 messages (recent context)
	keepFirst := 2
	keepLast := 8

	if systemMsg != nil {
		result = append(result, *systemMsg)
	}

	// Add first messages
	result = append(result, nonSystem[:keepFirst]...)

	// Add consolidation marker
	droppedCount := len(nonSystem) - keepFirst - keepLast
	if droppedCount > 0 {
		result = append(result, model.Message{
			Role:    model.RoleUser,
			Content: "[Earlier conversation context removed to manage context size. " + string(rune(droppedCount)) + " messages were consolidated.]",
		})
	}

	// Add last messages
	result = append(result, nonSystem[len(nonSystem)-keepLast:]...)

	return result
}

// emergencyPrune aggressively reduces context to bare minimum.
func (cm *ContextManager) emergencyPrune(messages []model.Message) []model.Message {
	var result []model.Message
	var systemMsg *model.Message

	// Keep system message
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			systemMsg = &msg
			break
		}
	}

	if systemMsg != nil {
		result = append(result, *systemMsg)
	}

	// Find last user message
	var lastUserMsg *model.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			lastUserMsg = &messages[i]
			break
		}
	}

	// Create emergency context summary
	summary := cm.createEmergencySummary(messages)
	result = append(result, model.Message{
		Role:    model.RoleUser,
		Content: "[CONTEXT EMERGENCY: Previous conversation condensed due to token limits]\n\nSummary of prior work:\n" + summary,
	})

	// Keep last 4 messages for continuity
	nonSystem := make([]model.Message, 0)
	for _, msg := range messages {
		if msg.Role != model.RoleSystem {
			nonSystem = append(nonSystem, msg)
		}
	}

	if len(nonSystem) > 4 {
		result = append(result, nonSystem[len(nonSystem)-4:]...)
	} else if lastUserMsg != nil && len(nonSystem) == 0 {
		result = append(result, *lastUserMsg)
	} else {
		result = append(result, nonSystem...)
	}

	return result
}

// createEmergencySummary extracts key information from messages for emergency context.
func (cm *ContextManager) createEmergencySummary(messages []model.Message) string {
	var summary strings.Builder

	// Extract tool calls made
	toolCalls := make([]string, 0)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			toolCalls = append(toolCalls, tc.Function.Name)
		}
	}

	if len(toolCalls) > 0 {
		summary.WriteString("Tools used: ")
		// Deduplicate and limit
		seen := make(map[string]bool)
		unique := make([]string, 0)
		for _, tc := range toolCalls {
			if !seen[tc] {
				seen[tc] = true
				unique = append(unique, tc)
			}
		}
		if len(unique) > 10 {
			unique = unique[:10]
		}
		summary.WriteString(strings.Join(unique, ", "))
		summary.WriteString("\n")
	}

	// Extract any file paths mentioned
	filePaths := extractFilePaths(messages)
	if len(filePaths) > 0 {
		summary.WriteString("Files referenced: ")
		if len(filePaths) > 5 {
			filePaths = filePaths[:5]
		}
		summary.WriteString(strings.Join(filePaths, ", "))
		summary.WriteString("\n")
	}

	if summary.Len() == 0 {
		summary.WriteString("(No structured summary available)")
	}

	return summary.String()
}

// extractFilePaths finds file paths in message contents.
func extractFilePaths(messages []model.Message) []string {
	var paths []string
	seen := make(map[string]bool)

	for _, msg := range messages {
		// Look for tool calls with file path arguments
		for _, tc := range msg.ToolCalls {
			if len(tc.Function.Arguments) > 0 {
				var args map[string]any
				if err := json.Unmarshal(tc.Function.Arguments, &args); err == nil {
					for key, val := range args {
						if strings.Contains(strings.ToLower(key), "path") || strings.Contains(strings.ToLower(key), "file") {
							if s, ok := val.(string); ok && !seen[s] {
								seen[s] = true
								paths = append(paths, s)
							}
						}
					}
				}
			}
		}
	}

	return paths
}

// IsTokenOverflowError checks if an error is a token overflow error.
func IsTokenOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "token count exceeds") ||
		strings.Contains(msg, "maximum number of tokens") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "too many tokens")
}
