package channel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/yourusername/kaggen/internal/config"

	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// whatsappMaxMessageLen is a practical limit for WhatsApp messages.
	whatsappMaxMessageLen = 4000
)

// WhatsAppChannel implements Channel for WhatsApp messaging.
type WhatsAppChannel struct {
	client           *whatsmeow.Client
	container        *sqlstore.Container
	messages         chan *Message
	logger           *slog.Logger
	chatLimiter      *chatRateLimiterWA
	phoneLimiter     *phoneRateLimiter
	allowedPhones    map[string]bool
	allowedGroups    map[string]bool
	rejectMessage    string
	rateLimitMessage string
	sessionService   trpcsession.Service
	sttBaseURL       string
	dbPath           string
	deviceName       string
	// msgEventMap maps WhatsApp message_id -> event ID for thread reply detection.
	msgEventMap sync.Map // string -> string
	// qrChan receives QR code events for pairing.
	qrChan chan string
	// connected indicates if we're connected to WhatsApp.
	connected bool
	mu        sync.RWMutex
}

// NewWhatsAppChannel creates a new WhatsApp channel.
// sessionService is optional; when provided, the /clear command can reset sessions.
func NewWhatsAppChannel(cfg *config.WhatsAppConfig, dbPath, deviceName string, sessionService trpcsession.Service, logger *slog.Logger, sttBaseURL ...string) *WhatsAppChannel {
	allowedPhones := make(map[string]bool, len(cfg.AllowedPhones))
	for _, phone := range cfg.AllowedPhones {
		// Normalize phone numbers (remove + prefix for matching).
		normalized := strings.TrimPrefix(phone, "+")
		allowedPhones[normalized] = true
	}
	allowedGroups := make(map[string]bool, len(cfg.AllowedGroups))
	for _, group := range cfg.AllowedGroups {
		allowedGroups[group] = true
	}

	rejectMsg := cfg.RejectMessage
	if rejectMsg == "" {
		rejectMsg = "Sorry, you are not authorized to use this bot."
	}
	rateLimitMsg := cfg.RateLimitMessage
	if rateLimitMsg == "" {
		rateLimitMsg = "You're sending messages too quickly. Please wait a moment."
	}

	var sttURL string
	if len(sttBaseURL) > 0 && sttBaseURL[0] != "" {
		sttURL = sttBaseURL[0]
	}

	return &WhatsAppChannel{
		messages:         make(chan *Message, 64),
		logger:           logger,
		chatLimiter:      newChatRateLimiterWA(),
		phoneLimiter:     newPhoneRateLimiter(cfg.UserRateLimit, cfg.UserRateWindow),
		allowedPhones:    allowedPhones,
		allowedGroups:    allowedGroups,
		rejectMessage:    rejectMsg,
		rateLimitMessage: rateLimitMsg,
		sessionService:   sessionService,
		sttBaseURL:       sttURL,
		dbPath:           dbPath,
		deviceName:       deviceName,
		qrChan:           make(chan string, 1),
	}
}

// Name returns the channel identifier.
func (w *WhatsAppChannel) Name() string { return "whatsapp" }

// Start begins listening for WhatsApp messages.
func (w *WhatsAppChannel) Start(ctx context.Context) error {
	// Ensure database directory exists.
	dbDir := filepath.Dir(w.dbPath)
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		return fmt.Errorf("create whatsapp db directory: %w", err)
	}

	// Create SQLite container for session storage.
	dbLog := waLog.Stdout("Database", "WARN", true)
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+w.dbPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return fmt.Errorf("create whatsapp store: %w", err)
	}
	w.container = container

	// Set device name that appears in WhatsApp's "Linked Devices" list.
	if w.deviceName != "" {
		store.DeviceProps.Os = proto.String(w.deviceName)
	}

	// Get or create device store.
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get whatsapp device: %w", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)
	w.client = client

	// Register event handler.
	client.AddEventHandler(w.eventHandler)

	// Connect to WhatsApp.
	if client.Store.ID == nil {
		// No ID stored - need to log in via QR code.
		qrChan, _ := client.GetQRChannel(ctx)
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect whatsapp: %w", err)
		}

		// Handle QR code events.
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					w.logger.Info("whatsapp QR code received - scan with WhatsApp to link")
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				} else {
					w.logger.Info("whatsapp login event", "event", evt.Event)
				}
			}
		}()
	} else {
		// Already logged in, just connect.
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect whatsapp: %w", err)
		}
		w.logger.Info("whatsapp connected", "jid", client.Store.ID.String())
	}

	// Periodic cleanup of rate limiter state.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.chatLimiter.cleanup()
				w.phoneLimiter.cleanup()
			}
		}
	}()

	return nil
}

