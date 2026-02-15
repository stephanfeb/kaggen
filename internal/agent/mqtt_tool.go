package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/secrets"
)

const (
	mqttDefaultTimeout      = 30 * time.Second
	mqttMaxTimeout          = 5 * time.Minute
	mqttConnectTimeout      = 10 * time.Second
	mqttMaxMessageBuffer    = 100
	mqttMaxConnections      = 10
	mqttStaleConnectionTime = 10 * time.Minute
)

// MQTTToolArgs defines the input arguments for the mqtt tool.
type MQTTToolArgs struct {
	// Action selection
	Action string `json:"action" jsonschema:"required,description=Action to perform: connect publish subscribe receive unsubscribe disconnect list_connections,enum=connect,enum=publish,enum=subscribe,enum=receive,enum=unsubscribe,enum=disconnect,enum=list_connections"`

	// Connection
	Broker       string `json:"broker,omitempty" jsonschema:"description=Broker name (from config). Required for connect."`
	ConnectionID string `json:"connection_id,omitempty" jsonschema:"description=Connection ID from connect. Required for publish/subscribe/receive/unsubscribe/disconnect."`

	// Publish/Subscribe
	Topic   string `json:"topic,omitempty" jsonschema:"description=MQTT topic. Required for publish/subscribe. Supports wildcards (+ single level or # multi level) for subscribe."`
	Payload string `json:"payload,omitempty" jsonschema:"description=Message payload as string. Required for publish."`
	PayloadJSON any    `json:"payload_json,omitempty" jsonschema:"description=Message payload as JSON (auto-serialized). Alternative to payload."`

	// QoS and options
	QoS      int  `json:"qos,omitempty" jsonschema:"description=Quality of Service level: 0 (at most once) 1 (at least once) 2 (exactly once). Default: 0."`
	Retained bool `json:"retained,omitempty" jsonschema:"description=Set retained flag on published message."`

	// Receive options
	TimeoutSecs int  `json:"timeout_seconds,omitempty" jsonschema:"description=Timeout for receive in seconds (default: 30 max: 300)."`
	WaitCount   int  `json:"wait_count,omitempty" jsonschema:"description=Number of messages to wait for (default: 1)."`
	DrainBuffer bool `json:"drain_buffer,omitempty" jsonschema:"description=Return all buffered messages immediately without waiting."`
}

// MQTTToolResult is the result of an MQTT operation.
type MQTTToolResult struct {
	Success      bool              `json:"success"`
	Message      string            `json:"message"`
	ConnectionID string            `json:"connection_id,omitempty"` // For connect
	Messages     []MQTTMessage     `json:"messages,omitempty"`      // For receive
	Connections  []MQTTConnInfo    `json:"connections,omitempty"`   // For list_connections
	Topics       []string          `json:"topics,omitempty"`        // Active subscriptions
}

// MQTTMessage represents a received MQTT message.
type MQTTMessage struct {
	Topic     string `json:"topic"`
	Payload   string `json:"payload"`
	QoS       int    `json:"qos"`
	Retained  bool   `json:"retained"`
	Timestamp string `json:"timestamp"`
}

// MQTTConnInfo describes an active MQTT connection.
type MQTTConnInfo struct {
	ID            string   `json:"id"`
	Broker        string   `json:"broker"`
	Connected     bool     `json:"connected"`
	Subscriptions []string `json:"subscriptions"`
	CreatedAt     string   `json:"created_at"`
	LastActivity  string   `json:"last_activity"`
	MessagesSent  int      `json:"messages_sent"`
	MessagesRecv  int      `json:"messages_received"`
	BufferedMsgs  int      `json:"buffered_messages"`
}

// MQTTConnectionManager manages MQTT broker connections.
type MQTTConnectionManager struct {
	mu          sync.RWMutex
	connections map[string]*mqttConnection
	configs     map[string]config.MQTTBroker
	maxConns    int
	logger      *slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	cleanupDone chan struct{}
}

type mqttConnection struct {
	id            string
	brokerName    string
	client        mqtt.Client
	subscriptions map[string]byte // topic -> QoS
	createdAt     time.Time
	lastActivity  time.Time
	messagesSent  int
	messagesRecv  int

	// Message buffer for receive action
	buffer chan MQTTMessage
	mu     sync.Mutex
}

// NewMQTTConnectionManager creates a new MQTT connection manager.
func NewMQTTConnectionManager(logger *slog.Logger) *MQTTConnectionManager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &MQTTConnectionManager{
		connections: make(map[string]*mqttConnection),
		configs:     make(map[string]config.MQTTBroker),
		maxConns:    mqttMaxConnections,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
		cleanupDone: make(chan struct{}),
	}
	go mgr.cleanupLoop()
	return mgr
}

