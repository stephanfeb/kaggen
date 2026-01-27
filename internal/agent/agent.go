// Package agent implements the Kaggen AI agent using trpc-agent-go.
package agent

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/memory"
)

const (
	// AgentName is the name of the Kaggen agent.
	AgentName = "kaggen"
)

// Agent wraps a trpc-agent-go llmagent for Kaggen.
type Agent struct {
	llmAgent *llmagent.LLMAgent
	memory   *memory.FileMemory
	tools    []tool.Tool
	model    model.Model
	logger   *slog.Logger
}

// NewAgent creates a new Kaggen agent.
// skillsRepo is an optional skill repository (may be nil).
func NewAgent(m model.Model, tools []tool.Tool, mem *memory.FileMemory, skillsRepo skill.Repository, logger *slog.Logger) (*Agent, error) {
	// Build instruction from bootstrap files
	instruction, err := buildInstruction(mem)
	if err != nil {
		return nil, fmt.Errorf("build instruction: %w", err)
	}

	opts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithTools(tools),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Kaggen personal AI assistant"),
	}

	// Enable framework skill support if a repository is provided.
	if skillsRepo != nil {
		opts = append(opts,
			llmagent.WithSkills(skillsRepo),
			llmagent.WithCodeExecutor(localexec.New()),
		)
	}

	la := llmagent.New(AgentName, opts...)

	return &Agent{
		llmAgent: la,
		memory:   mem,
		tools:    tools,
		model:    m,
		logger:   logger,
	}, nil
}

// Run executes the agent with the given invocation.
func (a *Agent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return a.llmAgent.Run(ctx, invocation)
}

// Tools returns the list of tools available to this agent.
func (a *Agent) Tools() []tool.Tool {
	return a.llmAgent.Tools()
}

// Info returns basic information about this agent.
func (a *Agent) Info() agent.Info {
	return a.llmAgent.Info()
}

// SubAgents returns the list of sub-agents available to this agent.
func (a *Agent) SubAgents() []agent.Agent {
	return a.llmAgent.SubAgents()
}

// FindSubAgent finds a sub-agent by name.
func (a *Agent) FindSubAgent(name string) agent.Agent {
	return a.llmAgent.FindSubAgent(name)
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

	return instruction, nil
}
