package channel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1024 * 1024 // 1MB
)

// TokenValidator is a function that validates an authentication token.
type TokenValidator func(token string) bool

// WebSocketChannel implements the Channel interface for WebSocket connections.
type WebSocketChannel struct {
	addr           string
	server         *http.Server
	messages       chan *Message
	clients        map[string]*wsClient
	mu             sync.RWMutex
	logger         *slog.Logger
	extraHandlers  map[string]http.HandlerFunc
	allowedOrigins map[string]bool // map of allowed origin prefixes for CORS validation
	authRequired   bool            // whether authentication is required
	tokenValidator TokenValidator  // validates auth tokens
	tlsCertFile    string          // TLS certificate file path
	tlsKeyFile     string          // TLS private key file path
}

// wsClient represents a connected WebSocket client.
type wsClient struct {
	id        string
	userID    string
	sessionID string
	conn      *websocket.Conn
	send      chan []byte
	mu        sync.Mutex
}

// WebSocketChannelOptions configures a WebSocket channel.
type WebSocketChannelOptions struct {
	AllowedOrigins []string       // Allowed CORS origins (defaults to localhost)
	AuthRequired   bool           // Whether authentication is required
	TokenValidator TokenValidator // Function to validate tokens (required if AuthRequired)
	TLSCertFile    string         // Path to TLS certificate file (enables TLS if set)
	TLSKeyFile     string         // Path to TLS private key file
}

// NewWebSocketChannel creates a new WebSocket channel.
// allowedOrigins is a list of origin prefixes (e.g., "http://localhost", "https://example.com").
// If empty, defaults to localhost variants only.
func NewWebSocketChannel(addr string, logger *slog.Logger, allowedOrigins []string) *WebSocketChannel {
	return NewWebSocketChannelWithOptions(addr, logger, WebSocketChannelOptions{
		AllowedOrigins: allowedOrigins,
	})
}

// NewWebSocketChannelWithOptions creates a new WebSocket channel with full options.
func NewWebSocketChannelWithOptions(addr string, logger *slog.Logger, opts WebSocketChannelOptions) *WebSocketChannel {
	// Build allowed origins map
	origins := make(map[string]bool)
	allowedOrigins := opts.AllowedOrigins
	if len(allowedOrigins) == 0 {
		// Default to localhost only
		allowedOrigins = []string{
			"http://localhost",
			"https://localhost",
			"http://127.0.0.1",
			"https://127.0.0.1",
		}
	}
	for _, o := range allowedOrigins {
		origins[o] = true
	}

	return &WebSocketChannel{
		addr:           addr,
		messages:       make(chan *Message, 100),
		clients:        make(map[string]*wsClient),
		logger:         logger,
		allowedOrigins: origins,
		authRequired:   opts.AuthRequired,
		tokenValidator: opts.TokenValidator,
		tlsCertFile:    opts.TLSCertFile,
		tlsKeyFile:     opts.TLSKeyFile,
	}
}

// TLSEnabled returns true if TLS is configured.
func (w *WebSocketChannel) TLSEnabled() bool {
	return w.tlsCertFile != "" && w.tlsKeyFile != ""
}

// checkOrigin validates the Origin header against the allowed origins list.
// Returns true if the origin is allowed or if there's no Origin header (same-origin request).
func (w *WebSocketChannel) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header typically means same-origin request
		return true
	}

	// Parse the origin to get scheme and host
	parsed, err := url.Parse(origin)
	if err != nil {
		w.logger.Warn("invalid origin header", "origin", origin, "error", err)
		return false
	}

	// Build origin key (scheme://host, without path)
	originKey := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)

	// Check exact match first
	if w.allowedOrigins[originKey] {
		return true
	}

	// Check prefix match (to handle ports like localhost:3000)
	for allowed := range w.allowedOrigins {
		if strings.HasPrefix(originKey, allowed) {
			return true
		}
	}

	w.logger.Warn("origin not allowed", "origin", origin, "allowed", w.allowedOrigins)
	return false
}

// upgrader returns a configured WebSocket upgrader with origin checking.
func (w *WebSocketChannel) upgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     w.checkOrigin,
	}
}

// Name returns the channel identifier.
func (w *WebSocketChannel) Name() string {
	return "websocket"
}

