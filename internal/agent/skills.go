package agent

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/secrets"
)

// globalOAuthTokenGetter is set by the server when OAuth is configured.
// It's used by skills to retrieve OAuth tokens.
var (
	globalOAuthTokenGetter OAuthTokenGetter
	oauthMu                sync.RWMutex
)

// SetOAuthTokenGetter sets the global OAuth token getter.
// This is called by the server on startup when OAuth is configured.
func SetOAuthTokenGetter(getter OAuthTokenGetter) {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	globalOAuthTokenGetter = getter
}

// getOAuthTokenGetter returns the current global OAuth token getter.
func getOAuthTokenGetter() OAuthTokenGetter {
	oauthMu.RLock()
	defer oauthMu.RUnlock()
	return globalOAuthTokenGetter
}

// CaseInsensitiveRepository wraps a skill.Repository to provide case-insensitive lookups.
// This handles LLMs like Gemini that may capitalize skill names (e.g., "Pandoc" vs "pandoc").
type CaseInsensitiveRepository struct {
	inner   skill.Repository
	nameMap map[string]string // lowercase -> actual name
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
	Tools          []string // allowed tool names
	GuardedTools   []string // tools that require human approval before execution
	NotifyTools    []string // tools that auto-execute but send a notification
	Secrets        []string // secret names this skill needs (injected into http_request tool)
	OAuthProviders []string // OAuth provider names this skill can use (e.g. ["google", "github"])
	Delegate       string   // "claude" for subprocess dispatch, empty for LLM agent
	ClaudeModel    string   // --model flag override
	ClaudeTools    string   // --allowed-tools override
	WorkDir        string   // --add-dir override
}

