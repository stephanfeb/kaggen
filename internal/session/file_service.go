package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
)

// SessionMetadata holds human-readable session info persisted in metadata.json.
type SessionMetadata struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ArchivedAt time.Time `json:"archived_at,omitempty"`
	Channel    string    `json:"channel,omitempty"`
	UserID     string    `json:"user_id"`
	// Thread fields — set when this session is a forked thread.
	ParentSessionID string `json:"parent_session_id,omitempty"`
	ParentEventID   string `json:"parent_event_id,omitempty"`
	IsThread        bool   `json:"is_thread,omitempty"`
}

// ContextCache stores the last LLM messages array for fast session reload.
type ContextCache struct {
	Messages   []model.Message `json:"messages"`
	UpdatedAt  time.Time       `json:"updated_at"`
	TokenCount int             `json:"token_count,omitempty"`
}

// CompactionHook is called before session compaction to allow memory extraction.
// This prevents data loss when context is compacted before async extraction completes.
type CompactionHook interface {
	// BeforeCompaction is called with the events about to be removed from the session.
	// The hook should extract any important information (e.g., memories) before they are lost.
	BeforeCompaction(ctx context.Context, sess *trpcsession.Session, eventsToRemove []event.Event) error
}

// FileService implements trpc-agent-go's session.Service using the local filesystem.
//
// Storage layout:
//
//	<dataDir>/
//	  <appName>/<userID>/<sessionID>/
//	    events.jsonl   – append-only event log
//	    state.json     – session-level state
//	    metadata.json  – session metadata (name, timestamps, channel)
//	    context.json   – cached LLM context (messages array)
//	  <appName>/
//	    app_state.json – app-scoped state
//	  <appName>/<userID>/
//	    user_state.json – user-scoped state
type FileService struct {
	dataDir        string
	mu             sync.RWMutex
	model          model.Model        // LLM for session summarization (optional, set via SetModel)
	namer          *SessionNamer      // Session auto-naming (optional, set via SetNamer)
	compactionHook CompactionHook     // Pre-compaction memory flush (optional)
	logger         *slog.Logger
}

// compactKeepEvents is the number of recent events to keep after compaction.
const compactKeepEvents = 20

// Compile-time check.
var _ trpcsession.Service = (*FileService)(nil)

// NewFileService creates a new file-backed session service rooted at dataDir.
func NewFileService(dataDir string) *FileService {
	return &FileService{
		dataDir: dataDir,
		logger:  slog.Default(),
	}
}

// SetNamer sets the session auto-namer.
func (s *FileService) SetNamer(n *SessionNamer) {
	s.namer = n
}

// SetLogger sets the logger for the file service.
func (s *FileService) SetLogger(l *slog.Logger) {
	s.logger = l
}

// sessionDir returns the directory for a specific session.
func (s *FileService) sessionDir(key trpcsession.Key) string {
	return filepath.Join(s.dataDir, key.AppName, key.UserID, key.SessionID)
}

// eventsPath returns the path to the events JSONL file for a session.
func (s *FileService) eventsPath(key trpcsession.Key) string {
	return filepath.Join(s.sessionDir(key), "events.jsonl")
}

// summaryPath returns the path to the summary JSON file for a session.
func (s *FileService) summaryPath(key trpcsession.Key) string {
	return filepath.Join(s.sessionDir(key), "summary.json")
}

// metadataPath returns the path to the metadata JSON file for a session.
func (s *FileService) metadataPath(key trpcsession.Key) string {
	return filepath.Join(s.sessionDir(key), "metadata.json")
}

// contextCachePath returns the path to the context cache JSON file for a session.
func (s *FileService) contextCachePath(key trpcsession.Key) string {
	return filepath.Join(s.sessionDir(key), "context.json")
}

// SetModel sets the LLM model used for session summarization (/compact).
func (s *FileService) SetModel(m model.Model) {
	s.model = m
}

