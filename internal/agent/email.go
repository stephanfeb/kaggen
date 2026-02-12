package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/oauth"
)

const (
	emailTimeout = 30 * time.Second
	maxMessages  = 50
)

// EmailTokenGetter retrieves an OAuth token for email authentication.
type EmailTokenGetter func(userID, provider string) (*oauth.Token, error)

// EmailProviderGetter retrieves OAuth provider configuration.
type EmailProviderGetter func(provider string) (config.OAuthProvider, bool)

// EmailToolArgs defines the input arguments for the email tool.
type EmailToolArgs struct {
	Action   string   `json:"action" jsonschema:"required,description=Action to perform: send list read or search,enum=send,enum=list,enum=read,enum=search"`
	Provider string   `json:"provider" jsonschema:"required,description=OAuth provider name (e.g. google) for authentication"`
	Email    string   `json:"email" jsonschema:"required,description=Your email address for authentication (e.g. user@gmail.com)"`
	Folder   string   `json:"folder,omitempty" jsonschema:"description=IMAP folder name (default: INBOX). Used for list/read/search actions"`
	To       []string `json:"to,omitempty" jsonschema:"description=Recipient email addresses. Required for send action"`
	CC       []string `json:"cc,omitempty" jsonschema:"description=CC recipients. Used for send action"`
	BCC      []string `json:"bcc,omitempty" jsonschema:"description=BCC recipients. Used for send action"`
	Subject  string   `json:"subject,omitempty" jsonschema:"description=Email subject. Required for send action"`
	Body     string   `json:"body,omitempty" jsonschema:"description=Email body (plain text). Required for send action"`
	MsgID    uint32   `json:"message_id,omitempty" jsonschema:"description=Message sequence number to read. Required for read action"`
	Query    string   `json:"query,omitempty" jsonschema:"description=Search query (IMAP search format). Used for search action"`
	Limit    int      `json:"limit,omitempty" jsonschema:"description=Maximum messages to return (default: 10 max: 50). Used for list/search actions"`
}

// EmailToolResult is the result of an email operation.
type EmailToolResult struct {
	Success  bool                `json:"success"`
	Message  string              `json:"message"`
	Messages []EmailToolMessage  `json:"messages,omitempty"` // For list/search
	Email    *EmailToolMessage   `json:"email,omitempty"`    // For read
}

// EmailToolMessage represents an email message summary.
type EmailToolMessage struct {
	SeqNum  uint32   `json:"seq_num"`
	Subject string   `json:"subject"`
	From    string   `json:"from"`
	To      []string `json:"to,omitempty"`
	Date    string   `json:"date"`
	Body    string   `json:"body,omitempty"` // Only populated for read action
}

// NewEmailTool creates a new email tool with OAuth support.
func NewEmailTool(userID string, allowedProviders []string, tokenGetter EmailTokenGetter, providerGetter EmailProviderGetter) tool.CallableTool {
	// Build allowed providers set
	allowed := make(map[string]bool)
	for _, p := range allowedProviders {
		allowed[p] = true
	}

	return function.NewFunctionTool(
		func(ctx context.Context, args EmailToolArgs) (*EmailToolResult, error) {
			return executeEmailTool(ctx, args, userID, allowed, tokenGetter, providerGetter)
		},
		function.WithName("email"),
		function.WithDescription("Send, list, read, and search emails via SMTP/IMAP with XOAUTH2 authentication. Requires OAuth authorization for the provider."),
	)
}

func executeEmailTool(ctx context.Context, args EmailToolArgs, userID string, allowedProviders map[string]bool, tokenGetter EmailTokenGetter, providerGetter EmailProviderGetter) (*EmailToolResult, error) {
	result := &EmailToolResult{}

	// Validate provider is allowed
	if len(allowedProviders) > 0 && !allowedProviders[args.Provider] {
		result.Message = fmt.Sprintf("OAuth provider %q not available to this skill", args.Provider)
		return result, nil
	}

	// Get OAuth token
	if tokenGetter == nil {
		result.Message = "OAuth not configured"
		return result, nil
	}
	token, err := tokenGetter(userID, args.Provider)
	if err != nil {
		if err == oauth.ErrTokenNotFound {
			result.Message = fmt.Sprintf("OAuth authorization required for %s. Please authorize via dashboard.", args.Provider)
			return result, nil
		}
		if err == oauth.ErrTokenExpired {
			result.Message = fmt.Sprintf("OAuth token for %s has expired. Please re-authorize via dashboard.", args.Provider)
			return result, nil
		}
		result.Message = fmt.Sprintf("OAuth token retrieval failed: %v", err)
		return result, nil
	}

	// Get provider configuration (for SMTP/IMAP servers)
	if providerGetter == nil {
		result.Message = "Provider configuration not available"
		return result, nil
	}
	provider, ok := providerGetter(args.Provider)
	if !ok {
		result.Message = fmt.Sprintf("Provider %q not configured", args.Provider)
		return result, nil
	}

	// Execute action
	switch args.Action {
	case "send":
		return sendEmailAction(ctx, args, token, provider)
	case "list":
		return listEmailsAction(ctx, args, token, provider)
	case "read":
		return readEmailAction(ctx, args, token, provider)
	case "search":
		return searchEmailsAction(ctx, args, token, provider)
	default:
		result.Message = fmt.Sprintf("Unknown action %q. Use: send, list, read, or search", args.Action)
		return result, nil
	}
}

