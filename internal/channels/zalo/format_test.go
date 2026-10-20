package zalo

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain text", "hello world", "hello world"},

		// Bold & italic
		{"bold stars", "this is **bold** text", "this is bold text"},
		{"bold underscores", "this is __bold__ text", "this is bold text"},
		{"bold+italic stars", "***important***", "important"},
		{"strikethrough", "this is ~~deleted~~ text", "this is deleted text"},

		// Code
		{"inline code", "use `fmt.Println` here", "use fmt.Println here"},
		{"fenced code block", "before\n```go\nfmt.Println(\"hi\")\n```\nafter", "before\nfmt.Println(\"hi\")\n\nafter"},
		{"fenced code block no lang", "```\ncode here\n```", "code here"},

		// Links & images
		{"link", "click [here](https://example.com) now", "click here (https://example.com) now"},
		{"image", "see ![alt](https://img.png) below", "see  below"},

		// Headers
		{"h1", "# Title", "Title"},
		{"h3", "### Section", "Section"},
		{"h6", "###### Deep", "Deep"},

		// Horizontal rules
		{"hr dashes", "above\n---\nbelow", "above\n\nbelow"},
		{"hr stars", "above\n***\nbelow", "above\n\nbelow"},

		// Blockquotes
		{"blockquote", "> this is quoted\n> second line", "this is quoted\nsecond line"},
		{"nested blockquote", "> > deep", "> deep"},

		// Bullets
		{"dash bullet", "- item one\n- item two", "• item one\n• item two"},
		{"star bullet", "* item one\n* item two", "• item one\n• item two"},
		{"plus bullet", "+ item one", "• item one"},
		{"indented bullet", "list:\n  - nested item", "list:\n  • nested item"},

		// Excessive newlines
		{"excessive newlines", "a\n\n\n\nb", "a\n\nb"},

		// Mixed
		{"mixed markdown", "## Hello\n\nThis is **bold** and `code`.\n\n- item\n- [link](url)\n\n> quote", "Hello\n\nThis is bold and code.\n\n• item\n• link (url)\n\nquote"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdown(tt.in)
			if got != tt.want {
				t.Errorf("StripMarkdown(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatMarkdownNativeStyles(t *testing.T) {
	formatted := FormatMarkdown("# Tiêu đề 😀\n**Đậm** và *nghiêng*; ~~bỏ~~; [link](https://example.com)")
	if formatted.Text != "Tiêu đề 😀\nĐậm và nghiêng; bỏ; link (https://example.com)" {
		t.Fatalf("text = %q", formatted.Text)
	}

	want := map[string]int{
		TextStyleBig: 1, TextStyleBold: 3, TextStyleItalic: 1, TextStyleStrikeThrough: 1,
	}
	got := make(map[string]int)
	for _, style := range formatted.Styles {
		got[style.Style]++
		if style.Start < 0 || style.Len <= 0 || style.Start+style.Len > utf16Length(formatted.Text) {
			t.Fatalf("invalid style range: %+v for %q", style, formatted.Text)
		}
	}
	for style, count := range want {
		if got[style] != count {
			t.Fatalf("style %q count = %d, want %d; all=%+v", style, got[style], count, formatted.Styles)
		}
	}
}

func TestFormatMarkdownUsesUTF16Offsets(t *testing.T) {
	formatted := FormatMarkdown("A 😀 **đậm**")
	if formatted.Text != "A 😀 đậm" || len(formatted.Styles) != 1 {
		t.Fatalf("formatted = %+v", formatted)
	}
	style := formatted.Styles[0]
	if style.Start != 5 || style.Len != 3 || style.Style != TextStyleBold {
		t.Fatalf("style = %+v, want UTF-16 start=5 len=3", style)
	}
}

func TestChunkFormattedMessageBudgetsAndPreservesStyles(t *testing.T) {
	input := make([]string, 85)
	for index := range input {
		input[index] = "**mục " + strings.Repeat("x", index%5+1) + "**"
	}
	formatted := FormatMarkdown(strings.Join(input, "\n"))
	chunks := ChunkFormattedMessage(formatted, nil)
	if len(chunks) < 3 {
		t.Fatalf("chunks = %d, want at least 3", len(chunks))
	}
	styleCount := 0
	var rebuilt strings.Builder
	for _, chunk := range chunks {
		if utf16Length(chunk.Text) > MaxNativeTextLength {
			t.Fatalf("chunk UTF-16 length = %d", utf16Length(chunk.Text))
		}
		if len(chunk.Styles) > MaxNativeTextStyles {
			t.Fatalf("chunk styles = %d", len(chunk.Styles))
		}
		if size := formattedPayloadBytes(FormattedMessage{Text: chunk.Text, Styles: chunk.Styles}); size > MaxNativePayloadBytes {
			t.Fatalf("chunk payload = %d bytes", size)
		}
		styleCount += len(chunk.Styles)
		rebuilt.WriteString(chunk.Text)
	}
	if rebuilt.String() != formatted.Text {
		t.Fatal("chunking changed message text")
	}
	if styleCount != len(formatted.Styles) {
		t.Fatalf("styles after chunking = %d, want %d", styleCount, len(formatted.Styles))
	}
}

func TestChunkFormattedMessagePayloadBytesAndProtectedRange(t *testing.T) {
	formatted := FormatMarkdown(strings.Repeat("Tiếng Việt 😀 **đậm** ", 180))
	mentionStart := utf16Length(formatted.Text)
	formatted.Text += "@Hải Anh"
	mentionEnd := utf16Length(formatted.Text)
	chunks := ChunkFormattedMessage(formatted, []TextRange{{Start: mentionStart, End: mentionEnd}})
	for _, chunk := range chunks {
		if chunk.Start < mentionEnd && chunk.End > mentionStart && (mentionStart < chunk.Start || mentionEnd > chunk.End) {
			t.Fatalf("mention split across chunk [%d,%d): mention [%d,%d)", chunk.Start, chunk.End, mentionStart, mentionEnd)
		}
	}
	for _, chunk := range chunks {
		if size := formattedPayloadBytes(FormattedMessage{Text: chunk.Text, Styles: chunk.Styles}); size > MaxNativePayloadBytes {
			t.Fatalf("chunk payload = %d bytes", size)
		}
	}
}

func TestFormatMarkdownKeepsFencedCodePlain(t *testing.T) {
	formatted := FormatMarkdown("```go\n# not heading\n**not bold**\n```")
	if formatted.Text != "# not heading\n**not bold**" || len(formatted.Styles) != 0 {
		t.Fatalf("formatted = %+v", formatted)
	}
}

func TestChunkFormattedMessageDoesNotSplitEmojiSurrogatePair(t *testing.T) {
	text := strings.Repeat("a", 1999) + "😀" + strings.Repeat("b", 100)
	formatted := FormattedMessage{
		Text:   text,
		Styles: []TextStyle{{Start: 0, Len: utf16Length(text), Style: TextStyleBold}},
	}
	chunks := ChunkFormattedMessage(formatted, nil)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want at least 2", len(chunks))
	}
	var rebuilt strings.Builder
	for _, chunk := range chunks {
		rebuilt.WriteString(chunk.Text)
		if !utf8.ValidString(chunk.Text) {
			t.Fatalf("chunk contains invalid UTF-8: %q", chunk.Text)
		}
	}
	if rebuilt.String() != text {
		t.Fatal("emoji was lost or duplicated at chunk boundary")
	}
}
