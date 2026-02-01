// Package gateway implements the multi-channel gateway server.
package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

// RespondFunc is a callback for sending responses through a channel.
type RespondFunc func(*channel.Response) error

// SessionResponder tracks the respond callback for active sessions so that
// async completion events can route responses back to the originating channel.
type SessionResponder struct {
	mu        sync.RWMutex
	responder map[string]RespondFunc // sessionID -> respond callback
	metadata  map[string]map[string]any // sessionID -> original message metadata
}

// NewSessionResponder creates a new session responder registry.
func NewSessionResponder() *SessionResponder {
	return &SessionResponder{
		responder: make(map[string]RespondFunc),
		metadata:  make(map[string]map[string]any),
	}
}

// Register stores the respond callback and metadata for a session.
func (sr *SessionResponder) Register(sessionID string, respond RespondFunc, metadata map[string]any) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.responder[sessionID] = respond
	sr.metadata[sessionID] = metadata
}

// Get returns the respond callback and metadata for a session.
func (sr *SessionResponder) Get(sessionID string) (RespondFunc, map[string]any, bool) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	fn, ok := sr.responder[sessionID]
	meta := sr.metadata[sessionID]
	return fn, meta, ok
}

// Handler processes messages from channels using the trpc-agent-go Runner.
type Handler struct {
	runner     runner.Runner
	logger     *slog.Logger
	responders *SessionResponder
}

// NewHandler creates a new message handler with a trpc-agent-go Runner.
func NewHandler(appName string, ag agent.Agent, sessionService session.Service, logger *slog.Logger, memService ...memory.Service) *Handler {
	var opts []runner.Option
	if sessionService != nil {
		opts = append(opts, runner.WithSessionService(sessionService))
	}
	if len(memService) > 0 && memService[0] != nil {
		opts = append(opts, runner.WithMemoryService(memService[0]))
	}

	r := runner.NewRunner(appName, ag, opts...)

	return &Handler{
		runner:     r,
		logger:     logger,
		responders: NewSessionResponder(),
	}
}

