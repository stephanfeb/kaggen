package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// agentsMDFile is the conventional filename for project-specific agent instructions.
const agentsMDFile = "AGENTS.md"

// pathRe matches absolute directory paths in task text.
// Captures paths like /Users/foo/claude-projects/myapp or /home/user/projects/app.
var pathRe = regexp.MustCompile(`(/(?:Users|home)/\S+?)(?:\s|$|[)\]"'])`)

// loadProjectContext extracts a project directory from the task text,
// looks for AGENTS.md in that directory (walking up to a boundary),
// and returns its content. Returns empty string if no project context found.
func loadProjectContext(taskText string) string {
	dir := findProjectDir(taskText)
	if dir == "" {
		return ""
	}
	path := findAgentsMD(dir)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// findProjectDir extracts the first absolute directory path from text.
// It looks for paths starting with /Users/ or /home/ and verifies the
// directory exists on disk.
func findProjectDir(text string) string {
	matches := pathRe.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		candidate := strings.TrimRight(m[1], "/.,;:")
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
		// Try parent in case the path points to a file
		parent := filepath.Dir(candidate)
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			return parent
		}
	}
	return ""
}

// findAgentsMD walks up from dir looking for AGENTS.md, stopping at the
// user's home directory or filesystem root to avoid reading unrelated files.
func findAgentsMD(dir string) string {
	home, _ := os.UserHomeDir()

	for {
		candidate := filepath.Join(dir, agentsMDFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}

		parent := filepath.Dir(dir)
		// Stop at home directory or filesystem root
		if parent == dir || dir == home {
			break
		}
		dir = parent
	}
	return ""
}
