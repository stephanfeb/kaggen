// Package agent provides graph-based execution for skills with guarded tools.
// This implements proper human-in-the-loop approval using the trpc-agent-go
// graph workflow system with checkpoints and interrupts.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/channel"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

// approvalDecisionKey is the context key for passing approval decisions to the approval_gate node.
// This is used because graph.Interrupt() doesn't reliably return resume values in trpc-agent-go.
const approvalDecisionKey contextKey = "approval_decision"

// GuardedExecution tracks a paused graph execution waiting for approval.
type GuardedExecution struct {
	ID           string // execution ID (same as approval ID)
	SessionID    string
	UserID       string
	SkillName    string
	ToolName     string
	ToolArgs     string
	Description  string
	LineageID    string
	CheckpointID string // ID of the checkpoint saved at interrupt time
	InvocationID string // CRITICAL: Original invocation ID - must be reused on resume for correct node positioning
	Graph        *graph.Graph
	Executor     *graph.Executor
	CreatedAt    time.Time
	DecisionCh   chan bool // channel to receive approval decision
}

// GuardedExecutionStore manages paused graph executions awaiting approval.
type GuardedExecutionStore struct {
	mu         sync.RWMutex
	executions map[string]*GuardedExecution // approval ID -> execution
	logger     *slog.Logger
}

// NewGuardedExecutionStore creates a new store.
func NewGuardedExecutionStore(logger *slog.Logger) *GuardedExecutionStore {
	return &GuardedExecutionStore{
		executions: make(map[string]*GuardedExecution),
		logger:     logger,
	}
}

// Store saves a guarded execution.
func (s *GuardedExecutionStore) Store(exec *GuardedExecution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions[exec.ID] = exec
}

// Get retrieves a guarded execution.
func (s *GuardedExecutionStore) Get(id string) (*GuardedExecution, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	exec, ok := s.executions[id]
	return exec, ok
}

// Delete removes a guarded execution.
func (s *GuardedExecutionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.executions, id)
}

// SignalDecision sends an approval decision to a waiting execution.
// Returns true if the execution was found and signaled, false otherwise.
func (s *GuardedExecutionStore) SignalDecision(id string, approved bool) bool {
	s.mu.RLock()
	exec, ok := s.executions[id]
	// Log current state for debugging
	execCount := len(s.executions)
	var execIDs []string
	for eid := range s.executions {
		execIDs = append(execIDs, eid)
	}
	s.mu.RUnlock()

	if s.logger != nil {
		s.logger.Info("SignalDecision: checking execution",
			"approval_id", id,
			"found", ok,
			"total_executions", execCount,
			"all_execution_ids", execIDs)
	}

	if !ok {
		if s.logger != nil {
			s.logger.Warn("SignalDecision: execution not found",
				"approval_id", id)
		}
		return false
	}

	if exec.DecisionCh == nil {
		if s.logger != nil {
			s.logger.Warn("SignalDecision: DecisionCh is nil",
				"approval_id", id,
				"session_id", exec.SessionID,
				"skill", exec.SkillName)
		}
		return false
	}

	select {
	case exec.DecisionCh <- approved:
		if s.logger != nil {
			s.logger.Info("SignalDecision: decision sent successfully",
				"approval_id", id,
				"approved", approved)
		}
		return true
	default:
		if s.logger != nil {
			s.logger.Warn("SignalDecision: channel full or closed",
				"approval_id", id,
				"approved", approved)
		}
		return false
	}
}

// InMemoryCheckpointSaver implements graph.CheckpointSaver for storing execution state.
type InMemoryCheckpointSaver struct {
	mu               sync.RWMutex
	checkpoints      map[string]*graph.Checkpoint         // checkpointID -> checkpoint
	byLineage        map[string][]string                  // lineageID -> []checkpointID (ordered)
	lineageByCP      map[string]string                    // checkpointID -> lineageID (for inheritance)
	writes           map[string][]graph.PendingWrite      // checkpointID -> writes
	metadata         map[string]*graph.CheckpointMetadata // checkpointID -> metadata
	lastCheckpointID string                               // Track most recently saved checkpoint
}

// NewInMemoryCheckpointSaver creates a new checkpoint saver.
func NewInMemoryCheckpointSaver() *InMemoryCheckpointSaver {
	return &InMemoryCheckpointSaver{
		checkpoints: make(map[string]*graph.Checkpoint),
		byLineage:   make(map[string][]string),
		lineageByCP: make(map[string]string),
		writes:      make(map[string][]graph.PendingWrite),
		metadata:    make(map[string]*graph.CheckpointMetadata),
	}
}

// Get retrieves a checkpoint by configuration.
func (s *InMemoryCheckpointSaver) Get(ctx context.Context, config map[string]any) (*graph.Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	checkpointID := graph.GetCheckpointID(config)
	lineageID := graph.GetLineageID(config)

	slog.Info("CheckpointSaver.Get called",
		"checkpoint_id", checkpointID,
		"lineage_id", lineageID,
		"total_checkpoints", len(s.checkpoints),
		"total_lineages", len(s.byLineage))

	if checkpointID != "" {
		cp := s.checkpoints[checkpointID]
		slog.Info("CheckpointSaver.Get by checkpoint_id", "found", cp != nil)
		return cp, nil
	}

	// Return latest for lineage
	if lineageID == "" {
		slog.Info("CheckpointSaver.Get: no lineage_id, returning nil")
		return nil, nil
	}

	ids := s.byLineage[lineageID]
	if len(ids) == 0 {
		slog.Info("CheckpointSaver.Get: no checkpoints for lineage", "lineage_id", lineageID)
		return nil, nil
	}
	latestID := ids[len(ids)-1]
	cp := s.checkpoints[latestID]
	slog.Info("CheckpointSaver.Get: found checkpoint by lineage",
		"lineage_id", lineageID,
		"checkpoint_id", latestID,
		"found", cp != nil)
	return cp, nil
}

// GetTuple retrieves a checkpoint tuple by configuration.
func (s *InMemoryCheckpointSaver) GetTuple(ctx context.Context, config map[string]any) (*graph.CheckpointTuple, error) {
	cp, err := s.Get(ctx, config)
	if err != nil || cp == nil {
		return nil, err
	}
	return &graph.CheckpointTuple{
		Checkpoint: cp,
		Metadata:   s.metadata[cp.ID],
	}, nil
}

