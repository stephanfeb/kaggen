// Package proactive implements the proactive engine for scheduled,
// webhook-triggered, and heartbeat-driven agent actions.
package proactive

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

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
}

// New creates a new proactive engine.
func New(cfg *config.ProactiveConfig, handler channel.Handler, channels ChannelResolver, logger *slog.Logger) *Engine {
	return &Engine{
		cfg:      cfg,
		handler:  handler,
		channels: channels,
		logger:   logger,
		cron:     cron.New(),
		mux:      http.NewServeMux(),
	}
}

// Start registers all jobs and begins the scheduler.
func (e *Engine) Start(ctx context.Context) error {
	// Register cron jobs
	for _, job := range e.cfg.Jobs {
		job := job
		_, err := e.cron.AddFunc(job.Schedule, func() {
			e.execute(ctx, job.Name, job.Prompt, job.UserID, job.SessionID, job.Channel, job.Metadata, false)
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
		e.mux.HandleFunc(wh.Path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
			prompt := strings.ReplaceAll(wh.Prompt, "{{.Payload}}", string(body))

			go e.execute(ctx, wh.Name, prompt, wh.UserID, wh.SessionID, wh.Channel, wh.Metadata, false)

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
		go func() {
			ticker := time.NewTicker(dur)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					e.execute(ctx, hb.Name, hb.Prompt, hb.UserID, hb.SessionID, hb.Channel, hb.Metadata, true)
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

// Stop shuts down the cron scheduler.
func (e *Engine) Stop() {
	stopCtx := e.cron.Stop()
	<-stopCtx.Done()
}

// execute runs a single proactive job.
func (e *Engine) execute(ctx context.Context, name, prompt, userID, sessionID, chName string, metadata map[string]any, heartbeat bool) {
	if sessionID == "" {
		sessionID = "proactive-" + name
	}

	ch := e.channels.Channel(chName)
	if ch == nil {
		e.logger.Error("proactive job: channel not found", "name", name, "channel", chName)
		return
	}

	msg := &channel.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    userID,
		Content:   prompt,
		Channel:   chName,
		Metadata:  copyMeta(metadata),
	}

	var finalContent string
	err := e.handler.HandleMessage(ctx, msg, func(resp *channel.Response) error {
		if resp.Done && resp.Content != "" {
			finalContent = resp.Content
		}
		// For non-heartbeat jobs, send "done" responses immediately
		if !heartbeat && resp.Type == "done" && resp.Content != "" {
			return ch.Send(ctx, resp)
		}
		return nil
	})
	if err != nil {
		e.logger.Error("proactive job failed", "name", name, "error", err)
		return
	}

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
		if err := ch.Send(ctx, resp); err != nil {
			e.logger.Error("heartbeat send failed", "name", name, "error", err)
		}
	}

	e.logger.Info("proactive job completed", "name", name)
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
