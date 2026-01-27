// Package skills loads and manages agent skills from the filesystem.
package skills

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a loaded skill.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Requires    Requires `yaml:"requires"`
	OS          []string `yaml:"os"`
	// Path is the directory containing this skill (relative to its root).
	Path string `yaml:"-"`
}

// Requires declares external dependencies for a skill.
type Requires struct {
	Bins []string `yaml:"bins"`
	Env  []string `yaml:"env"`
}

// Loader discovers and loads skills from the filesystem.
type Loader struct {
	workspaceSkills string // e.g. ~/.kaggen/workspace/skills
	managedSkills   string // e.g. ~/.kaggen/skills
}

// NewLoader creates a skill loader. Either path may be empty to skip.
func NewLoader(workspaceSkills, managedSkills string) *Loader {
	return &Loader{
		workspaceSkills: workspaceSkills,
		managedSkills:   managedSkills,
	}
}

// Load discovers and returns all available skills.
// Workspace skills take priority over managed skills with the same name.
func (l *Loader) Load() ([]Skill, error) {
	seen := make(map[string]bool)
	var skills []Skill

	// Workspace skills first (highest priority)
	if l.workspaceSkills != "" {
		ws, err := loadFromDir(l.workspaceSkills)
		if err != nil {
			return nil, fmt.Errorf("load workspace skills: %w", err)
		}
		for _, s := range ws {
			if !isCompatible(s) {
				continue
			}
			seen[s.Name] = true
			skills = append(skills, s)
		}
	}

	// Managed skills (lower priority)
	if l.managedSkills != "" {
		ms, err := loadFromDir(l.managedSkills)
		if err != nil {
			return nil, fmt.Errorf("load managed skills: %w", err)
		}
		for _, s := range ms {
			if seen[s.Name] || !isCompatible(s) {
				continue
			}
			skills = append(skills, s)
		}
	}

	return skills, nil
}

// loadFromDir scans a directory for skill subdirectories containing SKILL.md.
func loadFromDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
		s, err := parseSkillFile(skillFile)
		if err != nil {
			continue // skip unparseable skills
		}
		s.Path = filepath.Join("skills", entry.Name())
		skills = append(skills, s)
	}
	return skills, nil
}

// parseSkillFile reads and parses a SKILL.md file with YAML frontmatter.
func parseSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	return ParseSkill(string(data))
}

// ParseSkill parses a SKILL.md content string with YAML frontmatter.
func ParseSkill(content string) (Skill, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return Skill{}, fmt.Errorf("missing YAML frontmatter")
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Skill{}, fmt.Errorf("unclosed YAML frontmatter")
	}

	frontmatter := rest[:idx]
	var s Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &s); err != nil {
		return Skill{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	if s.Name == "" {
		return Skill{}, fmt.Errorf("skill name is required")
	}

	return s, nil
}

// isCompatible checks if a skill is compatible with the current platform
// and has required binaries available.
func isCompatible(s Skill) bool {
	// Check OS constraint
	if len(s.OS) > 0 {
		found := false
		for _, os := range s.OS {
			if os == runtime.GOOS {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check required binaries
	for _, bin := range s.Requires.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			return false
		}
	}

	// Check required env vars
	for _, env := range s.Requires.Env {
		if os.Getenv(env) == "" {
			return false
		}
	}

	return true
}

// FormatXML produces the <skills> XML block for system prompt injection.
func FormatXML(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<skills>\n")
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("  <skill name=%q path=%q>%s</skill>\n",
			s.Name, s.Path, html.EscapeString(s.Description)))
	}
	sb.WriteString("</skills>")
	return sb.String()
}
