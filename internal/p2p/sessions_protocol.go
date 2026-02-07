package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/yourusername/kaggen/internal/config"
	kaggenSession "github.com/yourusername/kaggen/internal/session"
)

// SessionsProtocol handles the /kaggen/sessions/1.0.0 protocol.
type SessionsProtocol struct {
	*APIHandler
	config *config.Config
}

// NewSessionsProtocol creates a new sessions protocol handler.
func NewSessionsProtocol(cfg *config.Config, logger *slog.Logger) *SessionsProtocol {
	h := &SessionsProtocol{
		APIHandler: NewAPIHandler(SessionsProtocolID, logger),
		config:     cfg,
	}

	// Register methods
	h.RegisterMethod("list", h.list)
	h.RegisterMethod("messages", h.messages)
	h.RegisterMethod("rename", h.rename)
	h.RegisterMethod("delete", h.deleteSession)
	h.RegisterMethod("archive", h.archive)

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *SessionsProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

// sessionInfo matches the dashboard's session info structure.
type sessionInfo struct {
	ID              string        `json:"id"`
	Name            string        `json:"name,omitempty"`
	UserID          string        `json:"user_id"`
	Channel         string        `json:"channel,omitempty"`
	CreatedAt       string        `json:"created_at,omitempty"`
	UpdatedAt       string        `json:"updated_at"`
	MessageCount    int           `json:"message_count"`
	IsThread        bool          `json:"is_thread,omitempty"`
	ParentSessionID string        `json:"parent_session_id,omitempty"`
	Threads         []sessionInfo `json:"threads,omitempty"`
}

type listParams struct {
	Flat bool `json:"flat,omitempty"`
}

func (p *SessionsProtocol) list(params json.RawMessage) (any, error) {
	var args listParams
	if len(params) > 0 {
		json.Unmarshal(params, &args)
	}

	sessionsDir := p.config.SessionsPath()
	appDir := filepath.Join(sessionsDir, "kaggen")

	var allSessions []sessionInfo

	userDirs, err := os.ReadDir(appDir)
	if err != nil {
		return map[string]any{"sessions": []any{}}, nil
	}

	fs := kaggenSession.NewFileService(sessionsDir)

	for _, userDir := range userDirs {
		if !userDir.IsDir() {
			continue
		}
		userPath := filepath.Join(appDir, userDir.Name())
		sessDirs, err := os.ReadDir(userPath)
		if err != nil {
			continue
		}
		for _, sessDir := range sessDirs {
			if !sessDir.IsDir() {
				continue
			}
			dirInfo, _ := sessDir.Info()

			eventsPath := filepath.Join(userPath, sessDir.Name(), "events.jsonl")
			msgCount := countLines(eventsPath)

			si := sessionInfo{
				ID:           sessDir.Name(),
				UserID:       userDir.Name(),
				MessageCount: msgCount,
			}

			key := session.Key{AppName: "kaggen", UserID: userDir.Name(), SessionID: sessDir.Name()}
			if meta, err := fs.ReadMetadata(key); err == nil && meta != nil {
				if !meta.ArchivedAt.IsZero() {
					continue
				}
				si.Name = meta.Name
				si.Channel = meta.Channel
				si.IsThread = meta.IsThread
				si.ParentSessionID = meta.ParentSessionID
				if !meta.CreatedAt.IsZero() {
					si.CreatedAt = meta.CreatedAt.Format(time.RFC3339)
				}
				if !meta.UpdatedAt.IsZero() {
					si.UpdatedAt = meta.UpdatedAt.Format(time.RFC3339)
				}
			}

			if si.UpdatedAt == "" && dirInfo != nil {
				si.UpdatedAt = dirInfo.ModTime().Format(time.RFC3339)
			}

			allSessions = append(allSessions, si)
		}
	}

	sort.Slice(allSessions, func(i, j int) bool {
		return allSessions[i].UpdatedAt > allSessions[j].UpdatedAt
	})

	// Nest threads unless flat listing requested
	if !args.Flat {
		parentMap := make(map[string]*sessionInfo)
		var topLevel []sessionInfo
		for i := range allSessions {
			if !allSessions[i].IsThread {
				topLevel = append(topLevel, allSessions[i])
				parentMap[allSessions[i].ID] = &topLevel[len(topLevel)-1]
			}
		}
		for _, s := range allSessions {
			if s.IsThread {
				if parent, ok := parentMap[s.ParentSessionID]; ok {
					parent.Threads = append(parent.Threads, s)
				} else {
					topLevel = append(topLevel, s)
				}
			}
		}
		allSessions = topLevel
	}

	if len(allSessions) > 100 {
		allSessions = allSessions[:100]
	}

	return map[string]any{"sessions": allSessions}, nil
}

type messagesParams struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Detail    string `json:"detail,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

func (p *SessionsProtocol) messages(params json.RawMessage) (any, error) {
	var args messagesParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.UserID == "" || args.SessionID == "" {
		return nil, fmt.Errorf("user_id and session_id are required")
	}

	if args.Detail == "" {
		args.Detail = "chat"
	}
	if args.Limit == 0 {
		args.Limit = 50
	}

	sessionsDir := p.config.SessionsPath()
	eventsPath := filepath.Join(sessionsDir, "kaggen", args.UserID, args.SessionID, "events.jsonl")

	events, _, err := kaggenSession.ReadEventJSONL(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}

	type msgOut struct {
		EventID   string   `json:"event_id"`
		Role      string   `json:"role"`
		Content   string   `json:"content"`
		Timestamp string   `json:"timestamp,omitempty"`
		ToolCalls []string `json:"tool_calls,omitempty"`
		ToolID    string   `json:"tool_id,omitempty"`
	}

	var messages []msgOut
	for _, evt := range events {
		if evt.Response == nil {
			continue
		}
		ts := ""
		if !evt.Timestamp.IsZero() {
			ts = evt.Timestamp.Format(time.RFC3339)
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message

			if args.Detail == "chat" {
				if msg.Role == "tool" || msg.ToolID != "" {
					continue
				}
				if msg.Content == "" && len(msg.ContentParts) == 0 {
					continue
				}
				if len(msg.ToolCalls) > 0 && msg.Content == "" {
					continue
				}
				messages = append(messages, msgOut{
					EventID:   evt.ID,
					Role:      string(msg.Role),
					Content:   msg.Content,
					Timestamp: ts,
				})
			} else {
				m := msgOut{
					EventID:   evt.ID,
					Role:      string(msg.Role),
					Content:   msg.Content,
					Timestamp: ts,
					ToolID:    msg.ToolID,
				}
				for _, tc := range msg.ToolCalls {
					m.ToolCalls = append(m.ToolCalls, tc.Function.Name)
				}
				messages = append(messages, m)
			}
		}
	}

	// Pagination
	if args.Offset > len(messages) {
		args.Offset = len(messages)
	}
	end := args.Offset + args.Limit
	if end > len(messages) {
		end = len(messages)
	}
	page := messages[args.Offset:end]

	// Read metadata
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: "kaggen", UserID: args.UserID, SessionID: args.SessionID}

	var name string
	if meta, err := fs.ReadMetadata(key); err == nil && meta != nil {
		name = meta.Name
	}

	return map[string]any{
		"session_id": args.SessionID,
		"name":       name,
		"messages":   page,
		"total":      len(messages),
	}, nil
}

type renameParams struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

func (p *SessionsProtocol) rename(params json.RawMessage) (any, error) {
	var args renameParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.UserID == "" || args.SessionID == "" || args.Name == "" {
		return nil, fmt.Errorf("user_id, session_id, and name are required")
	}

	sessionsDir := p.config.SessionsPath()
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: "kaggen", UserID: args.UserID, SessionID: args.SessionID}

	meta, err := fs.ReadMetadata(key)
	if err != nil || meta == nil {
		meta = &kaggenSession.SessionMetadata{
			ID:     args.SessionID,
			UserID: args.UserID,
		}
	}
	meta.Name = args.Name
	meta.UpdatedAt = time.Now().UTC()

	if err := fs.WriteMetadata(key, meta); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	return map[string]any{"success": true, "name": meta.Name}, nil
}

type sessionIDParams struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

func (p *SessionsProtocol) deleteSession(params json.RawMessage) (any, error) {
	var args sessionIDParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.UserID == "" || args.SessionID == "" {
		return nil, fmt.Errorf("user_id and session_id are required")
	}

	sessionsDir := p.config.SessionsPath()
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: "kaggen", UserID: args.UserID, SessionID: args.SessionID}

	if err := fs.DeleteSession(nil, key); err != nil {
		return nil, fmt.Errorf("failed to delete session: %w", err)
	}

	return map[string]any{"success": true}, nil
}

func (p *SessionsProtocol) archive(params json.RawMessage) (any, error) {
	var args sessionIDParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.UserID == "" || args.SessionID == "" {
		return nil, fmt.Errorf("user_id and session_id are required")
	}

	sessionsDir := p.config.SessionsPath()
	fs := kaggenSession.NewFileService(sessionsDir)
	key := session.Key{AppName: "kaggen", UserID: args.UserID, SessionID: args.SessionID}

	meta, err := fs.ReadMetadata(key)
	if err != nil || meta == nil {
		meta = &kaggenSession.SessionMetadata{
			ID:     args.SessionID,
			UserID: args.UserID,
		}
	}
	meta.ArchivedAt = time.Now().UTC()
	meta.UpdatedAt = time.Now().UTC()

	if err := fs.WriteMetadata(key, meta); err != nil {
		return nil, fmt.Errorf("failed to archive session: %w", err)
	}

	return map[string]any{"success": true}, nil
}

// countLines counts the number of lines in a file.
func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	return count
}
