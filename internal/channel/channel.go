// Package channel defines interfaces for multi-channel communication.
package channel

import (
	"context"
)

// Message represents an incoming message from any channel.
type Message struct {
	// ID is a unique identifier for this message.
	ID string `json:"id"`

	// SessionID identifies the conversation session.
	SessionID string `json:"session_id"`

	// UserID identifies the user sending the message.
	UserID string `json:"user_id"`

	// Content is the text content of the message.
	Content string `json:"content"`

	// Channel identifies the source channel (e.g., "websocket", "telegram").
	Channel string `json:"channel"`

	// Metadata contains channel-specific additional data.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Response represents an outgoing response to a channel.
type Response struct {
	// ID is a unique identifier for this response.
	ID string `json:"id"`

	// MessageID is the ID of the message being responded to.
	MessageID string `json:"message_id"`

	// SessionID identifies the conversation session.
	SessionID string `json:"session_id"`

	// Content is the text content of the response.
	Content string `json:"content"`

	// Type indicates the response type (text, thinking, tool_call, etc.).
	Type string `json:"type"`

	// Done indicates if this is the final response.
	Done bool `json:"done"`

	// Metadata contains additional response data.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Channel defines the interface for a communication channel.
type Channel interface {
	// Name returns the channel identifier.
	Name() string

	// Start begins listening for messages on this channel.
	// The provided context controls the channel's lifecycle.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel.
	Stop(ctx context.Context) error

	// Messages returns a channel for receiving incoming messages.
	Messages() <-chan *Message

	// Send sends a response back through the channel.
	Send(ctx context.Context, resp *Response) error
}

// Handler processes messages from channels.
type Handler interface {
	// HandleMessage processes an incoming message and returns responses.
	// Responses are sent through the provided callback as they become available.
	HandleMessage(ctx context.Context, msg *Message, respond func(*Response) error) error
}

// Router routes messages from multiple channels to handlers.
type Router struct {
	channels []Channel
	handler  Handler
}

// NewRouter creates a new message router.
func NewRouter(handler Handler) *Router {
	return &Router{
		handler: handler,
	}
}

// AddChannel adds a channel to the router.
func (r *Router) AddChannel(ch Channel) {
	r.channels = append(r.channels, ch)
}

// Start begins routing messages from all channels.
func (r *Router) Start(ctx context.Context) error {
	for _, ch := range r.channels {
		ch := ch // Avoid closure capture
		go r.routeChannel(ctx, ch)
	}
	return nil
}

// routeChannel handles messages from a single channel.
func (r *Router) routeChannel(ctx context.Context, ch Channel) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch.Messages():
			if !ok {
				return
			}
			// Handle message and send responses back through the channel
			r.handler.HandleMessage(ctx, msg, func(resp *Response) error {
				return ch.Send(ctx, resp)
			})
		}
	}
}

// Stop gracefully stops all channels.
func (r *Router) Stop(ctx context.Context) error {
	for _, ch := range r.channels {
		if err := ch.Stop(ctx); err != nil {
			return err
		}
	}
	return nil
}