// HandleMessage processes an incoming message and sends responses via the callback.
func (h *Handler) HandleMessage(ctx context.Context, msg *channel.Message, respond func(*channel.Response) error) error {
	h.logger.Info("handling message",
		"session_id", msg.SessionID,
		"channel", msg.Channel,
		"content_length", len(msg.Content))

	// Register the respond callback so async completions can route back.
	h.responders.Register(msg.SessionID, respond, msg.Metadata)

	// Create a user message for the runner, attaching any uploaded files.
	userMessage := model.NewUserMessage(msg.Content)
	for _, att := range msg.Attachments {
		if isImageMime(att.MimeType) {
			if err := userMessage.AddImageFilePath(att.Path, "auto"); err != nil {
				h.logger.Warn("failed to attach image", "path", att.Path, "error", err)
			}
		} else {
			if err := userMessage.AddFilePath(att.Path); err != nil {
				h.logger.Warn("failed to attach file", "path", att.Path, "error", err)
			}
		}
	}

	// Run the agent using the trpc-agent-go Runner
	events, err := h.runner.Run(
		ctx,
		msg.UserID,
		msg.SessionID,
		userMessage,
		agent.WithRequestID(uuid.New().String()),
	)
	if err != nil {
		h.logger.Error("agent run failed",
			"error", err,
			"session_id", msg.SessionID,
			"user_id", msg.UserID,
		)
		errResp := &channel.Response{
			ID:        uuid.New().String(),
			MessageID: msg.ID,
			SessionID: msg.SessionID,
			Type:      "error",
			Content:   fmt.Sprintf("Sorry, I encountered an error processing your request: %v", err),
			Done:      true,
			Metadata:  copyMetadata(msg.Metadata),
		}
		if sendErr := respond(errResp); sendErr != nil {
			h.logger.Warn("failed to send error response", "error", sendErr)
		}
		return fmt.Errorf("run agent: %w", err)
	}

	// Consume all events and send each text response immediately so the
	// user sees progress messages (e.g. "I'm building your dashboard...")
	// as they happen rather than only the final response.

	for evt := range events {
		resp := h.eventToResponse(evt, msg)
		if resp == nil {
			continue
		}

		switch resp.Type {
		case "tool_call", "tool_result":
			if err := respond(resp); err != nil {
				h.logger.Warn("failed to send response", "error", err)
			}
			continue
		case "error":
			if err := respond(resp); err != nil {
				h.logger.Warn("failed to send error response", "error", err)
			}
			continue
		}

		// Send text responses immediately.
		if resp.Content != "" {
			resp.Content, resp.Metadata = extractSendFiles(resp.Content, resp.Metadata)
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

	// Handle runner completion event.
	// The final text content is already emitted by the content block above
	// (with type "done" when evt.Done is true), so we skip runner completion
	// events to avoid sending duplicate messages.
	if evt.IsRunnerCompletion() {
		return nil
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

// sendFileRe matches [send_file: /path/to/file] directives in agent responses.
var sendFileRe = regexp.MustCompile(`\[send_file:\s*([^\]]+)\]`)

// publicDir returns the path to the public file-serving directory, creating it if needed.
func publicDir() string {
	dir := config.ExpandPath("~/.kaggen/public")
	os.MkdirAll(dir, 0755)
	return dir
}

// publishFile copies a file into the public directory with a content-hashed name.
// Returns the public filename (no path) or empty string on error.
func publishFile(srcPath string) string {
	srcPath = config.ExpandPath(strings.TrimSpace(srcPath))
	src, err := os.Open(srcPath)
	if err != nil {
		return ""
	}
	defer src.Close()

	// Hash a prefix of the content + original name for a stable short filename.
	h := sha256.New()
	io.CopyN(h, src, 64*1024) // hash first 64KB
	src.Seek(0, io.SeekStart)

	ext := filepath.Ext(srcPath)
	name := fmt.Sprintf("%x%s", h.Sum(nil)[:8], ext)

	dstPath := filepath.Join(publicDir(), name)
	if _, err := os.Stat(dstPath); err == nil {
		return name // already published
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		return ""
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dstPath)
		return ""
	}
	return name
}

// extractSendFiles scans text for [send_file: /path] directives, removes them,
// copies the file to the public directory, and sets metadata["send_file"] to the
// public filename (no server paths exposed).
func extractSendFiles(text string, meta map[string]any) (string, map[string]any) {
	matches := sendFileRe.FindStringSubmatch(text)
	if matches == nil {
		return text, meta
	}
	filePath := strings.TrimSpace(matches[1])

	// Publish file and expose only the public name.
	if pubName := publishFile(filePath); pubName != "" {
		meta["send_file"] = pubName
	}
	// Keep original path for channels that read local files (e.g. Telegram).
	meta["send_file_local"] = config.ExpandPath(filePath)

	text = strings.TrimSpace(sendFileRe.ReplaceAllString(text, ""))
	return text, meta
}

// isImageMime returns true if the MIME type is a supported image format.
func isImageMime(mime string) bool {
	return strings.HasPrefix(mime, "image/")
}

// InjectCompletion injects a task completion event into the coordinator's
// session, triggering a new reasoning turn. The coordinator sees the completion
// as an internal message and can synthesize/communicate the result to the user.
func (h *Handler) InjectCompletion(ctx context.Context, sessionID, userID, taskID, agentName, result string) error {
	content := fmt.Sprintf("[Task Completed: %s (agent: %s)]\n\n%s", taskID, agentName, result)

	respond, metadata, ok := h.responders.Get(sessionID)
	if !ok {
		h.logger.Warn("no responder for session, completion will not be delivered to user",
			"session_id", sessionID, "task_id", taskID)
		// Still run the agent so the result is recorded in session history,
		// but discard responses.
		respond = func(_ *channel.Response) error { return nil }
		metadata = map[string]any{}
	}

	msg := &channel.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    userID,
		Content:   content,
		Channel:   "internal",
		Metadata:  copyMetadata(metadata),
	}

	return h.HandleMessage(ctx, msg, respond)
}

// Responders returns the session responder registry, allowing external
// components (e.g. async agent tools) to look up respond callbacks.
func (h *Handler) Responders() *SessionResponder {
	return h.responders
}

// Close closes the handler and releases resources.
func (h *Handler) Close() error {
	if closer, ok := h.runner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
