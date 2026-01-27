package memory

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultAsyncMemoryNum   = 1
	defaultMemoryQueueSize  = 10
	defaultMemoryJobTimeout = 30 * time.Second
)

// memoryOperator defines the storage operations the auto worker calls.
type memoryOperator interface {
	ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error)
	AddMemory(ctx context.Context, userKey memory.UserKey, memory string, topics []string) error
	UpdateMemory(ctx context.Context, memoryKey memory.Key, memory string, topics []string) error
	DeleteMemory(ctx context.Context, memoryKey memory.Key) error
	ClearMemories(ctx context.Context, userKey memory.UserKey) error
}

type autoMemoryConfig struct {
	Extractor        extractor.MemoryExtractor
	AsyncMemoryNum   int
	MemoryQueueSize  int
	MemoryJobTimeout time.Duration
}

type autoMemoryJob struct {
	ctx      context.Context
	userKey  memory.UserKey
	sess     *session.Session
	latestTs time.Time
	messages []model.Message
}

// autoMemoryWorker manages async memory extraction workers.
type autoMemoryWorker struct {
	config   autoMemoryConfig
	operator memoryOperator
	logger   *slog.Logger
	jobChans []chan *autoMemoryJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

func newAutoMemoryWorker(config autoMemoryConfig, operator memoryOperator, logger *slog.Logger) *autoMemoryWorker {
	return &autoMemoryWorker{
		config:   config,
		operator: operator,
		logger:   logger,
	}
}

func (w *autoMemoryWorker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.config.Extractor == nil {
		return
	}
	num := w.config.AsyncMemoryNum
	if num <= 0 {
		num = defaultAsyncMemoryNum
	}
	queueSize := w.config.MemoryQueueSize
	if queueSize <= 0 {
		queueSize = defaultMemoryQueueSize
	}
	w.jobChans = make([]chan *autoMemoryJob, num)
	for i := 0; i < num; i++ {
		w.jobChans[i] = make(chan *autoMemoryJob, queueSize)
	}
	w.wg.Add(num)
	for _, ch := range w.jobChans {
		go func(ch chan *autoMemoryJob) {
			defer w.wg.Done()
			for job := range ch {
				w.processJob(job)
			}
		}(ch)
	}
	w.started = true
}

func (w *autoMemoryWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || len(w.jobChans) == 0 {
		return
	}
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.wg.Wait()
	w.jobChans = nil
	w.started = false
}

// EnqueueJob enqueues a session for async memory extraction.
func (w *autoMemoryWorker) EnqueueJob(ctx context.Context, sess *session.Session) error {
	if w.config.Extractor == nil || sess == nil {
		return nil
	}
	userKey := memory.UserKey{AppName: sess.AppName, UserID: sess.UserID}
	if userKey.AppName == "" || userKey.UserID == "" {
		return nil
	}

	since := readLastExtractAt(sess)
	latestTs, messages := scanDeltaSince(sess, since)
	if len(messages) == 0 {
		return nil
	}

	var lastExtractAtPtr *time.Time
	if !since.IsZero() {
		sinceUTC := since.UTC()
		lastExtractAtPtr = &sinceUTC
	}
	extractCtx := &extractor.ExtractionContext{
		UserKey:       userKey,
		Messages:      messages,
		LastExtractAt: lastExtractAtPtr,
	}

	if !w.config.Extractor.ShouldExtract(extractCtx) {
		return nil
	}

	job := &autoMemoryJob{
		ctx:      context.WithoutCancel(ctx),
		userKey:  userKey,
		sess:     sess,
		latestTs: latestTs,
		messages: messages,
	}
	if w.tryEnqueue(ctx, userKey, job) {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	// Queue full — process synchronously.
	timeout := w.config.MemoryJobTimeout
	if timeout <= 0 {
		timeout = defaultMemoryJobTimeout
	}
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := w.createAutoMemory(syncCtx, userKey, messages); err != nil {
		return err
	}
	writeLastExtractAt(sess, latestTs)
	return nil
}

func (w *autoMemoryWorker) tryEnqueue(ctx context.Context, userKey memory.UserKey, job *autoMemoryJob) bool {
	if ctx.Err() != nil {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}
	h := fnv.New32a()
	h.Write([]byte(userKey.AppName))
	h.Write([]byte(userKey.UserID))
	index := int(h.Sum32()) % len(w.jobChans)
	select {
	case w.jobChans[index] <- job:
		return true
	default:
		return false
	}
}

func (w *autoMemoryWorker) processJob(job *autoMemoryJob) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("panic in memory worker", "error", fmt.Sprintf("%v", r))
		}
	}()
	ctx := job.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := w.config.MemoryJobTimeout
	if timeout <= 0 {
		timeout = defaultMemoryJobTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := w.createAutoMemory(ctx, job.userKey, job.messages); err != nil {
		w.logger.Warn("auto_memory: job failed",
			"app", job.userKey.AppName, "user", job.userKey.UserID, "error", err)
		return
	}
	writeLastExtractAt(job.sess, job.latestTs)
}

func (w *autoMemoryWorker) createAutoMemory(ctx context.Context, userKey memory.UserKey, messages []model.Message) error {
	if w.config.Extractor == nil {
		return nil
	}
	existing, err := w.operator.ReadMemories(ctx, userKey, 0)
	if err != nil {
		w.logger.Warn("auto_memory: failed to read existing memories", "error", err)
		existing = nil
	}

	ops, err := w.config.Extractor.Extract(ctx, messages, existing)
	if err != nil {
		return fmt.Errorf("auto_memory: extract failed: %w", err)
	}

	for _, op := range ops {
		w.executeOp(ctx, userKey, op)
	}
	return nil
}

func (w *autoMemoryWorker) executeOp(ctx context.Context, userKey memory.UserKey, op *extractor.Operation) {
	switch op.Type {
	case extractor.OperationAdd:
		if err := w.operator.AddMemory(ctx, userKey, op.Memory, op.Topics); err != nil {
			w.logger.Warn("auto_memory: add failed", "error", err)
		}
	case extractor.OperationUpdate:
		key := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID}
		if err := w.operator.UpdateMemory(ctx, key, op.Memory, op.Topics); err != nil {
			w.logger.Warn("auto_memory: update failed", "error", err)
		}
	case extractor.OperationDelete:
		key := memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID}
		if err := w.operator.DeleteMemory(ctx, key); err != nil {
			w.logger.Warn("auto_memory: delete failed", "error", err)
		}
	case extractor.OperationClear:
		if err := w.operator.ClearMemories(ctx, userKey); err != nil {
			w.logger.Warn("auto_memory: clear failed", "error", err)
		}
	default:
		w.logger.Warn("auto_memory: unknown operation", "type", op.Type)
	}
}

// readLastExtractAt reads the last extraction timestamp from session state.
func readLastExtractAt(sess *session.Session) time.Time {
	raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return ts
}

// writeLastExtractAt writes the last extraction timestamp to session state.
func writeLastExtractAt(sess *session.Session, ts time.Time) {
	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)))
}

// scanDeltaSince scans session events since the given timestamp, returning messages.
func scanDeltaSince(sess *session.Session, since time.Time) (time.Time, []model.Message) {
	var latestTs time.Time
	var messages []model.Message
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for _, e := range sess.Events {
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		if e.Timestamp.After(latestTs) {
			latestTs = e.Timestamp
		}
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message
			if msg.Role == model.RoleTool || msg.ToolID != "" {
				continue
			}
			if msg.Content == "" && len(msg.ContentParts) == 0 {
				continue
			}
			if len(msg.ToolCalls) > 0 {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return latestTs, messages
}
