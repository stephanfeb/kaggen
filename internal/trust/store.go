package trust

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ThirdPartyMessage represents a message exchange with a third-party sender.
type ThirdPartyMessage struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id"`
	SenderPhone      string    `json:"sender_phone,omitempty"`
	SenderTelegramID int64     `json:"sender_telegram_id,omitempty"`
	SenderName       string    `json:"sender_name,omitempty"`
	Channel          string    `json:"channel"`
	UserMessage      string    `json:"user_message"`
	LLMResponse      string    `json:"llm_response"`
	CreatedAt        time.Time `json:"created_at"`
	Notified         bool      `json:"notified"`
}

// SessionSummary represents a summary of a third-party conversation session.
type SessionSummary struct {
	SessionID        string    `json:"session_id"`
	SenderPhone      string    `json:"sender_phone,omitempty"`
	SenderTelegramID int64     `json:"sender_telegram_id,omitempty"`
	SenderName       string    `json:"sender_name,omitempty"`
	Channel          string    `json:"channel"`
	MessageCount     int       `json:"message_count"`
	UnreadCount      int       `json:"unread_count"`
	LastMessageAt    time.Time `json:"last_message_at"`
	FirstMessageAt   time.Time `json:"first_message_at"`
}

// ThirdPartyStore persists third-party message exchanges in SQLite.
type ThirdPartyStore struct {
	db *sql.DB
}

// NewThirdPartyStore opens or creates the third-party messages database.
func NewThirdPartyStore(dbPath string) (*ThirdPartyStore, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS third_party_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			sender_phone TEXT,
			sender_telegram_id INTEGER,
			sender_name TEXT,
			channel TEXT NOT NULL,
			user_message TEXT NOT NULL,
			llm_response TEXT NOT NULL,
			created_at TEXT NOT NULL,
			notified INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_tp_session ON third_party_messages(session_id);
		CREATE INDEX IF NOT EXISTS idx_tp_created ON third_party_messages(created_at);
		CREATE INDEX IF NOT EXISTS idx_tp_notified ON third_party_messages(notified);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &ThirdPartyStore{db: db}, nil
}

// Add inserts a new third-party message exchange.
func (s *ThirdPartyStore) Add(msg *ThirdPartyMessage) error {
	_, err := s.db.Exec(
		`INSERT INTO third_party_messages
		 (id, session_id, sender_phone, sender_telegram_id, sender_name, channel, user_message, llm_response, created_at, notified)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.SessionID, msg.SenderPhone, msg.SenderTelegramID,
		msg.SenderName, msg.Channel, msg.UserMessage, msg.LLMResponse,
		msg.CreatedAt.UTC().Format(time.RFC3339), 0,
	)
	return err
}

// ListSessions returns summaries of all third-party conversation sessions.
func (s *ThirdPartyStore) ListSessions() ([]*SessionSummary, error) {
	rows, err := s.db.Query(`
		SELECT
			session_id,
			COALESCE(sender_phone, '') as sender_phone,
			COALESCE(sender_telegram_id, 0) as sender_telegram_id,
			COALESCE(MAX(sender_name), '') as sender_name,
			channel,
			COUNT(*) as message_count,
			SUM(CASE WHEN notified = 0 THEN 1 ELSE 0 END) as unread_count,
			MAX(created_at) as last_message_at,
			MIN(created_at) as first_message_at
		FROM third_party_messages
		GROUP BY session_id
		ORDER BY last_message_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*SessionSummary
	for rows.Next() {
		var ss SessionSummary
		var lastStr, firstStr string
		if err := rows.Scan(
			&ss.SessionID, &ss.SenderPhone, &ss.SenderTelegramID,
			&ss.SenderName, &ss.Channel, &ss.MessageCount, &ss.UnreadCount,
			&lastStr, &firstStr,
		); err != nil {
			return nil, err
		}
		ss.LastMessageAt, _ = time.Parse(time.RFC3339, lastStr)
		ss.FirstMessageAt, _ = time.Parse(time.RFC3339, firstStr)
		sessions = append(sessions, &ss)
	}
	return sessions, rows.Err()
}

// GetMessages returns messages for a specific session with pagination.
func (s *ThirdPartyStore) GetMessages(sessionID string, limit, offset int) ([]*ThirdPartyMessage, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`
		SELECT id, session_id, COALESCE(sender_phone, ''), COALESCE(sender_telegram_id, 0),
		       COALESCE(sender_name, ''), channel, user_message, llm_response, created_at, notified
		FROM third_party_messages
		WHERE session_id = ?
		ORDER BY created_at ASC
		LIMIT ? OFFSET ?
	`, sessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*ThirdPartyMessage
	for rows.Next() {
		var msg ThirdPartyMessage
		var createdStr string
		var notified int
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.SenderPhone, &msg.SenderTelegramID,
			&msg.SenderName, &msg.Channel, &msg.UserMessage, &msg.LLMResponse,
			&createdStr, &notified,
		); err != nil {
			return nil, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		msg.Notified = notified == 1
		messages = append(messages, &msg)
	}
	return messages, rows.Err()
}

// GetUnnotifiedCount returns the count of messages that haven't been included in a digest.
func (s *ThirdPartyStore) GetUnnotifiedCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM third_party_messages WHERE notified = 0`).Scan(&count)
	return count, err
}

// GetUnnotifiedMessages returns all messages that haven't been included in a digest.
func (s *ThirdPartyStore) GetUnnotifiedMessages() ([]*ThirdPartyMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, COALESCE(sender_phone, ''), COALESCE(sender_telegram_id, 0),
		       COALESCE(sender_name, ''), channel, user_message, llm_response, created_at, notified
		FROM third_party_messages
		WHERE notified = 0
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*ThirdPartyMessage
	for rows.Next() {
		var msg ThirdPartyMessage
		var createdStr string
		var notified int
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.SenderPhone, &msg.SenderTelegramID,
			&msg.SenderName, &msg.Channel, &msg.UserMessage, &msg.LLMResponse,
			&createdStr, &notified,
		); err != nil {
			return nil, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		msg.Notified = notified == 1
		messages = append(messages, &msg)
	}
	return messages, rows.Err()
}

// MarkNotified marks messages as notified (included in a digest).
func (s *ThirdPartyStore) MarkNotified(messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE third_party_messages SET notified = 1 WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range messageIDs {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// MarkSessionRead marks all messages in a session as notified/read.
func (s *ThirdPartyStore) MarkSessionRead(sessionID string) error {
	_, err := s.db.Exec(`UPDATE third_party_messages SET notified = 1 WHERE session_id = ?`, sessionID)
	return err
}

// GetMessageCount returns the total number of messages for a session.
func (s *ThirdPartyStore) GetMessageCount(sessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM third_party_messages WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// Close closes the database connection.
func (s *ThirdPartyStore) Close() error {
	return s.db.Close()
}
