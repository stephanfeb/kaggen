package agent

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/config"
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

// skillFrontmatter holds parsed SKILL.md frontmatter fields.
type skillFrontmatter struct {
	Tools        []string // allowed tool names
	GuardedTools []string // tools that require human approval before execution
	Delegate     string   // "claude" for subprocess dispatch, empty for LLM agent
	ClaudeModel  string   // --model flag override
	ClaudeTools  string   // --allowed-tools override
	WorkDir      string   // --add-dir override
}

// BuildSubAgents creates a specialist sub-agent for each skill in the repository,
// plus a general-purpose sub-agent with the provided tools. These sub-agents are
// used as members of the Coordinator Team.
// It also returns a map of guarded tool names to their owning skill name.
func BuildSubAgents(m model.Model, skillsRepo skill.Repository, generalTools []tool.Tool, logger *slog.Logger) ([]agent.Agent, map[string]string, error) {
	var agents []agent.Agent
	guardedTools := make(map[string]string) // tool name -> skill name

	// Load default claude config for sub-agents.
	cfg, _ := config.Load()
	defaultClaudeModel := "sonnet"
	defaultClaudeTools := "Bash,Read,Edit,Write,Glob,Grep"
	if cfg != nil {
		if cfg.Agent.ClaudeModel != "" {
			defaultClaudeModel = cfg.Agent.ClaudeModel
		}
		if cfg.Agent.ClaudeTools != "" {
			defaultClaudeTools = cfg.Agent.ClaudeTools
		}
	}

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

			fm := parseSkillFrontmatter(skillsRepo, summary.Name, logger)

			// Collect guarded tools for this skill.
			for _, gt := range fm.GuardedTools {
				guardedTools[gt] = summary.Name
			}
			if len(fm.GuardedTools) > 0 {
				logger.Info("skill guarded tools", "skill", summary.Name, "guarded", fm.GuardedTools)
			}

			// If skill delegates to claude, create a ClaudeAgent (subprocess).
			if fm.Delegate == "claude" {
				claudeModel := defaultClaudeModel
				if fm.ClaudeModel != "" {
					claudeModel = fm.ClaudeModel
				}
				claudeTools := defaultClaudeTools
				if fm.ClaudeTools != "" {
					claudeTools = fm.ClaudeTools
				}
				workDir := fm.WorkDir

				sa := NewClaudeAgent(summary.Name, summary.Description,
					WithClaudeModel(claudeModel),
					WithClaudeTools(claudeTools),
					WithClaudeWorkDir(workDir),
					WithClaudeInstruction(instruction),
					WithClaudeTimeout(30*time.Minute),
					WithClaudeLogger(logger),
				)
				agents = append(agents, sa)
				logger.Info("created claude sub-agent", "name", summary.Name, "model", claudeModel)
				continue
			}

			// Standard LLM agent path.
			agentTools := generalTools
			if len(fm.Tools) > 0 {
				agentTools = filterTools(generalTools, fm.Tools)
				logger.Info("skill tool filter", "skill", summary.Name, "allowed", fm.Tools)
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
		return nil, nil, fmt.Errorf("no sub-agents created")
	}

	return agents, guardedTools, nil
}

// parseSkillFrontmatter reads the SKILL.md frontmatter and returns parsed fields.
func parseSkillFrontmatter(repo skill.Repository, name string, _ *slog.Logger) skillFrontmatter {
	var fm skillFrontmatter
	dir, err := repo.Path(name)
	if err != nil {
		return fm
	}
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return fm
	}
	defer f.Close()

	rd := bufio.NewReader(f)
	// Read opening ---
	line, err := rd.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "---" {
		return fm
	}
	// Read frontmatter lines until closing ---
	for {
		l, err := rd.ReadString('\n')
		if err != nil || strings.TrimSpace(l) == "---" {
			break
		}
		i := strings.Index(l, ":")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(l[:i])
		val := strings.TrimSpace(l[i+1:])
		val = strings.Trim(val, "\"'")

		switch key {
		case "tools":
			val = strings.Trim(val, "[]")
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					fm.Tools = append(fm.Tools, t)
				}
			}
		case "guarded_tools":
			val = strings.Trim(val, "[]")
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					fm.GuardedTools = append(fm.GuardedTools, t)
				}
			}
		case "delegate":
			fm.Delegate = val
		case "claude_model":
			fm.ClaudeModel = val
		case "claude_tools":
			fm.ClaudeTools = val
		case "work_dir":
			fm.WorkDir = config.ExpandPath(val)
		}
	}
	return fm
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
