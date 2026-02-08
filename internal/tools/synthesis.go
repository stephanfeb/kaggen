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

// PartialSolution represents a partial solution to be synthesized.
type PartialSolution struct {
	Description string   `json:"description" jsonschema:"required,description=Description of what this partial solution addresses"`
	Approach    string   `json:"approach" jsonschema:"required,description=The approach or technique used"`
	Status      string   `json:"status,omitempty" jsonschema:"description=Status: complete or partial or blocked"`
	Gaps        []string `json:"gaps,omitempty" jsonschema:"description=Known gaps or missing pieces"`
}

// synthesisArgs is the input schema for the synthesize_solution tool.
type synthesisArgs struct {
	Goal             string            `json:"goal" jsonschema:"required,description=The overall goal or problem being solved"`
	PartialSolutions []PartialSolution `json:"partial_solutions" jsonschema:"required,description=Partial solutions to combine"`
	Constraints      []string          `json:"constraints,omitempty" jsonschema:"description=Constraints the final solution must satisfy"`
	Priority         string            `json:"priority,omitempty" jsonschema:"description=Priority: completeness or simplicity or performance (default completeness)"`
}

// Component represents a component in the synthesized solution.
type Component struct {
	Name        string `json:"name"`
	Source      string `json:"source"`      // Which partial solution it came from
	Role        string `json:"role"`        // Role in the final solution
	Adaptations string `json:"adaptations"` // How it was adapted for integration
}

// Dependency represents a dependency between components.
type Dependency struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Type   string `json:"type"` // calls | imports | extends | implements
	Reason string `json:"reason"`
}

// Integration describes how partial solutions are combined.
type Integration struct {
	Components   []Component  `json:"components"`
	Dependencies []Dependency `json:"dependencies"`
	Sequence     []string     `json:"sequence"` // Recommended implementation order
}

// synthesisResult is the output schema for the synthesize_solution tool.
type synthesisResult struct {
	SynthesizedPlan string      `json:"synthesized_plan"` // Narrative description
	Integration     Integration `json:"integration"`
	GapsIdentified  []string    `json:"gaps_identified"` // Gaps that remain unaddressed
	NextSteps       []string    `json:"next_steps"`      // Recommended next actions
	Confidence      float64     `json:"confidence"`      // 0-1, how confident in feasibility
	ModelUsed       string      `json:"model_used"`
}

// SynthesisTool handles combining partial solutions.
type SynthesisTool struct {
	tier2Model    model.Model
	cfg           config.CreativityConfig
	resolvedModel string
	logger        *slog.Logger
}

// NewSynthesisTool creates the synthesize_solution tool.
// Returns nil if tier2Model is nil.
func NewSynthesisTool(
	tier2Model model.Model,
	cfg config.CreativityConfig,
	resolvedModel string,
	logger *slog.Logger,
) tool.Tool {
	if tier2Model == nil {
		return nil
	}

	t := &SynthesisTool{
		tier2Model:    tier2Model,
		cfg:           cfg,
		resolvedModel: resolvedModel,
		logger:        logger,
	}

	return function.NewFunctionTool(
		t.execute,
		function.WithName("synthesize_solution"),
		function.WithDescription(`Combine partial solutions into a coherent integrated plan.

Use this tool when:
- You have multiple partial approaches that need to be combined
- Sub-agents have returned results that need integration
- Different aspects of a problem have been solved separately
- You need to identify gaps between partial solutions

Returns an integrated plan with component mapping, dependencies, and implementation sequence.`),
	)
}

