package zalo

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	MaxNativeTextLength   = 2000
	MaxNativeTextStyles   = 40
	MaxNativePayloadBytes = 3000
	mentionReserveBytes   = 60
)

const (
	TextStyleBold          = "b"
	TextStyleItalic        = "i"
	TextStyleStrikeThrough = "s"
	TextStyleBig           = "f_18"
	TextStyleRed           = "c_db342e"
	TextStyleGreen         = "c_15a85f"
	TextStyleOrange        = "c_f27806"
	TextStyleYellow        = "c_f7b503"
)

// TextStyle is a native Zalo style range. Start and Len use UTF-16 code units,
// matching the JavaScript offsets expected by Zalo's web API.
type TextStyle struct {
	Start int    `json:"start"`
	Len   int    `json:"len"`
	Style string `json:"st"`
}

// FormattedMessage is plain display text plus native Zalo style ranges.
type FormattedMessage struct {
	Text   string
	Styles []TextStyle
}

// TextRange protects a UTF-16 range, such as a native mention, from being
// split across chunks.
type TextRange struct {
	Start int
	End   int
}

// FormattedChunk contains one independently sendable part. Start and End are
// UTF-16 offsets into the complete formatted message.
type FormattedChunk struct {
	Text   string
	Styles []TextStyle
	Start  int
	End    int
}

var (
	headingRE     = regexp.MustCompile(`^\s*#{1,6}\s+(.+)$`)
	blockquoteRE  = regexp.MustCompile(`^\s*>\s?(.*)$`)
	bulletRE      = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	numberedRE    = regexp.MustCompile(`^(\s*)(\d+[.)]\s+)`)
	horizontalRE  = regexp.MustCompile(`^\s*([-*_]){3,}\s*$`)
	colorTagNames = map[string]string{
		"red": TextStyleRed, "do": TextStyleRed,
		"green": TextStyleGreen, "xanh": TextStyleGreen,
		"orange": TextStyleOrange, "cam": TextStyleOrange,
		"yellow": TextStyleYellow, "vang": TextStyleYellow,
	}
)

// FormatMarkdown converts the useful Markdown subset emitted by agents into
// Zalo's plain message text and native style ranges.
func FormatMarkdown(input string) FormattedMessage {
	raw := strings.TrimSpace(strings.ReplaceAll(input, "\r\n", "\n"))
	if raw == "" {
		return FormattedMessage{}
	}

	lines := strings.Split(raw, "\n")
	outLines := make([]string, 0, len(lines))
	styles := make([]TextStyle, 0)
	offset := 0
	inCodeBlock := false
	previousBlank := false

	appendLine := func(line string, lineStyles []TextStyle) {
		if len(outLines) > 0 {
			offset++ // newline is one UTF-16 code unit
		}
		outLines = append(outLines, line)
		styles = append(styles, lineStyles...)
		offset += utf16Length(line)
	}

	for _, sourceLine := range lines {
		line := sourceLine
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			appendLine(line, nil)
			previousBlank = false
			continue
		}
		if horizontalRE.MatchString(line) {
			continue
		}

		blank := strings.TrimSpace(line) == ""
		if blank && previousBlank {
			continue
		}
		previousBlank = blank

		base := offset
		if len(outLines) > 0 {
			base++
		}
		var wholeLineStyles []string
		if match := headingRE.FindStringSubmatch(line); match != nil {
			line = match[1]
			wholeLineStyles = []string{TextStyleBold, TextStyleBig}
		} else if match := blockquoteRE.FindStringSubmatch(line); match != nil {
			line = match[1]
			wholeLineStyles = []string{TextStyleItalic}
		}

		line = bulletRE.ReplaceAllStringFunc(line, func(match string) string {
			parts := bulletRE.FindStringSubmatch(match)
			if utf16Length(parts[1]) >= 2 {
				return parts[1] + "• "
			}
			return parts[1] + "- "
		})

		numberStart, numberLen := 0, 0
		if len(wholeLineStyles) == 0 {
			if match := numberedRE.FindStringSubmatch(line); match != nil {
				numberStart = utf16Length(match[1])
				numberLen = utf16Length(match[2])
			}
		}

		text, lineStyles := renderInlineMarkdown(line, base)
		textLen := utf16Length(text)
		for _, style := range wholeLineStyles {
			if textLen > 0 {
				lineStyles = append(lineStyles, TextStyle{Start: base, Len: textLen, Style: style})
			}
		}
		if numberLen > 0 {
			lineStyles = append(lineStyles,
				TextStyle{Start: base + numberStart, Len: numberLen, Style: TextStyleBold},
				TextStyle{Start: base + numberStart, Len: numberLen, Style: TextStyleBig},
			)
		}
		appendLine(text, lineStyles)
	}

	return FormattedMessage{Text: strings.Join(outLines, "\n"), Styles: styles}
}

