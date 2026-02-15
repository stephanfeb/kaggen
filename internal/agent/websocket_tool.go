package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/oauth"
)

// WebSocketToolArgs defines the input arguments for the websocket tool.
type WebSocketToolArgs struct {
	// Action selection
	Action string `json:"action" jsonschema:"required,description=Action to perform: connect send receive close list_connections,enum=connect,enum=send,enum=receive,enum=close,enum=list_connections"`

	// Connection identification
	ConnectionID string `json:"connection_id,omitempty" jsonschema:"description=Connection identifier. Required for send/receive/close. Returned by connect."`

	// Connect action fields
	URL     string            `json:"url,omitempty" jsonschema:"description=WebSocket URL (wss:// or ws://). Required for connect."`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"description=Custom headers for connection (e.g. Authorization)."`

	// Authentication (like http_request)
	AuthSecret    string `json:"auth_secret,omitempty" jsonschema:"description=Name of secret for authentication. Injected as Authorization header."`
	AuthScheme    string `json:"auth_scheme,omitempty" jsonschema:"description=Auth scheme: bearer (default) or api-key."`
	OAuthProvider string `json:"oauth_provider,omitempty" jsonschema:"description=OAuth provider name. Uses stored token for authentication."`

	// Send action fields
	Message     string `json:"message,omitempty" jsonschema:"description=Text message to send. Required for send action."`
	MessageJSON any    `json:"message_json,omitempty" jsonschema:"description=JSON object to send (serialized automatically). Alternative to message."`
	Binary      string `json:"binary,omitempty" jsonschema:"description=Base64-encoded binary data to send."`

	// Receive action fields
	TimeoutSecs int  `json:"timeout_seconds,omitempty" jsonschema:"description=Timeout for receive in seconds (default: 30 max: 300)."`
	WaitCount   int  `json:"wait_count,omitempty" jsonschema:"description=Number of messages to wait for (default: 1)."`
	DrainBuffer bool `json:"drain_buffer,omitempty" jsonschema:"description=Return all buffered messages immediately without waiting."`

	// Connect options
	SubProtocols     []string `json:"subprotocols,omitempty" jsonschema:"description=WebSocket subprotocols to request."`
	InsecureSkipTLS  bool     `json:"insecure_skip_tls,omitempty" jsonschema:"description=Skip TLS verification (only for localhost)."`
	PingIntervalSecs int      `json:"ping_interval_seconds,omitempty" jsonschema:"description=Ping interval in seconds (default: 30)."`
}

// WebSocketToolResult is the result of a WebSocket operation.
type WebSocketToolResult struct {
	Success      bool               `json:"success"`
	Message      string             `json:"message"`
	ConnectionID string             `json:"connection_id,omitempty"` // For connect
	Messages     []WebSocketMessage `json:"messages,omitempty"`      // For receive
	Connections  []ConnectionInfo   `json:"connections,omitempty"`   // For list_connections
}

// NewWebSocketTool creates a WebSocket tool with OAuth and secrets support.
func NewWebSocketTool(
	userID string,
	allowedProviders []string,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
	manager *WebSocketManager,
) tool.CallableTool {
	allowed := make(map[string]bool)
	for _, p := range allowedProviders {
		allowed[p] = true
	}

	return function.NewFunctionTool(
		func(ctx context.Context, args WebSocketToolArgs) (*WebSocketToolResult, error) {
			return executeWebSocketTool(ctx, args, userID, allowed, secrets, tokenGetter, manager)
		},
		function.WithName("websocket"),
		function.WithDescription("Manage WebSocket connections for real-time communication. Actions: connect (establish connection), send (send message), receive (read messages), close (disconnect), list_connections (show active connections). Use auth_secret or oauth_provider for authentication."),
	)
}

func executeWebSocketTool(
	ctx context.Context,
	args WebSocketToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
	manager *WebSocketManager,
) (*WebSocketToolResult, error) {
	switch args.Action {
	case "connect":
		return connectAction(ctx, args, userID, allowedProviders, secrets, tokenGetter, manager)
	case "send":
		return sendAction(ctx, args, manager)
	case "receive":
		return receiveAction(ctx, args, manager)
	case "close":
		return closeAction(args, manager)
	case "list_connections":
		return listConnectionsAction(manager)
	default:
		return &WebSocketToolResult{
			Message: fmt.Sprintf("Unknown action %q. Use: connect, send, receive, close, list_connections", args.Action),
		}, nil
	}
}

func connectAction(
	ctx context.Context,
	args WebSocketToolArgs,
	userID string,
	allowedProviders map[string]bool,
	secrets map[string]string,
	tokenGetter OAuthTokenGetter,
	manager *WebSocketManager,
) (*WebSocketToolResult, error) {
	result := &WebSocketToolResult{}

	if args.URL == "" {
		result.Message = "Error: 'url' is required for connect action"
		return result, nil
	}

	// Validate mutually exclusive auth options
	if args.AuthSecret != "" && args.OAuthProvider != "" {
		result.Message = "Error: auth_secret and oauth_provider are mutually exclusive"
		return result, nil
	}

	// Build headers
	headers := http.Header{}
	for k, v := range args.Headers {
		headers.Set(k, v)
	}

	// Handle OAuth (same pattern as http_request.go)
	if args.OAuthProvider != "" {
		if len(allowedProviders) > 0 && !allowedProviders[args.OAuthProvider] {
			result.Message = fmt.Sprintf("OAuth provider %q not available to this skill", args.OAuthProvider)
			return result, nil
		}

		if tokenGetter == nil {
			result.Message = "OAuth not configured"
			return result, nil
		}

		token, err := tokenGetter(userID, args.OAuthProvider)
		if err != nil {
			if errors.Is(err, oauth.ErrTokenNotFound) {
				result.Message = fmt.Sprintf("OAuth authorization required for %s. Please authorize via dashboard.", args.OAuthProvider)
				return result, nil
			}
			if errors.Is(err, oauth.ErrTokenExpired) {
				result.Message = fmt.Sprintf("OAuth token for %s has expired. Please re-authorize via dashboard.", args.OAuthProvider)
				return result, nil
			}
			result.Message = fmt.Sprintf("OAuth token retrieval failed: %v", err)
			return result, nil
		}

		tokenType := token.TokenType
		if tokenType == "" {
			tokenType = "Bearer"
		}
		headers.Set("Authorization", tokenType+" "+token.AccessToken)
	}

	// Handle secret-based auth (same pattern as http_request.go)
	if args.AuthSecret != "" {
		secretValue, ok := secrets[args.AuthSecret]
		if !ok {
			result.Message = fmt.Sprintf("Secret %q not available to this skill", args.AuthSecret)
			return result, nil
		}

		authScheme := strings.ToLower(args.AuthScheme)
		if authScheme == "" {
			authScheme = "bearer"
		}

		switch authScheme {
		case "bearer":
			headers.Set("Authorization", "Bearer "+secretValue)
		case "api-key", "apikey":
			headers.Set("Authorization", secretValue)
		default:
			headers.Set("Authorization", secretValue)
		}
	}

	// Build connect options
	opts := wsConnectOpts{
		SubProtocols:    args.SubProtocols,
		InsecureSkipTLS: args.InsecureSkipTLS,
	}
	if args.PingIntervalSecs > 0 {
		opts.PingInterval = time.Duration(args.PingIntervalSecs) * time.Second
	}

	// Connect
	connID, err := manager.Connect(ctx, args.URL, headers, opts)
	if err != nil {
		result.Message = fmt.Sprintf("Connection failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Connected to %s", args.URL)
	result.ConnectionID = connID
	return result, nil
}

func sendAction(ctx context.Context, args WebSocketToolArgs, manager *WebSocketManager) (*WebSocketToolResult, error) {
	result := &WebSocketToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for send action"
		return result, nil
	}

	var messageType int
	var data []byte

	if args.Binary != "" {
		// Binary message
		var err error
		data, err = base64.StdEncoding.DecodeString(args.Binary)
		if err != nil {
			result.Message = fmt.Sprintf("Invalid base64 in 'binary': %v", err)
			return result, nil
		}
		messageType = websocket.BinaryMessage
	} else if args.MessageJSON != nil {
		// JSON message
		var err error
		data, err = json.Marshal(args.MessageJSON)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to serialize message_json: %v", err)
			return result, nil
		}
		messageType = websocket.TextMessage
	} else if args.Message != "" {
		// Text message
		data = []byte(args.Message)
		messageType = websocket.TextMessage
	} else {
		result.Message = "Error: 'message', 'message_json', or 'binary' is required for send action"
		return result, nil
	}

	if err := manager.Send(args.ConnectionID, messageType, data); err != nil {
		result.Message = fmt.Sprintf("Send failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Sent %d bytes to connection %s", len(data), args.ConnectionID)
	return result, nil
}

func receiveAction(ctx context.Context, args WebSocketToolArgs, manager *WebSocketManager) (*WebSocketToolResult, error) {
	result := &WebSocketToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for receive action"
		return result, nil
	}

	// Set timeout
	timeout := wsDefaultTimeout
	if args.TimeoutSecs > 0 {
		timeout = time.Duration(args.TimeoutSecs) * time.Second
		if timeout > wsMaxTimeout {
			timeout = wsMaxTimeout
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait count
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

func closeAction(args WebSocketToolArgs, manager *WebSocketManager) (*WebSocketToolResult, error) {
	result := &WebSocketToolResult{}

	if args.ConnectionID == "" {
		result.Message = "Error: 'connection_id' is required for close action"
		return result, nil
	}

	if err := manager.Close(args.ConnectionID); err != nil {
		result.Message = fmt.Sprintf("Close failed: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Connection %s closed", args.ConnectionID)
	return result, nil
}

func listConnectionsAction(manager *WebSocketManager) (*WebSocketToolResult, error) {
	connections := manager.ListConnections()
	return &WebSocketToolResult{
		Success:     true,
		Message:     fmt.Sprintf("Found %d active connection(s)", len(connections)),
		Connections: connections,
	}, nil
}
