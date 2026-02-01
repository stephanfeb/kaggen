package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// SessionNamer generates human-readable session names from the first user message
// using a local Ollama model. Falls back to "Chat <date>" on any error.
type SessionNamer struct {
	ollamaURL   string
	ollamaModel string
	httpClient  *http.Client
	logger      *slog.Logger
}

// NewSessionNamer creates a new SessionNamer. Returns nil if ollamaURL is empty.
func NewSessionNamer(ollamaURL, ollamaModel string, logger *slog.Logger) *SessionNamer {
	if ollamaURL == "" {
		return nil
	}
	if ollamaModel == "" {
		ollamaModel = "qwen2.5:1.5b"
	}
	return &SessionNamer{
		ollamaURL:   ollamaURL,
		ollamaModel: ollamaModel,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		logger:      logger,
	}
}

type ollamaNamingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaNamingResponse struct {
	Response string `json:"response"`
}

// InferName generates a short session title from the first user message.
// Returns a fallback name on any error.
func (n *SessionNamer) InferName(ctx context.Context, firstMessage string) string {
	if n == nil {
		return fallbackName()
	}

	// Truncate very long messages to keep the prompt small.
	msg := firstMessage
	if len(msg) > 500 {
		msg = msg[:500]
	}

	prompt := fmt.Sprintf(
		"Generate a short (2-5 word) title for a conversation that starts with the following message. "+
			"Reply with ONLY the title, nothing else. No quotes, no punctuation at the end.\n\nMessage: %s", msg)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(ollamaNamingRequest{
		Model:  n.ollamaModel,
		Prompt: prompt,
		Stream: false,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.ollamaURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		n.logger.Debug("session_namer: request build failed", "error", err)
		return fallbackName()
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		n.logger.Debug("session_namer: ollama unreachable, using fallback", "error", err)
		return fallbackName()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		n.logger.Debug("session_namer: ollama non-200", "status", resp.StatusCode)
		return fallbackName()
	}

	var ollamaResp ollamaNamingResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		n.logger.Debug("session_namer: decode failed", "error", err)
		return fallbackName()
	}

	name := sanitizeSessionName(ollamaResp.Response)
	if name == "" {
		return fallbackName()
	}
	return name
}

// sanitizeSessionName cleans up the LLM output to a usable session name.
func sanitizeSessionName(raw string) string {
	name := strings.TrimSpace(raw)
	// Remove surrounding quotes if present.
	name = strings.Trim(name, "\"'`")
	// Take only the first line.
	if idx := strings.IndexAny(name, "\n\r"); idx >= 0 {
		name = name[:idx]
	}
	// Truncate to reasonable length.
	if len(name) > 60 {
		name = name[:60]
	}
	return strings.TrimSpace(name)
}

// fallbackName returns a default session name based on the current date.
func fallbackName() string {
	return "Chat " + time.Now().Format("Jan 2")
}
