// Package gateway implements the multi-channel gateway server.
package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/yourusername/kaggen/internal/channel"
)

// Handler processes messages from channels using the trpc-agent-go Runner.
type Handler struct {
	runner runner.Runner
	logger *slog.Logger
}

// NewHandler creates a new message handler with a trpc-agent-go Runner.
func NewHandler(appName string, ag agent.Agent, sessionService session.Service, logger *slog.Logger) *Handler {
	var opts []runner.Option
	if sessionService != nil {
		opts = append(opts, runner.WithSessionService(sessionService))
	}

	r := runner.NewRunner(appName, ag, opts...)

	return &Handler{
		runner: r,
		logger: logger,
	}
}

// HandleMessage processes an incoming message and sends responses via the callback.
func (h *Handler) HandleMessage(ctx context.Context, msg *channel.Message, respond func(*channel.Response) error) error {
	h.logger.Info("handling message",
		"session_id", msg.SessionID,
		"channel", msg.Channel,
		"content_length", len(msg.Content))

	// Create a user message for the runner
	userMessage := model.NewUserMessage(msg.Content)

	// Run the agent using the trpc-agent-go Runner
	events, err := h.runner.Run(
		ctx,
		msg.UserID,
		msg.SessionID,
		userMessage,
		agent.WithRequestID(uuid.New().String()),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}

	// Stream events back as responses
	for evt := range events {
		resp := h.eventToResponse(evt, msg)
		if resp != nil {
			if err := respond(resp); err != nil {
				h.logger.Warn("failed to send response", "error", err)
			}
		}
	}

	return nil
}

// eventToResponse converts a trpc-agent-go event to a channel response.
func (h *Handler) eventToResponse(evt *event.Event, msg *channel.Message) *channel.Response {
	if evt == nil || evt.Response == nil {
		return nil
	}

	resp := &channel.Response{
		ID:        uuid.New().String(),
		MessageID: msg.ID,
		SessionID: msg.SessionID,
		Done:      evt.Done,
		Metadata:  copyMetadata(msg.Metadata),
	}

	// Handle error responses
	if evt.Response.Error != nil {
		resp.Type = "error"
		resp.Content = evt.Response.Error.Message
		resp.Done = true
		resp.Metadata["error_type"] = evt.Response.Error.Type
		return resp
	}

	// Handle content from choices
	if len(evt.Response.Choices) > 0 {
		choice := evt.Response.Choices[0]

		// Check for tool calls
		if len(choice.Message.ToolCalls) > 0 {
			resp.Type = "tool_call"
			tc := choice.Message.ToolCalls[0]
			resp.Content = fmt.Sprintf("Calling tool: %s", tc.Function.Name)
			resp.Metadata["tool_name"] = tc.Function.Name
			resp.Metadata["tool_id"] = tc.ID
			resp.Metadata["tool_input"] = string(tc.Function.Arguments)
			return resp
		}

		// Check for tool result
		if choice.Message.Role == model.RoleTool {
			resp.Type = "tool_result"
			resp.Content = choice.Message.Content
			resp.Metadata["tool_id"] = choice.Message.ToolID
			return resp
		}

		// Regular text content
		if choice.Message.Content != "" || choice.Delta.Content != "" {
			content := choice.Message.Content
			if content == "" {
				content = choice.Delta.Content
			}

			if evt.Done {
				resp.Type = "done"
			} else if evt.IsPartial {
				resp.Type = "text"
			} else {
				resp.Type = "text"
			}
			resp.Content = content
			return resp
		}
	}

	// Handle runner completion event
	if evt.IsRunnerCompletion() {
		resp.Type = "done"
		resp.Done = true
		if len(evt.Response.Choices) > 0 {
			resp.Content = evt.Response.Choices[0].Message.Content
		}
		return resp
	}

	// Skip events without meaningful content
	return nil
}

// copyMetadata returns a shallow copy of the metadata map, preserving
// channel-specific keys (e.g. chat_id) so they are available when sending
// the response back through the originating channel.
func copyMetadata(src map[string]any) map[string]any {
	m := make(map[string]any, len(src))
	for k, v := range src {
		m[k] = v
	}
	return m
}

// Close closes the handler and releases resources.
func (h *Handler) Close() error {
	if closer, ok := h.runner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
