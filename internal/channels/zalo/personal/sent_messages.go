package personal

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

const maxTrackedSentMessagesPerChat = 100

const (
	metaZaloMessageID       = "zalo_message_id"
	metaZaloClientMessageID = "zalo_client_message_id"
	metaZaloMessageSenderID = "zalo_message_sender_id"
	metaZaloMessageType     = "zalo_message_type"
	metaZaloMessageTS       = "zalo_message_timestamp"
	metaZaloMessageContent  = "zalo_message_content"
	metaZaloMessageProperty = "zalo_message_property_ext"
	metaZaloMessageAttach   = "zalo_message_attachment"
	metaZaloMessageTTL      = "zalo_message_ttl"
)

func addZaloMessageMetadata(metadata map[string]string, msg protocol.TMessage, senderID, content string) {
	metadata["message_id"] = msg.MsgID
	metadata["client_message_id"] = msg.CliMsgID
	metadata[metaZaloMessageID] = msg.MsgID
	metadata[metaZaloClientMessageID] = msg.CliMsgID
	metadata[metaZaloMessageSenderID] = senderID
	metadata[metaZaloMessageType] = msg.MsgType
	metadata[metaZaloMessageTS] = msg.TS
	metadata[metaZaloMessageContent] = content
	if msg.TTL != 0 {
		metadata[metaZaloMessageTTL] = strconv.FormatInt(msg.TTL, 10)
	}
	if msg.PropertyExt != nil {
		if encoded, err := json.Marshal(msg.PropertyExt); err == nil {
			metadata[metaZaloMessageProperty] = string(encoded)
		}
	}
	if len(msg.Content.Raw) > 0 {
		metadata[metaZaloMessageAttach] = string(msg.Content.Raw)
	}
}

// SentMessage is a bounded in-memory record of a message sent by this account.
// Keeping both IDs lets native APIs safely target only messages owned by the bot.
type SentMessage struct {
	MessageID       string `json:"message_id"`
	ClientMessageID string `json:"client_message_id"`
	ChatID          string `json:"chat_id"`
	ThreadType      int    `json:"thread_type"`
	Kind            string `json:"kind"`
	SentAt          int64  `json:"sent_at"`
	Recalled        bool   `json:"recalled,omitempty"`
}

func (c *Channel) trackSentMessage(chatID string, threadType protocol.ThreadType, kind string, result protocol.SendResult) {
	if result.MessageID == "" || result.ClientMessageID == "" {
		slog.Warn("zalo_personal: sent message response missing IDs",
			"chat_id", chatID, "message_id", result.MessageID, "client_message_id", result.ClientMessageID)
		return
	}
	record := SentMessage{
		MessageID: result.MessageID, ClientMessageID: result.ClientMessageID,
		ChatID: chatID, ThreadType: int(threadType), Kind: kind, SentAt: time.Now().UnixMilli(),
	}
	c.sentMu.Lock()
	if c.sentMessages == nil {
		c.sentMessages = make(map[string][]SentMessage)
	}
	items := append(c.sentMessages[chatID], record)
	if len(items) > maxTrackedSentMessagesPerChat {
		items = append([]SentMessage(nil), items[len(items)-maxTrackedSentMessagesPerChat:]...)
	}
	c.sentMessages[chatID] = items
	c.sentMu.Unlock()
	slog.Debug("zalo_personal: sent message IDs tracked",
		"chat_id", chatID, "message_id", result.MessageID, "client_message_id", result.ClientMessageID, "kind", kind)
}

func (c *Channel) trackedSentMessages(chatID string) []SentMessage {
	c.sentMu.RLock()
	defer c.sentMu.RUnlock()
	items := c.sentMessages[chatID]
	result := make([]SentMessage, len(items))
	for i := range items {
		result[len(items)-1-i] = items[i]
	}
	return result
}

func (c *Channel) findTrackedSentMessage(chatID, messageID, clientMessageID string) (SentMessage, error) {
	c.sentMu.RLock()
	defer c.sentMu.RUnlock()
	items := c.sentMessages[chatID]
	if (messageID == "") != (clientMessageID == "") {
		return SentMessage{}, fmt.Errorf("zalo_personal: message_id and client_message_id must be provided together")
	}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Recalled {
			continue
		}
		if messageID == "" || (item.MessageID == messageID && item.ClientMessageID == clientMessageID) {
			return item, nil
		}
	}
	return SentMessage{}, fmt.Errorf("zalo_personal: no matching unrecalled bot message is tracked in this conversation")
}

func (c *Channel) markTrackedMessageRecalled(chatID, messageID, clientMessageID string) {
	c.sentMu.Lock()
	defer c.sentMu.Unlock()
	items := c.sentMessages[chatID]
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].MessageID == messageID && items[i].ClientMessageID == clientMessageID {
			items[i].Recalled = true
			c.sentMessages[chatID] = items
			return
		}
	}
}