// HandleFunc registers an additional HTTP handler to be served alongside
// the WebSocket and health endpoints. Must be called before Start.
func (w *WebSocketChannel) HandleFunc(pattern string, handler http.HandlerFunc) {
	if w.extraHandlers == nil {
		w.extraHandlers = make(map[string]http.HandlerFunc)
	}
	w.extraHandlers[pattern] = handler
}

// Start begins the WebSocket server.
func (w *WebSocketChannel) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", w.handleWebSocket)
	mux.HandleFunc("/health", w.handleHealth)
	for pattern, handler := range w.extraHandlers {
		mux.HandleFunc(pattern, handler)
	}

	w.server = &http.Server{
		Addr:              w.addr,
		Handler:           mux,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		w.Stop(context.Background())
	}()

	var err error
	if w.TLSEnabled() {
		w.logger.Info("starting WebSocket server with TLS", "addr", w.addr, "cert", w.tlsCertFile)
		err = w.server.ListenAndServeTLS(w.tlsCertFile, w.tlsKeyFile)
	} else {
		w.logger.Info("starting WebSocket server", "addr", w.addr)
		err = w.server.ListenAndServe()
	}

	if err != http.ErrServerClosed {
		return fmt.Errorf("websocket server error: %w", err)
	}

	return nil
}

// Stop gracefully shuts down the WebSocket server.
func (w *WebSocketChannel) Stop(ctx context.Context) error {
	w.logger.Info("stopping WebSocket server")

	// Close all client connections
	w.mu.Lock()
	for _, client := range w.clients {
		close(client.send)
		client.conn.Close()
	}
	w.clients = make(map[string]*wsClient)
	w.mu.Unlock()

	// Shutdown server
	if w.server != nil {
		return w.server.Shutdown(ctx)
	}
	return nil
}

// Messages returns the channel for receiving incoming messages.
func (w *WebSocketChannel) Messages() <-chan *Message {
	return w.messages
}

// Send sends a response to a specific session.
func (w *WebSocketChannel) Send(ctx context.Context, resp *Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Find clients for this session
	// Also match clients whose session is the parent of a thread response.
	parentSessionID, _ := resp.Metadata["parent_session_id"].(string)

	matched := 0
	for _, client := range w.clients {
		if client.sessionID != resp.SessionID && (parentSessionID == "" || client.sessionID != parentSessionID) {
			continue
		}
		matched++
		select {
		case client.send <- data:
			w.logger.Info("ws:send queued",
				"client_id", client.id,
				"session_id", resp.SessionID,
				"type", resp.Type,
				"done", resp.Done,
				"content_len", len(resp.Content))
		default:
			w.logger.Warn("client send buffer full", "client_id", client.id)
		}
	}
	if matched == 0 {
		w.logger.Warn("ws:send no matching client",
			"session_id", resp.SessionID,
			"type", resp.Type,
			"total_clients", len(w.clients))
	}

	return nil
}

// handleHealth handles health check requests.
func (w *WebSocketChannel) handleHealth(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
	rw.Write([]byte("ok"))
}

