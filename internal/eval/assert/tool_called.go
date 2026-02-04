package assert

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ToolCalled checks if a specific tool was called with optional parameter matching.
type ToolCalled struct {
	toolName string
	count    *int // nil means "at least once"
	minCount *int
	maxCount *int
	params   map[string]ParamMatcher
}

// ParamMatcher defines how to match a tool parameter.
type ParamMatcher struct {
	Exact    any    `yaml:"exact,omitempty"`
	Contains string `yaml:"contains,omitempty"`
	Regex    string `yaml:"regex,omitempty"`
}

// NewToolCalled creates a ToolCalled assertion from config.
func NewToolCalled(config Config) (Assertion, error) {
	if config.Tool == "" {
		return nil, fmt.Errorf("tool-called assertion requires 'tool' field")
	}

	a := &ToolCalled{
		toolName: config.Tool,
		count:    config.Count,
		minCount: config.MinCount,
		maxCount: config.MaxCount,
	}

	// Parse param matchers from config
	if len(config.Params) > 0 {
		a.params = make(map[string]ParamMatcher)
		for key, val := range config.Params {
			pm, err := parseParamMatcher(val)
			if err != nil {
				return nil, fmt.Errorf("param %q: %w", key, err)
			}
			a.params[key] = pm
		}
	}

	return a, nil
}

func parseParamMatcher(val any) (ParamMatcher, error) {
	switch v := val.(type) {
	case string:
		// Simple string value = exact match
		return ParamMatcher{Exact: v}, nil
	case map[string]any:
		pm := ParamMatcher{}
		if exact, ok := v["exact"]; ok {
			pm.Exact = exact
		}
		if contains, ok := v["contains"].(string); ok {
			pm.Contains = contains
		}
		if regex, ok := v["regex"].(string); ok {
			pm.Regex = regex
		}
		return pm, nil
	default:
		// Treat as exact match for other types
		return ParamMatcher{Exact: v}, nil
	}
}

func (a *ToolCalled) Type() string { return "tool-called" }

func (a *ToolCalled) Evaluate(ctx *Context) Result {
	// Find all calls to the specified tool
	var matches []RecordedToolCall
	for _, tc := range ctx.ToolCalls {
		if tc.Name == a.toolName {
			matches = append(matches, tc)
		}
	}

	// Check count constraints
	callCount := len(matches)

	if a.count != nil && callCount != *a.count {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("tool %q called %d times, expected exactly %d", a.toolName, callCount, *a.count),
		}
	}

	if a.minCount != nil && callCount < *a.minCount {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("tool %q called %d times, expected at least %d", a.toolName, callCount, *a.minCount),
		}
	}

	if a.maxCount != nil && callCount > *a.maxCount {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("tool %q called %d times, expected at most %d", a.toolName, callCount, *a.maxCount),
		}
	}

	// Default: at least one call required
	if a.count == nil && a.minCount == nil && callCount == 0 {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("tool %q was not called", a.toolName),
		}
	}

	// Check parameter matching if specified
	if len(a.params) > 0 {
		anyMatch := false
		var paramErrors []string

		for _, tc := range matches {
			matchResult, err := a.matchParams(tc.Args)
			if matchResult {
				anyMatch = true
				break
			}
			if err != "" {
				paramErrors = append(paramErrors, err)
			}
		}

		if !anyMatch {
			return Result{
				Type:   a.Type(),
				Passed: false,
				Score:  0.5, // Partial credit - tool called but params wrong
				Reason: fmt.Sprintf("tool %q called but params didn't match: %s", a.toolName, strings.Join(paramErrors, "; ")),
			}
		}
	}

	return Result{
		Type:   a.Type(),
		Passed: true,
		Score:  1.0,
		Reason: fmt.Sprintf("tool %q called %d time(s) with matching params", a.toolName, callCount),
	}
}

func (a *ToolCalled) matchParams(args map[string]any) (bool, string) {
	for key, matcher := range a.params {
		argVal, exists := args[key]
		if !exists {
			return false, fmt.Sprintf("param %q not present", key)
		}

		if !matchValue(argVal, matcher) {
			return false, fmt.Sprintf("param %q value %v didn't match", key, argVal)
		}
	}
	return true, ""
}

func matchValue(val any, matcher ParamMatcher) bool {
	// Convert value to string for matching
	var strVal string
	switch v := val.(type) {
	case string:
		strVal = v
	default:
		data, _ := json.Marshal(v)
		strVal = string(data)
	}

	// Check exact match
	if matcher.Exact != nil {
		switch e := matcher.Exact.(type) {
		case string:
			if strVal != e {
				return false
			}
		default:
			exactJSON, _ := json.Marshal(e)
			if strVal != string(exactJSON) {
				return false
			}
		}
	}

	// Check contains
	if matcher.Contains != "" && !strings.Contains(strVal, matcher.Contains) {
		return false
	}

	// Check regex
	if matcher.Regex != "" {
		re, err := regexp.Compile(matcher.Regex)
		if err != nil {
			return false
		}
		if !re.MatchString(strVal) {
			return false
		}
	}

	return true
}

// ToolSequence checks if tools were called in a specific order.
type ToolSequence struct {
	sequence []string
}

// NewToolSequence creates a ToolSequence assertion from config.
func NewToolSequence(config Config) (Assertion, error) {
	if len(config.Sequence) == 0 {
		return nil, fmt.Errorf("tool-sequence assertion requires 'sequence' field")
	}
	return &ToolSequence{sequence: config.Sequence}, nil
}

func (a *ToolSequence) Type() string { return "tool-sequence" }

func (a *ToolSequence) Evaluate(ctx *Context) Result {
	// Extract tool names in order
	var actualSequence []string
	for _, tc := range ctx.ToolCalls {
		actualSequence = append(actualSequence, tc.Name)
	}

	// Check if expected sequence is a subsequence of actual
	// (tools can be called in between, but order must be preserved)
	expectedIdx := 0
	for _, tool := range actualSequence {
		if expectedIdx < len(a.sequence) && tool == a.sequence[expectedIdx] {
			expectedIdx++
		}
	}

	if expectedIdx == len(a.sequence) {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: fmt.Sprintf("tools called in expected order: %v", a.sequence),
		}
	}

	// Calculate partial score based on how many expected tools were found in order
	score := float64(expectedIdx) / float64(len(a.sequence))

	return Result{
		Type:   a.Type(),
		Passed: false,
		Score:  score,
		Reason: fmt.Sprintf("expected sequence %v, found %d/%d in order (actual: %v)",
			a.sequence, expectedIdx, len(a.sequence), actualSequence),
	}
}
