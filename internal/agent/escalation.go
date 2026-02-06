// Package agent provides the coordinator agent and sub-agent orchestration.
package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yourusername/kaggen/internal/config"
)

// EscalationDecision contains the result of escalation analysis.
type EscalationDecision struct {
	ShouldEscalate bool     // Whether to escalate to Tier 2 reasoning
	Reason         string   // Why escalation is recommended (or not)
	Confidence     float64  // 0-1, how confident we are in the routing (lower = more likely to escalate)
	Triggers       []string // Which heuristics triggered
}

// EscalationContext provides context for escalation decisions.
type EscalationContext struct {
	Task              string       // The user's task/request
	AvailableSkills   []string     // Names of available sub-agents
	WorldModel        *WorldModel  // Session execution state (may be nil)
	PreviousAttempts  int          // How many times we've tried to route this task
}

// defaultAutoEscalateKeywords are keywords that trigger escalation by default.
var defaultAutoEscalateKeywords = []string{
	"design",
	"architect",
	"evaluate",
	"analyze",
	"compare options",
	"trade-off",
	"trade off",
	"tradeoff",
	"strategic",
	"thoroughly",
	"comprehensively",
	"in-depth",
	"deep dive",
}

// complexityIndicators are phrases that suggest higher complexity.
var complexityIndicators = []string{
	"multiple",
	"several",
	"integrate",
	"migration",
	"refactor",
	"overhaul",
	"redesign",
	"from scratch",
	"end-to-end",
	"full stack",
	"system",
	"architecture",
}

// Escalator evaluates whether a task should escalate to Tier 2 reasoning.
type Escalator struct {
	config   config.ReasoningConfig
	keywords []string
}

// NewEscalator creates a new escalation evaluator.
func NewEscalator(cfg config.ReasoningConfig) *Escalator {
	keywords := cfg.AutoEscalateKeywords
	if len(keywords) == 0 {
		keywords = defaultAutoEscalateKeywords
	}
	return &Escalator{
		config:   cfg,
		keywords: keywords,
	}
}

// ShouldEscalate evaluates whether a task should be escalated to Tier 2.
func (e *Escalator) ShouldEscalate(ctx *EscalationContext) EscalationDecision {
	decision := EscalationDecision{
		Confidence: 1.0, // Start with high confidence, reduce as we find issues
	}

	taskLower := strings.ToLower(ctx.Task)

	// Check 1: Keyword triggers (highest priority)
	for _, kw := range e.keywords {
		if strings.Contains(taskLower, strings.ToLower(kw)) {
			decision.ShouldEscalate = true
			decision.Reason = fmt.Sprintf("task contains architectural keyword: %q", kw)
			decision.Triggers = append(decision.Triggers, "keyword:"+kw)
			decision.Confidence = 0.3 // Low confidence in simple routing
			return decision
		}
	}

	// Check 2: Subtask count estimation
	subtaskCount := estimateSubtasks(ctx.Task)
	maxSubtasks := e.config.MaxSubtasksTrigger
	if maxSubtasks <= 0 {
		maxSubtasks = 5
	}
	if subtaskCount > maxSubtasks {
		decision.ShouldEscalate = true
		decision.Reason = fmt.Sprintf("task requires ~%d subtasks (threshold: %d)", subtaskCount, maxSubtasks)
		decision.Triggers = append(decision.Triggers, fmt.Sprintf("subtasks:%d", subtaskCount))
		decision.Confidence = 0.4
		return decision
	}

	// Check 3: Complexity indicators
	complexityScore := 0
	for _, ind := range complexityIndicators {
		if strings.Contains(taskLower, ind) {
			complexityScore++
			decision.Triggers = append(decision.Triggers, "complexity:"+ind)
		}
	}
	if complexityScore >= 3 {
		decision.ShouldEscalate = true
		decision.Reason = fmt.Sprintf("high complexity indicators (%d found)", complexityScore)
		decision.Confidence = 0.4
		return decision
	}

	// Check 4: No clear skill match
	skillMatchScore := calculateSkillMatch(ctx.Task, ctx.AvailableSkills)
	threshold := e.config.EscalationThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	if skillMatchScore < threshold {
		decision.ShouldEscalate = true
		decision.Reason = fmt.Sprintf("low skill match confidence (%.2f < %.2f)", skillMatchScore, threshold)
		decision.Triggers = append(decision.Triggers, fmt.Sprintf("skill_match:%.2f", skillMatchScore))
		decision.Confidence = skillMatchScore
		return decision
	}

	// Check 5: WorldModel-based stuck detection
	if ctx.WorldModel != nil {
		if stuck, reason := ctx.WorldModel.ShouldEscalate(); stuck {
			decision.ShouldEscalate = true
			decision.Reason = reason
			decision.Triggers = append(decision.Triggers, "stuck")
			decision.Confidence = 0.3
			return decision
		}
	}

	// Check 6: Multiple failed routing attempts
	if ctx.PreviousAttempts >= 2 {
		decision.ShouldEscalate = true
		decision.Reason = fmt.Sprintf("previous routing attempts failed (%d)", ctx.PreviousAttempts)
		decision.Triggers = append(decision.Triggers, "retries")
		decision.Confidence = 0.2
		return decision
	}

	// Calculate final confidence based on accumulated signals
	decision.Confidence = 1.0 - float64(complexityScore)*0.1 - float64(subtaskCount-1)*0.05
	if decision.Confidence < 0.5 {
		decision.Confidence = 0.5
	}
	return decision
}