// SetCompactionHook sets the hook called before session compaction.
// Use this to ensure memories are extracted before events are deleted.
func (s *FileService) SetCompactionHook(h CompactionHook) {
	s.compactionHook = h
}

// ReadMetadata reads the session metadata from disk. Returns nil if not found.
func (s *FileService) ReadMetadata(key trpcsession.Key) (*SessionMetadata, error) {
	var meta SessionMetadata
	if err := readJSON(s.metadataPath(key), &meta); err != nil {
		return nil, err
	}
	if meta.ID == "" {
		return nil, nil
	}
	return &meta, nil
}

// WriteMetadata writes session metadata to disk.
func (s *FileService) WriteMetadata(key trpcsession.Key, meta *SessionMetadata) error {
	return writeJSON(s.metadataPath(key), meta)
}

// ReadContextCache reads the cached LLM context from disk. Returns nil if not found.
func (s *FileService) ReadContextCache(key trpcsession.Key) (*ContextCache, error) {
	var cache ContextCache
	if err := readJSON(s.contextCachePath(key), &cache); err != nil {
		return nil, err
	}
	if len(cache.Messages) == 0 {
		return nil, nil
	}
	return &cache, nil
}

// WriteContextCache writes the LLM context cache to disk.
func (s *FileService) WriteContextCache(key trpcsession.Key, cache *ContextCache) error {
	return writeJSON(s.contextCachePath(key), cache)
}

// sessionStatePath returns the path to the session state JSON file.
func (s *FileService) sessionStatePath(key trpcsession.Key) string {
	return filepath.Join(s.sessionDir(key), "state.json")
}

// appStatePath returns the path to the app-level state JSON file.
func (s *FileService) appStatePath(appName string) string {
	return filepath.Join(s.dataDir, appName, "app_state.json")
}

// userStatePath returns the path to the user-level state JSON file.
func (s *FileService) userStatePath(userKey trpcsession.UserKey) string {
	return filepath.Join(s.dataDir, userKey.AppName, userKey.UserID, "user_state.json")
}

// CreateSession creates a new session and writes initial state to disk.
func (s *FileService) CreateSession(ctx context.Context, key trpcsession.Key, state trpcsession.StateMap, opts ...trpcsession.Option) (*trpcsession.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}
	if key.SessionID == "" {
		return nil, trpcsession.ErrSessionIDRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.sessionDir(key)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}

	sess := trpcsession.NewSession(key.AppName, key.UserID, key.SessionID)
	for k, v := range state {
		sess.SetState(k, v)
	}

	if err := writeJSON(s.sessionStatePath(key), sess.SnapshotState()); err != nil {
		return nil, fmt.Errorf("write session state: %w", err)
	}

	return s.loadMergedSession(key, sess)
}

