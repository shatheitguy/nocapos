package notes

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCleansUp(t *testing.T) {
	got, err := Parse([]byte(`[
		{"type":"p","spans":[{"text":"Buy "},{"text":"milk"},{"text":"","bold":true},{"text":"now","bold":true,"extra":1}]},
		{"type":"","spans":[{"text":"a\u0007b\tc"}]},
		{"type":"li","done":true,"spans":[{"text":"x"}]},
		{"type":"todo","done":true,"spans":[{"text":"site","href":" https://example.org/a?b=1 "}]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("blocks: %+v", got)
	}
	if s := got[0].Spans; len(s) != 2 || s[0].Text != "Buy milk" || s[1].Text != "now" || !s[1].Bold {
		t.Fatalf("merge: %+v", s)
	}
	if got[1].Type != Paragraph || got[1].Spans[0].Text != "ab c" {
		t.Fatalf("control chars: %+v", got[1])
	}
	if got[2].Done {
		t.Fatal("only todos are done")
	}
	if got[3].Spans[0].Href != "https://example.org/a?b=1" || !got[3].Done {
		t.Fatalf("link: %+v", got[3])
	}
	if PlainText(got) != "Buy milknow\nab c\n- x\n[x] site" {
		t.Fatalf("plain: %q", PlainText(got))
	}
	if Snippet("", got, 5) != "Buy …" {
		t.Fatalf("snippet: %q", Snippet("", got, 5))
	}
}

func TestParseRejects(t *testing.T) {
	for _, body := range []string{
		`{"type":"p"}`,
		`[{"type":"h1","spans":[]}]`,
		`[{"type":"p","spans":[{"text":"x","href":"javascript:alert(1)"}]}]`,
		`[{"type":"p","spans":[{"text":"x","href":"data:text/html,hi"}]}]`,
		`[{"type":"p","spans":[{"text":"x","href":"//evil.example"}]}]`,
		`[{"type":"p","spans":[{"text":"x","href":"http:///nohost"}]}]`,
		`[{"type":"p","spans":[{"text":"<b>x</b>","html":"<script>"}]}]x`,
	} {
		if _, err := Parse([]byte(body)); !errors.Is(err, ErrBadBody) {
			t.Errorf("%s: %v", body, err)
		}
	}
	// HTML is just text: it is never interpreted.
	b, err := Parse([]byte(`[{"type":"p","spans":[{"text":"<script>alert(1)</script>"}]}]`))
	if err != nil || b[0].Spans[0].Text != "<script>alert(1)</script>" {
		t.Fatalf("html as text: %+v %v", b, err)
	}
	many := "[" + strings.Repeat(`{"type":"p","spans":[]},`, MaxBlocks) + `{"type":"p","spans":[]}]`
	if _, err := Parse([]byte(many)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too many blocks: %v", err)
	}
	for _, empty := range []string{"", "null", "  []  "} {
		if b, err := Parse([]byte(empty)); err != nil || len(b) != 0 {
			t.Fatalf("%q: %v %v", empty, b, err)
		}
	}
}

func TestSafeHref(t *testing.T) {
	for h, ok := range map[string]bool{
		"https://nocap.local/x": true, "HTTP://A.B": true, "mailto:me@example.org": true,
		"mailto:": false, "ftp://x": false, "javascript:void(0)": false, "file:///etc/passwd": false,
		"https://a.b/" + strings.Repeat("x", MaxHrefLen): false,
	} {
		if _, got := SafeHref(h); got != ok {
			t.Errorf("%.40s: %v", h, got)
		}
	}
}

func TestTitleAndDesk(t *testing.T) {
	if s, err := CleanTitle("  Groceries\n "); err != nil || s != "Groceries" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := CleanTitle(strings.Repeat("é", MaxTitleRunes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if d := (Desk{X: -99999, Y: -5, W: 10, H: 5000}).Clamp(); d != (Desk{X: -2000, Y: 0, W: 160, H: 900}) {
		t.Fatalf("%+v", d)
	}
	for _, c := range Colors {
		if !ValidColor(c) {
			t.Fatal(c)
		}
	}
	if ValidColor("yellow") {
		t.Fatal("yellow")
	}
}
