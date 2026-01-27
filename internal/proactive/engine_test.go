package proactive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

// mockHandler implements channel.Handler for testing.
type mockHandler struct {
	response string
}

func (m *mockHandler) HandleMessage(_ context.Context, msg *channel.Message, respond func(*channel.Response) error) error {
	return respond(&channel.Response{
		ID:        "resp-1",
		SessionID: msg.SessionID,
		Content:   m.response,
		Type:      "done",
		Done:      true,
	})
}

// mockChannel implements channel.Channel for testing.
type mockChannel struct {
	mu   sync.Mutex
	sent []*channel.Response
}

func (m *mockChannel) Name() string                              { return "test" }
func (m *mockChannel) Start(_ context.Context) error             { return nil }
func (m *mockChannel) Stop(_ context.Context) error              { return nil }
func (m *mockChannel) Messages() <-chan *channel.Message          { return nil }
func (m *mockChannel) Send(_ context.Context, r *channel.Response) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, r)
	return nil
}

func (m *mockChannel) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type testResolver struct {
	ch channel.Channel
}

func (r *testResolver) Channel(name string) channel.Channel {
	if name == "test" {
		return r.ch
	}
	return nil
}

func TestCronExecution(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "hello from cron"}
	cfg := &config.ProactiveConfig{
		Jobs: []config.CronJobConfig{{
			Name:     "test-cron",
			Schedule: "@every 1s",
			Prompt:   "test prompt",
			UserID:   "user1",
			Channel:  "test",
		}},
	}

	logger := slog.Default()
	engine := New(cfg, handler, &testResolver{ch: ch}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

	// Wait for cron to fire
	deadline := time.After(3 * time.Second)
	for {
		if ch.sentCount() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("cron job did not fire within deadline")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestHeartbeatSuppressesEmpty(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: ""}
	cfg := &config.ProactiveConfig{
		Heartbeats: []config.HeartbeatConfig{{
			Name:     "test-hb",
			Interval: "100ms",
			Prompt:   "check",
			UserID:   "user1",
			Channel:  "test",
		}},
	}

	logger := slog.Default()
	engine := New(cfg, handler, &testResolver{ch: ch}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

	time.Sleep(500 * time.Millisecond)

	if ch.sentCount() != 0 {
		t.Errorf("expected no sends for empty heartbeat, got %d", ch.sentCount())
	}
}

func TestHeartbeatSendsNonEmpty(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "alert!"}
	cfg := &config.ProactiveConfig{
		Heartbeats: []config.HeartbeatConfig{{
			Name:     "test-hb",
			Interval: "100ms",
			Prompt:   "check",
			UserID:   "user1",
			Channel:  "test",
		}},
	}

	logger := slog.Default()
	engine := New(cfg, handler, &testResolver{ch: ch}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

	deadline := time.After(2 * time.Second)
	for {
		if ch.sentCount() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("heartbeat did not send within deadline")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestWebhookAccepted(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "webhook response"}
	cfg := &config.ProactiveConfig{
		Webhooks: []config.WebhookConfig{{
			Name:    "test-wh",
			Path:    "/hooks/test",
			Prompt:  "event: {{.Payload}}",
			UserID:  "user1",
			Channel: "test",
		}},
	}

	logger := slog.Default()
	engine := New(cfg, handler, &testResolver{ch: ch}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

	// POST to webhook
	req := httptest.NewRequest(http.MethodPost, "/hooks/test", strings.NewReader(`{"action":"push"}`))
	w := httptest.NewRecorder()
	engine.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

	// Wait for async execution
	deadline := time.After(2 * time.Second)
	for {
		if ch.sentCount() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("webhook did not produce response within deadline")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestWebhookRejectsGet(t *testing.T) {
	cfg := &config.ProactiveConfig{
		Webhooks: []config.WebhookConfig{{
			Name:    "test-wh",
			Path:    "/hooks/test",
			Prompt:  "event",
			UserID:  "user1",
			Channel: "test",
		}},
	}

	logger := slog.Default()
	engine := New(cfg, &mockHandler{}, &testResolver{}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)
	defer engine.Stop()

	req := httptest.NewRequest(http.MethodGet, "/hooks/test", nil)
	w := httptest.NewRecorder()
	engine.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
