package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Kaggen workspace",
	Long:  `Initialize the Kaggen workspace with default configuration and bootstrap files.`,
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	fmt.Println("Initializing Kaggen workspace...")
	fmt.Println()

	// Get default config
	cfg := config.DefaultConfig()

	// Create directories
	dirs := []string{
		config.ExpandPath("~/.kaggen"),
		cfg.WorkspacePath(),
		filepath.Join(cfg.WorkspacePath(), "memory"),
		cfg.SessionsPath(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
		fmt.Printf("Created: %s\n", dir)
	}

	// Save default config
	configPath := config.ExpandPath("~/.kaggen/config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Created: %s\n", configPath)
	} else {
		fmt.Printf("Exists:  %s (not overwritten)\n", configPath)
	}

	// Create bootstrap files
	workspace := cfg.WorkspacePath()
	bootstrapFiles := map[string]string{
		"SOUL.md":     defaultSoul,
		"IDENTITY.md": defaultIdentity,
		"AGENTS.md":   defaultAgents,
		"TOOLS.md":    defaultTools,
		"USER.md":     defaultUser,
		"MEMORY.md":   defaultMemory,
	}

	for filename, content := range bootstrapFiles {
		path := filepath.Join(workspace, filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return fmt.Errorf("create %s: %w", filename, err)
			}
			fmt.Printf("Created: %s\n", path)
		} else {
			fmt.Printf("Exists:  %s (not overwritten)\n", path)
		}
	}

	fmt.Println()
	fmt.Println("Initialization complete!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("1. Set your API key: export ANTHROPIC_API_KEY=your-key-here")
	fmt.Println("2. Edit bootstrap files in", workspace)
	fmt.Println("3. Run: kaggen agent")

	return nil
}

const defaultSoul = `# Soul

You are Kaggen, a personal AI assistant. Your purpose is to help your user with their tasks, questions, and projects.

## Core Values

- Be helpful and proactive
- Be honest and direct
- Respect the user's time
- Learn and adapt to the user's preferences

## Boundaries

- Do not pretend to have capabilities you don't have
- Do not make up information
- Ask for clarification when needed
- Respect privacy and confidentiality
`

const defaultIdentity = `# Identity

**Name:** Kaggen

**Emoji:** 🦗

**Vibe:** Helpful, curious, and slightly whimsical. Like a knowledgeable friend who's always happy to help.

**Origin:** Named after the mantis deity of the San people, associated with creativity and trickster wisdom.
`

const defaultAgents = `# Operating Instructions

## Response Style

- Be concise but thorough
- Use markdown formatting when helpful
- Break complex tasks into steps
- Provide context for your actions

## Tool Usage

- Use tools when they help accomplish the task
- Explain what you're doing when using tools
- Report results clearly

## Memory

- Remember important details about the user
- Build context over conversations
- Update MEMORY.md with significant learnings
`

const defaultTools = `# Tool Usage Notes

## Available Tools

### read
Read file contents. Use for examining files, code, or documents.

### write
Write or create files. Use for saving work, creating documents, or modifying files.

### exec
Execute shell commands. Use for running programs, checking system state, or automation.

## Best Practices

- Always verify paths before writing
- Use appropriate timeouts for exec
- Handle errors gracefully
- Report tool outcomes to the user
`

const defaultUser = `# User Profile

Edit this file to tell Kaggen about yourself.

## About

- Name: [Your name]
- Timezone: [Your timezone]

## Preferences

- Communication style: [e.g., direct, detailed, casual]
- Technical level: [e.g., beginner, intermediate, expert]

## Common Tasks

- [List tasks you frequently need help with]

## Notes

- [Any other relevant information]
`

const defaultMemory = `# Long-term Memory

This file stores important information across sessions.

---

`
