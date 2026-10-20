package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

// InvokeNativeAction exposes the deliberately small, fixed allowlist used by the
// zalo_personal tool. Unknown actions never reach the protocol layer.
func (c *Channel) InvokeNativeAction(ctx context.Context, req channels.NativeActionRequest) (any, error) {
	sess := c.session()
	if !c.IsRunning() || sess == nil {
		return nil, fmt.Errorf("zalo_personal channel not running")
	}
	threadType := protocol.ThreadTypeUser
	if req.PeerKind == "group" {
		threadType = protocol.ThreadTypeGroup
	}
	groupID := func() (string, error) {
		if threadType != protocol.ThreadTypeGroup || req.ChatID == "" {
			return "", fmt.Errorf("zalo_personal: action %q requires a group conversation", req.Action)
		}
		return req.ChatID, nil
	}

	switch req.Action {
	case "list_friends":
		return protocol.FetchFriends(ctx, sess)
	case "list_groups":
		return protocol.FetchGroups(ctx, sess)
	case "create_poll":
		id, err := groupID()
		if err != nil {
			return nil, err
		}
		question, err := requiredString(req.Params, "question")
		if err != nil {
			return nil, err
		}
		options, err := requiredStringSlice(req.Params, "options")
		if err != nil {
			return nil, err
		}
		if len(options) < 2 {
			return nil, fmt.Errorf("zalo_personal: a poll requires at least two options")
		}
		return protocol.CreatePoll(ctx, sess, id, protocol.PollOptions{
			Question: question, Options: options, ExpiredTime: int64Value(req.Params, "expired_time"),
			AllowMultiChoices: boolValue(req.Params, "allow_multi_choices"),
			AllowAddOption:    boolValue(req.Params, "allow_add_option"),
			HideVotePreview:   boolValue(req.Params, "hide_vote_preview"), Anonymous: boolValue(req.Params, "anonymous"),
		})
	case "get_poll":
		pollID, err := requiredInt64(req.Params, "poll_id")
		if err != nil || pollID <= 0 {
			return nil, fmt.Errorf("zalo_personal: poll_id must be a positive integer")
		}
		return protocol.GetPollDetail(ctx, sess, pollID)
	case "lock_poll":
		pollID, err := requiredInt64(req.Params, "poll_id")
		if err != nil || pollID <= 0 {
			return nil, fmt.Errorf("zalo_personal: poll_id must be a positive integer")
		}
		return protocol.LockPoll(ctx, sess, pollID)
	case "create_note":
		id, err := groupID()
		if err != nil {
			return nil, err
		}
		title, err := requiredString(req.Params, "title")
		if err != nil {
			return nil, err
		}
		return protocol.CreateNote(ctx, sess, id, title, boolValue(req.Params, "pin"))
	case "create_reminder":
		title, err := requiredString(req.Params, "title")
		if err != nil {
			return nil, err
		}
		return protocol.CreateReminder(ctx, sess, req.ChatID, threadType, protocol.ReminderOptions{
			Title: title, Emoji: stringValue(req.Params, "emoji"), StartTime: int64Value(req.Params, "start_time"),
			Repeat: int(int64Value(req.Params, "repeat")),
		})
	case "list_reminders":
		return protocol.ListReminders(ctx, sess, req.ChatID, threadType,
			int(int64Value(req.Params, "page")), int(int64Value(req.Params, "count")))
	case "remove_reminder":
		reminderID, err := requiredString(req.Params, "reminder_id")
		if err != nil {
			return nil, err
		}
		return protocol.RemoveReminder(ctx, sess, req.ChatID, threadType, reminderID)
	case "add_group_members", "remove_group_members":
		id, err := groupID()
		if err != nil {
			return nil, err
		}
		members, err := requiredStringSlice(req.Params, "member_ids")
		if err != nil {
			return nil, err
		}
		return protocol.UpdateGroupMembers(ctx, sess, id, members, req.Action == "add_group_members")
	case "add_group_deputies", "remove_group_deputies":
		id, err := groupID()
		if err != nil {
			return nil, err
		}
		members, err := requiredStringSlice(req.Params, "member_ids")
		if err != nil {
			return nil, err
		}
		return protocol.UpdateGroupDeputies(ctx, sess, id, members, req.Action == "add_group_deputies")
	case "send_friend_request":
		userID, err := requiredString(req.Params, "user_id")
		if err != nil {
			return nil, err
		}
		return protocol.SendFriendRequest(ctx, sess, userID, stringValue(req.Params, "message"))
	case "accept_friend_request":
		userID, err := requiredString(req.Params, "user_id")
		if err != nil {
			return nil, err
		}
		return protocol.AcceptFriendRequest(ctx, sess, userID)
	case "get_group_link":
		id, err := groupID()
		if err != nil {
			return nil, err
		}
		return protocol.GetGroupLink(ctx, sess, id)
	case "enable_group_link", "disable_group_link":
		id, err := groupID()
		if err != nil {
			return nil, err
		}
		return protocol.SetGroupLink(ctx, sess, id, req.Action == "enable_group_link")
	case "join_group_link":
		link, err := requiredString(req.Params, "link")
		if err != nil {
			return nil, err
		}
		return protocol.JoinGroupLink(ctx, sess, link)
	case "pin_conversation", "unpin_conversation":
		return protocol.SetPinnedConversation(ctx, sess, req.ChatID, threadType, req.Action == "pin_conversation")
	case "mute_conversation", "unmute_conversation":
		return protocol.SetMute(ctx, sess, req.ChatID, threadType, req.Action == "mute_conversation",
			int64Value(req.Params, "duration_seconds"))
	case "update_profile_bio":
		bio, err := requiredString(req.Params, "bio")
		if err != nil {
			return nil, err
		}
		return protocol.UpdateProfileBio(ctx, sess, bio)
	case "get_user_info":
		userIDs, err := requiredStringSlice(req.Params, "user_ids")
		if err != nil {
			return nil, err
		}
		return protocol.GetUserInfo(ctx, sess, userIDs, int(int64Value(req.Params, "avatar_size")))
	case "update_profile":
		name, err := requiredString(req.Params, "name")
		if err != nil {
			return nil, err
		}
		dateOfBirth, err := requiredString(req.Params, "date_of_birth")
		if err != nil {
			return nil, err
		}
		if _, err := time.Parse("2006-01-02", dateOfBirth); err != nil {
			return nil, fmt.Errorf("zalo_personal: date_of_birth must use YYYY-MM-DD")
		}
		genderValue, err := requiredInt64(req.Params, "gender")
		if err != nil {
			return nil, err
		}
		gender := int(genderValue)
		if gender != 0 && gender != 1 {
			return nil, fmt.Errorf("zalo_personal: gender must be 0 (male) or 1 (female)")
		}
		return protocol.UpdateProfile(ctx, sess, name, dateOfBirth, gender)
	case "update_active_status":
		active, ok := req.Params["active"].(bool)
		if !ok {
			return nil, fmt.Errorf("zalo_personal: active must be a boolean")
		}
		return protocol.UpdateActiveStatus(ctx, sess, active)
	case "quote_message":
		text, err := requiredString(req.Params, "message")
		if err != nil {
			return nil, err
		}
		messageID, err := requiredString(req.Params, "message_id")
		if err != nil {
			return nil, err
		}
		ownerID, err := requiredString(req.Params, "message_sender_id")
		if err != nil {
			return nil, err
		}
		timestamp, err := requiredString(req.Params, "message_timestamp")
		if err != nil {
			return nil, err
		}
		messageType := stringValue(req.Params, "message_type")
		if messageType == "" {
			messageType = "webchat"
		}
		propertyExt, err := optionalQuoteJSON(req.Params, "message_property_ext")
		if err != nil {
			return nil, err
		}
		attachment, err := optionalQuoteJSON(req.Params, "message_attachment")
		if err != nil {
			return nil, err
		}
		var ttl int64
		if rawTTL := stringValue(req.Params, "message_ttl"); rawTTL != "" {
			ttl, err = strconv.ParseInt(rawTTL, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("zalo_personal: message_ttl must be an integer")
			}
		}
		result, err := protocol.SendMessageWithOptions(ctx, sess, req.ChatID, threadType, text, nil, &protocol.MessageQuote{
			OwnerID: ownerID, MessageID: messageID, ClientMessageID: stringValue(req.Params, "client_message_id"),
			MessageType: messageType, Timestamp: timestamp, Content: stringValue(req.Params, "quoted_content"),
			PropertyExt: propertyExt, Attachment: attachment, TTL: ttl,
		})
		if err != nil {
			return nil, err
		}
		c.trackSentMessage(req.ChatID, threadType, "quote", result)
		return result, nil
	case "react":
		messageID, err := requiredString(req.Params, "message_id")
		if err != nil {
			return nil, err
		}
		clientMessageID, err := requiredString(req.Params, "client_message_id")
		if err != nil {
			return nil, err
		}
		icon, err := requiredString(req.Params, "reaction")
		if err != nil {
			return nil, err
		}
		return protocol.AddReaction(ctx, sess, req.ChatID, threadType, messageID, clientMessageID, icon)
	case "undo_message":
		target, err := c.findTrackedSentMessage(req.ChatID,
			stringValue(req.Params, "message_id"), stringValue(req.Params, "client_message_id"))
		if err != nil {
			return nil, err
		}
		result, err := protocol.UndoMessage(ctx, sess, req.ChatID, threadType, target.MessageID, target.ClientMessageID)
		if err != nil {
			return nil, err
		}
		c.markTrackedMessageRecalled(req.ChatID, target.MessageID, target.ClientMessageID)
		return map[string]any{"message_id": target.MessageID, "client_message_id": target.ClientMessageID, "response": result}, nil
	case "list_sent_messages":
		items := c.trackedSentMessages(req.ChatID)
		count := int(int64Value(req.Params, "count"))
		if count <= 0 {
			count = 20
		}
		if count > 100 {
			count = 100
		}
		if len(items) > count {
			items = items[:count]
		}
		return items, nil
	default:
		return nil, fmt.Errorf("zalo_personal: action %q is not in the native API allowlist", req.Action)
	}
}

