// Package proactive implements the proactive engine for scheduled,
// webhook-triggered, and heartbeat-driven agent actions.
package proactive

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

// Default timeouts per job type.
const (
	DefaultCronTimeout      = 5 * time.Minute
	DefaultWebhookTimeout   = 5 * time.Minute
	DefaultHeartbeatTimeout = 2 * time.Minute
	ShutdownGracePeriod     = 30 * time.Second
)

// Metrics tracks proactive engine counters.
type Metrics struct {
	JobsExecuted atomic.Int64
	JobsFailed   atomic.Int64
	JobsRetried  atomic.Int64
	JobsTimedOut atomic.Int64
}

// ChannelResolver finds a channel by name.
type ChannelResolver interface {
	Channel(name string) channel.Channel
}

// Engine runs proactive jobs (cron, webhooks, heartbeats).
type Engine struct {
	cfg      *config.ProactiveConfig
	handler  channel.Handler
	channels ChannelResolver
	logger   *slog.Logger
	cron     *cron.Cron
	mux      *http.ServeMux
	history  *HistoryStore
	metrics  Metrics

	parentCtx context.Context    // stored for Reload
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex // serializes Start/Stop/Reload
}

// New creates a new proactive engine. history may be nil to disable tracking.
func New(cfg *config.ProactiveConfig, handler channel.Handler, channels ChannelResolver, logger *slog.Logger, history *HistoryStore) *Engine {
	return &Engine{
		cfg:      cfg,
		handler:  handler,
		channels: channels,
		logger:   logger,
		cron:     cron.New(),
		mux:      http.NewServeMux(),
		history:  history,
	}
}

// GetMetrics returns a snapshot of the engine metrics.
func (e *Engine) GetMetrics() Metrics {
	var m Metrics
	m.JobsExecuted.Store(e.metrics.JobsExecuted.Load())
	m.JobsFailed.Store(e.metrics.JobsFailed.Load())
	m.JobsRetried.Store(e.metrics.JobsRetried.Load())
	m.JobsTimedOut.Store(e.metrics.JobsTimedOut.Load())
	return m
}

// Start registers all jobs and begins the scheduler.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startLocked(ctx)
}