// List retrieves checkpoints matching criteria.
func (s *InMemoryCheckpointSaver) List(ctx context.Context, config map[string]any, filter *graph.CheckpointFilter) ([]*graph.CheckpointTuple, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lineageID := graph.GetLineageID(config)
	var result []*graph.CheckpointTuple

	if lineageID != "" {
		for _, id := range s.byLineage[lineageID] {
			if cp, ok := s.checkpoints[id]; ok {
				result = append(result, &graph.CheckpointTuple{
					Checkpoint: cp,
					Metadata:   s.metadata[id],
				})
			}
		}
	} else {
		for id, cp := range s.checkpoints {
			result = append(result, &graph.CheckpointTuple{
				Checkpoint: cp,
				Metadata:   s.metadata[id],
			})
		}
	}
	return result, nil
}

// Put stores a checkpoint.
func (s *InMemoryCheckpointSaver) Put(ctx context.Context, req graph.PutRequest) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Checkpoint.ID == "" {
		req.Checkpoint.ID = uuid.New().String()
	}

	lineageID := graph.GetLineageID(req.Config)

	// CRITICAL FIX: trpc-agent-go only passes lineage_id on the FIRST checkpoint save.
	// Subsequent saves only include checkpoint_id (the parent). We need to inherit
	// the lineage_id from the parent checkpoint to maintain lineage tracking.
	// NOTE: trpc-agent-go puts checkpoint_id at TOP level, not under configurable.
	if lineageID == "" {
		parentID := getParentCheckpointID(req.Config)
		if parentID != "" {
			if inheritedLineage, ok := s.lineageByCP[parentID]; ok {
				lineageID = inheritedLineage
				slog.Info("CheckpointSaver.Put: inherited lineage from parent",
					"parent_id", parentID,
					"lineage_id", lineageID)
			}
		}
	}

	slog.Info("CheckpointSaver.Put called",
		"checkpoint_id", req.Checkpoint.ID,
		"lineage_id", lineageID,
		"config", req.Config)

	s.checkpoints[req.Checkpoint.ID] = req.Checkpoint
	if req.Metadata != nil {
		s.metadata[req.Checkpoint.ID] = req.Metadata
	}

	// Track by lineage
	if lineageID != "" {
		s.byLineage[lineageID] = append(s.byLineage[lineageID], req.Checkpoint.ID)
		s.lineageByCP[req.Checkpoint.ID] = lineageID // Store for inheritance
		slog.Info("CheckpointSaver.Put: stored by lineage",
			"lineage_id", lineageID,
			"checkpoint_count_for_lineage", len(s.byLineage[lineageID]))
	}

	// Track most recent checkpoint
	s.lastCheckpointID = req.Checkpoint.ID

	return map[string]any{"checkpoint_id": req.Checkpoint.ID}, nil
}

// PutWrites stores intermediate writes linked to a checkpoint.
func (s *InMemoryCheckpointSaver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get checkpoint ID from config
	checkpointID := graph.GetCheckpointID(req.Config)
	if checkpointID != "" {
		s.writes[checkpointID] = append(s.writes[checkpointID], req.Writes...)
	}
	return nil
}

// PutFull atomically stores a checkpoint with its pending writes.
func (s *InMemoryCheckpointSaver) PutFull(ctx context.Context, req graph.PutFullRequest) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Checkpoint.ID == "" {
		req.Checkpoint.ID = uuid.New().String()
	}

	lineageID := graph.GetLineageID(req.Config)

	// CRITICAL FIX: trpc-agent-go only passes lineage_id on the FIRST checkpoint save.
	// Subsequent saves only include checkpoint_id (the parent). We need to inherit
	// the lineage_id from the parent checkpoint to maintain lineage tracking.
	// NOTE: trpc-agent-go puts checkpoint_id at TOP level, not under configurable.
	if lineageID == "" {
		parentID := getParentCheckpointID(req.Config)
		if parentID != "" {
			if inheritedLineage, ok := s.lineageByCP[parentID]; ok {
				lineageID = inheritedLineage
				slog.Info("CheckpointSaver.PutFull: inherited lineage from parent",
					"parent_id", parentID,
					"lineage_id", lineageID)
			}
		}
	}

	slog.Info("CheckpointSaver.PutFull called",
		"checkpoint_id", req.Checkpoint.ID,
		"lineage_id", lineageID,
		"pending_writes", len(req.PendingWrites))

	s.checkpoints[req.Checkpoint.ID] = req.Checkpoint
	if req.Metadata != nil {
		s.metadata[req.Checkpoint.ID] = req.Metadata
	}
	if len(req.PendingWrites) > 0 {
		s.writes[req.Checkpoint.ID] = req.PendingWrites
	}

	// Track by lineage
	if lineageID != "" {
		s.byLineage[lineageID] = append(s.byLineage[lineageID], req.Checkpoint.ID)
		s.lineageByCP[req.Checkpoint.ID] = lineageID // Store for inheritance
		slog.Info("CheckpointSaver.PutFull: stored by lineage",
			"lineage_id", lineageID,
			"checkpoint_count_for_lineage", len(s.byLineage[lineageID]))
	}

	// Track most recent checkpoint
	s.lastCheckpointID = req.Checkpoint.ID

	return map[string]any{"checkpoint_id": req.Checkpoint.ID}, nil
}

// GetLastCheckpointID returns the ID of the most recently saved checkpoint.
func (s *InMemoryCheckpointSaver) GetLastCheckpointID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCheckpointID
}

// GetByID retrieves a checkpoint by its ID directly.
func (s *InMemoryCheckpointSaver) GetByID(checkpointID string) *graph.Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkpoints[checkpointID]
}

// DeleteLineage removes all checkpoints for a lineage.
func (s *InMemoryCheckpointSaver) DeleteLineage(ctx context.Context, lineageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range s.byLineage[lineageID] {
		delete(s.checkpoints, id)
		delete(s.writes, id)
		delete(s.metadata, id)
		delete(s.lineageByCP, id)
	}
	delete(s.byLineage, lineageID)
	return nil
}

