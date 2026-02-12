package trust

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Summarizer provides text summarization capability.
// Implemented by agent.LocalAgent to avoid import cycle.
type Summarizer interface {
	Summarize(ctx context.Context, prompt string) string
}

// TelegramSendFunc is the function signature for sending Telegram messages.
type TelegramSendFunc func(chatID int64, text string)

// TelegramOwnerNotifier sends batched digest notifications to the owner via Telegram.
type TelegramOwnerNotifier struct {
	sendFunc       TelegramSendFunc
	ownerIDs       []int64
	store          *ThirdPartyStore
	summarizer     Summarizer
	logger         *slog.Logger

	// Batching
	digestInterval time.Duration
	pending        []*ThirdPartyMessage
	pendingMu      sync.Mutex
	ticker         *time.Ticker
	stopCh         chan struct{}
	stopped        bool
	stoppedMu      sync.Mutex
}

// NewTelegramOwnerNotifier creates a new notifier with batched digest.
func NewTelegramOwnerNotifier(
	sendFunc TelegramSendFunc,
	ownerIDs []int64,
	store *ThirdPartyStore,
	summarizer Summarizer,
	logger *slog.Logger,
) *TelegramOwnerNotifier {
	if logger == nil {
		logger = slog.Default()
	}

	n := &TelegramOwnerNotifier{
		sendFunc:       sendFunc,
		ownerIDs:       ownerIDs,
		store:          store,
		summarizer:     summarizer,
		logger:         logger,
		digestInterval: 5 * time.Minute,
		pending:        make([]*ThirdPartyMessage, 0),
		stopCh:         make(chan struct{}),
	}

	n.startDigestLoop()
	return n
}

// SetDigestInterval changes the digest interval (for testing).
func (n *TelegramOwnerNotifier) SetDigestInterval(d time.Duration) {
	n.digestInterval = d
	// Restart ticker if already running
	if n.ticker != nil {
		n.ticker.Reset(d)
	}
}

// QueueNotification adds a message to the pending batch.
func (n *TelegramOwnerNotifier) QueueNotification(msg *ThirdPartyMessage) {
	n.pendingMu.Lock()
	n.pending = append(n.pending, msg)
	n.pendingMu.Unlock()

	n.logger.Debug("third-party message queued for digest",
		"session_id", msg.SessionID,
		"pending_count", len(n.pending))
}

// NotifyOwner sends a message to all owner Telegram IDs.
// Implements the OwnerNotifier interface.
func (n *TelegramOwnerNotifier) NotifyOwner(ctx context.Context, message string) error {
	if n.sendFunc == nil {
		return fmt.Errorf("telegram send function not configured")
	}
	if len(n.ownerIDs) == 0 {
		return fmt.Errorf("no owner telegram IDs configured")
	}

	for _, ownerID := range n.ownerIDs {
		n.sendFunc(ownerID, message)
	}
	return nil
}

// startDigestLoop starts the background ticker for sending batched digests.
func (n *TelegramOwnerNotifier) startDigestLoop() {
	n.ticker = time.NewTicker(n.digestInterval)
	go func() {
		for {
			select {
			case <-n.ticker.C:
				n.sendDigest()
			case <-n.stopCh:
				n.ticker.Stop()
				return
			}
		}
	}()
}

// sendDigest collects pending messages, summarizes, and sends to owner.
func (n *TelegramOwnerNotifier) sendDigest() {
	n.pendingMu.Lock()
	if len(n.pending) == 0 {
		n.pendingMu.Unlock()
		return
	}
	messages := n.pending
	n.pending = make([]*ThirdPartyMessage, 0)
	n.pendingMu.Unlock()

	n.logger.Info("sending third-party digest",
		"message_count", len(messages))

	// Group by sender
	bySender := n.groupBySender(messages)

	// Summarize using local LLM
	summary := n.summarizeDigest(context.Background(), bySender)

	// Format notification
	text := fmt.Sprintf("📨 *Third-party activity digest*\n\n"+
		"*%d new messages* from %d senders\n\n"+
		"%s\n\n"+
		"_View full conversations in mobile app_",
		len(messages), len(bySender), summary)

	// Send to all owners
	if err := n.NotifyOwner(context.Background(), text); err != nil {
		n.logger.Error("failed to send digest notification", "error", err)
		return
	}

	// Mark messages as notified in store
	if n.store != nil {
		var ids []string
		for _, msg := range messages {
			ids = append(ids, msg.ID)
		}
		if err := n.store.MarkNotified(ids); err != nil {
			n.logger.Warn("failed to mark messages as notified", "error", err)
		}
	}
}

// groupBySender groups messages by sender identifier.
func (n *TelegramOwnerNotifier) groupBySender(messages []*ThirdPartyMessage) map[string][]*ThirdPartyMessage {
	bySender := make(map[string][]*ThirdPartyMessage)
	for _, msg := range messages {
		// Use sender name, phone, or telegram ID as key
		key := msg.SenderName
		if key == "" && msg.SenderPhone != "" {
			key = msg.SenderPhone
		}
		if key == "" && msg.SenderTelegramID != 0 {
			key = fmt.Sprintf("Telegram:%d", msg.SenderTelegramID)
		}
		if key == "" {
			key = "Unknown"
		}
		bySender[key] = append(bySender[key], msg)
	}
	return bySender
}

// summarizeDigest uses the local LLM to create a concise summary.
func (n *TelegramOwnerNotifier) summarizeDigest(ctx context.Context, bySender map[string][]*ThirdPartyMessage) string {
	if n.summarizer == nil {
		// Fallback: just list senders and message counts
		var summary string
		for sender, msgs := range bySender {
			summary += fmt.Sprintf("• *%s*: %d messages\n", sender, len(msgs))
		}
		return summary
	}

	// Build prompt for summarization
	prompt := "Summarize these third-party conversations in 1-2 sentences each. Be concise:\n\n"
	for sender, msgs := range bySender {
		prompt += fmt.Sprintf("From %s:\n", sender)
		for _, m := range msgs {
			// Truncate long messages
			userMsg := m.UserMessage
			if len(userMsg) > 200 {
				userMsg = userMsg[:200] + "..."
			}
			botResp := m.LLMResponse
			if len(botResp) > 200 {
				botResp = botResp[:200] + "..."
			}
			prompt += fmt.Sprintf("- User: %s\n- Bot: %s\n", userMsg, botResp)
		}
		prompt += "\n"
	}

	summary := n.summarizer.Summarize(ctx, prompt)
	if summary == "" || summary == "(summarization failed)" {
		// Fallback to simple list
		var fallback string
		for sender, msgs := range bySender {
			fallback += fmt.Sprintf("• *%s*: %d messages\n", sender, len(msgs))
		}
		return fallback
	}

	return summary
}

// Stop stops the digest loop and sends any remaining pending messages.
func (n *TelegramOwnerNotifier) Stop() {
	n.stoppedMu.Lock()
	if n.stopped {
		n.stoppedMu.Unlock()
		return
	}
	n.stopped = true
	n.stoppedMu.Unlock()

	close(n.stopCh)

	// Send any remaining pending messages
	n.sendDigest()
}

// FlushNow immediately sends any pending messages (for testing).
func (n *TelegramOwnerNotifier) FlushNow() {
	n.sendDigest()
}

// PendingCount returns the number of pending messages (for testing).
func (n *TelegramOwnerNotifier) PendingCount() int {
	n.pendingMu.Lock()
	defer n.pendingMu.Unlock()
	return len(n.pending)
}
