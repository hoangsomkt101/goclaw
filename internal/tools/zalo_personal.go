package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

var zaloPersonalActions = []string{
	"list_friends", "list_groups",
	"create_poll", "get_poll", "lock_poll",
	"create_note", "create_reminder", "list_reminders", "remove_reminder",
	"add_group_members", "remove_group_members", "add_group_deputies", "remove_group_deputies",
	"send_friend_request", "accept_friend_request",
	"get_group_link", "enable_group_link", "disable_group_link", "join_group_link",
	"pin_conversation", "unpin_conversation", "mute_conversation", "unmute_conversation",
	"get_user_info", "update_profile", "update_profile_bio", "update_active_status",
	"react", "quote_message", "undo_message", "list_sent_messages",
}

var zaloPersonalConfirmActions = map[string]bool{
	"lock_poll": true, "remove_reminder": true,
	"add_group_members": true, "remove_group_members": true,
	"add_group_deputies": true, "remove_group_deputies": true,
	"send_friend_request": true, "accept_friend_request": true,
	"enable_group_link": true, "disable_group_link": true, "join_group_link": true,
	"update_profile": true, "update_profile_bio": true, "update_active_status": true,
	"undo_message": true,
}

// NativeActionInvoker abstracts channels.Manager.InvokeNativeAction.
type NativeActionInvoker func(ctx context.Context, channel string, req channels.NativeActionRequest) (any, error)

// NativeActionInvokerAware receives the live channel action bridge after channel startup wiring.
type NativeActionInvokerAware interface {
	SetNativeActionInvoker(NativeActionInvoker)
}

// ZaloPersonalTool exposes a strict allowlist of native Zalo Personal APIs.
type ZaloPersonalTool struct {
	invoker NativeActionInvoker
}

func NewZaloPersonalTool() *ZaloPersonalTool { return &ZaloPersonalTool{} }

func (t *ZaloPersonalTool) SetNativeActionInvoker(invoker NativeActionInvoker) { t.invoker = invoker }

func (t *ZaloPersonalTool) RequiredChannelTypes() []string { return []string{"zalo_personal"} }

func (t *ZaloPersonalTool) Name() string { return "zalo_personal" }

func (t *ZaloPersonalTool) Description() string {
	return "Use native Zalo Personal APIs in the current conversation: polls, notes, reminders, group membership/deputies, friend requests, group invite links, pin/mute, profile status, reactions, native quoted messages, recall of tracked bot messages, sent-message ID lookup, and contact/group discovery. For react and quote_message, omit message IDs to target the current inbound message. For undo_message, omit IDs to recall the latest tracked bot message. Only enumerated actions are allowed. Sensitive actions require explicit user confirmation followed by confirm=true. Use list_group_members before mentioning someone; outbound mention syntax is @[Display name](zalo_uid)."
}

