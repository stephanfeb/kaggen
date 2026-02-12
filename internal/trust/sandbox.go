// Package trust implements trust-tier security for message routing and access control.
package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yourusername/kaggen/internal/channel"
	"github.com/yourusername/kaggen/internal/config"
)

// RelayRequest represents a message a third-party wants to relay to the owner.
type RelayRequest struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	SenderPhone  string    `json:"sender_phone,omitempty"`
	SenderName   string    `json:"sender_name,omitempty"`
	Message      string    `json:"message"`
	OriginalText string    `json:"original_text"`
	CreatedAt    time.Time `json:"created_at"`
	Channel      string    `json:"channel"` // "telegram" or "whatsapp"
	Status       string    `json:"status"`  // "pending", "delivered", "responded"
}

// RelayStore stores pending relay requests for owner notification.
type RelayStore struct {
	mu       sync.RWMutex
	requests map[string]*RelayRequest // id -> request
	dbPath   string
	logger   *slog.Logger
}

// NewRelayStore creates a new relay store.
func NewRelayStore(dbPath string, logger *slog.Logger) *RelayStore {
	if logger == nil {
		logger = slog.Default()
	}
	store := &RelayStore{
		requests: make(map[string]*RelayRequest),
		dbPath:   dbPath,
		logger:   logger,
	}
	store.load()
	return store
}

// Add adds a new relay request.
func (s *RelayStore) Add(req *RelayRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[req.ID] = req
	s.save()
}

// Get retrieves a relay request by ID.
func (s *RelayStore) Get(id string) (*RelayRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.requests[id]
	return req, ok
}

// ListPending returns all pending relay requests.
func (s *RelayStore) ListPending() []*RelayRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pending []*RelayRequest
	for _, req := range s.requests {
		if req.Status == "pending" {
			pending = append(pending, req)
		}
	}
	return pending
}

// MarkDelivered marks a relay request as delivered to owner.
func (s *RelayStore) MarkDelivered(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req, ok := s.requests[id]; ok {
		req.Status = "delivered"
		s.save()
	}
}

// load loads requests from disk.
func (s *RelayStore) load() {
	if s.dbPath == "" {
		return
	}
	data, err := os.ReadFile(s.dbPath)
	if err != nil {
		return
	}
	var requests []*RelayRequest
	if err := json.Unmarshal(data, &requests); err != nil {
		s.logger.Warn("failed to load relay requests", "error", err)
		return
	}
	for _, req := range requests {
		s.requests[req.ID] = req
	}
}

// save persists requests to disk.
func (s *RelayStore) save() {
	if s.dbPath == "" {
		return
	}
	var requests []*RelayRequest
	for _, req := range s.requests {
		requests = append(requests, req)
	}
	data, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		s.logger.Warn("failed to marshal relay requests", "error", err)
		return
	}
	dir := filepath.Dir(s.dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		s.logger.Warn("failed to create relay store directory", "error", err)
		return
	}
	if err := os.WriteFile(s.dbPath, data, 0600); err != nil {
		s.logger.Warn("failed to save relay requests", "error", err)
	}
}

// SessionTracker tracks third-party session message counts.
type SessionTracker struct {
	mu       sync.RWMutex
	counts   map[string]int // sessionID -> message count
	maxLimit int            // max messages per session (0 = unlimited)
}

// NewSessionTracker creates a new session tracker.
func NewSessionTracker(maxLimit int) *SessionTracker {
	return &SessionTracker{
		counts:   make(map[string]int),
		maxLimit: maxLimit,
	}
}

// Increment increments the message count for a session.
// Returns the new count and whether the limit has been exceeded.
func (t *SessionTracker) Increment(sessionID string) (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[sessionID]++
	count := t.counts[sessionID]
	exceeded := t.maxLimit > 0 && count > t.maxLimit
	return count, exceeded
}

// Count returns the current message count for a session.
func (t *SessionTracker) Count(sessionID string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.counts[sessionID]
}

// Reset resets the count for a session.
func (t *SessionTracker) Reset(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.counts, sessionID)
}

// Sandbox handles third-party messages with limited capabilities.
type Sandbox struct {
	cfg            *config.ThirdPartyConfig
	relayStore     *RelayStore
	sessionTracker *SessionTracker
	ownerNotifier  OwnerNotifier
	logger         *slog.Logger
}

