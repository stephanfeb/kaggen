package agent

import (
	"context"
	"sync/atomic"

	"github.com/yourusername/kaggen/internal/pipeline"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// AgentProvider wraps an atomic pointer to an Agent, implementing the agent.Agent
// interface by delegating to the current agent. This enables hot-reload: a new
// Agent can be swapped in atomically while in-flight requests drain on the old one.
type AgentProvider struct {
	current atomic.Pointer[Agent]
}

// NewAgentProvider creates a provider initialised with the given agent.
func NewAgentProvider(a *Agent) *AgentProvider {
	p := &AgentProvider{}
	p.current.Store(a)
	return p
}

// Swap atomically replaces the current agent.
func (p *AgentProvider) Swap(a *Agent) {
	p.current.Store(a)
}

// Current returns the current agent.
func (p *AgentProvider) Current() *Agent {
	return p.current.Load()
}

// Run delegates to the current agent. The agent pointer is loaded once so the
// entire request executes against the same agent even if a swap occurs mid-flight.
func (p *AgentProvider) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return p.current.Load().Run(ctx, invocation)
}

// Info delegates to the current agent.
func (p *AgentProvider) Info() agent.Info {
	return p.current.Load().Info()
}

// Tools delegates to the current agent.
func (p *AgentProvider) Tools() []tool.Tool {
	return p.current.Load().Tools()
}

// SubAgents delegates to the current agent.
func (p *AgentProvider) SubAgents() []agent.Agent {
	return p.current.Load().SubAgents()
}

// FindSubAgent delegates to the current agent.
func (p *AgentProvider) FindSubAgent(name string) agent.Agent {
	return p.current.Load().FindSubAgent(name)
}

// Pipelines returns the loaded pipeline definitions from the current agent.
func (p *AgentProvider) Pipelines() []pipeline.Pipeline {
	return p.current.Load().Pipelines()
}

// InFlightStore returns the in-flight task store from the current agent.
func (p *AgentProvider) InFlightStore() *InFlightStore {
	return p.current.Load().InFlightStore()
}

// SetCompletionFunc sets the completion callback on the current agent.
func (p *AgentProvider) SetCompletionFunc(fn CompletionFunc) {
	p.current.Load().SetCompletionFunc(fn)
}