// Close releases resources held by the saver.
func (s *InMemoryCheckpointSaver) Close() error {
	return nil
}

// GuardedSkillRunner executes skills with guarded tools using graph workflows.
type GuardedSkillRunner struct {
	model           model.Model
	tools           map[string]tool.Tool
	guardedTools    map[string]string // tool name -> skill name
	checkpointSaver *InMemoryCheckpointSaver
	executionStore  *GuardedExecutionStore
	inFlightStore   *InFlightStore
	auditStore      *AuditStore
	notifyFn        func(sessionID string, resp *channel.Response) error
	logger          *slog.Logger
}

// NewGuardedSkillRunner creates a new runner.
func NewGuardedSkillRunner(
	m model.Model,
	tools []tool.Tool,
	guardedTools map[string]string,
	inFlight *InFlightStore,
	audit *AuditStore,
	logger *slog.Logger,
) *GuardedSkillRunner {
	toolMap := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		toolMap[t.Declaration().Name] = t
	}
	logger.Info("NewGuardedSkillRunner: created",
		"guardedTools", guardedTools,
		"totalTools", len(toolMap))
	return &GuardedSkillRunner{
		model:           m,
		tools:           toolMap,
		guardedTools:    guardedTools,
		checkpointSaver: NewInMemoryCheckpointSaver(),
		executionStore:  NewGuardedExecutionStore(logger),
		inFlightStore:   inFlight,
		auditStore:      audit,
		logger:          logger,
	}
}

// SetNotifyFunc sets the function to notify clients of approval requests.
func (r *GuardedSkillRunner) SetNotifyFunc(fn func(sessionID string, resp *channel.Response) error) {
	r.notifyFn = fn
}

// ExecutionStore returns the guarded execution store.
func (r *GuardedSkillRunner) ExecutionStore() *GuardedExecutionStore {
	return r.executionStore
}

// ErrApprovalPending is returned when a guarded tool requires human approval.
var ErrApprovalPending = fmt.Errorf("approval_pending")

// ApprovalPendingError contains details about a pending approval.
type ApprovalPendingError struct {
	ApprovalID string
	ToolName   string
	ToolArgs   string
	SkillName  string
	SessionID  string
	UserID     string
}

func (e *ApprovalPendingError) Error() string {
	return fmt.Sprintf("approval_pending: %s (tool: %s)", e.ApprovalID, e.ToolName)
}

