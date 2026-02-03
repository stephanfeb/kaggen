package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/pipeline"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
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
	TaskRunning         TaskStatus = "running"
	TaskCompleted       TaskStatus = "completed"
	TaskFailed          TaskStatus = "failed"
	TaskCancelled       TaskStatus = "cancelled"
	TaskPendingApproval TaskStatus = "pending_approval"
)

// TokenUsage tracks token consumption for a task or event.
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
}

// TaskEvent records a single event during task execution.
type TaskEvent struct {
	Timestamp time.Time    `json:"timestamp"`
	Type      string       `json:"type"` // "tool_call", "response", "error"
	Turn      int          `json:"turn"`
	Tools     []string     `json:"tools,omitempty"`
	Content   string       `json:"content,omitempty"` // preview (200 chars max)
	Tokens    *TokenUsage  `json:"tokens,omitempty"`
}

// ApprovalRequest holds details about a tool call awaiting human approval.
type ApprovalRequest struct {
	ToolName    string    `json:"tool_name"`
	Arguments   string    `json:"arguments"`    // raw JSON tool arguments
	SkillName   string    `json:"skill_name"`
	Description string    `json:"description"`  // human-readable summary
	RequestedAt time.Time `json:"requested_at"`
}

// TaskState tracks the state of an async sub-agent task.
type TaskState struct {
	ID              string             `json:"id"`
	AgentName       string             `json:"agent_name"`
	Task            string             `json:"task"`
	Status          TaskStatus         `json:"status"`
	Result          string             `json:"result,omitempty"`
	Error           string             `json:"error,omitempty"`
	Policy          TriggerPolicy      `json:"policy"`
	SessionID       string             `json:"session_id,omitempty"`
	UserID          string             `json:"user_id,omitempty"`
	StartedAt       time.Time          `json:"started_at"`
	DoneAt          *time.Time         `json:"done_at,omitempty"`
	Events          []*TaskEvent       `json:"events,omitempty"`
	TurnCount       int                `json:"turn_count"`
	TotalTokens     *TokenUsage        `json:"total_tokens,omitempty"`
	External        bool               `json:"external,omitempty"`        // true for external (non-agent) tasks awaiting callback
	CallbackSecret  string             `json:"-"`                         // HMAC secret for callback verification
	TimeoutAt       time.Time          `json:"timeout_at,omitempty"`     // auto-fail if no callback by this time
	ApprovalRequest *ApprovalRequest   `json:"approval_request,omitempty"` // set when status is pending_approval
	cancelFn        context.CancelFunc `json:"-"`                         // for external cancellation
}

// TaskEventCallback is called when a task event is added, enabling real-time broadcast.
type TaskEventCallback func(taskID string, evt *TaskEvent)

// InFlightStore tracks in-flight and recently completed async tasks.
type InFlightStore struct {
	mu          sync.RWMutex
	tasks       map[string]*TaskState
	onEvent     TaskEventCallback
	onEventLock sync.RWMutex

	// pipelineProgress tracks completed pipeline stages per session.
	// Key: sessionID → pipelineName → set of completed 1-based stage indices.
	pipelineProgress map[string]map[string]map[int]bool

	// pipelineStartTimes tracks when each pipeline was started per session.
	pipelineStartTimes map[string]map[string]time.Time

	// pipelineDescriptions stores a human-readable task description per pipeline run.
	pipelineDescriptions map[string]map[string]string // sessionID → pipeline → description
}

// NewInFlightStore creates a new in-flight task store.
func NewInFlightStore() *InFlightStore {
	return &InFlightStore{
		tasks:                make(map[string]*TaskState),
		pipelineProgress:     make(map[string]map[string]map[int]bool),
		pipelineStartTimes:   make(map[string]map[string]time.Time),
		pipelineDescriptions: make(map[string]map[string]string),
	}
}

