package assert

import (
	"fmt"
	"strings"
)

// AskedClarification asserts that the coordinator asked for clarification.
type AskedClarification struct {
	required   bool   // must ask clarification
	forbidden  bool   // must NOT ask clarification
	optional   bool   // pass either way (for tests that accept both behaviors)
	aboutTopic string // optional: clarification should mention this topic
}

// NewAskedClarification creates an AskedClarification assertion from config.
func NewAskedClarification(config Config) (Assertion, error) {
	required := false
	forbidden := false
	optional := false
	aboutTopic := config.About

	// Check direct config fields first
	if config.Required != nil {
		required = *config.Required
	}
	if config.Forbidden != nil {
		forbidden = *config.Forbidden
	}
	if config.Optional != nil {
		optional = *config.Optional
	}

	// Fall back to params for backwards compatibility
	if config.Params != nil {
		if r, ok := config.Params["required"].(bool); ok {
			required = r
		}
		if f, ok := config.Params["forbidden"].(bool); ok {
			forbidden = f
		}
		if o, ok := config.Params["optional"].(bool); ok {
			optional = o
		}
		if t, ok := config.Params["about"].(string); ok {
			aboutTopic = t
		}
	}

	// Default to required if none specified
	if !required && !forbidden && !optional {
		required = true
	}

	return &AskedClarification{
		required:   required,
		forbidden:  forbidden,
		optional:   optional,
		aboutTopic: aboutTopic,
	}, nil
}

func (a *AskedClarification) Type() string { return "asked-clarification" }

func (a *AskedClarification) Evaluate(ctx *Context) Result {
	hasClarification := len(ctx.Clarifications) > 0

	// Optional mode: pass either way, just report what happened
	if a.optional {
		if hasClarification {
			return Result{
				Type:   a.Type(),
				Passed: true,
				Score:  1.0,
				Reason: "coordinator asked for clarification (optional)",
			}
		}
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: "coordinator did not ask for clarification (optional)",
		}
	}

	if a.required && !hasClarification {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: "coordinator should have asked for clarification but didn't",
		}
	}

	if a.forbidden && hasClarification {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("coordinator asked clarification when it shouldn't: %q", ctx.Clarifications[0]),
		}
	}

	// Check topic if specified
	if a.aboutTopic != "" && hasClarification {
		topicFound := false
		for _, q := range ctx.Clarifications {
			if strings.Contains(strings.ToLower(q), strings.ToLower(a.aboutTopic)) {
				topicFound = true
				break
			}
		}
		if !topicFound {
			return Result{
				Type:   a.Type(),
				Passed: false,
				Score:  0.5,
				Reason: fmt.Sprintf("asked clarification but not about %q", a.aboutTopic),
			}
		}
	}

	if a.required && hasClarification {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: "coordinator correctly asked for clarification",
		}
	}

	if a.forbidden && !hasClarification {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: "coordinator correctly did not ask for clarification",
		}
	}

	return Result{
		Type:   a.Type(),
		Passed: true,
		Score:  1.0,
		Reason: "clarification behavior as expected",
	}
}
