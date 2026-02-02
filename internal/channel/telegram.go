package channel

import (
	"context"
	"fmt"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	sttBaseURL       string
	// msgEventMap maps Telegram message_id → event ID for thread reply detection.
	msgEventMap sync.Map // int (tg msg id) -> string (event id)
}

// NewTelegramChannel creates a new Telegram channel.
// sessionService is optional; when provided, the /clear command can reset sessions.
func NewTelegramChannel(token string, cfg *config.TelegramConfig, sessionService trpcsession.Service, logger *slog.Logger, sttBaseURL ...string) *TelegramChannel {
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

	var sttURL string
	if len(sttBaseURL) > 0 && sttBaseURL[0] != "" {
		sttURL = sttBaseURL[0]
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
		sttBaseURL:       sttURL,
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
				// Handle inline keyboard callback queries (approval buttons).
				if update.CallbackQuery != nil {
					t.handleCallbackQuery(update.CallbackQuery)
					continue
				}

				if update.Message == nil {
					continue
				}

				userID := update.Message.From.ID
				chatID := update.Message.Chat.ID

				if !t.isAuthorized(userID, chatID) {
					t.logger.Warn("rejected unauthorized message",
						"user_id", userID, "chat_id", chatID)
					t.SendText(chatID, t.rejectMessage)
					continue
				}

				if !t.userLimiter.allow(userID) {
					t.logger.Warn("user rate limited",
						"user_id", userID, "chat_id", chatID)
					t.SendText(chatID, t.rateLimitMessage)
					continue
				}

				// Handle /clear command
			if update.Message.IsCommand() && update.Message.Command() == "clear" {
				t.handleClear(ctx, update.Message)
				continue
			}

			// Handle /compact command
			if update.Message.IsCommand() && update.Message.Command() == "compact" {
				t.handleCompact(ctx, update.Message)
				continue
			}

			msg := t.telegramUpdateToMessage(update)
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

	// Handle approval requests with inline keyboard buttons.
	if resp.Type == "approval_required" {
		return t.sendApprovalKeyboard(chatID, resp)
	}

	// Skip tool calls, tool results, and empty responses — only send
	// user-facing text and error messages to Telegram.
	switch resp.Type {
	case "tool_call", "tool_result":
		return nil
	}
	if resp.Content == "" && resp.Metadata["send_file"] == nil && resp.Metadata["send_file_local"] == nil {
		return nil
	}

	// Send file if requested. Prefer local path for direct file access.
	if filePath, ok := resp.Metadata["send_file_local"].(string); ok && filePath != "" {
		return t.sendFile(chatID, filePath, resp.Content)
	}
	if filePath, ok := resp.Metadata["send_file"].(string); ok && filePath != "" {
		return t.sendFile(chatID, filePath, resp.Content)
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
		sentMsg, err := t.bot.Send(msg)
		if err != nil {
			return fmt.Errorf("send telegram message: %w", err)
		}

		// Store Telegram message_id → event_id mapping for thread detection.
		if eventID, ok := resp.Metadata["event_id"].(string); ok && eventID != "" {
			t.msgEventMap.Store(sentMsg.MessageID, eventID)
		}
	}

	return nil
}

// handleCallbackQuery processes inline keyboard button presses (e.g. approval actions).
func (t *TelegramChannel) handleCallbackQuery(cq *tgbotapi.CallbackQuery) {
	// Acknowledge the callback to dismiss the loading indicator.
	callback := tgbotapi.NewCallback(cq.ID, "")
	t.bot.Request(callback)

	// Parse "approve:<id>" or "reject:<id>".
	parts := strings.SplitN(cq.Data, ":", 2)
	if len(parts) != 2 {
		return
	}
	action := parts[0]   // "approve" or "reject"
	approvalID := parts[1]

	if action != "approve" && action != "reject" {
		return
	}

	// Edit the original message to show the result.
	var resultText string
	if action == "approve" {
		resultText = "Approved"
	} else {
		resultText = "Rejected"
	}
	if cq.Message != nil {
		edit := tgbotapi.NewEditMessageText(cq.Message.Chat.ID, cq.Message.MessageID,
			cq.Message.Text+"\n\n"+resultText)
		t.bot.Send(edit)
	}

	// Determine session/user from the callback query sender.
	chatID := int64(0)
	if cq.Message != nil {
		chatID = cq.Message.Chat.ID
	}
	userID := fmt.Sprintf("tg-%d", cq.From.ID)
	var sessionID string
	if cq.Message != nil && cq.Message.Chat.Type == "private" {
		sessionID = fmt.Sprintf("tg-dm-%d", cq.From.ID)
	} else if chatID != 0 {
		sessionID = fmt.Sprintf("tg-group-%d", chatID)
	}

	// Emit a synthetic message with approval metadata.
	msg := &Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    userID,
		Content:   "", // no text content
		Channel:   "telegram",
		Metadata: map[string]any{
			"chat_id":         fmt.Sprintf("%d", chatID),
			"approval_action": action,
			"approval_id":     approvalID,
		},
	}

	select {
	case t.messages <- msg:
	default:
		t.logger.Warn("message queue full, dropping approval callback")
	}
}

