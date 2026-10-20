package channels

import "testing"

func TestCopyRoutingMeta_PreservesPancakeCommentRouting(t *testing.T) {
	src := map[string]string{
		"pancake_mode":        "comment",
		"reply_to_comment_id": "msg-123",
		"sender_id":           "user-123",
	}

	got := copyRoutingMeta(src)

	for key, want := range map[string]string{
		"pancake_mode":        "comment",
		"reply_to_comment_id": "msg-123",
		"sender_id":           "user-123",
	} {
		if got[key] != want {
			t.Fatalf("copyRoutingMeta()[%q] = %q, want %q", key, got[key], want)
		}
	}
}

func TestCopyFinalRoutingMeta_PreservesPlaceholderAndPancakeMode(t *testing.T) {
	src := map[string]string{
		"placeholder_key": "placeholder-123",
		"pancake_mode":    "comment",
	}

	got := CopyFinalRoutingMeta(src)

	if got["placeholder_key"] != "placeholder-123" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "placeholder_key", got["placeholder_key"], "placeholder-123")
	}
	if got["pancake_mode"] != "comment" {
		t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", "pancake_mode", got["pancake_mode"], "comment")
	}
}

func TestZaloQuoteMetadataIsFinalReplyOnly(t *testing.T) {
	src := map[string]string{
		"zalo_message_id": "m1", "zalo_client_message_id": "c1",
		"zalo_message_sender_id": "u1", "zalo_message_type": "webchat",
		"zalo_message_timestamp": "1700000000000", "zalo_message_content": "hello",
		"zalo_message_property_ext": `{"color":0}`, "zalo_message_attachment": `{"href":"photo.jpg"}`,
		"zalo_message_ttl": "0",
	}
	final := CopyFinalRoutingMeta(src)
	for key, value := range src {
		if final[key] != value {
			t.Fatalf("final[%q] = %q, want %q", key, final[key], value)
		}
	}
	if followup := CopyFollowupRoutingMeta(src); len(followup) != 0 {
		t.Fatalf("follow-up metadata must not repeat native quote: %#v", followup)
	}
}

// TestCopyRoutingMeta_PreservesPancakePrivateReplyKeys verifies the metadata
// keys used by the private_reply DM (post_id, display_name, sender_id)
// survive inbound→outbound copy.
func TestCopyRoutingMeta_PreservesPancakePrivateReplyKeys(t *testing.T) {
	src := map[string]string{
		"post_id":      "post-42",
		"display_name": "Tuấn",
		"sender_id":    "user-1",
	}

	got := copyRoutingMeta(src)
	for k, want := range src {
		if got[k] != want {
			t.Fatalf("copyRoutingMeta()[%q] = %q, want %q", k, got[k], want)
		}
	}

	final := CopyFinalRoutingMeta(src)
	for k, want := range src {
		if final[k] != want {
			t.Fatalf("CopyFinalRoutingMeta()[%q] = %q, want %q", k, final[k], want)
		}
	}
}
