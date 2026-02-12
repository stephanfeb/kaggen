// Package agent provides AI agent implementations.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/trust"
)

// LocalAgent provides a local LLM (Ollama) backed agent for third-party conversations.
// This allows handling third-party messages without incurring frontier model API costs.
type LocalAgent struct {
	baseURL      string
	model        string
	systemPrompt string
	httpClient   *http.Client
	logger       *slog.Logger
	sessions     *localSessionStore

	// Persistence and notification (optional)
	store      *trust.ThirdPartyStore
	notifier   *trust.TelegramOwnerNotifier
	relayStore *trust.RelayStore
}

// localSessionStore stores conversation history per session.
type localSessionStore struct {
	mu       sync.RWMutex
	sessions map[string][]ollamaChatMessage // sessionID -> messages
	maxMsgs  int                            // max messages to keep per session
}

// newLocalSessionStore creates a new session store.
func newLocalSessionStore(maxMsgs int) *localSessionStore {
	if maxMsgs <= 0 {
		maxMsgs = 20 // default to last 20 messages
	}
	return &localSessionStore{
		sessions: make(map[string][]ollamaChatMessage),
		maxMsgs:  maxMsgs,
	}
}

// addMessage adds a message to a session's history.
func (s *localSessionStore) addMessage(sessionID string, msg ollamaChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sessionID] = append(s.sessions[sessionID], msg)

	// Trim to max messages (keep system prompt + recent)
	msgs := s.sessions[sessionID]
	if len(msgs) > s.maxMsgs {
		// Keep first message if it's a system prompt, plus the most recent messages
		var trimmed []ollamaChatMessage
		if len(msgs) > 0 && msgs[0].Role == "system" {
			trimmed = append(trimmed, msgs[0])
			trimmed = append(trimmed, msgs[len(msgs)-(s.maxMsgs-1):]...)
		} else {
			trimmed = msgs[len(msgs)-s.maxMsgs:]
		}
		s.sessions[sessionID] = trimmed
	}
}

// getHistory returns the message history for a session.
func (s *localSessionStore) getHistory(sessionID string) []ollamaChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if msgs, ok := s.sessions[sessionID]; ok {
		result := make([]ollamaChatMessage, len(msgs))
		copy(result, msgs)
		return result
	}
	return nil
}

// clearSession clears a session's history.
func (s *localSessionStore) clearSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// ollamaChatMessage represents a message in the Ollama chat format.
type ollamaChatMessage struct {
	Role      string          `json:"role"`                 // "system", "user", "assistant", "tool"
	Content   string          `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"` // For assistant messages with tool calls
}

// ollamaToolCall represents a tool call from the LLM.
type ollamaToolCall struct {
	Function ollamaFunctionCall `json:"function"`
}

// ollamaFunctionCall represents the function details in a tool call.
type ollamaFunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ollamaTool represents a tool definition for the LLM.
type ollamaTool struct {
	Type     string             `json:"type"` // "function"
	Function ollamaToolFunction `json:"function"`
}

// ollamaToolFunction represents a function tool definition.
type ollamaToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  ollamaToolParameters   `json:"parameters"`
}

// ollamaToolParameters represents the parameters schema for a tool.
type ollamaToolParameters struct {
	Type       string                          `json:"type"` // "object"
	Properties map[string]ollamaToolProperty   `json:"properties"`
	Required   []string                        `json:"required"`
}

// ollamaToolProperty represents a single property in the parameters schema.
type ollamaToolProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ollamaChatRequest is the request body for /api/chat.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Tools    []ollamaTool        `json:"tools,omitempty"`
	Stream   bool                `json:"stream"`
}

// ollamaChatResponse is the response body for /api/chat.
type ollamaChatResponse struct {
	Message ollamaChatMessage `json:"message"`
	Done    bool              `json:"done"`
}

// relayMessageArgs represents the arguments for the relay_message tool.
type relayMessageArgs struct {
	Message string `json:"message"`
	Urgency string `json:"urgency,omitempty"`
}

// relayMessageTool returns the tool definition for relaying messages.
func relayMessageTool() ollamaTool {
	return ollamaTool{
		Type: "function",
		Function: ollamaToolFunction{
			Name:        "relay_message",
			Description: "Relay an important message to the Prime Operator. Use this when the visitor explicitly asks you to pass a message, notify, inform, or contact the Prime Operator.",
			Parameters: ollamaToolParameters{
				Type: "object",
				Properties: map[string]ollamaToolProperty{
					"message": {
						Type:        "string",
						Description: "The message to relay to the Prime Operator. Summarize the visitor's request clearly and concisely.",
					},
					"urgency": {
						Type:        "string",
						Description: "The urgency level: 'low', 'normal', or 'high'. Default to 'normal' unless the visitor indicates urgency.",
					},
				},
				Required: []string{"message"},
			},
		},
	}
}

