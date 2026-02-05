package eval

import (
	"strings"
	"time"
)

// CoordinatorObserver tracks coordinator-level behavior during eval execution.
type CoordinatorObserver struct {
	// Skill delegation tracking
	SkillsDispatched map[string][]DispatchRecord // skill name -> dispatch records

	// Known skill names for detecting sync member tool calls
	skillNames map[string]bool

	// Tool calls made by coordinator
	ToolCalls []ToolCallRecord

	// Clarification questions asked
	Clarifications []string

	// All text responses from coordinator
	Responses []string

	// Timing
	StartTime time.Time
	EndTime   time.Time
}

// DispatchRecord captures a skill dispatch event.
type DispatchRecord struct {
	TaskID    string
	SkillName string
	Task      string
	Timestamp time.Time
}

// ToolCallRecord captures a tool call event.
type ToolCallRecord struct {
	ID        string
	Name      string
	Arguments map[string]any
	Timestamp time.Time
}

// NewCoordinatorObserver creates a new observer instance.
func NewCoordinatorObserver() *CoordinatorObserver {
	return &CoordinatorObserver{
		SkillsDispatched: make(map[string][]DispatchRecord),
		skillNames:       make(map[string]bool),
		ToolCalls:        make([]ToolCallRecord, 0),
		Clarifications:   make([]string, 0),
		Responses:        make([]string, 0),
		StartTime:        time.Now(),
	}
}

// SetSkillNames registers known skill names so the observer can detect sync member tool calls.
// When the coordinator calls a tool with a name matching a skill, it's tracked as a skill dispatch.
func (o *CoordinatorObserver) SetSkillNames(names []string) {
	for _, name := range names {
		o.skillNames[name] = true
	}
}

// RecordToolCall records a tool call made by the coordinator.
func (o *CoordinatorObserver) RecordToolCall(id, name string, args map[string]any) {
	o.ToolCalls = append(o.ToolCalls, ToolCallRecord{
		ID:        id,
		Name:      name,
		Arguments: args,
		Timestamp: time.Now(),
	})

	// Check if this is a dispatch_task call (async delegation)
	if name == "dispatch_task" {
		if agentName, ok := args["agent_name"].(string); ok {
			task, _ := args["task"].(string)
			taskID, _ := args["task_id"].(string)
			o.SkillsDispatched[agentName] = append(o.SkillsDispatched[agentName], DispatchRecord{
				TaskID:    taskID,
				SkillName: agentName,
				Task:      task,
				Timestamp: time.Now(),
			})
		}
		return
	}

	// Check if this is a sync member tool call
	// Tool names may have prefixes like "team-members-kaggen_" so we check for suffix matches
	matchedSkill := ""
	for skillName := range o.skillNames {
		if name == skillName || strings.HasSuffix(name, "_"+skillName) || strings.HasSuffix(name, "-"+skillName) {
			matchedSkill = skillName
			break
		}
	}
	if matchedSkill != "" {
		// Extract task from "request" or "message" argument
		task, _ := args["request"].(string)
		if task == "" {
			task, _ = args["message"].(string)
		}
		o.SkillsDispatched[matchedSkill] = append(o.SkillsDispatched[matchedSkill], DispatchRecord{
			TaskID:    id,
			SkillName: matchedSkill,
			Task:      task,
			Timestamp: time.Now(),
		})
	}
}

// RecordResponse records a text response from the coordinator.
func (o *CoordinatorObserver) RecordResponse(text string) {
	o.Responses = append(o.Responses, text)

	// Check if this is a clarification question
	if IsClarificationQuestion(text) {
		o.Clarifications = append(o.Clarifications, text)
	}
}

// Finish marks the observation as complete.
func (o *CoordinatorObserver) Finish() {
	o.EndTime = time.Now()
}

// Duration returns the total execution duration.
func (o *CoordinatorObserver) Duration() time.Duration {
	if o.EndTime.IsZero() {
		return time.Since(o.StartTime)
	}
	return o.EndTime.Sub(o.StartTime)
}

// GetSkillsUsed returns a list of skill names that were dispatched to.
func (o *CoordinatorObserver) GetSkillsUsed() []string {
	skills := make([]string, 0, len(o.SkillsDispatched))
	for name := range o.SkillsDispatched {
		skills = append(skills, name)
	}
	return skills
}

// WasSkillUsed returns true if the specified skill was dispatched to.
func (o *CoordinatorObserver) WasSkillUsed(skillName string) bool {
	_, ok := o.SkillsDispatched[skillName]
	return ok
}

// HasClarifications returns true if the coordinator asked for clarification.
func (o *CoordinatorObserver) HasClarifications() bool {
	return len(o.Clarifications) > 0
}

// IsClarificationQuestion heuristically detects if text is a clarification question.
func IsClarificationQuestion(text string) bool {
	lowerText := strings.ToLower(text)

	// Must have a question mark
	if !strings.Contains(text, "?") {
		return false
	}

	// Check for clarification phrases
	clarificationPhrases := []string{
		// Direct clarification requests
		"could you clarify",
		"please clarify",
		"can you clarify",
		"need clarification",
		// Phrases with "please" inserted (common polite forms)
		"could you please provide",
		"could you please clarify",
		"could you please specify",
		"could you please tell",
		"can you please provide",
		"can you please tell",
		"please provide",
		"please tell me",
		// Which/what questions
		"which one",
		"which file",
		"which config",
		"which",
		"what do you want",
		"what should",
		"what would you like",
		// Meaning clarification
		"do you mean",
		"did you mean",
		"are you referring to",
		// Specificity requests
		"can you specify",
		"please specify",
		"be more specific",
		"could you be more specific",
		// Information requests
		"more information",
		"need more details",
		"provide more details",
		"can you provide more",
		"i need to know",
		// Uncertainty expressions
		"not sure which",
		"unclear",
		"ambiguous",
		"not clear",
		// Polite questions
		"could you tell me",
		"would you like",
		"could you provide",
		"can you tell me",
		"how would you like",
		"where should",
		"where would you like",
		// Content requests
		"what content",
		"what text",
		"what should i write",
		"what should i create",
	}

	for _, phrase := range clarificationPhrases {
		if strings.Contains(lowerText, phrase) {
			return true
		}
	}

	return false
}

// FinalResponse returns the last text response from the coordinator.
func (o *CoordinatorObserver) FinalResponse() string {
	if len(o.Responses) == 0 {
		return ""
	}
	return o.Responses[len(o.Responses)-1]
}
