package memory

import (
	"math"
	"testing"
)

func TestParseStructuredContent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		content string
		meta    MemoryMetadata
	}{
		{
			name:    "full prefix",
			input:   "[type:opinion|conf:0.7|when:2025-06~2025-08|ent:Go,Rust] User prefers Go over Rust",
			content: "User prefers Go over Rust",
			meta: MemoryMetadata{
				Type: MemoryTypeOpinion, Confidence: 0.7,
				OccurredStart: "2025-06", OccurredEnd: "2025-08",
				Entities: []string{"Go", "Rust"},
			},
		},
		{
			name:    "type only",
			input:   "[type:experience] User visited Berlin",
			content: "User visited Berlin",
			meta:    MemoryMetadata{Type: MemoryTypeExperience, Confidence: 1.0},
		},
		{
			name:    "no prefix",
			input:   "Plain memory text",
			content: "Plain memory text",
			meta:    DefaultMetadata(),
		},
		{
			name:    "when point in time",
			input:   "[when:2025-01-15] Something happened",
			content: "Something happened",
			meta:    MemoryMetadata{Type: MemoryTypeFact, Confidence: 1.0, OccurredStart: "2025-01-15"},
		},
		{
			name:    "entities only",
			input:   "[ent:Alice,Bob] Alice met Bob at the park",
			content: "Alice met Bob at the park",
			meta:    MemoryMetadata{Type: MemoryTypeFact, Confidence: 1.0, Entities: []string{"Alice", "Bob"}},
		},
		{
			name:    "empty string",
			input:   "",
			content: "",
			meta:    DefaultMetadata(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, meta := ParseStructuredContent(tt.input)
			if content != tt.content {
				t.Errorf("content: got %q, want %q", content, tt.content)
			}
			if meta.Type != tt.meta.Type {
				t.Errorf("type: got %q, want %q", meta.Type, tt.meta.Type)
			}
			if math.Abs(meta.Confidence-tt.meta.Confidence) > 0.01 {
				t.Errorf("confidence: got %f, want %f", meta.Confidence, tt.meta.Confidence)
			}
			if meta.OccurredStart != tt.meta.OccurredStart {
				t.Errorf("occurred_start: got %q, want %q", meta.OccurredStart, tt.meta.OccurredStart)
			}
			if meta.OccurredEnd != tt.meta.OccurredEnd {
				t.Errorf("occurred_end: got %q, want %q", meta.OccurredEnd, tt.meta.OccurredEnd)
			}
			if len(meta.Entities) != len(tt.meta.Entities) {
				t.Errorf("entities: got %v, want %v", meta.Entities, tt.meta.Entities)
			}
		})
	}
}

func TestFormatStructuredContent(t *testing.T) {
	meta := MemoryMetadata{
		Type: MemoryTypeOpinion, Confidence: 0.8,
		OccurredStart: "2025-01", Entities: []string{"Go"},
	}
	result := FormatStructuredContent("User likes Go", meta)
	expected := "[type:opinion|conf:0.8|when:2025-01|ent:Go] User likes Go"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}

	// Default fact with no extra metadata → no prefix
	plain := FormatStructuredContent("Just a fact", DefaultMetadata())
	if plain != "Just a fact" {
		t.Errorf("expected no prefix for default metadata, got %q", plain)
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	original := MemoryMetadata{
		Type: MemoryTypeExperience, Confidence: 0.9,
		OccurredStart: "2024-03", OccurredEnd: "2024-06",
		Entities: []string{"Berlin", "Germany"},
	}
	formatted := FormatStructuredContent("Lived in Berlin", original)
	content, parsed := ParseStructuredContent(formatted)

	if content != "Lived in Berlin" {
		t.Errorf("content: got %q", content)
	}
	if parsed.Type != original.Type {
		t.Errorf("type mismatch: %v vs %v", parsed.Type, original.Type)
	}
	if math.Abs(parsed.Confidence-original.Confidence) > 0.01 {
		t.Errorf("confidence mismatch: %f vs %f", parsed.Confidence, original.Confidence)
	}
	if parsed.OccurredStart != original.OccurredStart || parsed.OccurredEnd != original.OccurredEnd {
		t.Errorf("temporal mismatch")
	}
	if len(parsed.Entities) != len(original.Entities) {
		t.Errorf("entities mismatch")
	}
}

func TestParseMetaTopics(t *testing.T) {
	topics := []string{"_type:opinion", "_conf:0.6", "programming", "Go"}
	semantic, meta := ParseMetaTopics(topics)

	if len(semantic) != 2 || semantic[0] != "programming" || semantic[1] != "Go" {
		t.Errorf("semantic topics: got %v", semantic)
	}
	if meta.Type != MemoryTypeOpinion {
		t.Errorf("type: got %v", meta.Type)
	}
	if math.Abs(meta.Confidence-0.6) > 0.01 {
		t.Errorf("confidence: got %f", meta.Confidence)
	}
}

func TestEvolveConfidence(t *testing.T) {
	// Reinforce: old=0.7, new=0.9 → moves toward 0.9
	result := EvolveConfidence(0.7, 0.9)
	if result <= 0.7 || result >= 0.9 {
		t.Errorf("expected between 0.7 and 0.9, got %f", result)
	}

	// Weaken: old=0.8, new=0.3 → moves toward 0.3
	result2 := EvolveConfidence(0.8, 0.3)
	if result2 >= 0.8 || result2 <= 0.3 {
		t.Errorf("expected between 0.3 and 0.8, got %f", result2)
	}

	// Clamping
	result3 := EvolveConfidence(1.0, 1.0)
	if result3 > 1.0 {
		t.Errorf("should clamp to 1.0, got %f", result3)
	}
}