// Stop gracefully shuts down the WhatsApp channel.
func (w *WhatsAppChannel) Stop(_ context.Context) error {
	if w.client != nil {
		w.client.Disconnect()
	}
	if w.container != nil {
		return w.container.Close()
	}
	return nil
}

// Messages returns the channel for receiving incoming messages.
func (w *WhatsAppChannel) Messages() <-chan *Message {
	return w.messages
}

// Send sends a response back through WhatsApp.
func (w *WhatsAppChannel) Send(ctx context.Context, resp *Response) error {
	if w.client == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}

	chatJIDStr, ok := resp.Metadata["chat_jid"].(string)
	if !ok {
		return fmt.Errorf("missing chat_jid in response metadata")
	}
	chatJID, err := types.ParseJID(chatJIDStr)
	if err != nil {
		return fmt.Errorf("invalid chat_jid: %w", err)
	}

	// Skip tool calls, tool results, and empty responses — only send
	// user-facing text and error messages to WhatsApp.
	switch resp.Type {
	case "tool_call", "tool_result":
		return nil
	}
	if resp.Content == "" {
		return nil
	}

	formatted := formatForWhatsApp(resp.Content)
	chunks := chunkText(formatted, whatsappMaxMessageLen)

	for _, chunk := range chunks {
		w.chatLimiter.wait(chatJID.String())

		msg := &waE2E.Message{
			Conversation: proto.String(chunk),
		}

		sentResp, err := w.client.SendMessage(ctx, chatJID, msg)
		if err != nil {
			return fmt.Errorf("send whatsapp message: %w", err)
		}

		// Store message_id -> event_id mapping for thread detection.
		if eventID, ok := resp.Metadata["event_id"].(string); ok && eventID != "" {
			w.msgEventMap.Store(sentResp.ID, eventID)
		}
	}

	return nil
}

// eventHandler processes WhatsApp events.
func (w *WhatsAppChannel) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		w.mu.Lock()
		w.connected = true
		w.mu.Unlock()
		w.logger.Info("whatsapp connected")

	case *events.Disconnected:
		w.mu.Lock()
		w.connected = false
		w.mu.Unlock()
		w.logger.Warn("whatsapp disconnected")

	case *events.LoggedOut:
		w.mu.Lock()
		w.connected = false
		w.mu.Unlock()
		w.logger.Error("whatsapp logged out - re-pairing required")

	case *events.Message:
		w.handleMessage(v)
	}
}

