package personal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels/typing"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// Send delivers an outbound message to a Zalo chat.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	sess := c.session()
	if !c.IsRunning() || sess == nil {
		return fmt.Errorf("zalo_personal channel not running")
	}

	// Translate agent Markdown to Zalo's native styles while preserving explicit
	// mention markup as native mention ranges.
	formatted, outboundMentions := formatOutboundMessage(msg.Content)

	// Stop typing indicator before sending response
	if ctrl, ok := c.typingCtrls.LoadAndDelete(msg.ChatID); ok {
		ctrl.(*typing.Controller).Stop()
	}

	threadType := protocol.ThreadTypeUser
	if c.IsGroupApproved(msg.ChatID) {
		threadType = protocol.ThreadTypeGroup
	} else if msg.Metadata != nil {
		if _, ok := msg.Metadata["group_id"]; ok {
			threadType = protocol.ThreadTypeGroup
			c.MarkGroupApproved(msg.ChatID)
		}
	}

	// Send media attachments.
	for _, media := range msg.Media {
		if protocol.IsImageFile(media.URL) {
			if err := c.sendImage(ctx, sess, msg.ChatID, threadType, media.URL, media.Caption); err != nil {
				slog.Warn("zalo_personal: failed to send image", "path", media.URL, "error", err)
			}
		} else {
			if err := c.sendFile(ctx, sess, msg.ChatID, threadType, media.URL); err != nil {
				slog.Warn("zalo_personal: failed to send file", "path", media.URL, "error", err)
			}
		}
	}

	// Send text content (if any remains after media).
	if formatted.Text != "" {
		return c.sendChunkedText(ctx, sess, msg.ChatID, threadType, formatted, outboundMentions, quoteFromMetadata(msg.Metadata))
	}
	return nil
}

