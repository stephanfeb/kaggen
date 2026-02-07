package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"

	pb "github.com/yourusername/kaggen/internal/p2p/proto"
)

// Protocol IDs for P2P API protocols.
const (
	SessionsProtocolID  protocol.ID = "/kaggen/sessions/1.0.0"
	TasksProtocolID     protocol.ID = "/kaggen/tasks/1.0.0"
	ApprovalsProtocolID protocol.ID = "/kaggen/approvals/1.0.0"
	SystemProtocolID    protocol.ID = "/kaggen/system/1.0.0"
	SecretsProtocolID   protocol.ID = "/kaggen/secrets/1.0.0"
	FilesProtocolID     protocol.ID = "/kaggen/files/1.0.0"
)

// MethodHandler is a function that handles a specific API method.
// It receives JSON-encoded params and returns a result or error.
type MethodHandler func(params json.RawMessage) (any, error)

// APIHandler provides base functionality for P2P API protocol handlers.
type APIHandler struct {
	protocolID protocol.ID
	methods    map[string]MethodHandler
	logger     *slog.Logger
}

// NewAPIHandler creates a new API handler for a given protocol.
func NewAPIHandler(protocolID protocol.ID, logger *slog.Logger) *APIHandler {
	return &APIHandler{
		protocolID: protocolID,
		methods:    make(map[string]MethodHandler),
		logger:     logger,
	}
}

// RegisterMethod registers a handler for a specific method name.
func (h *APIHandler) RegisterMethod(name string, handler MethodHandler) {
	h.methods[name] = handler
}

// HandleStream processes incoming API requests on a stream.
// It reads an APIRequest, dispatches to the appropriate method handler,
// and writes an APIResponse.
func (h *APIHandler) HandleStream(stream network.Stream) {
	defer stream.Close()

	peerID := stream.Conn().RemotePeer()
	h.logger.Debug("API stream opened",
		"protocol", h.protocolID,
		"peer", peerID)

	// Read the request.
	var req pb.APIRequest
	if err := ReadMessage(stream, &req); err != nil {
		h.logger.Warn("failed to read API request",
			"protocol", h.protocolID,
			"peer", peerID,
			"error", err)
		return
	}

	h.logger.Info("API request received",
		"protocol", h.protocolID,
		"peer", peerID,
		"method", req.Method,
		"request_id", req.Id)

	// Dispatch to method handler.
	resp := h.dispatch(&req)

	// Write the response.
	if err := WriteMessage(stream, resp); err != nil {
		h.logger.Warn("failed to write API response",
			"protocol", h.protocolID,
			"peer", peerID,
			"error", err)
		return
	}

	h.logger.Debug("API response sent",
		"protocol", h.protocolID,
		"peer", peerID,
		"success", resp.Success)
}

// dispatch routes a request to the appropriate method handler.
func (h *APIHandler) dispatch(req *pb.APIRequest) *pb.APIResponse {
	resp := &pb.APIResponse{
		Id: req.Id,
	}

	// Generate response ID if request didn't have one.
	if resp.Id == "" {
		resp.Id = uuid.New().String()
	}

	// Find the method handler.
	handler, ok := h.methods[req.Method]
	if !ok {
		resp.Success = false
		resp.Error = fmt.Sprintf("unknown method: %s", req.Method)
		return resp
	}

	// Call the handler.
	result, err := handler(req.Params)
	if err != nil {
		resp.Success = false
		resp.Error = err.Error()
		return resp
	}

	// Marshal the result to JSON.
	data, err := json.Marshal(result)
	if err != nil {
		resp.Success = false
		resp.Error = fmt.Sprintf("failed to marshal response: %v", err)
		return resp
	}

	resp.Success = true
	resp.Data = data
	return resp
}

// StreamHandler provides functionality for streaming API responses.
type StreamHandler struct {
	*APIHandler
	stream network.Stream
}

// NewStreamHandler creates a handler for streaming responses.
func NewStreamHandler(h *APIHandler, stream network.Stream) *StreamHandler {
	return &StreamHandler{
		APIHandler: h,
		stream:     stream,
	}
}

// SendEvent sends a streaming event to the client.
func (h *StreamHandler) SendEvent(eventType string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}

	evt := &pb.StreamEvent{
		Id:   uuid.New().String(),
		Type: eventType,
		Data: jsonData,
		Done: false,
	}

	return WriteMessage(h.stream, evt)
}

// SendDone sends the final event marker.
func (h *StreamHandler) SendDone() error {
	evt := &pb.StreamEvent{
		Id:   uuid.New().String(),
		Type: "done",
		Done: true,
	}
	return WriteMessage(h.stream, evt)
}

// unmarshalParams is a helper to unmarshal JSON params into a struct.
func unmarshalParams[T any](params json.RawMessage) (T, error) {
	var result T
	if len(params) == 0 {
		return result, nil
	}
	err := json.Unmarshal(params, &result)
	return result, err
}
