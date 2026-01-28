package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// TriggerPolicy controls how the coordinator is notified when a sub-agent completes.
type TriggerPolicy string

const (
	// TriggerAuto immediately triggers a coordinator turn on completion.
	TriggerAuto TriggerPolicy = "auto"
	// TriggerQueue queues the result for the next user-triggered turn.
	TriggerQueue TriggerPolicy = "queue"
)

// TaskStatus represents the status of an in-flight or completed async task.
type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
)

// TaskState tracks the state of an async sub-agent task.
type TaskState struct {
	ID        string        `json:"id"`
	AgentName string        `json:"agent_name"`
	Task      string        `json:"task"`
	Status    TaskStatus    `json:"status"`
	Result    string        `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
	Policy    TriggerPolicy `json:"policy"`
	SessionID string        `json:"session_id,omitempty"`
	UserID    string        `json:"user_id,omitempty"`
	StartedAt time.Time    `json:"started_at"`
	DoneAt    *time.Time   `json:"done_at,omitempty"`
}

// InFlightStore tracks in-flight and recently completed async tasks.
type InFlightStore struct {
	mu    sync.RWMutex
	tasks map[string]*TaskState
}

// NewInFlightStore creates a new in-flight task store.
func NewInFlightStore() *InFlightStore {
	return &InFlightStore{
		tasks: make(map[string]*TaskState),
	}
}

// Register adds a new running task to the store.
func (s *InFlightStore) Register(id, agentName, task string, policy TriggerPolicy, sessionID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id] = &TaskState{
		ID:        id,
		AgentName: agentName,
		Task:      task,
		Status:    TaskRunning,
		Policy:    policy,
		SessionID: sessionID,
		UserID:    userID,
		StartedAt: time.Now(),
	}
}

// Complete marks a task as completed with a result.
func (s *InFlightStore) Complete(id, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = TaskCompleted
		t.Result = result
		now := time.Now()
		t.DoneAt = &now
	}
}

// Fail marks a task as failed with an error.
func (s *InFlightStore) Fail(id, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = TaskFailed
		t.Error = errMsg
		now := time.Now()
		t.DoneAt = &now
	}
}

// Get returns a task by ID.
func (s *InFlightStore) Get(id string) (*TaskState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return t, ok
}

// List returns all tasks, optionally filtered by status.
func (s *InFlightStore) List(status TaskStatus) []*TaskState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*TaskState
	for _, t := range s.tasks {
		if status == "" || t.Status == status {
			out = append(out, t)
		}
	}
	return out
}

// QueuedResults returns completed tasks with queue policy, then removes them.
func (s *InFlightStore) QueuedResults() []*TaskState {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*TaskState
	for id, t := range s.tasks {
		if t.Policy == TriggerQueue && (t.Status == TaskCompleted || t.Status == TaskFailed) {
			out = append(out, t)
			delete(s.tasks, id)
		}
	}
	return out
}

// CompletionFunc is called when an async sub-agent finishes.
// It receives the task ID, result, error, and trigger policy.
type CompletionFunc func(taskID, result string, err error, policy TriggerPolicy)

// asyncDispatchRequest is the input schema for the async dispatch tool.
type asyncDispatchRequest struct {
	AgentName string `json:"agent_name" jsonschema:"required,description=Name of the sub-agent to dispatch"`
	Task      string `json:"task" jsonschema:"required,description=The task description to send to the sub-agent"`
	Policy    string `json:"policy,omitempty" jsonschema:"description=Completion trigger policy: auto (default) or queue,enum=auto,enum=queue"`
}

// asyncDispatchResponse is the output schema for the async dispatch tool.
type asyncDispatchResponse struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

// asyncDispatcher holds references needed to dispatch async sub-agent tasks.
type asyncDispatcher struct {
	agents        map[string]agent.Agent
	store         *InFlightStore
	completeFn    CompletionFunc
	mu            sync.RWMutex // protects completeFn
	model         model.Model
	memoryService memory.Service
	logger        *slog.Logger
}

// SetCompletionFunc updates the completion callback. This is used to wire up
// the handler after agent construction (breaking the circular dependency).
func (d *asyncDispatcher) SetCompletionFunc(fn CompletionFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completeFn = fn
}

func (d *asyncDispatcher) getCompleteFn() CompletionFunc {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.completeFn
}

// dispatch spawns a sub-agent in a goroutine and returns immediately.
func (d *asyncDispatcher) dispatch(ctx context.Context, req asyncDispatchRequest) (asyncDispatchResponse, error) {
	ag, ok := d.agents[req.AgentName]
	if !ok {
		available := make([]string, 0, len(d.agents))
		for name := range d.agents {
			available = append(available, name)
		}
		return asyncDispatchResponse{}, fmt.Errorf("unknown agent %q, available: %s", req.AgentName, strings.Join(available, ", "))
	}

	policy := TriggerPolicy(req.Policy)
	if policy == "" {
		policy = TriggerAuto
	}

	// Extract originating session/user from the invocation context so
	// completion events can be routed back to the correct session.
	var sessionID, userID string
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv.Session != nil {
		sessionID = inv.Session.ID
		userID = inv.Session.UserID
	}

	taskID := uuid.New().String()
	d.store.Register(taskID, req.AgentName, req.Task, policy, sessionID, userID)
	d.logger.Info("dispatched async task", "task_id", taskID, "agent", req.AgentName, "policy", policy, "session_id", sessionID)

	go func() {
		invOpts := []agent.InvocationOptions{
			agent.WithInvocationAgent(ag),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: req.Task,
			}),
			agent.WithInvocationModel(d.model),
		}
		if d.memoryService != nil {
			invOpts = append(invOpts, agent.WithInvocationMemoryService(d.memoryService))
		}
		inv := agent.NewInvocation(invOpts...)

		// Wrap context with the invocation so tools (e.g. memory_search)
		// can retrieve it via agent.InvocationFromContext(ctx).
		bgCtx := agent.NewInvocationContext(context.Background(), inv)

		events, err := ag.Run(bgCtx, inv)
		if err != nil {
			d.store.Fail(taskID, err.Error())
			d.getCompleteFn()(taskID, "", err, policy)
			return
		}

		// Collect the final text result from events.
		var result string
		for evt := range events {
			if evt == nil || evt.Response == nil {
				continue
			}
			if evt.Response.Error != nil {
				d.store.Fail(taskID, evt.Response.Error.Message)
				d.getCompleteFn()(taskID, "", fmt.Errorf("%s", evt.Response.Error.Message), policy)
				return
			}
			if len(evt.Response.Choices) > 0 {
				c := evt.Response.Choices[0]
				if c.Message.Content != "" {
					result = c.Message.Content
				}
			}
		}

		d.store.Complete(taskID, result)
		d.logger.Info("async task completed", "task_id", taskID, "agent", req.AgentName)
		d.getCompleteFn()(taskID, result, nil, policy)
	}()

	return asyncDispatchResponse{
		Status: "accepted",
		TaskID: taskID,
	}, nil
}

// NewAsyncDispatchTool creates a tool that dispatches tasks to sub-agents asynchronously.
// It returns both the tool and the dispatcher, so the caller can update the
// completion function later via SetCompletionFunc.
func NewAsyncDispatchTool(agents map[string]agent.Agent, store *InFlightStore, completeFn CompletionFunc, m model.Model, memSvc memory.Service, logger *slog.Logger) (tool.Tool, *asyncDispatcher) {
	d := &asyncDispatcher{
		agents:        agents,
		store:         store,
		completeFn:    completeFn,
		model:         m,
		memoryService: memSvc,
		logger:        logger,
	}
	t := function.NewFunctionTool(
		d.dispatch,
		function.WithName("dispatch_task"),
		function.WithDescription("Dispatch a task to a specialist sub-agent for asynchronous execution. Returns immediately with a task ID. Use task_status to check progress. Available agents: use the agent_name parameter to specify which specialist to use."),
	)
	return t, d
}