// sendImage uploads and sends an image file to a Zalo thread.
func (c *Channel) sendImage(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, filePath, caption string) error {
	upload, err := protocol.UploadImage(ctx, sess, chatID, threadType, filePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	result, err := protocol.SendImageTracked(ctx, sess, chatID, threadType, upload, caption)
	if err == nil {
		c.trackSentMessage(chatID, threadType, "image", result)
	}
	return err
}

// sendFile uploads and sends a file to a Zalo thread.
func (c *Channel) sendFile(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, filePath string) error {
	ln := c.getListener()
	if ln == nil {
		return fmt.Errorf("listener not available for file upload")
	}
	upload, err := protocol.UploadFile(ctx, sess, ln, chatID, threadType, filePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	result, err := protocol.SendFileTracked(ctx, sess, chatID, threadType, upload)
	if err == nil {
		c.trackSentMessage(chatID, threadType, "file", result)
	}
	return err
}

func (c *Channel) sendChunkedText(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, formatted zalo.FormattedMessage, mentions []outboundMention, quote *protocol.MessageQuote) error {
	protected := make([]zalo.TextRange, 0, len(mentions))
	for _, mention := range mentions {
		protected = append(protected, zalo.TextRange{Start: mention.Start, End: mention.End})
	}
	for index, chunk := range zalo.ChunkFormattedMessage(formatted, protected) {
		var nativeMentions []protocol.Mention
		if threadType == protocol.ThreadTypeGroup {
			for _, mention := range mentions {
				if mention.Start < chunk.Start || mention.End > chunk.End {
					continue
				}
				nativeMentions = append(nativeMentions, protocol.Mention{
					Pos: mention.Start - chunk.Start, UID: mention.UID,
					Len: mention.End - mention.Start, Type: mentionType(mention.UID),
				})
			}
		}
		nativeStyles := make([]protocol.TextStyle, 0, len(chunk.Styles))
		for _, style := range chunk.Styles {
			nativeStyles = append(nativeStyles, protocol.TextStyle{Start: style.Start, Len: style.Len, Style: style.Style})
		}
		chunkQuote := quote
		if index > 0 {
			chunkQuote = nil
		}
		result, err := protocol.SendMessageWithNativeOptions(ctx, sess, chatID, threadType, chunk.Text, nativeMentions, nativeStyles, chunkQuote)
		if err != nil && (len(nativeStyles) > 0 || len(nativeMentions) > 0) {
			var apiErr *protocol.SendAPIError
			if errors.As(err, &apiErr) {
				slog.Warn("zalo_personal: native formatting rejected; retrying plain text",
					"chat_id", chatID, "chunk", index+1, "error_code", apiErr.Code)
				result, err = protocol.SendMessageWithOptions(ctx, sess, chatID, threadType, chunk.Text, nil, chunkQuote)
			}
		}
		if err != nil {
			return err
		}
		c.trackSentMessage(chatID, threadType, "text", result)
	}
	return nil
}

func quoteFromMetadata(metadata map[string]string) *protocol.MessageQuote {
	if metadata == nil || metadata[metaZaloMessageID] == "" || metadata[metaZaloMessageSenderID] == "" || metadata[metaZaloMessageTS] == "" {
		return nil
	}
	messageType := metadata[metaZaloMessageType]
	if messageType == "" {
		messageType = "webchat"
	}
	quote := &protocol.MessageQuote{
		OwnerID: metadata[metaZaloMessageSenderID], MessageID: metadata[metaZaloMessageID],
		ClientMessageID: metadata[metaZaloClientMessageID], MessageType: messageType,
		Timestamp: metadata[metaZaloMessageTS], Content: metadata[metaZaloMessageContent],
	}
	if value := metadata[metaZaloMessageTTL]; value != "" {
		quote.TTL, _ = strconv.ParseInt(value, 10, 64)
	}
	quote.PropertyExt = decodeQuoteMetadata(metadata[metaZaloMessageProperty])
	quote.Attachment = decodeQuoteMetadata(metadata[metaZaloMessageAttach])
	return quote
}

func decodeQuoteMetadata(raw string) any {
	if raw == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		slog.Debug("zalo_personal: invalid quote metadata", "error", err)
		return nil
	}
	return value
}

type outboundMention struct {
	UID        string
	Start, End int // UTF-16 offsets in the final display text
}

type mentionSpec struct {
	UID, Name string
}

var (
	mentionMarkupRE = regexp.MustCompile(`@\[([^\]]+)\]\(([^)\s]+)\)`)
	mentionMarkerRE = regexp.MustCompile("\uE000([0-9]+)\uE001")
)

// formatOutboundMessage converts Markdown and @[Display name](zalo_uid) markup
// together so styles and mentions retain correct UTF-16 offsets.
func formatOutboundMessage(input string) (zalo.FormattedMessage, []outboundMention) {
	var specs []mentionSpec
	marked := mentionMarkupRE.ReplaceAllStringFunc(input, func(match string) string {
		parts := mentionMarkupRE.FindStringSubmatch(match)
		index := len(specs)
		specs = append(specs, mentionSpec{Name: parts[1], UID: parts[2]})
		return "\uE000" + strconv.Itoa(index) + "\uE001"
	})
	formatted := zalo.FormatMarkdown(marked)
	if len(specs) == 0 {
		return formatted, nil
	}

	matches := mentionMarkerRE.FindAllStringSubmatchIndex(formatted.Text, -1)
	var output strings.Builder
	mentions := make([]outboundMention, 0, len(matches))
	type replacement struct {
		oldStart, oldEnd int
		newStart, newEnd int
	}
	replacements := make([]replacement, 0, len(matches))
	cursor := 0
	newOffset := 0
	for _, match := range matches {
		prefix := formatted.Text[cursor:match[0]]
		output.WriteString(prefix)
		newOffset += utf16Length(prefix)
		index, err := strconv.Atoi(formatted.Text[match[2]:match[3]])
		if err != nil || index < 0 || index >= len(specs) {
			marker := formatted.Text[match[0]:match[1]]
			output.WriteString(marker)
			newOffset += utf16Length(marker)
			cursor = match[1]
			continue
		}
		visible := "@" + specs[index].Name
		oldStart := utf16Length(formatted.Text[:match[0]])
		oldEnd := oldStart + utf16Length(formatted.Text[match[0]:match[1]])
		start := newOffset
		output.WriteString(visible)
		newOffset += utf16Length(visible)
		mentions = append(mentions, outboundMention{UID: specs[index].UID, Start: start, End: newOffset})
		replacements = append(replacements, replacement{oldStart: oldStart, oldEnd: oldEnd, newStart: start, newEnd: newOffset})
		cursor = match[1]
	}
	output.WriteString(formatted.Text[cursor:])

	mapBoundary := func(position int, endBoundary bool) int {
		delta := 0
		for _, item := range replacements {
			if position <= item.oldStart {
				return position + delta
			}
			if position < item.oldEnd {
				if endBoundary {
					return item.newEnd
				}
				return item.newStart
			}
			delta += (item.newEnd - item.newStart) - (item.oldEnd - item.oldStart)
		}
		return position + delta
	}
	styles := make([]zalo.TextStyle, 0, len(formatted.Styles))
	for _, style := range formatted.Styles {
		start := mapBoundary(style.Start, false)
		end := mapBoundary(style.Start+style.Len, true)
		if end > start {
			styles = append(styles, zalo.TextStyle{Start: start, Len: end - start, Style: style.Style})
		}
	}
	return zalo.FormattedMessage{Text: output.String(), Styles: styles}, mentions
}

// parseOutboundMentions remains as a small compatibility helper for tests and
// callers that only need the final visible text and mention ranges.
func parseOutboundMentions(input string) (string, []outboundMention) {
	formatted, mentions := formatOutboundMessage(input)
	return formatted.Text, mentions
}

func utf16Length(value string) int { return len(utf16.Encode([]rune(value))) }

func mentionType(uid string) int {
	if uid == "-1" {
		return 1
	}
	return 0
}