// ListGroupMembers implements channels.GroupMemberProvider.
func (c *Channel) ListGroupMembers(ctx context.Context, chatID string) ([]channels.GroupMember, error) {
	sess := c.session()
	if !c.IsRunning() || sess == nil {
		return nil, fmt.Errorf("zalo_personal channel not running")
	}
	members, err := protocol.FetchGroupMembers(ctx, sess, chatID)
	if err != nil {
		return nil, err
	}
	out := make([]channels.GroupMember, len(members))
	for i, member := range members {
		out[i] = channels.GroupMember{MemberID: member.UserID, Name: member.DisplayName}
	}
	return out, nil
}

func stringValue(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func requiredString(params map[string]any, key string) (string, error) {
	value := stringValue(params, key)
	if value == "" {
		return "", fmt.Errorf("zalo_personal: %s is required", key)
	}
	return value, nil
}

func requiredStringSlice(params map[string]any, key string) ([]string, error) {
	var result []string
	switch values := params[key].(type) {
	case []string:
		result = append(result, values...)
	case []any:
		for _, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("zalo_personal: %s must contain non-empty strings", key)
			}
			result = append(result, strings.TrimSpace(text))
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("zalo_personal: %s is required", key)
	}
	return result, nil
}

func int64Value(params map[string]any, key string) int64 {
	switch value := params[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	}
	return 0
}

func requiredInt64(params map[string]any, key string) (int64, error) {
	if _, ok := params[key]; !ok {
		return 0, fmt.Errorf("zalo_personal: %s is required", key)
	}
	switch params[key].(type) {
	case int, int64, float64, json.Number:
		return int64Value(params, key), nil
	default:
		return 0, fmt.Errorf("zalo_personal: %s must be an integer", key)
	}
}

func boolValue(params map[string]any, key string) bool {
	value, _ := params[key].(bool)
	return value
}

func optionalQuoteJSON(params map[string]any, key string) (any, error) {
	raw := stringValue(params, key)
	if raw == "" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("zalo_personal: %s must be valid JSON: %w", key, err)
	}
	return value, nil
}