// RegisterBroker registers an MQTT broker configuration.
func (m *MQTTConnectionManager) RegisterBroker(name string, cfg config.MQTTBroker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[name] = cfg
}

// cleanupLoop periodically closes stale connections.
func (m *MQTTConnectionManager) cleanupLoop() {
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

func (m *MQTTConnectionManager) cleanupStale() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, conn := range m.connections {
		if now.Sub(conn.lastActivity) > mqttStaleConnectionTime {
			m.logger.Info("closing stale mqtt connection",
				"connection_id", id,
				"idle_time", now.Sub(conn.lastActivity))
			m.closeConnectionLocked(conn)
			delete(m.connections, id)
		}
	}
}

// Connect establishes a new MQTT connection to a broker.
func (m *MQTTConnectionManager) Connect(ctx context.Context, brokerName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check connection limit
	if len(m.connections) >= m.maxConns {
		return "", fmt.Errorf("max connections (%d) reached; disconnect unused connections first", m.maxConns)
	}

	// Get broker config
	cfg, ok := m.configs[brokerName]
	if !ok {
		return "", fmt.Errorf("broker %q not configured", brokerName)
	}

	// Resolve password from secrets
	password := cfg.Password
	if strings.HasPrefix(password, "secret:") {
		secretName := strings.TrimPrefix(password, "secret:")
		store := secrets.DefaultStore()
		val, err := store.Get(secretName)
		if err != nil {
			return "", fmt.Errorf("failed to get secret %q: %w", secretName, err)
		}
		password = val
	}

	// Build broker URL
	port := cfg.Port
	if port == 0 {
		if cfg.TLS {
			port = 8883
		} else {
			port = 1883
		}
	}
	scheme := "tcp"
	if cfg.TLS {
		scheme = "ssl"
	}
	brokerURL := fmt.Sprintf("%s://%s:%d", scheme, cfg.Host, port)

	// Generate client ID and connection ID
	connID := uuid.New().String()[:8]
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("kaggen-%s", connID)
	}

	// Configure MQTT client
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.SetConnectTimeout(mqttConnectTimeout)
	opts.SetAutoReconnect(true)
	opts.SetCleanSession(true)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(password)
	}

	// Configure TLS
	if cfg.TLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: cfg.SkipVerify,
		}

		// Load CA cert if provided
		if cfg.CACert != "" {
			caCert, err := os.ReadFile(config.ExpandPath(cfg.CACert))
			if err != nil {
				return "", fmt.Errorf("failed to read CA cert: %w", err)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)
			tlsConfig.RootCAs = caCertPool
		}

		// Load client cert if provided
		if cfg.ClientCert != "" && cfg.ClientKey != "" {
			cert, err := tls.LoadX509KeyPair(
				config.ExpandPath(cfg.ClientCert),
				config.ExpandPath(cfg.ClientKey),
			)
			if err != nil {
				return "", fmt.Errorf("failed to load client cert: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		opts.SetTLSConfig(tlsConfig)
	}

	// Create connection struct
	mqttConn := &mqttConnection{
		id:            connID,
		brokerName:    brokerName,
		subscriptions: make(map[string]byte),
		createdAt:     time.Now(),
		lastActivity:  time.Now(),
		buffer:        make(chan MQTTMessage, mqttMaxMessageBuffer),
	}

	// Set message handler
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		mqttConn.mu.Lock()
		mqttConn.messagesRecv++
		mqttConn.lastActivity = time.Now()
		mqttConn.mu.Unlock()

		mqttMsg := MQTTMessage{
			Topic:     msg.Topic(),
			Payload:   string(msg.Payload()),
			QoS:       int(msg.Qos()),
			Retained:  msg.Retained(),
			Timestamp: time.Now().Format(time.RFC3339),
		}

		select {
		case mqttConn.buffer <- mqttMsg:
		default:
			m.logger.Warn("mqtt buffer full, dropping message",
				"connection_id", connID, "topic", msg.Topic())
		}
	})

	// Connect
	client := mqtt.NewClient(opts)
	token := client.Connect()

	// Wait with timeout
	done := make(chan bool, 1)
	go func() {
		token.Wait()
		done <- true
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-done:
		if token.Error() != nil {
			return "", fmt.Errorf("connection failed: %w", token.Error())
		}
	}

	mqttConn.client = client
	m.connections[connID] = mqttConn

	m.logger.Info("mqtt connected", "connection_id", connID, "broker", brokerName)
	return connID, nil
}

// Publish publishes a message to a topic.
func (m *MQTTConnectionManager) Publish(connID, topic string, payload []byte, qos byte, retained bool) error {
	m.mu.RLock()
	conn, ok := m.connections[connID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}

	if !conn.client.IsConnected() {
		return fmt.Errorf("connection %q is not connected", connID)
	}

	token := conn.client.Publish(topic, qos, retained, payload)
	token.Wait()
	if token.Error() != nil {
		return fmt.Errorf("publish failed: %w", token.Error())
	}

	conn.mu.Lock()
	conn.messagesSent++
	conn.lastActivity = time.Now()
	conn.mu.Unlock()

	return nil
}