// NewLocalAgent creates a new local LLM agent using Ollama.
func NewLocalAgent(cfg *config.ThirdPartyConfig, logger *slog.Logger) *LocalAgent {
	if logger == nil {
		logger = slog.Default()
	}

	baseURL := "http://localhost:11434"
	model := "llama3.2:3b"
	systemPrompt := trust.DefaultSandboxSystemPrompt

	if cfg != nil {
		if cfg.LocalLLMModel != "" {
			model = cfg.LocalLLMModel
		}
		if cfg.SystemPrompt != "" {
			systemPrompt = cfg.SystemPrompt
		}
	}

	return &LocalAgent{
		baseURL:      baseURL,
		model:        model,
		systemPrompt: systemPrompt,
		httpClient:   &http.Client{Timeout: 60 * time.Second},
		logger:       logger,
		sessions:     newLocalSessionStore(20),
	}
}

// SetBaseURL sets the Ollama base URL (for testing).
func (a *LocalAgent) SetBaseURL(url string) {
	a.baseURL = url
}

// SetStore sets the third-party message store for persistence.
func (a *LocalAgent) SetStore(store *trust.ThirdPartyStore) {
	a.store = store
}

// SetNotifier sets the owner notifier for digest notifications.
func (a *LocalAgent) SetNotifier(notifier *trust.TelegramOwnerNotifier) {
	a.notifier = notifier
}

// SetRelayStore sets the relay store for message relay functionality.
func (a *LocalAgent) SetRelayStore(store *trust.RelayStore) {
	a.relayStore = store
}

// Summarize uses the local LLM to summarize text.
// Implements trust.Summarizer interface for digest notifications.
func (a *LocalAgent) Summarize(ctx context.Context, prompt string) string {
	msgs := []ollamaChatMessage{
		{Role: "system", Content: "You are a helpful assistant. Summarize the following concisely."},
		{Role: "user", Content: prompt},
	}
	resp, err := a.chatWithTools(ctx, msgs, nil)
	if err != nil {
		a.logger.Warn("summarization failed", "error", err)
		return "(summarization failed)"
	}
	return strings.TrimSpace(resp.Message.Content)
}

// HandleMessage processes a third-party message using the local LLM.
func (a *LocalAgent) HandleMessage(ctx context.Context, msg *channel.Message) (*channel.Response, error) {
	a.logger.Info("local agent handling message",
		"session_id", msg.SessionID,
		"content_length", len(msg.Content))

	// Get existing history or start fresh.
	history := a.sessions.getHistory(msg.SessionID)
	if len(history) == 0 {
		// Add system prompt as first message.
		history = append(history, ollamaChatMessage{
			Role:    "system",
			Content: a.systemPrompt,
		})
		a.sessions.addMessage(msg.SessionID, history[0])
	}

	// Add user message.
	userMsg := ollamaChatMessage{
		Role:    "user",
		Content: msg.Content,
	}
	a.sessions.addMessage(msg.SessionID, userMsg)
	history = append(history, userMsg)

	// Define tools (only relay_message for now).
	tools := []ollamaTool{relayMessageTool()}

	// Call Ollama with tools.
	chatResp, err := a.chatWithTools(ctx, history, tools)
	if err != nil {
		a.logger.Error("local agent chat failed", "error", err)
		return &channel.Response{
			SessionID: msg.SessionID,
			MessageID: msg.ID,
			Type:      "error",
			Content:   "I'm having trouble processing your message right now. Please try again later.",
			Done:      true,
		}, nil
	}

	var response string
	var relayExecuted bool

	// Check if the LLM wants to call a tool.
	if len(chatResp.Message.ToolCalls) > 0 {
		for _, toolCall := range chatResp.Message.ToolCalls {
			if toolCall.Function.Name == "relay_message" {
				// Parse the relay message arguments.
				var args relayMessageArgs
				if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
					a.logger.Warn("failed to parse relay_message args", "error", err)
					continue
				}

				a.logger.Info("relay_message tool called",
					"message", args.Message,
					"urgency", args.Urgency,
					"session_id", msg.SessionID)

				// Execute the relay.
				relayExecuted = a.executeRelay(ctx, msg, args)
			}
		}

		// Generate a follow-up response after tool execution.
		if relayExecuted {
			response = "I have relayed your message to the Prime Operator. They will be notified shortly."
		} else {
			response = chatResp.Message.Content
			if response == "" {
				response = "I attempted to relay your message, but encountered an issue. Please try again."
			}
		}
	} else {
		response = strings.TrimSpace(chatResp.Message.Content)
	}

	// Store assistant response.
	a.sessions.addMessage(msg.SessionID, ollamaChatMessage{
		Role:    "assistant",
		Content: response,
	})

	// Persist to store and queue notification (if configured).
	if a.store != nil || a.notifier != nil {
		tpMsg := &trust.ThirdPartyMessage{
			ID:               uuid.New().String(),
			SessionID:        msg.SessionID,
			SenderPhone:      msg.SenderPhone,
			SenderTelegramID: msg.SenderTelegramID,
			Channel:          msg.Channel,
			UserMessage:      msg.Content,
			LLMResponse:      response,
			CreatedAt:        time.Now(),
		}
		if name, ok := msg.Metadata["push_name"].(string); ok {
			tpMsg.SenderName = name
		}

		// Persist to SQLite store
		if a.store != nil {
			if err := a.store.Add(tpMsg); err != nil {
				a.logger.Warn("failed to persist third-party message", "error", err)
			}
		}

		// Queue for batched notification
		if a.notifier != nil {
			a.notifier.QueueNotification(tpMsg)
		}
	}

	return &channel.Response{
		SessionID: msg.SessionID,
		MessageID: msg.ID,
		Type:      "text",
		Content:   response,
		Done:      true,
	}, nil
}

