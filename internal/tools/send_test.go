package tools

import (
	"context"
	"testing"

	"github.com/yourusername/kaggen/internal/trust"
)

// mockTelegramSender implements TelegramSender for testing.
type mockTelegramSender struct {
	connected bool
	sentTo    int64
	sentText  string
}

func (m *mockTelegramSender) SendText(chatID int64, text string) {
	m.sentTo = chatID
	m.sentText = text
}

func (m *mockTelegramSender) IsConnected() bool {
	return m.connected
}

// mockWhatsAppSender implements WhatsAppSender for testing.
type mockWhatsAppSender struct {
	connected bool
	sentTo    string
	sentText  string
	err       error
}

func (m *mockWhatsAppSender) SendText(phone string, text string) error {
	m.sentTo = phone
	m.sentText = text
	return m.err
}

func (m *mockWhatsAppSender) IsConnected() bool {
	return m.connected
}

func TestWithTrustTier(t *testing.T) {
	ctx := context.Background()

	// Default should be ThirdParty.
	tier := getTrustTierFromContext(ctx)
	if tier != trust.TrustTierThirdParty {
		t.Errorf("expected ThirdParty for empty context, got %s", tier)
	}

	// With Owner tier.
	ctx = WithTrustTier(ctx, trust.TrustTierOwner)
	tier = getTrustTierFromContext(ctx)
	if tier != trust.TrustTierOwner {
		t.Errorf("expected Owner, got %s", tier)
	}

	// With Authorized tier.
	ctx = WithTrustTier(ctx, trust.TrustTierAuthorized)
	tier = getTrustTierFromContext(ctx)
	if tier != trust.TrustTierAuthorized {
		t.Errorf("expected Authorized, got %s", tier)
	}
}

func TestSendTelegram_OwnerOnly(t *testing.T) {
	mock := &mockTelegramSender{connected: true}
	ts := NewSendToolSet(mock, nil, nil)

	// Third-party should be denied.
	ctx := WithTrustTier(context.Background(), trust.TrustTierThirdParty)
	resp, err := ts.sendTelegram(ctx, sendTelegramRequest{ChatID: 123, Message: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected third-party to be denied")
	}
	if mock.sentTo != 0 {
		t.Error("should not have sent message")
	}

	// Authorized should be denied.
	ctx = WithTrustTier(context.Background(), trust.TrustTierAuthorized)
	resp, err = ts.sendTelegram(ctx, sendTelegramRequest{ChatID: 123, Message: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected authorized to be denied")
	}

	// Owner should succeed.
	ctx = WithTrustTier(context.Background(), trust.TrustTierOwner)
	resp, err = ts.sendTelegram(ctx, sendTelegramRequest{ChatID: 123, Message: "Hello!"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success, got error: %s", resp.Error)
	}
	if mock.sentTo != 123 {
		t.Errorf("expected chatID 123, got %d", mock.sentTo)
	}
	if mock.sentText != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", mock.sentText)
	}
}

func TestSendTelegram_NotConnected(t *testing.T) {
	mock := &mockTelegramSender{connected: false}
	ts := NewSendToolSet(mock, nil, nil)

	ctx := WithTrustTier(context.Background(), trust.TrustTierOwner)
	resp, err := ts.sendTelegram(ctx, sendTelegramRequest{ChatID: 123, Message: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure when not connected")
	}
	if resp.Error != "Telegram channel not connected" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestSendTelegram_EmptyMessage(t *testing.T) {
	mock := &mockTelegramSender{connected: true}
	ts := NewSendToolSet(mock, nil, nil)

	ctx := WithTrustTier(context.Background(), trust.TrustTierOwner)
	resp, err := ts.sendTelegram(ctx, sendTelegramRequest{ChatID: 123, Message: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure for empty message")
	}
	if resp.Error != "Message cannot be empty" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestSendWhatsApp_OwnerOnly(t *testing.T) {
	mock := &mockWhatsAppSender{connected: true}
	ts := NewSendToolSet(nil, mock, nil)

	// Third-party should be denied.
	ctx := WithTrustTier(context.Background(), trust.TrustTierThirdParty)
	resp, err := ts.sendWhatsApp(ctx, sendWhatsAppRequest{Phone: "+1234567890", Message: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected third-party to be denied")
	}

	// Owner should succeed.
	ctx = WithTrustTier(context.Background(), trust.TrustTierOwner)
	resp, err = ts.sendWhatsApp(ctx, sendWhatsAppRequest{Phone: "+1234567890", Message: "Hello!"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success, got error: %s", resp.Error)
	}
	if mock.sentTo != "+1234567890" {
		t.Errorf("expected phone +1234567890, got %s", mock.sentTo)
	}
	if mock.sentText != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", mock.sentText)
	}
}

func TestSendWhatsApp_NotConnected(t *testing.T) {
	mock := &mockWhatsAppSender{connected: false}
	ts := NewSendToolSet(nil, mock, nil)

	ctx := WithTrustTier(context.Background(), trust.TrustTierOwner)
	resp, err := ts.sendWhatsApp(ctx, sendWhatsAppRequest{Phone: "+1234567890", Message: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected failure when not connected")
	}
	if resp.Error != "WhatsApp channel not connected" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestToolsReturned(t *testing.T) {
	// Both channels available.
	ts := NewSendToolSet(&mockTelegramSender{}, &mockWhatsAppSender{}, nil)
	tools := ts.Tools()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	// Only Telegram.
	ts = NewSendToolSet(&mockTelegramSender{}, nil, nil)
	tools = ts.Tools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}

	// Neither channel.
	ts = NewSendToolSet(nil, nil, nil)
	tools = ts.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}
