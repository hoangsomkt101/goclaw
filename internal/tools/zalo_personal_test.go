package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
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
