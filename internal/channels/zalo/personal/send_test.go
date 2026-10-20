package personal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo"
	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
)

func TestParseOutboundMentions(t *testing.T) {
	text, mentions := parseOutboundMentions("**Chào** @[Hải Anh](12345) và @[😀](67890)!")
	if text != "Chào @Hải Anh và @😀!" {
		t.Fatalf("text = %q", text)
	}
	if len(mentions) != 2 {
		t.Fatalf("mentions = %d, want 2", len(mentions))
	}
	if mentions[0].UID != "12345" || sliceUTF16(text, mentions[0].Start, mentions[0].End) != "@Hải Anh" {
		t.Fatalf("first mention = %+v", mentions[0])
	}
	if mentions[1].Start != 17 {
		t.Fatalf("second mention UTF-16 position = %d, want 17", mentions[1].Start)
	}
	if got := mentions[1].End - mentions[1].Start; got != 3 {
		t.Fatalf("emoji mention UTF-16 length = %d, want 3", got)
	}
}

func TestFormatOutboundMessageKeepsMentionAndStyleOffsets(t *testing.T) {
	formatted, mentions := formatOutboundMessage("😀 **Chào @[Hải Anh](12345)**")
	if formatted.Text != "😀 Chào @Hải Anh" || len(mentions) != 1 {
		t.Fatalf("formatted=%+v mentions=%+v", formatted, mentions)
	}
	if mentions[0].Start != 8 || mentions[0].End != 16 {
		t.Fatalf("mention = %+v", mentions[0])
	}
	if len(formatted.Styles) != 1 || formatted.Styles[0].Start != 3 || formatted.Styles[0].Len != 13 {
		t.Fatalf("styles = %+v", formatted.Styles)
	}
}

func sliceUTF16(value string, start, end int) string {
	startByte, endByte := 0, len(value)
	units := 0
	for index, r := range value {
		if units == start {
			startByte = index
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		units += width
		if units == end {
			endByte = index + len(string(r))
			break
		}
	}
	return value[startByte:endByte]
}

func TestParseOutboundMentionsPlainMarkdown(t *testing.T) {
	text, mentions := parseOutboundMentions("**hello** [site](https://example.com)")
	if text != "hello site (https://example.com)" || len(mentions) != 0 {
		t.Fatalf("got text=%q mentions=%v", text, mentions)
	}
}

func TestSendChunkedTextFallsBackAfterNativeFormattingRejection(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	requests := make([]map[string]any, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := protocol.DecodeAESCBC(key, form.Get("params"))
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(plain, &payload); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, payload)
		if len(requests) == 1 {
			_ = json.NewEncoder(w).Encode(protocol.Response[*string]{ErrorCode: -201, ErrorMessage: "format rejected"})
			return
		}
		inner := `{"error_code":0,"data":{"msgId":987}}`
		encrypted, err := protocol.EncodeAESCBC(key, inner, false)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Response[*string]{Data: &encrypted})
	}))
	defer server.Close()

	sess := &protocol.Session{
		IMEI: "imei-1", SecretKey: base64.StdEncoding.EncodeToString(key), Client: server.Client(),
		LoginInfo: &protocol.LoginInfo{ZpwServiceMapV3: protocol.ZpwServiceMapV3{Chat: []string{server.URL}}},
	}
	channel := &Channel{sentMessages: make(map[string][]SentMessage)}
	if err := channel.sendChunkedText(context.Background(), sess, "user-1", protocol.ThreadTypeUser,
		zalo.FormatMarkdown("**hello**"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want native attempt plus plain fallback", len(requests))
	}
	if _, ok := requests[0]["textProperties"]; !ok {
		t.Fatal("first request has no native textProperties")
	}
	if _, ok := requests[1]["textProperties"]; ok {
		t.Fatal("plain fallback still contains textProperties")
	}
	tracked := channel.trackedSentMessages("user-1")
	if len(tracked) != 1 || tracked[0].MessageID != "987" {
		t.Fatalf("tracked = %+v", tracked)
	}
}

func TestSendChunkedTextDoesNotFallbackAfterAmbiguousTransportResponse(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	sess := &protocol.Session{
		IMEI: "imei-1", SecretKey: base64.StdEncoding.EncodeToString(key), Client: server.Client(),
		LoginInfo: &protocol.LoginInfo{ZpwServiceMapV3: protocol.ZpwServiceMapV3{Chat: []string{server.URL}}},
	}
	channel := &Channel{sentMessages: make(map[string][]SentMessage)}
	err := channel.sendChunkedText(context.Background(), sess, "user-1", protocol.ThreadTypeUser,
		zalo.FormatMarkdown("**hello**"), nil, nil)
	if err == nil {
		t.Fatal("expected response parsing error")
	}
	if requestCount != 1 {
		t.Fatalf("requests = %d, ambiguous failure must not be retried", requestCount)
	}
}
