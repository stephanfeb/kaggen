// Package pubsub provides a bridge that subscribes to a GCP Pub/Sub
// subscription and forwards messages to kaggen's /callbacks/{taskID} endpoint.
package pubsub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"cloud.google.com/go/pubsub"
)

// Bridge subscribes to a GCP Pub/Sub subscription and forwards messages
// to a local callback endpoint.
type Bridge struct {
	projectID    string
	subscription string
	callbackURL  string // base URL, e.g. "http://localhost:18789"
	logger       *slog.Logger

	client     *pubsub.Client
	httpClient *http.Client
	cancel     context.CancelFunc
}

// NewBridge creates a new Pub/Sub bridge.
func NewBridge(projectID, subscription, callbackURL string, logger *slog.Logger) *Bridge {
	return &Bridge{
		projectID:    projectID,
		subscription: subscription,
		callbackURL:  callbackURL,
		logger:       logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Start connects to Pub/Sub and begins pulling messages. It blocks until
// the context is cancelled or an unrecoverable error occurs.
func (b *Bridge) Start(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	client, err := pubsub.NewClient(ctx, b.projectID)
	if err != nil {
		return fmt.Errorf("create pubsub client: %w", err)
	}
	b.client = client

	sub := client.Subscription(b.subscription)

	// Verify the subscription exists.
	exists, err := sub.Exists(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("check subscription: %w", err)
	}
	if !exists {
		client.Close()
		return fmt.Errorf("subscription %q does not exist in project %q", b.subscription, b.projectID)
	}

	b.logger.Info("pubsub bridge started",
		"project", b.projectID,
		"subscription", b.subscription,
		"callback_url", b.callbackURL)

	// Receive blocks until ctx is cancelled.
	err = sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		b.handleMessage(ctx, msg)
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("pubsub receive: %w", err)
	}
	return nil
}

// Stop terminates the bridge.
func (b *Bridge) Stop() error {
	if b.cancel != nil {
		b.cancel()
	}
	if b.client != nil {
		return b.client.Close()
	}
	return nil
}

// handleMessage extracts the task_id from a Pub/Sub message and forwards
// the payload to the callback endpoint.
func (b *Bridge) handleMessage(_ context.Context, msg *pubsub.Message) {
	taskID := extractTaskID(msg)
	if taskID == "" {
		b.logger.Warn("pubsub message missing task_id, discarding",
			"message_id", msg.ID)
		msg.Ack()
		return
	}

	callbackURL := fmt.Sprintf("%s/callbacks/%s", b.callbackURL, taskID)

	resp, err := b.httpClient.Post(callbackURL, "application/json", bytes.NewReader(msg.Data))
	if err != nil {
		b.logger.Warn("callback request failed, nacking",
			"task_id", taskID, "error", err)
		msg.Nack()
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode == http.StatusOK:
		b.logger.Info("callback delivered", "task_id", taskID)
		msg.Ack()
	case resp.StatusCode == http.StatusNotFound:
		// Unknown task — discard to avoid infinite retry.
		b.logger.Warn("unknown task, acking to discard", "task_id", taskID)
		msg.Ack()
	case resp.StatusCode == http.StatusConflict:
		// Task already completed — discard.
		b.logger.Info("task already finished, acking", "task_id", taskID)
		msg.Ack()
	default:
		// Server error — nack for retry.
		b.logger.Warn("callback returned error, nacking",
			"task_id", taskID, "status", resp.StatusCode)
		msg.Nack()
	}
}

// extractTaskID gets the task_id from the message. It checks the Pub/Sub
// message attributes first, then falls back to parsing the JSON body.
func extractTaskID(msg *pubsub.Message) string {
	// Check attributes first (preferred — avoids parsing).
	if id, ok := msg.Attributes["task_id"]; ok && id != "" {
		return id
	}

	// Fall back to JSON body.
	var envelope struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(msg.Data, &envelope) == nil && envelope.TaskID != "" {
		return envelope.TaskID
	}

	return ""
}
