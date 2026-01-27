package channel

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// telegramMaxMessageLen is Telegram's maximum message length.
	telegramMaxMessageLen = 4096
)

// TelegramChannel implements Channel for Telegram bots.
type TelegramChannel struct {
	token    string
	bot      *tgbotapi.BotAPI
	messages chan *Message
	logger   *slog.Logger
}

// NewTelegramChannel creates a new Telegram channel with the given bot token.
func NewTelegramChannel(token string, logger *slog.Logger) *TelegramChannel {
	return &TelegramChannel{
		token:    token,
		messages: make(chan *Message, 64),
		logger:   logger,
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

	// Get optional reply-to message ID
	var replyToID int
	if mid, ok := resp.Metadata["message_id"].(string); ok {
		if v, err := strconv.Atoi(mid); err == nil {
			replyToID = v
		}
	}

	// Chunk long messages
	chunks := chunkText(resp.Content, telegramMaxMessageLen)
	for i, chunk := range chunks {
		msg := tgbotapi.NewMessage(chatID, chunk)
		// Only set reply-to on the first chunk
		if i == 0 && replyToID != 0 {
			msg.ReplyToMessageID = replyToID
		}
		if _, err := t.bot.Send(msg); err != nil {
			return fmt.Errorf("send telegram message: %w", err)
		}
	}

	return nil
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

		// Try to break at a newline within the limit
		cutAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
			cutAt = idx + 1
		}

		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}

	return chunks
}
