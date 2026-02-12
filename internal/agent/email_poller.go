package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/google/uuid"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/oauth"
	"github.com/yourusername/kaggen/internal/trust"
)

// EmailPoller periodically checks IMAP folders for new emails
// and stores them in thirdparty.db for human review.
type EmailPoller struct {
	config         config.EmailPollerConfig
	interval       time.Duration
	folders        []string
	store          *trust.ThirdPartyStore
	attachStore    *trust.AttachmentStore
	tokenGetter    EmailTokenGetter
	providerGetter EmailProviderGetter
	notifier       *trust.TelegramOwnerNotifier
	logger         *slog.Logger

	// UID tracking per folder (folder -> last seen UID)
	lastUIDs   map[string]uint32
	lastUIDsMu sync.RWMutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewEmailPoller creates a new email poller.
func NewEmailPoller(
	cfg config.EmailPollerConfig,
	interval time.Duration,
	folders []string,
	store *trust.ThirdPartyStore,
	attachStore *trust.AttachmentStore,
	tokenGetter EmailTokenGetter,
	providerGetter EmailProviderGetter,
	notifier *trust.TelegramOwnerNotifier,
	logger *slog.Logger,
) *EmailPoller {
	return &EmailPoller{
		config:         cfg,
		interval:       interval,
		folders:        folders,
		store:          store,
		attachStore:    attachStore,
		tokenGetter:    tokenGetter,
		providerGetter: providerGetter,
		notifier:       notifier,
		logger:         logger,
		lastUIDs:       make(map[string]uint32),
		stopCh:         make(chan struct{}),
	}
}

// Start begins the polling loop.
func (p *EmailPoller) Start(ctx context.Context) {
	p.logger.Info("starting email poller",
		"provider", p.config.Provider,
		"email", p.config.Email,
		"interval", p.interval,
		"folders", p.folders,
	)

	p.wg.Add(1)
	go p.pollLoop(ctx)
}

// Stop stops the polling loop and waits for it to finish.
func (p *EmailPoller) Stop() {
	close(p.stopCh)
	p.wg.Wait()
	p.logger.Info("email poller stopped")
}

func (p *EmailPoller) pollLoop(ctx context.Context) {
	defer p.wg.Done()

	// Initial poll
	if err := p.poll(ctx); err != nil {
		p.logger.Error("initial email poll failed", "error", err)
	}

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			if err := p.poll(ctx); err != nil {
				p.logger.Error("email poll failed", "error", err)
			}
		}
	}
}

func (p *EmailPoller) poll(ctx context.Context) error {
	// Get OAuth token
	token, err := p.tokenGetter(p.config.Email, p.config.Provider)
	if err != nil {
		if err == oauth.ErrTokenNotFound || err == oauth.ErrTokenExpired {
			p.logger.Warn("OAuth token unavailable for email poller",
				"provider", p.config.Provider,
				"error", err,
			)
			return nil // Don't treat as fatal error
		}
		return fmt.Errorf("token retrieval failed: %w", err)
	}

	// Get provider config
	provider, ok := p.providerGetter(p.config.Provider)
	if !ok {
		return fmt.Errorf("provider %q not configured", p.config.Provider)
	}

	if provider.IMAP == nil {
		return fmt.Errorf("IMAP not configured for provider %q", p.config.Provider)
	}

	// Connect to IMAP
	client, err := ConnectEmailIMAP(p.config.Email, token.AccessToken, provider)
	if err != nil {
		return fmt.Errorf("IMAP connection failed: %w", err)
	}
	defer client.Logout()

	// Poll each folder
	for _, folder := range p.folders {
		if err := p.pollFolder(ctx, client, folder); err != nil {
			p.logger.Error("failed to poll folder", "folder", folder, "error", err)
			// Continue with other folders
		}
	}

	return nil
}