func (e *Engine) startLocked(ctx context.Context) error {
	e.parentCtx = ctx
	ctx, e.cancel = context.WithCancel(ctx)

	// Register cron jobs
	for _, job := range e.cfg.Jobs {
		job := job
		timeout := parseDurationOr(job.Timeout, DefaultCronTimeout)
		_, err := e.cron.AddFunc(job.Schedule, func() {
			e.wg.Add(1)
			defer e.wg.Done()
			e.executeWithRetry(ctx, job.Name, "cron", job.Prompt, job.UserID, job.SessionID, job.Channel, job.Metadata, false, timeout, job.MaxRetries)
		})
		if err != nil {
			return fmt.Errorf("register cron job %q: %w", job.Name, err)
		}
		e.logger.Info("registered cron job", "name", job.Name, "schedule", job.Schedule)
	}
	e.cron.Start()

	// Register webhooks
	for _, wh := range e.cfg.Webhooks {
		wh := wh
		timeout := parseDurationOr(wh.Timeout, DefaultWebhookTimeout)
		e.mux.HandleFunc(wh.Path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))

			// HMAC signature verification
			if wh.Secret != "" {
				sig := r.Header.Get("X-Hub-Signature-256")
				if !verifyHMAC([]byte(wh.Secret), body, sig) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}

			prompt := strings.ReplaceAll(wh.Prompt, "{{.Payload}}", string(body))

			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				e.executeWithRetry(ctx, wh.Name, "webhook", prompt, wh.UserID, wh.SessionID, wh.Channel, wh.Metadata, false, timeout, wh.MaxRetries)
			}()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"status":"accepted"}`))
		})
		e.logger.Info("registered webhook", "name", wh.Name, "path", wh.Path)
	}

	// Start heartbeats
	for _, hb := range e.cfg.Heartbeats {
		hb := hb
		dur, err := time.ParseDuration(hb.Interval)
		if err != nil {
			return fmt.Errorf("parse heartbeat interval %q: %w", hb.Name, err)
		}
		timeout := parseDurationOr(hb.Timeout, DefaultHeartbeatTimeout)
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			ticker := time.NewTicker(dur)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					e.executeWithRetry(ctx, hb.Name, "heartbeat", hb.Prompt, hb.UserID, hb.SessionID, hb.Channel, hb.Metadata, true, timeout, hb.MaxRetries)
				}
			}
		}()
		e.logger.Info("registered heartbeat", "name", hb.Name, "interval", hb.Interval)
	}

	return nil
}

// Mux returns the webhook HTTP handler for mounting on the gateway server.
func (e *Engine) Mux() *http.ServeMux {
	return e.mux
}

// Stop shuts down the engine, waiting for running jobs to complete.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
	if e.history != nil {
		e.history.Close()
	}
}

func (e *Engine) stopLocked() {
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	stopCtx := e.cron.Stop()
	<-stopCtx.Done()

	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(ShutdownGracePeriod):
		e.logger.Warn("proactive engine: forced shutdown after grace period")
	}
}

// Reload stops the current scheduler, replaces the config, and starts fresh.
func (e *Engine) Reload(cfg *config.ProactiveConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.stopLocked()

	e.cfg = cfg
	e.cron = cron.New()

	return e.startLocked(e.parentCtx)
}

// executeWithRetry wraps execute with exponential backoff retries.
func (e *Engine) executeWithRetry(ctx context.Context, name, jobType, prompt, userID, sessionID, chName string, metadata map[string]any, heartbeat bool, timeout time.Duration, maxRetries int) {
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			e.metrics.JobsRetried.Add(1)
			e.logger.Info("retrying proactive job", "name", name, "attempt", attempt, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}

		err := e.execute(ctx, name, jobType, prompt, userID, sessionID, chName, metadata, heartbeat, timeout, attempt+1)
		if err == nil {
			return
		}

		if ctx.Err() != nil {
			return
		}
	}
}

// execute runs a single proactive job with a timeout. Returns nil on success.
func (e *Engine) execute(ctx context.Context, name, jobType, prompt, userID, sessionID, chName string, metadata map[string]any, heartbeat bool, timeout time.Duration, attempt int) error {
	if sessionID == "" {
		sessionID = "proactive-" + name
	}

	ch := e.channels.Channel(chName)
	if ch == nil {
		e.logger.Error("proactive job: channel not found", "name", name, "channel", chName)
		return fmt.Errorf("channel not found: %s", chName)
	}

	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startedAt := time.Now()

	msg := &channel.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    userID,
		Content:   prompt,
		Channel:   chName,
		Metadata:  copyMeta(metadata),
	}

	var finalContent string
	err := e.handler.HandleMessage(jobCtx, msg, func(resp *channel.Response) error {
		if resp.Done && resp.Content != "" {
			finalContent = resp.Content
		}
		if !heartbeat && resp.Type == "done" && resp.Content != "" {
			return ch.Send(jobCtx, resp)
		}
		return nil
	})

	duration := time.Since(startedAt)
	status := "success"
	var errMsg string

	if err != nil {
		e.metrics.JobsFailed.Add(1)
		if jobCtx.Err() == context.DeadlineExceeded {
			e.metrics.JobsTimedOut.Add(1)
			status = "timeout"
			e.logger.Error("proactive job timed out", "name", name, "timeout", timeout)
		} else {
			status = "failure"
			e.logger.Error("proactive job failed", "name", name, "error", err)
		}
		errMsg = err.Error()
	} else {
		e.metrics.JobsExecuted.Add(1)

		// Heartbeat: only send if agent produced non-empty content
		if heartbeat && strings.TrimSpace(finalContent) != "" {
			resp := &channel.Response{
				ID:        uuid.New().String(),
				SessionID: sessionID,
				Content:   finalContent,
				Type:      "done",
				Done:      true,
				Metadata:  copyMeta(metadata),
			}
			if sendErr := ch.Send(ctx, resp); sendErr != nil {
				e.logger.Error("heartbeat send failed", "name", name, "error", sendErr)
			}
		}

		e.logger.Info("proactive job completed", "name", name)
	}

	// Record to history
	if e.history != nil {
		if recErr := e.history.Record(name, jobType, startedAt, duration, status, errMsg, attempt); recErr != nil {
			e.logger.Error("failed to record job history", "name", name, "error", recErr)
		}
	}

	if err != nil {
		return err
	}
	return nil
}

// verifyHMAC checks the X-Hub-Signature-256 header against the body.
func verifyHMAC(secret, body []byte, signature string) bool {
	sig := strings.TrimPrefix(signature, "sha256=")
	if sig == "" || sig == signature {
		return false
	}
	decoded, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), decoded)
}

func parseDurationOr(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func copyMeta(src map[string]any) map[string]any {
	if src == nil {
		return make(map[string]any)
	}
	m := make(map[string]any, len(src))
	for k, v := range src {
		m[k] = v
	}
	return m
}