// GetSession reads a session from disk.
func (s *FileService) GetSession(ctx context.Context, key trpcsession.Key, opts ...trpcsession.Option) (*trpcsession.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := s.sessionDir(key)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	events, warn, err := ReadEventJSONL(s.eventsPath(key))
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	if warn != nil {
		sessionID := key.SessionID
		if warn.FileSize > 0 {
			slog.Warn("session file is very large — use /compact to summarize or /clear to reset",
				"session_id", sessionID,
				"file_size_mb", warn.FileSize/(1024*1024),
			)
		}
		if warn.EventCount > 0 {
			slog.Warn("session has many events — use /compact to summarize or /clear to reset",
				"session_id", sessionID,
				"event_count", warn.EventCount,
			)
		}
		if warn.Skipped > 0 {
			slog.Warn("skipped corrupt lines in session file",
				"session_id", sessionID,
				"skipped", warn.Skipped,
			)
		}
	}

	var state trpcsession.StateMap
	if err := readJSON(s.sessionStatePath(key), &state); err != nil {
		return nil, fmt.Errorf("read session state: %w", err)
	}

	sessOpts := []trpcsession.SessionOptions{
		trpcsession.WithSessionEvents(events),
	}
	if state != nil {
		sessOpts = append(sessOpts, trpcsession.WithSessionState(state))
	}

	// Read directory mod time for timestamps
	info, _ := os.Stat(dir)
	if info != nil {
		sessOpts = append(sessOpts, trpcsession.WithSessionUpdatedAt(info.ModTime()))
	}

	sess := trpcsession.NewSession(key.AppName, key.UserID, key.SessionID, sessOpts...)

	// Load persisted summary if it exists.
	var summary trpcsession.Summary
	if err := readJSON(s.summaryPath(key), &summary); err == nil && summary.Summary != "" {
		sess.SummariesMu.Lock()
		if sess.Summaries == nil {
			sess.Summaries = make(map[string]*trpcsession.Summary)
		}
		sess.Summaries[""] = &summary
		sess.SummariesMu.Unlock()
	}

	opt := &trpcsession.Options{}
	for _, o := range opts {
		o(opt)
	}
	sess.ApplyEventFiltering(
		trpcsession.WithEventNum(opt.EventNum),
		trpcsession.WithEventTime(opt.EventTime),
	)

	return s.loadMergedSession(key, sess)
}

// ListSessions scans the user directory for session subdirectories.
func (s *FileService) ListSessions(ctx context.Context, userKey trpcsession.UserKey, opts ...trpcsession.Option) ([]*trpcsession.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	userDir := filepath.Join(s.dataDir, userKey.AppName, userKey.UserID)
	entries, err := os.ReadDir(userDir)
	if os.IsNotExist(err) {
		return []*trpcsession.Session{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user directory: %w", err)
	}

	var sessions []*trpcsession.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		key := trpcsession.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: entry.Name(),
		}
		// Use unlocked read since we already hold s.mu.RLock
		sess, err := s.getSessionUnlocked(key, opts...)
		if err != nil || sess == nil {
			continue
		}
		sessions = append(sessions, sess)
	}

	return sessions, nil
}

// getSessionUnlocked reads a session without acquiring locks (caller must hold lock).
func (s *FileService) getSessionUnlocked(key trpcsession.Key, opts ...trpcsession.Option) (*trpcsession.Session, error) {
	dir := s.sessionDir(key)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	events, _, err := ReadEventJSONL(s.eventsPath(key))
	if err != nil {
		return nil, err
	}

	var state trpcsession.StateMap
	if err := readJSON(s.sessionStatePath(key), &state); err != nil {
		return nil, err
	}

	sessOpts := []trpcsession.SessionOptions{
		trpcsession.WithSessionEvents(events),
	}
	if state != nil {
		sessOpts = append(sessOpts, trpcsession.WithSessionState(state))
	}

	sess := trpcsession.NewSession(key.AppName, key.UserID, key.SessionID, sessOpts...)

	opt := &trpcsession.Options{}
	for _, o := range opts {
		o(opt)
	}
	sess.ApplyEventFiltering(
		trpcsession.WithEventNum(opt.EventNum),
		trpcsession.WithEventTime(opt.EventTime),
	)

	return s.loadMergedStateUnlocked(key, sess)
}

// DeleteSession removes the session directory.
func (s *FileService) DeleteSession(ctx context.Context, key trpcsession.Key, opts ...trpcsession.Option) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.sessionDir(key)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session directory: %w", err)
	}
	return nil
}