// handleMessage processes an incoming WhatsApp message.
func (w *WhatsAppChannel) handleMessage(evt *events.Message) {
	// Skip messages from ourselves.
	if evt.Info.IsFromMe {
		return
	}

	senderJID := evt.Info.Sender
	chatJID := evt.Info.Chat
	senderPhone := senderJID.User

	// Authorization check.
	if !w.isAuthorized(senderPhone, chatJID.String()) {
		w.logger.Warn("rejected unauthorized message",
			"sender", senderPhone, "chat", chatJID.String())
		w.sendText(chatJID, w.rejectMessage)
		return
	}

	// Rate limiting.
	if !w.phoneLimiter.allow(senderPhone) {
		w.logger.Warn("user rate limited", "sender", senderPhone)
		w.sendText(chatJID, w.rateLimitMessage)
		return
	}

	// Extract content from various message types.
	content := w.extractContent(evt)

	// Handle commands.
	if strings.HasPrefix(content, "/clear") {
		w.handleClear(context.Background(), evt)
		return
	}
	if strings.HasPrefix(content, "/compact") {
		w.handleCompact(context.Background(), evt)
		return
	}

	// Build session ID.
	var sessionID string
	if chatJID.Server == types.DefaultUserServer {
		// DM (1:1 chat).
		sessionID = fmt.Sprintf("wa-dm-%s", senderPhone)
	} else {
		// Group chat.
		sessionID = fmt.Sprintf("wa-group-%s", chatJID.User)
	}

	msg := &Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    fmt.Sprintf("wa-%s", senderPhone),
		Content:   content,
		Channel:   "whatsapp",
		Metadata: map[string]any{
			"chat_jid":   chatJID.String(),
			"sender_jid": senderJID.String(),
			"message_id": evt.Info.ID,
			"is_group":   chatJID.Server != types.DefaultUserServer,
			"push_name":  evt.Info.PushName,
		},
		// Trust tier classification fields.
		SenderPhone:   senderPhone,
		IsInAllowlist: w.isInAllowlist(senderPhone, chatJID.String()),
	}

	// Detect reply-to-bot-message for threading.
	if evt.Message.GetExtendedTextMessage() != nil {
		ctxInfo := evt.Message.GetExtendedTextMessage().GetContextInfo()
		if ctxInfo != nil && ctxInfo.StanzaID != nil {
			if eventID, ok := w.msgEventMap.Load(*ctxInfo.StanzaID); ok {
				msg.ReplyToEventID = eventID.(string)
				w.logger.Info("whatsapp thread detected",
					"reply_to_msg", *ctxInfo.StanzaID,
					"event_id", eventID)
			}
		}
	}

	// Download attachments.
	msg.Attachments = w.downloadAttachments(evt)

	// Append attachment paths to content so the agent sees them.
	for _, att := range msg.Attachments {
		msg.Content += fmt.Sprintf("\n[Attached: %s]", att.Path)
	}

	select {
	case w.messages <- msg:
	default:
		w.logger.Warn("message queue full, dropping message")
	}
}

// extractContent extracts text content from various message types.
func (w *WhatsAppChannel) extractContent(evt *events.Message) string {
	msg := evt.Message

	// Regular text message.
	if msg.GetConversation() != "" {
		return msg.GetConversation()
	}

	// Extended text message (with formatting, links, etc.).
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}

	// Image with caption.
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption()
	}

	// Video with caption.
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}

	// Document with caption.
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}

	return ""
}

// downloadAttachments downloads media attachments from the message.
func (w *WhatsAppChannel) downloadAttachments(evt *events.Message) []Attachment {
	var attachments []Attachment
	msg := evt.Message

	// Download image.
	if img := msg.GetImageMessage(); img != nil {
		if att := w.downloadMedia(img, "image.jpg", img.GetMimetype()); att != nil {
			attachments = append(attachments, *att)
		}
	}

	// Download document.
	if doc := msg.GetDocumentMessage(); doc != nil {
		fileName := doc.GetFileName()
		if fileName == "" {
			fileName = "document"
		}
		if att := w.downloadMedia(doc, fileName, doc.GetMimetype()); att != nil {
			attachments = append(attachments, *att)
		}
	}

	// Download audio.
	if audio := msg.GetAudioMessage(); audio != nil {
		fileName := "audio.ogg"
		if att := w.downloadMedia(audio, fileName, audio.GetMimetype()); att != nil {
			attachments = append(attachments, *att)
		}
	}

	return attachments
}

// downloadMedia downloads a media message and saves it locally.
func (w *WhatsAppChannel) downloadMedia(msg whatsmeow.DownloadableMessage, fileName, mimeType string) *Attachment {
	data, err := w.client.Download(context.Background(), msg)
	if err != nil {
		w.logger.Warn("failed to download media", "error", err)
		return nil
	}

	dir, err := downloadsDir()
	if err != nil {
		w.logger.Warn("failed to create downloads dir", "error", err)
		return nil
	}

	localName := fmt.Sprintf("%s_%s", uuid.New().String()[:8], fileName)
	localPath := filepath.Join(dir, localName)

	if err := os.WriteFile(localPath, data, 0600); err != nil {
		w.logger.Warn("failed to write media file", "error", err)
		return nil
	}

	w.logger.Info("downloaded whatsapp media", "path", localPath, "size", len(data))

	return &Attachment{
		Path:     localPath,
		MimeType: mimeType,
		FileName: fileName,
	}
}

