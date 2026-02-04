// Package assert provides assertion types for evaluating agent behavior.
package assert

import (
	"fmt"
	"time"
)

// RecordedToolCall captures a tool invocation during evaluation.
type RecordedToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Args      map[string]any `json:"args"`
	Result    string         `json:"result,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Duration  time.Duration  `json:"duration,omitempty"`
}

// Context provides context for assertion evaluation.
type Context struct {
	// Original user message (instruction)
	Instruction string

	// Final agent response
	Response string

	// All tool calls made during execution
	ToolCalls []RecordedToolCall

	// Execution metrics
	TurnCount  int
	TokensUsed int
	Duration   time.Duration
}

// Result is the outcome of evaluating one assertion.
type Result struct {
	Type   string  `json:"type"`
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"` // 0.0 to 1.0
	Reason string  `json:"reason"`
}

// Config is the YAML representation of an assertion.
type Config struct {
	Type string `yaml:"type" json:"type"`

	// Common fields used by multiple assertion types
	Value    string         `yaml:"value,omitempty" json:"value,omitempty"`
	Tool     string         `yaml:"tool,omitempty" json:"tool,omitempty"`
	Params   map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
	Rubric   string         `yaml:"rubric,omitempty" json:"rubric,omitempty"`
	MinScore float64        `yaml:"min_score,omitempty" json:"min_score,omitempty"`
	Sequence []string       `yaml:"sequence,omitempty" json:"sequence,omitempty"`
	Count    *int           `yaml:"count,omitempty" json:"count,omitempty"`
	MinCount *int           `yaml:"min_count,omitempty" json:"min_count,omitempty"`
	MaxCount *int           `yaml:"max_count,omitempty" json:"max_count,omitempty"`
}

// Assertion evaluates one aspect of agent behavior.
type Assertion interface {
	// Type returns the assertion type name (e.g., "contains", "tool-called").
	Type() string

	// Evaluate runs the assertion against the given context.
	Evaluate(ctx *Context) Result
}

// Registry maps assertion type names to factory functions.
var Registry = map[string]func(config Config) (Assertion, error){
	"contains":      NewContains,
	"not-contains":  NewNotContains,
	"regex":         NewRegex,
	"tool-called":   NewToolCalled,
	"tool-sequence": NewToolSequence,
	"llm-rubric":    NewLLMRubric,
}

// FromConfig creates an Assertion from a YAML config.
func FromConfig(config Config) (Assertion, error) {
	factory, ok := Registry[config.Type]
	if !ok {
		return nil, fmt.Errorf("unknown assertion type: %q", config.Type)
	}
	return factory(config)
}

// FromConfigs creates multiple Assertions from YAML configs.
func FromConfigs(configs []Config) ([]Assertion, error) {
	assertions := make([]Assertion, 0, len(configs))
	for i, cfg := range configs {
		a, err := FromConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("assertion %d: %w", i, err)
		}
		assertions = append(assertions, a)
	}
	return assertions, nil
}

// EvaluateAll runs all assertions and returns results.
func EvaluateAll(assertions []Assertion, ctx *Context) []Result {
	results := make([]Result, 0, len(assertions))
	for _, a := range assertions {
		results = append(results, a.Evaluate(ctx))
	}
	return results
}

// AllPassed returns true if all assertions passed.
func AllPassed(results []Result) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// ComputeScore calculates a weighted average score from assertion results.
// By default, all assertions are weighted equally.
func ComputeScore(results []Result) float64 {
	if len(results) == 0 {
		return 0.0
	}
	var total float64
	for _, r := range results {
		total += r.Score
	}
	return total / float64(len(results))
}
