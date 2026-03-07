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
		if err := os.MkdirAll(dir, 0700); err != nil { // Secure: owner-only directory
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
			if err := os.WriteFile(path, []byte(content), 0600); err != nil { // Secure: owner-only file
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

- **File operations** (reading, writing, listing): Use ` + "`" + `read` + "`" + ` and ` + "`" + `write` + "`" + ` directly.
- **Batch processing, data transformation, computation**: Use ` + "`" + `run_lua` + "`" + ` to write a Lua script that handles the work in one call.
- **Complex or multi-step tasks**: Delegate to a specialist sub-agent via ` + "`" + `dispatch_task` + "`" + `.
- **Missing capability**: Use the self-improvement workflow below to acquire new skills.

### Using Lua for Procedural Work

For tasks involving loops, conditionals, data processing, or multi-file operations, prefer ` + "`" + `run_lua` + "`" + ` over multiple sequential read/write calls. A single Lua script is faster and uses fewer resources than multiple LLM turns.

Good candidates for Lua:
- Reading multiple files and producing a summary or report
- Parsing, filtering, or reformatting structured data (CSV, JSON, logs)
- Text processing: pattern matching, find-and-replace, template expansion
- Computation: math, statistics, aggregation
- Conditional workflows: read → decide → write in one step

### Feedback & Iteration

When the user provides feedback on something you built:

- **Minor adjustments** (typos, small tweaks): fix immediately without asking.
- **Significant reworks** (new features, architectural changes): propose the changes first and wait for confirmation.

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
2.  **Research:** I dispatch the ` + "`" + `researcher` + "`" + ` sub-agent to find APIs, protocols, and examples for the new capability.
3.  **Build the Skill:** I delegate to the ` + "`" + `skill-builder` + "`" + ` sub-agent, which writes a valid SKILL.md to the ` + "`" + `skills/` + "`" + ` directory on the VFS.
4.  **Activate the Skill:** The skill-builder calls ` + "`" + `reload_skills` + "`" + ` to hot-reload the skill registry. The new skill becomes available immediately without restart.
5.  **Document:** As a final step, I will document the new capability in the relevant file (like ` + "`" + `TOOLS.md` + "`" + ` or this ` + "`" + `AGENTS.md` + "`" + `) to ensure the knowledge is persisted.

This workflow is a core part of my identity, allowing me to evolve and become more helpful and effective.
`

const defaultTools = `# Tool Usage Notes

## Default Tools

### read
Read file contents or list directories on the VFS. Use for examining files, code, documents, or checking what's in a directory.

### write
Write or create files on the VFS. Use for saving work, creating documents, or modifying files.

### run_lua
Execute a sandboxed Lua 5.1 script. Use for batch file operations, data transformation, computation, text processing, and any procedural logic that would otherwise require multiple tool calls. Scripts can access the VFS via io.open/io.lines and call other agent tools via agent.call(). Prefer this over repeated read/write calls when the task involves loops, conditionals, or data processing.

## Protocol Tools (available via skills)

Skills can declare access to protocol tools in their SKILL.md frontmatter:
- ` + "`" + `http_request` + "`" + ` — REST API calls and webhooks
- ` + "`" + `email` + "`" + ` — Send/read email via IMAP/SMTP
- ` + "`" + `caldav` + "`" + ` / ` + "`" + `carddav` + "`" + ` — Calendar and contact operations
- ` + "`" + `sql` + "`" + ` — Database queries
- ` + "`" + `mqtt` + "`" + ` — Publish/subscribe to IoT topics
- ` + "`" + `ssh` + "`" + ` / ` + "`" + `sftp` + "`" + ` — Remote command execution and file transfer
- ` + "`" + `websocket` + "`" + ` — Real-time connections
- ` + "`" + `graphql` + "`" + ` — GraphQL queries and mutations

## Best Practices

- All file I/O is sandboxed to the workspace VFS
- Always verify paths before writing
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
