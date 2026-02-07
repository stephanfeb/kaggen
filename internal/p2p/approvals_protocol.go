package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/yourusername/kaggen/internal/agent"
)

// ApprovalInjector is the interface for injecting approval completions.
type ApprovalInjector interface {
	InjectCompletion(ctx context.Context, sessionID, userID, taskID, agentName, result string) error
}

// ApprovalsProtocol handles the /kaggen/approvals/1.0.0 protocol.
type ApprovalsProtocol struct {
	*APIHandler
	inFlightStore *agent.InFlightStore
	guardedRunner *agent.GuardedSkillRunner
	injector      ApprovalInjector
}

// NewApprovalsProtocol creates a new approvals protocol handler.
func NewApprovalsProtocol(
	store *agent.InFlightStore,
	guardedRunner *agent.GuardedSkillRunner,
	injector ApprovalInjector,
	logger *slog.Logger,
) *ApprovalsProtocol {
	h := &ApprovalsProtocol{
		APIHandler:    NewAPIHandler(ApprovalsProtocolID, logger),
		inFlightStore: store,
		guardedRunner: guardedRunner,
		injector:      injector,
	}

	h.RegisterMethod("list", h.list)
	h.RegisterMethod("approve", h.approve)
	h.RegisterMethod("reject", h.reject)

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *ApprovalsProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

type approvalOut struct {
	ID          string `json:"id"`
	ToolName    string `json:"tool_name"`
	SkillName   string `json:"skill_name"`
	Description string `json:"description"`
	Arguments   string `json:"arguments"`
	SessionID   string `json:"session_id"`
	UserID      string `json:"user_id"`
	RequestedAt string `json:"requested_at"`
	TimeoutAt   string `json:"timeout_at"`
}

func (p *ApprovalsProtocol) list(params json.RawMessage) (any, error) {
	all := p.inFlightStore.List(agent.TaskPendingApproval)

	out := make([]approvalOut, 0, len(all))
	for _, t := range all {
		if t.ApprovalRequest == nil {
			continue
		}
		out = append(out, approvalOut{
			ID:          t.ID,
			ToolName:    t.ApprovalRequest.ToolName,
			SkillName:   t.ApprovalRequest.SkillName,
			Description: t.ApprovalRequest.Description,
			Arguments:   t.ApprovalRequest.Arguments,
			SessionID:   t.SessionID,
			UserID:      t.UserID,
			RequestedAt: t.ApprovalRequest.RequestedAt.Format(time.RFC3339),
			TimeoutAt:   t.TimeoutAt.Format(time.RFC3339),
		})
	}

	return map[string]any{"approvals": out}, nil
}

type approvalActionParams struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

func (p *ApprovalsProtocol) approve(params json.RawMessage) (any, error) {
	return p.handleAction(params, true)
}

func (p *ApprovalsProtocol) reject(params json.RawMessage) (any, error) {
	return p.handleAction(params, false)
}

func (p *ApprovalsProtocol) handleAction(params json.RawMessage, isApprove bool) (any, error) {
	var args approvalActionParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.ID == "" {
		return nil, fmt.Errorf("id is required")
	}

	task, ok := p.inFlightStore.Get(args.ID)
	if !ok || task.Status != agent.TaskPendingApproval {
		return nil, fmt.Errorf("approval not found or already resolved")
	}

	var action, result string
	if isApprove {
		action = "approved"
		p.inFlightStore.Complete(args.ID, "approved")
		result = fmt.Sprintf("Tool %s was APPROVED by user. You may now retry the action.", task.ApprovalRequest.ToolName)
	} else {
		action = "rejected"
		reason := args.Reason
		if reason == "" {
			reason = "rejected by user"
		}
		p.inFlightStore.Fail(args.ID, reason)
		result = fmt.Sprintf("Tool %s was REJECTED by user. Reason: %s. Find an alternative approach.", task.ApprovalRequest.ToolName, reason)
	}

	// Try graph-based SignalDecision first
	if p.guardedRunner != nil {
		if signaled := p.guardedRunner.ExecutionStore().SignalDecision(args.ID, isApprove); signaled {
			return map[string]any{"success": true, "status": action, "id": args.ID}, nil
		}
	}

	// Fall back to legacy InjectCompletion
	if p.injector != nil {
		_ = p.injector.InjectCompletion(context.Background(), task.SessionID, task.UserID, args.ID, task.AgentName, result)
	}

	return map[string]any{"success": true, "status": action, "id": args.ID}, nil
}