// BuildSubAgents creates a specialist sub-agent for each skill in the repository,
// plus a general-purpose sub-agent with the provided tools. These sub-agents are
// used as members of the Coordinator Team.
// It also returns a map of guarded tool names to their owning skill name.
// If guardedRunner is non-nil, skills with guarded_tools will use GuardedSkillAgent
// which implements proper graph-based pause/resume semantics.
// The callbacks parameter is kept for notify-tier tools on non-guarded agents.
func BuildSubAgents(m model.Model, skillsRepo skill.Repository, generalTools []tool.Tool, callbacks *tool.Callbacks, guardedRunner *GuardedSkillRunner, logger *slog.Logger) ([]agent.Agent, map[string]string, map[string]string, error) {
	var agents []agent.Agent
	guardedTools := make(map[string]string) // tool name -> skill name
	notifyTools := make(map[string]string)  // tool name -> skill name

	// Log callback status at entry
	logger.Info("SKILLS BuildSubAgents called", "hasCallbacks", callbacks != nil, "callbacks_ptr", fmt.Sprintf("%p", callbacks))

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

			// Collect guarded and notify tools for this skill.
			for _, gt := range fm.GuardedTools {
				guardedTools[gt] = summary.Name
			}
			for _, nt := range fm.NotifyTools {
				notifyTools[nt] = summary.Name
			}
			if len(fm.GuardedTools) > 0 || len(fm.NotifyTools) > 0 {
				logger.Info("skill tool gates", "skill", summary.Name, "guarded", fm.GuardedTools, "notify", fm.NotifyTools)
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

			// If skill declares secrets or oauth_providers, inject http_request tool with appropriate auth
			if len(fm.Secrets) > 0 || len(fm.OAuthProviders) > 0 {
				skillSecrets := make(map[string]string)

				// Fetch secrets
				if len(fm.Secrets) > 0 {
					store := secrets.DefaultStore()
					for _, secretName := range fm.Secrets {
						val, err := store.Get(secretName)
						if err != nil {
							logger.Warn("skill secret not found", "skill", summary.Name, "secret", secretName, "error", err)
							continue
						}
						skillSecrets[secretName] = val
					}
				}

				// Validate OAuth providers are configured
				if len(fm.OAuthProviders) > 0 && cfg != nil {
					for _, provider := range fm.OAuthProviders {
						if _, ok := cfg.GetOAuthProvider(provider); !ok {
							logger.Warn("skill oauth provider not configured", "skill", summary.Name, "provider", provider)
						}
					}
				}

				// Create http_request tool with OAuth support
				// Note: userID is "default" for now - skills don't have user context at build time
				// The actual user ID should be passed through the request context at runtime
				httpTool := NewHttpRequestToolWithOAuth(
					skillSecrets,
					"default", // TODO: get user ID from context at runtime
					fm.OAuthProviders,
					getOAuthTokenGetter(),
				)
				agentTools = append(agentTools, httpTool)

				if len(skillSecrets) > 0 || len(fm.OAuthProviders) > 0 {
					logger.Info("skill auth injected", "skill", summary.Name, "secrets", len(skillSecrets), "oauth_providers", fm.OAuthProviders)
				}
			}

			// If skill has guarded tools and we have a runner, use GuardedSkillAgent
			// which implements proper graph-based pause/resume for approvals.
			if len(fm.GuardedTools) > 0 && guardedRunner != nil {
				sa := NewGuardedSkillAgent(
					summary.Name,
					summary.Description,
					instruction,
					agentTools,
					guardedRunner,
					logger,
				)
				agents = append(agents, sa)
				logger.Info("created guarded skill sub-agent", "name", summary.Name, "tools", len(agentTools), "guarded", fm.GuardedTools)
				continue
			}

			// For skills without guarded tools, use standard llmagent
			opts := []llmagent.Option{
				llmagent.WithModel(m),
				llmagent.WithInstruction(instruction),
				llmagent.WithDescription(summary.Description),
				llmagent.WithTools(agentTools),
				llmagent.WithMaxLLMCalls(25),
				llmagent.WithMaxToolIterations(30),
			}
			if callbacks != nil {
				opts = append(opts, llmagent.WithToolCallbacks(callbacks))
				logger.Info("SKILLS: Attaching callbacks to skill sub-agent", "skill", summary.Name, "callbacks_ptr", fmt.Sprintf("%p", callbacks))
			}
			sa := llmagent.New(summary.Name, opts...)
			logger.Info("SKILLS: Created sub-agent object", "skill", summary.Name, "agent_ptr", fmt.Sprintf("%p", sa))

			agents = append(agents, sa)
			logger.Info("created skill sub-agent", "name", summary.Name, "tools", len(agentTools))
		}
	}

	// Create a general-purpose sub-agent with the standard tools (read, write, exec, etc.).
	gpOpts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithTools(generalTools),
		llmagent.WithInstruction("You are a general-purpose assistant. Use the available tools to complete tasks. Report your results clearly."),
		llmagent.WithDescription("General-purpose agent with file read/write, exec, and other standard tools. Use for tasks that don't match a specific skill."),
		llmagent.WithMaxLLMCalls(25),
		llmagent.WithMaxToolIterations(30),
	}
	if callbacks != nil {
		gpOpts = append(gpOpts, llmagent.WithToolCallbacks(callbacks))
		logger.Info("SKILLS: Attaching callbacks to general sub-agent", "callbacks_ptr", fmt.Sprintf("%p", callbacks))
	} else {
		logger.Info("SKILLS: NO callbacks for general sub-agent")
	}
	gp := llmagent.New("general", gpOpts...)
	logger.Info("SKILLS: Created general sub-agent object", "agent_ptr", fmt.Sprintf("%p", gp))
	agents = append(agents, gp)

	if len(agents) == 0 {
		return nil, nil, nil, fmt.Errorf("no sub-agents created")
	}

	return agents, guardedTools, notifyTools, nil
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
		case "notify_tools":
			val = strings.Trim(val, "[]")
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					fm.NotifyTools = append(fm.NotifyTools, t)
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
		case "secrets":
			val = strings.Trim(val, "[]")
			for _, s := range strings.Split(val, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					fm.Secrets = append(fm.Secrets, s)
				}
			}
		case "oauth_providers":
			val = strings.Trim(val, "[]")
			for _, p := range strings.Split(val, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					fm.OAuthProviders = append(fm.OAuthProviders, p)
				}
			}
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
