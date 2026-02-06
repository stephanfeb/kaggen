// Package model defines the interface for AI model providers.
package model

import (
	"context"
	"strings"

	"github.com/yourusername/kaggen/pkg/protocol"
)

// Model is the interface that all model providers must implement.
type Model interface {
	// Generate sends messages to the model and returns a response.
	// The tools parameter defines available tools for the model to use.
	Generate(ctx context.Context, messages []protocol.Message, tools []protocol.ToolDef) (*protocol.Response, error)
}

// Family represents a model provider family.
type Family string

const (
	FamilyAnthropic Family = "anthropic"
	FamilyGemini    Family = "gemini"
	FamilyZAI       Family = "zai"
)

// Tier2Defaults maps model families to their best deep-thinking models.
var Tier2Defaults = map[Family]string{
	FamilyAnthropic: "claude-opus-4-5-20251101",
	FamilyGemini:    "gemini-2.5-pro-preview-06-05",
	FamilyZAI:       "glm-4.7",
}

// DetectFamily extracts the model family from a "provider/model" string.
// Returns FamilyAnthropic as the default if no provider prefix is found.
func DetectFamily(modelString string) Family {
	if strings.HasPrefix(modelString, "gemini/") {
		return FamilyGemini
	}
	if strings.HasPrefix(modelString, "zai/") {
		return FamilyZAI
	}
	return FamilyAnthropic // default
}

// Tier2ModelForFamily returns the full "provider/model" string for the Tier 2
// (deep thinking) model for a given family.
func Tier2ModelForFamily(family Family) string {
	if m, ok := Tier2Defaults[family]; ok {
		return string(family) + "/" + m
	}
	return "anthropic/" + Tier2Defaults[FamilyAnthropic]
}
