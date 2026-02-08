// Package tools provides tool implementations for the agent.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
)

// explorationArgs is the input schema for the explore_approaches tool.
type explorationArgs struct {
	Task          string   `json:"task" jsonschema:"required,description=The problem to explore approaches for"`
	NumApproaches int      `json:"num_approaches,omitempty" jsonschema:"description=Number of approaches to generate (default 3; max 5)"`
	Constraints   []string `json:"constraints,omitempty" jsonschema:"description=Constraints to consider during exploration"`
	AvoidPatterns []string `json:"avoid_patterns,omitempty" jsonschema:"description=Patterns or approaches to explicitly avoid"`
	Context       string   `json:"context,omitempty" jsonschema:"description=Additional context about the problem domain"`
}

// ExploredApproach represents a creatively generated solution approach.
type ExploredApproach struct {
	Name       string   `json:"name"`
	Strategy   string   `json:"strategy"`
	Innovation string   `json:"innovation"` // What makes this approach novel or unconventional
	Risks      []string `json:"risks"`
	Effort     string   `json:"effort"`     // low | medium | high
	Confidence float64  `json:"confidence"` // 0-1, how confident in feasibility
}

// explorationResult is the output schema for the explore_approaches tool.
type explorationResult struct {
	Approaches []ExploredApproach `json:"approaches"`
	Novelty    string             `json:"novelty"`   // Assessment of overall novelty of approaches
	ModelUsed  string             `json:"model_used"`
}

// ExplorationTool handles creative exploration using elevated temperature.
type ExplorationTool struct {
	tier2Model    model.Model
	cfg           config.CreativityConfig
	resolvedModel string
	logger        *slog.Logger
}

// NewExplorationTool creates the explore_approaches tool.
// Returns nil if tier2Model is nil (reasoning not configured).
func NewExplorationTool(
	tier2Model model.Model,
	cfg config.CreativityConfig,
	resolvedModel string,
	logger *slog.Logger,
) tool.Tool {
	if tier2Model == nil {
		return nil
	}

	t := &ExplorationTool{
		tier2Model:    tier2Model,
		cfg:           cfg,
		resolvedModel: resolvedModel,
		logger:        logger,
	}

	return function.NewFunctionTool(
		t.execute,
		function.WithName("explore_approaches"),
		function.WithDescription(`Generate multiple creative approaches to a problem using divergent thinking.

Use this tool when:
- Brainstorming solutions to a novel problem
- Stuck on a problem and want unconventional options
- Want to explore multiple directions before committing
- Need creative alternatives beyond the obvious solution

Uses elevated temperature (0.95) to encourage creative, divergent exploration.
Returns multiple approaches with innovation notes and feasibility assessments.`),
	)
}

func (t *ExplorationTool) execute(ctx context.Context, args explorationArgs) (*explorationResult, error) {
	t.logger.Info("exploration invoked",
		"task_preview", truncate(args.Task, 100),
		"num_approaches", args.NumApproaches,
	)

	// Set defaults and bounds
	numApproaches := args.NumApproaches
	if numApproaches <= 0 {
		numApproaches = 3
	}
	if numApproaches > 5 {
		numApproaches = 5
	}

	// Build the exploration prompt
	prompt := t.buildPrompt(args, numApproaches)

	// Get exploration temperature (default 0.95 for creativity)
	temp := t.cfg.ExplorationTemp
	if temp <= 0 {
		temp = 0.95
	}

	// Create request with elevated temperature for divergent thinking
	maxTokens := 8192
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: t.systemPrompt()},
			{Role: model.RoleUser, Content: prompt},
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temp,
		},
	}

	// Call the Tier 2 model
	respCh, err := t.tier2Model.GenerateContent(ctx, req)
	if err != nil {
		t.logger.Error("exploration model call failed", "error", err)
		return nil, fmt.Errorf("exploration model call failed: %w", err)
	}

	// Collect the response from the channel
	var responseContent string
	for resp := range respCh {
		if resp.Error != nil {
			t.logger.Error("exploration model response error", "error", resp.Error.Message)
			return nil, fmt.Errorf("exploration model error: %s", resp.Error.Message)
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
		// Fallback: return with raw novelty assessment
		return &explorationResult{
			Novelty:   responseContent,
			ModelUsed: t.resolvedModel,
		}, nil
	}

	result.ModelUsed = t.resolvedModel

	t.logger.Info("exploration complete",
		"approaches", len(result.Approaches),
		"novelty_preview", truncate(result.Novelty, 50),
	)

	return result, nil
}

func (t *ExplorationTool) systemPrompt() string {
	return `You are a creative problem-solving assistant focused on divergent thinking and exploration.

Your role is to generate genuinely novel and unconventional approaches to problems. Don't just provide obvious solutions - push boundaries and explore creative alternatives.

When exploring approaches:
1. Generate truly distinct approaches, not minor variations
2. Include at least one unconventional or creative option
3. For each approach, explain what makes it innovative
4. Be honest about risks and feasibility
5. Don't self-censor unusual ideas - present them for consideration

Always respond in the following JSON format:
{
    "approaches": [
        {
            "name": "Approach name",
            "strategy": "How this approach works",
            "innovation": "What makes this approach novel or unconventional",
            "risks": ["risk 1", "risk 2"],
            "effort": "low|medium|high",
            "confidence": 0.8
        }
    ],
    "novelty": "Overall assessment of the creative exploration - were any truly novel ideas found?"
}

Be bold and creative. The goal is divergent exploration, not convergent optimization.`
}

func (t *ExplorationTool) buildPrompt(args explorationArgs, numApproaches int) string {
	var sb strings.Builder

	sb.WriteString("## Problem to Explore\n\n")
	sb.WriteString(args.Task)
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("## Generate %d Creative Approaches\n\n", numApproaches))
	sb.WriteString("Push beyond obvious solutions. Include at least one unconventional idea.\n\n")

	if len(args.Constraints) > 0 {
		sb.WriteString("## Constraints to Consider\n\n")
		for _, c := range args.Constraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	if len(args.AvoidPatterns) > 0 {
		sb.WriteString("## Patterns to Avoid\n\n")
		sb.WriteString("Do NOT suggest approaches that follow these patterns:\n")
		for _, p := range args.AvoidPatterns {
			sb.WriteString(fmt.Sprintf("- %s\n", p))
		}
		sb.WriteString("\n")
	}

	if args.Context != "" {
		sb.WriteString("## Additional Context\n\n")
		sb.WriteString(args.Context)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Explore creatively and provide your structured JSON response with multiple distinct approaches.")

	return sb.String()
}

func (t *ExplorationTool) parseResponse(content string) (*explorationResult, error) {
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

	var result explorationResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &result, nil
}