// AppendEvent appends an event to the session's JSONL file and updates stored session state.
func (s *FileService) AppendEvent(ctx context.Context, sess *trpcsession.Session, evt *event.Event, opts ...trpcsession.Option) error {
	if sess == nil {
		return trpcsession.ErrNilSession
	}
	key := trpcsession.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this is the first event (metadata.json doesn't exist yet).
	metaPath := s.metadataPath(key)
	isFirst := false
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		isFirst = true
	}

	// Update the in-memory session (mirrors inmemory behavior)
	sess.UpdateUserSession(evt, opts...)

	// Append event to JSONL
	if err := AppendEventJSONL(s.eventsPath(key), evt); err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	// Persist session state
	if err := writeJSON(s.sessionStatePath(key), sess.SnapshotState()); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}

	now := time.Now().UTC()

	if isFirst {
		// Write initial metadata for new session.
		meta := &SessionMetadata{
			ID:        key.SessionID,
			UserID:    key.UserID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := writeJSON(metaPath, meta); err != nil {
			s.logger.Warn("failed to write session metadata", "error", err)
		}

		// Fire async naming from first user message.
		if s.namer != nil {
			var firstContent string
			if evt.Author == "user" && evt.Response != nil {
				for _, choice := range evt.Response.Choices {
					if choice.Message.Content != "" {
						firstContent = choice.Message.Content
						break
					}
				}
			}
			if firstContent != "" {
				go s.asyncInferName(key, firstContent)
			}
		}
	} else {
		// Bump UpdatedAt on existing metadata.
		go s.bumpMetadataUpdatedAt(key, now)
	}

	// Cache LLM context when a response with usage info arrives.
	if evt.Response != nil && evt.Response.Usage != nil && evt.Response.Usage.TotalTokens > 0 {
		go s.cacheContext(sess, key, evt.Response.Usage.TotalTokens)
	}

	return nil
}

// asyncInferName generates a session name from the first message and updates metadata.
func (s *FileService) asyncInferName(key trpcsession.Key, firstMessage string) {
	name := s.namer.InferName(context.Background(), firstMessage)

	s.mu.Lock()
	defer s.mu.Unlock()

	var meta SessionMetadata
	if err := readJSON(s.metadataPath(key), &meta); err != nil || meta.ID == "" {
		return
	}
	meta.Name = name
	if err := writeJSON(s.metadataPath(key), &meta); err != nil {
		s.logger.Warn("failed to update session name", "error", err)
	}
}

// bumpMetadataUpdatedAt updates the UpdatedAt timestamp in metadata.
func (s *FileService) bumpMetadataUpdatedAt(key trpcsession.Key, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var meta SessionMetadata
	if err := readJSON(s.metadataPath(key), &meta); err != nil || meta.ID == "" {
		return
	}
	meta.UpdatedAt = t
	_ = writeJSON(s.metadataPath(key), &meta)
}

// cacheContext snapshots the current session messages as cached LLM context.
func (s *FileService) cacheContext(sess *trpcsession.Session, key trpcsession.Key, tokenCount int) {
	sess.EventMu.RLock()
	var messages []model.Message
	for _, e := range sess.Events {
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			if choice.Message.Content == "" && len(choice.Message.ContentParts) == 0 && len(choice.Message.ToolCalls) == 0 {
				continue
			}
			messages = append(messages, choice.Message)
		}
	}
	sess.EventMu.RUnlock()

	if len(messages) == 0 {
		return
	}

	cache := &ContextCache{
		Messages:   messages,
		UpdatedAt:  time.Now().UTC(),
		TokenCount: tokenCount,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(s.contextCachePath(key), cache); err != nil {
		s.logger.Warn("failed to write context cache", "error", err)
	}
}

// maxForkEvents is the maximum number of events copied when forking a session.
// If the parent has more events up to the fork point, only the last maxForkEvents
// are copied (along with any existing summary).
const maxForkEvents = 100

