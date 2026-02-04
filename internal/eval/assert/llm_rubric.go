package assert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// LLMRubric uses an LLM to evaluate whether the response satisfies a rubric.
type LLMRubric struct {
	rubric   string
	minScore float64
}

// JudgeModel is the model interface required for LLM-as-judge.
// This allows the assertion to be used with any model that implements GenerateContent.
type JudgeModel interface {
	GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error)
}

// DefaultJudgeModel is set by the Runner before evaluation.
// This allows the LLMRubric assertion to access the configured judge model.
var DefaultJudgeModel JudgeModel

// NewLLMRubric creates an LLMRubric assertion from config.
func NewLLMRubric(config Config) (Assertion, error) {
	if config.Rubric == "" {
		return nil, fmt.Errorf("llm-rubric assertion requires 'rubric' field")
	}

	minScore := 0.7 // Default threshold
	if config.MinScore > 0 {
		minScore = config.MinScore
	}

	return &LLMRubric{
		rubric:   config.Rubric,
		minScore: minScore,
	}, nil
}

func (a *LLMRubric) Type() string { return "llm-rubric" }

func (a *LLMRubric) Evaluate(ctx *Context) Result {
	if DefaultJudgeModel == nil {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: "no judge model configured",
		}
	}

	// Build the judge prompt
	prompt := buildJudgePrompt(ctx.Instruction, ctx.Response, a.rubric)

	// Call the judge model
	judgment, err := callJudgeModel(DefaultJudgeModel, prompt)
	if err != nil {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("judge model error: %v", err),
		}
	}

	passed := judgment.Score >= a.minScore

	return Result{
		Type:   a.Type(),
		Passed: passed,
		Score:  judgment.Score,
		Reason: judgment.Reason,
	}
}

// Judgment is the parsed response from the judge model.
type Judgment struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

func buildJudgePrompt(instruction, response, rubric string) string {
	return fmt.Sprintf(`You are an AI evaluation judge. Your task is to evaluate whether an assistant's response follows the given instruction correctly.

## Instruction Given to Assistant
%s

## Assistant's Response
%s

## Evaluation Criteria (Rubric)
%s

## Your Task
Score the response on a scale of 0.0 to 1.0:
- 1.0 = Perfectly satisfies the rubric
- 0.8 = Minor issues but essentially correct
- 0.5 = Partially correct, misses some aspects
- 0.2 = Mostly incorrect but attempted the task
- 0.0 = Completely wrong or didn't attempt

Respond with ONLY a JSON object (no markdown, no explanation outside JSON):
{"score": <float between 0.0 and 1.0>, "reason": "<one sentence explanation>"}`, instruction, response, rubric)
}

func callJudgeModel(m JudgeModel, prompt string) (*Judgment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &model.Request{
		Messages: []model.Message{
			{
				Role:    model.RoleUser,
				Content: prompt,
			},
		},
	}

	respCh, err := m.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	// Collect response
	var responseText string
	for resp := range respCh {
		if resp.Error != nil {
			return nil, fmt.Errorf("model error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			responseText += resp.Choices[0].Message.Content
		}
	}

	// Parse JSON response
	judgment, err := parseJudgment(responseText)
	if err != nil {
		return nil, fmt.Errorf("parse judgment: %w (response: %q)", err, responseText)
	}

	return judgment, nil
}

func parseJudgment(response string) (*Judgment, error) {
	// Try to extract JSON from the response
	response = strings.TrimSpace(response)

	// Handle markdown code blocks
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		if idx := strings.Index(response, "```"); idx >= 0 {
			response = response[:idx]
		}
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		if idx := strings.Index(response, "```"); idx >= 0 {
			response = response[:idx]
		}
	}

	response = strings.TrimSpace(response)

	// Parse JSON
	var judgment Judgment
	if err := json.Unmarshal([]byte(response), &judgment); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Validate score range
	if judgment.Score < 0 {
		judgment.Score = 0
	}
	if judgment.Score > 1 {
		judgment.Score = 1
	}

	return &judgment, nil
}
