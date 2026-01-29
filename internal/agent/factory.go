package agent

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	trpcagent "trpc.group/trpc-go/trpc-agent-go/agent"
	trpcmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/memory"
)

// BuildInitialAgent loads skills from the given directories and constructs the
// initial Kaggen agent. This is the same logic used by Rebuild(), extracted so
// callers can create the first agent before the factory exists.
func BuildInitialAgent(
	m model.Model,
	tools []tool.Tool,
	fileMemory *memory.FileMemory,
	skillDirs []string,
	memService trpcmemory.Service,
	logger *slog.Logger,
	maxHistoryRuns ...int,
) (*Agent, error) {
	skillsRepo := loadSkills(skillDirs, logger)

	var subAgents []trpcagent.Agent
	if skillsRepo != nil {
		var err error
		subAgents, err = BuildSubAgents(m, skillsRepo, tools, logger)
		if err != nil {
			logger.Warn("failed to build sub-agents, falling back to single agent", "error", err)
		}
		if n := len(skillsRepo.Summaries()); n > 0 {
			logger.Info("skills loaded", "count", n)
		}
	}

	return NewAgent(m, tools, fileMemory, subAgents, nil, memService, logger, maxHistoryRuns...)
}

// loadSkills creates a case-insensitive skill repository from the given directories.
func loadSkills(dirs []string, logger *slog.Logger) skill.Repository {
	if len(dirs) == 0 {
		return nil
	}

	resolved := make([]string, 0, len(dirs))
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			logger.Warn("skill dir resolve failed", "dir", d, "error", err)
			continue
		}
		resolved = append(resolved, abs)
	}

	fsRepo, err := skill.NewFSRepository(resolved...)
	if err != nil {
		logger.Warn("failed to load skills", "error", err)
		return nil
	}
	if fsRepo == nil {
		return nil
	}
	return NewCaseInsensitiveRepository(fsRepo)
}

// AgentFactory holds the stable dependencies needed to rebuild the Kaggen agent.
// On Rebuild(), it creates a fresh skills repository, builds new sub-agents, constructs
// a new Agent (Team), and atomically swaps it into the AgentProvider.
type AgentFactory struct {
	model          model.Model
	tools          []tool.Tool
	fileMemory     *memory.FileMemory
	memService     trpcmemory.Service
	skillDirs      []string
	completeFn     CompletionFunc
	provider       *AgentProvider
	logger         *slog.Logger
	maxHistoryRuns int
	mu             sync.Mutex // serializes rebuilds
}

// NewAgentFactory creates a factory with the given stable dependencies.
// skillDirs are the directories to scan for skills (e.g. workspace/skills, ~/.kaggen/skills).
func NewAgentFactory(
	m model.Model,
	tools []tool.Tool,
	fileMemory *memory.FileMemory,
	memService trpcmemory.Service,
	skillDirs []string,
	provider *AgentProvider,
	logger *slog.Logger,
	maxHistoryRuns ...int,
) *AgentFactory {
	hist := 0
	if len(maxHistoryRuns) > 0 {
		hist = maxHistoryRuns[0]
	}
	return &AgentFactory{
		model:          m,
		tools:          tools,
		fileMemory:     fileMemory,
		memService:     memService,
		skillDirs:      skillDirs,
		provider:       provider,
		logger:         logger,
		maxHistoryRuns: hist,
	}
}

// SetCompletionFunc stores the completion callback. It is re-applied to
// each newly built agent during Rebuild().
func (f *AgentFactory) SetCompletionFunc(fn CompletionFunc) {
	f.mu.Lock()
	f.completeFn = fn
	f.mu.Unlock()

	// Also apply to the current agent immediately.
	f.provider.SetCompletionFunc(fn)
}

// Rebuild loads skills from disk, builds new sub-agents and a new Agent,
// then atomically swaps it into the provider. In-flight requests on the
// old agent continue undisturbed.
func (f *AgentFactory) Rebuild() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Load skills from filesystem.
	skillsRepo := loadSkills(f.skillDirs, f.logger)

	// Build sub-agents from skills.
	var subAgents []trpcagent.Agent
	if skillsRepo != nil {
		var err error
		subAgents, err = BuildSubAgents(f.model, skillsRepo, f.tools, f.logger)
		if err != nil {
			f.logger.Warn("failed to build sub-agents", "error", err)
		}
	}

	// Log what we loaded.
	skillCount := 0
	if skillsRepo != nil {
		skillCount = len(skillsRepo.Summaries())
	}
	f.logger.Info("skills reloaded", "count", skillCount, "sub_agents", len(subAgents))

	// Build new agent.
	ag, err := NewAgent(f.model, f.tools, f.fileMemory, subAgents, f.completeFn, f.memService, f.logger, f.maxHistoryRuns)
	if err != nil {
		return fmt.Errorf("rebuild agent: %w", err)
	}

	// Swap atomically.
	f.provider.Swap(ag)
	return nil
}
