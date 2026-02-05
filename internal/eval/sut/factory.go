// Package sut provides the System Under Test factory for production-equivalent eval testing.
package sut

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/memory"
	"github.com/yourusername/kaggen/internal/tools"
)

// SystemUnderTest wraps the production-equivalent agent system for eval testing.
type SystemUnderTest struct {
	Agent      *agent.Agent
	Workspace  string
	SkillsDir  string
	SkillNames []string // Names of loaded skills for tracking sync delegation
	Logger     *slog.Logger
}

// Config configures the System Under Test.
type Config struct {
	// Model to use for the coordinator and sub-agents
	Model model.Model

	// Workspace directory for test files (created if empty)
	Workspace string

	// Directory containing skill definitions for testing
	SkillsDir string

	// Bootstrap files to use (optional - creates minimal if empty)
	BootstrapDir string

	// Execution limits
	MaxTurns int
	Timeout  time.Duration

	// Logger
	Logger *slog.Logger
}

// New creates a production-equivalent agent system for eval testing.
// This builds the FULL coordinator + sub-agents team, exactly like production.
func New(cfg Config) (*SystemUnderTest, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	// Create workspace if not provided
	workspace := cfg.Workspace
	if workspace == "" {
		var err error
		workspace, err = os.MkdirTemp("", "eval-sut-*")
		if err != nil {
			return nil, fmt.Errorf("create temp workspace: %w", err)
		}
	}

	// Set up bootstrap files in workspace
	if err := setupBootstrap(workspace, cfg.BootstrapDir); err != nil {
		return nil, fmt.Errorf("setup bootstrap: %w", err)
	}

	// Create file memory
	fileMemory := memory.NewFileMemory(workspace)

	// Create tools
	agentTools := tools.DefaultTools(workspace)

	// Collect skill directories - resolve to absolute paths
	var skillDirs []string
	var skillNames []string
	if cfg.SkillsDir != "" {
		absPath, err := filepath.Abs(cfg.SkillsDir)
		if err != nil {
			logger.Warn("Failed to resolve skills dir to absolute path", "path", cfg.SkillsDir, "error", err)
			absPath = cfg.SkillsDir
		}
		skillDirs = append(skillDirs, absPath)
		logger.Info("Loading skills", "dir", absPath)

		// Extract skill names from directory for tracking sync delegation
		skillNames = extractSkillNames(absPath, logger)
		logger.Info("Found skills", "count", len(skillNames), "names", skillNames)
	}

	// Build the FULL coordinator + sub-agents team using production code
	ag, err := agent.BuildInitialAgent(
		cfg.Model,
		agentTools,
		fileMemory,
		skillDirs,
		nil, // memory service (not needed for eval)
		nil, // backlog store (not needed for eval)
		logger,
		cfg.MaxTurns,
	)
	if err != nil {
		return nil, fmt.Errorf("build agent: %w", err)
	}

	return &SystemUnderTest{
		Agent:      ag,
		Workspace:  workspace,
		SkillsDir:  cfg.SkillsDir,
		SkillNames: skillNames,
		Logger:     logger,
	}, nil
}

// Cleanup removes temporary resources created by the SUT.
func (s *SystemUnderTest) Cleanup() {
	// Only remove workspace if it was auto-created (starts with eval-sut-)
	if filepath.Base(s.Workspace)[:9] == "eval-sut-" {
		os.RemoveAll(s.Workspace)
	}
}

// setupBootstrap creates minimal bootstrap files in the workspace.
func setupBootstrap(workspace, bootstrapDir string) error {
	// If bootstrap dir provided, copy files from there
	if bootstrapDir != "" {
		return copyBootstrapFiles(bootstrapDir, workspace)
	}

	// Create minimal bootstrap files for eval
	files := map[string]string{
		"SOUL.md": `# Soul

You are a helpful AI assistant. Your purpose is to help users with their tasks.

## Core Values

- Be helpful and direct
- Use available skills appropriately
- Ask for clarification when instructions are ambiguous
`,
		"TOOLS.md": `# Tool Usage

## Available Tools

### read
Read file contents. Use for examining files.

### write
Write or create files.

### exec
Execute shell commands.

## Guidelines

- Use the read tool to view file contents
- Use the write tool to create or modify files
- Use exec for shell commands like git, ls, find
`,
		"AGENTS.md": `# Operating Instructions

## Skill Selection

When you receive a task:
1. Assess what the task requires
2. Select the most appropriate skill from your available skills
3. If the task is ambiguous, ask for clarification
4. Delegate to the selected skill

## Response Style

- Be concise but thorough
- Report results clearly
`,
	}

	for name, content := range files {
		path := filepath.Join(workspace, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return nil
}

// copyBootstrapFiles copies bootstrap files from source to dest directory.
func copyBootstrapFiles(src, dest string) error {
	bootstrapFiles := []string{"SOUL.md", "IDENTITY.md", "AGENTS.md", "TOOLS.md", "USER.md", "MEMORY.md"}

	for _, name := range bootstrapFiles {
		srcPath := filepath.Join(src, name)
		destPath := filepath.Join(dest, name)

		data, err := os.ReadFile(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Skip missing files
			}
			return fmt.Errorf("read %s: %w", name, err)
		}

		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return nil
}

// InFlightStore returns the in-flight task store from the agent.
func (s *SystemUnderTest) InFlightStore() *agent.InFlightStore {
	return s.Agent.InFlightStore()
}

// extractSkillNames reads skill names from a skills directory.
// Skills are identified by directories containing a SKILL.md file.
func extractSkillNames(skillsDir string, logger *slog.Logger) []string {
	var names []string

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		logger.Warn("Failed to read skills directory", "dir", skillsDir, "error", err)
		return names
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillFile := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			names = append(names, entry.Name())
		}
	}

	return names
}
