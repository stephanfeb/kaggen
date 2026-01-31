package agent

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CaseInsensitiveRepository wraps a skill.Repository to provide case-insensitive lookups.
// This handles LLMs like Gemini that may capitalize skill names (e.g., "Pandoc" vs "pandoc").
type CaseInsensitiveRepository struct {
	inner    skill.Repository
	nameMap  map[string]string // lowercase -> actual name
}

// NewCaseInsensitiveRepository wraps an existing repository with case-insensitive lookup.
func NewCaseInsensitiveRepository(repo skill.Repository) *CaseInsensitiveRepository {
	nameMap := make(map[string]string)
	for _, s := range repo.Summaries() {
		nameMap[strings.ToLower(s.Name)] = s.Name
	}
	return &CaseInsensitiveRepository{
		inner:   repo,
		nameMap: nameMap,
	}
}

// Get returns a skill by name (case-insensitive).
func (r *CaseInsensitiveRepository) Get(name string) (*skill.Skill, error) {
	if actual, ok := r.nameMap[strings.ToLower(name)]; ok {
		return r.inner.Get(actual)
	}
	return r.inner.Get(name)
}

// Summaries returns all skill summaries.
func (r *CaseInsensitiveRepository) Summaries() []skill.Summary {
	return r.inner.Summaries()
}

// Path returns the directory path for a skill (case-insensitive).
func (r *CaseInsensitiveRepository) Path(name string) (string, error) {
	if actual, ok := r.nameMap[strings.ToLower(name)]; ok {
		return r.inner.Path(actual)
	}
	return r.inner.Path(name)
}

// BuildSubAgents creates a specialist sub-agent for each skill in the repository,
// plus a general-purpose sub-agent with the provided tools. These sub-agents are
// used as members of the Coordinator Team.
func BuildSubAgents(m model.Model, skillsRepo skill.Repository, generalTools []tool.Tool, logger *slog.Logger) ([]agent.Agent, error) {
	var agents []agent.Agent

	// Create a sub-agent for each skill.
	if skillsRepo != nil {
		for _, summary := range skillsRepo.Summaries() {
			sk, err := skillsRepo.Get(summary.Name)
			if err != nil {
				logger.Warn("failed to load skill for sub-agent", "skill", summary.Name, "error", err)
				continue
			}

			instruction := sk.Body
			if instruction == "" {
				instruction = summary.Description
			}

			// Determine which tools this skill agent gets.
			agentTools := generalTools
			if allowedTools := parseSkillTools(skillsRepo, summary.Name, logger); len(allowedTools) > 0 {
				agentTools = filterTools(generalTools, allowedTools)
				logger.Info("skill tool filter", "skill", summary.Name, "allowed", allowedTools)
			}

			sa := llmagent.New(summary.Name,
				llmagent.WithModel(m),
				llmagent.WithInstruction(instruction),
				llmagent.WithDescription(summary.Description),
				llmagent.WithTools(agentTools),
				llmagent.WithMaxLLMCalls(25),
				llmagent.WithMaxToolIterations(30),
			)

			agents = append(agents, sa)
			logger.Info("created skill sub-agent", "name", summary.Name, "tools", len(agentTools))
		}
	}

	// Create a general-purpose sub-agent with the standard tools (read, write, exec, etc.).
	gp := llmagent.New("general",
		llmagent.WithModel(m),
		llmagent.WithTools(generalTools),
		llmagent.WithInstruction("You are a general-purpose assistant. Use the available tools to complete tasks. Report your results clearly."),
		llmagent.WithDescription("General-purpose agent with file read/write, exec, and other standard tools. Use for tasks that don't match a specific skill."),
		llmagent.WithMaxLLMCalls(25),
		llmagent.WithMaxToolIterations(30),
	)
	agents = append(agents, gp)

	if len(agents) == 0 {
		return nil, fmt.Errorf("no sub-agents created")
	}

	return agents, nil
}

// parseSkillTools reads the SKILL.md frontmatter for a "tools" field and returns
// the list of allowed tool names. Returns nil if no tools field is specified.
func parseSkillTools(repo skill.Repository, name string, _ *slog.Logger) []string {
	dir, err := repo.Path(name)
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil
	}
	defer f.Close()

	rd := bufio.NewReader(f)
	// Read opening ---
	line, err := rd.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "---" {
		return nil
	}
	// Read frontmatter lines until closing ---
	for {
		l, err := rd.ReadString('\n')
		if err != nil || strings.TrimSpace(l) == "---" {
			break
		}
		if i := strings.Index(l, ":"); i >= 0 {
			key := strings.TrimSpace(l[:i])
			if key == "tools" {
				val := strings.TrimSpace(l[i+1:])
				val = strings.Trim(val, "[]\"'")
				var tools []string
				for _, t := range strings.Split(val, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						tools = append(tools, t)
					}
				}
				return tools
			}
		}
	}
	return nil
}

// filterTools returns only tools whose Declaration().Name is in the allowed list.
func filterTools(all []tool.Tool, allowed []string) []tool.Tool {
	set := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		set[name] = true
	}
	var filtered []tool.Tool
	for _, t := range all {
		if set[t.Declaration().Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
