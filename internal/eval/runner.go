package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yourusername/kaggen/internal/eval/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Runner executes evaluation test cases.
type Runner struct {
	// Model to use for the agent being evaluated
	model model.Model

	// Model to use for LLM-as-judge (can be the same or different)
	judgeModel model.Model

	// Tools available to the agent
	tools []tool.Tool

	// Base system instruction (can be overridden per case)
	systemInstruction string

	// Default configuration
	config RunConfig
}

// NewRunner creates a new evaluation runner.
func NewRunner(opts ...RunnerOption) *Runner {
	r := &Runner{
		config: RunConfig{
			MaxTurns: 10,
			Timeout:  2 * time.Minute,
		},
	}
	for _, opt := range opts {
		opt(r)
	}

	// Set the judge model for LLM-as-judge assertions
	if r.judgeModel != nil {
		assert.DefaultJudgeModel = r.judgeModel
	}

	return r
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithModel sets the model to evaluate.
func WithModel(m model.Model) RunnerOption {
	return func(r *Runner) { r.model = m }
}

// WithJudgeModel sets the model for LLM-as-judge assertions.
func WithJudgeModel(m model.Model) RunnerOption {
	return func(r *Runner) { r.judgeModel = m }
}

// WithTools sets the tools available to the agent.
func WithTools(tools []tool.Tool) RunnerOption {
	return func(r *Runner) { r.tools = tools }
}

// WithSystemInstruction sets the default system instruction.
func WithSystemInstruction(inst string) RunnerOption {
	return func(r *Runner) { r.systemInstruction = inst }
}

// WithConfig sets the run configuration.
func WithConfig(cfg RunConfig) RunnerOption {
	return func(r *Runner) { r.config = cfg }
}

// RunCase executes a single test case and returns the result.
func (r *Runner) RunCase(ctx context.Context, tc EvalCase) (*EvalResult, error) {
	startTime := time.Now()

	// Set up workspace
	workspace, err := r.setupWorkspace(tc)
	if err != nil {
		return nil, fmt.Errorf("setup workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	// Create tool collector to capture tool calls
	collector := &toolCollector{}

	// Wrap tools with collector
	wrappedTools := r.wrapToolsWithCollector(collector, workspace)

	// Determine system instruction
	systemInstruction := r.systemInstruction
	if tc.SystemPrompt != "" {
		systemInstruction = tc.SystemPrompt
	}

	// Create agent
	ag := llmagent.New("eval-agent",
		llmagent.WithModel(r.model),
		llmagent.WithTools(wrappedTools),
		llmagent.WithInstruction(systemInstruction),
		llmagent.WithMaxLLMCalls(tc.MaxTurns),
	)

	// Set up timeout
	timeout := r.config.Timeout
	if tc.Timeout > 0 {
		timeout = tc.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create invocation
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(ag),
		agent.WithInvocationMessage(model.Message{
			Role:    model.RoleUser,
			Content: tc.UserMessage,
		}),
		agent.WithInvocationModel(r.model),
		agent.WithInvocationSession(&trpcsession.Session{
			ID:     "eval-" + tc.ID,
			UserID: "eval-user",
		}),
	)
	ctx = agent.NewInvocationContext(ctx, inv)

	// Run agent
	evCh, err := ag.Run(ctx, inv)
	if err != nil {
		return &EvalResult{
			CaseID:   tc.ID,
			CaseName: tc.Name,
			Passed:   false,
			Errors:   []string{fmt.Sprintf("agent run failed: %v", err)},
			Duration: time.Since(startTime),
		}, nil
	}

	// Collect events and final response
	var finalOutput string
	var turnCount int
	for evt := range evCh {
		if evt != nil && evt.Response != nil {
			turnCount++
			if len(evt.Response.Choices) > 0 {
				content := evt.Response.Choices[0].Message.Content
				if content != "" && !evt.Response.IsToolCallResponse() {
					finalOutput = content
				}
			}
		}
	}

	duration := time.Since(startTime)

	// Build assertion context
	assertCtx := &AssertContext{
		Instruction: tc.UserMessage,
		Response:    finalOutput,
		ToolCalls:   collector.calls,
		TurnCount:   turnCount,
		Duration:    duration,
	}

	// Parse and run assertions
	assertions, err := assert.FromConfigs(tc.Assert)
	if err != nil {
		return &EvalResult{
			CaseID:   tc.ID,
			CaseName: tc.Name,
			Passed:   false,
			Errors:   []string{fmt.Sprintf("parse assertions: %v", err)},
			Duration: duration,
		}, nil
	}

	assertResults := assert.EvaluateAll(assertions, assertCtx)
	passed := assert.AllPassed(assertResults)
	score := assert.ComputeScore(assertResults)

	return &EvalResult{
		CaseID:      tc.ID,
		CaseName:    tc.Name,
		Passed:      passed,
		Score:       score,
		Assertions:  assertResults,
		TurnCount:   turnCount,
		Duration:    duration,
		ToolCalls:   collector.calls,
		FinalOutput: finalOutput,
	}, nil
}

// RunSuite executes all test cases and returns a summary.
func (r *Runner) RunSuite(ctx context.Context, cases []EvalCase) (*EvalSummary, error) {
	summary := &EvalSummary{
		RunID:          uuid.New().String(),
		Timestamp:      time.Now(),
		Config:         r.config,
		TotalCases:     len(cases),
		Results:        make([]EvalResult, 0, len(cases)),
		CategoryScores: make(map[string]CategoryScore),
	}

	for _, tc := range cases {
		result, err := r.RunCase(ctx, tc)
		if err != nil {
			// Record error as failed result
			result = &EvalResult{
				CaseID:   tc.ID,
				CaseName: tc.Name,
				Passed:   false,
				Errors:   []string{err.Error()},
			}
		}

		summary.Results = append(summary.Results, *result)

		if result.Passed {
			summary.PassedCases++
		}

		// Update category scores
		if tc.Category != "" {
			cat := summary.CategoryScores[tc.Category]
			cat.Category = tc.Category
			cat.TotalCases++
			if result.Passed {
				cat.PassedCases++
			}
			cat.AvgScore = (cat.AvgScore*float64(cat.TotalCases-1) + result.Score) / float64(cat.TotalCases)
			summary.CategoryScores[tc.Category] = cat
		}
	}

	// Calculate overall metrics
	if summary.TotalCases > 0 {
		summary.PassRate = float64(summary.PassedCases) / float64(summary.TotalCases)

		var totalScore float64
		for _, r := range summary.Results {
			totalScore += r.Score
		}
		summary.AvgScore = totalScore / float64(len(summary.Results))
	}

	// Calculate category pass rates
	for name, cat := range summary.CategoryScores {
		if cat.TotalCases > 0 {
			cat.PassRate = float64(cat.PassedCases) / float64(cat.TotalCases)
			summary.CategoryScores[name] = cat
		}
	}

	return summary, nil
}

// setupWorkspace creates a temporary workspace with context files.
func (r *Runner) setupWorkspace(tc EvalCase) (string, error) {
	workspace, err := os.MkdirTemp("", "eval-workspace-*")
	if err != nil {
		return "", err
	}

	if tc.Context != nil && len(tc.Context.Files) > 0 {
		for path, content := range tc.Context.Files {
			fullPath := filepath.Join(workspace, path)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				os.RemoveAll(workspace)
				return "", fmt.Errorf("create dir for %s: %w", path, err)
			}
			if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
				os.RemoveAll(workspace)
				return "", fmt.Errorf("write %s: %w", path, err)
			}
		}
	}

	return workspace, nil
}

// wrapToolsWithCollector wraps tools to capture calls.
func (r *Runner) wrapToolsWithCollector(collector *toolCollector, workspace string) []tool.Tool {
	// For now, return the original tools
	// In a full implementation, we'd wrap each tool to intercept calls
	// The collector is populated via the session events
	return r.tools
}

// toolCollector captures tool calls during execution.
type toolCollector struct {
	mu    sync.Mutex
	calls []RecordedToolCall
}

func (c *toolCollector) record(call RecordedToolCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}