// Subscribe subscribes to a topic.
func (m *MQTTConnectionManager) Subscribe(connID, topic string, qos byte) error {
	m.mu.RLock()
	conn, ok := m.connections[connID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}

	if !conn.client.IsConnected() {
		return fmt.Errorf("connection %q is not connected", connID)
	}

	token := conn.client.Subscribe(topic, qos, nil)
	token.Wait()
	if token.Error() != nil {
		return fmt.Errorf("subscribe failed: %w", token.Error())
	}

	conn.mu.Lock()
	conn.subscriptions[topic] = qos
	conn.lastActivity = time.Now()
	conn.mu.Unlock()

	return nil
}

// Unsubscribe unsubscribes from a topic.
func (m *MQTTConnectionManager) Unsubscribe(connID, topic string) error {
	m.mu.RLock()
	conn, ok := m.connections[connID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}

	if !conn.client.IsConnected() {
		return fmt.Errorf("connection %q is not connected", connID)
	}

	token := conn.client.Unsubscribe(topic)
	token.Wait()
	if token.Error() != nil {
		return fmt.Errorf("unsubscribe failed: %w", token.Error())
	}

	conn.mu.Lock()
	delete(conn.subscriptions, topic)
	conn.lastActivity = time.Now()
	conn.mu.Unlock()

	return nil
}

// Receive waits for messages.
func (m *MQTTConnectionManager) Receive(ctx context.Context, connID string, count int, drain bool) ([]MQTTMessage, error) {
	m.mu.RLock()
	conn, ok := m.connections[connID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("connection %q not found", connID)
	}

	messages := make([]MQTTMessage, 0, count)

	// Drain mode: return all buffered messages immediately
	if drain {
		for {
			select {
			case msg := <-conn.buffer:
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
		case msg := <-conn.buffer:
			messages = append(messages, msg)
		}
	}

	conn.mu.Lock()
	conn.lastActivity = time.Now()
	conn.mu.Unlock()

	return messages, nil
}

// Disconnect closes a connection.
func (m *MQTTConnectionManager) Disconnect(connID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, ok := m.connections[connID]
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}

	m.closeConnectionLocked(conn)
	delete(m.connections, connID)
	return nil
}

func (m *MQTTConnectionManager) closeConnectionLocked(conn *mqttConnection) {
	if conn.client != nil && conn.client.IsConnected() {
		conn.client.Disconnect(250)
	}
	close(conn.buffer)
}

// ListConnections returns info about all connections.
func (m *MQTTConnectionManager) ListConnections() []MQTTConnInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]MQTTConnInfo, 0, len(m.connections))
	for _, conn := range m.connections {
		conn.mu.Lock()
		subs := make([]string, 0, len(conn.subscriptions))
		for topic := range conn.subscriptions {
			subs = append(subs, topic)
		}
		infos = append(infos, MQTTConnInfo{
			ID:            conn.id,
			Broker:        conn.brokerName,
			Connected:     conn.client != nil && conn.client.IsConnected(),
			Subscriptions: subs,
			CreatedAt:     conn.createdAt.Format(time.RFC3339),
			LastActivity:  conn.lastActivity.Format(time.RFC3339),
			MessagesSent:  conn.messagesSent,
			MessagesRecv:  conn.messagesRecv,
			BufferedMsgs:  len(conn.buffer),
		})
		conn.mu.Unlock()
	}
	return infos
}

// BrokerNames returns a list of registered broker names.
func (m *MQTTConnectionManager) BrokerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	return names
}

// Shutdown closes all connections.
func (m *MQTTConnectionManager) Shutdown() {
	m.cancel()
	<-m.cleanupDone

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, conn := range m.connections {
		m.closeConnectionLocked(conn)
		delete(m.connections, id)
	}
}

// NewMQTTTool creates an MQTT tool with the given connection manager.
func NewMQTTTool(manager *MQTTConnectionManager) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args MQTTToolArgs) (*MQTTToolResult, error) {
			return executeMQTTTool(ctx, args, manager)
		},
		function.WithName("mqtt"),
		function.WithDescription("Interact with MQTT brokers for IoT and pub/sub messaging. Actions: connect (establish connection), publish (send message), subscribe (listen to topic), receive (get messages), unsubscribe (stop listening), disconnect (close connection), list_connections (show active). Supports QoS 0/1/2 and topic wildcards (+/#)."),
	)
}

