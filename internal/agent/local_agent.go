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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/trust"
)

// PromptBasedFunctionInstructions is prepended to the system prompt when the model
// doesn't support native Ollama tools. This enables function calling via output parsing.
const PromptBasedFunctionInstructions = `AVAILABLE FUNCTIONS:
You have access to a relay function. When you decide to use it, output ONLY the function call in this exact format on its own line - no other text before or after:

[relay_message(message="<the message>", urgency="<low|normal|high>")]

Use relay_message when the visitor explicitly asks you to pass a message, notify, inform, or contact the Prime Operator.

Example: User says "Tell the Prime Operator I need help with order #123"
Your response should be ONLY: [relay_message(message="Visitor needs help with order #123", urgency="normal")]

After outputting the function call, do NOT add any additional text. The system will handle the response.

---

`

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

	// Tool capability tracking (auto-detected on first call)
	toolCapMu           sync.RWMutex
	toolCapChecked      bool
	supportsNativeTools bool
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

// Regex patterns for parsing prompt-based function calls.
// Matches: [relay_message(message="...", urgency="...")]
var (
	promptFuncRegex = regexp.MustCompile(`\[relay_message\(([^)]+)\)\]`)
	argRegex        = regexp.MustCompile(`(\w+)=["']([^"']+)["']`)
)

// parseNativeToolCall extracts relay args from native Ollama tool calls.
func parseNativeToolCall(toolCalls []ollamaToolCall) *relayMessageArgs {
	for _, tc := range toolCalls {
		if tc.Function.Name == "relay_message" {
			var args relayMessageArgs
			if json.Unmarshal(tc.Function.Arguments, &args) == nil {
				return &args
			}
		}
	}
	return nil
}

// parsePromptBasedCall extracts relay args from prompt-based output.
// Matches: [relay_message(message="...", urgency="...")]
func parsePromptBasedCall(content string) *relayMessageArgs {
	matches := promptFuncRegex.FindStringSubmatch(content)
	if len(matches) < 2 {
		return nil
	}

	args := &relayMessageArgs{Urgency: "normal"}
	argMatches := argRegex.FindAllStringSubmatch(matches[1], -1)
	for _, m := range argMatches {
		if len(m) >= 3 {
			switch m[1] {
			case "message":
				args.Message = m[2]
			case "urgency":
				args.Urgency = m[2]
			}
		}
	}

	if args.Message == "" {
		return nil
	}
	return args
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

// supportsTools returns whether the model supports native Ollama tools.
// Auto-detected on first call attempt.
func (a *LocalAgent) supportsTools() bool {
	a.toolCapMu.RLock()
	defer a.toolCapMu.RUnlock()
	return a.supportsNativeTools
}

// isToolCapChecked returns whether tool capability has been checked.
func (a *LocalAgent) isToolCapChecked() bool {
	a.toolCapMu.RLock()
	defer a.toolCapMu.RUnlock()
	return a.toolCapChecked
}

// markToolSupport caches whether the model supports native tools.
func (a *LocalAgent) markToolSupport(supported bool) {
	a.toolCapMu.Lock()
	defer a.toolCapMu.Unlock()
	a.toolCapChecked = true
	a.supportsNativeTools = supported
}

// buildSystemPrompt returns the full system prompt.
// Adds function calling instructions only if model doesn't support native tools.
func (a *LocalAgent) buildSystemPrompt() string {
	a.toolCapMu.RLock()
	needsPromptFunctions := a.toolCapChecked && !a.supportsNativeTools
	a.toolCapMu.RUnlock()

	if needsPromptFunctions {
		return PromptBasedFunctionInstructions + a.systemPrompt
	}
	return a.systemPrompt
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
// Uses adaptive function calling: tries native Ollama tools first, falls back to prompt-based.
func (a *LocalAgent) HandleMessage(ctx context.Context, msg *channel.Message) (*channel.Response, error) {
	a.logger.Info("local agent handling message",
		"session_id", msg.SessionID,
		"content_length", len(msg.Content))

	// Get existing history or start fresh.
	history := a.sessions.getHistory(msg.SessionID)
	if len(history) == 0 {
		// Add system prompt as first message (may include function instructions if needed).
		history = append(history, ollamaChatMessage{
			Role:    "system",
			Content: a.buildSystemPrompt(),
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

	// Adaptive function calling: try native tools first, fallback to prompt-based.
	tools := []ollamaTool{relayMessageTool()}
	var chatResp *ollamaChatResponse
	var err error

	if !a.isToolCapChecked() || a.supportsTools() {
		// Try native tools (first call or model supports them).
		chatResp, err = a.chatWithTools(ctx, history, tools)

		// Check for "does not support tools" error.
		if err != nil && strings.Contains(err.Error(), "does not support tools") {
			a.markToolSupport(false)
			a.logger.Info("model does not support native tools, switching to prompt-based",
				"model", a.model)

			// Rebuild history with function instructions prepended to system prompt.
			if len(history) > 0 && history[0].Role == "system" {
				history[0].Content = a.buildSystemPrompt()
				// Update the stored system prompt for this session.
				a.sessions.clearSession(msg.SessionID)
				for _, m := range history {
					a.sessions.addMessage(msg.SessionID, m)
				}
			}

			// Retry without tools parameter.
			chatResp, err = a.chatWithTools(ctx, history, nil)
		} else if err == nil {
			// Native tools worked - mark as supported.
			if !a.isToolCapChecked() {
				a.markToolSupport(true)
				a.logger.Info("model supports native tools", "model", a.model)
			}
		}
	} else {
		// Model known to not support tools - use prompt-based directly.
		chatResp, err = a.chatWithTools(ctx, history, nil)
	}

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
	var relayArgs *relayMessageArgs

	// Check for function calls (either native or parsed from output).
	if len(chatResp.Message.ToolCalls) > 0 {
		// Native tool call from Ollama.
		relayArgs = parseNativeToolCall(chatResp.Message.ToolCalls)
	} else {
		// Try parsing from output text (prompt-based function calling).
		relayArgs = parsePromptBasedCall(chatResp.Message.Content)
	}

	if relayArgs != nil {
		a.logger.Info("relay_message function detected",
			"message", relayArgs.Message,
			"urgency", relayArgs.Urgency,
			"session_id", msg.SessionID,
			"native_tools", a.supportsTools())

		// Execute the relay.
		relayExecuted = a.executeRelay(ctx, msg, *relayArgs)

		if relayExecuted {
			response = "I have relayed your message to the Prime Operator. They will be notified shortly."
		} else {
			response = "I attempted to relay your message, but encountered an issue. Please try again."
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