func (p *EmailPoller) pollFolder(ctx context.Context, c *client.Client, folder string) error {
	// Select folder (read-only)
	mbox, err := c.Select(folder, true)
	if err != nil {
		return fmt.Errorf("failed to select folder: %w", err)
	}

	if mbox.Messages == 0 {
		return nil
	}

	// Get last seen UID for this folder
	p.lastUIDsMu.RLock()
	lastUID := p.lastUIDs[folder]
	p.lastUIDsMu.RUnlock()

	// Search for messages newer than lastUID
	// If lastUID is 0, we start from the most recent messages
	var seqNums []uint32
	if lastUID == 0 {
		// First run: just note the current highest UID without fetching old mail
		// This prevents flooding the system with old emails on first start
		p.lastUIDsMu.Lock()
		p.lastUIDs[folder] = mbox.UidNext - 1
		p.lastUIDsMu.Unlock()
		p.logger.Info("initialized email poller UID tracking",
			"folder", folder,
			"starting_uid", mbox.UidNext-1,
		)
		return nil
	}

	// Search for UIDs greater than lastUID
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(lastUID+1, 0) // UID:* means "lastUID+1 to end"

	seqNums, err = c.UidSearch(criteria)
	if err != nil {
		return fmt.Errorf("UID search failed: %w", err)
	}

	if len(seqNums) == 0 {
		return nil
	}

	p.logger.Info("found new emails", "folder", folder, "count", len(seqNums))

	// Fetch the messages
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(seqNums...)

	// Fetch envelope and full body
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid, section.FetchItem()}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqSet, items, messages)
	}()

	var maxUID uint32
	for msg := range messages {
		if msg.Uid > maxUID {
			maxUID = msg.Uid
		}

		if err := p.processMessage(ctx, msg, folder); err != nil {
			p.logger.Error("failed to process email", "uid", msg.Uid, "error", err)
			// Continue with other messages
		}
	}

	if err := <-done; err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}

	// Update last seen UID
	if maxUID > 0 {
		p.lastUIDsMu.Lock()
		if maxUID > p.lastUIDs[folder] {
			p.lastUIDs[folder] = maxUID
		}
		p.lastUIDsMu.Unlock()
	}

	return nil
}

func (p *EmailPoller) processMessage(ctx context.Context, msg *imap.Message, folder string) error {
	if msg.Envelope == nil {
		return fmt.Errorf("message has no envelope")
	}

	// Extract Message-ID for deduplication
	messageID := msg.Envelope.MessageId
	if messageID == "" {
		messageID = fmt.Sprintf("uid-%d@%s", msg.Uid, folder)
	}

	// Check if we've already processed this email
	exists, err := p.store.EmailExists(messageID)
	if err != nil {
		p.logger.Warn("failed to check email existence", "message_id", messageID, "error", err)
	} else if exists {
		p.logger.Debug("skipping duplicate email", "message_id", messageID)
		return nil
	}

	// Extract sender email
	var senderEmail, senderName string
	if len(msg.Envelope.From) > 0 {
		from := msg.Envelope.From[0]
		senderEmail = fmt.Sprintf("%s@%s", from.MailboxName, from.HostName)
		senderName = from.PersonalName
	}

	// Generate session ID based on sender
	sessionID := fmt.Sprintf("email:%s", senderEmail)

	// Generate message ID
	id := uuid.New().String()

	// Extract body from raw message
	var body string
	var attachments []emailAttachmentData
	for _, literal := range msg.Body {
		if literal != nil {
			body, attachments = p.parseEmailBody(literal)
			break
		}
	}

	// Create third-party message
	tpMsg := &trust.ThirdPartyMessage{
		ID:             id,
		SessionID:      sessionID,
		SenderEmail:    senderEmail,
		SenderName:     senderName,
		EmailSubject:   msg.Envelope.Subject,
		EmailMessageID: messageID,
		Channel:        "email",
		UserMessage:    body,
		LLMResponse:    "", // No LLM response for ingested emails
		CreatedAt:      msg.Envelope.Date,
		Notified:       false,
	}

	// Store the message
	if err := p.store.Add(tpMsg); err != nil {
		return fmt.Errorf("failed to store email: %w", err)
	}

	// Store attachments
	for _, att := range attachments {
		if p.attachStore == nil {
			continue
		}

		relativePath, err := p.attachStore.Save(id, att.filename, att.data)
		if err != nil {
			p.logger.Error("failed to save attachment",
				"message_id", id,
				"filename", att.filename,
				"error", err,
			)
			continue
		}

		emailAtt := &trust.EmailAttachment{
			ID:          uuid.New().String(),
			MessageID:   id,
			Filename:    att.filename,
			ContentType: att.contentType,
			Size:        int64(len(att.data)),
			FilePath:    relativePath,
			CreatedAt:   time.Now(),
		}

		if err := p.store.AddAttachment(emailAtt); err != nil {
			p.logger.Error("failed to store attachment metadata",
				"message_id", id,
				"filename", att.filename,
				"error", err,
			)
		}
	}

	p.logger.Info("ingested email",
		"id", id,
		"from", senderEmail,
		"subject", msg.Envelope.Subject,
		"attachments", len(attachments),
	)

	// Queue notification if notifier is configured
	if p.notifier != nil {
		p.notifier.QueueNotification(tpMsg)
	}

	return nil
}

