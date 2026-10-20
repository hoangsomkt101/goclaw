package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func zaloToolContext() context.Context {
	ctx := context.Background()
	ctx = WithToolChannelType(ctx, "zalo_personal")
	ctx = WithToolChannel(ctx, "zalo-main")
	ctx = WithToolChatID(ctx, "group-1")
	return WithToolPeerKind(ctx, "group")
}

func TestZaloPersonalToolRequiresConfirmation(t *testing.T) {
	called := false
	tool := NewZaloPersonalTool()
	tool.SetNativeActionInvoker(func(context.Context, string, channels.NativeActionRequest) (any, error) {
		called = true
		return nil, nil
	})
	result := tool.Execute(zaloToolContext(), map[string]any{"action": "remove_group_members", "member_ids": []any{"123"}})
	if !result.IsError || !strings.Contains(result.ForLLM, "confirm=true") {
		t.Fatalf("result = %+v", result)
	}
	if called {
		t.Fatal("invoker called without confirmation")
	}
}

func TestZaloPersonalToolPassesCurrentConversation(t *testing.T) {
	tool := NewZaloPersonalTool()
	tool.SetNativeActionInvoker(func(_ context.Context, channel string, req channels.NativeActionRequest) (any, error) {
		if channel != "zalo-main" || req.ChatID != "group-1" || req.PeerKind != "group" || req.Action != "create_poll" {
			t.Fatalf("request = channel %q, %+v", channel, req)
		}
		return map[string]any{"poll_id": 7}, nil
	})
	result := tool.Execute(zaloToolContext(), map[string]any{
		"action": "create_poll", "question": "Q", "options": []any{"A", "B"},
	})
	if result.IsError || !strings.Contains(result.ForLLM, `"poll_id":7`) {
		t.Fatalf("result = %+v", result)
	}
}

func TestZaloPersonalToolRejectsUnknownAction(t *testing.T) {
	tool := NewZaloPersonalTool()
	tool.SetNativeActionInvoker(func(context.Context, string, channels.NativeActionRequest) (any, error) {
		t.Fatal("invoker must not be called")
		return nil, nil
	})
	result := tool.Execute(zaloToolContext(), map[string]any{"action": "raw_request"})
	if !result.IsError || !strings.Contains(result.ForLLM, "not allowed") {
		t.Fatalf("result = %+v", result)
	}
}

func TestZaloPersonalToolDefaultsReactionToCurrentMessage(t *testing.T) {
	tool := NewZaloPersonalTool()
	tool.SetNativeActionInvoker(func(_ context.Context, _ string, req channels.NativeActionRequest) (any, error) {
		if req.Params["message_id"] != "m-current" || req.Params["client_message_id"] != "c-current" {
			t.Fatalf("params = %#v", req.Params)
		}
		return map[string]any{"ok": true}, nil
	})
	ctx := store.WithRunContext(zaloToolContext(), &store.RunContext{
		MessageID: "m-current", ClientMessageID: "c-current", SenderID: "user-1",
		MessageType: "webchat", MessageTimestamp: "1700000000000", MessageContent: "hello",
		MessageProperty: `{"color":0}`, MessageAttachment: `{"href":"photo.jpg"}`, MessageTTL: "0",
	})
	result := tool.Execute(ctx, map[string]any{"action": "react", "reaction": "LIKE"})
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
}

func TestZaloPersonalToolDefaultsQuoteToCurrentMessage(t *testing.T) {
	tool := NewZaloPersonalTool()
	tool.SetNativeActionInvoker(func(_ context.Context, _ string, req channels.NativeActionRequest) (any, error) {
		want := map[string]string{
			"message_id": "m-current", "client_message_id": "c-current", "message_sender_id": "user-1",
			"message_type": "webchat", "message_timestamp": "1700000000000", "quoted_content": "hello",
			"message_property_ext": `{"color":0}`, "message_attachment": `{"href":"photo.jpg"}`, "message_ttl": "0",
		}
		for key, value := range want {
			if req.Params[key] != value {
				t.Fatalf("%s = %#v, want %q; params=%#v", key, req.Params[key], value, req.Params)
			}
		}
		return map[string]any{"message_id": "sent"}, nil
	})
	ctx := store.WithRunContext(zaloToolContext(), &store.RunContext{
		MessageID: "m-current", ClientMessageID: "c-current", SenderID: "user-1",
		MessageType: "webchat", MessageTimestamp: "1700000000000", MessageContent: "hello",
		MessageProperty: `{"color":0}`, MessageAttachment: `{"href":"photo.jpg"}`, MessageTTL: "0",
	})
	result := tool.Execute(ctx, map[string]any{"action": "quote_message", "message": "reply"})
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
}