// sendEmailAction sends an email via SMTP with XOAUTH2.
func sendEmailAction(ctx context.Context, args EmailToolArgs, token *oauth.Token, provider config.OAuthProvider) (*EmailToolResult, error) {
	result := &EmailToolResult{}

	// Validate required fields
	if len(args.To) == 0 {
		result.Message = "Error: 'to' is required for send action"
		return result, nil
	}
	if args.Subject == "" {
		result.Message = "Error: 'subject' is required for send action"
		return result, nil
	}

	// Check SMTP configuration
	if provider.SMTP == nil {
		result.Message = fmt.Sprintf("SMTP server not configured for provider %q", args.Provider)
		return result, nil
	}

	// Build recipients list
	recipients := append([]string{}, args.To...)
	recipients = append(recipients, args.CC...)
	recipients = append(recipients, args.BCC...)

	// Build email message with required headers
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("From: %s\r\n", args.Email))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(args.To, ", ")))
	if len(args.CC) > 0 {
		msg.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(args.CC, ", ")))
	}
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", args.Subject))
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	msg.WriteString(fmt.Sprintf("Message-ID: <%d.%s@kaggen>\r\n", time.Now().UnixNano(), args.Email))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(args.Body)
	msg.WriteString("\r\n")

	// Connect and send
	addr := fmt.Sprintf("%s:%d", provider.SMTP.Host, provider.SMTP.Port)

	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: emailTimeout}

	if provider.SMTP.TLS {
		// Implicit TLS (port 465)
		tlsConfig := &tls.Config{ServerName: provider.SMTP.Host}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		result.Message = fmt.Sprintf("Failed to connect to SMTP server: %v", err)
		return result, nil
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, provider.SMTP.Host)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create SMTP client: %v", err)
		return result, nil
	}
	defer c.Close()

	// STARTTLS if needed
	if provider.SMTP.StartTLS {
		tlsConfig := &tls.Config{ServerName: provider.SMTP.Host}
		if err := c.StartTLS(tlsConfig); err != nil {
			result.Message = fmt.Sprintf("STARTTLS failed: %v", err)
			return result, nil
		}
	}

	// Authenticate with XOAUTH2
	auth := &emailXoauth2Client{username: args.Email, token: token.AccessToken}
	if err := c.Auth(auth); err != nil {
		result.Message = fmt.Sprintf("XOAUTH2 authentication failed: %v", err)
		return result, nil
	}

	// Send email
	if err := c.Mail(args.Email); err != nil {
		result.Message = fmt.Sprintf("MAIL FROM failed: %v", err)
		return result, nil
	}
	for _, rcpt := range recipients {
		if err := c.Rcpt(rcpt); err != nil {
			result.Message = fmt.Sprintf("RCPT TO failed for %s: %v", rcpt, err)
			return result, nil
		}
	}

	w, err := c.Data()
	if err != nil {
		result.Message = fmt.Sprintf("DATA command failed: %v", err)
		return result, nil
	}
	_, err = w.Write([]byte(msg.String()))
	if err != nil {
		result.Message = fmt.Sprintf("Failed to write email body: %v", err)
		return result, nil
	}
	if err := w.Close(); err != nil {
		result.Message = fmt.Sprintf("Failed to close data writer: %v", err)
		return result, nil
	}

	if err := c.Quit(); err != nil {
		// Quit error is non-fatal
	}

	result.Success = true
	result.Message = fmt.Sprintf("Email sent successfully to %s", strings.Join(args.To, ", "))
	return result, nil
}

