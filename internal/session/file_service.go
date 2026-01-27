package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
)

// FileService implements trpc-agent-go's session.Service using the local filesystem.
//
// Storage layout:
//
//	<dataDir>/
//	  <appName>/<userID>/<sessionID>/
//	    events.jsonl   – append-only event log
//	    state.json     – session-level state
//	  <appName>/
//	    app_state.json – app-scoped state
//	  <appName>/<userID>/
//	    user_state.json – user-scoped state
type FileService struct {
	dataDir string
	mu      sync.RWMutex
}

// Compile-time check.
var _ trpcsession.Service = (*FileService)(nil)

// NewFileService creates a new file-backed session service rooted at dataDir.
func NewFileService(dataDir string) *FileService {
	return &FileService{dataDir: dataDir}
}

// sessionDir returns the directory for a specific session.
func (s *FileService) sessionDir(key trpcsession.Key) string {
	return filepath.Join(s.dataDir, key.AppName, key.UserID, key.SessionID)
}

// eventsPath returns the path to the events JSONL file for a session.
func (s *FileService) eventsPath(key trpcsession.Key) string {
	return filepath.Join(s.sessionDir(key), "events.jsonl")
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
	if err := os.MkdirAll(dir, 0755); err != nil {
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

	events, err := ReadEventJSONL(s.eventsPath(key))
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
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

	events, err := ReadEventJSONL(s.eventsPath(key))
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

	return nil
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

// CreateSessionSummary is a no-op stub (summarization requires an LLM).
func (s *FileService) CreateSessionSummary(ctx context.Context, sess *trpcsession.Session, filterKey string, force bool) error {
	return nil
}

// EnqueueSummaryJob is a no-op stub.
func (s *FileService) EnqueueSummaryJob(ctx context.Context, sess *trpcsession.Session, filterKey string, force bool) error {
	return nil
}

// GetSessionSummaryText is a no-op stub.
func (s *FileService) GetSessionSummaryText(ctx context.Context, sess *trpcsession.Session, opts ...trpcsession.SummaryOption) (string, bool) {
	return "", false
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

