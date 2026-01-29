// Package agent implements the Kaggen AI agent using trpc-agent-go.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	trpcmemory "trpc.group/trpc-go/trpc-agent-go/memory"
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
	team          *team.Team
	memory        *memory.FileMemory
	tools         []tool.Tool
	model         model.Model
	logger        *slog.Logger
	inFlightStore *InFlightStore
	dispatcher    *asyncDispatcher
}

// NewAgent creates a new Kaggen agent using the Coordinator Team pattern.
// When subAgents is non-empty, a Team is created with the coordinator delegating
// to specialist sub-agents. When subAgents is empty, a single-agent team is
// created with a general-purpose member as a fallback.
//
// completeFn is called when an async sub-agent finishes. It may be nil during
// construction and set later via SetCompletionFunc (to break circular deps).
func NewAgent(m model.Model, tools []tool.Tool, mem *memory.FileMemory, subAgents []agent.Agent, completeFn CompletionFunc, memSvc trpcmemory.Service, logger *slog.Logger, maxHistoryRuns ...int) (*Agent, error) {
	// Build instruction from bootstrap files.
	instruction, err := buildInstruction(mem, subAgents)
	if err != nil {
		return nil, fmt.Errorf("build instruction: %w", err)
	}

	// If no sub-agents were provided, create a minimal general-purpose member.
	if len(subAgents) == 0 {
		gp := llmagent.New("general",
			llmagent.WithModel(m),
			llmagent.WithTools(tools),
			llmagent.WithInstruction("You are a general-purpose assistant. Use the available tools to complete tasks."),
			llmagent.WithDescription("General-purpose agent with standard tools."),
		)
		subAgents = []agent.Agent{gp}
	}

	// Build the async dispatch infrastructure.
	store := NewInFlightStore()
	agentMap := make(map[string]agent.Agent, len(subAgents))
	for _, sa := range subAgents {
		agentMap[sa.Info().Name] = sa
	}

	// Use a no-op completion func if none provided yet.
	if completeFn == nil {
		completeFn = func(taskID, result string, err error, policy TriggerPolicy) {}
	}

	dispatchTool, dispatcher := NewAsyncDispatchTool(agentMap, store, completeFn, m, memSvc, logger)
	statusTool := NewTaskStatusTool(store)

	// Coordinator gets direct tools + async dispatch + task status.
	allTools := make([]tool.Tool, 0, len(tools)+2)
	allTools = append(allTools, tools...)
	allTools = append(allTools, dispatchTool, statusTool)

	// Default to 40 history messages if not specified (prevents unbounded context growth).
	historyLimit := 40
	if len(maxHistoryRuns) > 0 && maxHistoryRuns[0] > 0 {
		historyLimit = maxHistoryRuns[0]
	}

	coordinatorOpts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithTools(allTools),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Kaggen personal AI assistant coordinator"),
		llmagent.WithMaxHistoryRuns(historyLimit),
	}

	coordinator := llmagent.New(AgentName, coordinatorOpts...)

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
		team:          t,
		memory:        mem,
		tools:         tools,
		model:         m,
		logger:        logger,
		inFlightStore: store,
		dispatcher:    dispatcher,
	}, nil
}

// InFlightStore returns the async task store for external components
// (e.g. the handler) to query task state.
func (a *Agent) InFlightStore() *InFlightStore {
	return a.inFlightStore
}

// SetCompletionFunc updates the async dispatch completion callback.
// Call this after handler construction to wire up completion event injection.
func (a *Agent) SetCompletionFunc(fn CompletionFunc) {
	a.dispatcher.SetCompletionFunc(fn)
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
func buildInstruction(mem *memory.FileMemory, subAgents []agent.Agent) (string, error) {
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
	instruction += "You have access to specialist sub-agents. You can invoke them in two ways:\n\n"
	instruction += "### Synchronous (team member tools)\n"
	instruction += "Call a sub-agent directly as a tool. The call blocks until the sub-agent finishes. Use this for quick tasks.\n\n"
	instruction += "### Asynchronous (dispatch_task)\n"
	instruction += "Use the `dispatch_task` tool to run a sub-agent in the background. It returns immediately with a task ID.\n"
	instruction += "Use `task_status` to check progress. When the task completes, a completion event will be injected into your session.\n"
	instruction += "Use async dispatch for long-running tasks (e.g. building software, research, data processing).\n\n"
	instruction += "### Guidelines\n"
	instruction += "1. For simple questions or tasks you can handle directly, do so without delegating\n"
	instruction += "2. Decompose complex tasks into sub-tasks and delegate to specialists\n"
	instruction += "3. Use async dispatch for tasks that may take a long time\n"
	instruction += "4. Notify the user when you start long-running work and when it completes\n"
	instruction += "5. Synthesize results from sub-agents into a coherent response for the user\n"
	instruction += "6. If a sub-agent fails, try a different approach or ask the user for guidance\n"
	instruction += "7. When you receive a [Task Completed] message, summarize the result for the user\n"

	// List available sub-agents.
	if len(subAgents) > 0 {
		instruction += "\n### Available Sub-Agents\n\n"
		for _, sa := range subAgents {
			info := sa.Info()
			instruction += fmt.Sprintf("- **%s**: %s\n", info.Name, info.Description)
		}
	}

	// Build agent name list for dispatch_task reference.
	if len(subAgents) > 0 {
		names := make([]string, len(subAgents))
		for i, sa := range subAgents {
			names[i] = sa.Info().Name
		}
		instruction += fmt.Sprintf("\nValid agent_name values for dispatch_task: %s\n", strings.Join(names, ", "))
	}

	return instruction, nil
}
