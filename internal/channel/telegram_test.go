package channel

import (
	"log/slog"
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
	chunks := chunkText("hello", 4096)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("expected single chunk, got %v", chunks)
	}

	text := string(make([]byte, 4096))
	chunks = chunkText(text, 4096)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for exact boundary, got %d", len(chunks))
	}

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
	text := string(make([]byte, 4000)) + "\n" + string(make([]byte, 200))
	chunks := chunkText(text, 4096)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
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

// --- Markdown ---

func TestFormatForTelegram_PlainText(t *testing.T) {
	got := formatForTelegram("hello world")
	if got != "hello world" {
		t.Errorf("expected plain text unchanged, got %q", got)
	}
}

func TestFormatForTelegram_Bold(t *testing.T) {
	got := formatForTelegram("this is **bold** text")
	if got != "this is <b>bold</b> text" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForTelegram_Italic(t *testing.T) {
	got := formatForTelegram("this is *italic* text")
	if got != "this is <i>italic</i> text" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForTelegram_InlineCode(t *testing.T) {
	got := formatForTelegram("use `fmt.Println`")
	if got != "use <code>fmt.Println</code>" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForTelegram_CodeBlock(t *testing.T) {
	got := formatForTelegram("```go\nfmt.Println()\n```")
	if got != "<pre>fmt.Println()\n</pre>" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForTelegram_HTMLEscape(t *testing.T) {
	got := formatForTelegram("a < b > c & d")
	if got != "a &lt; b &gt; c &amp; d" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForTelegram_Empty(t *testing.T) {
	if got := formatForTelegram(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- Typing ---

func TestSendTyping_NilBot(t *testing.T) {
	tc := &TelegramChannel{logger: slog.Default()}
	// Should not panic with nil bot.
	tc.sendTyping(12345)
}

// --- Authorization ---

func TestIsAuthorized_EmptyAllowlist(t *testing.T) {
	tc := &TelegramChannel{
		allowedUsers: make(map[int64]bool),
		allowedChats: make(map[int64]bool),
	}
	if !tc.isAuthorized(123, 456) {
		t.Error("expected true when allowlists are empty")
	}
}

func TestIsAuthorized_UserAllowed(t *testing.T) {
	tc := &TelegramChannel{
		allowedUsers: map[int64]bool{123: true},
		allowedChats: make(map[int64]bool),
	}
	if !tc.isAuthorized(123, 999) {
		t.Error("expected true for allowed user")
	}
	if tc.isAuthorized(456, 999) {
		t.Error("expected false for non-allowed user")
	}
}

func TestIsAuthorized_ChatAllowed(t *testing.T) {
	tc := &TelegramChannel{
		allowedUsers: make(map[int64]bool),
		allowedChats: map[int64]bool{789: true},
	}
	if !tc.isAuthorized(999, 789) {
		t.Error("expected true for allowed chat")
	}
	if tc.isAuthorized(999, 123) {
		t.Error("expected false for non-allowed chat")
	}
}

// --- User rate limiter ---

func TestUserRateLimiter_Allow(t *testing.T) {
	rl := newUserRateLimiter(3, 60)
	uid := int64(123)

	for i := 0; i < 3; i++ {
		if !rl.allow(uid) {
			t.Errorf("message %d should be allowed", i+1)
		}
	}
	if rl.allow(uid) {
		t.Error("4th message should be denied")
	}
}

func TestUserRateLimiter_DifferentUsers(t *testing.T) {
	rl := newUserRateLimiter(1, 60)

	if !rl.allow(100) {
		t.Error("first user should be allowed")
	}
	if !rl.allow(200) {
		t.Error("second user should be allowed")
	}
	if rl.allow(100) {
		t.Error("first user 2nd message should be denied")
	}
}

func TestChatRateLimiter_Cleanup(t *testing.T) {
	rl := newChatRateLimiter()
	rl.lastSent[1] = rl.lastSent[1] // zero value
	rl.cleanup()
	if _, ok := rl.lastSent[1]; ok {
		t.Error("expected stale entry to be cleaned up")
	}
}
