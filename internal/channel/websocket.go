package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins in development
		// TODO: Make this configurable for production
		return true
	},
}

// WebSocketChannel implements the Channel interface for WebSocket connections.
type WebSocketChannel struct {
	addr          string
	server        *http.Server
	messages      chan *Message
	clients       map[string]*wsClient
	mu            sync.RWMutex
	logger        *slog.Logger
	extraHandlers map[string]http.HandlerFunc
}

// wsClient represents a connected WebSocket client.
type wsClient struct {
	id        string
	sessionID string
	conn      *websocket.Conn
	send      chan []byte
	mu        sync.Mutex
}

// NewWebSocketChannel creates a new WebSocket channel.
func NewWebSocketChannel(addr string, logger *slog.Logger) *WebSocketChannel {
	return &WebSocketChannel{
		addr:     addr,
		messages: make(chan *Message, 100),
		clients:  make(map[string]*wsClient),
		logger:   logger,
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
		Addr:    w.addr,
		Handler: mux,
	}

	w.logger.Info("starting WebSocket server", "addr", w.addr)

	go func() {
		<-ctx.Done()
		w.Stop(context.Background())
	}()

	if err := w.server.ListenAndServe(); err != http.ErrServerClosed {
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
	for _, client := range w.clients {
		if client.sessionID == resp.SessionID {
			select {
			case client.send <- data:
			default:
				w.logger.Warn("client send buffer full", "client_id", client.id)
			}
		}
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
	conn, err := upgrader.Upgrade(rw, r, nil)
	if err != nil {
		w.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	// Get or generate session ID from query param
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		sessionID = "main"
	}

	clientID := uuid.New().String()
	client := &wsClient{
		id:        clientID,
		sessionID: sessionID,
		conn:      conn,
		send:      make(chan []byte, 256),
	}

	w.mu.Lock()
	w.clients[clientID] = client
	w.mu.Unlock()

	w.logger.Info("client connected", "client_id", clientID, "session_id", sessionID)

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
			Content   string         `json:"content"`
			SessionID string         `json:"session_id,omitempty"`
			Metadata  map[string]any `json:"metadata,omitempty"`
		}
		if err := json.Unmarshal(data, &wsMsg); err != nil {
			w.logger.Warn("invalid message format", "error", err)
			continue
		}

		// Use provided session ID or client's default
		sessionID := wsMsg.SessionID
		if sessionID == "" {
			sessionID = client.sessionID
		}

		// Create and queue message
		msg := &Message{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			UserID:    client.id,
			Content:   wsMsg.Content,
			Channel:   "websocket",
			Metadata:  wsMsg.Metadata,
		}

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