// listEmailsAction lists emails in a folder via IMAP.
func listEmailsAction(ctx context.Context, args EmailToolArgs, token *oauth.Token, provider config.OAuthProvider) (*EmailToolResult, error) {
	result := &EmailToolResult{}

	if provider.IMAP == nil {
		result.Message = fmt.Sprintf("IMAP server not configured for provider %q", args.Provider)
		return result, nil
	}

	folder := args.Folder
	if folder == "" {
		folder = "INBOX"
	}
	limit := args.Limit
	if limit <= 0 || limit > maxMessages {
		limit = 10
	}

	c, err := ConnectEmailIMAP(args.Email, token.AccessToken, provider)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}
	defer c.Logout()

	// Select mailbox
	mbox, err := c.Select(folder, true) // read-only
	if err != nil {
		result.Message = fmt.Sprintf("Failed to select folder %q: %v", folder, err)
		return result, nil
	}

	if mbox.Messages == 0 {
		result.Success = true
		result.Message = fmt.Sprintf("No messages in %s", folder)
		result.Messages = []EmailToolMessage{}
		return result, nil
	}

	// Fetch most recent messages
	from := uint32(1)
	if mbox.Messages > uint32(limit) {
		from = mbox.Messages - uint32(limit) + 1
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(from, mbox.Messages)

	messages := make(chan *imap.Message, limit)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope}, messages)
	}()

	result.Messages = []EmailToolMessage{}
	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}
		em := EmailToolMessage{
			SeqNum:  msg.SeqNum,
			Subject: msg.Envelope.Subject,
			Date:    msg.Envelope.Date.Format(time.RFC3339),
		}
		if len(msg.Envelope.From) > 0 {
			em.From = FormatEmailAddress(msg.Envelope.From[0])
		}
		for _, addr := range msg.Envelope.To {
			em.To = append(em.To, FormatEmailAddress(addr))
		}
		result.Messages = append(result.Messages, em)
	}

	if err := <-done; err != nil {
		result.Message = fmt.Sprintf("Fetch failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Listed %d messages from %s", len(result.Messages), folder)
	return result, nil
}

// readEmailAction reads a specific email by sequence number.
func readEmailAction(ctx context.Context, args EmailToolArgs, token *oauth.Token, provider config.OAuthProvider) (*EmailToolResult, error) {
	result := &EmailToolResult{}

	if provider.IMAP == nil {
		result.Message = fmt.Sprintf("IMAP server not configured for provider %q", args.Provider)
		return result, nil
	}

	if args.MsgID == 0 {
		result.Message = "Error: 'message_id' is required for read action"
		return result, nil
	}

	folder := args.Folder
	if folder == "" {
		folder = "INBOX"
	}

	c, err := ConnectEmailIMAP(args.Email, token.AccessToken, provider)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}
	defer c.Logout()

	// Select mailbox
	_, err = c.Select(folder, true)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to select folder %q: %v", folder, err)
		return result, nil
	}

	// Fetch the message
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(args.MsgID)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqSet, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		result.Message = fmt.Sprintf("Fetch failed: %v", err)
		return result, nil
	}

	if msg == nil {
		result.Message = fmt.Sprintf("Message %d not found", args.MsgID)
		return result, nil
	}

	em := EmailToolMessage{
		SeqNum:  msg.SeqNum,
		Subject: msg.Envelope.Subject,
		Date:    msg.Envelope.Date.Format(time.RFC3339),
	}
	if len(msg.Envelope.From) > 0 {
		em.From = FormatEmailAddress(msg.Envelope.From[0])
	}
	for _, addr := range msg.Envelope.To {
		em.To = append(em.To, FormatEmailAddress(addr))
	}

	// Extract body
	for _, v := range msg.Body {
		if v != nil {
			body, _ := io.ReadAll(io.LimitReader(v, 100*1024)) // 100KB limit
			em.Body = string(body)
			break
		}
	}

	result.Success = true
	result.Message = fmt.Sprintf("Read message %d", args.MsgID)
	result.Email = &em
	return result, nil
}

