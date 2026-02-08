// Package tools provides tool implementations for the agent.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/config"
)

// planDeliberateArgs is the input schema for the plan_deliberate tool.
type planDeliberateArgs struct {
	Task              string   `json:"task" jsonschema:"required,description=The task or goal to deliberate on before decomposition"`
	Constraints       []string `json:"constraints,omitempty" jsonschema:"description=Constraints to consider (e.g. time; quality; cost; simplicity)"`
	ExplorationBudget int      `json:"exploration_budget,omitempty" jsonschema:"description=Number of approaches to consider (default 3; max 5)"`
	MustConsider      []string `json:"must_consider,omitempty" jsonschema:"description=Specific approaches that must be evaluated"`
	Context           string   `json:"context,omitempty" jsonschema:"description=Additional context about the problem domain or constraints"`
}

// planDeliberateResult is the output schema for the plan_deliberate tool.
type planDeliberateResult struct {
	DeliberationID string     `json:"deliberation_id"` // UUID for linking to backlog_decompose
	Approaches     []Approach `json:"approaches"`      // Evaluated options (reuses Approach from reasoning.go)
	Selected       string     `json:"selected"`        // Chosen approach name
	Rationale      string     `json:"rationale"`       // Why this approach was selected
	Risks          []string   `json:"risks"`           // Identified risks with selected approach
	Mitigations    []string   `json:"mitigations"`     // How to handle identified risks
	ModelUsed      string     `json:"model_used"`      // Which model performed the analysis
}

// DeliberationTool handles strategic deliberation using a Tier 2 model.
type DeliberationTool struct {
	tier2Model    model.Model
	store         *backlog.Store
	cfg           config.ReasoningConfig
	resolvedModel string
	logger        *slog.Logger
}

// NewDeliberationTool creates the plan_deliberate tool.
// resolvedModel is the actual "provider/model" string being used (e.g., "anthropic/claude-opus-4-5-20251101").
// Returns nil if tier2Model is nil (reasoning not configured).
func NewDeliberationTool(
	tier2Model model.Model,
	store *backlog.Store,
	cfg config.ReasoningConfig,
	resolvedModel string,
	logger *slog.Logger,
) tool.Tool {
	if tier2Model == nil {
		return nil
	}

	t := &DeliberationTool{
		tier2Model:    tier2Model,
		store:         store,
		cfg:           cfg,
		resolvedModel: resolvedModel,
		logger:        logger,
	}

	return function.NewFunctionTool(
		t.execute,
		function.WithName("plan_deliberate"),
		function.WithDescription(`Evaluate multiple approaches before committing to a task decomposition plan.

Use this tool when:
- A task has multiple valid approaches with trade-offs to evaluate
- Strategic or architectural decisions are needed before decomposition
- You want to explicitly reason about alternatives before acting
- The task involves design choices with significant downstream impact

Returns a deliberation_id that can be passed to backlog_decompose to link the execution plan to this strategic deliberation, creating an audit trail.`),
	)
}

func (t *DeliberationTool) execute(ctx context.Context, args planDeliberateArgs) (*planDeliberateResult, error) {
	t.logger.Info("deliberation invoked",
		"task_preview", truncate(args.Task, 100),
		"constraints", args.Constraints,
		"budget", args.ExplorationBudget,
	)

	// Set defaults and bounds
	budget := args.ExplorationBudget
	if budget <= 0 {
		budget = 3
	}
	if budget > 5 {
		budget = 5
	}

	// Build the deliberation prompt
	prompt := t.buildPrompt(args, budget)

	// Create request for the Tier 2 model
	maxTokens := t.cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: t.systemPrompt()},
			{Role: model.RoleUser, Content: prompt},
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
		},
	}

	// Call the Tier 2 model
	respCh, err := t.tier2Model.GenerateContent(ctx, req)
	if err != nil {
		t.logger.Error("tier 2 model call failed", "error", err)
		return nil, fmt.Errorf("deliberation model call failed: %w", err)
	}

	// Collect the response from the channel
	var responseContent string
	for resp := range respCh {
		if resp.Error != nil {
			t.logger.Error("tier 2 model response error", "error", resp.Error.Message)
			return nil, fmt.Errorf("deliberation model error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			responseContent = resp.Choices[0].Message.Content
		}
	}

	// Parse the structured response
	result, err := t.parseResponse(responseContent)
	if err != nil {
		t.logger.Warn("failed to parse structured response, returning raw",
			"error", err,
			"response_preview", truncate(responseContent, 200),
		)
		// Fallback: return with raw rationale
		return &planDeliberateResult{
			DeliberationID: uuid.New().String(),
			Rationale:      responseContent,
			ModelUsed:      t.resolvedModel,
		}, nil
	}

	// Generate deliberation ID
	result.DeliberationID = uuid.New().String()
	result.ModelUsed = t.resolvedModel

	// Persist deliberation for future reference and audit trail
	if t.store != nil {
		record := &backlog.DeliberationRecord{
			ID:          result.DeliberationID,
			Task:        args.Task,
			Constraints: args.Constraints,
			Approaches:  toBacklogApproaches(result.Approaches),
			Selected:    result.Selected,
			Rationale:   result.Rationale,
			Risks:       result.Risks,
			Mitigations: result.Mitigations,
			CreatedAt:   time.Now().UTC(),
		}
		if err := t.store.AddDeliberation(record); err != nil {
			t.logger.Warn("failed to store deliberation", "id", result.DeliberationID, "error", err)
			// Continue anyway - the tool result is still useful
		}
	}

	t.logger.Info("deliberation complete",
		"id", result.DeliberationID,
		"selected", result.Selected,
		"approaches", len(result.Approaches),
	)

	return result, nil
}

