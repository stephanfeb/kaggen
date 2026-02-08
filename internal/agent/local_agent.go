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
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"`
}

// ollamaChatRequest is the request body for /api/chat.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

// ollamaChatResponse is the response body for /api/chat.
type ollamaChatResponse struct {
	Message ollamaChatMessage `json:"message"`
	Done    bool              `json:"done"`
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

	// Call Ollama.
	response, err := a.chat(ctx, history)
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

	// Store assistant response.
	a.sessions.addMessage(msg.SessionID, ollamaChatMessage{
		Role:    "assistant",
		Content: response,
	})

	return &channel.Response{
		SessionID: msg.SessionID,
		MessageID: msg.ID,
		Type:      "text",
		Content:   response,
		Done:      true,
	}, nil
}

// chat sends messages to Ollama and returns the response.
func (a *LocalAgent) chat(ctx context.Context, messages []ollamaChatMessage) (string, error) {
	reqBody := ollamaChatRequest{
		Model:    a.model,
		Messages: messages,
		Stream:   false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return strings.TrimSpace(chatResp.Message.Content), nil
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
