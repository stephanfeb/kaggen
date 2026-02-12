package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/yourusername/kaggen/internal/trust"
)

// ThirdPartyProtocolID is the protocol ID for third-party message browsing.
const ThirdPartyProtocolID protocol.ID = "/kaggen/thirdparty/1.0.0"

// ThirdPartyProtocol handles the /kaggen/thirdparty/1.0.0 protocol.
// It allows mobile clients to browse third-party conversations.
type ThirdPartyProtocol struct {
	*APIHandler
	store *trust.ThirdPartyStore
}

// NewThirdPartyProtocol creates a new third-party protocol handler.
func NewThirdPartyProtocol(store *trust.ThirdPartyStore, logger *slog.Logger) *ThirdPartyProtocol {
	h := &ThirdPartyProtocol{
		APIHandler: NewAPIHandler(ThirdPartyProtocolID, logger),
		store:      store,
	}

	h.RegisterMethod("sessions", h.sessions)
	h.RegisterMethod("messages", h.messages)
	h.RegisterMethod("unread_count", h.unreadCount)
	h.RegisterMethod("mark_read", h.markRead)

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *ThirdPartyProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

// sessionOut is the output format for a session summary.
type sessionOut struct {
	SessionID        string `json:"session_id"`
	SenderPhone      string `json:"sender_phone,omitempty"`
	SenderTelegramID int64  `json:"sender_telegram_id,omitempty"`
	SenderName       string `json:"sender_name,omitempty"`
	Channel          string `json:"channel"`
	MessageCount     int    `json:"message_count"`
	UnreadCount      int    `json:"unread_count"`
	LastMessageAt    string `json:"last_message_at"`
	FirstMessageAt   string `json:"first_message_at"`
}

// sessions returns a list of third-party conversation sessions.
func (p *ThirdPartyProtocol) sessions(params json.RawMessage) (any, error) {
	if p.store == nil {
		return nil, fmt.Errorf("third-party store not configured")
	}

	sessions, err := p.store.ListSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	out := make([]sessionOut, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionOut{
			SessionID:        s.SessionID,
			SenderPhone:      s.SenderPhone,
			SenderTelegramID: s.SenderTelegramID,
			SenderName:       s.SenderName,
			Channel:          s.Channel,
			MessageCount:     s.MessageCount,
			UnreadCount:      s.UnreadCount,
			LastMessageAt:    s.LastMessageAt.Format("2006-01-02T15:04:05Z07:00"),
			FirstMessageAt:   s.FirstMessageAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return map[string]any{"sessions": out}, nil
}

// messageOut is the output format for a single message.
type messageOut struct {
	ID          string `json:"id"`
	UserMessage string `json:"user_message"`
	LLMResponse string `json:"llm_response"`
	CreatedAt   string `json:"created_at"`
	Notified    bool   `json:"notified"`
}

// thirdPartyMessagesParams are the parameters for the messages method.
type thirdPartyMessagesParams struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// messages returns messages for a specific third-party session.
func (p *ThirdPartyProtocol) messages(params json.RawMessage) (any, error) {
	if p.store == nil {
		return nil, fmt.Errorf("third-party store not configured")
	}

	var args thirdPartyMessagesParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	if args.Limit <= 0 {
		args.Limit = 50
	}

	messages, err := p.store.GetMessages(args.SessionID, args.Limit, args.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	out := make([]messageOut, 0, len(messages))
	for _, m := range messages {
		out = append(out, messageOut{
			ID:          m.ID,
			UserMessage: m.UserMessage,
			LLMResponse: m.LLMResponse,
			CreatedAt:   m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Notified:    m.Notified,
		})
	}

	// Get total count for pagination
	total, _ := p.store.GetMessageCount(args.SessionID)

	return map[string]any{
		"session_id": args.SessionID,
		"messages":   out,
		"total":      total,
		"limit":      args.Limit,
		"offset":     args.Offset,
	}, nil
}

// unreadCount returns the count of unnotified messages.
func (p *ThirdPartyProtocol) unreadCount(params json.RawMessage) (any, error) {
	if p.store == nil {
		return nil, fmt.Errorf("third-party store not configured")
	}

	count, err := p.store.GetUnnotifiedCount()
	if err != nil {
		return nil, fmt.Errorf("failed to get unread count: %w", err)
	}

	return map[string]any{"unread_count": count}, nil
}

// thirdPartyMarkReadParams are the parameters for the mark_read method.
type thirdPartyMarkReadParams struct {
	SessionID string `json:"session_id"`
}

// markRead marks all messages in a session as read/notified.
func (p *ThirdPartyProtocol) markRead(params json.RawMessage) (any, error) {
	if p.store == nil {
		return nil, fmt.Errorf("third-party store not configured")
	}

	var args thirdPartyMarkReadParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	if err := p.store.MarkSessionRead(args.SessionID); err != nil {
		return nil, fmt.Errorf("failed to mark session read: %w", err)
	}

	return map[string]any{"success": true, "session_id": args.SessionID}, nil
}
