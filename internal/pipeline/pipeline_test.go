package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAll_ValidPipeline(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: test_pipeline
description: A test pipeline
trigger: "test requests"
stages:
  - agent: agent_a
    description: First stage
  - agent: agent_b
    description: Second stage
    on_fail: agent_a
    max_retries: 2
`
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	pipelines, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(pipelines) != 1 {
		t.Fatalf("expected 1 pipeline, got %d", len(pipelines))
	}

	p := pipelines[0]
	if p.Name != "test_pipeline" {
		t.Errorf("name = %q, want %q", p.Name, "test_pipeline")
	}
	if len(p.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(p.Stages))
	}
	if p.Stages[1].OnFail != "agent_a" {
		t.Errorf("stage 2 on_fail = %q, want %q", p.Stages[1].OnFail, "agent_a")
	}
	if p.Stages[1].MaxRetries != 2 {
		t.Errorf("stage 2 max_retries = %d, want 2", p.Stages[1].MaxRetries)
	}
}

func TestLoadAll_MissingDir(t *testing.T) {
	pipelines, err := LoadAll("/nonexistent/dir")
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got: %v", err)
	}
	if pipelines != nil {
		t.Errorf("expected nil pipelines for missing dir")
	}
}

func TestLoadAll_MissingName(t *testing.T) {
	dir := t.TempDir()
	yaml := `
description: No name
trigger: "test"
stages:
  - agent: a
    description: stage
`
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(yaml), 0644)

	_, err := LoadAll(dir)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadAll_NoStages(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: empty
description: No stages
trigger: "test"
stages: []
`
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(yaml), 0644)

	_, err := LoadAll(dir)
	if err == nil {
		t.Fatal("expected error for empty stages")
	}
}

func TestBuildInstruction(t *testing.T) {
	pipelines := []Pipeline{
		{
			Name:    "software_dev",
			Trigger: "building software",
			Stages: []Stage{
				{Agent: "coder", Description: "Builds code"},
				{Agent: "qa", Description: "Tests code", OnFail: "coder", MaxRetries: 3},
			},
		},
	}

	result := BuildInstruction(pipelines)
	if result == "" {
		t.Fatal("expected non-empty instruction")
	}
	if !contains(result, "Software Dev") {
		t.Error("expected title-cased pipeline name in output")
	}
	if !contains(result, "dispatch `coder`") {
		t.Error("expected coder stage in output")
	}
	if !contains(result, "re-dispatch `coder`") {
		t.Error("expected retry policy in output")
	}
}

func TestBuildInstruction_Empty(t *testing.T) {
	if result := BuildInstruction(nil); result != "" {
		t.Errorf("expected empty string for nil pipelines, got %q", result)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