// makeApprovalGateNode creates a graph node that handles approval gating using
// the proper graph.Interrupt pattern from trpc-agent-go.
//
// Flow:
//  1. First call: calls graph.Interrupt() which returns InterruptError, THEN
//     creates approval request, notifies client, stores execution
//  2. Handler catches interrupt, shows approval to user
//  3. User approves, handler resumes with resume_map containing decision AND approval_id
//  4. Resume call: graph.Interrupt() returns the resume value immediately
//  5. Node proceeds based on decision
//
// IMPORTANT: graph.Interrupt() must be called BEFORE any side effects (notification,
// storing execution) because on resume, the node function runs from the beginning
// again. By calling Interrupt first, we can detect if this is a first call (error)
// or a resume (returns value) and only do side effects on first call.
func (r *GuardedSkillRunner) makeApprovalGateNode(skillName string) func(ctx context.Context, state graph.State) (any, error) {
	return func(ctx context.Context, state graph.State) (any, error) {
		// Log all state keys to understand what we have
		stateKeys := make([]string, 0, len(state))
		for k := range state {
			stateKeys = append(stateKeys, k)
		}
		r.logger.Info("approval_gate: ENTERED", "skill", skillName, "state_keys", stateKeys)

		// Extract session info from context (for session/user IDs)
		var sessionID, userID string
		if inv, ok := agent.InvocationFromContext(ctx); ok {
			if inv.Session != nil {
				sessionID = inv.Session.ID
				userID = inv.Session.UserID
			}
		}

		// CRITICAL: Get invocationID from state's configurable, NOT from context.
		// The context may contain the parent agent's invocation, but we need the
		// lineage_id that we explicitly set in Run() to match checkpoint storage.
		// The executor uses InvocationID as lineage_id, so they must match.
		var invocationID string
		if configurable, ok := state["configurable"].(map[string]any); ok {
			if lid, ok := configurable["lineage_id"].(string); ok {
				invocationID = lid
			}
		}
		r.logger.Info("approval_gate: extracted invocationID from state", "invocation_id", invocationID)

		// Extract tool info from messages
		var toolName, toolArgs string
		messages, _ := graph.GetStateValue[[]model.Message](state, "messages")
		r.logger.Info("approval_gate: messages in state", "count", len(messages))
		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]
			for _, tc := range lastMsg.ToolCalls {
				if _, guarded := r.guardedTools[tc.Function.Name]; guarded {
					toolName = tc.Function.Name
					toolArgs = string(tc.Function.Arguments)
					break
				}
			}
		}
		r.logger.Info("approval_gate: extracted tool info", "tool", toolName, "args_len", len(toolArgs), "session", sessionID)

		// Create approval details for the interrupt prompt
		// Note: We generate a new approvalID here, but it's only used if this is the first call.
		// On resume, we'll get the original approvalID from the resume value.
		approvalID := uuid.New().String()
		desc := formatApprovalDescription(toolName, []byte(toolArgs))

		approvalDetails := map[string]any{
			"approval_id": approvalID,
			"tool_name":   toolName,
			"tool_args":   toolArgs,
			"skill_name":  skillName,
			"description": desc,
			"session_id":  sessionID,
			"user_id":     userID,
		}

		r.logger.Info("approval_gate: calling graph.Interrupt",
			"approval_id", approvalID,
			"tool", toolName,
			"skill", skillName)

		// Check if this is a RESUME by looking for the approval decision in context.
		// We store it in context in resumeExecution() because graph.Interrupt()
		// doesn't reliably return the resume value in trpc-agent-go.
		var resumeDecision map[string]any
		if decision := ctx.Value(approvalDecisionKey); decision != nil {
			if approval, ok := decision.(map[string]any); ok {
				resumeDecision = approval
				r.logger.Info("approval_gate: found resume decision in context",
					"approved", approval["approved"],
					"original_approval_id", approval["approval_id"])
			}
		}

		// If we have a resume decision, skip the interrupt and use it directly
		if resumeDecision != nil {
			r.logger.Info("approval_gate: using resume decision (skipping graph.Interrupt)",
				"approved", resumeDecision["approved"])

			approved, _ := resumeDecision["approved"].(bool)
			originalApprovalID, _ := resumeDecision["approval_id"].(string)

			// Clean up execution store using the original approvalID
			r.executionStore.Delete(originalApprovalID)

			// Record resolution in audit
			if r.auditStore != nil {
				resolution := "approved"
				if !approved {
					resolution = "rejected"
				}
				_ = r.auditStore.RecordResolution(originalApprovalID, resolution, userID, time.Now())
			}

			// Update InFlightStore
			if r.inFlightStore != nil {
				if approved {
					r.inFlightStore.Complete(originalApprovalID, "approved")
				} else {
					r.inFlightStore.Fail(originalApprovalID, "rejected")
				}
			}

			if approved {
				r.logger.Info("approval_gate: approved via resume, continuing to execute_tools",
					"original_approval_id", originalApprovalID,
					"tool", toolName)
				return map[string]any{"approval_granted": true}, nil
			}

			r.logger.Info("approval_gate: rejected via resume, stopping execution",
				"original_approval_id", originalApprovalID,
				"tool", toolName)
			return nil, fmt.Errorf("tool %s was rejected by user", toolName)
		}

		// No resume decision - this is a FIRST CALL
		// Call graph.Interrupt to trigger the checkpoint and pause
		result, err := graph.Interrupt(ctx, state, "approval", approvalDetails)

		if err != nil {
			// FIRST CALL - InterruptError means execution pauses here
			// NOW we do all the side effects: register, notify, store execution
			r.logger.Info("approval_gate: interrupt triggered (first call), registering approval",
				"approval_id", approvalID,
				"error_type", fmt.Sprintf("%T", err))

			// Register in InFlightStore for dashboard visibility
			if r.inFlightStore != nil {
				r.inFlightStore.RegisterApproval(approvalID, skillName, toolName, toolArgs, desc, sessionID, userID, 30*time.Minute)
			}

			// Record in audit log
			if r.auditStore != nil {
				_ = r.auditStore.RecordRequest(approvalID, toolName, skillName, toolArgs, desc, sessionID, userID, time.Now())
			}

			// Notify client
			if r.notifyFn != nil {
				r.logger.Info("notifying client of approval request", "approval_id", approvalID)
				_ = r.notifyFn(sessionID, &channel.Response{
					ID:        approvalID,
					SessionID: sessionID,
					Type:      "approval_required",
					Metadata:  approvalDetails,
				})
			}

			// Get the checkpoint ID that was just saved by Interrupt
			// This is critical for resuming since lineage_id isn't preserved by trpc-agent-go
			checkpointID := r.checkpointSaver.GetLastCheckpointID()
			r.logger.Info("approval_gate: captured checkpoint after interrupt",
				"approval_id", approvalID,
				"checkpoint_id", checkpointID)

			// Store execution info for resume handling
			// Create DecisionCh so the GuardedSkillAgent wrapper can wait on it
			// CRITICAL: Store InvocationID - the executor uses it as lineage_id for checkpoint tracking
			// and for resuming from the correct node position
			exec := &GuardedExecution{
				ID:           approvalID,
				SessionID:    sessionID,
				UserID:       userID,
				SkillName:    skillName,
				ToolName:     toolName,
				ToolArgs:     toolArgs,
				Description:  desc,
				CheckpointID: checkpointID,
				InvocationID: invocationID,
				CreatedAt:    time.Now(),
				DecisionCh:   make(chan bool, 1),
			}
			r.executionStore.Store(exec)
			r.logger.Info("approval_gate: stored execution with invocation ID",
				"approval_id", approvalID,
				"invocation_id", invocationID)

			return nil, err
		}

		// RESUME - graph.Interrupt() returned the resume value immediately
		// Extract the original approvalID from the resume value (passed in resumeExecution)
		var originalApprovalID string
		approved := false

		switch v := result.(type) {
		case bool:
			// Legacy format - just a boolean (shouldn't happen with new code)
			approved = v
			originalApprovalID = approvalID // fallback to the newly generated one
		case map[string]any:
			if a, ok := v["approved"].(bool); ok {
				approved = a
			}
			if id, ok := v["approval_id"].(string); ok {
				originalApprovalID = id
			} else {
				originalApprovalID = approvalID // fallback
			}
		}

		r.logger.Info("approval_gate: resumed with decision",
			"original_approval_id", originalApprovalID,
			"approved", approved,
			"result", result,
			"result_type", fmt.Sprintf("%T", result))

		// Clean up execution store using the ORIGINAL approvalID
		r.executionStore.Delete(originalApprovalID)

		// Record resolution in audit
		if r.auditStore != nil {
			resolution := "approved"
			if !approved {
				resolution = "rejected"
			}
			_ = r.auditStore.RecordResolution(originalApprovalID, resolution, userID, time.Now())
		}

		// Update InFlightStore
		if r.inFlightStore != nil {
			if approved {
				r.inFlightStore.Complete(originalApprovalID, "approved")
			} else {
				r.inFlightStore.Fail(originalApprovalID, "rejected")
			}
		}

		if approved {
			r.logger.Info("approval_gate: approved, continuing to execute_tools",
				"approval_id", originalApprovalID,
				"tool", toolName)
			return map[string]any{"approval_granted": true}, nil
		}

		// Rejected
		r.logger.Info("approval_gate: rejected, stopping execution",
			"approval_id", originalApprovalID,
			"tool", toolName)
		return nil, fmt.Errorf("tool %s was rejected by user", toolName)
	}
}