type emailAttachmentData struct {
	filename    string
	contentType string
	data        []byte
}

// parseEmailBody extracts the text body and attachments from an email.
func (p *EmailPoller) parseEmailBody(r io.Reader) (string, []emailAttachmentData) {
	// Read the entire message
	data, err := io.ReadAll(io.LimitReader(r, 10*1024*1024)) // 10MB limit
	if err != nil {
		p.logger.Warn("failed to read email body", "error", err)
		return "", nil
	}

	// Parse as mail message
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		// If parsing fails, return raw content as text
		return string(data), nil
	}

	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Return body as-is
		body, _ := io.ReadAll(msg.Body)
		return string(body), nil
	}

	var textBody string
	var attachments []emailAttachmentData

	if strings.HasPrefix(mediaType, "multipart/") {
		textBody, attachments = p.parseMultipart(msg.Body, params["boundary"])
	} else if strings.HasPrefix(mediaType, "text/") {
		body, _ := io.ReadAll(msg.Body)
		textBody = string(body)
	} else {
		// Non-text single-part message (rare)
		body, _ := io.ReadAll(msg.Body)
		textBody = string(body)
	}

	return textBody, attachments
}

func (p *EmailPoller) parseMultipart(r io.Reader, boundary string) (string, []emailAttachmentData) {
	if boundary == "" {
		return "", nil
	}

	mr := multipart.NewReader(r, boundary)
	var textParts []string
	var attachments []emailAttachmentData

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		contentType := part.Header.Get("Content-Type")
		contentDisposition := part.Header.Get("Content-Disposition")
		mediaType, params, _ := mime.ParseMediaType(contentType)

		// Check if this is an attachment
		if strings.HasPrefix(contentDisposition, "attachment") {
			filename := part.FileName()
			if filename == "" {
				filename = params["name"]
			}
			if filename == "" {
				filename = fmt.Sprintf("attachment_%d", len(attachments)+1)
			}

			data, err := io.ReadAll(io.LimitReader(part, 10*1024*1024)) // 10MB per attachment
			if err == nil {
				attachments = append(attachments, emailAttachmentData{
					filename:    filename,
					contentType: contentType,
					data:        data,
				})
			}
			continue
		}

		// Handle nested multipart
		if strings.HasPrefix(mediaType, "multipart/") {
			nestedText, nestedAttach := p.parseMultipart(part, params["boundary"])
			if nestedText != "" {
				textParts = append(textParts, nestedText)
			}
			attachments = append(attachments, nestedAttach...)
			continue
		}

		// Handle text parts
		if strings.HasPrefix(mediaType, "text/") {
			data, err := io.ReadAll(io.LimitReader(part, 1024*1024)) // 1MB for text
			if err == nil {
				// Prefer text/plain over text/html
				if mediaType == "text/plain" || len(textParts) == 0 {
					if mediaType == "text/plain" && len(textParts) > 0 {
						// Replace HTML with plain text
						textParts = []string{string(data)}
					} else {
						textParts = append(textParts, string(data))
					}
				}
			}
		}
	}

	return strings.Join(textParts, "\n\n"), attachments
}
