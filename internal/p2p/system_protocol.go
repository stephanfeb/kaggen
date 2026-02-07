package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/backlog"
	"github.com/yourusername/kaggen/internal/config"
)

// SystemProtocol handles the /kaggen/system/1.0.0 protocol.
type SystemProtocol struct {
	*APIHandler
	config        *config.Config
	agentProvider *agent.AgentProvider
	backlogStore  *backlog.Store
	startTime     time.Time
	clientCount   func() int
}

// NewSystemProtocol creates a new system protocol handler.
func NewSystemProtocol(
	cfg *config.Config,
	provider *agent.AgentProvider,
	store *backlog.Store,
	startTime time.Time,
	clientCount func() int,
	logger *slog.Logger,
) *SystemProtocol {
	h := &SystemProtocol{
		APIHandler:    NewAPIHandler(SystemProtocolID, logger),
		config:        cfg,
		agentProvider: provider,
		backlogStore:  store,
		startTime:     startTime,
		clientCount:   clientCount,
	}

	h.RegisterMethod("overview", h.overview)
	h.RegisterMethod("config", h.getConfig)
	h.RegisterMethod("settings", h.settings)
	h.RegisterMethod("skills", h.skills)
	h.RegisterMethod("backlog", h.listBacklog)
	h.RegisterMethod("plan", h.getPlan)
	// logs method would require streaming, handled separately

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *SystemProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

func (p *SystemProtocol) overview(params json.RawMessage) (any, error) {
	uptime := time.Since(p.startTime)

	store := p.agentProvider.InFlightStore()
	allTasks := store.List("")
	running, completed, failed, cancelled := 0, 0, 0, 0
	var totalTokens agent.TokenUsage
	for _, t := range allTasks {
		switch t.Status {
		case agent.TaskRunning:
			running++
		case agent.TaskCompleted:
			completed++
		case agent.TaskFailed:
			failed++
		case agent.TaskCancelled:
			cancelled++
		}
		if t.TotalTokens != nil {
			totalTokens.Input += t.TotalTokens.Input
			totalTokens.Output += t.TotalTokens.Output
			totalTokens.Total += t.TotalTokens.Total
		}
	}

	var backlogPending int
	if p.backlogStore != nil {
		items, err := p.backlogStore.List(backlog.Filter{Status: "pending", Limit: 500})
		if err == nil {
			backlogPending = len(items)
		}
	}

	skillCount := len(p.agentProvider.SubAgents())

	clients := 0
	if p.clientCount != nil {
		clients = p.clientCount()
	}

	return map[string]any{
		"status":              "healthy",
		"uptime_seconds":      int64(uptime.Seconds()),
		"uptime":              formatDuration(uptime),
		"model":               p.config.Agent.Model,
		"connected_clients":   clients,
		"inflight_tasks":      running,
		"total_tasks":         len(allTasks),
		"tasks_completed":     completed,
		"tasks_failed":        failed,
		"tasks_cancelled":     cancelled,
		"backlog_pending":     backlogPending,
		"skills_loaded":       skillCount,
		"memory_enabled":      p.config.Memory.Search.Enabled,
		"telegram_enabled":    p.config.Channels.Telegram.Enabled,
		"total_tokens_input":  totalTokens.Input,
		"total_tokens_output": totalTokens.Output,
		"total_tokens":        totalTokens.Total,
	}, nil
}

func (p *SystemProtocol) getConfig(params json.RawMessage) (any, error) {
	data, _ := json.Marshal(p.config)
	var m map[string]any
	json.Unmarshal(data, &m)

	// Redact sensitive fields
	redactNestedKey(m, "channels", "telegram", "bot_token")
	redactNestedKey(m, "session", "redis", "password")
	redactNestedKey(m, "session", "postgres", "password")
	redactNestedKey(m, "proactive", "webhooks")

	return map[string]any{"config": m}, nil
}

func (p *SystemProtocol) settings(params json.RawMessage) (any, error) {
	return map[string]any{
		"auth_enabled":    p.config.Security.Auth.Enabled,
		"gateway_bind":    p.config.Gateway.Bind,
		"gateway_port":    p.config.Gateway.Port,
		"allowed_origins": p.config.Gateway.GetAllowedOrigins(),
		"sandbox_enabled": p.config.Security.CommandSandbox.Enabled,
		"p2p": map[string]any{
			"enabled": p.config.P2P.Enabled,
			"port":    p.config.P2P.Port,
			"topics":  p.config.P2P.Topics,
		},
	}, nil
}

func (p *SystemProtocol) skills(params json.RawMessage) (any, error) {
	subAgents := p.agentProvider.SubAgents()

	type skillInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	skills := make([]skillInfo, 0, len(subAgents))
	for _, sa := range subAgents {
		info := sa.Info()
		skills = append(skills, skillInfo{
			Name:        info.Name,
			Description: info.Description,
		})
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return map[string]any{"skills": skills}, nil
}

type backlogParams struct {
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	All      bool   `json:"all,omitempty"`
}

func (p *SystemProtocol) listBacklog(params json.RawMessage) (any, error) {
	if p.backlogStore == nil {
		return map[string]any{"items": []any{}}, nil
	}

	var args backlogParams
	if len(params) > 0 {
		json.Unmarshal(params, &args)
	}

	f := backlog.Filter{
		Status:   args.Status,
		Priority: args.Priority,
		ParentID: args.ParentID,
		Limit:    200,
	}
	if f.ParentID == "" && !args.All {
		f.TopLevel = true
	}

	items, err := p.backlogStore.List(f)
	if err != nil {
		return nil, fmt.Errorf("failed to list backlog: %w", err)
	}
	if items == nil {
		items = []*backlog.Item{}
	}

	return map[string]any{"items": items}, nil
}

type planParams struct {
	ID string `json:"id"`
}

func (p *SystemProtocol) getPlan(params json.RawMessage) (any, error) {
	if p.backlogStore == nil {
		return nil, fmt.Errorf("backlog not configured")
	}

	var args planParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.ID == "" {
		return nil, fmt.Errorf("id is required")
	}

	parent, children, err := p.backlogStore.GetWithChildren(args.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}
	if children == nil {
		children = []*backlog.Item{}
	}

	return map[string]any{
		"plan":     parent,
		"subtasks": children,
	}, nil
}

// --- helpers ---

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	parts = append(parts, fmt.Sprintf("%dm", mins))
	return strings.Join(parts, " ")
}

func redactNestedKey(m map[string]any, keys ...string) {
	if len(keys) == 0 {
		return
	}
	if len(keys) == 1 {
		if _, ok := m[keys[0]]; ok {
			m[keys[0]] = "***"
		}
		return
	}
	if nested, ok := m[keys[0]].(map[string]any); ok {
		redactNestedKey(nested, keys[1:]...)
	}
}
