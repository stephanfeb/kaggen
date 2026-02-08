package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

func TestNewLocalAgent_Defaults(t *testing.T) {
	agent := NewLocalAgent(nil, nil)

	if agent.model != "llama3.2:3b" {
		t.Errorf("expected default model llama3.2:3b, got %s", agent.model)
	}
	if agent.baseURL != "http://localhost:11434" {
		t.Errorf("expected default baseURL, got %s", agent.baseURL)
	}
}

func TestNewLocalAgent_CustomConfig(t *testing.T) {
	cfg := &config.ThirdPartyConfig{
		LocalLLMModel: "mistral:7b",
		SystemPrompt:  "Custom prompt",
	}
	agent := NewLocalAgent(cfg, nil)

	if agent.model != "mistral:7b" {
		t.Errorf("expected model mistral:7b, got %s", agent.model)
	}
	if agent.systemPrompt != "Custom prompt" {
		t.Errorf("expected custom prompt, got %s", agent.systemPrompt)
	}
}

func TestLocalSessionStore(t *testing.T) {
	store := newLocalSessionStore(5)

	// Add messages.
	store.addMessage("session1", ollamaChatMessage{Role: "system", Content: "System"})
	store.addMessage("session1", ollamaChatMessage{Role: "user", Content: "Hello"})
	store.addMessage("session1", ollamaChatMessage{Role: "assistant", Content: "Hi there!"})

	history := store.getHistory("session1")
	if len(history) != 3 {
		t.Errorf("expected 3 messages, got %d", len(history))
	}

	// Clear session.
	store.clearSession("session1")
	history = store.getHistory("session1")
	if len(history) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(history))
	}
}

func TestLocalSessionStore_Trimming(t *testing.T) {
	store := newLocalSessionStore(4) // max 4 messages

	// Add system prompt + 5 user/assistant pairs = 11 messages.
	store.addMessage("session1", ollamaChatMessage{Role: "system", Content: "System"})
	for i := 0; i < 5; i++ {
		store.addMessage("session1", ollamaChatMessage{Role: "user", Content: "User msg"})
		store.addMessage("session1", ollamaChatMessage{Role: "assistant", Content: "Assistant msg"})
	}

	history := store.getHistory("session1")
	if len(history) != 4 {
		t.Errorf("expected 4 messages after trimming, got %d", len(history))
	}

	// First should still be system prompt.
	if history[0].Role != "system" {
		t.Errorf("expected first message to be system, got %s", history[0].Role)
	}
}

func TestLocalAgent_HandleMessage(t *testing.T) {
	// Create mock Ollama server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var req ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify request.
		if req.Model == "" {
			t.Error("expected model in request")
		}
		if len(req.Messages) < 2 {
			t.Errorf("expected at least 2 messages (system + user), got %d", len(req.Messages))
		}

		// Return mock response.
		resp := ollamaChatResponse{
			Message: ollamaChatMessage{
				Role:    "assistant",
				Content: "Hello! How can I help you?",
			},
			Done: true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	agent := NewLocalAgent(nil, nil)
	agent.SetBaseURL(server.URL)

	msg := &channel.Message{
		ID:        "msg-123",
		SessionID: "session-456",
		Content:   "Hello",
	}

	resp, err := agent.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Type != "text" {
		t.Errorf("expected type 'text', got %s", resp.Type)
	}
	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("unexpected content: %s", resp.Content)
	}
	if !resp.Done {
		t.Error("expected Done to be true")
	}

	// Second message should have history.
	msg2 := &channel.Message{
		ID:        "msg-124",
		SessionID: "session-456",
		Content:   "What's 2+2?",
	}
	resp2, err := agent.HandleMessage(context.Background(), msg2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.Content == "" {
		t.Error("expected non-empty response")
	}
}

func TestLocalAgent_HandleMessage_Error(t *testing.T) {
	// Create server that returns an error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	agent := NewLocalAgent(nil, nil)
	agent.SetBaseURL(server.URL)

	msg := &channel.Message{
		ID:        "msg-123",
		SessionID: "session-456",
		Content:   "Hello",
	}

	resp, err := agent.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return a graceful error message.
	if resp.Type != "error" {
		t.Errorf("expected type 'error', got %s", resp.Type)
	}
	if resp.Content == "" {
		t.Error("expected error message content")
	}
}

func TestLocalAgent_IsAvailable(t *testing.T) {
	// Server that responds to /api/tags.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"models":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	agent := NewLocalAgent(nil, nil)
	agent.SetBaseURL(server.URL)

	if !agent.IsAvailable(context.Background()) {
		t.Error("expected IsAvailable to return true")
	}

	// Test with unreachable server.
	agent.SetBaseURL("http://localhost:99999")
	if agent.IsAvailable(context.Background()) {
		t.Error("expected IsAvailable to return false for unreachable server")
	}
}

func TestLocalAgent_ClearSession(t *testing.T) {
	agent := NewLocalAgent(nil, nil)

	// Add some history.
	agent.sessions.addMessage("session-1", ollamaChatMessage{Role: "user", Content: "Hello"})

	// Verify it exists.
	if len(agent.sessions.getHistory("session-1")) != 1 {
		t.Error("expected history to exist")
	}

	// Clear it.
	agent.ClearSession("session-1")

	// Verify it's gone.
	if len(agent.sessions.getHistory("session-1")) != 0 {
		t.Error("expected history to be cleared")
	}
}