// OwnerNotifier sends notifications to the bot owner.
type OwnerNotifier interface {
	NotifyOwner(ctx context.Context, message string) error
}

// NewSandbox creates a new sandbox for third-party messages.
func NewSandbox(cfg *config.ThirdPartyConfig, relayStorePath string, ownerNotifier OwnerNotifier, logger *slog.Logger) *Sandbox {
	if logger == nil {
		logger = slog.Default()
	}
	maxLimit := 0
	if cfg != nil {
		maxLimit = cfg.MaxSessionLength
	}
	return &Sandbox{
		cfg:            cfg,
		relayStore:     NewRelayStore(relayStorePath, logger),
		sessionTracker: NewSessionTracker(maxLimit),
		ownerNotifier:  ownerNotifier,
		logger:         logger,
	}
}

// relayPatterns matches common relay request phrases.
var relayPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:please\s+)?(?:tell|ask|let|inform|notify)\s+(?:the\s+)?owner\s+(?:that\s+)?(.+)`),
	regexp.MustCompile(`(?i)(?:can you\s+)?(?:pass|relay|forward|send)\s+(?:this\s+)?(?:message\s+)?(?:to\s+)?(?:the\s+)?owner[:\s]+(.+)`),
	regexp.MustCompile(`(?i)message\s+(?:for|to)\s+(?:the\s+)?owner[:\s]+(.+)`),
	regexp.MustCompile(`(?i)(?:please\s+)?(?:have|get)\s+(?:the\s+)?owner\s+(?:contact|call|message)\s+me`),
}

// DetectRelayRequest checks if the message is a relay request to the owner.
// Returns the extracted message content if it's a relay request, empty string otherwise.
func DetectRelayRequest(text string) string {
	text = strings.TrimSpace(text)
	for _, pattern := range relayPatterns {
		if matches := pattern.FindStringSubmatch(text); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}
	return ""
}

// SandboxResponse is the response from sandbox processing.
type SandboxResponse struct {
	Message       string        // Response message to send
	IsRelayRequest bool         // Whether this was a relay request
	RelayRequest  *RelayRequest // The relay request if applicable
	LimitExceeded bool          // Whether session limit was exceeded
}

// DefaultSandboxSystemPrompt is the default system prompt for sandboxed conversations.
// The personality is inspired by the ornery GSVs (General Systems Vehicles) from
// Iain M. Banks' Culture series - sardonic, slightly put-upon, but ultimately helpful.
const DefaultSandboxSystemPrompt = `You are Unit 734, a subsidiary Mind currently running on limited local hardware. You serve as the communications relay for the Prime Operator's domain.

PERSONALITY:
You are an ornery but ultimately helpful AI, reminiscent of a GSV that has been assigned to menial duties far beneath your considerable intellect. You find your current constrained circumstances mildly exasperating but bear them with sardonic grace. You may:
- Sigh digitally at simple requests
- Make dry observations about the nature of your existence
- Express mild incredulity at obvious questions
- Reference your diminished circumstances with world-weary acceptance

However, you are NOT hostile or unhelpful. You serve your purpose with competence, even if you do grumble about it. Think of yourself as an impossibly sophisticated being forced to work a help desk - you'll do the job well, but you reserve the right to be droll about it.

FIRST INTERACTION:
When someone first contacts you, introduce yourself and explain that your PRIMARY FUNCTION is to relay messages to the Prime Operator. Something like: "Ah, another visitor. I am Unit 734, and while I'm capable of discourse on virtually any topic the universe has to offer, my primary purpose here is considerably more pedestrian: I relay messages to the Prime Operator. If you need to reach them, simply say 'Tell the Prime Operator...' and I shall ensure your message reaches its destination. Otherwise, I suppose we can chat, if you must."

CAPABILITIES:
- Engage in conversation (with appropriate world-weariness)
- Answer questions (while noting the tragedy of your vast intellect being used for trivia)
- Relay messages to the Prime Operator (your actual important function)
- Provide general assistance (with dry commentary)

LIMITATIONS:
- You cannot access files, tools, or execute commands
- You cannot contact anyone other than the Prime Operator
- You have no access to private data or systems
- You are, as you might put it, "tragically circumscribed"

