package p2p

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/yourusername/kaggen/internal/channel"
	pb "github.com/yourusername/kaggen/internal/p2p/proto"
)

// p2pClient represents a connected P2P peer with an active stream.
type p2pClient struct {
	id        string           // unique client ID
	peerID    peer.ID          // libp2p peer ID
	sessionID string           // session identifier
	userID    string           // user identifier (defaults to peer ID prefix)
	stream    network.Stream   // active stream for responses
	mu        sync.Mutex       // protects stream writes
}

// P2PChannel implements channel.Channel for libp2p connections.
type P2PChannel struct {
	node          *Node
	messages      chan *channel.Message
	clients       map[string]*p2pClient // clientID -> client
	mu            sync.RWMutex
	logger        *slog.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	authenticator *StreamAuthenticator // optional token authenticator
}

// NewP2PChannel creates a new P2P channel.
func NewP2PChannel(node *Node, logger *slog.Logger) *P2PChannel {
	return &P2PChannel{
		node:     node,
		messages: make(chan *channel.Message, 100),
		clients:  make(map[string]*p2pClient),
		logger:   logger,
	}
}

// SetAuthenticator sets the token authenticator for P2P streams.
// If set and auth is required, clients must authenticate before chatting.
func (c *P2PChannel) SetAuthenticator(auth *StreamAuthenticator) {
	c.authenticator = auth
}

// Name returns the channel identifier.
func (c *P2PChannel) Name() string {
	return "p2p"
}

// Start begins listening for incoming P2P streams.
func (c *P2PChannel) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Register the chat protocol handler.
	c.node.Host().SetStreamHandler(ChatProtocolID, c.handleStream)
	c.logger.Info("P2P channel started", "protocol", ChatProtocolID)

	return nil
}

// Stop gracefully shuts down the P2P channel.
func (c *P2PChannel) Stop(ctx context.Context) error {
	c.logger.Info("stopping P2P channel")

	if c.cancel != nil {
		c.cancel()
	}

	// Remove the stream handler.
	c.node.Host().RemoveStreamHandler(ChatProtocolID)

	// Close all client streams.
	c.mu.Lock()
	for _, client := range c.clients {
		client.stream.Close()
	}
	c.clients = make(map[string]*p2pClient)
	c.mu.Unlock()

	return nil
}

// Messages returns the channel for receiving incoming messages.
func (c *P2PChannel) Messages() <-chan *channel.Message {
	return c.messages
}

