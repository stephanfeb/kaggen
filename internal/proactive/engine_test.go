package proactive

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

// mockHandler implements channel.Handler for testing.
type mockHandler struct {
	response string
	delay    time.Duration
	failN    int // fail first N calls
	calls    atomic.Int32
}

func (m *mockHandler) HandleMessage(ctx context.Context, msg *channel.Message, respond func(*channel.Response) error) error {
	n := m.calls.Add(1)

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if m.failN > 0 && int(n) <= m.failN {
		return fmt.Errorf("mock failure %d", n)
	}

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

func (m *mockChannel) Name() string                                        { return "test" }
func (m *mockChannel) Start(_ context.Context) error                       { return nil }
func (m *mockChannel) Stop(_ context.Context) error                        { return nil }
func (m *mockChannel) Messages() <-chan *channel.Message                   { return nil }
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
	engine := New(cfg, handler, &testResolver{ch: ch}, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

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
	engine := New(cfg, handler, &testResolver{ch: ch}, logger, nil)

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
	engine := New(cfg, handler, &testResolver{ch: ch}, logger, nil)

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
	engine := New(cfg, handler, &testResolver{ch: ch}, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", strings.NewReader(`{"action":"push"}`))
	w := httptest.NewRecorder()
	engine.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

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
	engine := New(cfg, &mockHandler{}, &testResolver{}, logger, nil)

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

func TestWebhookHMACValid(t *testing.T) {
	ch := &mockChannel{}
	secret := "test-secret-123"
	handler := &mockHandler{response: "ok"}
	cfg := &config.ProactiveConfig{
		Webhooks: []config.WebhookConfig{{
			Name:    "test-wh",
			Path:    "/hooks/test",
			Prompt:  "event: {{.Payload}}",
			UserID:  "user1",
			Channel: "test",
			Secret:  secret,
		}},
	}

	engine := New(cfg, handler, &testResolver{ch: ch}, slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)
	defer engine.Stop()

	body := []byte(`{"action":"push"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	engine.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestWebhookHMACInvalid(t *testing.T) {
	cfg := &config.ProactiveConfig{
		Webhooks: []config.WebhookConfig{{
			Name:    "test-wh",
			Path:    "/hooks/test",
			Prompt:  "event",
			UserID:  "user1",
			Channel: "test",
			Secret:  "correct-secret",
		}},
	}

	engine := New(cfg, &mockHandler{}, &testResolver{}, slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)
	defer engine.Stop()

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", strings.NewReader("body"))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	w := httptest.NewRecorder()
	engine.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestWebhookNoSecretSkipsVerification(t *testing.T) {
	ch := &mockChannel{}
	cfg := &config.ProactiveConfig{
		Webhooks: []config.WebhookConfig{{
			Name:    "test-wh",
			Path:    "/hooks/test",
			Prompt:  "event",
			UserID:  "user1",
			Channel: "test",
			// No Secret set
		}},
	}

	engine := New(cfg, &mockHandler{response: "ok"}, &testResolver{ch: ch}, slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.Start(ctx)
	defer engine.Stop()

	req := httptest.NewRequest(http.MethodPost, "/hooks/test", strings.NewReader("body"))
	w := httptest.NewRecorder()
	engine.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestJobTimeout(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "slow", delay: 2 * time.Second}
	cfg := &config.ProactiveConfig{
		Jobs: []config.CronJobConfig{{
			Name:     "slow-job",
			Schedule: "@every 1s",
			Prompt:   "test",
			UserID:   "user1",
			Channel:  "test",
			Timeout:  "100ms",
		}},
	}

	engine := New(cfg, handler, &testResolver{ch: ch}, slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer engine.Stop()

	// Wait for the job to fire and timeout
	time.Sleep(2 * time.Second)

	if engine.metrics.JobsTimedOut.Load() == 0 {
		t.Error("expected at least one timeout")
	}
	if ch.sentCount() != 0 {
		t.Errorf("expected no sends for timed out job, got %d", ch.sentCount())
	}
}

func TestRetryOnFailure(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "ok", failN: 2} // fail first 2, succeed on 3rd
	cfg := &config.ProactiveConfig{}

	engine := New(cfg, handler, &testResolver{ch: ch}, slog.Default(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	engine.executeWithRetry(ctx, "retry-test", "cron", "test", "user1", "", "test", nil, false, 5*time.Second, 3)

	calls := handler.calls.Load()
	if calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls)
	}
	if engine.metrics.JobsRetried.Load() != 2 {
		t.Errorf("expected 2 retries, got %d", engine.metrics.JobsRetried.Load())
	}
}

func TestRetryExhausted(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "ok", failN: 100} // always fail
	cfg := &config.ProactiveConfig{}

	engine := New(cfg, handler, &testResolver{ch: ch}, slog.Default(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	engine.executeWithRetry(ctx, "exhaust-test", "cron", "test", "user1", "", "test", nil, false, 5*time.Second, 2)

	calls := handler.calls.Load()
	if calls != 3 { // initial + 2 retries
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if engine.metrics.JobsFailed.Load() != 3 {
		t.Errorf("expected 3 failures, got %d", engine.metrics.JobsFailed.Load())
	}
}

func TestCleanShutdown(t *testing.T) {
	handler := &mockHandler{response: "ok", delay: 500 * time.Millisecond}
	cfg := &config.ProactiveConfig{
		Heartbeats: []config.HeartbeatConfig{{
			Name:     "slow-hb",
			Interval: "100ms",
			Prompt:   "check",
			UserID:   "user1",
			Channel:  "test",
		}},
	}

	engine := New(cfg, handler, &testResolver{ch: &mockChannel{}}, slog.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Let heartbeat fire at least once
	time.Sleep(200 * time.Millisecond)

	// Cancel and stop — should not hang
	cancel()
	done := make(chan struct{})
	go func() {
		engine.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5 seconds")
	}
}

func TestMetricsIncrement(t *testing.T) {
	ch := &mockChannel{}
	handler := &mockHandler{response: "ok"}
	cfg := &config.ProactiveConfig{}

	engine := New(cfg, handler, &testResolver{ch: ch}, slog.Default(), nil)
	ctx := context.Background()

	// Successful execution
	err := engine.execute(ctx, "test", "cron", "prompt", "user1", "", "test", nil, false, 5*time.Second, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := engine.GetMetrics()
	if m.JobsExecuted.Load() != 1 {
		t.Errorf("expected 1 executed, got %d", m.JobsExecuted.Load())
	}
}
