package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/proactive"
)

// CronToolSet holds CRUD tools for managing proactive schedules.
type CronToolSet struct {
	cfg    *config.Config
	engine *proactive.Engine
	mu     sync.Mutex
}

// NewCronToolSet creates the toolset and returns both the toolset (for wiring
// the engine later) and the tool slice (for agent registration).
func NewCronToolSet(cfg *config.Config) (*CronToolSet, []tool.Tool) {
	ts := &CronToolSet{cfg: cfg}
	tools := []tool.Tool{
		ts.listTool(),
		ts.addTool(),
		ts.updateTool(),
		ts.deleteTool(),
	}
	return ts, tools
}

// SetEngine wires the live proactive engine for runtime reload.
// When nil (e.g. CLI mode), mutations persist to config but don't take effect until restart.
func (ts *CronToolSet) SetEngine(e *proactive.Engine) {
	ts.mu.Lock()
	ts.engine = e
	ts.mu.Unlock()
}

func (ts *CronToolSet) reload() error {
	ts.mu.Lock()
	engine := ts.engine
	ts.mu.Unlock()
	if engine != nil {
		return engine.Reload(&ts.cfg.Proactive)
	}
	return nil
}

// --- cron_schedules (list) ---

type scheduleEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`              // "cron", "webhook", "heartbeat"
	Schedule string `json:"schedule,omitempty"` // crontab expression (cron jobs)
	Interval string `json:"interval,omitempty"` // Go duration (heartbeats)
	Path     string `json:"path,omitempty"`     // HTTP path (webhooks)
	Prompt   string `json:"prompt"`
	Channel  string `json:"channel"`
}

type schedulesResponse struct {
	Schedules []scheduleEntry `json:"schedules"`
	Count     int             `json:"count"`
}

func (ts *CronToolSet) listTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (*schedulesResponse, error) {
			return ts.list(), nil
		},
		function.WithName("cron_schedules"),
		function.WithDescription("List all configured proactive schedules (cron jobs, webhooks, heartbeats) and their settings."),
	)
}

func (ts *CronToolSet) list() *schedulesResponse {
	cfg := &ts.cfg.Proactive
	var entries []scheduleEntry

	for _, j := range cfg.Jobs {
		entries = append(entries, scheduleEntry{
			Name: j.Name, Type: "cron", Schedule: j.Schedule,
			Prompt: j.Prompt, Channel: j.Channel,
		})
	}
	for _, w := range cfg.Webhooks {
		entries = append(entries, scheduleEntry{
			Name: w.Name, Type: "webhook", Path: w.Path,
			Prompt: w.Prompt, Channel: w.Channel,
		})
	}
	for _, h := range cfg.Heartbeats {
		entries = append(entries, scheduleEntry{
			Name: h.Name, Type: "heartbeat", Interval: h.Interval,
			Prompt: h.Prompt, Channel: h.Channel,
		})
	}

	return &schedulesResponse{Schedules: entries, Count: len(entries)}
}

// --- cron_add ---

type cronAddRequest struct {
	Type       string         `json:"type" jsonschema:"required,enum=cron|webhook|heartbeat,description=Schedule type: cron or webhook or heartbeat"`
	Name       string         `json:"name" jsonschema:"required,description=Unique name for this schedule"`
	Schedule   string         `json:"schedule,omitempty" jsonschema:"description=Crontab expression (required for type=cron). Examples: @daily or @every 1h or 0 9 * * 1-5"`
	Interval   string         `json:"interval,omitempty" jsonschema:"description=Go duration (required for type=heartbeat). Examples: 5m or 1h"`
	Path       string         `json:"path,omitempty" jsonschema:"description=HTTP path (required for type=webhook). Example: /hooks/github"`
	Prompt     string         `json:"prompt" jsonschema:"required,description=The prompt sent to the agent when triggered"`
	Channel    string         `json:"channel" jsonschema:"required,description=Delivery channel: telegram or websocket"`
	UserID     string         `json:"user_id" jsonschema:"required,description=User ID for the job"`
	SessionID  string         `json:"session_id,omitempty" jsonschema:"description=Session ID (defaults to proactive-{name})"`
	Metadata   map[string]any `json:"metadata,omitempty" jsonschema:"description=Channel metadata (e.g. chat_id for telegram)"`
	Timeout    string         `json:"timeout,omitempty" jsonschema:"description=Job timeout as Go duration (default 5m for cron/webhook or 2m for heartbeat)"`
	MaxRetries int            `json:"max_retries,omitempty" jsonschema:"description=Max retry attempts on failure (default 0)"`
	Secret     string         `json:"secret,omitempty" jsonschema:"description=HMAC-SHA256 secret for webhook signature verification"`
}

type cronMutationResponse struct {
	Message string `json:"message"`
}

func (ts *CronToolSet) addTool() tool.Tool {
	return function.NewFunctionTool(
		ts.add,
		function.WithName("cron_add"),
		function.WithDescription("Add a new proactive schedule (cron job, webhook, or heartbeat). Persisted to config and applied immediately."),
	)
}

func (ts *CronToolSet) add(_ context.Context, req cronAddRequest) (*cronMutationResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if ts.nameExists(req.Name) {
		return nil, fmt.Errorf("schedule %q already exists", req.Name)
	}

	cfg := &ts.cfg.Proactive

	switch req.Type {
	case "cron":
		if req.Schedule == "" {
			return nil, fmt.Errorf("schedule is required for type=cron")
		}
		if _, err := cron.ParseStandard(req.Schedule); err != nil {
			// Also try robfig extended syntax (@every, @daily, etc.)
			parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
			if _, err2 := parser.Parse(req.Schedule); err2 != nil {
				return nil, fmt.Errorf("invalid cron schedule %q: %w", req.Schedule, err)
			}
		}
		cfg.Jobs = append(cfg.Jobs, config.CronJobConfig{
			Name: req.Name, Schedule: req.Schedule, Prompt: req.Prompt,
			Channel: req.Channel, UserID: req.UserID, SessionID: req.SessionID,
			Metadata: req.Metadata, Timeout: req.Timeout, MaxRetries: req.MaxRetries,
		})

	case "webhook":
		if req.Path == "" {
			return nil, fmt.Errorf("path is required for type=webhook")
		}
		cfg.Webhooks = append(cfg.Webhooks, config.WebhookConfig{
			Name: req.Name, Path: req.Path, Prompt: req.Prompt,
			Channel: req.Channel, UserID: req.UserID, SessionID: req.SessionID,
			Metadata: req.Metadata, Timeout: req.Timeout, MaxRetries: req.MaxRetries,
			Secret: req.Secret,
		})

	case "heartbeat":
		if req.Interval == "" {
			return nil, fmt.Errorf("interval is required for type=heartbeat")
		}
		if _, err := time.ParseDuration(req.Interval); err != nil {
			return nil, fmt.Errorf("invalid interval %q: %w", req.Interval, err)
		}
		cfg.Heartbeats = append(cfg.Heartbeats, config.HeartbeatConfig{
			Name: req.Name, Interval: req.Interval, Prompt: req.Prompt,
			Channel: req.Channel, UserID: req.UserID, SessionID: req.SessionID,
			Metadata: req.Metadata, Timeout: req.Timeout, MaxRetries: req.MaxRetries,
		})

	default:
		return nil, fmt.Errorf("type must be cron, webhook, or heartbeat")
	}

	if err := ts.cfg.Save(); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}
	if err := ts.reload(); err != nil {
		return nil, fmt.Errorf("reload engine: %w", err)
	}

	return &cronMutationResponse{Message: fmt.Sprintf("Added %s schedule %q", req.Type, req.Name)}, nil
}

// --- cron_update ---

type cronUpdateRequest struct {
	Name       string         `json:"name" jsonschema:"required,description=Name of the schedule to update"`
	Schedule   string         `json:"schedule,omitempty" jsonschema:"description=New crontab expression (cron jobs only)"`
	Interval   string         `json:"interval,omitempty" jsonschema:"description=New interval (heartbeats only)"`
	Path       string         `json:"path,omitempty" jsonschema:"description=New HTTP path (webhooks only)"`
	Prompt     string         `json:"prompt,omitempty" jsonschema:"description=New prompt"`
	Channel    string         `json:"channel,omitempty" jsonschema:"description=New channel"`
	UserID     string         `json:"user_id,omitempty" jsonschema:"description=New user ID"`
	SessionID  string         `json:"session_id,omitempty" jsonschema:"description=New session ID"`
	Metadata   map[string]any `json:"metadata,omitempty" jsonschema:"description=New metadata (replaces existing)"`
	Timeout    string         `json:"timeout,omitempty" jsonschema:"description=New timeout"`
	MaxRetries *int           `json:"max_retries,omitempty" jsonschema:"description=New max retries"`
}

func (ts *CronToolSet) updateTool() tool.Tool {
	return function.NewFunctionTool(
		ts.update,
		function.WithName("cron_update"),
		function.WithDescription("Update an existing proactive schedule by name. Only provided fields are changed."),
	)
}

func (ts *CronToolSet) update(_ context.Context, req cronUpdateRequest) (*cronMutationResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	cfg := &ts.cfg.Proactive

	// Try cron jobs.
	for i := range cfg.Jobs {
		if cfg.Jobs[i].Name == req.Name {
			j := &cfg.Jobs[i]
			if req.Schedule != "" {
				if _, err := cron.ParseStandard(req.Schedule); err != nil {
					parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
					if _, err2 := parser.Parse(req.Schedule); err2 != nil {
						return nil, fmt.Errorf("invalid cron schedule %q: %w", req.Schedule, err)
					}
				}
				j.Schedule = req.Schedule
			}
			if req.Prompt != "" {
				j.Prompt = req.Prompt
			}
			if req.Channel != "" {
				j.Channel = req.Channel
			}
			if req.UserID != "" {
				j.UserID = req.UserID
			}
			if req.SessionID != "" {
				j.SessionID = req.SessionID
			}
			if req.Metadata != nil {
				j.Metadata = req.Metadata
			}
			if req.Timeout != "" {
				j.Timeout = req.Timeout
			}
			if req.MaxRetries != nil {
				j.MaxRetries = *req.MaxRetries
			}
			return ts.saveAndReload(req.Name, "cron")
		}
	}

	// Try webhooks.
	for i := range cfg.Webhooks {
		if cfg.Webhooks[i].Name == req.Name {
			w := &cfg.Webhooks[i]
			if req.Path != "" {
				w.Path = req.Path
			}
			if req.Prompt != "" {
				w.Prompt = req.Prompt
			}
			if req.Channel != "" {
				w.Channel = req.Channel
			}
			if req.UserID != "" {
				w.UserID = req.UserID
			}
			if req.SessionID != "" {
				w.SessionID = req.SessionID
			}
			if req.Metadata != nil {
				w.Metadata = req.Metadata
			}
			if req.Timeout != "" {
				w.Timeout = req.Timeout
			}
			if req.MaxRetries != nil {
				w.MaxRetries = *req.MaxRetries
			}
			return ts.saveAndReload(req.Name, "webhook")
		}
	}

	// Try heartbeats.
	for i := range cfg.Heartbeats {
		if cfg.Heartbeats[i].Name == req.Name {
			h := &cfg.Heartbeats[i]
			if req.Interval != "" {
				if _, err := time.ParseDuration(req.Interval); err != nil {
					return nil, fmt.Errorf("invalid interval %q: %w", req.Interval, err)
				}
				h.Interval = req.Interval
			}
			if req.Prompt != "" {
				h.Prompt = req.Prompt
			}
			if req.Channel != "" {
				h.Channel = req.Channel
			}
			if req.UserID != "" {
				h.UserID = req.UserID
			}
			if req.SessionID != "" {
				h.SessionID = req.SessionID
			}
			if req.Metadata != nil {
				h.Metadata = req.Metadata
			}
			if req.Timeout != "" {
				h.Timeout = req.Timeout
			}
			if req.MaxRetries != nil {
				h.MaxRetries = *req.MaxRetries
			}
			return ts.saveAndReload(req.Name, "heartbeat")
		}
	}

	return nil, fmt.Errorf("schedule %q not found", req.Name)
}

// --- cron_delete ---

type cronDeleteRequest struct {
	Name string `json:"name" jsonschema:"required,description=Name of the schedule to delete"`
}

func (ts *CronToolSet) deleteTool() tool.Tool {
	return function.NewFunctionTool(
		ts.delete,
		function.WithName("cron_delete"),
		function.WithDescription("Delete a proactive schedule by name. Removes it from config and stops it immediately."),
	)
}

func (ts *CronToolSet) delete(_ context.Context, req cronDeleteRequest) (*cronMutationResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	cfg := &ts.cfg.Proactive

	for i, j := range cfg.Jobs {
		if j.Name == req.Name {
			cfg.Jobs = append(cfg.Jobs[:i], cfg.Jobs[i+1:]...)
			return ts.saveAndReload(req.Name, "cron")
		}
	}
	for i, w := range cfg.Webhooks {
		if w.Name == req.Name {
			cfg.Webhooks = append(cfg.Webhooks[:i], cfg.Webhooks[i+1:]...)
			return ts.saveAndReload(req.Name, "webhook")
		}
	}
	for i, h := range cfg.Heartbeats {
		if h.Name == req.Name {
			cfg.Heartbeats = append(cfg.Heartbeats[:i], cfg.Heartbeats[i+1:]...)
			return ts.saveAndReload(req.Name, "heartbeat")
		}
	}

	return nil, fmt.Errorf("schedule %q not found", req.Name)
}

// --- helpers ---

func (ts *CronToolSet) saveAndReload(name, typ string) (*cronMutationResponse, error) {
	if err := ts.cfg.Save(); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}
	if err := ts.reload(); err != nil {
		return nil, fmt.Errorf("reload engine: %w", err)
	}
	return &cronMutationResponse{Message: fmt.Sprintf("Updated %s schedule %q", typ, name)}, nil
}

func (ts *CronToolSet) nameExists(name string) bool {
	cfg := &ts.cfg.Proactive
	for _, j := range cfg.Jobs {
		if j.Name == name {
			return true
		}
	}
	for _, w := range cfg.Webhooks {
		if w.Name == name {
			return true
		}
	}
	for _, h := range cfg.Heartbeats {
		if h.Name == name {
			return true
		}
	}
	return false
}