// ForkSession creates a new thread session by copying events from parentKey up to
// (and including) the event with the given ID. A thread context system event is
// appended to orient the agent. Returns the key for the new thread session.
func (s *FileService) ForkSession(parentKey trpcsession.Key, upToEventID, threadName string) (trpcsession.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Read parent events.
	events, _, err := ReadEventJSONL(s.eventsPath(parentKey))
	if err != nil {
		return trpcsession.Key{}, fmt.Errorf("read parent events: %w", err)
	}

	// Find the target event.
	cutIdx := -1
	for i := range events {
		if events[i].ID == upToEventID {
			cutIdx = i
			break
		}
	}
	if cutIdx < 0 {
		return trpcsession.Key{}, fmt.Errorf("event %s not found in session %s", upToEventID, parentKey.SessionID)
	}

	// Slice events up to and including the target.
	forked := events[:cutIdx+1]

	// Extract content of the replied-to event for the context injection.
	var quotedContent string
	targetEvt := events[cutIdx]
	if targetEvt.Response != nil {
		for _, choice := range targetEvt.Response.Choices {
			if choice.Message.Content != "" {
				quotedContent = choice.Message.Content
				break
			}
		}
	}

	// If too many events, keep only the tail + copy parent summary.
	var parentSummary *trpcsession.Summary
	if len(forked) > maxForkEvents {
		forked = forked[len(forked)-maxForkEvents:]
		// Read parent summary if it exists.
		var sum trpcsession.Summary
		if err := readJSON(s.summaryPath(parentKey), &sum); err == nil && sum.Summary != "" {
			parentSummary = &sum
		}
	}

	// Create new thread session.
	threadID := uuid.New().String()
	threadKey := trpcsession.Key{
		AppName:   parentKey.AppName,
		UserID:    parentKey.UserID,
		SessionID: threadID,
	}
	threadDir := s.sessionDir(threadKey)
	if err := os.MkdirAll(threadDir, 0700); err != nil { // Secure: owner-only directory
		return trpcsession.Key{}, fmt.Errorf("create thread directory: %w", err)
	}

	// Write forked events.
	for i := range forked {
		if err := AppendEventJSONL(s.eventsPath(threadKey), &forked[i]); err != nil {
			os.RemoveAll(threadDir)
			return trpcsession.Key{}, fmt.Errorf("write forked event %d: %w", i, err)
		}
	}

	// Append thread context event.
	contextEvt := makeThreadContextEvent(quotedContent)
	if err := AppendEventJSONL(s.eventsPath(threadKey), contextEvt); err != nil {
		os.RemoveAll(threadDir)
		return trpcsession.Key{}, fmt.Errorf("write thread context event: %w", err)
	}

	// Copy parent summary if we truncated.
	if parentSummary != nil {
		_ = writeJSON(s.summaryPath(threadKey), parentSummary)
	}

	// Copy state.json from parent.
	if src, err := os.Open(s.sessionStatePath(parentKey)); err == nil {
		defer src.Close()
		if dst, err := os.Create(s.sessionStatePath(threadKey)); err == nil {
			io.Copy(dst, src)
			dst.Close()
		}
	}

	// Auto-generate thread name.
	if threadName == "" && quotedContent != "" {
		threadName = quotedContent
		if len(threadName) > 50 {
			threadName = threadName[:50] + "…"
		}
		threadName = "Re: " + threadName
	}
	if threadName == "" {
		threadName = "Thread " + time.Now().Format("Jan 2 15:04")
	}

	// Write metadata.
	now := time.Now().UTC()
	meta := &SessionMetadata{
		ID:              threadID,
		Name:            threadName,
		CreatedAt:       now,
		UpdatedAt:       now,
		UserID:          parentKey.UserID,
		ParentSessionID: parentKey.SessionID,
		ParentEventID:   upToEventID,
		IsThread:        true,
	}
	if err := writeJSON(s.metadataPath(threadKey), meta); err != nil {
		s.logger.Warn("failed to write thread metadata", "error", err)
	}

	// Fire async naming if namer is available (will refine the auto-name).
	if s.namer != nil && quotedContent != "" {
		go s.asyncInferName(threadKey, "Thread about: "+quotedContent)
	}

	s.logger.Info("session forked",
		"parent", parentKey.SessionID,
		"thread", threadID,
		"events_copied", len(forked),
		"reply_to_event", upToEventID)

	return threadKey, nil
}