func executeMQTTTool(
	ctx context.Context,
	args MQTTToolArgs,
	manager *MQTTConnectionManager,
) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	switch args.Action {
	case "connect":
		return connectMQTT(ctx, args, manager)
	case "publish":
		return publishMQTT(ctx, args, manager)
	case "subscribe":
		return subscribeMQTT(ctx, args, manager)
	case "receive":
		return receiveMQTT(ctx, args, manager)
	case "unsubscribe":
		return unsubscribeMQTT(ctx, args, manager)
	case "disconnect":
		return disconnectMQTT(args, manager)
	case "list_connections":
		return listMQTTConnections(manager)
	default:
		result.Message = fmt.Sprintf("Unknown action %q. Use: connect, publish, subscribe, receive, unsubscribe, disconnect, list_connections", args.Action)
		return result, nil
	}
}

func connectMQTT(ctx context.Context, args MQTTToolArgs, manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	if args.Broker == "" {
		result.Message = "Error: 'broker' is required for connect action"
		return result, nil
	}

	connID, err := manager.Connect(ctx, args.Broker)
	if err != nil {
		result.Message = fmt.Sprintf("Connection failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Connected to broker %q", args.Broker)
	result.ConnectionID = connID
	return result, nil
}

func publishMQTT(ctx context.Context, args MQTTToolArgs, manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for publish action"
		return result, nil
	}
	if args.Topic == "" {
		result.Message = "Error: 'topic' is required for publish action"
		return result, nil
	}

	var payload []byte
	if args.PayloadJSON != nil {
		var err error
		payload, err = json.Marshal(args.PayloadJSON)
		if err != nil {
			result.Message = fmt.Sprintf("Error serializing payload_json: %v", err)
			return result, nil
		}
	} else if args.Payload != "" {
		payload = []byte(args.Payload)
	} else {
		result.Message = "Error: 'payload' or 'payload_json' is required for publish action"
		return result, nil
	}

	qos := byte(args.QoS)
	if qos > 2 {
		qos = 0
	}

	if err := manager.Publish(args.ConnectionID, args.Topic, payload, qos, args.Retained); err != nil {
		result.Message = fmt.Sprintf("Publish failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Published %d bytes to topic %q (QoS %d)", len(payload), args.Topic, qos)
	return result, nil
}

func subscribeMQTT(ctx context.Context, args MQTTToolArgs, manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for subscribe action"
		return result, nil
	}
	if args.Topic == "" {
		result.Message = "Error: 'topic' is required for subscribe action"
		return result, nil
	}

	qos := byte(args.QoS)
	if qos > 2 {
		qos = 0
	}

	if err := manager.Subscribe(args.ConnectionID, args.Topic, qos); err != nil {
		result.Message = fmt.Sprintf("Subscribe failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Subscribed to topic %q (QoS %d)", args.Topic, qos)
	return result, nil
}

func receiveMQTT(ctx context.Context, args MQTTToolArgs, manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for receive action"
		return result, nil
	}

	// Set timeout
	timeout := mqttDefaultTimeout
	if args.TimeoutSecs > 0 {
		timeout = time.Duration(args.TimeoutSecs) * time.Second
		if timeout > mqttMaxTimeout {
			timeout = mqttMaxTimeout
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	count := args.WaitCount
	if count <= 0 {
		count = 1
	}

	messages, err := manager.Receive(ctx, args.ConnectionID, count, args.DrainBuffer)
	if err != nil && err != context.DeadlineExceeded {
		result.Message = fmt.Sprintf("Receive failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Messages = messages
	if len(messages) == 0 {
		result.Message = "No messages received (timeout)"
	} else {
		result.Message = fmt.Sprintf("Received %d message(s)", len(messages))
	}
	return result, nil
}

func unsubscribeMQTT(ctx context.Context, args MQTTToolArgs, manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for unsubscribe action"
		return result, nil
	}
	if args.Topic == "" {
		result.Message = "Error: 'topic' is required for unsubscribe action"
		return result, nil
	}

	if err := manager.Unsubscribe(args.ConnectionID, args.Topic); err != nil {
		result.Message = fmt.Sprintf("Unsubscribe failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Unsubscribed from topic %q", args.Topic)
	return result, nil
}

func disconnectMQTT(args MQTTToolArgs, manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	result := &MQTTToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for disconnect action"
		return result, nil
	}

	if err := manager.Disconnect(args.ConnectionID); err != nil {
		result.Message = fmt.Sprintf("Disconnect failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Disconnected connection %s", args.ConnectionID)
	return result, nil
}

func listMQTTConnections(manager *MQTTConnectionManager) (*MQTTToolResult, error) {
	connections := manager.ListConnections()
	return &MQTTToolResult{
		Success:     true,
		Message:     fmt.Sprintf("Found %d active connection(s)", len(connections)),
		Connections: connections,
	}, nil
}
