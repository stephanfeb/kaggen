package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testSkillMd = `---
name: web-search
description: Search the web using DuckDuckGo
requires:
  bins: []
  env: []
os:
  - darwin
  - linux
---

# Web Search Skill

Use this skill when the user asks to search the web.
`

func TestParseSkill(t *testing.T) {
	s, err := ParseSkill(testSkillMd)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	if s.Name != "web-search" {
		t.Errorf("name = %q, want %q", s.Name, "web-search")
	}
	if s.Description != "Search the web using DuckDuckGo" {
		t.Errorf("description = %q", s.Description)
	}
	if len(s.OS) != 2 {
		t.Errorf("os = %v, want 2 entries", s.OS)
	}
}

func TestParseSkill_MissingFrontmatter(t *testing.T) {
	_, err := ParseSkill("# Just markdown, no frontmatter")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseSkill_MissingName(t *testing.T) {
	_, err := ParseSkill("---\ndescription: foo\n---\n# Body")
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestFormatXML(t *testing.T) {
	skills := []Skill{
		{Name: "web-search", Description: "Search the web", Path: "skills/web-search"},
		{Name: "calendar", Description: "Manage events", Path: "skills/calendar"},
	}
	xml := FormatXML(skills)
	if !strings.Contains(xml, "<skills>") {
		t.Error("missing <skills> tag")
	}
	if !strings.Contains(xml, `name="web-search"`) {
		t.Error("missing web-search skill")
	}
	if !strings.Contains(xml, `name="calendar"`) {
		t.Error("missing calendar skill")
	}
}

func TestFormatXML_Empty(t *testing.T) {
	if xml := FormatXML(nil); xml != "" {
		t.Errorf("expected empty string, got %q", xml)
	}
}

func TestLoadFromDir(t *testing.T) {
	dir := t.TempDir()

	// Create a skill
	skillDir := filepath.Join(dir, "hello")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: hello
description: Say hello
---
# Hello Skill
`), 0644)

	skills, err := loadFromDir(dir)
	if err != nil {
		t.Fatalf("loadFromDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	if skills[0].Name != "hello" {
		t.Errorf("name = %q", skills[0].Name)
	}
}

func TestLoadFromDir_NonExistent(t *testing.T) {
	skills, err := loadFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestLoaderPriority(t *testing.T) {
	wsDir := t.TempDir()
	mgDir := t.TempDir()

	// Same skill name in both dirs, different descriptions
	for _, pair := range []struct {
		dir, desc string
	}{
		{wsDir, "workspace version"},
		{mgDir, "managed version"},
	} {
		d := filepath.Join(pair.dir, "dupe")
		os.MkdirAll(d, 0755)
		os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: dupe\ndescription: "+pair.desc+"\n---\n# D\n"), 0644)
	}

	loader := NewLoader(wsDir, mgDir)
	skills, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1 (deduped)", len(skills))
	}
	if skills[0].Description != "workspace version" {
		t.Errorf("expected workspace version to win, got %q", skills[0].Description)
	}
}

func TestIsCompatible_OS(t *testing.T) {
	s := Skill{Name: "test", OS: []string{"impossible-os"}}
	if isCompatible(s) {
		t.Error("should be incompatible with impossible OS")
	}

	s2 := Skill{Name: "test", OS: []string{runtime.GOOS}}
	if !isCompatible(s2) {
		t.Error("should be compatible with current OS")
	}

	s3 := Skill{Name: "test"} // empty OS = all
	if !isCompatible(s3) {
		t.Error("empty OS should be compatible")
	}
}

func TestIsCompatible_Bins(t *testing.T) {
	s := Skill{Name: "test", Requires: Requires{Bins: []string{"nonexistent-binary-xyz"}}}
	if isCompatible(s) {
		t.Error("should be incompatible with missing binary")
	}
}
