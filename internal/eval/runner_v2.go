package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/yourusername/kaggen/internal/eval/assert"
	"github.com/yourusername/kaggen/internal/eval/sut"
)

// ResultCallback is called after each test case completes.
// This allows incremental processing of results (e.g., writing traces).
type ResultCallback func(result *EvalResult, tc EvalCase)

// RunnerV2 executes evaluation cases against the production coordinator system.
type RunnerV2 struct {
	// Model to use for the coordinator and sub-agents
	model model.Model

	// Model to use for LLM-as-judge assertions
	judgeModel model.Model

	// Skills directory for test skills
	skillsDir string

	// Default configuration
	config RunConfigV2

	// Callback called after each test case completes
	resultCallback ResultCallback

	// Logger for progress output
	logger *slog.Logger
}

// RunConfigV2 configures the V2 runner.
type RunConfigV2 struct {
	// Model name for reporting
	ModelName string

	// Maximum coordinator turns
	MaxTurns int

	// Timeout per test case
	Timeout time.Duration

	// Skills directory
	SkillsDir string

	// Suite path for reporting
	SuitePath string
}

// NewRunnerV2 creates a new V2 evaluation runner.
func NewRunnerV2(opts ...RunnerV2Option) *RunnerV2 {
	r := &RunnerV2{
		config: RunConfigV2{
			MaxTurns: 25,
			Timeout:  5 * time.Minute,
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

// RunnerV2Option configures a RunnerV2.
type RunnerV2Option func(*RunnerV2)

// WithModelV2 sets the model for the coordinator.
func WithModelV2(m model.Model) RunnerV2Option {
	return func(r *RunnerV2) { r.model = m }
}

// WithJudgeModelV2 sets the model for LLM-as-judge assertions.
func WithJudgeModelV2(m model.Model) RunnerV2Option {
	return func(r *RunnerV2) { r.judgeModel = m }
}

// WithSkillsDir sets the skills directory.
func WithSkillsDir(dir string) RunnerV2Option {
	return func(r *RunnerV2) { r.skillsDir = dir }
}

// WithConfigV2 sets the run configuration.
func WithConfigV2(cfg RunConfigV2) RunnerV2Option {
	return func(r *RunnerV2) { r.config = cfg }
}

// WithResultCallback sets a callback that's called after each test case completes.
// Use this for incremental trace writing or progress reporting.
func WithResultCallback(cb ResultCallback) RunnerV2Option {
	return func(r *RunnerV2) { r.resultCallback = cb }
}

// WithLoggerV2 sets the logger for progress output.
func WithLoggerV2(l *slog.Logger) RunnerV2Option {
	return func(r *RunnerV2) { r.logger = l }
}

// RunCaseV2 executes a single test case against the production coordinator system.
func (r *RunnerV2) RunCaseV2(ctx context.Context, tc EvalCase) (*EvalResult, error) {
	startTime := time.Now()

	// Create temporary workspace for this test case
	workspace, err := os.MkdirTemp("", "eval-v2-*")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	// Set up context files in workspace
	if tc.Context != nil && len(tc.Context.Files) > 0 {
		for path, content := range tc.Context.Files {
			fullPath := filepath.Join(workspace, path)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return nil, fmt.Errorf("create dir for %s: %w", path, err)
			}
			if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
				return nil, fmt.Errorf("write %s: %w", path, err)
			}
		}
	}

	// Determine skills directory
	skillsDir := r.skillsDir
	if skillsDir == "" {
		skillsDir = r.config.SkillsDir
	}

	// Create the System Under Test (full production coordinator + sub-agents)
	system, err := sut.New(sut.Config{
		Model:     r.model,
		Workspace: workspace,
		SkillsDir: skillsDir,
		MaxTurns:  r.config.MaxTurns,
		Timeout:   r.config.Timeout,
	})
	if err != nil {
		return &EvalResult{
			CaseID:   tc.ID,
			CaseName: tc.Name,
			Passed:   false,
			Errors:   []string{fmt.Sprintf("failed to create SUT: %v", err)},
			Duration: time.Since(startTime),
		}, nil
	}
	defer system.Cleanup()

	// Set up timeout
	timeout := r.config.Timeout
	if tc.Timeout > 0 {
		timeout = tc.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Get conversation turns (handles both single-turn and multi-turn formats)
	conversationTurns := tc.GetConversationTurns()

	// Session ID for this test case - the trpc runner handles session persistence
	// automatically via the inmemory session service created in the SUT
	sessionID := "eval-v2-" + tc.ID
	userID := "eval-user"

	// Aggregate results across all conversation turns
	var allAssertResults []AssertResult
	var allToolCalls []RecordedToolCall
	var allTrace []TraceEvent
	var turnResults []TurnResult
	var totalTurnCount int
	var finalOutput string
	allPassed := true

	// Process each conversation turn
	for turnIdx, convTurn := range conversationTurns {
		// Create observer for this turn
		observer := NewCoordinatorObserver()
		if len(system.SkillNames) > 0 {
			observer.SetSkillNames(system.SkillNames)
		}

		// Create user message for this turn
		userMessage := model.NewUserMessage(convTurn.User)

		// Run the coordinator using the trpc runner.
		// The runner automatically loads session history from the inmemory session service,
		// preserving conversation context across turns (matching production behavior).
		evCh, err := system.Runner.Run(ctx, userID, sessionID, userMessage)
		if err != nil {
			return &EvalResult{
				CaseID:      tc.ID,
				CaseName:    tc.Name,
				Passed:      false,
				Errors:      []string{fmt.Sprintf("turn %d: coordinator run failed: %v", turnIdx+1, err)},
				Duration:    time.Since(startTime),
				TurnResults: turnResults,
			}, nil
		}

		// Collect events for this turn
		var turnOutput string
		var turnToolCalls []RecordedToolCall
		var turnTrace []TraceEvent
		var turnEventCount int

		for evt := range evCh {
			if evt == nil {
				continue
			}

			if evt.Response != nil {
				turnEventCount++
				totalTurnCount++

				if len(evt.Response.Choices) > 0 {
					msg := evt.Response.Choices[0].Message

					// Extract text content
					if msg.Content != "" {
						turnOutput = msg.Content
						finalOutput = msg.Content
						observer.RecordResponse(msg.Content)

						turnTrace = append(turnTrace, TraceEvent{
							Turn:      totalTurnCount,
							Timestamp: time.Now(),
							Type:      "text",
							Content:   msg.Content,
						})
					}

					// Extract tool calls
					for _, toolCall := range msg.ToolCalls {
						var args map[string]any
						if len(toolCall.Function.Arguments) > 0 {
							json.Unmarshal(toolCall.Function.Arguments, &args)
						}

						recorded := RecordedToolCall{
							ID:   toolCall.ID,
							Name: toolCall.Function.Name,
							Args: args,
						}
						turnToolCalls = append(turnToolCalls, recorded)
						observer.RecordToolCall(toolCall.ID, toolCall.Function.Name, args)

						turnTrace = append(turnTrace, TraceEvent{
							Turn:      totalTurnCount,
							Timestamp: time.Now(),
							Type:      "tool_call",
							ToolName:  toolCall.Function.Name,
							ToolArgs:  args,
						})
					}
				}
			}
		}

		observer.Finish()

		// Accumulate tool calls and trace
		allToolCalls = append(allToolCalls, turnToolCalls...)
		allTrace = append(allTrace, turnTrace...)

		// Run assertions for this turn if any are defined
		var turnAssertResults []AssertResult
		turnPassed := true

		if len(convTurn.Assert) > 0 {
			assertCtx := &assert.Context{
				Instruction:      convTurn.User,
				Response:         turnOutput,
				ToolCalls:        convertToolCalls(turnToolCalls),
				TurnCount:        turnEventCount,
				Duration:         time.Since(startTime),
				SkillsDispatched: convertSkillsDispatched(observer.SkillsDispatched),
				Clarifications:   observer.Clarifications,
				AllResponses:     observer.Responses,
			}

			assertions, err := assert.FromConfigs(convTurn.Assert)
			if err != nil {
				return &EvalResult{
					CaseID:      tc.ID,
					CaseName:    tc.Name,
					Passed:      false,
					Errors:      []string{fmt.Sprintf("turn %d: parse assertions: %v", turnIdx+1, err)},
					Duration:    time.Since(startTime),
					TurnResults: turnResults,
				}, nil
			}

			turnAssertResults = assert.EvaluateAll(assertions, assertCtx)
			turnPassed = assert.AllPassed(turnAssertResults)
			allAssertResults = append(allAssertResults, turnAssertResults...)
		}

		// Record turn result
		turnResults = append(turnResults, TurnResult{
			TurnIndex:   turnIdx,
			UserMessage: convTurn.User,
			Response:    turnOutput,
			Assertions:  turnAssertResults,
			Passed:      turnPassed,
			ToolCalls:   turnToolCalls,
		})

		if !turnPassed {
			allPassed = false
			// Stop processing further turns if this turn's assertions failed
			slog.Info("Multi-turn conversation stopped due to failed assertion",
				"case", tc.ID,
				"turn", turnIdx+1,
				"totalTurns", len(conversationTurns))
			break
		}

		slog.Debug("Conversation turn completed",
			"case", tc.ID,
			"turn", turnIdx+1,
			"totalTurns", len(conversationTurns),
			"response", turnOutput[:min(len(turnOutput), 100)])
	}

	duration := time.Since(startTime)

	// Log summary
	slog.Info("Eval case complete",
		"case", tc.ID,
		"multiTurn", tc.IsMultiTurn(),
		"conversationTurns", len(turnResults),
		"totalEvents", totalTurnCount,
		"passed", allPassed)

	score := assert.ComputeScore(allAssertResults)

	return &EvalResult{
		CaseID:         tc.ID,
		CaseName:       tc.Name,
		Passed:         allPassed,
		Score:          score,
		Assertions:     allAssertResults,
		TurnResults:    turnResults,
		TurnCount:      totalTurnCount,
		Duration:       duration,
		ToolCalls:      allToolCalls,
		FinalOutput:    finalOutput,
		ExecutionTrace: allTrace,
	}, nil
}

// RunSuiteV2 executes all test cases and returns a summary.
func (r *RunnerV2) RunSuiteV2(ctx context.Context, cases []EvalCase) (*EvalSummary, error) {
	summary := &EvalSummary{
		RunID:          uuid.New().String(),
		Timestamp:      time.Now(),
		Config:         RunConfig{ModelName: r.config.ModelName, MaxTurns: r.config.MaxTurns, Timeout: r.config.Timeout, SuitePath: r.config.SuitePath},
		TotalCases:     len(cases),
		Results:        make([]EvalResult, 0, len(cases)),
		CategoryScores: make(map[string]CategoryScore),
	}

	for i, tc := range cases {
		// Log progress
		if r.logger != nil {
			r.logger.Info("Running test case", "case", tc.ID, "name", tc.Name, "progress", fmt.Sprintf("%d/%d", i+1, len(cases)))
		}

		result, err := r.RunCaseV2(ctx, tc)
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

		// Call result callback for incremental processing (e.g., trace writing)
		if r.resultCallback != nil {
			r.resultCallback(result, tc)
		}

		// Log result
		if r.logger != nil {
			status := "PASSED"
			if !result.Passed {
				status = "FAILED"
			}
			r.logger.Info("Test case completed", "case", tc.ID, "status", status, "turns", result.TurnCount, "duration", result.Duration)
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

// convertToolCalls converts internal tool calls to assert format.
func convertToolCalls(calls []RecordedToolCall) []assert.RecordedToolCall {
	result := make([]assert.RecordedToolCall, len(calls))
	for i, c := range calls {
		result[i] = assert.RecordedToolCall{
			ID:   c.ID,
			Name: c.Name,
			Args: c.Args,
		}
	}
	return result
}

// convertSkillsDispatched converts observer dispatch records to assert format.
func convertSkillsDispatched(dispatched map[string][]DispatchRecord) map[string][]assert.DispatchRecord {
	result := make(map[string][]assert.DispatchRecord)
	for skill, records := range dispatched {
		assertRecords := make([]assert.DispatchRecord, len(records))
		for i, r := range records {
			assertRecords[i] = assert.DispatchRecord{
				TaskID:    r.TaskID,
				SkillName: r.SkillName,
				Task:      r.Task,
				Timestamp: r.Timestamp,
			}
		}
		result[skill] = assertRecords
	}
	return result
}
