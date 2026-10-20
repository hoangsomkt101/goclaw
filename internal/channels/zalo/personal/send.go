package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/typing"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

const maxTextLength = 2000

// Send delivers an outbound message to a Zalo chat.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	sess := c.session()
	if !c.IsRunning() || sess == nil {
		return fmt.Errorf("zalo_personal channel not running")
	}

	// Preserve explicit Zalo mention markup while stripping other Markdown.
	var outboundMentions []outboundMention
	msg.Content, outboundMentions = parseOutboundMentions(msg.Content)

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
	if msg.Content != "" {
		return c.sendChunkedText(ctx, sess, msg.ChatID, threadType, msg.Content, outboundMentions, quoteFromMetadata(msg.Metadata))
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

func (c *Channel) sendChunkedText(ctx context.Context, sess *protocol.Session, chatID string, threadType protocol.ThreadType, text string, mentions []outboundMention, quote *protocol.MessageQuote) error {
	searchFrom := 0
	for index, chunk := range channels.ChunkMarkdown(text, maxTextLength) {
		relativeStart := strings.Index(text[searchFrom:], chunk)
		chunkStart := searchFrom
		if relativeStart >= 0 {
			chunkStart += relativeStart
		}
		chunkEnd := chunkStart + len(chunk)
		var nativeMentions []protocol.Mention
		if threadType == protocol.ThreadTypeGroup {
			for _, mention := range mentions {
				if mention.Start < chunkStart || mention.End > chunkEnd {
					continue
				}
				localStart := mention.Start - chunkStart
				localEnd := mention.End - chunkStart
				nativeMentions = append(nativeMentions, protocol.Mention{
					Pos: utf16Length(chunk[:localStart]), UID: mention.UID,
					Len: utf16Length(chunk[localStart:localEnd]), Type: mentionType(mention.UID),
				})
			}
		}
		chunkQuote := quote
		if index > 0 {
			chunkQuote = nil
		}
		result, err := protocol.SendMessageWithOptions(ctx, sess, chatID, threadType, chunk, nativeMentions, chunkQuote)
		if err != nil {
			return err
		}
		c.trackSentMessage(chatID, threadType, "text", result)
		searchFrom = chunkEnd
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
	Start, End int // byte offsets in the final plain-text message
}

type mentionSpec struct {
	UID, Name string
}

var (
	mentionMarkupRE = regexp.MustCompile(`@\[([^\]]+)\]\(([^)\s]+)\)`)
	mentionMarkerRE = regexp.MustCompile("\uE000([0-9]+)\uE001")
)

// parseOutboundMentions converts @[Display name](zalo_uid) into visible @Display name
// text while retaining exact ranges for Zalo's native mentionInfo payload.
func parseOutboundMentions(input string) (string, []outboundMention) {
	var specs []mentionSpec
	marked := mentionMarkupRE.ReplaceAllStringFunc(input, func(match string) string {
		parts := mentionMarkupRE.FindStringSubmatch(match)
		index := len(specs)
		specs = append(specs, mentionSpec{Name: parts[1], UID: parts[2]})
		return "\uE000" + strconv.Itoa(index) + "\uE001"
	})
	plain := zalo.StripMarkdown(marked)
	if len(specs) == 0 {
		return plain, nil
	}

	matches := mentionMarkerRE.FindAllStringSubmatchIndex(plain, -1)
	var output strings.Builder
	mentions := make([]outboundMention, 0, len(matches))
	cursor := 0
	for _, match := range matches {
		output.WriteString(plain[cursor:match[0]])
		index, err := strconv.Atoi(plain[match[2]:match[3]])
		if err != nil || index < 0 || index >= len(specs) {
			output.WriteString(plain[match[0]:match[1]])
			cursor = match[1]
			continue
		}
		visible := "@" + specs[index].Name
		start := output.Len()
		output.WriteString(visible)
		mentions = append(mentions, outboundMention{UID: specs[index].UID, Start: start, End: output.Len()})
		cursor = match[1]
	}
	output.WriteString(plain[cursor:])
	return output.String(), mentions
}

func utf16Length(value string) int { return len(utf16.Encode([]rune(value))) }

func mentionType(uid string) int {
	if uid == "-1" {
		return 1
	}
	return 0
}
