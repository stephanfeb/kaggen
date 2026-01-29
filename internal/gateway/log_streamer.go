package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const ringBufferSize = 1000

// LogEntry represents a single log entry captured by the streamer.
type LogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

// LogStreamer captures slog output into a ring buffer and broadcasts to SSE clients.
type LogStreamer struct {
	mu      sync.RWMutex
	buffer  [ringBufferSize]*LogEntry
	pos     int
	count   int
	clients map[string]chan *LogEntry
}

// NewLogStreamer creates a new log streamer.
func NewLogStreamer() *LogStreamer {
	return &LogStreamer{
		clients: make(map[string]chan *LogEntry),
	}
}

// Write adds a log entry to the ring buffer and broadcasts to subscribers.
func (ls *LogStreamer) Write(entry *LogEntry) {
	ls.mu.Lock()
	ls.buffer[ls.pos] = entry
	ls.pos = (ls.pos + 1) % ringBufferSize
	if ls.count < ringBufferSize {
		ls.count++
	}

	// Copy client channels under lock to avoid race
	clients := make([]chan *LogEntry, 0, len(ls.clients))
	for _, ch := range ls.clients {
		clients = append(clients, ch)
	}
	ls.mu.Unlock()

	// Broadcast outside lock
	for _, ch := range clients {
		select {
		case ch <- entry:
		default:
			// Drop if client is slow
		}
	}
}

// Recent returns the last n log entries in chronological order.
func (ls *LogStreamer) Recent(n int) []*LogEntry {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	if n > ls.count {
		n = ls.count
	}
	if n == 0 {
		return nil
	}

	entries := make([]*LogEntry, n)
	start := (ls.pos - n + ringBufferSize) % ringBufferSize
	for i := 0; i < n; i++ {
		entries[i] = ls.buffer[(start+i)%ringBufferSize]
	}
	return entries
}

// Subscribe returns a channel that receives new log entries.
func (ls *LogStreamer) Subscribe(id string) <-chan *LogEntry {
	ch := make(chan *LogEntry, 64)
	ls.mu.Lock()
	ls.clients[id] = ch
	ls.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber.
func (ls *LogStreamer) Unsubscribe(id string) {
	ls.mu.Lock()
	if ch, ok := ls.clients[id]; ok {
		close(ch)
		delete(ls.clients, id)
	}
	ls.mu.Unlock()
}

// StreamerHandler is a slog.Handler that captures logs into a LogStreamer
// and chains to a next handler.
type StreamerHandler struct {
	streamer *LogStreamer
	next     slog.Handler
	attrs    []slog.Attr
	groups   []string
}

// NewStreamerHandler creates a slog handler that writes to both the streamer and the next handler.
func NewStreamerHandler(streamer *LogStreamer, next slog.Handler) *StreamerHandler {
	return &StreamerHandler{
		streamer: streamer,
		next:     next,
	}
}

// Enabled reports whether the handler handles records at the given level.
func (h *StreamerHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle processes a log record: captures it into the ring buffer and forwards to next.
func (h *StreamerHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(map[string]any)

	// Add pre-set attrs
	for _, a := range h.attrs {
		key := a.Key
		for _, g := range h.groups {
			key = g + "." + key
		}
		attrs[key] = a.Value.Any()
	}

	// Add record attrs
	r.Attrs(func(a slog.Attr) bool {
		key := a.Key
		for _, g := range h.groups {
			key = g + "." + key
		}
		attrs[key] = a.Value.Any()
		return true
	})

	entry := &LogEntry{
		Timestamp: r.Time,
		Level:     r.Level.String(),
		Message:   r.Message,
	}
	if len(attrs) > 0 {
		entry.Attrs = attrs
	}

	h.streamer.Write(entry)

	return h.next.Handle(ctx, r)
}

// WithAttrs returns a new handler with the given attributes.
func (h *StreamerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &StreamerHandler{
		streamer: h.streamer,
		next:     h.next.WithAttrs(attrs),
		attrs:    append(h.attrs, attrs...),
		groups:   h.groups,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *StreamerHandler) WithGroup(name string) slog.Handler {
	return &StreamerHandler{
		streamer: h.streamer,
		next:     h.next.WithGroup(name),
		attrs:    h.attrs,
		groups:   append(h.groups, name),
	}
}

// ServeSSE writes log entries as SSE events to the response writer.
// It first sends recent history, then streams new entries until the context is done.
func (ls *LogStreamer) ServeSSE(ctx context.Context, write func(data []byte) error, flush func()) error {
	// Send recent history
	recent := ls.Recent(200)
	for _, entry := range recent {
		data, _ := json.Marshal(entry)
		if err := write([]byte(fmt.Sprintf("data: %s\n\n", data))); err != nil {
			return err
		}
	}
	flush()

	// Subscribe and stream
	id := fmt.Sprintf("sse-%d", time.Now().UnixNano())
	ch := ls.Subscribe(id)
	defer ls.Unsubscribe(id)

	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-ch:
			if !ok {
				return nil
			}
			data, _ := json.Marshal(entry)
			if err := write([]byte(fmt.Sprintf("data: %s\n\n", data))); err != nil {
				return err
			}
			flush()
		}
	}
}
