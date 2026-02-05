// Package eval provides a framework for evaluating agent performance.
package eval

import (
	"encoding/json"
	"time"

	"github.com/yourusername/kaggen/internal/eval/assert"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Type aliases for assert types used in this package.
type (
	AssertConfig     = assert.Config
	AssertContext    = assert.Context
	AssertResult     = assert.Result
	RecordedToolCall = assert.RecordedToolCall
)

// ConversationTurn defines a single turn in a multi-turn evaluation.
// Each turn has a user message and optional assertions to run after that turn.
type ConversationTurn struct {
	// User message to send
	User string `yaml:"user" json:"user"`

	// Assertions to run after this turn completes
	Assert []AssertConfig `yaml:"assert,omitempty" json:"assert,omitempty"`
}

// EvalCase defines a single evaluation test case loaded from YAML.
type EvalCase struct {
	ID          string       `yaml:"id" json:"id"`
	Name        string       `yaml:"name" json:"name"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	Category    string       `yaml:"category,omitempty" json:"category,omitempty"`

	// Input - Single turn (backward compatible)
	SystemPrompt string       `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	UserMessage  string       `yaml:"user_message,omitempty" json:"user_message,omitempty"`
	Context      *CaseContext `yaml:"context,omitempty" json:"context,omitempty"`

	// Input - Multi-turn conversation
	// If Turns is set, UserMessage is ignored
	Turns []ConversationTurn `yaml:"turns,omitempty" json:"turns,omitempty"`

	// Expected behavior - list of assertions (for single-turn mode)
	// In multi-turn mode, use assertions within each turn
	Assert []AssertConfig `yaml:"assert,omitempty" json:"assert,omitempty"`

	// Execution constraints
	MaxTurns int           `yaml:"max_turns,omitempty" json:"max_turns,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// IsMultiTurn returns true if this case uses multi-turn conversation format.
func (c *EvalCase) IsMultiTurn() bool {
	return len(c.Turns) > 0
}

// GetConversationTurns returns the conversation turns for this case.
// For single-turn cases, it wraps UserMessage and Assert into a single turn.
func (c *EvalCase) GetConversationTurns() []ConversationTurn {
	if c.IsMultiTurn() {
		return c.Turns
	}
	// Wrap single-turn format into multi-turn structure
	return []ConversationTurn{
		{
			User:   c.UserMessage,
			Assert: c.Assert,
		},
	}
}

// CaseContext defines the workspace context for a test case.
type CaseContext struct {
	// Files to create in the workspace before running
	Files map[string]string `yaml:"files,omitempty" json:"files,omitempty"`

	// Environment variables to set
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Working directory (relative to workspace)
	WorkDir string `yaml:"work_dir,omitempty" json:"work_dir,omitempty"`
}

// TraceEvent represents a single event in the coordinator's execution trace.
// Used for debugging excessive turn counts and understanding coordinator behavior.
type TraceEvent struct {
	Turn      int            `json:"turn"`
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"` // "text", "tool_call"
	Content   string         `json:"content,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolArgs  map[string]any `json:"tool_args,omitempty"`
}

// TurnResult captures the outcome of a single conversation turn.
type TurnResult struct {
	TurnIndex   int            `json:"turn_index"`
	UserMessage string         `json:"user_message"`
	Response    string         `json:"response"`
	Assertions  []AssertResult `json:"assertions,omitempty"`
	Passed      bool           `json:"passed"`
	ToolCalls   []RecordedToolCall `json:"tool_calls,omitempty"`
}

// EvalResult captures the outcome of running one test case.
type EvalResult struct {
	CaseID   string `json:"case_id"`
	CaseName string `json:"case_name"`
	Passed   bool   `json:"passed"`

	// Individual assertion results (aggregated from all turns)
	Assertions []AssertResult `json:"assertions"`

	// Per-turn results for multi-turn conversations
	TurnResults []TurnResult `json:"turn_results,omitempty"`

	// Composite score (weighted average of assertion scores)
	Score float64 `json:"score"`

	// Execution metrics
	TurnCount  int           `json:"turn_count"`
	TokensUsed int           `json:"tokens_used"`
	Duration   time.Duration `json:"duration"`

	// Recorded data for debugging
	ToolCalls   []RecordedToolCall `json:"tool_calls"`
	FinalOutput string             `json:"final_output"`

	// Execution trace for debugging (turn-by-turn log of coordinator behavior)
	ExecutionTrace []TraceEvent `json:"execution_trace,omitempty"`

	// Errors during execution (not assertion failures)
	Errors []string `json:"errors,omitempty"`
}

// EvalSummary aggregates results across multiple test cases.
type EvalSummary struct {
	RunID     string    `json:"run_id"`
	Timestamp time.Time `json:"timestamp"`
	Config    RunConfig `json:"config"`

	// Aggregate metrics
	TotalCases  int     `json:"total_cases"`
	PassedCases int     `json:"passed_cases"`
	PassRate    float64 `json:"pass_rate"`
	AvgScore    float64 `json:"avg_score"`

	// By category
	CategoryScores map[string]CategoryScore `json:"category_scores,omitempty"`

	// Individual results
	Results []EvalResult `json:"results"`
}

// CategoryScore aggregates scores for a category.
type CategoryScore struct {
	Category    string  `json:"category"`
	TotalCases  int     `json:"total_cases"`
	PassedCases int     `json:"passed_cases"`
	PassRate    float64 `json:"pass_rate"`
	AvgScore    float64 `json:"avg_score"`
}

// RunConfig captures the configuration used for an eval run.
type RunConfig struct {
	ModelName    string        `json:"model_name"`
	MaxTurns     int           `json:"max_turns"`
	Timeout      time.Duration `json:"timeout"`
	SuitePath    string        `json:"suite_path"`
	ReplayFile   string        `json:"replay_file,omitempty"`
	RecordOutput string        `json:"record_output,omitempty"`
}

// Turn represents one model interaction in a replay trace.
type Turn struct {
	Request  *model.Request  `json:"request"`
	Response *model.Response `json:"response"`
}

// Trace is a complete recording of model interactions for one eval case.
type Trace struct {
	CaseID    string    `json:"case_id"`
	Timestamp time.Time `json:"timestamp"`
	Model     string    `json:"model"`
	Turns     []Turn    `json:"turns"`
}

// MarshalJSON implements custom JSON marshaling for Duration fields.
func (r EvalResult) MarshalJSON() ([]byte, error) {
	type Alias EvalResult
	return json.Marshal(&struct {
		Duration string `json:"duration"`
		*Alias
	}{
		Duration: r.Duration.String(),
		Alias:    (*Alias)(&r),
	})
}
