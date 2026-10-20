package personal

import "testing"

func TestParseOutboundMentions(t *testing.T) {
	text, mentions := parseOutboundMentions("**Chào** @[Hải Anh](12345) và @[😀](67890)!")
	if text != "Chào @Hải Anh và @😀!" {
		t.Fatalf("text = %q", text)
	}
	if len(mentions) != 2 {
		t.Fatalf("mentions = %d, want 2", len(mentions))
	}
	if mentions[0].UID != "12345" || text[mentions[0].Start:mentions[0].End] != "@Hải Anh" {
		t.Fatalf("first mention = %+v", mentions[0])
	}
	if got := utf16Length(text[:mentions[1].Start]); got != 17 {
		t.Fatalf("second mention UTF-16 position = %d, want 17", got)
	}
	if got := utf16Length(text[mentions[1].Start:mentions[1].End]); got != 3 {
		t.Fatalf("emoji mention UTF-16 length = %d, want 3", got)
	}
}

func TestParseOutboundMentionsPlainMarkdown(t *testing.T) {
	text, mentions := parseOutboundMentions("**hello** [site](https://example.com)")
	if text != "hello site (https://example.com)" || len(mentions) != 0 {
		t.Fatalf("got text=%q mentions=%v", text, mentions)
	}
}