// makeThreadContextEvent creates a system event injected at the start of a
// thread to orient the agent about the conversation branch.
func makeThreadContextEvent(quotedContent string) *event.Event {
	content := "[Thread Context] This conversation is a focused thread branching from an earlier discussion."
	if quotedContent != "" {
		content += fmt.Sprintf(
			"\nThe user is replying to this specific message:\n---\n%s\n---\n"+
				"Stay focused on this topic. The user wants to dive deeper into this particular point.",
			quotedContent,
		)
	}
	return &event.Event{
		ID:        uuid.New().String(),
		Author:    "system",
		Timestamp: time.Now().UTC(),
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: content}},
			},
		},
	}
}

// UpdateAppState merges state into the app-level state file.
func (s *FileService) UpdateAppState(ctx context.Context, appName string, state trpcsession.StateMap) error {
	if appName == "" {
		return trpcsession.ErrAppNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.appStatePath(appName)
	var existing trpcsession.StateMap
	if err := readJSON(path, &existing); err != nil {
		return fmt.Errorf("read app state: %w", err)
	}
	if existing == nil {
		existing = make(trpcsession.StateMap)
	}

	for k, v := range state {
		k = strings.TrimPrefix(k, trpcsession.StateAppPrefix)
		existing[k] = v
	}

	return writeJSON(path, existing)
}

// DeleteAppState deletes a key from app-level state.
func (s *FileService) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return trpcsession.ErrAppNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.appStatePath(appName)
	var existing trpcsession.StateMap
	if err := readJSON(path, &existing); err != nil {
		return fmt.Errorf("read app state: %w", err)
	}
	if existing == nil {
		return nil
	}

	key = strings.TrimPrefix(key, trpcsession.StateAppPrefix)
	delete(existing, key)
	return writeJSON(path, existing)
}

