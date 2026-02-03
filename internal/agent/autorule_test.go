package agent

import (
	"testing"
)

func TestCompileAutoRules(t *testing.T) {
	rules := []AutoApproveRule{
		{Tool: "Bash", Pattern: `^Run command: git (status|log|diff)`},
		{Tool: "Read"},
	}
	compiled, err := CompileAutoRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(compiled))
	}
	if compiled[1].Pattern != nil {
		t.Error("expected nil pattern for Read rule")
	}
}

func TestCompileAutoRulesInvalidRegex(t *testing.T) {
	rules := []AutoApproveRule{{Tool: "Bash", Pattern: `[invalid`}}
	_, err := CompileAutoRules(rules)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestMatchAutoRule(t *testing.T) {
	rules := []AutoApproveRule{
		{Tool: "Bash", Pattern: `^Run command: git (status|log|diff)`},
		{Tool: "Read"},
	}
	compiled, _ := CompileAutoRules(rules)

	tests := []struct {
		tool string
		desc string
		want bool
	}{
		{"Bash", "Run command: git status", true},
		{"Bash", "Run command: git log --oneline", true},
		{"Bash", "Run command: rm -rf /", false},
		{"Bash", "Run command: kubectl apply", false},
		{"Read", "Read file: /tmp/foo.go", true},
		{"Read", "", true}, // nil pattern matches everything
		{"Write", "Write file: /tmp/bar.go", false},
	}
	for _, tt := range tests {
		got := MatchAutoRule(compiled, tt.tool, tt.desc)
		if got != tt.want {
			t.Errorf("MatchAutoRule(%q, %q) = %v, want %v", tt.tool, tt.desc, got, tt.want)
		}
	}
}