// SetEventCallback sets a callback invoked on every task event (for dashboard broadcast).
func (s *InFlightStore) SetEventCallback(fn TaskEventCallback) {
	s.onEventLock.Lock()
	defer s.onEventLock.Unlock()
	s.onEvent = fn
}

// AddEvent appends an event to a task's event log and updates turn count.
func (s *InFlightStore) AddEvent(id string, evt *TaskEvent) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if ok {
		t.Events = append(t.Events, evt)
		t.TurnCount = evt.Turn
		// Accumulate tokens
		if evt.Tokens != nil {
			if t.TotalTokens == nil {
				t.TotalTokens = &TokenUsage{}
			}
			t.TotalTokens.Input += evt.Tokens.Input
			t.TotalTokens.Output += evt.Tokens.Output
			t.TotalTokens.Total += evt.Tokens.Total
		}
	}
	s.mu.Unlock()

	// Fire callback outside lock
	s.onEventLock.RLock()
	fn := s.onEvent
	s.onEventLock.RUnlock()
	if fn != nil && ok {
		fn(id, evt)
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

// UpdateTask updates the task description (used to enrich coordinator task names).
func (s *InFlightStore) UpdateTask(id, task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Task = task
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
		// Don't overwrite a cancellation — Cancel() already set the status.
		if t.Status == TaskCancelled {
			return
		}
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
		if t.Policy == TriggerQueue && (t.Status == TaskCompleted || t.Status == TaskFailed || t.Status == TaskCancelled) {
			out = append(out, t)
			delete(s.tasks, id)
		}
	}
	return out
}

// RecordPipelineStage marks a pipeline stage as completed for a session.
func (s *InFlightStore) RecordPipelineStage(sessionID, pipelineName string, stage int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipelineProgress[sessionID] == nil {
		s.pipelineProgress[sessionID] = make(map[string]map[int]bool)
	}
	if s.pipelineProgress[sessionID][pipelineName] == nil {
		s.pipelineProgress[sessionID][pipelineName] = make(map[int]bool)
	}
	s.pipelineProgress[sessionID][pipelineName][stage] = true
}

// IsPipelineStageCompleted checks if a specific pipeline stage has completed for a session.
func (s *InFlightStore) IsPipelineStageCompleted(sessionID, pipelineName string, stage int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pipelineProgress[sessionID] == nil {
		return false
	}
	if s.pipelineProgress[sessionID][pipelineName] == nil {
		return false
	}
	return s.pipelineProgress[sessionID][pipelineName][stage]
}

// PipelineProgress returns a copy of all pipeline progress data.
// Structure: sessionID → pipelineName → set of completed stage indices.
func (s *InFlightStore) PipelineProgress() map[string]map[string]map[int]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]map[string]map[int]bool, len(s.pipelineProgress))
	for sid, pipelines := range s.pipelineProgress {
		cp[sid] = make(map[string]map[int]bool, len(pipelines))
		for pname, stages := range pipelines {
			cp[sid][pname] = make(map[int]bool, len(stages))
			for stage := range stages {
				cp[sid][pname][stage] = true
			}
		}
	}
	return cp
}

// SetCancelFunc stores a cancel function for a task, enabling external cancellation.
func (s *InFlightStore) SetCancelFunc(id string, fn context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.cancelFn = fn
	}
}

// Cancel cancels a running task. Returns true if the task was found and cancelled.
func (s *InFlightStore) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Status != TaskRunning {
		return false
	}
	if t.cancelFn != nil {
		t.cancelFn()
	}
	t.Status = TaskCancelled
	t.Error = "cancelled by user"
	now := time.Now()
	t.DoneAt = &now
	return true
}

