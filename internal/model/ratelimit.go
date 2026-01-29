// Package model provides a concurrency-limiting wrapper for model.Model adapters.
package model

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RateLimitedModel wraps a model.Model with a semaphore to limit concurrent
// API calls. This prevents rate-limit errors when multiple goroutines
// (sub-agents, async dispatch, proactive jobs, memory extraction) all make
// LLM calls simultaneously.
type RateLimitedModel struct {
	inner model.Model
	sem   chan struct{}
}

// NewRateLimitedModel wraps inner with a concurrency limit of maxConcurrent.
// If maxConcurrent <= 0, the inner model is returned unwrapped.
func NewRateLimitedModel(inner model.Model, maxConcurrent int) model.Model {
	if maxConcurrent <= 0 {
		return inner
	}
	return &RateLimitedModel{
		inner: inner,
		sem:   make(chan struct{}, maxConcurrent),
	}
}

// GenerateContent acquires a semaphore slot before delegating to the inner
// model. The slot is released when the returned response channel is closed.
func (m *RateLimitedModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	// Acquire slot, respecting context cancellation.
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	innerCh, err := m.inner.GenerateContent(ctx, req)
	if err != nil {
		<-m.sem // release on error
		return nil, err
	}

	// Proxy the channel and release the slot when the inner channel closes.
	out := make(chan *model.Response, 1)
	go func() {
		defer func() { <-m.sem }()
		defer close(out)
		for resp := range innerCh {
			out <- resp
		}
	}()

	return out, nil
}

// Info delegates to the inner model.
func (m *RateLimitedModel) Info() model.Info {
	return m.inner.Info()
}