// sendApprovalKeyboard sends an inline keyboard with Approve/Reject buttons.
func (t *TelegramChannel) sendApprovalKeyboard(chatID int64, resp *Response) error {
	approvalID, _ := resp.Metadata["approval_id"].(string)
	toolName, _ := resp.Metadata["tool_name"].(string)
	description, _ := resp.Metadata["description"].(string)

	text := fmt.Sprintf("Approval required: %s\n> %s", toolName, description)
	if len(text) > telegramMaxMessageLen {
		text = text[:telegramMaxMessageLen-3] + "..."
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Approve", "approve:"+approvalID),
			tgbotapi.NewInlineKeyboardButtonData("Reject", "reject:"+approvalID),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	_, err := t.bot.Send(msg)
	return err
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

// SendText sends a plain text message to a chat. Exported for use by alerting systems.
func (t *TelegramChannel) SendText(chatID int64, text string) {
	if t.bot == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := t.bot.Send(msg); err != nil {
		t.logger.Warn("failed to send text message", "error", err)
	}
}

// sendFile sends a file to a Telegram chat. Images are sent as photos; everything else as documents.
const telegramMaxCaptionLen = 1024

func (t *TelegramChannel) sendFile(chatID int64, path, caption string) error {
	t.chatLimiter.wait(chatID)

	path = config.ExpandPath(path)

	// Telegram captions are limited to 1024 characters. If the caption is
	// too long, send it as a separate text message first, then send the
	// file without a caption.
	if len(caption) > telegramMaxCaptionLen {
		textMsg := tgbotapi.NewMessage(chatID, caption)
		if _, err := t.bot.Send(textMsg); err != nil {
			t.logger.Warn("failed to send caption as message", "error", err)
		}
		t.chatLimiter.wait(chatID)
		caption = ""
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		msg := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(path))
		if caption != "" {
			msg.Caption = caption
		}
		_, err := t.bot.Send(msg)
		return err
	default:
		msg := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(path))
		if caption != "" {
			msg.Caption = caption
		}
		_, err := t.bot.Send(msg)
		return err
	}
}

// handleClear processes the /clear command by deleting the current session.
func (t *TelegramChannel) handleClear(ctx context.Context, m *tgbotapi.Message) {
	chatID := m.Chat.ID

	if t.sessionService == nil {
		t.SendText(chatID, "Session clearing is not available.")
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
		t.SendText(chatID, "Failed to clear session. Please try again.")
		return
	}

	t.logger.Info("session cleared via /clear", "session_id", sessionID, "user_id", userID)
	t.SendText(chatID, "Session cleared. Starting fresh!")
}

