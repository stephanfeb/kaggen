// Package model provides model wrappers including usage tracking.
package model

import (
	"context"
	"log/slog"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// usageKey is the context key for usage tracking.
type usageKey struct{}

// trackingIDKey is the context key for the tracking ID.
type trackingIDKey struct{}

// UsageAccumulator tracks token usage across multiple LLM calls.
type UsageAccumulator struct {
	mu     sync.Mutex
	Input  int
	Output int
	Total  int
}

// Add adds usage from a response to the accumulator.
func (u *UsageAccumulator) Add(usage *model.Usage) {
	if usage == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Input += usage.PromptTokens
	u.Output += usage.CompletionTokens
	u.Total += usage.TotalTokens
}

// Get returns the accumulated usage values.
func (u *UsageAccumulator) Get() (input, output, total int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.Input, u.Output, u.Total
}

// Reset clears the accumulated usage.
func (u *UsageAccumulator) Reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Input = 0
	u.Output = 0
	u.Total = 0
}

// WithUsageTracking returns a new context with a fresh UsageAccumulator.
// Use GetUsageAccumulator to retrieve accumulated usage after calls complete.
func WithUsageTracking(ctx context.Context) context.Context {
	return context.WithValue(ctx, usageKey{}, &UsageAccumulator{})
}

// GetUsageAccumulator retrieves the UsageAccumulator from context, if present.
func GetUsageAccumulator(ctx context.Context) *UsageAccumulator {
	if acc, ok := ctx.Value(usageKey{}).(*UsageAccumulator); ok {
		return acc
	}
	return nil
}

// WithTrackingID returns a context with a tracking ID for usage accumulation.
func WithTrackingID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, trackingIDKey{}, id)
}

// GetTrackingID retrieves the tracking ID from context, if present.
func GetTrackingID(ctx context.Context) string {
	if id, ok := ctx.Value(trackingIDKey{}).(string); ok {
		return id
	}
	return ""
}

// globalUsageRegistry tracks usage by ID for cases where context doesn't propagate.
var globalUsageRegistry = &usageRegistry{
	accumulators: make(map[string]*UsageAccumulator),
}

type usageRegistry struct {
	mu           sync.RWMutex
	accumulators map[string]*UsageAccumulator
}

// RegisterTracking creates a new accumulator for the given ID.
func RegisterTracking(id string) {
	globalUsageRegistry.mu.Lock()
	defer globalUsageRegistry.mu.Unlock()
	globalUsageRegistry.accumulators[id] = &UsageAccumulator{}
}

// GetTrackedUsage retrieves and removes the accumulated usage for an ID.
func GetTrackedUsage(id string) (input, output, total int) {
	globalUsageRegistry.mu.Lock()
	defer globalUsageRegistry.mu.Unlock()
	if acc, ok := globalUsageRegistry.accumulators[id]; ok {
		input, output, total = acc.Get()
		delete(globalUsageRegistry.accumulators, id)
	}
	return
}

// addToTracking adds usage to a tracked accumulator by ID.
func addToTracking(id string, usage *model.Usage) {
	if usage == nil || id == "" {
		return
	}
	globalUsageRegistry.mu.RLock()
	acc, ok := globalUsageRegistry.accumulators[id]
	globalUsageRegistry.mu.RUnlock()
	if ok {
		acc.Add(usage)
	}
}

// addToAllActiveTracking adds usage to ALL active accumulators in the registry.
// This is used when we can't determine which specific tracking ID to use.
func addToAllActiveTracking(usage *model.Usage) {
	if usage == nil {
		return
	}
	globalUsageRegistry.mu.RLock()
	defer globalUsageRegistry.mu.RUnlock()
	for _, acc := range globalUsageRegistry.accumulators {
		acc.Add(usage)
	}
}

// hasActiveTracking returns true if there are any active tracking IDs.
func hasActiveTracking() bool {
	globalUsageRegistry.mu.RLock()
	defer globalUsageRegistry.mu.RUnlock()
	return len(globalUsageRegistry.accumulators) > 0
}

// UsageTrackingModel wraps a model.Model and accumulates token usage.
// It supports two tracking modes:
// 1. Context-based: if UsageAccumulator is in context, accumulates there
// 2. ID-based: if a tracking ID is registered, accumulates to the global registry
type UsageTrackingModel struct {
	inner  model.Model
	logger *slog.Logger

	// Fallback: track the most recent active call for cases where context doesn't propagate
	activeIDMu sync.RWMutex
	activeID   string
}

// NewUsageTrackingModel wraps inner with usage tracking capability.
func NewUsageTrackingModel(inner model.Model) *UsageTrackingModel {
	return &UsageTrackingModel{inner: inner}
}

// NewUsageTrackingModelWithLogger wraps inner with usage tracking and logging.
func NewUsageTrackingModelWithLogger(inner model.Model, logger *slog.Logger) *UsageTrackingModel {
	return &UsageTrackingModel{inner: inner, logger: logger}
}

// SetActiveTrackingID sets the active tracking ID for calls that don't propagate context.
// This should be called before executing a sub-agent and cleared after.
func (m *UsageTrackingModel) SetActiveTrackingID(id string) {
	m.activeIDMu.Lock()
	defer m.activeIDMu.Unlock()
	m.activeID = id
}

// ClearActiveTrackingID clears the active tracking ID.
func (m *UsageTrackingModel) ClearActiveTrackingID() {
	m.activeIDMu.Lock()
	defer m.activeIDMu.Unlock()
	m.activeID = ""
}

// GenerateContent delegates to the inner model and accumulates usage.
func (m *UsageTrackingModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	innerCh, err := m.inner.GenerateContent(ctx, req)
	if err != nil {
		return nil, err
	}

	// Determine where to accumulate usage
	acc := GetUsageAccumulator(ctx)
	trackingID := GetTrackingID(ctx)

	// Fallback to active tracking ID if context-based tracking isn't available
	if acc == nil && trackingID == "" {
		m.activeIDMu.RLock()
		trackingID = m.activeID
		m.activeIDMu.RUnlock()
	}

	// Check if there's any active tracking in the global registry
	// This handles cases where context doesn't propagate but we have registered tracking
	useGlobalTracking := hasActiveTracking()

	// If no tracking mechanism available, pass through unchanged
	if acc == nil && trackingID == "" && !useGlobalTracking {
		return innerCh, nil
	}

	// Proxy the channel and accumulate usage from responses
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		for resp := range innerCh {
			if resp.Usage != nil {
				// Log usage for debugging
				if m.logger != nil {
					m.logger.Debug("UsageTrackingModel: captured usage",
						"input", resp.Usage.PromptTokens,
						"output", resp.Usage.CompletionTokens,
						"total", resp.Usage.TotalTokens,
						"tracking_id", trackingID,
						"has_ctx_acc", acc != nil,
						"use_global", useGlobalTracking)
				}

				// Accumulate to context-based accumulator
				if acc != nil {
					acc.Add(resp.Usage)
				}

				// Accumulate to ID-based registry (specific ID or all active)
				if trackingID != "" {
					addToTracking(trackingID, resp.Usage)
				} else if useGlobalTracking {
					// Fallback: add to all active tracking IDs
					// This handles cases where context doesn't propagate
					addToAllActiveTracking(resp.Usage)
				}
			}
			out <- resp
		}
	}()

	return out, nil
}

// Info delegates to the inner model.
func (m *UsageTrackingModel) Info() model.Info {
	return m.inner.Info()
}

// Inner returns the wrapped model (useful for unwrapping).
func (m *UsageTrackingModel) Inner() model.Model {
	return m.inner
}
