package model

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockModel is a test model that returns configured responses.
type mockModel struct {
	responses []*model.Response
	callCount int
}

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, len(m.responses))
	for _, resp := range m.responses {
		ch <- resp
	}
	close(ch)
	m.callCount++
	return ch, nil
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: "mock"}
}

func TestUsageAccumulator(t *testing.T) {
	acc := &UsageAccumulator{}

	// Add first usage
	acc.Add(&model.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	})

	input, output, total := acc.Get()
	if input != 100 || output != 50 || total != 150 {
		t.Errorf("expected 100/50/150, got %d/%d/%d", input, output, total)
	}

	// Add second usage
	acc.Add(&model.Usage{
		PromptTokens:     200,
		CompletionTokens: 100,
		TotalTokens:      300,
	})

	input, output, total = acc.Get()
	if input != 300 || output != 150 || total != 450 {
		t.Errorf("expected 300/150/450, got %d/%d/%d", input, output, total)
	}

	// Test nil usage (should not panic)
	acc.Add(nil)
	input, output, total = acc.Get()
	if input != 300 || output != 150 || total != 450 {
		t.Errorf("nil add should not change values, got %d/%d/%d", input, output, total)
	}

	// Test reset
	acc.Reset()
	input, output, total = acc.Get()
	if input != 0 || output != 0 || total != 0 {
		t.Errorf("reset should zero values, got %d/%d/%d", input, output, total)
	}
}

func TestWithUsageTracking(t *testing.T) {
	ctx := context.Background()

	// No accumulator in base context
	if acc := GetUsageAccumulator(ctx); acc != nil {
		t.Error("expected nil accumulator in base context")
	}

	// Add tracking
	trackingCtx := WithUsageTracking(ctx)
	acc := GetUsageAccumulator(trackingCtx)
	if acc == nil {
		t.Fatal("expected non-nil accumulator in tracking context")
	}

	// Should be empty initially
	input, output, total := acc.Get()
	if input != 0 || output != 0 || total != 0 {
		t.Errorf("expected 0/0/0, got %d/%d/%d", input, output, total)
	}
}

func TestUsageTrackingModel(t *testing.T) {
	// Create mock model with usage in response
	mock := &mockModel{
		responses: []*model.Response{
			{
				Usage: &model.Usage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
		},
	}

	tracked := NewUsageTrackingModel(mock)

	// Call without tracking context - should pass through
	ctx := context.Background()
	ch, err := tracked.GenerateContent(ctx, &model.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	// Call with tracking context - should accumulate
	trackingCtx := WithUsageTracking(ctx)
	mock.responses = []*model.Response{
		{
			Usage: &model.Usage{
				PromptTokens:     200,
				CompletionTokens: 100,
				TotalTokens:      300,
			},
		},
	}
	ch, err = tracked.GenerateContent(trackingCtx, &model.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	// Check accumulated usage
	acc := GetUsageAccumulator(trackingCtx)
	if acc == nil {
		t.Fatal("expected accumulator")
	}
	input, output, total := acc.Get()
	if input != 200 || output != 100 || total != 300 {
		t.Errorf("expected 200/100/300, got %d/%d/%d", input, output, total)
	}
}

func TestUsageTrackingModelMultipleCalls(t *testing.T) {
	mock := &mockModel{}
	tracked := NewUsageTrackingModel(mock)

	// Use same tracking context for multiple calls
	trackingCtx := WithUsageTracking(context.Background())

	// First call
	mock.responses = []*model.Response{
		{
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		},
	}
	ch, _ := tracked.GenerateContent(trackingCtx, &model.Request{})
	for range ch {
	}

	// Second call
	mock.responses = []*model.Response{
		{
			Usage: &model.Usage{
				PromptTokens:     200,
				CompletionTokens: 100,
				TotalTokens:      300,
			},
		},
	}
	ch, _ = tracked.GenerateContent(trackingCtx, &model.Request{})
	for range ch {
	}

	// Should have accumulated both
	acc := GetUsageAccumulator(trackingCtx)
	input, output, total := acc.Get()
	if input != 300 || output != 150 || total != 450 {
		t.Errorf("expected accumulated 300/150/450, got %d/%d/%d", input, output, total)
	}
}

func TestIDBasedTracking(t *testing.T) {
	mock := &mockModel{
		responses: []*model.Response{
			{
				Usage: &model.Usage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
		},
	}

	tracked := NewUsageTrackingModel(mock)

	// Register tracking by ID
	trackingID := "test-task-123"
	RegisterTracking(trackingID)

	// Call with tracking ID in context
	ctx := WithTrackingID(context.Background(), trackingID)
	ch, err := tracked.GenerateContent(ctx, &model.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	// Retrieve tracked usage
	input, output, total := GetTrackedUsage(trackingID)
	if input != 100 || output != 50 || total != 150 {
		t.Errorf("expected 100/50/150, got %d/%d/%d", input, output, total)
	}

	// Second retrieval should return zeros (ID was cleaned up)
	input, output, total = GetTrackedUsage(trackingID)
	if input != 0 || output != 0 || total != 0 {
		t.Errorf("expected 0/0/0 after cleanup, got %d/%d/%d", input, output, total)
	}
}

func TestGlobalTrackingFallback(t *testing.T) {
	mock := &mockModel{
		responses: []*model.Response{
			{
				Usage: &model.Usage{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
		},
	}

	tracked := NewUsageTrackingModel(mock)

	// Register tracking by ID (simulating BeforeTool callback)
	trackingID := "test-fallback-456"
	RegisterTracking(trackingID)

	// Call WITHOUT tracking ID in context (simulating context not propagating)
	// The global registry should still catch the usage
	ctx := context.Background()
	ch, err := tracked.GenerateContent(ctx, &model.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range ch {
	}

	// Retrieve tracked usage - should have been accumulated via global fallback
	input, output, total := GetTrackedUsage(trackingID)
	if input != 100 || output != 50 || total != 150 {
		t.Errorf("expected 100/50/150 via global fallback, got %d/%d/%d", input, output, total)
	}
}
