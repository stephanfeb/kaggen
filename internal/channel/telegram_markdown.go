package channel

import (
	"regexp"
	"strings"
)

var (
	// Patterns for markdown → HTML conversion.
	reBoldAsterisks   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBoldUnderscores = regexp.MustCompile(`__(.+?)__`)
	reItalicAsterisk  = regexp.MustCompile(`\*(.+?)\*`)
	reItalicUnderscore = regexp.MustCompile(`_(.+?)_`)
	reInlineCode      = regexp.MustCompile("`([^`]+)`")
	reCodeBlock       = regexp.MustCompile("(?s)```(?:\\w*)\n?(.*?)```")
)

// formatForTelegram converts standard Markdown to Telegram HTML.
// Falls back to the original text if conversion would be lossy.
func formatForTelegram(text string) string {
	if text == "" {
		return text
	}

	// Extract code blocks first to protect them from further processing.
	var codeBlocks []string
	result := reCodeBlock.ReplaceAllStringFunc(text, func(match string) string {
		inner := reCodeBlock.FindStringSubmatch(match)[1]
		placeholder := "\x00CODE" + string(rune(len(codeBlocks))) + "\x00"
		codeBlocks = append(codeBlocks, "<pre>"+escapeHTML(inner)+"</pre>")
		return placeholder
	})

	// Extract inline code.
	var inlineBlocks []string
	result = reInlineCode.ReplaceAllStringFunc(result, func(match string) string {
		inner := reInlineCode.FindStringSubmatch(match)[1]
		placeholder := "\x00INLINE" + string(rune(len(inlineBlocks))) + "\x00"
		inlineBlocks = append(inlineBlocks, "<code>"+escapeHTML(inner)+"</code>")
		return placeholder
	})

	// Escape HTML in remaining text.
	result = escapeHTML(result)

	// Convert markdown formatting to HTML.
	result = reBoldAsterisks.ReplaceAllString(result, "<b>$1</b>")
	result = reBoldUnderscores.ReplaceAllString(result, "<b>$1</b>")
	result = reItalicAsterisk.ReplaceAllString(result, "<i>$1</i>")
	result = reItalicUnderscore.ReplaceAllString(result, "<i>$1</i>")

	// Restore code blocks and inline code.
	for i, block := range inlineBlocks {
		placeholder := "\x00INLINE" + string(rune(i)) + "\x00"
		result = strings.Replace(result, escapeHTML(placeholder), block, 1)
	}
	for i, block := range codeBlocks {
		placeholder := "\x00CODE" + string(rune(i)) + "\x00"
		result = strings.Replace(result, escapeHTML(placeholder), block, 1)
	}

	return result
}

// escapeHTML escapes characters that are special in HTML.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
