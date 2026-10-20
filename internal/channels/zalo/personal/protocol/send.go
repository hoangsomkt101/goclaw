package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"time"
)

// SendMessage sends a text message to a user or group.
// threadID: user UID (DM) or group ID (group).
func SendMessage(ctx context.Context, sess *Session, threadID string, threadType ThreadType, text string) (string, error) {
	return SendMessageWithMentions(ctx, sess, threadID, threadType, text, nil)
}

// SendResult identifies a successfully submitted Zalo message. Both IDs are
// required by follow-up APIs such as reaction and undo.
type SendResult struct {
	MessageID       string `json:"message_id"`
	ClientMessageID string `json:"client_message_id"`
}

// MessageQuote contains the source-message fields required by Zalo's native
// quote endpoint.
type MessageQuote struct {
	OwnerID         string
	MessageID       string
	ClientMessageID string
	MessageType     string
	Timestamp       string
	Content         string
	PropertyExt     any
	Attachment      any
	TTL             int64
}

// Mention identifies a UTF-16 range in a group message for a native Zalo mention.
// Zalo's web API uses JavaScript string offsets, hence UTF-16 rather than Go byte offsets.
type Mention struct {
	Pos  int    `json:"pos"`
	UID  string `json:"uid"`
	Len  int    `json:"len"`
	Type int    `json:"type"`
}

// TextStyle identifies a UTF-16 range formatted with one of Zalo's native
// style codes (for example b, i, s, or f_18).
type TextStyle struct {
	Start int    `json:"start"`
	Len   int    `json:"len"`
	Style string `json:"st"`
}

// SendAPIError means Zalo parsed and explicitly rejected a send request. It is
// intentionally distinct from transport and response-decoding failures so a
// caller can safely retry a formatting rejection without duplicating a message.
type SendAPIError struct {
	Code    int
	Message string
}

