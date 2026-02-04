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

// EvalCase defines a single evaluation test case loaded from YAML.
type EvalCase struct {
	ID          string       `yaml:"id" json:"id"`
	Name        string       `yaml:"name" json:"name"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty"`
	Category    string       `yaml:"category,omitempty" json:"category,omitempty"`

	// Input
	SystemPrompt string       `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	UserMessage  string       `yaml:"user_message" json:"user_message"`
	Context      *CaseContext `yaml:"context,omitempty" json:"context,omitempty"`

	// Expected behavior - list of assertions
	Assert []AssertConfig `yaml:"assert" json:"assert"`

	// Execution constraints
	MaxTurns int           `yaml:"max_turns,omitempty" json:"max_turns,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
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

// EvalResult captures the outcome of running one test case.
type EvalResult struct {
	CaseID   string `json:"case_id"`
	CaseName string `json:"case_name"`
	Passed   bool   `json:"passed"`

	// Individual assertion results
	Assertions []AssertResult `json:"assertions"`

	// Composite score (weighted average of assertion scores)
	Score float64 `json:"score"`

	// Execution metrics
	TurnCount  int           `json:"turn_count"`
	TokensUsed int           `json:"tokens_used"`
	Duration   time.Duration `json:"duration"`

	// Recorded data for debugging
	ToolCalls   []RecordedToolCall `json:"tool_calls"`
	FinalOutput string             `json:"final_output"`

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
