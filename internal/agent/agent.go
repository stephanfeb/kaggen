// Package agent implements the Kaggen AI agent using trpc-agent-go.
package agent

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/memory"
)

const (
	// AgentName is the name of the Kaggen agent.
	AgentName = "kaggen"
)

// Agent wraps a trpc-agent-go graphagent for Kaggen.
type Agent struct {
	graphAgent agent.Agent
	memory     *memory.FileMemory
	tools      []tool.Tool
	model      model.Model
	logger     *slog.Logger
}

// NewAgent creates a new Kaggen agent.
func NewAgent(m model.Model, tools []tool.Tool, mem *memory.FileMemory, logger *slog.Logger) (*Agent, error) {
	// Build instruction from bootstrap files
	instruction, err := buildInstruction(mem)
	if err != nil {
		return nil, fmt.Errorf("build instruction: %w", err)
	}

	// Create the graph for the agent
	g, err := buildAgentGraph(m, tools, instruction)
	if err != nil {
		return nil, fmt.Errorf("build graph: %w", err)
	}

	// Create the graph agent
	graphAgent, err := graphagent.New(
		AgentName,
		g,
		graphagent.WithDescription("Kaggen personal AI assistant"),
	)
	if err != nil {
		return nil, fmt.Errorf("create graph agent: %w", err)
	}

	return &Agent{
		graphAgent: graphAgent,
		memory:     mem,
		tools:      tools,
		model:      m,
		logger:     logger,
	}, nil
}

// Run executes the agent with the given invocation.
func (a *Agent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return a.graphAgent.Run(ctx, invocation)
}

// Tools returns the list of tools available to this agent.
func (a *Agent) Tools() []tool.Tool {
	return a.tools
}

// Info returns basic information about this agent.
func (a *Agent) Info() agent.Info {
	return a.graphAgent.Info()
}

// SubAgents returns the list of sub-agents available to this agent.
func (a *Agent) SubAgents() []agent.Agent {
	return a.graphAgent.SubAgents()
}

// FindSubAgent finds a sub-agent by name.
func (a *Agent) FindSubAgent(name string) agent.Agent {
	return a.graphAgent.FindSubAgent(name)
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

	return instruction, nil
}

// buildAgentGraph creates the graph for the Kaggen agent.
// This is a simple ReAct-style graph with an LLM node and tools node.
func buildAgentGraph(m model.Model, tools []tool.Tool, instruction string) (*graph.Graph, error) {
	// Create tool map for the LLM node
	toolMap := make(map[string]tool.Tool)
	for _, t := range tools {
		decl := t.Declaration()
		toolMap[decl.Name] = t
	}

	// Use the predefined messages state schema
	schema := graph.MessagesStateSchema()

	// Build the graph with LLM and tools nodes
	g, err := graph.NewStateGraph(schema).
		// LLM node - takes user input and decides what to do
		AddLLMNode("llm", m, instruction, toolMap).
		// Tools node - executes tool calls from LLM
		AddToolsNode("tools", toolMap).
		// Set entry point
		SetEntryPoint("llm").
		// Add conditional edges after LLM node
		AddConditionalEdges(
			"llm",
			routeAfterLLM,
			map[string]string{
				"tools": "tools",
				"end":   graph.End,
			},
		).
		// After tools execution, go back to LLM
		AddEdge("tools", "llm").
		// Compile the graph
		Compile()

	if err != nil {
		return nil, fmt.Errorf("compile graph: %w", err)
	}

	return g, nil
}

// routeAfterLLM determines the next step after the LLM node.
// If there were tool calls, go to tools node. Otherwise, end.
func routeAfterLLM(ctx context.Context, state graph.State) (string, error) {
	// Check if there are pending tool calls by looking at the messages
	messages, ok := state[graph.StateKeyMessages].([]model.Message)
	if !ok || len(messages) == 0 {
		return "end", nil
	}

	// Get the last message (should be assistant message with potential tool calls)
	lastMsg := messages[len(messages)-1]
	if lastMsg.Role == model.RoleAssistant && len(lastMsg.ToolCalls) > 0 {
		return "tools", nil
	}

	return "end", nil
}
