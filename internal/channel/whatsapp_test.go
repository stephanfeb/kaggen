package channel

import (
	"log/slog"
	"testing"

	"github.com/yourusername/kaggen/internal/config"
)

func TestWhatsAppName(t *testing.T) {
	wc := &WhatsAppChannel{}
	if wc.Name() != "whatsapp" {
		t.Errorf("expected whatsapp, got %s", wc.Name())
	}
}

func TestWhatsAppIsConnected_Default(t *testing.T) {
	wc := &WhatsAppChannel{}
	if wc.IsConnected() {
		t.Error("expected disconnected by default")
	}
}

// --- Authorization ---

func TestWhatsAppIsAuthorized_EmptyAllowlist(t *testing.T) {
	wc := &WhatsAppChannel{
		allowedPhones: make(map[string]bool),
		allowedGroups: make(map[string]bool),
	}
	if !wc.isAuthorized("1234567890", "group@g.us") {
		t.Error("expected true when allowlists are empty")
	}
}

func TestWhatsAppIsAuthorized_PhoneAllowed(t *testing.T) {
	wc := &WhatsAppChannel{
		allowedPhones: map[string]bool{"1234567890": true},
		allowedGroups: make(map[string]bool),
	}
	if !wc.isAuthorized("1234567890", "somegroup@g.us") {
		t.Error("expected true for allowed phone")
	}
	if wc.isAuthorized("9876543210", "somegroup@g.us") {
		t.Error("expected false for non-allowed phone")
	}
}

func TestWhatsAppIsAuthorized_GroupAllowed(t *testing.T) {
	wc := &WhatsAppChannel{
		allowedPhones: make(map[string]bool),
		allowedGroups: map[string]bool{"mygroup@g.us": true},
	}
	if !wc.isAuthorized("anyone", "mygroup@g.us") {
		t.Error("expected true for allowed group")
	}
	if wc.isAuthorized("anyone", "othergroup@g.us") {
		t.Error("expected false for non-allowed group")
	}
}

// --- Phone rate limiter ---

func TestPhoneRateLimiter_Allow(t *testing.T) {
	rl := newPhoneRateLimiter(3, 60)
	phone := "1234567890"

	for i := 0; i < 3; i++ {
		if !rl.allow(phone) {
			t.Errorf("message %d should be allowed", i+1)
		}
	}
	if rl.allow(phone) {
		t.Error("4th message should be denied")
	}
}

func TestPhoneRateLimiter_DifferentPhones(t *testing.T) {
	rl := newPhoneRateLimiter(1, 60)

	if !rl.allow("111") {
		t.Error("first phone should be allowed")
	}
	if !rl.allow("222") {
		t.Error("second phone should be allowed")
	}
	if rl.allow("111") {
		t.Error("first phone 2nd message should be denied")
	}
}

func TestPhoneRateLimiter_Cleanup(t *testing.T) {
	rl := newPhoneRateLimiter(10, 60)
	rl.timestamps["old"] = nil // empty slice simulating old entries
	rl.cleanup()
	if _, ok := rl.timestamps["old"]; ok {
		t.Error("expected stale entry to be cleaned up")
	}
}

func TestChatRateLimiterWA_Cleanup(t *testing.T) {
	rl := newChatRateLimiterWA()
	rl.lastSent["old@s.whatsapp.net"] = rl.lastSent["old@s.whatsapp.net"] // zero time
	rl.cleanup()
	if _, ok := rl.lastSent["old@s.whatsapp.net"]; ok {
		t.Error("expected stale entry to be cleaned up")
	}
}

// --- Markdown formatting ---

func TestFormatForWhatsApp_PlainText(t *testing.T) {
	got := formatForWhatsApp("hello world")
	if got != "hello world" {
		t.Errorf("expected plain text unchanged, got %q", got)
	}
}

func TestFormatForWhatsApp_Bold(t *testing.T) {
	got := formatForWhatsApp("this is **bold** text")
	if got != "this is *bold* text" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForWhatsApp_InlineCode(t *testing.T) {
	got := formatForWhatsApp("use `fmt.Println`")
	if got != "use `fmt.Println`" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForWhatsApp_CodeBlock(t *testing.T) {
	got := formatForWhatsApp("```go\nfmt.Println()\n```")
	if got != "```fmt.Println()\n```" {
		t.Errorf("got %q", got)
	}
}

func TestFormatForWhatsApp_Empty(t *testing.T) {
	if got := formatForWhatsApp(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- Constructor ---

func TestNewWhatsAppChannel_Defaults(t *testing.T) {
	cfg := &config.WhatsAppConfig{}
	wc := NewWhatsAppChannel(cfg, "/tmp/test.db", "Test Bot", nil, slog.Default())

	if wc.rejectMessage == "" {
		t.Error("expected default reject message")
	}
	if wc.rateLimitMessage == "" {
		t.Error("expected default rate limit message")
	}
	if wc.dbPath != "/tmp/test.db" {
		t.Errorf("expected /tmp/test.db, got %s", wc.dbPath)
	}
	if wc.deviceName != "Test Bot" {
		t.Errorf("expected device name 'Test Bot', got %s", wc.deviceName)
	}
}

func TestNewWhatsAppChannel_PhoneNormalization(t *testing.T) {
	cfg := &config.WhatsAppConfig{
		AllowedPhones: []string{"+1234567890", "9876543210"},
	}
	wc := NewWhatsAppChannel(cfg, "/tmp/test.db", "Test Bot", nil, slog.Default())

	// Phone with + prefix should be normalized.
	if !wc.allowedPhones["1234567890"] {
		t.Error("expected +1234567890 to be normalized to 1234567890")
	}
	// Phone without + should be kept as-is.
	if !wc.allowedPhones["9876543210"] {
		t.Error("expected 9876543210 to be present")
	}
}

// --- Config ---

func TestWhatsAppSessionDBPath_Default(t *testing.T) {
	cfg := config.DefaultConfig()
	path := cfg.WhatsAppSessionDBPath()
	if path == "" {
		t.Error("expected non-empty default path")
	}
}

func TestWhatsAppSessionDBPath_Custom(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.WhatsApp.SessionDBPath = "/custom/path.db"
	path := cfg.WhatsAppSessionDBPath()
	if path != "/custom/path.db" {
		t.Errorf("expected /custom/path.db, got %s", path)
	}
}
