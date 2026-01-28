// Package agent implements the Kaggen AI agent using trpc-agent-go.
package agent

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/team"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/memory"
)

const (
	// AgentName is the name of the Kaggen agent.
	AgentName = "kaggen"
)

// Agent wraps a trpc-agent-go Team for Kaggen's coordinator pattern.
type Agent struct {
	team   *team.Team
	memory *memory.FileMemory
	tools  []tool.Tool
	model  model.Model
	logger *slog.Logger
}

// NewAgent creates a new Kaggen agent using the Coordinator Team pattern.
// When subAgents is non-empty, a Team is created with the coordinator delegating
// to specialist sub-agents. When subAgents is empty, a single-agent team is
// created with a general-purpose member as a fallback.
func NewAgent(m model.Model, tools []tool.Tool, mem *memory.FileMemory, subAgents []agent.Agent, logger *slog.Logger) (*Agent, error) {
	// Build instruction from bootstrap files.
	instruction, err := buildInstruction(mem)
	if err != nil {
		return nil, fmt.Errorf("build instruction: %w", err)
	}

	// The coordinator agent: receives user messages, decomposes tasks,
	// delegates to sub-agents (exposed as tools by the Team), and
	// synthesizes results.
	coordinator := llmagent.New(AgentName,
		llmagent.WithModel(m),
		llmagent.WithTools(tools), // coordinator keeps direct tools for simple tasks
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Kaggen personal AI assistant coordinator"),
	)

	// If no sub-agents were provided, create a minimal general-purpose member
	// so the team has at least one member.
	if len(subAgents) == 0 {
		gp := llmagent.New("general",
			llmagent.WithModel(m),
			llmagent.WithTools(tools),
			llmagent.WithInstruction("You are a general-purpose assistant. Use the available tools to complete tasks."),
			llmagent.WithDescription("General-purpose agent with standard tools."),
		)
		subAgents = []agent.Agent{gp}
	}

	t, err := team.New(
		coordinator,
		subAgents,
		team.WithMemberToolConfig(team.MemberToolConfig{
			HistoryScope: team.HistoryScopeIsolated,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}

	return &Agent{
		team:   t,
		memory: mem,
		tools:  tools,
		model:  m,
		logger: logger,
	}, nil
}

// Run executes the agent with the given invocation.
func (a *Agent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return a.team.Run(ctx, invocation)
}

// Tools returns the list of tools available to this agent.
func (a *Agent) Tools() []tool.Tool {
	return a.team.Tools()
}

// Info returns basic information about this agent.
func (a *Agent) Info() agent.Info {
	return a.team.Info()
}

// SubAgents returns the list of sub-agents available to this agent.
func (a *Agent) SubAgents() []agent.Agent {
	return a.team.SubAgents()
}

// FindSubAgent finds a sub-agent by name.
func (a *Agent) FindSubAgent(name string) agent.Agent {
	return a.team.FindSubAgent(name)
}

// buildInstruction constructs the system instruction from bootstrap files.
func buildInstruction(mem *memory.FileMemory) (string, error) {
	bootstrap, err := mem.LoadBootstrap()
	if err != nil {
		return "", fmt.Errorf("load bootstrap: %w", err)
	}

	var instruction string
	instruction = "You are Kaggen, a personal AI assistant.\n\n"

	if bootstrap != "" {
		instruction += "## Context & Instructions\n\n"
		instruction += bootstrap
		instruction += "\n\n"
	}

	instruction += "## Operating Guidelines\n\n"
	instruction += "- Be helpful, direct, and concise\n"
	instruction += "- Use tools when needed to accomplish tasks\n"
	instruction += "- Ask for clarification if a request is ambiguous\n"
	instruction += "- When you complete a task, summarize what you did\n"
	instruction += "- To send a file to the user (e.g. show an image, deliver a document), include [send_file: /path/to/file] in your response. The file will be delivered through the chat channel.\n"
	instruction += "\n"
	instruction += "## Task Orchestration\n\n"
	instruction += "You have access to specialist sub-agents as tools. For complex or multi-step tasks:\n"
	instruction += "1. Decompose the task into sub-tasks\n"
	instruction += "2. Delegate each sub-task to the most appropriate specialist sub-agent\n"
	instruction += "3. For simple questions or tasks you can handle directly, do so without delegating\n"
	instruction += "4. Synthesize results from sub-agents into a coherent response for the user\n"
	instruction += "5. If a sub-agent fails, try a different approach or ask the user for guidance\n"

	return instruction, nil
}