// ListAppStates returns the app-level state.
func (s *FileService) ListAppStates(ctx context.Context, appName string) (trpcsession.StateMap, error) {
	if appName == "" {
		return nil, trpcsession.ErrAppNameRequired
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var state trpcsession.StateMap
	if err := readJSON(s.appStatePath(appName), &state); err != nil {
		return nil, fmt.Errorf("read app state: %w", err)
	}
	if state == nil {
		return make(trpcsession.StateMap), nil
	}
	return state, nil
}

// UpdateUserState merges state into the user-level state file.
func (s *FileService) UpdateUserState(ctx context.Context, userKey trpcsession.UserKey, state trpcsession.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	for k := range state {
		if strings.HasPrefix(k, trpcsession.StateAppPrefix) {
			return fmt.Errorf("update user state: %s is not allowed", k)
		}
		if strings.HasPrefix(k, trpcsession.StateTempPrefix) {
			return fmt.Errorf("update user state: %s is not allowed", k)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.userStatePath(userKey)
	var existing trpcsession.StateMap
	if err := readJSON(path, &existing); err != nil {
		return fmt.Errorf("read user state: %w", err)
	}
	if existing == nil {
		existing = make(trpcsession.StateMap)
	}

	for k, v := range state {
		k = strings.TrimPrefix(k, trpcsession.StateUserPrefix)
		existing[k] = v
	}

	return writeJSON(path, existing)
}

// ListUserStates returns the user-level state.
func (s *FileService) ListUserStates(ctx context.Context, userKey trpcsession.UserKey) (trpcsession.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var state trpcsession.StateMap
	if err := readJSON(s.userStatePath(userKey), &state); err != nil {
		return nil, fmt.Errorf("read user state: %w", err)
	}
	if state == nil {
		return make(trpcsession.StateMap), nil
	}
	return state, nil
}

// DeleteUserState deletes a key from user-level state.
func (s *FileService) DeleteUserState(ctx context.Context, userKey trpcsession.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.userStatePath(userKey)
	var existing trpcsession.StateMap
	if err := readJSON(path, &existing); err != nil {
		return fmt.Errorf("read user state: %w", err)
	}
	if existing == nil {
		return nil
	}

	key = strings.TrimPrefix(key, trpcsession.StateUserPrefix)
	delete(existing, key)
	return writeJSON(path, existing)
}

// UpdateSessionState updates session-level state without appending an event.
func (s *FileService) UpdateSessionState(ctx context.Context, key trpcsession.Key, state trpcsession.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	for k := range state {
		if strings.HasPrefix(k, trpcsession.StateAppPrefix) {
			return fmt.Errorf("update session state: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, trpcsession.StateUserPrefix) {
			return fmt.Errorf("update session state: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.sessionStatePath(key)
	var existing trpcsession.StateMap
	if err := readJSON(path, &existing); err != nil {
		return fmt.Errorf("read session state: %w", err)
	}
	if existing == nil {
		existing = make(trpcsession.StateMap)
	}

	for k, v := range state {
		existing[k] = v
	}

	return writeJSON(path, existing)
}

// CreateSessionSummary summarizes the session's conversation history using the
// LLM and truncates the events file, keeping only the most recent events.
// The summary is stored both in memory (on sess.Summaries) and on disk (summary.json).
func (s *FileService) CreateSessionSummary(ctx context.Context, sess *trpcsession.Session, filterKey string, force bool) error {
	if s.model == nil {
		return fmt.Errorf("no LLM model configured for session summarization (call SetModel first)")
	}

	// Check if summary already exists and force is not set.
	sess.SummariesMu.RLock()
	existing := sess.Summaries[filterKey]
	sess.SummariesMu.RUnlock()
	if existing != nil && existing.Summary != "" && !force {
		return nil // already summarized
	}

	// Build conversation transcript from events.
	sess.EventMu.RLock()
	events := make([]event.Event, len(sess.Events))
	copy(events, sess.Events)
	sess.EventMu.RUnlock()

	if len(events) <= compactKeepEvents {
		return nil // too few events to compact
	}

	// Events to summarize (all except the most recent ones we keep).
	toSummarize := events[:len(events)-compactKeepEvents]

	// Call pre-compaction hook to extract memories BEFORE events are lost.
	// This is critical for preventing data loss - memories must be extracted
	// before the events are truncated from the session.
	if s.compactionHook != nil {
		if err := s.compactionHook.BeforeCompaction(ctx, sess, toSummarize); err != nil {
			s.logger.Warn("pre-compaction memory extraction failed",
				"session_id", sess.ID,
				"events_to_remove", len(toSummarize),
				"error", err)
			// Continue with compaction even if extraction fails - the alternative
			// (blocking compaction) could cause context overflow issues.
		}
	}

	transcript := formatTranscript(toSummarize)

	if strings.TrimSpace(transcript) == "" {
		return nil
	}

	// Call LLM to generate summary.
	prompt := "Summarize the following conversation concisely. Capture the key topics, " +
		"decisions made, tasks completed, and any important context needed to continue " +
		"the conversation. Write in third person, past tense.\n\n" + transcript

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: prompt},
		},
	}

	respCh, err := s.model.GenerateContent(ctx, req)
	if err != nil {
		return fmt.Errorf("generate summary: %w", err)
	}

	var summaryText string
	for resp := range respCh {
		if resp.Error != nil {
			return fmt.Errorf("summary LLM error: %s", resp.Error.Message)
		}
		for _, choice := range resp.Choices {
			summaryText += choice.Message.Content
		}
	}

	if strings.TrimSpace(summaryText) == "" {
		return fmt.Errorf("LLM returned empty summary")
	}

	// Store summary on session object.
	now := time.Now().UTC()
	summary := &trpcsession.Summary{
		Summary:   summaryText,
		UpdatedAt: now,
	}

	sess.SummariesMu.Lock()
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*trpcsession.Summary)
	}
	sess.Summaries[filterKey] = summary
	sess.SummariesMu.Unlock()

	// Persist summary to disk.
	key := trpcsession.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := writeJSON(s.summaryPath(key), summary); err != nil {
		return fmt.Errorf("persist summary: %w", err)
	}

	// Truncate events file to keep only recent events.
	keptEvents := events[len(events)-compactKeepEvents:]
	eventsPath := s.eventsPath(key)
	tmpPath := eventsPath + ".compact.tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create compact temp file: %w", err)
	}
	for i := range keptEvents {
		data, err := json.Marshal(&keptEvents[i])
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshal event %d: %w", i, err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write event %d: %w", i, err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close compact temp file: %w", err)
	}
	if err := os.Rename(tmpPath, eventsPath); err != nil {
		return fmt.Errorf("rename compact file: %w", err)
	}

	// Update in-memory events.
	sess.EventMu.Lock()
	sess.Events = keptEvents
	sess.EventMu.Unlock()

	slog.Info("session compacted",
		"session_id", sess.ID,
		"events_before", len(events),
		"events_after", len(keptEvents),
		"summary_length", len(summaryText),
	)

	return nil
}

