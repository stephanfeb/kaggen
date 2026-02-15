package agent

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	wsDefaultTimeout      = 30 * time.Second
	wsMaxTimeout          = 5 * time.Minute
	wsDefaultPingInterval = 30 * time.Second
	wsMaxBufferSize       = 100           // Max buffered messages per connection
	wsMaxMessageSize      = 1024 * 1024   // 1MB
	wsMaxConnections      = 10            // Max concurrent connections per manager
	wsStaleConnectionTime = 10 * time.Minute
	wsWriteWait           = 10 * time.Second
)

// WebSocketMessage represents a received WebSocket message.
type WebSocketMessage struct {
	Type      string `json:"type"`           // "text", "binary", "ping", "pong", "close"
	Data      string `json:"data,omitempty"` // Text content or base64 for binary
	Timestamp string `json:"timestamp"`      // RFC3339
	Error     string `json:"error,omitempty"`
}

// ConnectionInfo describes an active WebSocket connection.
type ConnectionInfo struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	State        string `json:"state"` // "connecting", "open", "closing", "closed"
	CreatedAt    string `json:"created_at"`
	LastActivity string `json:"last_activity"`
	MessagesSent int    `json:"messages_sent"`
	MessagesRecv int    `json:"messages_received"`
	BufferedMsgs int    `json:"buffered_messages"`
}

// WebSocketManager manages WebSocket connections for a skill.
type WebSocketManager struct {
	mu          sync.RWMutex
	connections map[string]*wsConnection
	maxConns    int
	logger      *slog.Logger
	cleanupDone chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
}

type wsConnection struct {
	id           string
	url          string
	conn         *websocket.Conn
	state        string // "connecting", "open", "closing", "closed"
	createdAt    time.Time
	lastActivity time.Time
	messagesSent int
	messagesRecv int

	// Message buffer for receive action
	buffer   chan WebSocketMessage
	bufferMu sync.Mutex

	// Read pump goroutine
	readCtx    context.Context
	readCancel context.CancelFunc

	// Ping/pong
	pingInterval time.Duration
}

// wsConnectOpts holds options for WebSocket connection.
type wsConnectOpts struct {
	SubProtocols    []string
	InsecureSkipTLS bool
	PingInterval    time.Duration
}

// NewWebSocketManager creates a new connection manager.
func NewWebSocketManager(logger *slog.Logger) *WebSocketManager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &WebSocketManager{
		connections: make(map[string]*wsConnection),
		maxConns:    wsMaxConnections,
		logger:      logger,
		cleanupDone: make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
	go mgr.cleanupLoop()
	return mgr
}

// cleanupLoop periodically closes stale connections.
func (m *WebSocketManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	defer close(m.cleanupDone)

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupStale()
		}
	}
}

func (m *WebSocketManager) cleanupStale() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, conn := range m.connections {
		if now.Sub(conn.lastActivity) > wsStaleConnectionTime {
			m.logger.Info("closing stale websocket connection",
				"connection_id", id,
				"idle_time", now.Sub(conn.lastActivity))
			m.closeConnectionLocked(conn)
			delete(m.connections, id)
		}
	}
}

// Connect establishes a new WebSocket connection.
func (m *WebSocketManager) Connect(ctx context.Context, wsURL string, headers http.Header, opts wsConnectOpts) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check connection limit
	if len(m.connections) >= m.maxConns {
		return "", fmt.Errorf("max connections (%d) reached; close unused connections first", m.maxConns)
	}

	// Validate URL
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		return "", fmt.Errorf("invalid websocket URL: must start with ws:// or wss://")
	}

	// Build dialer
	dialer := &websocket.Dialer{
		HandshakeTimeout: wsDefaultTimeout,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}

	if opts.InsecureSkipTLS {
		// Only allow for localhost
		parsed, err := url.Parse(wsURL)
		if err != nil {
			return "", fmt.Errorf("invalid URL: %w", err)
		}
		host := strings.Split(parsed.Host, ":")[0]
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return "", fmt.Errorf("insecure TLS only allowed for localhost")
		}
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	if len(opts.SubProtocols) > 0 {
		dialer.Subprotocols = opts.SubProtocols
	}

	// Connect
	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil {
			return "", fmt.Errorf("websocket connect failed (HTTP %d): %w", resp.StatusCode, err)
		}
		return "", fmt.Errorf("websocket connect failed: %w", err)
	}

	// Generate connection ID
	connID := uuid.New().String()[:8] // Short ID for usability

	// Set up connection
	readCtx, readCancel := context.WithCancel(m.ctx)
	pingInterval := opts.PingInterval
	if pingInterval == 0 {
		pingInterval = wsDefaultPingInterval
	}

	wsConn := &wsConnection{
		id:           connID,
		url:          wsURL,
		conn:         conn,
		state:        "open",
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		buffer:       make(chan WebSocketMessage, wsMaxBufferSize),
		readCtx:      readCtx,
		readCancel:   readCancel,
		pingInterval: pingInterval,
	}

	m.connections[connID] = wsConn

	// Start read pump
	go m.readPump(wsConn)

	m.logger.Info("websocket connected", "connection_id", connID, "url", wsURL)
	return connID, nil
}

