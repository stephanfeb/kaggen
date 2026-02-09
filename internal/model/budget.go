// Package model defines the interface for AI model providers.
package model

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ProviderBudget defines token limits for a model provider.
type ProviderBudget struct {
	MaxInputTokens  int     // Hard limit from provider
	MaxOutputTokens int     // Max response tokens
	SafetyMargin    float64 // Reserve percentage (e.g., 0.1 for 10%)
}

// ContextPruner is an interface for components that can prune message context.
type ContextPruner interface {
	// CheckAndPrune evaluates messages and applies pruning if needed.
	// Returns the pruned messages and whether pruning occurred.
	CheckAndPrune(messages []model.Message) ([]model.Message, bool)
}

// contextPrunerKey is the context key for ContextPruner.
type contextPrunerKey struct{}

// WithContextPruner adds a ContextPruner to the context.
func WithContextPruner(ctx context.Context, pruner ContextPruner) context.Context {
	return context.WithValue(ctx, contextPrunerKey{}, pruner)
}

// ContextPrunerFromContext retrieves a ContextPruner from the context.
func ContextPrunerFromContext(ctx context.Context) ContextPruner {
	if pruner, ok := ctx.Value(contextPrunerKey{}).(ContextPruner); ok {
		return pruner
	}
	return nil
}

// EffectiveLimit returns the safe input token limit after applying the safety margin.
func (b ProviderBudget) EffectiveLimit() int {
	return int(float64(b.MaxInputTokens) * (1 - b.SafetyMargin))
}

// DefaultBudgets maps model families to their default token budgets.
var DefaultBudgets = map[Family]ProviderBudget{
	FamilyAnthropic: {MaxInputTokens: 200000, MaxOutputTokens: 16384, SafetyMargin: 0.1},
	FamilyGemini:    {MaxInputTokens: 1048576, MaxOutputTokens: 8192, SafetyMargin: 0.1},
	FamilyZAI:       {MaxInputTokens: 131072, MaxOutputTokens: 16384, SafetyMargin: 0.1},
}

// BudgetForFamily returns the token budget for a given model family.
// Falls back to Anthropic budget if family is unknown.
func BudgetForFamily(family Family) ProviderBudget {
	if budget, ok := DefaultBudgets[family]; ok {
		return budget
	}
	return DefaultBudgets[FamilyAnthropic]
}

// BudgetForModel returns the token budget for a model string (e.g., "gemini/gemini-2.5-pro").
func BudgetForModel(modelString string) ProviderBudget {
	return BudgetForFamily(DetectFamily(modelString))
}
