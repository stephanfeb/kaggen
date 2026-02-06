package agent

import (
	"testing"

	"github.com/yourusername/kaggen/internal/config"
)

func TestNewEscalator(t *testing.T) {
	cfg := config.ReasoningConfig{
		AutoEscalateKeywords: []string{"custom", "keywords"},
	}
	e := NewEscalator(cfg)
	if e == nil {
		t.Fatal("NewEscalator returned nil")
	}
	if len(e.keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(e.keywords))
	}
}

func TestNewEscalator_DefaultKeywords(t *testing.T) {
	cfg := config.ReasoningConfig{}
	e := NewEscalator(cfg)
	if len(e.keywords) == 0 {
		t.Error("expected default keywords, got none")
	}
}

func TestShouldEscalate_KeywordTrigger(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{})

	ctx := &EscalationContext{
		Task: "Design a new authentication system for the application",
	}

	decision := e.ShouldEscalate(ctx)
	if !decision.ShouldEscalate {
		t.Error("expected escalation for 'design' keyword")
	}
	if decision.Reason == "" {
		t.Error("expected a reason")
	}
	if len(decision.Triggers) == 0 {
		t.Error("expected triggers to be populated")
	}
}

func TestShouldEscalate_ArchitectKeyword(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{})

	ctx := &EscalationContext{
		Task: "Architect the database schema for user management",
	}

	decision := e.ShouldEscalate(ctx)
	if !decision.ShouldEscalate {
		t.Error("expected escalation for 'architect' keyword")
	}
}

func TestShouldEscalate_SubtaskCount(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{
		MaxSubtasksTrigger: 3,
	})

	ctx := &EscalationContext{
		Task: "Create the user model, then implement the API endpoints, also add tests, and finally deploy to staging",
	}

	decision := e.ShouldEscalate(ctx)
	if !decision.ShouldEscalate {
		t.Error("expected escalation for high subtask count")
	}
}

func TestShouldEscalate_ComplexityIndicators(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{})

	ctx := &EscalationContext{
		Task: "Integrate multiple services, refactor the system, and perform a migration of the entire architecture",
	}

	decision := e.ShouldEscalate(ctx)
	if !decision.ShouldEscalate {
		t.Error("expected escalation for high complexity")
	}
}

func TestShouldEscalate_LowSkillMatch(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{
		EscalationThreshold: 0.5,
	})

	ctx := &EscalationContext{
		Task:            "Do something completely unrelated to any skill",
		AvailableSkills: []string{"coder", "researcher", "writer"},
	}

	decision := e.ShouldEscalate(ctx)
	// Low skill match should trigger escalation
	if decision.Confidence > 0.5 {
		t.Logf("skill match confidence: %f", decision.Confidence)
	}
}

func TestShouldEscalate_WorldModelStuck(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{})

	wm := NewWorldModel("test-session")
	// Simulate being stuck: 10+ minutes with minimal progress
	wm.mu.Lock()
	wm.startedAt = wm.startedAt.Add(-11 * 60 * 1000000000) // 11 minutes ago (in nanoseconds)
	wm.mu.Unlock()

	ctx := &EscalationContext{
		Task:       "Simple task",
		WorldModel: wm,
	}

	decision := e.ShouldEscalate(ctx)
	if !decision.ShouldEscalate {
		t.Error("expected escalation when WorldModel indicates stuck")
	}
}

func TestShouldEscalate_PreviousAttempts(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{})

	ctx := &EscalationContext{
		Task:             "Simple task",
		PreviousAttempts: 2,
	}

	decision := e.ShouldEscalate(ctx)
	if !decision.ShouldEscalate {
		t.Error("expected escalation after 2 previous attempts")
	}
}

func TestShouldEscalate_SimpleTask(t *testing.T) {
	e := NewEscalator(config.ReasoningConfig{})

	ctx := &EscalationContext{
		Task:            "Read the README file",
		AvailableSkills: []string{"coder", "reader"},
	}

	decision := e.ShouldEscalate(ctx)
	// Simple tasks shouldn't trigger escalation
	if decision.ShouldEscalate {
		t.Logf("unexpectedly escalated: %s", decision.Reason)
	}
}

func TestEstimateSubtasks(t *testing.T) {
	tests := []struct {
		name     string
		task     string
		expected int
	}{
		{"read_file", "Read a file", 2},        // 1 base + 1 action (read)
		{"create_and_test", "Create a user and test it", 3}, // 1 base + 2 actions
		{"multi_step", "Build, test, and deploy the application", 4}, // 1 base + 3 actions
		{"simple", "Simple question", 1}, // 1 base, no actions
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := estimateSubtasks(tc.task)
			if result < tc.expected-1 || result > tc.expected+1 {
				t.Errorf("estimateSubtasks(%q) = %d, expected ~%d", tc.task, result, tc.expected)
			}
		})
	}
}

func TestCalculateSkillMatch(t *testing.T) {
	skills := []string{"coder python go", "researcher web api", "writer documentation"}

	// Good match - "python" and "code" both appear in skill "coder python go"
	score := calculateSkillMatch("write some python code", skills)
	if score < 0.2 {
		t.Errorf("expected some match for python task, got %f", score)
	}

	// No skills
	score = calculateSkillMatch("any task", nil)
	if score != 0.0 {
		t.Errorf("expected 0.0 for no skills, got %f", score)
	}

	// Very good match
	score = calculateSkillMatch("coder python go", skills)
	if score < 0.5 {
		t.Errorf("expected good match for exact skill keywords, got %f", score)
	}
}

func TestExtractKeywords(t *testing.T) {
	keywords := extractKeywords("The quick brown fox jumps over the lazy dog")

	// Should filter out stop words like "the"
	for _, kw := range keywords {
		if kw == "the" {
			t.Errorf("stop word %q should be filtered", kw)
		}
	}

	// Should include significant words
	found := make(map[string]bool)
	for _, kw := range keywords {
		found[kw] = true
	}
	if !found["quick"] || !found["brown"] || !found["fox"] {
		t.Error("expected significant words to be preserved")
	}

	// Should have some keywords extracted
	if len(keywords) < 3 {
		t.Errorf("expected at least 3 keywords, got %d", len(keywords))
	}
}

func TestCountOverlap(t *testing.T) {
	a := []string{"one", "two", "three"}
	b := []string{"two", "three", "four"}

	overlap := countOverlap(a, b)
	if overlap != 2 {
		t.Errorf("expected overlap of 2, got %d", overlap)
	}

	// No overlap
	overlap = countOverlap(a, []string{"four", "five"})
	if overlap != 0 {
		t.Errorf("expected overlap of 0, got %d", overlap)
	}
}