// buildSkillGraph creates a graph workflow for a skill with guarded tools.
// The graph structure is:
//
//	start → llm_plan → check_tools ─┬─► approval_gate (interrupt) → execute_tools → llm_continue
//	                                └─► execute_tools → llm_continue
func (r *GuardedSkillRunner) buildSkillGraph(skillName, instruction string, skillTools []tool.Tool) (*graph.Graph, error) {
	// Create state schema
	schema := graph.MessagesStateSchema()

	// Filter tools to only those allowed for this skill
	skillToolMap := make(map[string]tool.Tool, len(skillTools))
	for _, t := range skillTools {
		skillToolMap[t.Declaration().Name] = t
	}

	// Create graph
	g := graph.NewStateGraph(schema)

	// Node: LLM planning - generates tool calls
	g.AddNode("llm_plan", graph.NewLLMNodeFunc(
		r.model,
		instruction,
		skillToolMap,
	))

	// Node: Check if any tool calls require approval
	g.AddNode("check_tools", func(ctx context.Context, state graph.State) (any, error) {
		messages, _ := graph.GetStateValue[[]model.Message](state, "messages")
		r.logger.Info("check_tools: examining messages", "count", len(messages), "guardedTools", r.guardedTools)
		if len(messages) == 0 {
			r.logger.Info("check_tools: no messages, skipping approval")
			return map[string]any{"needs_approval": false}, nil
		}

		lastMsg := messages[len(messages)-1]
		r.logger.Info("check_tools: checking last message", "role", lastMsg.Role, "toolCalls", len(lastMsg.ToolCalls))
		for _, tc := range lastMsg.ToolCalls {
			r.logger.Info("check_tools: found tool call", "tool", tc.Function.Name)
			if _, guarded := r.guardedTools[tc.Function.Name]; guarded {
				r.logger.Info("check_tools: GUARDED tool detected, requiring approval", "tool", tc.Function.Name)
				// Store the tool call info for the approval gate
				return map[string]any{
					"needs_approval":    true,
					"pending_tool_call": tc,
					"pending_tool_name": tc.Function.Name,
					"pending_tool_args": string(tc.Function.Arguments),
				}, nil
			}
		}
		r.logger.Info("check_tools: no guarded tools found, proceeding")
		return map[string]any{"needs_approval": false}, nil
	})

	// Node: Approval gate - pauses execution for human approval
	// We use a closure to capture skillName for the approval request
	g.AddNode("approval_gate", r.makeApprovalGateNode(skillName))

	// Node: Execute tools - wraps graph.NewToolsNodeFunc with logging
	toolsNodeFn := graph.NewToolsNodeFunc(skillToolMap)
	g.AddNode("execute_tools", func(ctx context.Context, state graph.State) (any, error) {
		messages, _ := graph.GetStateValue[[]model.Message](state, "messages")
		r.logger.Info("execute_tools: ENTERED", "message_count", len(messages))

		// Log pending tool calls
		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]
			r.logger.Info("execute_tools: last message",
				"role", lastMsg.Role,
				"tool_calls", len(lastMsg.ToolCalls))
			for _, tc := range lastMsg.ToolCalls {
				r.logger.Info("execute_tools: will execute tool",
					"tool", tc.Function.Name,
					"args", string(tc.Function.Arguments))
			}
		}

		// Call the actual tools node
		result, err := toolsNodeFn(ctx, state)
		r.logger.Info("execute_tools: completed", "error", err)
		return result, err
	})

	// Node: Continue with LLM after tool execution
	g.AddNode("llm_continue", graph.NewLLMNodeFunc(
		r.model,
		instruction,
		skillToolMap,
	))

	// Edges
	g.SetEntryPoint("llm_plan")
	g.AddEdge("llm_plan", "check_tools")

	// Conditional: route based on whether approval is needed
	// Note: We check tool calls directly here because node return values
	// are not automatically merged into graph state in trpc-agent-go.
	g.AddConditionalEdges("check_tools", func(ctx context.Context, state graph.State) (string, error) {
		messages, _ := graph.GetStateValue[[]model.Message](state, "messages")
		r.logger.Info("check_tools ROUTING: evaluating", "messageCount", len(messages))

		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]
			for _, tc := range lastMsg.ToolCalls {
				if _, guarded := r.guardedTools[tc.Function.Name]; guarded {
					r.logger.Info("check_tools ROUTING: -> approval_gate (guarded tool detected)", "tool", tc.Function.Name)
					return "approval_gate", nil
				}
			}
		}

		r.logger.Info("check_tools ROUTING: -> execute_tools")
		return "execute_tools", nil
	}, map[string]string{
		"approval_gate": "approval_gate",
		"execute_tools": "execute_tools",
	})

	// After approval gate - always continue to execute_tools.
	// If the user rejected, approval_gate returns an error which stops execution before
	// reaching this routing. Node return values aren't merged into state in trpc-agent-go,
	// so we can't check "approval_granted" here.
	g.AddEdge("approval_gate", "execute_tools")

	// After tool execution, continue with LLM
	g.AddEdge("execute_tools", "llm_continue")

	// LLM continue can loop back or end
	g.AddConditionalEdges("llm_continue", func(ctx context.Context, state graph.State) (string, error) {
		messages, _ := graph.GetStateValue[[]model.Message](state, "messages")
		if len(messages) == 0 {
			return graph.End, nil
		}
		lastMsg := messages[len(messages)-1]
		if len(lastMsg.ToolCalls) > 0 {
			return "check_tools", nil // More tool calls
		}
		return graph.End, nil
	}, map[string]string{
		"check_tools": "check_tools",
		graph.End:     graph.End,
	})

	return g.Compile()
}

// RunResult contains the result of a skill execution including the graph and executor
// needed for resuming after an interrupt.
type RunResult struct {
	Events    <-chan *event.Event
	Graph     *graph.Graph
	Executor  *graph.Executor
	LineageID string // Needed for checkpoint restoration on resume
}

