package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCreatePollRequestShape(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/poll/create" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := DecodeAESCBC(key, form.Get("params"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(plain, &captured); err != nil {
			t.Fatal(err)
		}
		inner := `{"error_code":0,"data":{"poll_id":77}}`
		encrypted, err := EncodeAESCBC(key, inner, false)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(Response[*string]{Data: &encrypted})
	}))
	defer server.Close()

	sess := &Session{
		IMEI: "imei-1", SecretKey: base64.StdEncoding.EncodeToString(key), Client: server.Client(),
		LoginInfo: &LoginInfo{ZpwServiceMapV3: ZpwServiceMapV3{Group: []string{server.URL}}},
	}
	result, err := CreatePoll(context.Background(), sess, "group-1", PollOptions{
		Question: "Ăn gì?", Options: []string{"Phở", "Cơm"}, AllowMultiChoices: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"poll_id":77}` {
		t.Fatalf("result = %s", result)
	}
	if captured["group_id"] != "group-1" || captured["question"] != "Ăn gì?" || captured["imei"] != "imei-1" {
		t.Fatalf("payload = %#v", captured)
	}
	if captured["allow_multi_choices"] != true || captured["poll_type"] != float64(0) {
		t.Fatalf("payload options = %#v", captured)
	}
}

func TestSendMessageWithMentionsRequestShape(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/group/mention" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		plain, err := DecodeAESCBC(key, r.Form.Get("params"))
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(plain, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["message"] != "Chào @Hải" || payload["grid"] != "group-1" {
			t.Errorf("payload = %#v", payload)
		}
		var mentions []Mention
		if err := json.Unmarshal([]byte(payload["mentionInfo"].(string)), &mentions); err != nil {
			t.Fatal(err)
		}
		if len(mentions) != 1 || mentions[0].UID != "123" || mentions[0].Pos != 5 || mentions[0].Len != 5 {
			t.Errorf("mentions = %+v", mentions)
		}
		inner := `{"error_code":0,"data":{"msgId":123}}`
		encrypted, err := EncodeAESCBC(key, inner, false)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(Response[*string]{Data: &encrypted})
	}))
	defer server.Close()

	sess := &Session{
		IMEI: "imei-1", SecretKey: base64.StdEncoding.EncodeToString(key), Client: server.Client(),
		LoginInfo: &LoginInfo{ZpwServiceMapV3: ZpwServiceMapV3{Group: []string{server.URL}}},
	}
	messageID, err := SendMessageWithMentions(context.Background(), sess, "group-1", ThreadTypeGroup,
		"Chào @Hải", []Mention{{Pos: 5, UID: "123", Len: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "123" {
		t.Fatalf("messageID = %q", messageID)
	}
}

func TestSendMessageWithQuoteReturnsPairedIDs(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/group/quote" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		plain, err := DecodeAESCBC(key, r.Form.Get("params"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(plain, &captured); err != nil {
			t.Fatal(err)
		}
		inner := `{"error_code":0,"data":{"msgId":456}}`
		encrypted, err := EncodeAESCBC(key, inner, false)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(Response[*string]{Data: &encrypted})
	}))
	defer server.Close()

	sess := &Session{
		IMEI: "imei-1", SecretKey: base64.StdEncoding.EncodeToString(key), Client: server.Client(),
		LoginInfo: &LoginInfo{ZpwServiceMapV3: ZpwServiceMapV3{Group: []string{server.URL}}},
	}
	result, err := SendMessageWithOptions(context.Background(), sess, "group-1", ThreadTypeGroup, "reply", nil, &MessageQuote{
		OwnerID: "user-1", MessageID: "7001", ClientMessageID: "8001", MessageType: "webchat",
		Timestamp: "1700000000000", Content: "original",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "456" || result.ClientMessageID == "" {
		t.Fatalf("result = %+v", result)
	}
	if captured["qmsgOwner"] != "user-1" || captured["qmsgId"] != "7001" || captured["qmsgCliId"] != "8001" {
		t.Fatalf("quote payload = %#v", captured)
	}
	if captured["qmsgType"] != float64(1) || captured["qmsg"] != "original" || captured["grid"] != "group-1" {
		t.Fatalf("quote payload = %#v", captured)
	}
	if fmt.Sprintf("%.0f", captured["clientId"].(float64)) != result.ClientMessageID {
		t.Fatalf("clientId payload=%v result=%q", captured["clientId"], result.ClientMessageID)
	}
}

func TestUndoMessageRequestShape(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/group/undomsg" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		plain, err := DecodeAESCBC(key, r.Form.Get("params"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(plain, &captured); err != nil {
			t.Fatal(err)
		}
		inner := `{"error_code":0,"data":{"status":1}}`
		encrypted, err := EncodeAESCBC(key, inner, false)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(Response[*string]{Data: &encrypted})
	}))
	defer server.Close()

	sess := &Session{
		IMEI: "imei-1", SecretKey: base64.StdEncoding.EncodeToString(key), Client: server.Client(),
		LoginInfo: &LoginInfo{ZpwServiceMapV3: ZpwServiceMapV3{Group: []string{server.URL}}},
	}
	if _, err := UndoMessage(context.Background(), sess, "group-1", ThreadTypeGroup, "456", "789"); err != nil {
		t.Fatal(err)
	}
	if captured["msgId"] != "456" || captured["cliMsgIdUndo"] != "789" || captured["grid"] != "group-1" || captured["imei"] != "imei-1" {
		t.Fatalf("undo payload = %#v", captured)
	}
}