// RegisterApproval registers a tool call that is pending human approval.
// It behaves like an external task — the reaper will auto-fail it on timeout.
func (s *InFlightStore) RegisterApproval(id, skillName, toolName, args, description, sessionID, userID string, timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id] = &TaskState{
		ID:        id,
		AgentName: skillName,
		Task:      description,
		Status:    TaskPendingApproval,
		Policy:    TriggerAuto,
		SessionID: sessionID,
		UserID:    userID,
		StartedAt: time.Now(),
		External:  true, // reuse external reaper for timeout
		TimeoutAt: time.Now().Add(timeout),
		ApprovalRequest: &ApprovalRequest{
			ToolName:    toolName,
			Arguments:   args,
			SkillName:   skillName,
			Description: description,
			RequestedAt: time.Now(),
		},
	}
}

// RegisterExternal registers an external task that will be completed via HTTP callback.
func (s *InFlightStore) RegisterExternal(id, name, secret string, policy TriggerPolicy, sessionID, userID string, timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id] = &TaskState{
		ID:             id,
		AgentName:      "external",
		Task:           name,
		Status:         TaskRunning,
		Policy:         policy,
		SessionID:      sessionID,
		UserID:         userID,
		StartedAt:      time.Now(),
		External:       true,
		CallbackSecret: secret,
		TimeoutAt:      time.Now().Add(timeout),
	}
}

// StartExternalReaper launches a goroutine that periodically checks for
// expired external tasks and marks them as failed. It stops when ctx is cancelled.
func (s *InFlightStore) StartExternalReaper(ctx context.Context, onTimeout func(taskID string, state *TaskState)) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reapExpiredExternal(onTimeout)
			}
		}
	}()
}

func (s *InFlightStore) reapExpiredExternal(onTimeout func(taskID string, state *TaskState)) {
	now := time.Now()
	s.mu.Lock()
	var expired []*TaskState
	for _, t := range s.tasks {
		if t.External && (t.Status == TaskRunning || t.Status == TaskPendingApproval) && !t.TimeoutAt.IsZero() && now.After(t.TimeoutAt) {
			t.Status = TaskFailed
			t.Error = "timed out waiting for callback"
			doneAt := now
			t.DoneAt = &doneAt
			expired = append(expired, t)
		}
	}
	s.mu.Unlock()

	for _, t := range expired {
		if onTimeout != nil {
			onTimeout(t.ID, t)
		}
	}
}

// RecordPipelineStart records the start time and task description of a pipeline for a session.
func (s *InFlightStore) RecordPipelineStart(sessionID, pipelineName, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipelineStartTimes[sessionID] == nil {
		s.pipelineStartTimes[sessionID] = make(map[string]time.Time)
	}
	// Only record if not already started (don't reset on retry).
	if _, exists := s.pipelineStartTimes[sessionID][pipelineName]; !exists {
		s.pipelineStartTimes[sessionID][pipelineName] = time.Now()
	}
	if s.pipelineDescriptions[sessionID] == nil {
		s.pipelineDescriptions[sessionID] = make(map[string]string)
	}
	if s.pipelineDescriptions[sessionID][pipelineName] == "" {
		// Truncate to a short summary.
		desc := description
		if len(desc) > 120 {
			desc = desc[:120] + "..."
		}
		s.pipelineDescriptions[sessionID][pipelineName] = desc
	}
}

// PipelineDescription returns the task description for a pipeline run.
func (s *InFlightStore) PipelineDescription(sessionID, pipelineName string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pipelineDescriptions[sessionID] == nil {
		return ""
	}
	return s.pipelineDescriptions[sessionID][pipelineName]
}

// PipelineElapsed returns how long a pipeline has been running for a session.
func (s *InFlightStore) PipelineElapsed(sessionID, pipelineName string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pipelineStartTimes[sessionID] == nil {
		return 0
	}
	start, ok := s.pipelineStartTimes[sessionID][pipelineName]
	if !ok {
		return 0
	}
	return time.Since(start)
}

// CompletionFunc is called when an async sub-agent finishes.
// It receives the task ID, result, error, and trigger policy.
type CompletionFunc func(taskID, result string, err error, policy TriggerPolicy)

