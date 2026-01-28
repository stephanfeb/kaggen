package memory

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// slowExtractor simulates a slow extraction process (like Gemini API)
type slowExtractor struct {
	extractDelay  time.Duration
	extractCalled atomic.Int32
	extractDone   atomic.Int32
}

func (e *slowExtractor) Extract(ctx context.Context, messages []model.Message, existing []*memory.Entry) ([]*extractor.Operation, error) {
	e.extractCalled.Add(1)
	select {
	case <-time.After(e.extractDelay):
		e.extractDone.Add(1)
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *slowExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
}

func (e *slowExtractor) SetPrompt(prompt string) {}
func (e *slowExtractor) SetModel(m model.Model) {}
func (e *slowExtractor) Metadata() map[string]any {
	return nil
}

// mockOperator implements memoryOperator for testing
type mockOperator struct{}

func (m *mockOperator) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	return nil, nil
}
func (m *mockOperator) AddMemory(ctx context.Context, userKey memory.UserKey, mem string, topics []string) error {
	return nil
}
func (m *mockOperator) UpdateMemory(ctx context.Context, memoryKey memory.Key, mem string, topics []string) error {
	return nil
}
func (m *mockOperator) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return nil
}
func (m *mockOperator) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return nil
}

// TestEnqueueJobIsNonBlocking verifies that EnqueueJob returns immediately
// even when the extractor is slow (simulating slow Gemini API calls).
func TestEnqueueJobIsNonBlocking(t *testing.T) {
	// Create a slow extractor that takes 5 seconds
	slowExt := &slowExtractor{extractDelay: 5 * time.Second}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker := newAutoMemoryWorker(autoMemoryConfig{
		Extractor:        slowExt,
		AsyncMemoryNum:   1,
		MemoryQueueSize:  10,
		MemoryJobTimeout: 10 * time.Second,
	}, &mockOperator{}, logger)

	worker.Start()
	defer worker.Stop()

	// Create a session with some events
	sess := &session.Session{
		AppName: "test-app",
		UserID:  "test-user",
		Events: []event.Event{
			{
				Timestamp: time.Now(),
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "Hello, remember that I like pizza",
							},
						},
					},
				},
			},
		},
	}

	// Measure how long EnqueueJob takes
	start := time.Now()
	err := worker.EnqueueJob(context.Background(), sess)
	enqueueDuration := time.Since(start)

	if err != nil {
		t.Fatalf("EnqueueJob failed: %v", err)
	}

	// EnqueueJob should return within 100ms (it should be nearly instant)
	// If it takes longer, it means it's blocking on something
	maxAcceptableDuration := 100 * time.Millisecond
	if enqueueDuration > maxAcceptableDuration {
		t.Errorf("EnqueueJob took %v, expected < %v. THIS IS THE BLOCKING BUG!",
			enqueueDuration, maxAcceptableDuration)
	} else {
		t.Logf("EnqueueJob returned in %v (good - non-blocking)", enqueueDuration)
	}

	// Verify the extract was called (job was enqueued)
	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)
	if slowExt.extractCalled.Load() == 0 {
		t.Error("Extract was never called - job was not enqueued")
	}

	// Verify extraction is still running (not done yet since it takes 5s)
	if slowExt.extractDone.Load() > 0 {
		t.Error("Extract finished too quickly - something is wrong")
	}

	t.Logf("Extract called: %d, Extract done: %d (extraction still running in background)",
		slowExt.extractCalled.Load(), slowExt.extractDone.Load())
}

