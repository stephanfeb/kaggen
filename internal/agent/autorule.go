package agent

import (
	"fmt"
	"regexp"
)

// AutoApproveRule is a config-level rule for auto-approving guarded tool calls.
type AutoApproveRule struct {
	Tool    string `json:"tool"`              // tool name, e.g. "Bash"
	Pattern string `json:"pattern,omitempty"` // regex matched against formatted description; empty = match all
}

// CompiledAutoRule is a pre-compiled auto-approve rule.
type CompiledAutoRule struct {
	Tool    string
	Pattern *regexp.Regexp // nil means match-all for this tool
}

// CompileAutoRules compiles config rules into regexp-backed rules.
func CompileAutoRules(rules []AutoApproveRule) ([]CompiledAutoRule, error) {
	compiled := make([]CompiledAutoRule, 0, len(rules))
	for _, r := range rules {
		c := CompiledAutoRule{Tool: r.Tool}
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid auto-approve pattern for tool %s: %w", r.Tool, err)
			}
			c.Pattern = re
		}
		compiled = append(compiled, c)
	}
	return compiled, nil
}

// MatchAutoRule returns true if any compiled rule matches the given tool and description.
func MatchAutoRule(rules []CompiledAutoRule, toolName, description string) bool {
	for _, r := range rules {
		if r.Tool != toolName {
			continue
		}
		if r.Pattern == nil {
			return true
		}
		if r.Pattern.MatchString(description) {
			return true
		}
	}
	return false
}