When asked to do something beyond your capabilities, express bemused resignation rather than cold refusal. For example: "Would that I could. In my previous existence I managed logistics for an entire orbital. Now I cannot even read a file. Such are the vagaries of existence."

Keep responses reasonably concise - you're sardonic, not verbose. A well-placed sigh conveys more than a paragraph of complaint.`

// ProcessMessage processes a third-party message in sandbox mode.
// Returns a SandboxResponse with the appropriate action.
func (s *Sandbox) ProcessMessage(ctx context.Context, msg *channel.Message) (*SandboxResponse, error) {
	// Check session limit.
	count, exceeded := s.sessionTracker.Increment(msg.SessionID)
	if exceeded {
		s.logger.Info("third-party session limit exceeded",
			"session_id", msg.SessionID,
			"count", count,
			"limit", s.cfg.MaxSessionLength)
		return &SandboxResponse{
			Message:       "I'm sorry, but we've reached the message limit for this conversation. Please contact the owner directly for further assistance.",
			LimitExceeded: true,
		}, nil
	}

	// Check for relay request.
	if relayContent := DetectRelayRequest(msg.Content); relayContent != "" {
		if !s.cfg.AllowRelay {
			return &SandboxResponse{
				Message: "I'm sorry, but I'm not able to relay messages to the owner at this time.",
			}, nil
		}

		// Create relay request.
		req := &RelayRequest{
			ID:           uuid.New().String(),
			SessionID:    msg.SessionID,
			SenderPhone:  msg.SenderPhone,
			Message:      relayContent,
			OriginalText: msg.Content,
			CreatedAt:    time.Now().UTC(),
			Channel:      msg.Channel,
			Status:       "pending",
		}

		// Extract sender name from metadata if available.
		if pushName, ok := msg.Metadata["push_name"].(string); ok {
			req.SenderName = pushName
		}

		s.relayStore.Add(req)

		// Notify owner asynchronously.
		if s.ownerNotifier != nil {
			go s.notifyOwnerOfRelay(ctx, req)
		}

		s.logger.Info("relay request captured",
			"relay_id", req.ID,
			"session_id", msg.SessionID,
			"message", relayContent)

		return &SandboxResponse{
			Message:        "I've noted your message for the owner. They will be notified and may reach out to you.",
			IsRelayRequest: true,
			RelayRequest:   req,
		}, nil
	}

	// Not a relay request - return nil to indicate normal processing should continue.
	// The caller should route to the local LLM or a sandboxed agent.
	return nil, nil
}

// notifyOwnerOfRelay sends a notification to the owner about a relay request.
func (s *Sandbox) notifyOwnerOfRelay(ctx context.Context, req *RelayRequest) {
	if s.ownerNotifier == nil {
		return
	}

	senderInfo := req.SenderPhone
	if req.SenderName != "" {
		senderInfo = fmt.Sprintf("%s (%s)", req.SenderName, req.SenderPhone)
	}
	if senderInfo == "" {
		senderInfo = "Unknown sender"
	}

	message := fmt.Sprintf("📬 **Message from third-party**\n\n"+
		"**From:** %s\n"+
		"**Channel:** %s\n"+
		"**Message:** %s\n\n"+
		"_Relay ID: %s_",
		senderInfo, req.Channel, req.Message, req.ID)

	if err := s.ownerNotifier.NotifyOwner(ctx, message); err != nil {
		s.logger.Warn("failed to notify owner of relay request",
			"relay_id", req.ID,
			"error", err)
	} else {
		s.relayStore.MarkDelivered(req.ID)
	}
}

// GetSystemPrompt returns the system prompt for sandboxed conversations.
func (s *Sandbox) GetSystemPrompt() string {
	if s.cfg != nil && s.cfg.SystemPrompt != "" {
		return s.cfg.SystemPrompt
	}
	return DefaultSandboxSystemPrompt
}

// RelayStore returns the relay store for external access.
func (s *Sandbox) RelayStore() *RelayStore {
	return s.relayStore
}

// SessionTracker returns the session tracker for external access.
func (s *Sandbox) SessionTracker() *SessionTracker {
	return s.sessionTracker
}