// handleWebSocket handles incoming WebSocket connections.
func (w *WebSocketChannel) handleWebSocket(rw http.ResponseWriter, r *http.Request) {
	// Check authentication if required
	if w.authRequired {
		token := w.extractToken(r)
		if token == "" {
			w.logger.Warn("websocket auth: no token provided", "remote", r.RemoteAddr)
			http.Error(rw, "Unauthorized: token required", http.StatusUnauthorized)
			return
		}

		if w.tokenValidator == nil || !w.tokenValidator(token) {
			w.logger.Warn("websocket auth: invalid token", "remote", r.RemoteAddr)
			http.Error(rw, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		w.logger.Debug("websocket auth: token validated", "remote", r.RemoteAddr)
	}

	conn, err := w.upgrader().Upgrade(rw, r, nil)
	if err != nil {
		w.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	// Get or generate session ID from query param.
	// If not specified, generate a UUID for a new session.
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Get user ID from query param so the runner can locate
	// the correct session directory for history continuity.
	userID := r.URL.Query().Get("user_id")

	clientID := uuid.New().String()
	client := &wsClient{
		id:        clientID,
		userID:    userID,
		sessionID: sessionID,
		conn:      conn,
		send:      make(chan []byte, 256),
	}

	w.mu.Lock()
	w.clients[clientID] = client
	w.mu.Unlock()

	w.logger.Info("client connected", "client_id", clientID, "session_id", sessionID)

	// Notify client of assigned session ID so it can track/resume.
	welcome, _ := json.Marshal(map[string]any{
		"type":       "session",
		"session_id": sessionID,
	})
	client.send <- welcome

	// Start reader and writer goroutines
	go w.readPump(client)
	go w.writePump(client)
}

// readPump handles reading messages from the WebSocket connection.
func (w *WebSocketChannel) readPump(client *wsClient) {
	defer func() {
		w.mu.Lock()
		delete(w.clients, client.id)
		w.mu.Unlock()
		client.conn.Close()
		w.logger.Info("client disconnected", "client_id", client.id)
	}()

	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				w.logger.Error("websocket read error", "error", err)
			}
			return
		}

		// Parse incoming message
		var wsMsg struct {
			Content        string         `json:"content"`
			SessionID      string         `json:"session_id,omitempty"`
			ReplyToEventID string         `json:"reply_to_event_id,omitempty"`
			Metadata       map[string]any `json:"metadata,omitempty"`
		}
		if err := json.Unmarshal(data, &wsMsg); err != nil {
			w.logger.Warn("invalid message format", "error", err)
			continue
		}

		// Use provided session ID or client's default.
		// If the client specifies a session, update the client's association
		// so that Send() can route responses back correctly.
		sessionID := wsMsg.SessionID
		if sessionID == "" {
			sessionID = client.sessionID
		} else if sessionID != client.sessionID {
			w.mu.Lock()
			client.sessionID = sessionID
			w.mu.Unlock()
			w.logger.Debug("client session updated", "client_id", client.id, "session_id", sessionID)
		}

		// Create and queue message.
		// Use the authenticated userID when available so the runner
		// can locate the correct session history on disk.
		msgUserID := client.userID
		if msgUserID == "" {
			msgUserID = client.id
		}
		msg := &Message{
			ID:             uuid.New().String(),
			SessionID:      sessionID,
			UserID:         msgUserID,
			Content:        wsMsg.Content,
			Channel:        "websocket",
			Metadata:       wsMsg.Metadata,
			ReplyToEventID: wsMsg.ReplyToEventID,
		}

		// Extract base64-encoded image from metadata and convert to attachment.
		if imgB64, ok := wsMsg.Metadata["image"].(string); ok && imgB64 != "" {
			imgData, err := base64.StdEncoding.DecodeString(imgB64)
			if err == nil {
				tmpDir := filepath.Join(os.TempDir(), "kaggen-uploads")
				os.MkdirAll(tmpDir, 0700) // Secure: owner-only directory
				tmpFile := filepath.Join(tmpDir, uuid.New().String()+".jpg")
				if err := os.WriteFile(tmpFile, imgData, 0600); err == nil { // Secure: owner-only file
					msg.Attachments = append(msg.Attachments, Attachment{
						Path:     tmpFile,
						MimeType: "image/jpeg",
						FileName: "uploaded-image.jpg",
					})
					w.logger.Info("ws:image attachment saved", "path", tmpFile, "size", len(imgData))
				}
			} else {
				w.logger.Warn("ws:image base64 decode failed", "error", err)
			}
			delete(msg.Metadata, "image")
		}

		w.logger.Info("ws:recv message",
			"client_id", client.id,
			"client_session", client.sessionID,
			"msg_session", sessionID,
			"ws_session_field", wsMsg.SessionID,
			"content_len", len(wsMsg.Content))

		select {
		case w.messages <- msg:
		default:
			w.logger.Warn("message queue full, dropping message")
		}
	}
}

// writePump handles writing messages to the WebSocket connection.
func (w *WebSocketChannel) writePump(client *wsClient) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel closed
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			client.mu.Lock()
			err := client.conn.WriteMessage(websocket.TextMessage, message)
			client.mu.Unlock()
			if err != nil {
				w.logger.Error("websocket write error", "error", err)
				return
			}

		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Broadcast sends a message to all connected clients.
func (w *WebSocketChannel) Broadcast(data []byte) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for _, client := range w.clients {
		select {
		case client.send <- data:
		default:
			w.logger.Warn("client send buffer full during broadcast", "client_id", client.id)
		}
	}
}

// ClientCount returns the number of connected clients.
func (w *WebSocketChannel) ClientCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.clients)
}

// extractToken extracts the authentication token from the request.
// Checks query parameter "token" first, then Authorization header.
func (w *WebSocketChannel) extractToken(r *http.Request) string {
	// Check query parameter first (for WebSocket connections)
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	// Support "Bearer <token>" format
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Also support plain token in Authorization header
	return auth
}