func renderInlineMarkdown(line string, base int) (string, []TextStyle) {
	var output strings.Builder
	outputUnits := 0
	styles := make([]TextStyle, 0)

	addStyle := func(start, length int, style string) {
		if length > 0 {
			styles = append(styles, TextStyle{Start: base + start, Len: length, Style: style})
		}
	}

	for index := 0; index < len(line); {
		rest := line[index:]
		current := outputUnits

		if name, inner, consumed, ok := matchColorTag(rest); ok {
			text, nested := renderInlineMarkdown(inner, base+current)
			output.WriteString(text)
			outputUnits += utf16Length(text)
			styles = append(styles, nested...)
			addStyle(current, utf16Length(text), colorTagNames[name])
			index += consumed
			continue
		}

		if strings.HasPrefix(rest, "![") {
			if consumed := markdownLinkLength(rest[1:]); consumed > 0 {
				index += consumed + 1
				continue
			}
		}
		if strings.HasPrefix(rest, "[") {
			if label, target, consumed, ok := parseMarkdownLink(rest); ok {
				output.WriteString(label)
				outputUnits += utf16Length(label)
				addStyle(current, utf16Length(label), TextStyleBold)
				output.WriteString(" (" + target + ")")
				outputUnits += utf16Length(" (" + target + ")")
				index += consumed
				continue
			}
		}

		if inner, consumed, ok := delimited(rest, "***"); ok {
			text, nested := renderInlineMarkdown(inner, base+current)
			output.WriteString(text)
			outputUnits += utf16Length(text)
			styles = append(styles, nested...)
			length := utf16Length(text)
			addStyle(current, length, TextStyleBold)
			addStyle(current, length, TextStyleItalic)
			index += consumed
			continue
		}
		if inner, consumed, ok := delimited(rest, "___"); ok {
			text, nested := renderInlineMarkdown(inner, base+current)
			output.WriteString(text)
			outputUnits += utf16Length(text)
			styles = append(styles, nested...)
			length := utf16Length(text)
			addStyle(current, length, TextStyleBold)
			addStyle(current, length, TextStyleItalic)
			index += consumed
			continue
		}
		if inner, consumed, ok := delimited(rest, "~~"); ok {
			text, nested := renderInlineMarkdown(inner, base+current)
			output.WriteString(text)
			outputUnits += utf16Length(text)
			styles = append(styles, nested...)
			addStyle(current, utf16Length(text), TextStyleStrikeThrough)
			index += consumed
			continue
		}
		if inner, consumed, ok := delimited(rest, "**"); ok {
			text, nested := renderInlineMarkdown(inner, base+current)
			output.WriteString(text)
			outputUnits += utf16Length(text)
			styles = append(styles, nested...)
			addStyle(current, utf16Length(text), TextStyleBold)
			index += consumed
			continue
		}
		if inner, consumed, ok := delimited(rest, "__"); ok {
			text, nested := renderInlineMarkdown(inner, base+current)
			output.WriteString(text)
			outputUnits += utf16Length(text)
			styles = append(styles, nested...)
			addStyle(current, utf16Length(text), TextStyleBold)
			index += consumed
			continue
		}
		if strings.HasPrefix(rest, "`") {
			if end := strings.Index(rest[1:], "`"); end >= 0 {
				inner := rest[1 : end+1]
				output.WriteString(inner)
				outputUnits += utf16Length(inner)
				addStyle(current, utf16Length(inner), TextStyleBold)
				index += end + 2
				continue
			}
		}

		if (strings.HasPrefix(rest, "*") || strings.HasPrefix(rest, "_")) && inlineBoundary(line, index) {
			delimiter := rest[:1]
			if inner, consumed, ok := delimited(rest, delimiter); ok && inner != "" && !strings.ContainsAny(inner[:1], " \t*_\n") {
				text, nested := renderInlineMarkdown(inner, base+current)
				output.WriteString(text)
				outputUnits += utf16Length(text)
				styles = append(styles, nested...)
				addStyle(current, utf16Length(text), TextStyleItalic)
				index += consumed
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(rest)
		output.WriteRune(r)
		if r > 0xffff {
			outputUnits += 2
		} else {
			outputUnits++
		}
		index += size
	}

	return output.String(), styles
}

func delimited(value, delimiter string) (string, int, bool) {
	if !strings.HasPrefix(value, delimiter) {
		return "", 0, false
	}
	end := strings.Index(value[len(delimiter):], delimiter)
	if end < 0 {
		return "", 0, false
	}
	innerStart := len(delimiter)
	innerEnd := innerStart + end
	if innerEnd == innerStart {
		return "", 0, false
	}
	return value[innerStart:innerEnd], innerEnd + len(delimiter), true
}

func matchColorTag(value string) (string, string, int, bool) {
	lower := strings.ToLower(value)
	for name := range colorTagNames {
		open := "[" + name + "]"
		if !strings.HasPrefix(lower, open) {
			continue
		}
		close := "[/" + name + "]"
		closeIndex := strings.Index(lower[len(open):], close)
		if closeIndex < 0 {
			continue
		}
		innerEnd := len(open) + closeIndex
		return name, value[len(open):innerEnd], innerEnd + len(close), true
	}
	return "", "", 0, false
}

func parseMarkdownLink(value string) (string, string, int, bool) {
	if !strings.HasPrefix(value, "[") {
		return "", "", 0, false
	}
	labelEnd := strings.Index(value, "](")
	if labelEnd <= 1 {
		return "", "", 0, false
	}
	targetEnd := strings.Index(value[labelEnd+2:], ")")
	if targetEnd < 1 {
		return "", "", 0, false
	}
	targetEnd += labelEnd + 2
	target := value[labelEnd+2 : targetEnd]
	if strings.ContainsAny(target, " \t\n") {
		return "", "", 0, false
	}
	return value[1:labelEnd], target, targetEnd + 1, true
}

func markdownLinkLength(value string) int {
	_, _, consumed, ok := parseMarkdownLink(value)
	if !ok {
		return 0
	}
	return consumed
}

func inlineBoundary(line string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(line[:index])
	return strings.ContainsRune(" \t\n([\"'—–-", previous)
}

// ChunkFormattedMessage splits formatted text without losing styles. It
// enforces Zalo's conservative character, style-count and payload budgets.
func ChunkFormattedMessage(formatted FormattedMessage, protected []TextRange) []FormattedChunk {
	if formatted.Text == "" {
		return nil
	}
	total := utf16Length(formatted.Text)
	if total <= MaxNativeTextLength && len(formatted.Styles) <= MaxNativeTextStyles && formattedPayloadBytes(formatted) <= MaxNativePayloadBytes {
		return []FormattedChunk{{Text: formatted.Text, Styles: formatted.Styles, Start: 0, End: total}}
	}

	chunks := make([]FormattedChunk, 0, total/MaxNativeTextLength+1)
	for start := 0; start < total; {
		end := min(start+MaxNativeTextLength, total)
		end = floorUTF16Boundary(formatted.Text, end)
		end = preferReadableBoundary(formatted.Text, start, end)
		end = protectBoundary(end, start, protected)

		for styleCountInRange(formatted.Styles, start, end) > MaxNativeTextStyles && end > start+1 {
			candidate := lastStyleBoundary(formatted.Styles, protected, start, end)
			if candidate <= start || candidate >= end {
				candidate = start + (end-start)/2
			}
			candidate = floorUTF16Boundary(formatted.Text, candidate)
			end = protectBoundary(candidate, start, protected)
		}

		chunk := sliceFormattedMessage(formatted, start, end)
		for formattedPayloadBytes(FormattedMessage{Text: chunk.Text, Styles: chunk.Styles}) > MaxNativePayloadBytes && end > start+1 {
			bytes := formattedPayloadBytes(FormattedMessage{Text: chunk.Text, Styles: chunk.Styles})
			candidate := start + (end-start)*MaxNativePayloadBytes/bytes
			if candidate <= start {
				candidate = start + 1
			}
			candidate = floorUTF16Boundary(formatted.Text, candidate)
			if candidate <= start {
				candidate = nextUTF16Boundary(formatted.Text, start)
			}
			candidate = preferReadableBoundary(formatted.Text, start, candidate)
			candidate = protectBoundary(candidate, start, protected)
			if candidate >= end {
				candidate = end - 1
				candidate = protectBoundary(candidate, start, protected)
			}
			end = candidate
			chunk = sliceFormattedMessage(formatted, start, end)
		}
		if end <= start {
			end = nextUTF16Boundary(formatted.Text, start)
			chunk = sliceFormattedMessage(formatted, start, end)
		}
		chunks = append(chunks, chunk)
		start = end
	}
	return chunks
}

func sliceFormattedMessage(formatted FormattedMessage, start, end int) FormattedChunk {
	startByte := byteIndexAtUTF16(formatted.Text, start)
	endByte := byteIndexAtUTF16(formatted.Text, end)
	styles := make([]TextStyle, 0)
	for _, style := range formatted.Styles {
		styleEnd := style.Start + style.Len
		if style.Start >= end || styleEnd <= start {
			continue
		}
		clippedStart := max(style.Start, start)
		clippedEnd := min(styleEnd, end)
		styles = append(styles, TextStyle{Start: clippedStart - start, Len: clippedEnd - clippedStart, Style: style.Style})
	}
	return FormattedChunk{Text: formatted.Text[startByte:endByte], Styles: styles, Start: start, End: end}
}

func formattedPayloadBytes(formatted FormattedMessage) int {
	styleJSON, _ := json.Marshal(formatted.Styles)
	return len([]byte(formatted.Text)) + len(styleJSON) + strings.Count(formatted.Text, "@")*mentionReserveBytes
}

func styleCountInRange(styles []TextStyle, start, end int) int {
	count := 0
	for _, style := range styles {
		if style.Start < end && style.Start+style.Len > start {
			count++
		}
	}
	return count
}

func lastStyleBoundary(styles []TextStyle, protected []TextRange, start, end int) int {
	boundaries := make([]int, 0, len(styles)*2)
	for _, style := range styles {
		for _, boundary := range []int{style.Start, style.Start + style.Len} {
			if boundary > start && boundary < end {
				boundaries = append(boundaries, boundary)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(boundaries)))
	for _, boundary := range boundaries {
		boundary = protectBoundary(boundary, start, protected)
		if boundary > start && boundary < end && styleCountInRange(styles, start, boundary) <= MaxNativeTextStyles {
			return boundary
		}
	}
	return 0
}

func protectBoundary(boundary, start int, protected []TextRange) int {
	for _, item := range protected {
		if boundary > item.Start && boundary < item.End {
			if item.Start > start {
				return item.Start
			}
			return item.End
		}
	}
	return boundary
}

func preferReadableBoundary(text string, start, end int) int {
	total := utf16Length(text)
	if end >= total {
		return total
	}
	startByte := byteIndexAtUTF16(text, start)
	endByte := byteIndexAtUTF16(text, end)
	window := text[startByte:endByte]
	windowUnits := end - start
	for _, marker := range []string{"\n\n", "\n", ". ", "; ", ", ", " "} {
		index := strings.LastIndex(window, marker)
		if index <= 0 {
			continue
		}
		candidate := start + utf16Length(window[:index+len(marker)])
		if candidate-start >= windowUnits*6/10 {
			return candidate
		}
	}
	return end
}

func byteIndexAtUTF16(value string, target int) int {
	if target <= 0 {
		return 0
	}
	units := 0
	for index, r := range value {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > target {
			return index
		}
		units += width
		if units == target {
			return index + utf8.RuneLen(r)
		}
	}
	return len(value)
}

func floorUTF16Boundary(value string, target int) int {
	return utf16Length(value[:byteIndexAtUTF16(value, target)])
}

func nextUTF16Boundary(value string, start int) int {
	startByte := byteIndexAtUTF16(value, start)
	if startByte >= len(value) {
		return start
	}
	r, _ := utf8.DecodeRuneInString(value[startByte:])
	if r > 0xffff {
		return start + 2
	}
	return start + 1
}

func utf16Length(value string) int { return len(utf16.Encode([]rune(value))) }
