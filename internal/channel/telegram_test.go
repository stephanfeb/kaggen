package channel

import (
	"os"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/yourusername/kaggen/internal/config"
)

func TestTelegramSessionID_DM(t *testing.T) {
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			From:      &tgbotapi.User{ID: 42},
			Chat:      &tgbotapi.Chat{ID: 42, Type: "private"},
			Text:      "hello",
		},
	}

	msg := telegramUpdateToMessage(update)

	if msg.SessionID != "tg-dm-42" {
		t.Errorf("expected session ID tg-dm-42, got %s", msg.SessionID)
	}
	if msg.UserID != "tg-42" {
		t.Errorf("expected user ID tg-42, got %s", msg.UserID)
	}
	if msg.Channel != "telegram" {
		t.Errorf("expected channel telegram, got %s", msg.Channel)
	}
}

func TestTelegramSessionID_Group(t *testing.T) {
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 2,
			From:      &tgbotapi.User{ID: 42},
			Chat:      &tgbotapi.Chat{ID: -100123, Type: "supergroup"},
			Text:      "hello group",
		},
	}

	msg := telegramUpdateToMessage(update)

	if msg.SessionID != "tg-group--100123" {
		t.Errorf("expected session ID tg-group--100123, got %s", msg.SessionID)
	}
	if msg.UserID != "tg-42" {
		t.Errorf("expected user ID tg-42, got %s", msg.UserID)
	}
}

func TestTelegramMessageMetadata(t *testing.T) {
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 99,
			From:      &tgbotapi.User{ID: 7},
			Chat:      &tgbotapi.Chat{ID: 7, Type: "private"},
			Text:      "test",
		},
	}

	msg := telegramUpdateToMessage(update)

	if msg.Metadata["chat_id"] != "7" {
		t.Errorf("expected chat_id 7, got %v", msg.Metadata["chat_id"])
	}
	if msg.Metadata["message_id"] != "99" {
		t.Errorf("expected message_id 99, got %v", msg.Metadata["message_id"])
	}
	if msg.Metadata["chat_type"] != "private" {
		t.Errorf("expected chat_type private, got %v", msg.Metadata["chat_type"])
	}
}

func TestChunkText(t *testing.T) {
	// Short text — no chunking
	chunks := chunkText("hello", 4096)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("expected single chunk, got %v", chunks)
	}

	// Exact boundary
	text := string(make([]byte, 4096))
	chunks = chunkText(text, 4096)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for exact boundary, got %d", len(chunks))
	}

	// Over boundary — should split
	long := string(make([]byte, 5000))
	chunks = chunkText(long, 4096)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 4096 {
		t.Errorf("first chunk should be 4096, got %d", len(chunks[0]))
	}
	if len(chunks[1]) != 904 {
		t.Errorf("second chunk should be 904, got %d", len(chunks[1]))
	}
}

func TestChunkText_PreferNewline(t *testing.T) {
	// Build text with a newline before the limit
	text := string(make([]byte, 4000)) + "\n" + string(make([]byte, 200))
	chunks := chunkText(text, 4096)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
	// First chunk should break at the newline (4001 chars including newline)
	if len(chunks[0]) != 4001 {
		t.Errorf("expected first chunk to break at newline (4001), got %d", len(chunks[0]))
	}
}

func TestTelegramBotToken_ConfigOverEnv(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Telegram.BotToken = "from-config"
	t.Setenv("TELEGRAM_BOT_TOKEN", "from-env")

	got := cfg.TelegramBotToken()
	if got != "from-config" {
		t.Errorf("expected from-config, got %s", got)
	}
}

func TestTelegramBotToken_FallbackToEnv(t *testing.T) {
	cfg := config.DefaultConfig()
	t.Setenv("TELEGRAM_BOT_TOKEN", "from-env")

	got := cfg.TelegramBotToken()
	if got != "from-env" {
		t.Errorf("expected from-env, got %s", got)
	}
}

func TestTelegramBotToken_Empty(t *testing.T) {
	cfg := config.DefaultConfig()
	os.Unsetenv("TELEGRAM_BOT_TOKEN")

	got := cfg.TelegramBotToken()
	if got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}