// estimateSubtasks estimates the number of subtasks from task description.
func estimateSubtasks(task string) int {
	// Count action verbs and conjunctions
	actionPattern := regexp.MustCompile(`\b(create|implement|build|add|update|modify|refactor|test|deploy|configure|integrate|migrate|fix|change|remove|delete|write|set up|setup)\b`)
	conjunctionPattern := regexp.MustCompile(`\b(and then|then|also|additionally|plus|as well as|after that|next|finally)\b`)

	taskLower := strings.ToLower(task)
	actions := len(actionPattern.FindAllString(taskLower, -1))
	conjunctions := len(conjunctionPattern.FindAllString(taskLower, -1))

	// Base estimate: at least 1, plus actions, plus conjunctions suggest multiple steps
	estimate := 1 + actions + conjunctions/2

	// Cap at reasonable maximum
	if estimate > 10 {
		estimate = 10
	}

	return estimate
}

// calculateSkillMatch computes how well available skills match the task.
func calculateSkillMatch(task string, skills []string) float64 {
	if len(skills) == 0 {
		return 0.0
	}

	taskWords := extractKeywords(task)
	if len(taskWords) == 0 {
		return 0.5 // Neutral if we can't extract keywords
	}

	bestMatch := 0.0
	for _, skill := range skills {
		skillWords := extractKeywords(skill)
		overlap := countOverlap(taskWords, skillWords)
		matchScore := float64(overlap) / float64(len(taskWords))
		if matchScore > bestMatch {
			bestMatch = matchScore
		}
	}

	return bestMatch
}

// extractKeywords extracts significant words from text.
func extractKeywords(text string) []string {
	// Remove common words and extract significant terms
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"to": true, "for": true, "of": true, "in": true, "on": true,
		"with": true, "and": true, "or": true, "that": true, "this": true,
		"it": true, "be": true, "as": true, "at": true, "by": true,
		"from": true, "have": true, "has": true, "had": true, "do": true,
		"does": true, "did": true, "will": true, "would": true, "could": true,
		"should": true, "may": true, "might": true, "must": true, "can": true,
		"i": true, "you": true, "we": true, "they": true, "he": true, "she": true,
		"me": true, "my": true, "your": true, "our": true, "their": true,
	}

	words := strings.Fields(strings.ToLower(text))
	var keywords []string
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'()[]{}/-_")
		if len(w) > 2 && !stopWords[w] {
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// countOverlap counts overlapping words between two sets.
func countOverlap(a, b []string) int {
	bSet := make(map[string]bool)
	for _, w := range b {
		bSet[w] = true
	}
	count := 0
	for _, w := range a {
		if bSet[w] {
			count++
		}
	}
	return count
}
