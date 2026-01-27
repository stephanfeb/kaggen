package channel

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/yourusername/kaggen/internal/config"

	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// telegramMaxMessageLen is Telegram's maximum message length.
	telegramMaxMessageLen = 4096
)

// TelegramChannel implements Channel for Telegram bots.
type TelegramChannel struct {
	token            string
	bot              *tgbotapi.BotAPI
	messages         chan *Message
	logger           *slog.Logger
	chatLimiter      *chatRateLimiter
	userLimiter      *userRateLimiter
	allowedUsers     map[int64]bool
	allowedChats     map[int64]bool
	rejectMessage    string
	rateLimitMessage string
	sessionService   trpcsession.Service
}

// NewTelegramChannel creates a new Telegram channel.
// sessionService is optional; when provided, the /clear command can reset sessions.
func NewTelegramChannel(token string, cfg *config.TelegramConfig, sessionService trpcsession.Service, logger *slog.Logger) *TelegramChannel {
	allowedUsers := make(map[int64]bool, len(cfg.AllowedUsers))
	for _, uid := range cfg.AllowedUsers {
		allowedUsers[uid] = true
	}
	allowedChats := make(map[int64]bool, len(cfg.AllowedChats))
	for _, cid := range cfg.AllowedChats {
		allowedChats[cid] = true
	}

	rejectMsg := cfg.RejectMessage
	if rejectMsg == "" {
		rejectMsg = "Sorry, you are not authorized to use this bot."
	}
	rateLimitMsg := cfg.RateLimitMessage
	if rateLimitMsg == "" {
		rateLimitMsg = "You're sending messages too quickly. Please wait a moment."
	}

	return &TelegramChannel{
		token:            token,
		messages:         make(chan *Message, 64),
		logger:           logger,
		chatLimiter:      newChatRateLimiter(),
		userLimiter:      newUserRateLimiter(cfg.UserRateLimit, cfg.UserRateWindow),
		allowedUsers:     allowedUsers,
		allowedChats:     allowedChats,
		rejectMessage:    rejectMsg,
		rateLimitMessage: rateLimitMsg,
		sessionService:   sessionService,
	}
}

// Name returns the channel identifier.
func (t *TelegramChannel) Name() string { return "telegram" }

