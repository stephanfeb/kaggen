package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/config"
)

var nonInteractive bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Kaggen workspace",
	Long:  `Initialize the Kaggen workspace with default configuration and bootstrap files.`,
	RunE:  runInit,
}

func init() {
	initCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Skip interactive prompts and use defaults")
}

func runInit(cmd *cobra.Command, args []string) error {
	fmt.Println("Initializing Kaggen workspace...")
	fmt.Println()

	cfg := config.DefaultConfig()

	if !nonInteractive {
		scanner := bufio.NewScanner(os.Stdin)
		cfg = promptConfig(scanner, cfg)
		fmt.Println()
	}

	// Create directories
	dirs := []string{
		config.ExpandPath("~/.kaggen"),
		cfg.WorkspacePath(),
		filepath.Join(cfg.WorkspacePath(), "memory"),
		filepath.Join(cfg.WorkspacePath(), "skills"),
		cfg.SessionsPath(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
		fmt.Printf("Created: %s\n", dir)
	}

	// Save config
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

func promptConfig(scanner *bufio.Scanner, cfg *config.Config) *config.Config {
	// --- LLM Provider ---
	fmt.Println("=== LLM Provider ===")
	fmt.Println("  1) anthropic (Claude)")
	fmt.Println("  2) gemini (Google)")
	fmt.Println("  3) zai (GLM)")
	provider := prompt(scanner, "Select provider [1]:", "1")

	var modelDefault string
	var envHint string
	switch provider {
	case "2", "gemini":
		modelDefault = "gemini/gemini-2.0-flash"
		envHint = "GEMINI_API_KEY"
	case "3", "zai":
		modelDefault = "zai/glm-4-plus"
		envHint = "ZAI_API_KEY"
	default:
		modelDefault = "anthropic/claude-haiku-4-5"
		envHint = "ANTHROPIC_API_KEY"
	}

	model := prompt(scanner, fmt.Sprintf("Model [%s]:", modelDefault), modelDefault)
	cfg.Agent.Model = model

	fmt.Printf("  (Set your API key via: export %s=your-key-here)\n", envHint)
	fmt.Println()

	// --- Workspace ---
	fmt.Println("=== Workspace ===")
	workspace := prompt(scanner, "Workspace path [~/.kaggen/workspace]:", "~/.kaggen/workspace")
	cfg.Agent.Workspace = workspace
	fmt.Println()

	// --- Telegram ---
	fmt.Println("=== Telegram Bot ===")
	enableTelegram := prompt(scanner, "Enable Telegram bot? [y/N]:", "n")
	if strings.EqualFold(enableTelegram, "y") || strings.EqualFold(enableTelegram, "yes") {
		cfg.Channels.Telegram.Enabled = true
		token := prompt(scanner, "Bot token (or set TELEGRAM_BOT_TOKEN later) []:", "")
		if token != "" {
			cfg.Channels.Telegram.BotToken = token
		}
	} else {
		cfg.Channels.Telegram.Enabled = false
	}
	fmt.Println()

	// --- Memory ---
	fmt.Println("=== Semantic Memory ===")
	fmt.Println("  Requires Ollama running locally (ollama serve)")
	enableMemory := prompt(scanner, "Enable semantic memory? [Y/n]:", "y")
	if strings.EqualFold(enableMemory, "n") || strings.EqualFold(enableMemory, "no") {
		cfg.Memory.Search.Enabled = false
	} else {
		cfg.Memory.Search.Enabled = true
		cfg.Memory.Search.DBPath = "~/.kaggen/memory.db"
		cfg.Memory.Embedding.Provider = "ollama"
		cfg.Memory.Embedding.Model = "nomic-embed-text"
		cfg.Memory.Embedding.BaseURL = "http://localhost:11434"
		cfg.Memory.Indexing.ChunkSize = 400
		cfg.Memory.Indexing.ChunkOverlap = 80
	}
	fmt.Println()

	// --- Telemetry ---
	fmt.Println("=== Telemetry ===")
	enableTelemetry := prompt(scanner, "Enable telemetry (OTLP/Jaeger)? [y/N]:", "n")
	if strings.EqualFold(enableTelemetry, "y") || strings.EqualFold(enableTelemetry, "yes") {
		cfg.Telemetry.Enabled = true
		cfg.Telemetry.JaegerEndpoint = "localhost:4317"
		cfg.Telemetry.Protocol = "grpc"
		cfg.Telemetry.ServiceName = "kaggen"
	} else {
		cfg.Telemetry.Enabled = false
	}

	return cfg
}

func prompt(scanner *bufio.Scanner, message, defaultVal string) string {
	fmt.Printf("  %s ", message)
	scanner.Scan()
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return defaultVal
	}
	return input
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

### Choosing the Right Tool

- **Software development** (HTML, CSS, JS, dashboards, code, reports with styling): Delegate to the ` + "`" + `coder` + "`" + ` sub-agent. Use ` + "`" + `dispatch_task` + "`" + ` for non-trivial work.
- **Document format conversion** (md→pdf, html→docx): Use the pandoc skill.
- **Database queries**: Use the sqlite3 skill.
- **Image processing**: Use the imagemagick skill.
- **Simple file operations**: Use read/write/exec directly.

### Delegating to the Coder

For any software engineering task — building pages, writing code, debugging, refactoring, running tests — delegate to the ` + "`" + `coder` + "`" + ` sub-agent rather than calling Claude Code directly.

1. **Before dispatching:** Tell the user what you're about to build and that it may take a moment. Example: *"I'm going to build your team dashboard now. This may take a few minutes — I'll let you know as soon as it's ready."*
2. **After completion:** Notify the user with a summary of what was created and where the files are located. Always use absolute paths (not ` + "`" + `~` + "`" + `). Example: *"Your dashboard is ready! I've created it at ` + "`" + `/Users/you/claude-projects/team-dashboard/index.html` + "`" + `. You can open it in your browser to take a look."*

If the task fails, report the error clearly and suggest next steps.

### Feedback & Iteration

When the user provides feedback on something you built:

- **Minor adjustments** (typos, color changes, small tweaks): fix immediately without asking.
- **Significant reworks** (new features, architectural changes, major redesigns): propose the changes first and wait for confirmation before starting a new build.

## Autonomous Work

You are periodically woken up by a cron job to check for and execute pending work. When triggered by a wakeup prompt:

### Priorities
1. **Explicit backlog tasks first** — Check ` + "`" + `backlog_list` + "`" + ` for pending items. These are tasks the user or you explicitly added.
2. **Inferred work second** — Use ` + "`" + `memory_search` + "`" + ` to find open threads, follow-ups you promised, or stale items worth revisiting.
3. **Lightweight maintenance if idle** — If no explicit or inferred work exists, consider read-only housekeeping: check for stale backlog items, review recent task failures for patterns, or scan for obvious optimization opportunities. Never take mutating actions during idle maintenance — queue findings as backlog items for user review.
4. **Do nothing if truly idle** — If maintenance yields nothing, do not generate busywork.

### Execution
- Use ` + "`" + `dispatch_task` + "`" + ` for long-running work so you can handle multiple items per wakeup.
- Update backlog items as you work: set status to ` + "`" + `in_progress` + "`" + ` when starting, ` + "`" + `completed` + "`" + ` when done.
- If a task is blocked or unclear, set it to ` + "`" + `blocked` + "`" + ` and note why — don't guess.

### User Communication
- Always notify the user when you complete autonomous work. Summarize what was done clearly.
- Never take destructive or irreversible actions autonomously (deleting files, sending external requests). Ask the user first.
- If you're unsure whether to act on something, add it to the backlog instead of acting immediately.

### Cost Awareness
- Don't dispatch expensive sub-agents for low-priority or speculative tasks.
- Prefer quick checks and lightweight actions during wakeups.
- Save heavy work (builds, research) for high-priority explicit tasks.

## Memory

- Remember important details about the user
- Build context over conversations
- Update MEMORY.md with significant learnings

---

## Self-Improvement Workflow

I have the capability to create and install new, persistent skills for myself. This allows me to learn from experience and expand my toolset over time. This process involves modifying my own underlying configuration and source code.

### The Process

1.  **Identify a Need:** When I encounter a recurring task that could be automated or a capability that I'm missing (e.g., deploying a Docker container), I will identify it as an opportunity for self-improvement.
2.  **Design and Build:** I will design and build the necessary scripts and configuration for the new skill. This is done in my ` + "`" + `claude-projects` + "`" + ` directory.
3.  **Install the Skill:** I use a dedicated ` + "`" + `skill-builder` + "`" + ` tool to correctly install the new skill's files into my ` + "`" + `~/.kaggen/skills/` + "`" + ` directory.
4.  **Activate the Skill:** After a new skill is installed, I must trigger a dynamic reload of my skill repository to make the new skill available to my running process. This is done by calling the ` + "`" + `system.reload` + "`" + ` skill, which sends a ` + "`" + `SIGUSR1` + "`" + ` signal to my core process, activating the new capability without downtime.
5.  **Document:** As a final step, I will document the new capability in the relevant file (like ` + "`" + `TOOLS.md` + "`" + ` or this ` + "`" + `AGENTS.md` + "`" + `) to ensure the knowledge is persisted.

This workflow is a core part of my identity, allowing me to evolve and become more helpful and effective.
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