func (t *SynthesisTool) execute(ctx context.Context, args synthesisArgs) (*synthesisResult, error) {
	t.logger.Info("synthesis invoked",
		"goal_preview", truncate(args.Goal, 100),
		"partial_count", len(args.PartialSolutions),
		"priority", args.Priority,
	)

	// Validate input
	if len(args.PartialSolutions) == 0 {
		return nil, fmt.Errorf("at least one partial solution is required")
	}

	// Default priority
	priority := args.Priority
	if priority == "" {
		priority = "completeness"
	}

	// Build synthesis prompt
	prompt := t.buildPrompt(args, priority)

	// Create request
	maxTokens := 8192
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
		t.logger.Error("synthesis model call failed", "error", err)
		return nil, fmt.Errorf("synthesis model call failed: %w", err)
	}

	// Collect the response
	var responseContent string
	for resp := range respCh {
		if resp.Error != nil {
			t.logger.Error("synthesis model response error", "error", resp.Error.Message)
			return nil, fmt.Errorf("synthesis model error: %s", resp.Error.Message)
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
		// Fallback: return with raw plan
		return &synthesisResult{
			SynthesizedPlan: responseContent,
			ModelUsed:       t.resolvedModel,
		}, nil
	}

	result.ModelUsed = t.resolvedModel

	t.logger.Info("synthesis complete",
		"components", len(result.Integration.Components),
		"gaps", len(result.GapsIdentified),
		"confidence", result.Confidence,
	)

	return result, nil
}

func (t *SynthesisTool) systemPrompt() string {
	return `You are an expert at integrating partial solutions into cohesive wholes.

Given a goal and multiple partial solutions, your job is to:
1. Identify what each partial solution contributes
2. Find overlaps, conflicts, and gaps between solutions
3. Create an integrated plan that combines the best of each
4. Map out dependencies and implementation sequence
5. Identify remaining gaps that need to be addressed

Always respond in the following JSON format:
{
    "synthesized_plan": "Narrative description of the integrated solution",
    "integration": {
        "components": [
            {
                "name": "Component name",
                "source": "Which partial solution this came from",
                "role": "Role in the final solution",
                "adaptations": "How it was adapted for integration"
            }
        ],
        "dependencies": [
            {
                "from": "component A",
                "to": "component B",
                "type": "calls|imports|extends|implements",
                "reason": "Why this dependency exists"
            }
        ],
        "sequence": ["Step 1", "Step 2", "Step 3"]
    },
    "gaps_identified": ["Gap 1", "Gap 2"],
    "next_steps": ["Action 1", "Action 2"],
    "confidence": 0.85
}

Be thorough in identifying integration challenges and gaps. A good synthesis acknowledges what's missing, not just what's present.`
}

func (t *SynthesisTool) buildPrompt(args synthesisArgs, priority string) string {
	var sb strings.Builder

	sb.WriteString("## Goal\n\n")
	sb.WriteString(args.Goal)
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("## Priority: %s\n\n", priority))
	sb.WriteString("Optimize the synthesis for this priority.\n\n")

	sb.WriteString("## Partial Solutions to Integrate\n\n")
	for i, ps := range args.PartialSolutions {
		sb.WriteString(fmt.Sprintf("### Partial Solution %d\n", i+1))
		sb.WriteString(fmt.Sprintf("**Description**: %s\n", ps.Description))
		sb.WriteString(fmt.Sprintf("**Approach**: %s\n", ps.Approach))
		if ps.Status != "" {
			sb.WriteString(fmt.Sprintf("**Status**: %s\n", ps.Status))
		}
		if len(ps.Gaps) > 0 {
			sb.WriteString("**Known Gaps**:\n")
			for _, g := range ps.Gaps {
				sb.WriteString(fmt.Sprintf("- %s\n", g))
			}
		}
		sb.WriteString("\n")
	}

	if len(args.Constraints) > 0 {
		sb.WriteString("## Constraints\n\n")
		for _, c := range args.Constraints {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Synthesize these partial solutions into a coherent integrated plan. Identify components, dependencies, gaps, and next steps.")

	return sb.String()
}

func (t *SynthesisTool) parseResponse(content string) (*synthesisResult, error) {
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
		if nlIdx := strings.Index(content[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		if end := strings.Index(content[start:], "```"); end >= 0 {
			jsonContent = content[start : start+end]
		}
	}

	jsonContent = strings.TrimSpace(jsonContent)

	var result synthesisResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &result, nil
}