func (t *ZaloPersonalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":               map[string]any{"type": "string", "enum": zaloPersonalActions},
			"confirm":              map[string]any{"type": "boolean", "description": "Required only for sensitive actions, after explicit user confirmation"},
			"question":             map[string]any{"type": "string"},
			"options":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"expired_time":         map[string]any{"type": "integer", "description": "Poll expiration Unix time in milliseconds; 0 means no expiration"},
			"allow_multi_choices":  map[string]any{"type": "boolean"},
			"allow_add_option":     map[string]any{"type": "boolean"},
			"hide_vote_preview":    map[string]any{"type": "boolean"},
			"anonymous":            map[string]any{"type": "boolean"},
			"poll_id":              map[string]any{"type": "integer"},
			"title":                map[string]any{"type": "string"},
			"pin":                  map[string]any{"type": "boolean"},
			"emoji":                map[string]any{"type": "string"},
			"start_time":           map[string]any{"type": "integer", "description": "Reminder start Unix time in milliseconds"},
			"repeat":               map[string]any{"type": "integer"},
			"page":                 map[string]any{"type": "integer"},
			"count":                map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"reminder_id":          map[string]any{"type": "string"},
			"member_ids":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"user_id":              map[string]any{"type": "string"},
			"message":              map[string]any{"type": "string"},
			"link":                 map[string]any{"type": "string"},
			"duration_seconds":     map[string]any{"type": "integer", "description": "Mute duration in seconds; 0 means forever"},
			"bio":                  map[string]any{"type": "string"},
			"user_ids":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"avatar_size":          map[string]any{"type": "integer", "enum": []int{120, 180, 240}},
			"name":                 map[string]any{"type": "string"},
			"date_of_birth":        map[string]any{"type": "string", "description": "YYYY-MM-DD"},
			"gender":               map[string]any{"type": "integer", "enum": []int{0, 1}, "description": "0 male, 1 female"},
			"active":               map[string]any{"type": "boolean"},
			"message_id":           map[string]any{"type": "string"},
			"client_message_id":    map[string]any{"type": "string"},
			"message_sender_id":    map[string]any{"type": "string"},
			"message_type":         map[string]any{"type": "string"},
			"message_timestamp":    map[string]any{"type": "string"},
			"quoted_content":       map[string]any{"type": "string"},
			"message_property_ext": map[string]any{"type": "string", "description": "Internal Zalo propertyExt JSON for media quotes"},
			"message_attachment":   map[string]any{"type": "string", "description": "Internal Zalo attachment JSON for media quotes"},
			"message_ttl":          map[string]any{"type": "string", "description": "Internal Zalo TTL for native quotes"},
			"reaction":             map[string]any{"type": "string", "enum": []string{"LIKE", "HEART", "HAHA", "WOW", "CRY", "ANGRY", "DISLIKE", "LOVE"}},
		},
		"required": []string{"action"},
	}
}

func (t *ZaloPersonalTool) Execute(ctx context.Context, args map[string]any) *Result {
	if t.invoker == nil {
		return ErrorResult("zalo_personal: native action bridge is unavailable")
	}
	if ToolChannelTypeFromCtx(ctx) != "zalo_personal" {
		return ErrorResult("zalo_personal: tool is only available in a Zalo Personal conversation")
	}
	action, _ := args["action"].(string)
	if !slices.Contains(zaloPersonalActions, action) {
		return ErrorResult(fmt.Sprintf("zalo_personal: action %q is not allowed", action))
	}
	if zaloPersonalConfirmActions[action] {
		confirmed, _ := args["confirm"].(bool)
		if !confirmed {
			slog.Warn("security.zalo_personal_confirmation_required", "action", action)
			return ErrorResult(fmt.Sprintf("zalo_personal: action %q requires explicit user confirmation; ask the user, then retry with confirm=true", action))
		}
	}

	channel := ToolChannelFromCtx(ctx)
	if channel == "" {
		return ErrorResult("zalo_personal: current channel is missing")
	}
	params := make(map[string]any, len(args))
	for key, value := range args {
		if key != "action" && key != "confirm" {
			params[key] = value
		}
	}
	if action == "react" || action == "quote_message" {
		applyCurrentZaloMessageDefaults(ctx, params)
	}
	result, err := t.invoker(ctx, channel, channels.NativeActionRequest{
		Action: action, ChatID: ToolChatIDFromCtx(ctx), PeerKind: ToolPeerKindFromCtx(ctx), Params: params,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("zalo_personal: %s failed: %v", action, err))
	}
	data, err := json.Marshal(map[string]any{"action": action, "result": result})
	if err != nil {
		return ErrorResult(fmt.Sprintf("zalo_personal: encode result: %v", err))
	}
	return NewResult(string(data))
}

func applyCurrentZaloMessageDefaults(ctx context.Context, params map[string]any) {
	rc := store.RunContextFromCtx(ctx)
	if rc == nil {
		return
	}
	defaults := map[string]string{
		"message_id":           rc.MessageID,
		"client_message_id":    rc.ClientMessageID,
		"message_sender_id":    rc.SenderID,
		"message_type":         rc.MessageType,
		"message_timestamp":    rc.MessageTimestamp,
		"quoted_content":       rc.MessageContent,
		"message_property_ext": rc.MessageProperty,
		"message_attachment":   rc.MessageAttachment,
		"message_ttl":          rc.MessageTTL,
	}
	for key, value := range defaults {
		current, _ := params[key].(string)
		if value != "" && current == "" {
			params[key] = value
		}
	}
}
