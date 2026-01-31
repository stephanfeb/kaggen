// Command kaggen-pubsub-bridge is a standalone sidecar that subscribes to a
// GCP Pub/Sub subscription and forwards messages to kaggen's callback endpoint.
//
// Usage:
//
//	kaggen-pubsub-bridge --project my-project --subscription kaggen-callbacks
//	kaggen-pubsub-bridge --callback-url http://localhost:18789
//
// Environment variables:
//
//	GOOGLE_CLOUD_PROJECT  — GCP project ID (overridden by --project)
//	PUBSUB_SUBSCRIPTION   — subscription name (overridden by --subscription)
//	CALLBACK_URL          — callback base URL (overridden by --callback-url)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yourusername/kaggen/internal/pubsub"
)

func main() {
	project := flag.String("project", envOrDefault("GOOGLE_CLOUD_PROJECT", ""), "GCP project ID")
	subscription := flag.String("subscription", envOrDefault("PUBSUB_SUBSCRIPTION", ""), "Pub/Sub subscription name")
	callbackURL := flag.String("callback-url", envOrDefault("CALLBACK_URL", "http://localhost:18789"), "Kaggen callback base URL")
	flag.Parse()

	if *project == "" {
		fmt.Fprintln(os.Stderr, "error: --project or GOOGLE_CLOUD_PROJECT required")
		os.Exit(1)
	}
	if *subscription == "" {
		fmt.Fprintln(os.Stderr, "error: --subscription or PUBSUB_SUBSCRIPTION required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("shutting down")
		cancel()
	}()

	bridge := pubsub.NewBridge(*project, *subscription, *callbackURL, logger)
	if err := bridge.Start(ctx); err != nil {
		logger.Error("bridge failed", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