// Start begins listening for Telegram updates via long-polling.
func (t *TelegramChannel) Start(ctx context.Context) error {
	bot, err := tgbotapi.NewBotAPI(t.token)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}
	t.bot = bot
	t.logger.Info("telegram bot started", "username", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := bot.GetUpdatesChan(u)

	// Periodic cleanup of rate limiter state.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.chatLimiter.cleanup()
				t.userLimiter.cleanup()
			}
		}
	}()

	go func() {
		defer close(t.messages)
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.Message == nil {
					continue
				}

				userID := update.Message.From.ID
				chatID := update.Message.Chat.ID

				if !t.isAuthorized(userID, chatID) {
					t.logger.Warn("rejected unauthorized message",
						"user_id", userID, "chat_id", chatID)
					t.sendText(chatID, t.rejectMessage)
					continue
				}

				if !t.userLimiter.allow(userID) {
					t.logger.Warn("user rate limited",
						"user_id", userID, "chat_id", chatID)
					t.sendText(chatID, t.rateLimitMessage)
					continue
				}

				// Handle /clear command
			if update.Message.IsCommand() && update.Message.Command() == "clear" {
				t.handleClear(ctx, update.Message)
				continue
			}

			msg := telegramUpdateToMessage(update)
				select {
				case t.messages <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return nil
}

// Stop gracefully shuts down the Telegram channel.
func (t *TelegramChannel) Stop(_ context.Context) error {
	if t.bot != nil {
		t.bot.StopReceivingUpdates()
	}
	return nil
}

// Messages returns the channel for receiving incoming messages.
func (t *TelegramChannel) Messages() <-chan *Message {
	return t.messages
}

// Send sends a response back through Telegram.
func (t *TelegramChannel) Send(_ context.Context, resp *Response) error {
	if t.bot == nil {
		return fmt.Errorf("telegram bot not initialized")
	}

	chatIDStr, ok := resp.Metadata["chat_id"].(string)
	if !ok {
		return fmt.Errorf("missing chat_id in response metadata")
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat_id: %w", err)
	}

	// Send typing indicator for non-final responses.
	if !resp.Done {
		t.sendTyping(chatID)
	}

	// Skip sending empty or intermediate non-text responses.
	if resp.Content == "" {
		return nil
	}

	var replyToID int
	if mid, ok := resp.Metadata["message_id"].(string); ok {
		if v, err := strconv.Atoi(mid); err == nil {
			replyToID = v
		}
	}

	formatted := formatForTelegram(resp.Content)
	chunks := chunkText(formatted, telegramMaxMessageLen)
	for i, chunk := range chunks {
		t.chatLimiter.wait(chatID)

		msg := tgbotapi.NewMessage(chatID, chunk)
		msg.ParseMode = "HTML"
		if i == 0 && replyToID != 0 {
			msg.ReplyToMessageID = replyToID
		}
		if _, err := t.bot.Send(msg); err != nil {
			return fmt.Errorf("send telegram message: %w", err)
		}
	}

	return nil
}

// isAuthorized returns true if the user or chat is allowed.
// If both allowlists are empty, all users are allowed.
func (t *TelegramChannel) isAuthorized(userID, chatID int64) bool {
	if len(t.allowedUsers) == 0 && len(t.allowedChats) == 0 {
		return true
	}
	return t.allowedUsers[userID] || t.allowedChats[chatID]
}

// sendTyping sends a "typing" chat action.
func (t *TelegramChannel) sendTyping(chatID int64) {
	if t.bot == nil {
		return
	}
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	if _, err := t.bot.Request(action); err != nil {
		t.logger.Warn("failed to send typing indicator", "error", err)
	}
}

// sendText sends a plain text message to a chat (used for reject/rate-limit notices).
func (t *TelegramChannel) sendText(chatID int64, text string) {
	if t.bot == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := t.bot.Send(msg); err != nil {
		t.logger.Warn("failed to send text message", "error", err)
	}
}

// handleClear processes the /clear command by deleting the current session.
func (t *TelegramChannel) handleClear(ctx context.Context, m *tgbotapi.Message) {
	chatID := m.Chat.ID

	if t.sessionService == nil {
		t.sendText(chatID, "Session clearing is not available.")
		return
	}

	var sessionID string
	if m.Chat.Type == "private" {
		sessionID = fmt.Sprintf("tg-dm-%d", m.From.ID)
	} else {
		sessionID = fmt.Sprintf("tg-group-%d", chatID)
	}

	userID := fmt.Sprintf("tg-%d", m.From.ID)
	key := trpcsession.Key{AppName: "kaggen", UserID: userID, SessionID: sessionID}

	if err := t.sessionService.DeleteSession(ctx, key); err != nil {
		t.logger.Warn("failed to clear session", "session_id", sessionID, "error", err)
		t.sendText(chatID, "Failed to clear session. Please try again.")
		return
	}

	t.logger.Info("session cleared via /clear", "session_id", sessionID, "user_id", userID)
	t.sendText(chatID, "Session cleared. Starting fresh!")
}

// telegramUpdateToMessage converts a Telegram update to a channel Message.
func telegramUpdateToMessage(update tgbotapi.Update) *Message {
	m := update.Message
	userID := fmt.Sprintf("tg-%d", m.From.ID)
	chatID := fmt.Sprintf("%d", m.Chat.ID)

	var sessionID string
	if m.Chat.Type == "private" {
		sessionID = fmt.Sprintf("tg-dm-%d", m.From.ID)
	} else {
		sessionID = fmt.Sprintf("tg-group-%d", m.Chat.ID)
	}

	return &Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    userID,
		Content:   m.Text,
		Channel:   "telegram",
		Metadata: map[string]any{
			"chat_id":    chatID,
			"message_id": fmt.Sprintf("%d", m.MessageID),
			"chat_type":  m.Chat.Type,
		},
	}
}

// chunkText splits text into chunks of at most maxLen characters,
// preferring to break at newlines.
func chunkText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		cutAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
			cutAt = idx + 1
		}

		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}

	return chunks
}
