package memory

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// MemoryType classifies a memory entry.
type MemoryType string

const (
	MemoryTypeFact        MemoryType = "fact"
	MemoryTypeExperience  MemoryType = "experience"
	MemoryTypeOpinion     MemoryType = "opinion"
	MemoryTypeObservation MemoryType = "observation"
)

// MemoryMetadata holds structured metadata extracted from a memory's content prefix or topics.
type MemoryMetadata struct {
	Type          MemoryType
	Confidence    float64
	OccurredStart string   // ISO8601 or partial (e.g. "2025-06")
	OccurredEnd   string   // ISO8601 or partial; empty = point-in-time / ongoing
	Entities      []string // canonical entity names
}

// DefaultMetadata returns metadata with sensible defaults.
func DefaultMetadata() MemoryMetadata {
	return MemoryMetadata{
		Type:       MemoryTypeFact,
		Confidence: 1.0,
	}
}

// prefixRe matches the structured prefix: [type:...|conf:...|when:...|ent:...]
var prefixRe = regexp.MustCompile(`^\[([^\]]+)\]\s*`)

// ParseStructuredContent extracts metadata from a structured content prefix.
// Input:  "[type:opinion|conf:0.7|when:2025-06~2025-08|ent:Go,Rust] User prefers Go"
// Output: "User prefers Go", MemoryMetadata{Type:"opinion", Confidence:0.7, ...}
func ParseStructuredContent(raw string) (string, MemoryMetadata) {
	meta := DefaultMetadata()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw, meta
	}

	loc := prefixRe.FindStringIndex(raw)
	if loc == nil {
		return raw, meta
	}

	// Extract the bracket content (without [ and ])
	bracketContent := raw[1 : loc[1]-2] // skip leading [ and trailing "] "
	cleanContent := strings.TrimSpace(raw[loc[1]:])

	// Parse pipe-delimited key:value pairs
	parts := strings.Split(bracketContent, "|")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])

		switch key {
		case "type":
			switch MemoryType(val) {
			case MemoryTypeFact, MemoryTypeExperience, MemoryTypeOpinion, MemoryTypeObservation:
				meta.Type = MemoryType(val)
			}
		case "conf":
			var c float64
			if _, err := fmt.Sscanf(val, "%f", &c); err == nil {
				meta.Confidence = clampConfidence(c)
			}
		case "when":
			if idx := strings.Index(val, "~"); idx >= 0 {
				meta.OccurredStart = strings.TrimSpace(val[:idx])
				meta.OccurredEnd = strings.TrimSpace(val[idx+1:])
			} else {
				meta.OccurredStart = val
			}
		case "ent":
			for _, e := range strings.Split(val, ",") {
				e = strings.TrimSpace(e)
				if e != "" {
					meta.Entities = append(meta.Entities, e)
				}
			}
		}
	}

	return cleanContent, meta
}

// FormatStructuredContent serializes metadata into the structured prefix convention.
func FormatStructuredContent(content string, meta MemoryMetadata) string {
	var parts []string

	if meta.Type != "" && meta.Type != MemoryTypeFact {
		parts = append(parts, "type:"+string(meta.Type))
	}
	if meta.Confidence < 1.0 {
		parts = append(parts, fmt.Sprintf("conf:%.1f", meta.Confidence))
	}
	if meta.OccurredStart != "" {
		when := meta.OccurredStart
		if meta.OccurredEnd != "" {
			when += "~" + meta.OccurredEnd
		}
		parts = append(parts, "when:"+when)
	}
	if len(meta.Entities) > 0 {
		parts = append(parts, "ent:"+strings.Join(meta.Entities, ","))
	}

	if len(parts) == 0 {
		return content
	}
	return "[" + strings.Join(parts, "|") + "] " + content
}

// ParseMetaTopics separates _-prefixed metadata topics from semantic topics.
// Input:  ["_type:opinion", "_conf:0.7", "programming", "Go"]
// Output: ["programming", "Go"], MemoryMetadata{Type:"opinion", Confidence:0.7}
func ParseMetaTopics(topics []string) ([]string, MemoryMetadata) {
	meta := DefaultMetadata()
	var semantic []string

	for _, t := range topics {
		if !strings.HasPrefix(t, "_") {
			semantic = append(semantic, t)
			continue
		}
		kv := strings.SplitN(t[1:], ":", 2) // strip leading _
		if len(kv) != 2 {
			semantic = append(semantic, t) // malformed, keep as-is
			continue
		}
		key, val := kv[0], kv[1]
		switch key {
		case "type":
			switch MemoryType(val) {
			case MemoryTypeFact, MemoryTypeExperience, MemoryTypeOpinion, MemoryTypeObservation:
				meta.Type = MemoryType(val)
			}
		case "conf":
			var c float64
			if _, err := fmt.Sscanf(val, "%f", &c); err == nil {
				meta.Confidence = clampConfidence(c)
			}
		default:
			semantic = append(semantic, t) // unknown meta key, keep
		}
	}

	return semantic, meta
}

// MergeMetadata combines metadata from content prefix and topics.
// Content prefix takes precedence for fields it specifies.
func MergeMetadata(fromContent, fromTopics MemoryMetadata) MemoryMetadata {
	m := fromContent

	// If content didn't specify type but topics did, use topics
	if m.Type == MemoryTypeFact && fromTopics.Type != MemoryTypeFact {
		m.Type = fromTopics.Type
	}
	// If content didn't specify confidence but topics did
	if m.Confidence == 1.0 && fromTopics.Confidence < 1.0 {
		m.Confidence = fromTopics.Confidence
	}

	return m
}

// EvolveConfidence applies exponential moving average smoothing for opinion updates.
func EvolveConfidence(oldConf, newConf float64) float64 {
	const alpha = 0.3
	result := alpha*newConf + (1-alpha)*oldConf
	return clampConfidence(result)
}

func clampConfidence(c float64) float64 {
	return math.Max(0.0, math.Min(1.0, c))
}
