package vfs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// SkillRepository implements skill.Repository backed by a VFS.
// It scans a "skills/" directory in the VFS for SKILL.md files,
// allowing agents to create skills at runtime by writing to the VFS.
type SkillRepository struct {
	filesystem FS
	// name -> directory path (relative to VFS root, e.g. "skills/myskill")
	index map[string]string
	// allowedTools restricts which tools VFS-created skills can declare.
	// nil means no restriction.
	allowedTools map[string]bool
}

// NewSkillRepository creates a VFS-backed skill repository.
// It scans the given directory (e.g. "skills") for subdirectories
// containing SKILL.md files.
// If allowedTools is non-nil, skills can only declare tools in this set.
func NewSkillRepository(filesystem FS, dir string, allowedTools []string) (*SkillRepository, error) {
	r := &SkillRepository{
		filesystem: filesystem,
		index:      make(map[string]string),
	}
	if len(allowedTools) > 0 {
		r.allowedTools = make(map[string]bool, len(allowedTools))
		for _, t := range allowedTools {
			r.allowedTools[t] = true
		}
	}
	if err := r.scan(dir); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *SkillRepository) scan(root string) error {
	entries, err := r.filesystem.ReadDir(root)
	if err != nil {
		// If the skills directory doesn't exist yet, that's fine.
		if isNotExist(err) {
			return nil
		}
		return fmt.Errorf("scan skills dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := root + "/" + entry.Name() + "/SKILL.md"
		data, err := r.filesystem.ReadFile(skillPath)
		if err != nil {
			continue // no SKILL.md in this directory
		}
		sum := parseSummaryFromBytes(data)
		if sum.Name == "" {
			sum.Name = entry.Name()
		}
		if _, exists := r.index[sum.Name]; !exists {
			r.index[sum.Name] = root + "/" + entry.Name()
		}
	}
	return nil
}

// Summaries returns all skill summaries found on the VFS.
func (r *SkillRepository) Summaries() []skill.Summary {
	out := make([]skill.Summary, 0, len(r.index))
	for name, dir := range r.index {
		data, err := r.filesystem.ReadFile(dir + "/SKILL.md")
		if err != nil {
			continue
		}
		sum := parseSummaryFromBytes(data)
		if sum.Name == "" {
			sum.Name = name
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns a full skill by name.
func (r *SkillRepository) Get(name string) (*skill.Skill, error) {
	dir, ok := r.index[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	data, err := r.filesystem.ReadFile(dir + "/SKILL.md")
	if err != nil {
		return nil, err
	}
	sum, body := parseFullFromBytes(data)
	if sum.Name == "" {
		sum.Name = name
	}
	docs := r.readDocs(dir)
	return &skill.Skill{Summary: sum, Body: body, Docs: docs}, nil
}

// Path returns the VFS directory path for a skill.
func (r *SkillRepository) Path(name string) (string, error) {
	dir, ok := r.index[name]
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return dir, nil
}

// ReadRaw returns the raw SKILL.md bytes for a skill, read through the VFS.
func (r *SkillRepository) ReadRaw(name string) ([]byte, error) {
	dir, ok := r.index[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	return r.filesystem.ReadFile(dir + "/SKILL.md")
}

func (r *SkillRepository) readDocs(dir string) []skill.Doc {
	entries, err := r.filesystem.ReadDir(dir)
	if err != nil {
		return nil
	}
	var docs []skill.Doc
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.EqualFold(name, "SKILL.md") {
			continue
		}
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".txt") {
			continue
		}
		data, err := r.filesystem.ReadFile(dir + "/" + name)
		if err != nil {
			continue
		}
		docs = append(docs, skill.Doc{
			Path:    name,
			Content: string(data),
		})
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Path < docs[j].Path
	})
	return docs
}

// parseSummaryFromBytes extracts name and description from SKILL.md front matter.
func parseSummaryFromBytes(data []byte) skill.Summary {
	fm, _ := splitFrontMatter(string(data))
	return skill.Summary{
		Name:        fm["name"],
		Description: fm["description"],
	}
}

// parseFullFromBytes extracts front matter summary and body.
func parseFullFromBytes(data []byte) (skill.Summary, string) {
	fm, body := splitFrontMatter(string(data))
	return skill.Summary{
		Name:        fm["name"],
		Description: fm["description"],
	}, body
}

// splitFrontMatter splits YAML front matter from markdown body.
func splitFrontMatter(text string) (map[string]string, string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return map[string]string{}, text
	}
	idx := strings.Index(text[4:], "\n---\n")
	if idx < 0 {
		return map[string]string{}, text
	}
	fm := text[4 : 4+idx]
	body := text[4+idx+5:]
	m := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewBufferString(fm))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, ":"); i >= 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			m[k] = strings.Trim(v, " \"'")
		}
	}
	return m, body
}

// isNotExist checks if an error indicates a file/directory doesn't exist.
func isNotExist(err error) bool {
	var pathErr *fs.PathError
	if ok := errors.As(err, &pathErr); ok {
		return pathErr.Err == fs.ErrNotExist
	}
	return false
}