// handleCompact processes the /compact command by summarizing and truncating the session.
func (t *TelegramChannel) handleCompact(ctx context.Context, m *tgbotapi.Message) {
	chatID := m.Chat.ID

	if t.sessionService == nil {
		t.SendText(chatID, "Session compaction is not available.")
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

	t.SendText(chatID, "Compacting session... this may take a moment.")

	sess, err := t.sessionService.GetSession(ctx, key)
	if err != nil || sess == nil {
		t.SendText(chatID, "No session to compact.")
		return
	}

	if err := t.sessionService.CreateSessionSummary(ctx, sess, "", true); err != nil {
		t.logger.Warn("failed to compact session", "session_id", sessionID, "error", err)
		t.SendText(chatID, fmt.Sprintf("Failed to compact session: %v", err))
		return
	}

	t.logger.Info("session compacted via /compact", "session_id", sessionID, "user_id", userID)
	t.SendText(chatID, "Session compacted! Kept the last 20 messages with a summary of prior history.")
}

// telegramUpdateToMessage converts a Telegram update to a channel Message,
// downloading any attached photos or documents to ~/.kaggen/downloads/.
func (t *TelegramChannel) telegramUpdateToMessage(update tgbotapi.Update) *Message {
	m := update.Message
	userID := fmt.Sprintf("tg-%d", m.From.ID)
	chatID := fmt.Sprintf("%d", m.Chat.ID)

	var sessionID string
	if m.Chat.Type == "private" {
		sessionID = fmt.Sprintf("tg-dm-%d", m.From.ID)
	} else {
		sessionID = fmt.Sprintf("tg-group-%d", m.Chat.ID)
	}

	// Use Caption for media messages, Text for plain messages.
	content := m.Text
	if content == "" {
		content = m.Caption
	}

	// Transcribe voice messages (requires STT service).
	if m.Voice != nil && t.sttBaseURL != "" {
		if text, err := t.transcribeAudio(m.Voice.FileID, "voice.ogg"); err != nil {
			t.logger.Warn("failed to transcribe voice message", "error", err)
		} else if text != "" {
			if content != "" {
				content += "\n"
			}
			content += text
		}
	}

	// Transcribe audio files (requires STT service).
	if m.Audio != nil && t.sttBaseURL != "" {
		fileName := m.Audio.FileName
		if fileName == "" {
			fileName = "audio.ogg"
		}
		if text, err := t.transcribeAudio(m.Audio.FileID, fileName); err != nil {
			t.logger.Warn("failed to transcribe audio", "error", err)
		} else if text != "" {
			if content != "" {
				content += "\n"
			}
			content += text
		}
	}

	msg := &Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		UserID:    userID,
		Content:   content,
		Channel:   "telegram",
		Metadata: map[string]any{
			"chat_id":    chatID,
			"message_id": fmt.Sprintf("%d", m.MessageID),
			"chat_type":  m.Chat.Type,
		},
	}

	// Detect reply-to-bot-message for threading.
	if m.ReplyToMessage != nil && t.bot != nil && m.ReplyToMessage.From != nil && m.ReplyToMessage.From.ID == t.bot.Self.ID {
		tgMsgID := m.ReplyToMessage.MessageID
		if eventID, ok := t.msgEventMap.Load(tgMsgID); ok {
			msg.ReplyToEventID = eventID.(string)
			t.logger.Info("telegram thread detected",
				"reply_to_tg_msg", tgMsgID,
				"event_id", eventID)
		}
	}

	// Download attached photo (pick largest resolution).
	if len(m.Photo) > 0 {
		photo := m.Photo[len(m.Photo)-1]
		if att, err := t.downloadFile(photo.FileID, "photo.jpg"); err != nil {
			t.logger.Warn("failed to download photo", "error", err)
		} else {
			att.MimeType = "image/jpeg"
			msg.Attachments = append(msg.Attachments, *att)
		}
	}

	// Download attached document.
	if m.Document != nil {
		fileName := m.Document.FileName
		if fileName == "" {
			fileName = "document"
		}
		if att, err := t.downloadFile(m.Document.FileID, fileName); err != nil {
			t.logger.Warn("failed to download document", "error", err)
		} else {
			att.MimeType = m.Document.MimeType
			att.FileName = fileName
			msg.Attachments = append(msg.Attachments, *att)
		}
	}

	// Append attachment paths to content so the agent sees them.
	for _, att := range msg.Attachments {
		msg.Content += fmt.Sprintf("\n[Attached: %s]", att.Path)
	}

	return msg
}

// downloadsDir returns the path to ~/.kaggen/downloads/, creating it if needed.
func downloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".kaggen", "downloads")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// downloadFile downloads a Telegram file by its fileID and saves it locally.
func (t *TelegramChannel) downloadFile(fileID, fileName string) (*Attachment, error) {
	file, err := t.bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}

	url := file.Link(t.bot.Token)
	resp, err := http.Get(url) //nolint:gosec // Telegram API URL
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	dir, err := downloadsDir()
	if err != nil {
		return nil, fmt.Errorf("create downloads dir: %w", err)
	}

	// Use file unique ID + original name to avoid collisions.
	localName := fmt.Sprintf("%s_%s", file.FileUniqueID, fileName)
	localPath := filepath.Join(dir, localName)

	out, err := os.Create(localPath)
	if err != nil {
		return nil, fmt.Errorf("create local file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	t.logger.Info("downloaded telegram file", "path", localPath, "size", file.FileSize)

	return &Attachment{
		Path:     localPath,
		FileName: fileName,
	}, nil
}

// transcribeAudio downloads a Telegram audio file and sends it to the
// whisper-service for speech-to-text transcription.
func (t *TelegramChannel) transcribeAudio(fileID, fileName string) (string, error) {
	file, err := t.bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}

	fileURL := file.Link(t.bot.Token)
	resp, err := http.Get(fileURL) //nolint:gosec // Telegram API URL
	if err != nil {
		return "", fmt.Errorf("download audio: %w", err)
	}
	defer resp.Body.Close()

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read audio: %w", err)
	}

	// Build multipart request for whisper-service.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", fmt.Errorf("write form file: %w", err)
	}
	w.Close()

	transcribeURL := strings.TrimRight(t.sttBaseURL, "/") + "/transcribe"
	req, err := http.NewRequest("POST", transcribeURL, &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	sttResp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcribe request: %w", err)
	}
	defer sttResp.Body.Close()

	if sttResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sttResp.Body)
		return "", fmt.Errorf("transcribe failed (status %d): %s", sttResp.StatusCode, body)
	}

	var result struct {
		Status string `json:"status"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(sttResp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode transcription: %w", err)
	}

	t.logger.Info("transcribed voice message", "text_length", len(result.Text))
	return result.Text, nil
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
