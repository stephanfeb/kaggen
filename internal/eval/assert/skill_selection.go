package assert

import (
	"fmt"
)

// SkillSelected asserts that the coordinator delegated to specific skill(s).
type SkillSelected struct {
	skillName string
	required  bool // skill must be selected
	forbidden bool // skill must NOT be selected
}

// NewSkillSelected creates a SkillSelected assertion from config.
func NewSkillSelected(config Config) (Assertion, error) {
	if config.Skill == "" {
		return nil, fmt.Errorf("skill-selected assertion requires 'skill' field")
	}

	// Determine mode from config fields or params
	required := true
	forbidden := false

	// Check direct config fields first
	if config.Required != nil {
		required = *config.Required
	}
	if config.Forbidden != nil {
		forbidden = *config.Forbidden
		if forbidden {
			required = false
		}
	}

	// Fall back to params for backwards compatibility
	if config.Params != nil {
		if r, ok := config.Params["required"].(bool); ok {
			required = r
		}
		if f, ok := config.Params["forbidden"].(bool); ok {
			forbidden = f
			if forbidden {
				required = false
			}
		}
	}

	return &SkillSelected{
		skillName: config.Skill,
		required:  required,
		forbidden: forbidden,
	}, nil
}

func (a *SkillSelected) Type() string { return "skill-selected" }

func (a *SkillSelected) Evaluate(ctx *Context) Result {
	// Check if this skill was used via SkillsDispatched in context
	skillUsed := false
	if ctx.SkillsDispatched != nil {
		_, skillUsed = ctx.SkillsDispatched[a.skillName]
	}

	if a.required && !skillUsed {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("skill %q was not selected (expected)", a.skillName),
		}
	}

	if a.forbidden && skillUsed {
		return Result{
			Type:   a.Type(),
			Passed: false,
			Score:  0.0,
			Reason: fmt.Sprintf("skill %q was selected (forbidden)", a.skillName),
		}
	}

	if a.required && skillUsed {
		return Result{
			Type:   a.Type(),
			Passed: true,
			Score:  1.0,
			Reason: fmt.Sprintf("skill %q was correctly selected", a.skillName),
		}
	}

	return Result{
		Type:   a.Type(),
		Passed: true,
		Score:  1.0,
		Reason: fmt.Sprintf("skill %q correctly not selected", a.skillName),
	}
}
