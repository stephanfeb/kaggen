package memory

import (
	"strings"
	"testing"
)

func TestChunkMarkdown_Basic(t *testing.T) {
	content := `# Heading 1

This is a paragraph with some text content.

# Heading 2

Another paragraph here with different content.

And yet another paragraph.`

	chunks := chunkMarkdown(content, 400, 80)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// With a large chunk size, everything should be in one chunk
	combined := chunks[0].Content
	if !strings.Contains(combined, "Heading 1") {
		t.Error("missing Heading 1")
	}
	if !strings.Contains(combined, "Heading 2") {
		t.Error("missing Heading 2")
	}
}

func TestChunkMarkdown_SmallChunkSize(t *testing.T) {
	content := `# First Section

Word one two three four five six seven eight nine ten.

# Second Section

Word eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty.

# Third Section

Word twentyone twentytwo twentythree twentyfour twentyfive.`

	chunks := chunkMarkdown(content, 10, 2)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Verify all content is represented
	var allContent string
	for _, c := range chunks {
		allContent += c.Content + " "
	}
	if !strings.Contains(allContent, "First Section") {
		t.Error("missing First Section")
	}
	if !strings.Contains(allContent, "Third Section") {
		t.Error("missing Third Section")
	}
}

func TestChunkMarkdown_Empty(t *testing.T) {
	chunks := chunkMarkdown("", 400, 80)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty content, got %d", len(chunks))
	}
}

func TestWordCount(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  hello  world  ", 2},
		{"one\ntwo\tthree", 3},
	}

	for _, tt := range tests {
		got := wordCount(tt.input)
		if got != tt.want {
			t.Errorf("wordCount(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
