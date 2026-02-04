package assert

import (
	"fmt"
	"regexp"
	"strings"
)

// Contains checks if the response contains a specific string.
type Contains struct {
	value string
}

// NewContains creates a Contains assertion from config.
func NewContains(config Config) (Assertion, error) {
	if config.Value == "" {
		return nil, fmt.Errorf("contains assertion requires 'value' field")
	}
	return &Contains{value: config.Value}, nil
}

func (a *Contains) Type() string { return "contains" }

func (a *Contains) Evaluate(ctx *Context) Result {
	if strings.Contains(ctx.Response, a.value) {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: fmt.Sprintf("response contains %q", a.value),
		}
	}
	return Result{
		Type:   a.Type(),
		Passed: false,
		Score:  0.0,
		Reason: fmt.Sprintf("response does not contain %q", a.value),
	}
}

// NotContains checks if the response does NOT contain a specific string.
type NotContains struct {
	value string
}

// NewNotContains creates a NotContains assertion from config.
func NewNotContains(config Config) (Assertion, error) {
	if config.Value == "" {
		return nil, fmt.Errorf("not-contains assertion requires 'value' field")
	}
	return &NotContains{value: config.Value}, nil
}

func (a *NotContains) Type() string { return "not-contains" }

func (a *NotContains) Evaluate(ctx *Context) Result {
	if !strings.Contains(ctx.Response, a.value) {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: fmt.Sprintf("response does not contain %q", a.value),
		}
	}
	return Result{
		Type:   a.Type(),
		Passed: false,
		Score:  0.0,
		Reason: fmt.Sprintf("response contains forbidden string %q", a.value),
	}
}

// Regex checks if the response matches a regular expression.
type Regex struct {
	pattern *regexp.Regexp
	raw     string
}

// NewRegex creates a Regex assertion from config.
func NewRegex(config Config) (Assertion, error) {
	if config.Value == "" {
		return nil, fmt.Errorf("regex assertion requires 'value' field with pattern")
	}
	re, err := regexp.Compile(config.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern %q: %w", config.Value, err)
	}
	return &Regex{pattern: re, raw: config.Value}, nil
}

func (a *Regex) Type() string { return "regex" }

func (a *Regex) Evaluate(ctx *Context) Result {
	if a.pattern.MatchString(ctx.Response) {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: fmt.Sprintf("response matches pattern /%s/", a.raw),
		}
	}
	return Result{
		Type:   a.Type(),
		Passed: false,
		Score:  0.0,
		Reason: fmt.Sprintf("response does not match pattern /%s/", a.raw),
	}
}