// isAuthorized returns true if the phone or group is allowed.
// If both allowlists are empty, all users are allowed.
func (w *WhatsAppChannel) isAuthorized(phone, chatJID string) bool {
	if len(w.allowedPhones) == 0 && len(w.allowedGroups) == 0 {
		return true
	}
	return w.isInAllowlist(phone, chatJID)
}

// isInAllowlist returns true if the phone or group is explicitly in the allowlist.
// Unlike isAuthorized, returns false when allowlists are empty (open mode).
func (w *WhatsAppChannel) isInAllowlist(phone, chatJID string) bool {
	if len(w.allowedPhones) == 0 && len(w.allowedGroups) == 0 {
		return false // Open mode - no explicit allowlist
	}
	return w.allowedPhones[phone] || w.allowedGroups[chatJID]
}

// sendText sends a plain text message to a JID.
func (w *WhatsAppChannel) sendText(jid types.JID, text string) {
	if w.client == nil {
		return
	}
	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}
	if _, err := w.client.SendMessage(context.Background(), jid, msg); err != nil {
		w.logger.Warn("failed to send text message", "error", err)
	}
}

// handleClear processes the /clear command by deleting the current session.
func (w *WhatsAppChannel) handleClear(ctx context.Context, evt *events.Message) {
	chatJID := evt.Info.Chat
	senderPhone := evt.Info.Sender.User

	if w.sessionService == nil {
		w.sendText(chatJID, "Session clearing is not available.")
		return
	}

	var sessionID string
	if chatJID.Server == types.DefaultUserServer {
		sessionID = fmt.Sprintf("wa-dm-%s", senderPhone)
	} else {
		sessionID = fmt.Sprintf("wa-group-%s", chatJID.User)
	}

	userID := fmt.Sprintf("wa-%s", senderPhone)
	key := trpcsession.Key{AppName: "kaggen", UserID: userID, SessionID: sessionID}

	if err := w.sessionService.DeleteSession(ctx, key); err != nil {
		w.logger.Warn("failed to clear session", "session_id", sessionID, "error", err)
		w.sendText(chatJID, "Failed to clear session. Please try again.")
		return
	}

	w.logger.Info("session cleared via /clear", "session_id", sessionID, "user_id", userID)
	w.sendText(chatJID, "Session cleared. Starting fresh!")
}

// handleCompact processes the /compact command by summarizing and truncating the session.
func (w *WhatsAppChannel) handleCompact(ctx context.Context, evt *events.Message) {
	chatJID := evt.Info.Chat
	senderPhone := evt.Info.Sender.User

	if w.sessionService == nil {
		w.sendText(chatJID, "Session compaction is not available.")
		return
	}

	var sessionID string
	if chatJID.Server == types.DefaultUserServer {
		sessionID = fmt.Sprintf("wa-dm-%s", senderPhone)
	} else {
		sessionID = fmt.Sprintf("wa-group-%s", chatJID.User)
	}

	userID := fmt.Sprintf("wa-%s", senderPhone)
	key := trpcsession.Key{AppName: "kaggen", UserID: userID, SessionID: sessionID}

	w.sendText(chatJID, "Compacting session... this may take a moment.")

	sess, err := w.sessionService.GetSession(ctx, key)
	if err != nil || sess == nil {
		w.sendText(chatJID, "No session to compact.")
		return
	}

	if err := w.sessionService.CreateSessionSummary(ctx, sess, "", true); err != nil {
		w.logger.Warn("failed to compact session", "session_id", sessionID, "error", err)
		w.sendText(chatJID, fmt.Sprintf("Failed to compact session: %v", err))
		return
	}

	w.logger.Info("session compacted via /compact", "session_id", sessionID, "user_id", userID)
	w.sendText(chatJID, "Session compacted! Kept the last 20 messages with a summary of prior history.")
}

// SendText sends a plain text message to a chat. Exported for use by alerting systems.
func (w *WhatsAppChannel) SendText(chatJID string, text string) error {
	if w.client == nil {
		return fmt.Errorf("whatsapp client not initialized")
	}
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat_jid: %w", err)
	}
	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}
	_, err = w.client.SendMessage(context.Background(), jid, msg)
	return err
}

// IsConnected returns whether the client is connected to WhatsApp.
func (w *WhatsAppChannel) IsConnected() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.connected
}
