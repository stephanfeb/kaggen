// Package model defines the interface for AI model providers.
package model

import (
	"context"

	"github.com/yourusername/kaggen/pkg/protocol"
)

// Model is the interface that all model providers must implement.
type Model interface {
	// Generate sends messages to the model and returns a response.
	// The tools parameter defines available tools for the model to use.
	Generate(ctx context.Context, messages []protocol.Message, tools []protocol.ToolDef) (*protocol.Response, error)
}