// Run executes a skill with guarded tools.
// Returns a RunResult containing the event channel, graph, and executor.
// The graph and executor are needed for resuming after an interrupt.
func (r *GuardedSkillRunner) Run(
	ctx context.Context,
	skillName string,
	instruction string,
	skillTools []tool.Tool,
	userMessage string,
	sessionID string,
	userID string,
) (*RunResult, error) {
	r.logger.Info("GuardedSkillRunner.Run: starting",
		"skill", skillName,
		"guardedTools", r.guardedTools,
		"skillToolsCount", len(skillTools),
		"message", userMessage)

	g, err := r.buildSkillGraph(skillName, instruction, skillTools)
	if err != nil {
		r.logger.Error("GuardedSkillRunner.Run: failed to build graph", "error", err)
		return nil, fmt.Errorf("build graph: %w", err)
	}
	r.logger.Info("GuardedSkillRunner.Run: graph built successfully")

	executor, err := graph.NewExecutor(g,
		graph.WithCheckpointSaver(r.checkpointSaver),
		graph.WithMaxSteps(50),
	)
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	// CRITICAL: Use the SAME ID for both lineage_id and InvocationID.
	// The executor uses InvocationID as lineage_id when looking up checkpoints.
	// If they differ, resume will fail to find checkpoints stored during initial execution.
	lineageID := uuid.New().String()

	// Initial state with user message
	initialState := graph.State{
		"messages": []model.Message{
			model.NewUserMessage(userMessage),
		},
		"configurable": map[string]any{
			"lineage_id": lineageID,
		},
	}

	invocation := &agent.Invocation{
		InvocationID: lineageID, // MUST match lineage_id for checkpoint lookup to work
		Session: &session.Session{
			ID:     sessionID,
			UserID: userID,
		},
	}

	// Execute the graph
	// The approval_gate node uses graph.Interrupt() which will cause the executor
	// to return an InterruptError when a guarded tool is detected.
	events, err := executor.Execute(ctx, initialState, invocation)

	// Always try to update pending executions with Executor and Graph.
	// The approval_gate node stores the execution but doesn't have access to these,
	// and we need them for Resume() to work. This handles both sync (error returned)
	// and async (events channel returned) cases.
	r.executionStore.mu.Lock()
	for _, exec := range r.executionStore.executions {
		if exec.SessionID == sessionID && exec.SkillName == skillName && exec.Executor == nil {
			exec.Graph = g
			exec.Executor = executor
			exec.LineageID = lineageID
			r.logger.Info("GuardedSkillRunner.Run: updated execution with executor",
				"approval_id", exec.ID,
				"session_id", sessionID,
				"skill", skillName,
				"lineage_id", lineageID)
		}
	}
	r.executionStore.mu.Unlock()

	if err != nil {
		r.logger.Info("GuardedSkillRunner.Run: executor.Execute returned error",
			"error", err,
			"error_type", fmt.Sprintf("%T", err))

		// Check if this is an interrupt error (approval pending)
		if graph.IsInterruptError(err) {
			r.logger.Info("GuardedSkillRunner.Run: interrupt detected, execution paused for approval")

			// Get the interrupt details from the error
			interruptErr, _ := graph.GetInterruptError(err)
			if interruptErr != nil {
				r.logger.Info("GuardedSkillRunner.Run: interrupt value",
					"value", interruptErr.Value,
					"value_type", fmt.Sprintf("%T", interruptErr.Value))

				// Extract approval ID from the interrupt value to update execution with lineage info
				if details, ok := interruptErr.Value.(map[string]any); ok {
					if approvalID, ok := details["approval_id"].(string); ok {
						// Update the stored execution with lineage info for resume
						if exec, ok := r.executionStore.Get(approvalID); ok {
							exec.LineageID = lineageID
							exec.Graph = g
							exec.Executor = executor
							r.executionStore.Store(exec)
							r.logger.Info("GuardedSkillRunner.Run: updated execution with lineage info",
								"approval_id", approvalID,
								"lineage_id", lineageID)
						}
					}
				}
			}

			// Return an empty closed channel to signal "waiting for approval"
			// The actual resumption will happen via Resume() when approval is granted
			outChan := make(chan *event.Event)
			close(outChan)
			return &RunResult{Events: outChan, Graph: g, Executor: executor, LineageID: lineageID}, nil
		}

		return nil, fmt.Errorf("execute graph: %w", err)
	}

	r.logger.Info("GuardedSkillRunner.Run: executor started, returning event channel")
	return &RunResult{Events: events, Graph: g, Executor: executor, LineageID: lineageID}, nil
}

// handleInterruptEvent processes an interrupt event and creates an approval request.
func (r *GuardedSkillRunner) handleInterruptEvent(
	ctx context.Context,
	executor *graph.Executor,
	g *graph.Graph,
	lineageID string,
	interruptValue map[string]any,
	sessionID string,
	userID string,
	skillName string,
) {
	approvalID := uuid.New().String()

	toolName, _ := interruptValue["tool_name"].(string)
	toolArgs, _ := interruptValue["tool_args"].(string)

	desc := formatApprovalDescription(toolName, []byte(toolArgs))

	// Store the execution for resumption
	exec := &GuardedExecution{
		ID:          approvalID,
		SessionID:   sessionID,
		UserID:      userID,
		SkillName:   skillName,
		ToolName:    toolName,
		ToolArgs:    toolArgs,
		Description: desc,
		LineageID:   lineageID,
		Graph:       g,
		Executor:    executor,
		CreatedAt:   time.Now(),
	}
	r.executionStore.Store(exec)

	// Register in InFlightStore for dashboard visibility
	if r.inFlightStore != nil {
		r.inFlightStore.RegisterApproval(approvalID, skillName, toolName, toolArgs, desc, sessionID, userID, 30*time.Minute)
	}

	// Record in audit log
	if r.auditStore != nil {
		_ = r.auditStore.RecordRequest(approvalID, toolName, skillName, toolArgs, desc, sessionID, userID, time.Now())
	}

	// Notify client
	if r.notifyFn != nil {
		_ = r.notifyFn(sessionID, &channel.Response{
			ID:        approvalID,
			SessionID: sessionID,
			Type:      "approval_required",
			Metadata: map[string]any{
				"approval_id": approvalID,
				"tool_name":   toolName,
				"skill_name":  skillName,
				"description": desc,
				"arguments":   toolArgs,
			},
		})
	}

	r.logger.Info("approval request created",
		"approval_id", approvalID,
		"tool", toolName,
		"skill", skillName,
		"session_id", sessionID)
}