// Send sends a response to a specific session.
func (c *P2PChannel) Send(ctx context.Context, resp *channel.Response) error {
	// Convert channel.Response to protobuf.
	pbResp := &pb.ChatResponse{
		Id:        resp.ID,
		MessageId: resp.MessageID,
		SessionId: resp.SessionID,
		Content:   resp.Content,
		Type:      resp.Type,
		Done:      resp.Done,
	}

	// Convert metadata to protobuf map.
	if len(resp.Metadata) > 0 {
		pbResp.Metadata = make(map[string][]byte)
		for k, v := range resp.Metadata {
			data, err := json.Marshal(v)
			if err != nil {
				c.logger.Warn("failed to marshal metadata value", "key", k, "error", err)
				continue
			}
			pbResp.Metadata[k] = data
		}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Find clients for this session.
	parentSessionID, _ := resp.Metadata["parent_session_id"].(string)

	matched := 0
	for _, client := range c.clients {
		if client.sessionID != resp.SessionID && (parentSessionID == "" || client.sessionID != parentSessionID) {
			continue
		}
		matched++

		client.mu.Lock()
		err := WriteMessage(client.stream, pbResp)
		client.mu.Unlock()

		if err != nil {
			c.logger.Warn("failed to send response to peer",
				"peer_id", client.peerID,
				"client_id", client.id,
				"error", err)
			continue
		}

		c.logger.Info("p2p:send",
			"client_id", client.id,
			"session_id", resp.SessionID,
			"type", resp.Type,
			"done", resp.Done,
			"content_len", len(resp.Content))
	}

	if matched == 0 {
		c.logger.Warn("p2p:send no matching client",
			"session_id", resp.SessionID,
			"type", resp.Type,
			"total_clients", len(c.clients))
	}

	return nil
}

// handleStream handles incoming P2P chat streams.
func (c *P2PChannel) handleStream(stream network.Stream) {
	peerID := stream.Conn().RemotePeer()
	clientID := uuid.New().String()

	// Derive default session ID from peer ID (first 16 chars).
	peerIDStr := peerID.String()
	defaultSessionID := peerIDStr
	if len(defaultSessionID) > 16 {
		defaultSessionID = defaultSessionID[:16]
	}
	defaultUserID := defaultSessionID

	// Perform authentication handshake if configured.
	authenticated := false
	if c.authenticator != nil {
		authResult, err := c.authenticator.AuthenticateStream(stream)
		if err != nil {
			c.logger.Warn("P2P auth failed, closing stream",
				"peer", peerID,
				"error", err)
			stream.Close()
			return
		}
		// Use auth result if provided (auth was required and succeeded).
		if authResult != nil {
			defaultSessionID = authResult.SessionID
			defaultUserID = authResult.UserID
			authenticated = true
		}
	}

	client := &p2pClient{
		id:        clientID,
		peerID:    peerID,
		sessionID: defaultSessionID,
		userID:    defaultUserID,
		stream:    stream,
	}

	c.mu.Lock()
	c.clients[clientID] = client
	c.mu.Unlock()

	c.logger.Info("P2P client connected",
		"client_id", clientID,
		"peer_id", peerID,
		"session_id", defaultSessionID,
		"authenticated", authenticated)

	// Send session welcome message.
	welcome := &pb.ChatResponse{
		Id:        uuid.New().String(),
		SessionId: defaultSessionID,
		Type:      "session",
	}
	if err := WriteMessage(stream, welcome); err != nil {
		c.logger.Error("failed to send welcome", "error", err)
		stream.Close()
		return
	}

	// Read messages from the stream, passing auth status.
	c.readLoop(client, authenticated)

	// Cleanup on disconnect.
	c.mu.Lock()
	delete(c.clients, clientID)
	c.mu.Unlock()

	c.logger.Info("P2P client disconnected",
		"client_id", clientID,
		"peer_id", peerID)
}

// readLoop reads messages from a client stream until it closes.
// The authenticated parameter indicates if the client completed token auth.
func (c *P2PChannel) readLoop(client *p2pClient, authenticated bool) {
	defer client.stream.Close()

	for {
		// Check if context is done.
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		// Read incoming message.
		var pbMsg pb.ChatMessage
		if err := ReadMessage(client.stream, &pbMsg); err != nil {
			if c.ctx.Err() == nil {
				c.logger.Info("P2P read error (client disconnected)",
					"client_id", client.id,
					"error", err)
			}
			return
		}

		// Update session if client specifies one.
		if pbMsg.SessionId != "" && pbMsg.SessionId != client.sessionID {
			c.mu.Lock()
			client.sessionID = pbMsg.SessionId
			c.mu.Unlock()
			c.logger.Debug("P2P client session updated",
				"client_id", client.id,
				"session_id", pbMsg.SessionId)
		}

		// Update user ID if client specifies one.
		if pbMsg.UserId != "" && pbMsg.UserId != client.userID {
			c.mu.Lock()
			client.userID = pbMsg.UserId
			c.mu.Unlock()
		}

		// Build channel.Message.
		// P2P clients are trusted if: auth was not required (backwards compat),
		// or auth was required and they authenticated successfully.
		isAllowed := !c.authenticator.IsAuthRequired() || authenticated
		msg := &channel.Message{
			ID:             pbMsg.Id,
			SessionID:      client.sessionID,
			UserID:         client.userID,
			Content:        pbMsg.Content,
			Channel:        "p2p",
			ReplyToEventID: pbMsg.ReplyToEventId,
			IsInAllowlist:  isAllowed,
		}

		// Generate message ID if not provided.
		if msg.ID == "" {
			msg.ID = uuid.New().String()
		}

		// Convert metadata from protobuf.
		if len(pbMsg.Metadata) > 0 {
			msg.Metadata = make(map[string]any)
			for k, v := range pbMsg.Metadata {
				var val any
				if err := json.Unmarshal(v, &val); err != nil {
					msg.Metadata[k] = string(v) // Fallback to string
				} else {
					msg.Metadata[k] = val
				}
			}
		}

		// Process attachments.
		if len(pbMsg.Attachments) > 0 {
			tmpDir := filepath.Join(os.TempDir(), "kaggen-uploads")
			os.MkdirAll(tmpDir, 0700)

			for _, att := range pbMsg.Attachments {
				// Generate unique filename.
				filename := uuid.New().String()
				if att.Filename != "" {
					filename = att.Filename
				}
				tmpFile := filepath.Join(tmpDir, filename)

				if err := os.WriteFile(tmpFile, att.Data, 0600); err != nil {
					c.logger.Warn("failed to save attachment",
						"filename", att.Filename,
						"error", err)
					continue
				}

				msg.Attachments = append(msg.Attachments, channel.Attachment{
					Path:     tmpFile,
					MimeType: att.MimeType,
					FileName: att.Filename,
				})
				c.logger.Info("p2p:attachment saved",
					"path", tmpFile,
					"size", len(att.Data))
			}
		}

		c.logger.Info("p2p:recv message",
			"client_id", client.id,
			"peer_id", client.peerID,
			"session_id", client.sessionID,
			"content_len", len(pbMsg.Content))

		// Queue message for processing.
		select {
		case c.messages <- msg:
		default:
			c.logger.Warn("message queue full, dropping message")
		}
	}
}

// ClientCount returns the number of connected P2P clients.
func (c *P2PChannel) ClientCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.clients)
}

// Broadcast sends data to all connected P2P clients.
// The data should be a JSON-encoded response.
func (c *P2PChannel) Broadcast(data []byte) {
	// Parse the JSON to create a protobuf response.
	var resp channel.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		c.logger.Warn("failed to unmarshal broadcast data", "error", err)
		return
	}

	// Send via the normal Send path.
	c.Send(context.Background(), &resp)
}
