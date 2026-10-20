package personal

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

func TestSentMessageTrackingAndRecallSafety(t *testing.T) {
	channel := &Channel{sentMessages: make(map[string][]SentMessage)}
	channel.trackSentMessage("group-1", protocol.ThreadTypeGroup, "text", protocol.SendResult{
		MessageID: "m1", ClientMessageID: "c1",
	})
	channel.trackSentMessage("group-1", protocol.ThreadTypeGroup, "image", protocol.SendResult{
		MessageID: "m2", ClientMessageID: "c2",
	})

	latest, err := channel.findTrackedSentMessage("group-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if latest.MessageID != "m2" || latest.ClientMessageID != "c2" {
		t.Fatalf("latest = %+v", latest)
	}
	if _, err := channel.findTrackedSentMessage("group-1", "user-message", "user-client"); err == nil {
		t.Fatal("untracked message must not be recallable")
	}

	channel.markTrackedMessageRecalled("group-1", "m2", "c2")
	latest, err = channel.findTrackedSentMessage("group-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if latest.MessageID != "m1" {
		t.Fatalf("latest after recall = %+v", latest)
	}
}

func TestSentMessageTrackingIsBounded(t *testing.T) {
	channel := &Channel{sentMessages: make(map[string][]SentMessage)}
	for i := range maxTrackedSentMessagesPerChat + 5 {
		id := string(rune('a' + i))
		channel.trackSentMessage("chat-1", protocol.ThreadTypeUser, "text", protocol.SendResult{
			MessageID: id, ClientMessageID: "c" + id,
		})
	}
	items := channel.trackedSentMessages("chat-1")
	if len(items) != maxTrackedSentMessagesPerChat {
		t.Fatalf("tracked len = %d, want %d", len(items), maxTrackedSentMessagesPerChat)
	}
}

func TestQuoteFromMetadata(t *testing.T) {
	quote := quoteFromMetadata(map[string]string{
		metaZaloMessageID: "m1", metaZaloClientMessageID: "c1", metaZaloMessageSenderID: "u1",
		metaZaloMessageType: "webchat", metaZaloMessageTS: "1700000000000", metaZaloMessageContent: "hello",
		metaZaloMessageProperty: `{"color":0}`, metaZaloMessageAttach: `{"href":"photo.jpg","thumb":"thumb.jpg"}`,
		metaZaloMessageTTL: "10",
	})
	if quote == nil || quote.MessageID != "m1" || quote.ClientMessageID != "c1" || quote.OwnerID != "u1" || quote.Content != "hello" {
		t.Fatalf("quote = %+v", quote)
	}
	if quote.PropertyExt == nil || quote.Attachment == nil || quote.TTL != 10 {
		t.Fatalf("media quote metadata = %+v", quote)
	}
	if quoteFromMetadata(map[string]string{metaZaloMessageID: "m1"}) != nil {
		t.Fatal("incomplete quote metadata must be ignored")
	}
}