// searchEmailsAction searches for emails matching a query.
func searchEmailsAction(ctx context.Context, args EmailToolArgs, token *oauth.Token, provider config.OAuthProvider) (*EmailToolResult, error) {
	result := &EmailToolResult{}

	if provider.IMAP == nil {
		result.Message = fmt.Sprintf("IMAP server not configured for provider %q", args.Provider)
		return result, nil
	}

	if args.Query == "" {
		result.Message = "Error: 'query' is required for search action"
		return result, nil
	}

	folder := args.Folder
	if folder == "" {
		folder = "INBOX"
	}
	limit := args.Limit
	if limit <= 0 || limit > maxMessages {
		limit = 10
	}

	c, err := ConnectEmailIMAP(args.Email, token.AccessToken, provider)
	if err != nil {
		result.Message = err.Error()
		return result, nil
	}
	defer c.Logout()

	// Select mailbox
	_, err = c.Select(folder, true)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to select folder %q: %v", folder, err)
		return result, nil
	}

	// Parse search criteria
	criteria := parseEmailSearchQuery(args.Query)

	// Search
	seqNums, err := c.Search(criteria)
	if err != nil {
		result.Message = fmt.Sprintf("Search failed: %v", err)
		return result, nil
	}

	if len(seqNums) == 0 {
		result.Success = true
		result.Message = "No messages found matching query"
		result.Messages = []EmailToolMessage{}
		return result, nil
	}

	// Limit results
	if len(seqNums) > limit {
		seqNums = seqNums[len(seqNums)-limit:]
	}

	// Fetch matching messages
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(seqNums...)

	messages := make(chan *imap.Message, limit)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope}, messages)
	}()

	result.Messages = []EmailToolMessage{}
	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}
		em := EmailToolMessage{
			SeqNum:  msg.SeqNum,
			Subject: msg.Envelope.Subject,
			Date:    msg.Envelope.Date.Format(time.RFC3339),
		}
		if len(msg.Envelope.From) > 0 {
			em.From = FormatEmailAddress(msg.Envelope.From[0])
		}
		result.Messages = append(result.Messages, em)
	}

	if err := <-done; err != nil {
		result.Message = fmt.Sprintf("Fetch failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Found %d messages matching query", len(result.Messages))
	return result, nil
}

// ConnectEmailIMAP establishes an IMAP connection with XOAUTH2.
func ConnectEmailIMAP(email, accessToken string, provider config.OAuthProvider) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", provider.IMAP.Host, provider.IMAP.Port)

	var c *client.Client
	var err error

	if provider.IMAP.TLS {
		c, err = client.DialTLS(addr, nil)
	} else {
		c, err = client.Dial(addr)
		if err == nil && provider.IMAP.StartTLS {
			if err = c.StartTLS(nil); err != nil {
				c.Logout()
				return nil, fmt.Errorf("IMAP STARTTLS failed: %w", err)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to IMAP server: %w", err)
	}

	// Authenticate with XOAUTH2
	saslClient := &emailXoauth2SASLClient{username: email, token: accessToken}
	if err := c.Authenticate(saslClient); err != nil {
		c.Logout()
		return nil, fmt.Errorf("XOAUTH2 authentication failed: %w", err)
	}

	return c, nil
}

// emailXoauth2SASLClient implements sasl.Client for XOAUTH2.
type emailXoauth2SASLClient struct {
	username string
	token    string
}

func (c *emailXoauth2SASLClient) Start() (mech string, ir []byte, err error) {
	resp := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.username, c.token)
	return "XOAUTH2", []byte(resp), nil
}

func (c *emailXoauth2SASLClient) Next(challenge []byte) ([]byte, error) {
	// XOAUTH2 doesn't expect challenges; if we get one, it's an error response
	return nil, fmt.Errorf("server challenge (likely auth error): %s", string(challenge))
}

// emailXoauth2Client implements smtp.Auth for XOAUTH2.
type emailXoauth2Client struct {
	username string
	token    string
}

func (c *emailXoauth2Client) Start(server *smtp.ServerInfo) (string, []byte, error) {
	resp := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.username, c.token)
	return "XOAUTH2", []byte(resp), nil
}

func (c *emailXoauth2Client) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("unexpected server challenge: %s", string(fromServer))
	}
	return nil, nil
}

// FormatEmailAddress formats an IMAP address.
func FormatEmailAddress(addr *imap.Address) string {
	if addr == nil {
		return ""
	}
	if addr.PersonalName != "" {
		return fmt.Sprintf("%s <%s@%s>", addr.PersonalName, addr.MailboxName, addr.HostName)
	}
	return fmt.Sprintf("%s@%s", addr.MailboxName, addr.HostName)
}

// parseEmailSearchQuery converts a simple search query to IMAP search criteria.
// Supports: from:email, to:email, subject:text, or plain text for body search.
func parseEmailSearchQuery(query string) *imap.SearchCriteria {
	criteria := imap.NewSearchCriteria()

	parts := strings.Fields(query)
	for _, part := range parts {
		if strings.HasPrefix(part, "from:") {
			criteria.Header.Add("From", strings.TrimPrefix(part, "from:"))
		} else if strings.HasPrefix(part, "to:") {
			criteria.Header.Add("To", strings.TrimPrefix(part, "to:"))
		} else if strings.HasPrefix(part, "subject:") {
			criteria.Header.Add("Subject", strings.TrimPrefix(part, "subject:"))
		} else {
			// Text search in body
			criteria.Text = append(criteria.Text, part)
		}
	}

	return criteria
}