func (e *SendAPIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("zalo_personal: send error code %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("zalo_personal: send error code %d", e.Code)
}

// SendMessageWithMentions sends a text message with optional native group mentions.
func SendMessageWithMentions(ctx context.Context, sess *Session, threadID string, threadType ThreadType, text string, mentions []Mention) (string, error) {
	result, err := SendMessageWithOptions(ctx, sess, threadID, threadType, text, mentions, nil)
	return result.MessageID, err
}

// SendMessageWithOptions sends text with optional mentions and a native quote,
// returning the server and client IDs used for later message operations.
func SendMessageWithOptions(ctx context.Context, sess *Session, threadID string, threadType ThreadType, text string, mentions []Mention, quote *MessageQuote) (SendResult, error) {
	return SendMessageWithNativeOptions(ctx, sess, threadID, threadType, text, mentions, nil, quote)
}

// SendMessageWithNativeOptions sends text with optional native mentions,
// styles, and quote metadata.
func SendMessageWithNativeOptions(ctx context.Context, sess *Session, threadID string, threadType ThreadType, text string, mentions []Mention, styles []TextStyle, quote *MessageQuote) (SendResult, error) {
	if text == "" {
		return SendResult{}, fmt.Errorf("zalo_personal: message text cannot be empty")
	}
	if len(mentions) > 0 && threadType != ThreadTypeGroup {
		return SendResult{}, fmt.Errorf("zalo_personal: mentions are only supported in group messages")
	}

	serviceKey := "chat"
	apiPath := "/api/message/sms"
	if threadType == ThreadTypeGroup {
		serviceKey = "group"
		if len(mentions) > 0 {
			apiPath = "/api/group/mention"
		} else {
			apiPath = "/api/group/sendmsg"
		}
	}

	baseURL := getServiceURL(sess, serviceKey)
	if baseURL == "" {
		return SendResult{}, fmt.Errorf("zalo_personal: no service URL for %s", serviceKey)
	}

	// Build payload
	clientID := time.Now().UnixMilli()
	payload := map[string]any{
		"message":  text,
		"clientId": clientID,
		"ttl":      0,
	}
	if len(styles) > 0 {
		textProperties, err := json.Marshal(struct {
			Styles  []TextStyle `json:"styles"`
			Version int         `json:"ver"`
		}{Styles: styles})
		if err != nil {
			return SendResult{}, fmt.Errorf("zalo_personal: encode text styles: %w", err)
		}
		payload["textProperties"] = string(textProperties)
	}
	if threadType == ThreadTypeGroup {
		payload["grid"] = threadID
		payload["visibility"] = 0
		if len(mentions) > 0 {
			mentionJSON, err := json.Marshal(mentions)
			if err != nil {
				return SendResult{}, fmt.Errorf("zalo_personal: encode mentions: %w", err)
			}
			payload["mentionInfo"] = string(mentionJSON)
		}
	} else {
		payload["toid"] = threadID
		payload["imei"] = sess.IMEI
	}
	if quote != nil {
		payload["qmsgOwner"] = quote.OwnerID
		payload["qmsgId"] = quote.MessageID
		payload["qmsgCliId"] = quote.ClientMessageID
		payload["qmsgType"] = clientMessageType(quote.MessageType)
		payload["qmsgTs"] = quote.Timestamp
		payload["qmsg"] = quote.Content
		if quote.Attachment != nil {
			payload["qmsg"] = ""
		}
		payload["qmsgTTL"] = quote.TTL
		quoteAttachment := quote.PropertyExt
		if quote.Attachment != nil {
			quoteAttachment = prepareQuoteAttachment(quote.Attachment)
		}
		if threadType == ThreadTypeGroup && quoteAttachment != nil {
			attachment, err := json.Marshal(quoteAttachment)
			if err != nil {
				return SendResult{}, fmt.Errorf("zalo_personal: encode quote attachment: %w", err)
			}
			payload["qmsgAttach"] = string(attachment)
		}
	}

	// Encrypt payload with session secret key
	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return SendResult{}, fmt.Errorf("zalo_personal: encrypt send payload: %w", err)
	}
	if quote != nil {
		apiPath = "/api/message/quote"
		if threadType == ThreadTypeGroup {
			apiPath = "/api/group/quote"
		}
	}

	// Build URL with standard params
	sendURL := makeURL(sess, baseURL+apiPath, map[string]any{"nretry": 0}, true)

	// POST form-encoded
	form := buildFormBody(map[string]string{"params": encData})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, form)
	if err != nil {
		return SendResult{}, err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return SendResult{}, fmt.Errorf("zalo_personal: send message: %w", err)
	}
	defer resp.Body.Close()

	// Send response is encrypted: {"error_code":0, "data":"<encrypted>"}
	var envelope Response[*string]
	if err := readJSON(resp, &envelope); err != nil {
		return SendResult{}, fmt.Errorf("zalo_personal: parse send response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		return SendResult{}, &SendAPIError{Code: envelope.ErrorCode, Message: envelope.ErrorMessage}
	}
	if envelope.Data == nil {
		return SendResult{ClientMessageID: fmt.Sprint(clientID)}, nil
	}

	plain, err := decryptDataField(sess, *envelope.Data)
	if err != nil {
		return SendResult{}, fmt.Errorf("zalo_personal: decrypt send response: %w", err)
	}

	var result struct {
		MsgID json.Number `json:"msgId"`
	}
	if err := json.Unmarshal(plain, &result); err != nil {
		return SendResult{}, fmt.Errorf("zalo_personal: parse send result: %w", err)
	}

	return SendResult{MessageID: result.MsgID.String(), ClientMessageID: fmt.Sprint(clientID)}, nil
}

func prepareQuoteAttachment(value any) any {
	source, ok := value.(map[string]any)
	if !ok {
		return value
	}
	result := make(map[string]any, len(source)+3)
	maps.Copy(result, source)
	if thumb, ok := source["thumb"]; ok {
		result["thumbUrl"] = thumb
	}
	if href, ok := source["href"]; ok {
		result["oriUrl"] = href
		result["normalUrl"] = href
	}
	return result
}