// EnqueueSummaryJob delegates to synchronous CreateSessionSummary.
func (s *FileService) EnqueueSummaryJob(ctx context.Context, sess *trpcsession.Session, filterKey string, force bool) error {
	return s.CreateSessionSummary(ctx, sess, filterKey, force)
}

// GetSessionSummaryText returns the summary text for the session if one exists.
func (s *FileService) GetSessionSummaryText(ctx context.Context, sess *trpcsession.Session, opts ...trpcsession.SummaryOption) (string, bool) {
	var so trpcsession.SummaryOptions
	for _, o := range opts {
		o(&so)
	}

	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	if sess.Summaries == nil {
		return "", false
	}

	sum := sess.Summaries[so.FilterKey]
	if sum == nil || sum.Summary == "" {
		return "", false
	}
	return sum.Summary, true
}

// formatTranscript converts events into a readable conversation transcript.
func formatTranscript(events []event.Event) string {
	var b strings.Builder
	for _, evt := range events {
		if evt.Response == nil {
			continue
		}
		role := evt.Author
		for _, choice := range evt.Response.Choices {
			content := strings.TrimSpace(choice.Message.Content)
			if content == "" {
				continue
			}
			// Truncate very long messages to avoid overwhelming the summarizer.
			if len(content) > 2000 {
				content = content[:2000] + "... [truncated]"
			}
			fmt.Fprintf(&b, "%s: %s\n\n", role, content)
		}
	}
	return b.String()
}

// Close is a no-op (no connections to close).
func (s *FileService) Close() error {
	return nil
}

// loadMergedSession merges app and user state into a session (caller must hold lock).
func (s *FileService) loadMergedSession(key trpcsession.Key, sess *trpcsession.Session) (*trpcsession.Session, error) {
	return s.loadMergedStateUnlocked(key, sess)
}

// loadMergedStateUnlocked reads app/user state files and merges them into the session.
func (s *FileService) loadMergedStateUnlocked(key trpcsession.Key, sess *trpcsession.Session) (*trpcsession.Session, error) {
	var appState trpcsession.StateMap
	_ = readJSON(s.appStatePath(key.AppName), &appState)
	for k, v := range appState {
		sess.SetState(trpcsession.StateAppPrefix+k, v)
	}

	userKey := trpcsession.UserKey{AppName: key.AppName, UserID: key.UserID}
	var userState trpcsession.StateMap
	_ = readJSON(s.userStatePath(userKey), &userState)
	for k, v := range userState {
		sess.SetState(trpcsession.StateUserPrefix+k, v)
	}

	return sess, nil
}