func (t *DeliberationTool) systemPrompt() string {
	return `You are a strategic planning assistant. Your role is to evaluate multiple approaches to a task and recommend the best one before implementation begins.

When analyzing a task:
1. Generate the requested number of distinct approaches (not variations of the same idea)
2. Each approach should be genuinely different in strategy, not just implementation details
3. Evaluate trade-offs honestly - every approach has downsides
4. Consider the stated constraints when evaluating approaches
5. Select the approach that best balances the constraints
6. Identify concrete risks with the selected approach
7. Provide actionable mitigations for each risk

Always respond in the following JSON format:
{
    "approaches": [
        {
            "name": "Approach name",
            "strategy": "How this approach works",
            "pros": ["advantage 1", "advantage 2"],
            "cons": ["disadvantage 1", "disadvantage 2"],
            "skills_required": ["skill1", "skill2"],
            "effort": "low|medium|high"
        }
    ],
    "selected": "Name of recommended approach",
    "rationale": "Why this approach is recommended given the constraints",
    "risks": ["risk 1", "risk 2"],
    "mitigations": ["mitigation for risk 1", "mitigation for risk 2"]
}

Be strategic and thorough. The goal is to make an informed decision before committing to implementation.`
}

func (t *DeliberationTool) buildPrompt(args planDeliberateArgs, budget int) string {
	var sb strings.Builder

	sb.WriteString("## Task to Deliberate On\n\n")
	sb.WriteString(args.Task)
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("## Exploration Budget: %d Approaches\n\n", budget))
	sb.WriteString("Please generate exactly this many distinct approaches.\n\n")

	if len(args.Constraints) > 0 {
		sb.WriteString("## Constraints to Consider\n\n")
		for _, c := range args.Constraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	if len(args.MustConsider) > 0 {
		sb.WriteString("## Approaches That Must Be Evaluated\n\n")
		sb.WriteString("The following approaches must be included in your analysis:\n")
		for _, a := range args.MustConsider {
			sb.WriteString(fmt.Sprintf("- %s\n", a))
		}
		sb.WriteString("\n")
	}

	if args.Context != "" {
		sb.WriteString("## Additional Context\n\n")
		sb.WriteString(args.Context)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Please evaluate these approaches and provide your structured JSON recommendation.")

	return sb.String()
}

func (t *DeliberationTool) parseResponse(content string) (*planDeliberateResult, error) {
	if content == "" {
		return nil, fmt.Errorf("empty response from model")
	}

	// Try to extract JSON from the response
	jsonContent := content

	// Handle cases where JSON is wrapped in markdown code blocks
	if idx := strings.Index(content, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(content[start:], "```"); end >= 0 {
			jsonContent = content[start : start+end]
		}
	} else if idx := strings.Index(content, "```"); idx >= 0 {
		start := idx + 3
		// Skip any language identifier on the same line
		if nlIdx := strings.Index(content[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		if end := strings.Index(content[start:], "```"); end >= 0 {
			jsonContent = content[start : start+end]
		}
	}

	jsonContent = strings.TrimSpace(jsonContent)

	var result planDeliberateResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &result, nil
}

// toBacklogApproaches converts tool Approaches to backlog Approaches.
// This handles the case where the types are in different packages.
func toBacklogApproaches(approaches []Approach) []backlog.Approach {
	result := make([]backlog.Approach, len(approaches))
	for i, a := range approaches {
		result[i] = backlog.Approach{
			Name:           a.Name,
			Strategy:       a.Strategy,
			Pros:           a.Pros,
			Cons:           a.Cons,
			SkillsRequired: a.SkillsRequired,
			Effort:         a.Effort,
		}
	}
	return result
}