func clientMessageType(messageType string) int {
	switch messageType {
	case "chat.voice":
		return 31
	case "chat.photo":
		return 32
	case "chat.sticker":
		return 36
	case "chat.doodle":
		return 37
	case "chat.recommended", "chat.link":
		return 38
	case "chat.location.new":
		return 43
	case "chat.video.msg":
		return 44
	case "share.file":
		return 46
	case "chat.gif":
		return 49
	default:
		return 1
	}
}

// SendTypingEvent sends a typing indicator to a user or group.
// Zalo typing lasts ~5s server-side; callers should re-send every 3-4s.
func SendTypingEvent(ctx context.Context, sess *Session, threadID string, threadType ThreadType) error {
	serviceKey := "chat"
	apiPath := "/api/message/typing"
	if threadType == ThreadTypeGroup {
		serviceKey = "group"
		apiPath = "/api/group/typing"
	}

	baseURL := getServiceURL(sess, serviceKey)
	if baseURL == "" {
		return fmt.Errorf("zalo_personal: no service URL for %s", serviceKey)
	}

	payload := map[string]any{"imei": sess.IMEI}
	if threadType == ThreadTypeGroup {
		payload["grid"] = threadID
	} else {
		payload["toid"] = threadID
		payload["destType"] = 3 // DestTypeUser
	}

	encData, err := encryptPayload(sess, payload)
	if err != nil {
		return fmt.Errorf("zalo_personal: encrypt typing payload: %w", err)
	}

	typingURL := makeURL(sess, baseURL+apiPath, nil, true)
	form := buildFormBody(map[string]string{"params": encData})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, typingURL, form)
	if err != nil {
		return err
	}
	setDefaultHeaders(req, sess)

	resp, err := sess.Client.Do(req)
	if err != nil {
		return fmt.Errorf("zalo_personal: send typing: %w", err)
	}
	defer resp.Body.Close()

	var envelope Response[any]
	if err := readJSON(resp, &envelope); err != nil {
		return fmt.Errorf("zalo_personal: parse typing response: %w", err)
	}
	if envelope.ErrorCode != 0 {
		return fmt.Errorf("zalo_personal: typing error code %d", envelope.ErrorCode)
	}
	return nil
}

// getServiceURL extracts a service base URL from LoginInfo.
func getServiceURL(sess *Session, service string) string {
	if sess.LoginInfo == nil {
		return ""
	}
	var urls []string
	switch service {
	case "chat":
		urls = sess.LoginInfo.ZpwServiceMapV3.Chat
	case "group":
		urls = sess.LoginInfo.ZpwServiceMapV3.Group
	case "file":
		urls = sess.LoginInfo.ZpwServiceMapV3.File
	case "profile":
		urls = sess.LoginInfo.ZpwServiceMapV3.Profile
	case "group_poll":
		urls = sess.LoginInfo.ZpwServiceMapV3.GroupPoll
	case "group_board":
		urls = sess.LoginInfo.ZpwServiceMapV3.GroupBoard
	case "friend":
		urls = sess.LoginInfo.ZpwServiceMapV3.Friend
	case "conversation":
		urls = sess.LoginInfo.ZpwServiceMapV3.Conversation
	case "reaction":
		urls = sess.LoginInfo.ZpwServiceMapV3.Reaction
	}
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

// encryptPayload encrypts a JSON payload with the session's secret key via AES-CBC.
func encryptPayload(sess *Session, payload map[string]any) (string, error) {
	blob, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	key, err := base64.StdEncoding.DecodeString(sess.SecretKey)
	if err != nil {
		return "", fmt.Errorf("decode secret key: %w", err)
	}
	return EncodeAESCBC(key, string(blob), false)
}