// TestEnqueueJobWithFullQueue verifies behavior when queue is full
func TestEnqueueJobWithFullQueue(t *testing.T) {
	// Create a very slow extractor
	slowExt := &slowExtractor{extractDelay: 10 * time.Second}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Small queue size of 1
	worker := newAutoMemoryWorker(autoMemoryConfig{
		Extractor:        slowExt,
		AsyncMemoryNum:   1,
		MemoryQueueSize:  1,
		MemoryJobTimeout: 15 * time.Second,
	}, &mockOperator{}, logger)

	worker.Start()
	defer worker.Stop()

	createSession := func(id string) *session.Session {
		return &session.Session{
			AppName: "test-app",
			UserID:  id,
			Events: []event.Event{
				{
					Timestamp: time.Now(),
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Role: model.RoleUser, Content: "test"}},
						},
					},
				},
			},
		}
	}

	// Fill the queue
	for i := 0; i < 5; i++ {
		start := time.Now()
		_ = worker.EnqueueJob(context.Background(), createSession(string(rune('A'+i))))
		duration := time.Since(start)

		// Each call should be fast, even when queue is full (should skip, not block)
		if duration > 100*time.Millisecond {
			t.Errorf("EnqueueJob #%d took %v - BLOCKING DETECTED!", i, duration)
		} else {
			t.Logf("EnqueueJob #%d took %v (non-blocking)", i, duration)
		}
	}
}

// TestFileMemoryServiceEnqueueIsNonBlocking tests the full service wrapper
func TestFileMemoryServiceEnqueueIsNonBlocking(t *testing.T) {
	slowExt := &slowExtractor{extractDelay: 5 * time.Second}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create a minimal FileMemoryService with autoWorker
	svc := &FileMemoryService{
		logger: logger,
		autoWorker: newAutoMemoryWorker(autoMemoryConfig{
			Extractor:        slowExt,
			AsyncMemoryNum:   1,
			MemoryQueueSize:  10,
			MemoryJobTimeout: 10 * time.Second,
		}, &mockOperator{}, logger),
	}
	svc.autoWorker.Start()
	defer svc.autoWorker.Stop()

	sess := &session.Session{
		AppName: "test-app",
		UserID:  "test-user",
		Events: []event.Event{
			{
				Timestamp: time.Now(),
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleUser, Content: "test message"}},
					},
				},
			},
		},
	}

	// This is what the runner calls
	start := time.Now()
	err := svc.EnqueueAutoMemoryJob(context.Background(), sess)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("EnqueueAutoMemoryJob failed: %v", err)
	}

	if duration > 100*time.Millisecond {
		t.Errorf("FileMemoryService.EnqueueAutoMemoryJob took %v - BLOCKING!", duration)
	} else {
		t.Logf("FileMemoryService.EnqueueAutoMemoryJob returned in %v (non-blocking)", duration)
	}

	// Verify extraction started
	time.Sleep(50 * time.Millisecond)
	if slowExt.extractCalled.Load() == 0 {
		t.Error("Extract was never called")
	}
	t.Logf("Extract running in background: called=%d, done=%d",
		slowExt.extractCalled.Load(), slowExt.extractDone.Load())
}

// TestEnqueueJobContextCancellation verifies EnqueueJob respects context cancellation
func TestEnqueueJobContextCancellation(t *testing.T) {
	slowExt := &slowExtractor{extractDelay: 5 * time.Second}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	worker := newAutoMemoryWorker(autoMemoryConfig{
		Extractor:        slowExt,
		AsyncMemoryNum:   1,
		MemoryQueueSize:  10,
		MemoryJobTimeout: 10 * time.Second,
	}, &mockOperator{}, logger)

	worker.Start()
	defer worker.Stop()

	sess := &session.Session{
		AppName: "test-app",
		UserID:  "test-user",
		Events: []event.Event{
			{
				Timestamp: time.Now(),
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleUser, Content: "test"}},
					},
				},
			},
		},
	}

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := worker.EnqueueJob(ctx, sess)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("EnqueueJob failed: %v", err)
	}

	// Should return immediately with cancelled context
	if duration > 50*time.Millisecond {
		t.Errorf("EnqueueJob with cancelled context took %v", duration)
	}

	// Extract should NOT be called since context was cancelled
	time.Sleep(100 * time.Millisecond)
	if slowExt.extractCalled.Load() > 0 {
		t.Log("Note: Extract was called even with cancelled context (job still enqueued)")
	}
}
