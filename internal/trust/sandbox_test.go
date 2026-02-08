package trust

import (
	"context"
	"testing"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

func TestDetectRelayRequest(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Please tell the owner I need help", "I need help"},
		{"Tell owner that the server is down", "the server is down"},
		{"Can you pass this message to the owner: call me back", "call me back"},
		{"Message for owner: I have a question about the project", "I have a question about the project"},
		{"Please have the owner contact me", ""},  // Pattern matches but capture group is empty
		{"Hello, how are you?", ""},               // Not a relay request
		{"What's the weather like?", ""},          // Not a relay request
		{"Ask the owner about the meeting", "about the meeting"},
		{"Let the owner know I'm waiting", "know I'm waiting"},
		{"notify owner that payment was received", "payment was received"},
		{"Relay to owner: urgent matter", "urgent matter"},
		{"Forward this to the owner: need approval", "need approval"},
	}

	for _, tt := range tests {
		got := DetectRelayRequest(tt.input)
		if got != tt.expected {
			t.Errorf("DetectRelayRequest(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSessionTracker(t *testing.T) {
	tracker := NewSessionTracker(3)

	// First 3 messages should be within limit.
	for i := 1; i <= 3; i++ {
		count, exceeded := tracker.Increment("session1")
		if count != i {
			t.Errorf("expected count %d, got %d", i, count)
		}
		if exceeded {
			t.Errorf("message %d should not exceed limit", i)
		}
	}

	// 4th message should exceed limit.
	count, exceeded := tracker.Increment("session1")
	if count != 4 {
		t.Errorf("expected count 4, got %d", count)
	}
	if !exceeded {
		t.Error("4th message should exceed limit")
	}

	// Different session should start fresh.
	count, exceeded = tracker.Increment("session2")
	if count != 1 {
		t.Errorf("expected count 1 for new session, got %d", count)
	}
	if exceeded {
		t.Error("first message in new session should not exceed limit")
	}

	// Reset should clear count.
	tracker.Reset("session1")
	if tracker.Count("session1") != 0 {
		t.Error("expected count 0 after reset")
	}
}

func TestSessionTracker_Unlimited(t *testing.T) {
	tracker := NewSessionTracker(0) // 0 = unlimited

	// Many messages should never exceed limit.
	for i := 1; i <= 100; i++ {
		_, exceeded := tracker.Increment("session1")
		if exceeded {
			t.Errorf("message %d should not exceed unlimited limit", i)
		}
	}
}

func TestRelayStore(t *testing.T) {
	store := NewRelayStore("", nil) // In-memory only

	req := &RelayRequest{
		ID:        "test-123",
		SessionID: "session-456",
		Message:   "Hello owner",
		Status:    "pending",
	}

	store.Add(req)

	// Should be retrievable.
	got, ok := store.Get("test-123")
	if !ok {
		t.Fatal("expected to find request")
	}
	if got.Message != "Hello owner" {
		t.Errorf("expected message 'Hello owner', got %q", got.Message)
	}

	// Should be in pending list.
	pending := store.ListPending()
	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}

	// Mark as delivered.
	store.MarkDelivered("test-123")
	got, _ = store.Get("test-123")
	if got.Status != "delivered" {
		t.Errorf("expected status 'delivered', got %q", got.Status)
	}

	// Should not be in pending list anymore.
	pending = store.ListPending()
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after delivery, got %d", len(pending))
	}
}

type mockOwnerNotifier struct {
	notified bool
	message  string
}

func (m *mockOwnerNotifier) NotifyOwner(ctx context.Context, message string) error {
	m.notified = true
	m.message = message
	return nil
}

func TestSandbox_ProcessMessage_RelayRequest(t *testing.T) {
	cfg := &config.ThirdPartyConfig{
		Enabled:          true,
		AllowRelay:       true,
		MaxSessionLength: 10,
	}
	notifier := &mockOwnerNotifier{}
	sandbox := NewSandbox(cfg, "", notifier, nil)

	msg := &channel.Message{
		SessionID:   "test-session",
		Content:     "Please tell the owner I need help with my account",
		SenderPhone: "+1234567890",
		Channel:     "whatsapp",
		Metadata:    map[string]any{"push_name": "John"},
	}

	resp, err := sandbox.ProcessMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response for relay request")
	}

	if !resp.IsRelayRequest {
		t.Error("expected IsRelayRequest to be true")
	}

	if resp.RelayRequest == nil {
		t.Fatal("expected RelayRequest to be set")
	}

	if resp.RelayRequest.Message != "I need help with my account" {
		t.Errorf("expected message 'I need help with my account', got %q", resp.RelayRequest.Message)
	}

	if resp.RelayRequest.SenderName != "John" {
		t.Errorf("expected sender name 'John', got %q", resp.RelayRequest.SenderName)
	}
}

func TestSandbox_ProcessMessage_RelayDisabled(t *testing.T) {
	cfg := &config.ThirdPartyConfig{
		Enabled:    true,
		AllowRelay: false,
	}
	sandbox := NewSandbox(cfg, "", nil, nil)

	msg := &channel.Message{
		SessionID: "test-session",
		Content:   "Please tell the owner I need help",
		Channel:   "telegram",
	}

	resp, err := sandbox.ProcessMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("expected response")
	}

	if resp.IsRelayRequest {
		t.Error("should not be marked as relay request when disabled")
	}

	if !containsString(resp.Message, "not able to relay") {
		t.Errorf("expected refusal message, got %q", resp.Message)
	}
}

func TestSandbox_ProcessMessage_SessionLimit(t *testing.T) {
	cfg := &config.ThirdPartyConfig{
		Enabled:          true,
		MaxSessionLength: 2,
	}
	sandbox := NewSandbox(cfg, "", nil, nil)

	msg := &channel.Message{
		SessionID: "test-session",
		Content:   "Hello",
		Channel:   "telegram",
	}

	// First two messages should return nil (normal processing).
	for i := 0; i < 2; i++ {
		resp, err := sandbox.ProcessMessage(context.Background(), msg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp != nil {
			t.Errorf("message %d should return nil for normal processing", i+1)
		}
	}

	// Third message should exceed limit.
	resp, err := sandbox.ProcessMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response for exceeded limit")
	}
	if !resp.LimitExceeded {
		t.Error("expected LimitExceeded to be true")
	}
}

func TestSandbox_ProcessMessage_NormalMessage(t *testing.T) {
	cfg := &config.ThirdPartyConfig{
		Enabled:          true,
		MaxSessionLength: 10,
	}
	sandbox := NewSandbox(cfg, "", nil, nil)

	msg := &channel.Message{
		SessionID: "test-session",
		Content:   "What's the weather like today?",
		Channel:   "telegram",
	}

	resp, err := sandbox.ProcessMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Normal messages should return nil, indicating the caller should
	// route to local LLM or sandboxed agent.
	if resp != nil {
		t.Error("normal message should return nil for external processing")
	}
}

func TestSandbox_GetSystemPrompt(t *testing.T) {
	// Default prompt.
	sandbox := NewSandbox(nil, "", nil, nil)
	prompt := sandbox.GetSystemPrompt()
	if prompt != DefaultSandboxSystemPrompt {
		t.Error("expected default prompt when config is nil")
	}

	// Custom prompt.
	cfg := &config.ThirdPartyConfig{
		SystemPrompt: "Custom prompt here",
	}
	sandbox = NewSandbox(cfg, "", nil, nil)
	prompt = sandbox.GetSystemPrompt()
	if prompt != "Custom prompt here" {
		t.Errorf("expected custom prompt, got %q", prompt)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