// executeRelay stores a relay request and notifies the owner.
func (a *LocalAgent) executeRelay(ctx context.Context, msg *channel.Message, args relayMessageArgs) bool {
	// Create the relay request.
	req := &trust.RelayRequest{
		ID:           uuid.New().String(),
		SessionID:    msg.SessionID,
		SenderPhone:  msg.SenderPhone,
		Message:      args.Message,
		OriginalText: msg.Content,
		CreatedAt:    time.Now().UTC(),
		Channel:      msg.Channel,
		Status:       "pending",
	}

	// Extract sender name from metadata if available.
	if pushName, ok := msg.Metadata["push_name"].(string); ok {
		req.SenderName = pushName
	}

	// Store the relay request.
	if a.relayStore != nil {
		a.relayStore.Add(req)
		a.logger.Info("relay request stored",
			"relay_id", req.ID,
			"session_id", msg.SessionID,
			"message", args.Message)
	} else {
		a.logger.Warn("relay_message called but no relay store configured")
	}

	// Notify owner via the notifier if available.
	if a.notifier != nil {
		urgencyEmoji := "📬"
		if args.Urgency == "high" {
			urgencyEmoji = "🚨"
		}

		senderInfo := req.SenderPhone
		if req.SenderName != "" {
			senderInfo = fmt.Sprintf("%s (%s)", req.SenderName, req.SenderPhone)
		}
		if senderInfo == "" {
			senderInfo = "Unknown sender"
		}

		notifyMsg := fmt.Sprintf("%s *Relay from third-party*\n\n"+
			"*From:* %s\n"+
			"*Channel:* %s\n"+
			"*Message:* %s\n\n"+
			"_Relay ID: %s_",
			urgencyEmoji, senderInfo, req.Channel, args.Message, req.ID)

		if err := a.notifier.NotifyOwner(ctx, notifyMsg); err != nil {
			a.logger.Warn("failed to notify owner of relay", "error", err)
			return false
		}
	}

	return true
}

// chat sends messages to Ollama and returns the text response (no tools).
func (a *LocalAgent) chat(ctx context.Context, messages []ollamaChatMessage) (string, error) {
	resp, err := a.chatWithTools(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

// chatWithTools sends messages to Ollama with optional tool support.
func (a *LocalAgent) chatWithTools(ctx context.Context, messages []ollamaChatMessage, tools []ollamaTool) (*ollamaChatResponse, error) {
	reqBody := ollamaChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &chatResp, nil
}

// ClearSession clears the conversation history for a session.
func (a *LocalAgent) ClearSession(sessionID string) {
	a.sessions.clearSession(sessionID)
}

// IsAvailable checks if the local LLM (Ollama) is reachable.
func (a *LocalAgent) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Debug("local LLM not available", "error", err)
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// Model returns the configured model name.
func (a *LocalAgent) Model() string {
	return a.model
}
