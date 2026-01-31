package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	kaggenAgent "github.com/yourusername/kaggen/internal/agent"
)

// CallbackURLFunc returns the current callback base URL.
// This is a function to support dynamic URLs (e.g. from tunnel discovery).
type CallbackURLFunc func() string

// ExternalTaskToolSet provides tools for registering external tasks that will
// be completed via HTTP callback or Pub/Sub message.
type ExternalTaskToolSet struct {
	store       *kaggenAgent.InFlightStore
	callbackURL CallbackURLFunc
	pubsubTopic string // populated when pubsub is enabled (informational for agent)
}

// NewExternalTaskToolSet creates a new external task tool set.
func NewExternalTaskToolSet(store *kaggenAgent.InFlightStore, callbackURL CallbackURLFunc) *ExternalTaskToolSet {
	return &ExternalTaskToolSet{
		store:       store,
		callbackURL: callbackURL,
	}
}

// SetPubSubTopic sets the Pub/Sub topic name to include in register responses.
func (ts *ExternalTaskToolSet) SetPubSubTopic(topic string) {
	ts.pubsubTopic = topic
}

// Tools returns the tools in this set.
func (ts *ExternalTaskToolSet) Tools() []tool.Tool {
	return []tool.Tool{ts.registerTool(), ts.listExternalTool()}
}

type externalRegisterRequest struct {
	Name           string `json:"name" jsonschema:"required,description=Human-readable description of the external task"`
	CallbackSecret string `json:"callback_secret,omitempty" jsonschema:"description=Optional HMAC-SHA256 secret for verifying callbacks"`
	Timeout        string `json:"timeout,omitempty" jsonschema:"description=How long to wait for callback before auto-failing (Go duration e.g. 30m or 2h). Default: 1h"`
	Policy         string `json:"policy,omitempty" jsonschema:"description=Completion trigger policy: auto (inject immediately) or queue (wait for next user turn). Default: auto,enum=auto,enum=queue"`
}

type externalRegisterResponse struct {
	TaskID      string `json:"task_id"`
	CallbackURL string `json:"callback_url"`
	StatusURL   string `json:"status_url"`
	Timeout     string `json:"timeout"`
	PubSubTopic string `json:"pubsub_topic,omitempty"` // set when Pub/Sub bridge is enabled
}

func (ts *ExternalTaskToolSet) registerTool() tool.Tool {
	return function.NewFunctionTool(
		ts.register,
		function.WithName("external_task_register"),
		function.WithDescription("Register an external task that will be completed via HTTP callback or Pub/Sub message. Returns a task_id and callback_url to pass to the external system. The external system should POST results to the callback_url, or publish to the pubsub_topic with a task_id attribute. Use this before launching external work (e.g. GCP instances, CI pipelines, external APIs)."),
	)
}

func (ts *ExternalTaskToolSet) register(ctx context.Context, req externalRegisterRequest) (*externalRegisterResponse, error) {
	baseURL := ts.callbackURL()
	if baseURL == "" {
		return nil, fmt.Errorf("no callback URL available — ensure tunnel is enabled or callback_base_url is configured")
	}

	timeout := 1 * time.Hour
	if req.Timeout != "" {
		d, err := time.ParseDuration(req.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", req.Timeout, err)
		}
		timeout = d
	}

	policy := kaggenAgent.TriggerAuto
	if req.Policy == "queue" {
		policy = kaggenAgent.TriggerQueue
	}

	taskID := uuid.New().String()

	// Extract session/user from invocation context.
	var sessionID, userID string
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv.Session != nil {
		sessionID = inv.Session.ID
		userID = inv.Session.UserID
	}

	ts.store.RegisterExternal(taskID, req.Name, req.CallbackSecret, policy, sessionID, userID, timeout)

	callbackURL := fmt.Sprintf("%s/callbacks/%s", baseURL, taskID)
	statusURL := fmt.Sprintf("%s/callbacks/%s/status", baseURL, taskID)

	resp := &externalRegisterResponse{
		TaskID:      taskID,
		CallbackURL: callbackURL,
		StatusURL:   statusURL,
		Timeout:     timeout.String(),
		PubSubTopic: ts.pubsubTopic,
	}
	return resp, nil
}

type externalListResponse struct {
	Tasks []externalTaskSummary `json:"tasks"`
}

type externalTaskSummary struct {
	TaskID    string `json:"task_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
	TimeoutAt string `json:"timeout_at"`
}

func (ts *ExternalTaskToolSet) listExternalTool() tool.Tool {
	return function.NewFunctionTool(
		ts.listExternal,
		function.WithName("external_task_list"),
		function.WithDescription("List all registered external tasks and their current status."),
	)
}

func (ts *ExternalTaskToolSet) listExternal(_ context.Context, _ struct{}) (*externalListResponse, error) {
	all := ts.store.List("")
	var tasks []externalTaskSummary
	for _, t := range all {
		if !t.External {
			continue
		}
		tasks = append(tasks, externalTaskSummary{
			TaskID:    t.ID,
			Name:      t.Task,
			Status:    string(t.Status),
			StartedAt: t.StartedAt.Format(time.RFC3339),
			TimeoutAt: t.TimeoutAt.Format(time.RFC3339),
		})
	}
	return &externalListResponse{Tasks: tasks}, nil
}