// asyncDispatchRequest is the input schema for the async dispatch tool.
type asyncDispatchRequest struct {
	AgentName     string `json:"agent_name" jsonschema:"required,description=Name of the sub-agent to dispatch"`
	Task          string `json:"task" jsonschema:"required,description=The task description to send to the sub-agent"`
	Policy        string `json:"policy,omitempty" jsonschema:"description=Completion trigger policy: auto (default) or queue,enum=auto,enum=queue"`
	Pipeline      string `json:"pipeline,omitempty" jsonschema:"description=Optional pipeline name. When set stage gates are enforced (stages must complete in order). Omit for standalone agent dispatch."`
	BacklogItemID string `json:"backlog_item_id,omitempty" jsonschema:"description=Optional backlog item ID to track. Status is auto-updated on completion or failure."`
}

// asyncDispatchResponse is the output schema for the async dispatch tool.
type asyncDispatchResponse struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

// asyncDispatcher holds references needed to dispatch async sub-agent tasks.
type asyncDispatcher struct {
	agents         map[string]agent.Agent
	store          *InFlightStore
	backlogStore   *backlog.Store
	completeFn     CompletionFunc
	mu             sync.RWMutex // protects completeFn
	model          model.Model
	memoryService  memory.Service
	logger         *slog.Logger
	pipelines      []pipeline.Pipeline
	pipelineAgents map[string]pipeline.PipelineAgent
	maxTurns       int
	supervisor     *Supervisor
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
	d.logger.Info("ASYNC dispatch: using agent", "name", req.AgentName, "agent_ptr", fmt.Sprintf("%p", ag))

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

	// Pipeline stage gate: only enforced when pipeline mode is explicitly requested.
	if req.Pipeline != "" {
		if pa, ok := d.pipelineAgents[req.AgentName]; ok && pa.Pipeline == req.Pipeline && pa.Stage > 1 {
			for stage := 1; stage < pa.Stage; stage++ {
				if !d.store.IsPipelineStageCompleted(sessionID, pa.Pipeline, stage) {
					priorAgent := pipeline.FindAgentAtStage(d.pipelines, pa.Pipeline, stage)
					return asyncDispatchResponse{}, fmt.Errorf(
						"cannot dispatch %q — pipeline %q requires stage %d (%s) to complete first; wait for the [Task Completed] callback",
						req.AgentName, pa.Pipeline, stage, priorAgent)
				}
			}
		}

		// Pipeline-level timeout: reject if total pipeline time exceeds limit.
		const maxPipelineDuration = 2 * time.Hour
		if pa, ok := d.pipelineAgents[req.AgentName]; ok && pa.Pipeline == req.Pipeline {
			if pa.Stage == 1 {
				d.store.RecordPipelineStart(sessionID, pa.Pipeline, req.Task)
			}
			if elapsed := d.store.PipelineElapsed(sessionID, pa.Pipeline); elapsed > maxPipelineDuration {
				return asyncDispatchResponse{}, fmt.Errorf(
					"pipeline %q has been running for %s (limit %s); aborting",
					pa.Pipeline, elapsed.Round(time.Minute), maxPipelineDuration)
			}
		}
	}

	taskID := uuid.New().String()
	d.store.Register(taskID, req.AgentName, req.Task, policy, sessionID, userID)
	d.logger.Info("dispatched async task", "task_id", taskID, "agent", req.AgentName, "policy", policy, "session_id", sessionID)

	// Inject project-specific context (AGENTS.md) if a project directory
	// is referenced in the task text.
	taskMessage := req.Task
	if projectCtx := loadProjectContext(req.Task); projectCtx != "" {
		taskMessage = "## Project Context\n\n" + projectCtx + "\n\n---\n\n" + req.Task
		d.logger.Info("injected project context", "task_id", taskID, "agent", req.AgentName)
	}

	go func() {
		// Helper to mark a linked backlog item as failed.
		failBacklogItem := func() {
			if req.BacklogItemID != "" && d.backlogStore != nil {
				failedStatus := "failed"
				_ = d.backlogStore.Update(req.BacklogItemID, backlog.Update{Status: &failedStatus})
			}
		}

		invOpts := []agent.InvocationOptions{
			agent.WithInvocationAgent(ag),
			agent.WithInvocationMessage(model.Message{
				Role:    model.RoleUser,
				Content: taskMessage,
			}),
			agent.WithInvocationModel(d.model),
			// Provide a session to prevent nil pointer in code executor
			// (codeexecution.go accesses invocation.Session.ID).
			agent.WithInvocationSession(&trpcsession.Session{
				ID:     taskID,
				UserID: sessionID,
			}),
		}
		if d.memoryService != nil {
			invOpts = append(invOpts, agent.WithInvocationMemoryService(d.memoryService))
		}
		inv := agent.NewInvocation(invOpts...)

		// Seed the session with the user message event so that
		// ApplyEventFiltering (called inside UpdateUserSession) finds
		// at least one user message and does not wipe all events.
		// Without this, every call to UpdateUserSession clears
		// session.Events because the filtering requires a user message.
		userEvt := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
			Choices: []model.Choice{{
				Index:   0,
				Message: model.Message{Role: model.RoleUser, Content: taskMessage},
			}},
		})
		inv.Session.UpdateUserSession(userEvt)

		// NOTE: Safety limits (MaxLLMCalls, MaxToolIterations) are set at agent
		// construction time via llmagent.WithMaxLLMCalls/WithMaxToolIterations.
		// Setting them here on the invocation has no effect because
		// llmagent.setupInvocation() overwrites them with the agent's options.

		// Create a context with timeout to enforce hard deadline.
		bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		d.store.SetCancelFunc(taskID, cancel)

		// Wrap context with the invocation so tools (e.g. memory_search)
		// can retrieve it via agent.InvocationFromContext(ctx).
		bgCtx = agent.NewInvocationContext(bgCtx, inv)

		events, err := ag.Run(bgCtx, inv)
		if err != nil {
			d.store.Fail(taskID, err.Error())
			failBacklogItem()
			d.getCompleteFn()(taskID, "", err, policy)
			return
		}

		// Collect the final text result from events and record task events.
		var result string
		var turnCount int
		var consecutiveErrors int
		const maxConsecutiveErrors = 3
		for evt := range events {
			if evt == nil {
				continue
			}

			// Persist event to session so the next LLM call sees
			// conversation history. Without a Runner, session.Events
			// stays empty and each call starts fresh.
			inv.Session.UpdateUserSession(evt)

			// Signal completion for barrier events. The flow loop
			// blocks until NotifyCompletion is called. Must happen
			// AFTER UpdateUserSession so history is available when
			// the flow unblocks and calls the LLM again.
			if evt.RequiresCompletion {
				key := agent.GetAppendEventNoticeKey(evt.ID)
				_ = inv.NotifyCompletion(bgCtx, key)
			}

			if evt.Response == nil {
				continue
			}
			turnCount++

			// Turn limit circuit breaker.
			if turnCount >= d.maxTurns {
				errMsg := fmt.Sprintf("aborted: exceeded %d turns without completing", d.maxTurns)
				d.logger.Warn("turn limit reached", "task_id", taskID, "agent", req.AgentName, "turns", turnCount)
				d.store.Fail(taskID, errMsg)
				failBacklogItem()
				d.getCompleteFn()(taskID, "", fmt.Errorf("%s", errMsg), policy)
				cancel()
				return
			}

			// Capture token usage from response.
			var tokens *TokenUsage
			if evt.Response.Usage != nil {
				tokens = &TokenUsage{
					Input:  evt.Response.Usage.PromptTokens,
					Output: evt.Response.Usage.CompletionTokens,
					Total:  evt.Response.Usage.TotalTokens,
				}
			}

			if evt.Response.Error != nil {
				d.store.AddEvent(taskID, &TaskEvent{
					Timestamp: time.Now(),
					Type:      "error",
					Turn:      turnCount,
					Content:   evt.Response.Error.Message,
					Tokens:    tokens,
				})
				d.logger.Error("async task error",
					"task_id", taskID,
					"agent", req.AgentName,
					"turn", turnCount,
					"error", evt.Response.Error.Message)
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					d.logger.Warn("circuit breaker: aborting after consecutive errors",
						"task_id", taskID, "agent", req.AgentName, "count", consecutiveErrors)
					d.store.Fail(taskID, fmt.Sprintf("aborted after %d consecutive errors: %s", consecutiveErrors, evt.Response.Error.Message))
					failBacklogItem()
					d.getCompleteFn()(taskID, "", fmt.Errorf("aborted after %d consecutive errors", consecutiveErrors), policy)
					cancel()
					return
				}
				continue
			}

			if len(evt.Response.Choices) > 0 {
				consecutiveErrors = 0
				c := evt.Response.Choices[0]

				// Record and log tool calls
				if len(c.Message.ToolCalls) > 0 {
					toolNames := make([]string, len(c.Message.ToolCalls))
					for i, tc := range c.Message.ToolCalls {
						toolNames[i] = tc.Function.Name
					}
					d.store.AddEvent(taskID, &TaskEvent{
						Timestamp: time.Now(),
						Type:      "tool_call",
						Turn:      turnCount,
						Tools:     toolNames,
						Tokens:    tokens,
					})
					d.logger.Info("async task tool calls",
						"task_id", taskID,
						"agent", req.AgentName,
						"turn", turnCount,
						"tools", toolNames)
				}

				// Record and log text content
				if c.Message.Content != "" {
					result = c.Message.Content
					preview := c.Message.Content
					if len(preview) > 200 {
						preview = preview[:200] + "..."
					}
					d.store.AddEvent(taskID, &TaskEvent{
						Timestamp: time.Now(),
						Type:      "response",
						Turn:      turnCount,
						Content:   preview,
						Tokens:    tokens,
					})
					d.logger.Info("async task response",
						"task_id", taskID,
						"agent", req.AgentName,
						"turn", turnCount,
						"content_length", len(c.Message.Content),
						"content_preview", preview)
				}

				if c.FinishReason != nil {
					d.logger.Debug("async task finish reason",
						"task_id", taskID,
						"agent", req.AgentName,
						"turn", turnCount,
						"finish_reason", *c.FinishReason)
				}
			}

			// Supervisor evaluation: check if agent is on track.
			if d.supervisor != nil {
				// Build a TaskEvent for the supervisor from the last recorded event.
				var supervisorEvt *TaskEvent
				if evt.Response.Error != nil {
					supervisorEvt = &TaskEvent{Type: "error", Content: evt.Response.Error.Message}
				} else if len(evt.Response.Choices) > 0 {
					c := evt.Response.Choices[0]
					supervisorEvt = &TaskEvent{Type: "response", Content: c.Message.Content, Turn: turnCount}
					if len(c.Message.ToolCalls) > 0 {
						supervisorEvt.Type = "tool_call"
						names := make([]string, len(c.Message.ToolCalls))
						for i, tc := range c.Message.ToolCalls {
							names[i] = tc.Function.Name
						}
						supervisorEvt.Tools = names
					}
				}

				if supervisorEvt != nil {
					verdict := d.supervisor.Evaluate(taskID, supervisorEvt, req.Task)
					switch verdict.Action {
					case "correct":
						if claudeAg, ok := ag.(*ClaudeAgent); ok {
							if !d.supervisor.IncrementCorrections(taskID) {
								errMsg := "exceeded max corrections: " + verdict.Reason
								d.logger.Warn("supervisor: aborting task", "task_id", taskID, "reason", errMsg)
								d.store.Fail(taskID, errMsg)
								failBacklogItem()
								d.getCompleteFn()(taskID, "", fmt.Errorf("%s", errMsg), policy)
								cancel()
								return
							}
							sessionID := claudeAg.Kill()
							d.store.AddEvent(taskID, &TaskEvent{
								Timestamp: time.Now(),
								Type:      "supervisor_correction",
								Turn:      turnCount,
								Content:   verdict.Reason,
							})
							d.logger.Info("supervisor: correcting agent",
								"task_id", taskID, "reason", verdict.Reason, "session_id", sessionID)
							newEvents, err := claudeAg.Resume(bgCtx, inv, sessionID, verdict.Correction)
							if err != nil {
								errMsg := "resume failed: " + err.Error()
								d.store.Fail(taskID, errMsg)
								failBacklogItem()
								d.getCompleteFn()(taskID, "", fmt.Errorf("%s", errMsg), policy)
								cancel()
								return
							}
							// Swap to the new event channel — drain remaining events from the
							// resumed subprocess by recursing into a helper or re-reading.
							// Since we can't reassign the range variable, we drain the new channel inline.
							for resumeEvt := range newEvents {
								if resumeEvt == nil {
									continue
								}
								inv.Session.UpdateUserSession(resumeEvt)
								if resumeEvt.RequiresCompletion {
									key := agent.GetAppendEventNoticeKey(resumeEvt.ID)
									_ = inv.NotifyCompletion(bgCtx, key)
								}
								if resumeEvt.Response == nil {
									continue
								}
								turnCount++
								if turnCount >= d.maxTurns {
									errMsg := fmt.Sprintf("aborted: exceeded %d turns without completing", d.maxTurns)
									d.store.Fail(taskID, errMsg)
									failBacklogItem()
									d.getCompleteFn()(taskID, "", fmt.Errorf("%s", errMsg), policy)
									cancel()
									return
								}
								if resumeEvt.Response.Error != nil {
									d.store.AddEvent(taskID, &TaskEvent{
										Timestamp: time.Now(), Type: "error", Turn: turnCount,
										Content: resumeEvt.Response.Error.Message,
									})
									continue
								}
								if len(resumeEvt.Response.Choices) > 0 {
									rc := resumeEvt.Response.Choices[0]
									if rc.Message.Content != "" {
										result = rc.Message.Content
										preview := rc.Message.Content
										if len(preview) > 200 {
											preview = preview[:200] + "..."
										}
										d.store.AddEvent(taskID, &TaskEvent{
											Timestamp: time.Now(), Type: "response", Turn: turnCount, Content: preview,
										})
									}
									if len(rc.Message.ToolCalls) > 0 {
										names := make([]string, len(rc.Message.ToolCalls))
										for i, tc := range rc.Message.ToolCalls {
											names[i] = tc.Function.Name
										}
										d.store.AddEvent(taskID, &TaskEvent{
											Timestamp: time.Now(), Type: "tool_call", Turn: turnCount, Tools: names,
										})
									}
								}
							}
							// After draining resumed events, we're done with this dispatch.
							// Break out of the original event loop.
							break
						}
					case "abort":
						d.logger.Warn("supervisor: aborting task", "task_id", taskID, "reason", verdict.Reason)
						d.store.Fail(taskID, verdict.Reason)
						failBacklogItem()
						d.getCompleteFn()(taskID, "", fmt.Errorf("%s", verdict.Reason), policy)
						cancel()
						return
					}
				}
			}
		}

		d.logger.Info("async task event loop done",
			"task_id", taskID,
			"agent", req.AgentName,
			"total_turns", turnCount)

		// If the context was cancelled, determine whether it was a user cancellation
		// or a timeout/circuit breaker.
		if bgCtx.Err() != nil {
			// Check if Cancel() already marked this as TaskCancelled.
			if t, ok := d.store.Get(taskID); ok && t.Status == TaskCancelled {
				d.logger.Info("async task cancelled by user", "task_id", taskID, "agent", req.AgentName)
				errMsg := "cancelled by user"
				if req.Pipeline != "" {
					if pa, ok := d.pipelineAgents[req.AgentName]; ok && pa.Pipeline == req.Pipeline {
						errMsg = fmt.Sprintf("cancelled by user — pipeline %q stage %d (%s) can be retried by dispatching the same agent again",
							pa.Pipeline, pa.Stage, req.AgentName)
					}
				}
				failBacklogItem()
				d.getCompleteFn()(taskID, "", fmt.Errorf("%s", errMsg), policy)
				return
			}
			errMsg := fmt.Sprintf("timed out after %d turns", turnCount)
			d.store.Fail(taskID, errMsg)
			failBacklogItem()
			d.logger.Warn("async task timed out", "task_id", taskID, "agent", req.AgentName, "turns", turnCount)
			d.getCompleteFn()(taskID, "", fmt.Errorf("%s", errMsg), policy)
			return
		}

		d.store.Complete(taskID, result)
		// Update backlog item status if linked.
		if req.BacklogItemID != "" && d.backlogStore != nil {
			summary := result
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
			if err := d.backlogStore.Complete(req.BacklogItemID, summary); err != nil {
				d.logger.Warn("failed to complete backlog item", "backlog_item_id", req.BacklogItemID, "error", err)
			} else {
				d.logger.Info("backlog item completed", "backlog_item_id", req.BacklogItemID)
				// Check if parent plan is now fully done.
				item, getErr := d.backlogStore.Get(req.BacklogItemID)
				if getErr == nil && item.ParentID != "" {
					if allDone, checkErr := d.backlogStore.CheckParentCompletion(item.ParentID); checkErr == nil && allDone {
						completedStatus := "completed"
						_ = d.backlogStore.Update(item.ParentID, backlog.Update{Status: &completedStatus})
						d.logger.Info("parent plan auto-completed", "parent_id", item.ParentID)
					}
				}
			}
		}
		// Record pipeline stage completion so subsequent stages can be dispatched.
		if req.Pipeline != "" {
			if pa, ok := d.pipelineAgents[req.AgentName]; ok && pa.Pipeline == req.Pipeline {
				d.store.RecordPipelineStage(sessionID, pa.Pipeline, pa.Stage)
				d.logger.Info("pipeline stage completed", "pipeline", pa.Pipeline, "stage", pa.Stage, "agent", req.AgentName, "session_id", sessionID)
			}
		}
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
func NewAsyncDispatchTool(agents map[string]agent.Agent, store *InFlightStore, completeFn CompletionFunc, m model.Model, memSvc memory.Service, logger *slog.Logger, pipelines []pipeline.Pipeline, pipelineAgents map[string]pipeline.PipelineAgent, bStore *backlog.Store, supervisor *Supervisor, maxTurns ...int) (tool.Tool, *asyncDispatcher) {
	turns := 75
	if len(maxTurns) > 0 && maxTurns[0] > 0 {
		turns = maxTurns[0]
	}
	d := &asyncDispatcher{
		agents:         agents,
		store:          store,
		backlogStore:   bStore,
		completeFn:     completeFn,
		model:          m,
		memoryService:  memSvc,
		logger:         logger,
		pipelines:      pipelines,
		pipelineAgents: pipelineAgents,
		maxTurns:       turns,
		supervisor:     supervisor,
	}
	t := function.NewFunctionTool(
		d.dispatch,
		function.WithName("dispatch_task"),
		function.WithDescription("Dispatch a task to a specialist sub-agent for asynchronous execution. Returns immediately with a task ID. Use task_status to check progress. Available agents: use the agent_name parameter to specify which specialist to use."),
	)
	return t, d
}
