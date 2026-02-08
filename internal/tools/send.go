// Package tools provides agent tools for the kaggen system.
package tools

import (
	"context"
	"fmt"
	"log/slog"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/trust"
)

// TelegramSender is the interface required for sending Telegram messages.
type TelegramSender interface {
	SendText(chatID int64, text string)
	IsConnected() bool
}

// WhatsAppSender is the interface required for sending WhatsApp messages.
type WhatsAppSender interface {
	SendText(phone string, text string) error
	IsConnected() bool
}

// SendToolSet provides tools for proactive message sending to Telegram and WhatsApp.
// These tools are owner-only and require the sender to have TrustTierOwner.
type SendToolSet struct {
	telegram TelegramSender
	whatsapp WhatsAppSender
	logger   *slog.Logger
}

// NewSendToolSet creates a new send tool set.
func NewSendToolSet(telegram TelegramSender, whatsapp WhatsAppSender, logger *slog.Logger) *SendToolSet {
	if logger == nil {
		logger = slog.Default()
	}
	return &SendToolSet{
		telegram: telegram,
		whatsapp: whatsapp,
		logger:   logger,
	}
}

// Tools returns the tools in this set.
func (ts *SendToolSet) Tools() []tool.Tool {
	var tools []tool.Tool
	if ts.telegram != nil {
		tools = append(tools, ts.sendTelegramTool())
	}
	if ts.whatsapp != nil {
		tools = append(tools, ts.sendWhatsAppTool())
	}
	return tools
}

// --- Send Telegram Tool ---

type sendTelegramRequest struct {
	ChatID  int64  `json:"chat_id" jsonschema:"required,description=Telegram chat ID to send the message to. Use a negative number for group chats."`
	Message string `json:"message" jsonschema:"required,description=The message text to send. Supports Markdown formatting."`
}

type sendTelegramResponse struct {
	Success bool   `json:"success"`
	ChatID  int64  `json:"chat_id"`
	Error   string `json:"error,omitempty"`
}

func (ts *SendToolSet) sendTelegramTool() tool.Tool {
	return function.NewFunctionTool(
		ts.sendTelegram,
		function.WithName("send_telegram"),
		function.WithDescription("Send a message to a Telegram user or group. OWNER-ONLY: This tool can only be used by the bot owner to proactively reach out to third parties. Use this for alerts, notifications, or when the user explicitly asks you to message someone."),
	)
}

func (ts *SendToolSet) sendTelegram(ctx context.Context, req sendTelegramRequest) (*sendTelegramResponse, error) {
	// Check trust tier from invocation context.
	tier := getTrustTierFromContext(ctx)
	if !tier.CanSendToOthers() {
		ts.logger.Warn("send_telegram denied: insufficient trust tier",
			"trust_tier", tier.String(),
			"chat_id", req.ChatID)
		return &sendTelegramResponse{
			Success: false,
			ChatID:  req.ChatID,
			Error:   "Permission denied: only the bot owner can use send_telegram",
		}, nil
	}

	if ts.telegram == nil {
		return &sendTelegramResponse{
			Success: false,
			ChatID:  req.ChatID,
			Error:   "Telegram channel not configured",
		}, nil
	}

	if !ts.telegram.IsConnected() {
		return &sendTelegramResponse{
			Success: false,
			ChatID:  req.ChatID,
			Error:   "Telegram channel not connected",
		}, nil
	}

	if req.Message == "" {
		return &sendTelegramResponse{
			Success: false,
			ChatID:  req.ChatID,
			Error:   "Message cannot be empty",
		}, nil
	}

	ts.logger.Info("sending telegram message",
		"chat_id", req.ChatID,
		"message_length", len(req.Message))

	ts.telegram.SendText(req.ChatID, req.Message)

	return &sendTelegramResponse{
		Success: true,
		ChatID:  req.ChatID,
	}, nil
}

// --- Send WhatsApp Tool ---

type sendWhatsAppRequest struct {
	Phone   string `json:"phone" jsonschema:"required,description=Phone number with country code (e.g. '+1234567890' or '1234567890'). The '+' prefix is optional."`
	Message string `json:"message" jsonschema:"required,description=The message text to send."`
}

type sendWhatsAppResponse struct {
	Success bool   `json:"success"`
	Phone   string `json:"phone"`
	Error   string `json:"error,omitempty"`
}

func (ts *SendToolSet) sendWhatsAppTool() tool.Tool {
	return function.NewFunctionTool(
		ts.sendWhatsApp,
		function.WithName("send_whatsapp"),
		function.WithDescription("Send a WhatsApp message to a phone number. OWNER-ONLY: This tool can only be used by the bot owner to proactively reach out to third parties. Use this for alerts, notifications, or when the user explicitly asks you to message someone."),
	)
}

func (ts *SendToolSet) sendWhatsApp(ctx context.Context, req sendWhatsAppRequest) (*sendWhatsAppResponse, error) {
	// Check trust tier from invocation context.
	tier := getTrustTierFromContext(ctx)
	if !tier.CanSendToOthers() {
		ts.logger.Warn("send_whatsapp denied: insufficient trust tier",
			"trust_tier", tier.String(),
			"phone", req.Phone)
		return &sendWhatsAppResponse{
			Success: false,
			Phone:   req.Phone,
			Error:   "Permission denied: only the bot owner can use send_whatsapp",
		}, nil
	}

	if ts.whatsapp == nil {
		return &sendWhatsAppResponse{
			Success: false,
			Phone:   req.Phone,
			Error:   "WhatsApp channel not configured",
		}, nil
	}

	if !ts.whatsapp.IsConnected() {
		return &sendWhatsAppResponse{
			Success: false,
			Phone:   req.Phone,
			Error:   "WhatsApp channel not connected",
		}, nil
	}

	if req.Message == "" {
		return &sendWhatsAppResponse{
			Success: false,
			Phone:   req.Phone,
			Error:   "Message cannot be empty",
		}, nil
	}

	ts.logger.Info("sending whatsapp message",
		"phone", req.Phone,
		"message_length", len(req.Message))

	if err := ts.whatsapp.SendText(req.Phone, req.Message); err != nil {
		return &sendWhatsAppResponse{
			Success: false,
			Phone:   req.Phone,
			Error:   fmt.Sprintf("Failed to send: %v", err),
		}, nil
	}

	return &sendWhatsAppResponse{
		Success: true,
		Phone:   req.Phone,
	}, nil
}

// trustTierContextKey is the context key for trust tier.
type trustTierContextKey struct{}

// getTrustTierFromContext extracts the trust tier from the context.
// If not found, defaults to ThirdParty (most restrictive) for safety.
func getTrustTierFromContext(ctx context.Context) trust.TrustTier {
	if tier, ok := ctx.Value(trustTierContextKey{}).(trust.TrustTier); ok {
		return tier
	}
	return trust.TrustTierThirdParty
}

// WithTrustTier returns a new context with the trust tier set.
// The handler should call this before invoking the agent.
func WithTrustTier(ctx context.Context, tier trust.TrustTier) context.Context {
	return context.WithValue(ctx, trustTierContextKey{}, tier)
}
