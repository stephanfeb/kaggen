package channel

import (
	"regexp"
	"strings"
)

var (
	// Patterns for markdown → WhatsApp conversion.
	// WhatsApp uses: *bold*, _italic_, ~strikethrough~, ```code```
	waReBoldAsterisks   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	waReBoldUnderscores = regexp.MustCompile(`__(.+?)__`)
	waReCodeBlock       = regexp.MustCompile("(?s)```(?:\\w*)\n?(.*?)```")
	waReInlineCode      = regexp.MustCompile("`([^`]+)`")
)

// formatForWhatsApp converts standard Markdown to WhatsApp formatting.
// WhatsApp supports: *bold*, _italic_, ~strikethrough~, ```monospace```
func formatForWhatsApp(text string) string {
	if text == "" {
		return text
	}

	// Extract code blocks first to protect them from further processing.
	var codeBlocks []string
	result := waReCodeBlock.ReplaceAllStringFunc(text, func(match string) string {
		inner := waReCodeBlock.FindStringSubmatch(match)[1]
		placeholder := "\x00CODE" + string(rune(len(codeBlocks))) + "\x00"
		codeBlocks = append(codeBlocks, "```"+inner+"```")
		return placeholder
	})

	// Extract inline code.
	var inlineBlocks []string
	result = waReInlineCode.ReplaceAllStringFunc(result, func(match string) string {
		inner := waReInlineCode.FindStringSubmatch(match)[1]
		placeholder := "\x00INLINE" + string(rune(len(inlineBlocks))) + "\x00"
		inlineBlocks = append(inlineBlocks, "`"+inner+"`")
		return placeholder
	})

	// Convert markdown bold to WhatsApp bold.
	// **text** or __text__ → *text*
	result = waReBoldAsterisks.ReplaceAllString(result, "*$1*")
	result = waReBoldUnderscores.ReplaceAllString(result, "*$1*")

	// Note: Single underscores for italic (_text_) are already valid WhatsApp syntax.
	// Single asterisks for italic need to stay as-is since we now use * for bold.

	// Restore inline code.
	for i, block := range inlineBlocks {
		placeholder := "\x00INLINE" + string(rune(i)) + "\x00"
		result = strings.Replace(result, placeholder, block, 1)
	}

	// Restore code blocks.
	for i, block := range codeBlocks {
		placeholder := "\x00CODE" + string(rune(i)) + "\x00"
		result = strings.Replace(result, placeholder, block, 1)
	}

	return result
}
