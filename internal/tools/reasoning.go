// Package tools provides tool implementations for the agent.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/model"
	"github.com/yourusername/kaggen/pkg/protocol"
)

// reasoningEscalateArgs is the input schema for the reasoning_escalate tool.
type reasoningEscalateArgs struct {
	Task       string            `json:"task" jsonschema:"required,description=The complex task requiring deep analysis"`
	Reason     string            `json:"reason" jsonschema:"required,description=Why escalation is needed (e.g. architectural decision; multiple approaches; high complexity)"`
	Context    string            `json:"context,omitempty" jsonschema:"description=Additional context about the problem"`
	WorldState map[string]string `json:"world_state,omitempty" jsonschema:"description=Current project/execution state key-value pairs"`
}

// Approach represents a potential solution strategy.
type Approach struct {
	Name           string   `json:"name"`
	Strategy       string   `json:"strategy"`
	Pros           []string `json:"pros"`
	Cons           []string `json:"cons"`
	SkillsRequired []string `json:"skills_required"`
	Effort         string   `json:"effort"` // low | medium | high
}

// reasoningEscalateResult is the output schema for the reasoning_escalate tool.
type reasoningEscalateResult struct {
	Analysis     string     `json:"analysis"`       // Deep analysis of the problem
	Approaches   []Approach `json:"approaches"`     // Evaluated options
	SelectedPlan string     `json:"selected_plan"`  // Recommended approach name
	Confidence   float64    `json:"confidence"`     // 0-1 confidence in recommendation
	NextSteps    []string   `json:"next_steps"`     // Concrete actions to take
	ModelUsed    string     `json:"model_used"`     // Which model performed the analysis
	WorldContext string     `json:"world_context"`  // Execution summary if available
}

// ReasoningTool handles reasoning escalation to a Tier 2 model.
type ReasoningTool struct {
	tier2Model model.Model
	store      *kaggenAgent.InFlightStore
	cfg        config.ReasoningConfig
	logger     *slog.Logger
}

// NewReasoningTool creates the reasoning_escalate tool.
// Returns nil if tier2Model is nil (reasoning not configured).
func NewReasoningTool(tier2Model model.Model, store *kaggenAgent.InFlightStore, cfg config.ReasoningConfig, logger *slog.Logger) tool.Tool {
	if tier2Model == nil {
		return nil
	}

	t := &ReasoningTool{
		tier2Model: tier2Model,
		store:      store,
		cfg:        cfg,
		logger:     logger,
	}

	return function.NewFunctionTool(
		t.execute,
		function.WithName("reasoning_escalate"),
		function.WithDescription(`Escalate a complex task to a deeper reasoning model for thorough analysis.
Use this when:
- Task involves architectural or design decisions
- Multiple valid approaches exist with trade-offs to evaluate
- Task decomposition suggests >5 subtasks
- Low confidence in the best approach
- Keywords present: design, architect, evaluate, analyze, compare, trade-off

Returns structured analysis with multiple approaches, recommendations, and next steps.`),
	)
}

func (t *ReasoningTool) execute(ctx context.Context, args reasoningEscalateArgs) (*reasoningEscalateResult, error) {
	t.logger.Info("reasoning escalation invoked",
		"task_preview", truncate(args.Task, 100),
		"reason", args.Reason,
	)

	// Extract session ID and get WorldModel context
	var worldContext string
	var sessionID string
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv.Session != nil {
		sessionID = inv.Session.ID
	}
	if sessionID != "" && t.store != nil {
		wm := t.store.GetWorldModel(sessionID)
		if wm != nil {
			summary := wm.GetExecutionSummary()
			worldContext = summary.String()
		}
	}

	// Build the reasoning prompt
	prompt := t.buildPrompt(args, worldContext)

	// Create messages for the Tier 2 model
	messages := []protocol.Message{
		{
			Role:    "user",
			Content: t.systemPrompt() + "\n\n" + prompt,
		},
	}

	// Call the Tier 2 model
	response, err := t.tier2Model.Generate(ctx, messages, nil)
	if err != nil {
		t.logger.Error("tier 2 model call failed", "error", err)
		return nil, fmt.Errorf("reasoning model call failed: %w", err)
	}

	// Parse the structured response
	result, err := t.parseResponse(response.Content)
	if err != nil {
		t.logger.Warn("failed to parse structured response, returning raw",
			"error", err,
			"response_preview", truncate(response.Content, 200),
		)
		// Fallback: return the raw analysis
		return &reasoningEscalateResult{
			Analysis:     response.Content,
			Confidence:   0.5,
			ModelUsed:    t.cfg.Tier2Model,
			WorldContext: worldContext,
		}, nil
	}

	result.ModelUsed = t.cfg.Tier2Model
	result.WorldContext = worldContext
	t.logger.Info("reasoning escalation complete",
		"selected_plan", result.SelectedPlan,
		"confidence", result.Confidence,
		"approaches", len(result.Approaches),
	)

	return result, nil
}

func (t *ReasoningTool) systemPrompt() string {
	return `You are a strategic reasoning assistant. Your role is to deeply analyze complex tasks and provide structured recommendations.

When analyzing a task:
1. Consider multiple distinct approaches (at least 2-3)
2. Evaluate trade-offs honestly (every approach has downsides)
3. Be specific about effort and skills required
4. Recommend the best approach with clear rationale
5. Provide concrete, actionable next steps

Always respond in the following JSON format:
{
    "analysis": "Deep analysis of the problem and constraints",
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
    "selected_plan": "Name of recommended approach",
    "confidence": 0.85,
    "next_steps": ["Step 1", "Step 2", "Step 3"]
}

Be thorough but concise. Focus on actionable insights.`
}

func (t *ReasoningTool) buildPrompt(args reasoningEscalateArgs, worldContext string) string {
	var sb strings.Builder

	sb.WriteString("## Task Requiring Analysis\n\n")
	sb.WriteString(args.Task)
	sb.WriteString("\n\n")

	sb.WriteString("## Reason for Escalation\n\n")
	sb.WriteString(args.Reason)
	sb.WriteString("\n\n")

	if args.Context != "" {
		sb.WriteString("## Additional Context\n\n")
		sb.WriteString(args.Context)
		sb.WriteString("\n\n")
	}

	// Add world state if provided
	if len(args.WorldState) > 0 {
		sb.WriteString("## Current Project State\n\n")
		for k, v := range args.WorldState {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", k, v))
		}
		sb.WriteString("\n")
	}

	// Add execution context from WorldModel if available
	if worldContext != "" {
		sb.WriteString("## Execution Context\n\n")
		sb.WriteString(worldContext)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Please analyze this task and provide your structured JSON recommendation.")

	return sb.String()
}

func (t *ReasoningTool) parseResponse(content string) (*reasoningEscalateResult, error) {
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

	var result reasoningEscalateResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &result, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