// Resume continues a paused execution after approval/rejection.
// This is called from external handlers (e.g., when signaling via DecisionCh isn't available).
func (r *GuardedSkillRunner) Resume(ctx context.Context, approvalID string, approved bool) (<-chan *event.Event, error) {
	exec, ok := r.executionStore.Get(approvalID)
	if !ok {
		return nil, fmt.Errorf("execution not found: %s", approvalID)
	}
	return r.resumeExecution(ctx, exec, approved)
}

// resumeExecution performs the actual graph resume using the given execution.
func (r *GuardedSkillRunner) resumeExecution(ctx context.Context, exec *GuardedExecution, approved bool) (<-chan *event.Event, error) {
	r.logger.Info("resumeExecution: starting",
		"approval_id", exec.ID,
		"approved", approved,
		"checkpoint_id", exec.CheckpointID,
		"lineage_id", exec.LineageID,
		"has_executor", exec.Executor != nil,
		"has_graph", exec.Graph != nil)

	// Safety checks
	if exec.Graph == nil {
		return nil, fmt.Errorf("cannot resume execution %s: Graph is nil", exec.ID)
	}
	if exec.Executor == nil {
		return nil, fmt.Errorf("cannot resume execution %s: Executor is nil", exec.ID)
	}
	if exec.CheckpointID == "" {
		return nil, fmt.Errorf("cannot resume execution %s: CheckpointID is empty (checkpoint cannot be restored)", exec.ID)
	}
	if exec.InvocationID == "" {
		return nil, fmt.Errorf("cannot resume execution %s: InvocationID is empty (cannot resume from correct node)", exec.ID)
	}

	// Verify the checkpoint exists by looking it up directly by ID
	cp := r.checkpointSaver.GetByID(exec.CheckpointID)
	r.logger.Info("resumeExecution: checkpoint lookup by ID",
		"checkpoint_id", exec.CheckpointID,
		"checkpoint_found", cp != nil)
	if cp == nil {
		return nil, fmt.Errorf("cannot resume execution %s: checkpoint %s not found", exec.ID, exec.CheckpointID)
	}

	// CRITICAL: Use the SAME executor from the original execution.
	// Creating a new executor doesn't work because the executor maintains internal
	// state about which interrupts have been triggered. The trpc-agent-go graph.Interrupt()
	// only returns the resume value (instead of error) when called on the same executor
	// that originally triggered the interrupt.
	executor := exec.Executor

	// Create resume command with approval decision AND the original approval_id
	// The key must match what approval_gate uses in graph.Interrupt(ctx, state, "approval", ...)
	// We pass approval_id so the approval_gate node can clean up the correct execution on resume
	resumeCmd := graph.NewResumeCommand().WithResumeMap(map[string]any{
		"approval": map[string]any{
			"approved":    approved,
			"approval_id": exec.ID, // Pass original approval ID for cleanup
		},
	})

	// CRITICAL: The executor does NOT automatically load checkpoint state from checkpoint_id.
	// We must manually merge the checkpoint's ChannelValues into the resume state.
	// This restores messages, tool calls, and other state from before the interrupt.
	resumeState := graph.State{
		graph.StateKeyCommand: resumeCmd,
	}

	// Merge checkpoint's channel values into resume state
	if cp.ChannelValues != nil {
		for k, v := range cp.ChannelValues {
			resumeState[k] = v
		}
		r.logger.Info("resumeExecution: merged checkpoint state",
			"checkpoint_id", exec.CheckpointID,
			"channel_keys", len(cp.ChannelValues))
	}

	// CRITICAL: Store the approval decision in configurable so approval_gate can find it.
	// We can't rely on graph.Interrupt() returning the resume value, so we pass it through state.
	configurable, ok := resumeState["configurable"].(map[string]any)
	if !ok {
		configurable = make(map[string]any)
	}
	// Make a copy to avoid modifying the checkpoint's configurable
	newConfigurable := make(map[string]any)
	for k, v := range configurable {
		newConfigurable[k] = v
	}
	newConfigurable["__approval_decision__"] = map[string]any{
		"approved":    approved,
		"approval_id": exec.ID,
	}
	resumeState["configurable"] = newConfigurable
	r.logger.Info("resumeExecution: stored approval decision in configurable",
		"approved", approved,
		"approval_id", exec.ID)

	r.logger.Info("resumeExecution: created resume state",
		"state_keys", getStateKeys(resumeState),
		"checkpoint_id", exec.CheckpointID,
		"invocation_id", exec.InvocationID)

	// CRITICAL FIX: Use the SAME InvocationID as the original execution.
	// The executor uses InvocationID as lineage_id for checkpoint tracking.
	// Using a different InvocationID makes the executor start from the entry point
	// instead of resuming from the interrupted node.
	invocation := &agent.Invocation{
		InvocationID: exec.InvocationID,
		Session: &session.Session{
			ID:     exec.SessionID,
			UserID: exec.UserID,
		},
	}

	// CRITICAL: Store the approval decision in context for approval_gate to find.
	// We can't rely on graph.Interrupt() returning the resume value, and the executor
	// loads checkpoint state which overwrites any state modifications we make.
	// Context is the only reliable way to pass the decision to the node.
	ctx = context.WithValue(ctx, approvalDecisionKey, map[string]any{
		"approved":    approved,
		"approval_id": exec.ID,
	})
	r.logger.Info("resumeExecution: stored approval decision in context",
		"approved", approved,
		"approval_id", exec.ID)

	events, err := executor.Execute(ctx, resumeState, invocation)
	if err != nil {
		return nil, fmt.Errorf("resume execution: %w", err)
	}

	r.logger.Info("execution resumed",
		"approval_id", exec.ID,
		"approved", approved,
		"tool", exec.ToolName)

	return events, nil
}

// GuardedSkillAgent wraps a GuardedSkillRunner to implement the agent.Agent interface.
// This allows skills with guarded tools to be used as sub-agents in the existing
// Team/dispatch architecture while using proper graph-based pause/resume semantics.
type GuardedSkillAgent struct {
	name        string
	description string
	instruction string
	tools       []tool.Tool
	runner      *GuardedSkillRunner
	logger      *slog.Logger
}