// readPump reads messages from the WebSocket and buffers them.
func (m *WebSocketManager) readPump(wc *wsConnection) {
	defer func() {
		m.mu.Lock()
		if wc.state != "closed" {
			wc.state = "closed"
		}
		m.mu.Unlock()
	}()

	wc.conn.SetReadLimit(wsMaxMessageSize)
	wc.conn.SetPongHandler(func(string) error {
		wc.conn.SetReadDeadline(time.Now().Add(wc.pingInterval * 2))
		return nil
	})

	// Start ping sender
	go m.pingLoop(wc)

	for {
		select {
		case <-wc.readCtx.Done():
			return
		default:
		}

		msgType, data, err := wc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				m.logger.Warn("websocket read error", "connection_id", wc.id, "error", err)
			}
			// Buffer error message for receive action
			select {
			case wc.buffer <- WebSocketMessage{
				Type:      "close",
				Error:     err.Error(),
				Timestamp: time.Now().Format(time.RFC3339),
			}:
			default:
			}
			return
		}

		wc.lastActivity = time.Now()
		wc.messagesRecv++

		msg := WebSocketMessage{
			Timestamp: time.Now().Format(time.RFC3339),
		}

		switch msgType {
		case websocket.TextMessage:
			msg.Type = "text"
			msg.Data = string(data)
		case websocket.BinaryMessage:
			msg.Type = "binary"
			msg.Data = base64.StdEncoding.EncodeToString(data)
		case websocket.PingMessage:
			msg.Type = "ping"
		case websocket.PongMessage:
			msg.Type = "pong"
		}

		// Buffer message (non-blocking, drop if full)
		select {
		case wc.buffer <- msg:
		default:
			m.logger.Warn("websocket buffer full, dropping message",
				"connection_id", wc.id)
		}
	}
}

func (m *WebSocketManager) pingLoop(wc *wsConnection) {
	ticker := time.NewTicker(wc.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-wc.readCtx.Done():
			return
		case <-ticker.C:
			if err := wc.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				return
			}
		}
	}
}

// Send sends a message on an existing connection.
func (m *WebSocketManager) Send(connID string, messageType int, data []byte) error {
	m.mu.RLock()
	wc, ok := m.connections[connID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	if wc.state != "open" {
		return fmt.Errorf("connection %q is %s", connID, wc.state)
	}

	wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	if err := wc.conn.WriteMessage(messageType, data); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	wc.lastActivity = time.Now()
	wc.messagesSent++
	return nil
}

// Receive waits for messages on a connection.
func (m *WebSocketManager) Receive(ctx context.Context, connID string, count int, drain bool) ([]WebSocketMessage, error) {
	m.mu.RLock()
	wc, ok := m.connections[connID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("connection %q not found", connID)
	}

	messages := make([]WebSocketMessage, 0, count)

	// Drain mode: return all buffered messages immediately
	if drain {
		for {
			select {
			case msg := <-wc.buffer:
				messages = append(messages, msg)
			default:
				return messages, nil
			}
		}
	}

	// Wait mode: block until we have count messages or timeout
	for len(messages) < count {
		select {
		case <-ctx.Done():
			return messages, ctx.Err()
		case msg := <-wc.buffer:
			messages = append(messages, msg)
			if msg.Type == "close" {
				return messages, nil // Connection closed
			}
		}
	}

	wc.lastActivity = time.Now()
	return messages, nil
}

// Close closes a specific connection.
func (m *WebSocketManager) Close(connID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	wc, ok := m.connections[connID]
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}

	m.closeConnectionLocked(wc)
	delete(m.connections, connID)
	return nil
}

func (m *WebSocketManager) closeConnectionLocked(wc *wsConnection) {
	wc.state = "closing"
	wc.readCancel()
	wc.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(5*time.Second))
	wc.conn.Close()
	wc.state = "closed"
	close(wc.buffer)
}

// ListConnections returns info about all connections.
func (m *WebSocketManager) ListConnections() []ConnectionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]ConnectionInfo, 0, len(m.connections))
	for _, wc := range m.connections {
		infos = append(infos, ConnectionInfo{
			ID:           wc.id,
			URL:          wc.url,
			State:        wc.state,
			CreatedAt:    wc.createdAt.Format(time.RFC3339),
			LastActivity: wc.lastActivity.Format(time.RFC3339),
			MessagesSent: wc.messagesSent,
			MessagesRecv: wc.messagesRecv,
			BufferedMsgs: len(wc.buffer),
		})
	}
	return infos
}

// Shutdown closes all connections and stops the cleanup loop.
func (m *WebSocketManager) Shutdown() {
	m.cancel()
	<-m.cleanupDone

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, conn := range m.connections {
		m.closeConnectionLocked(conn)
		delete(m.connections, id)
	}
}