// NewGuardedSkillAgent creates an agent that uses graph-based execution for skills
// with guarded tools. This replaces the broken BeforeTool-callback approach.
func NewGuardedSkillAgent(
	name string,
	description string,
	instruction string,
	tools []tool.Tool,
	runner *GuardedSkillRunner,
	logger *slog.Logger,
) *GuardedSkillAgent {
	return &GuardedSkillAgent{
		name:        name,
		description: description,
		instruction: instruction,
		tools:       tools,
		runner:      runner,
		logger:      logger,
	}
}

// Run executes the skill using the graph-based runner.
// This wrapper keeps the output channel open when an interrupt (approval request)
// occurs, waits for the approval decision, then resumes and forwards events.
// From the coordinator's perspective, this call stays "in progress" until approval is resolved.
func (a *GuardedSkillAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Extract message from invocation
	message := ""
	if invocation.Message.Content != "" {
		message = invocation.Message.Content
	}

	sessionID := ""
	userID := ""
	if invocation.Session != nil {
		sessionID = invocation.Session.ID
		userID = invocation.Session.UserID
	}

	a.logger.Info("GuardedSkillAgent.Run starting",
		"skill", a.name,
		"session_id", sessionID,
		"message_len", len(message))

	// Start execution
	result, err := a.runner.Run(ctx, a.name, a.instruction, a.tools, message, sessionID, userID)
	if err != nil {
		return nil, err
	}

	// Create output channel that we control
	outChan := make(chan *event.Event, 100)

	go func() {
		defer close(outChan)

		// Forward initial events
		for evt := range result.Events {
			if evt != nil {
				outChan <- evt
			}
		}

		// Check if there's a pending approval for this session
		// by searching the execution store for matching session ID
		var pendingExec *GuardedExecution
		a.runner.executionStore.mu.RLock()
		for _, exec := range a.runner.executionStore.executions {
			if exec.SessionID == sessionID && exec.SkillName == a.name {
				pendingExec = exec
				break
			}
		}
		a.runner.executionStore.mu.RUnlock()

		if pendingExec == nil {
			// No pending approval - execution completed normally
			a.logger.Info("GuardedSkillAgent.Run: no pending approval, completed normally",
				"skill", a.name,
				"session_id", sessionID)
			return
		}

		// IMPORTANT: Update the pending execution with Graph, Executor, and LineageID from the run result.
		// The approval gate node stores the execution before these are available,
		// so we must set them here before Resume can be called.
		// LineageID is critical for checkpoint restoration (which contains the messages).
		if pendingExec.Executor == nil && result.Executor != nil {
			a.runner.executionStore.mu.Lock()
			pendingExec.Graph = result.Graph
			pendingExec.Executor = result.Executor
			pendingExec.LineageID = result.LineageID
			a.runner.executionStore.mu.Unlock()
			a.logger.Info("GuardedSkillAgent.Run: updated pending execution with Graph/Executor/LineageID",
				"approval_id", pendingExec.ID,
				"session_id", sessionID,
				"lineage_id", result.LineageID)
		}

		a.logger.Info("GuardedSkillAgent.Run: found pending approval, waiting for decision",
			"skill", a.name,
			"approval_id", pendingExec.ID,
			"session_id", sessionID)

		// Wait for approval decision
		select {
		case approved := <-pendingExec.DecisionCh:
			a.logger.Info("GuardedSkillAgent.Run: received approval decision",
				"approval_id", pendingExec.ID,
				"approved", approved)

			// Resume execution
			resumeEvents, err := a.runner.Resume(ctx, pendingExec.ID, approved)
			if err != nil {
				a.logger.Error("GuardedSkillAgent.Run: resume failed",
					"approval_id", pendingExec.ID,
					"error", err)
				// Send error event
				resp := &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: fmt.Sprintf("Error resuming after approval: %v", err),
							},
						},
					},
				}
				outChan <- event.NewResponseEvent(pendingExec.ID, a.name, resp)
				return
			}

			// Forward resumed events
			for evt := range resumeEvents {
				if evt != nil {
					outChan <- evt
				}
			}

			a.logger.Info("GuardedSkillAgent.Run: resume completed",
				"approval_id", pendingExec.ID,
				"skill", a.name)

		case <-time.After(30 * time.Minute):
			a.logger.Warn("GuardedSkillAgent.Run: approval timeout",
				"approval_id", pendingExec.ID,
				"skill", a.name)

			// Clean up
			a.runner.executionStore.Delete(pendingExec.ID)
			if a.runner.inFlightStore != nil {
				a.runner.inFlightStore.Fail(pendingExec.ID, "timeout")
			}

			// Send timeout message
			resp := &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: fmt.Sprintf("Approval timeout for tool %s after 30 minutes.", pendingExec.ToolName),
						},
					},
				},
			}
			outChan <- event.NewResponseEvent(pendingExec.ID, a.name, resp)

		case <-ctx.Done():
			a.logger.Info("GuardedSkillAgent.Run: context cancelled",
				"approval_id", pendingExec.ID,
				"skill", a.name)
			return
		}
	}()

	return outChan, nil
}

// Tools returns the tools available to this agent.
func (a *GuardedSkillAgent) Tools() []tool.Tool {
	return a.tools
}

// Info returns the agent's basic information.
func (a *GuardedSkillAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// SubAgents returns an empty slice - guarded skill agents don't have sub-agents.
func (a *GuardedSkillAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent returns nil - guarded skill agents don't have sub-agents.
func (a *GuardedSkillAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

// getStateKeys returns the keys present in a graph.State for debugging.
func getStateKeys(state graph.State) []string {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	return keys
}

// getParentCheckpointID extracts checkpoint_id from config, checking both top level
// and under configurable. trpc-agent-go puts checkpoint_id at the top level after
// the first checkpoint save, not under configurable.
func getParentCheckpointID(config map[string]any) string {
	// First check top level (where trpc-agent-go puts it after first save)
	if id, ok := config["checkpoint_id"].(string); ok && id != "" {
		return id
	}
	// Fall back to configurable (standard location)
	return graph.GetCheckpointID(config)
}
