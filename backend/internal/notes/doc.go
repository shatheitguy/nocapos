// Package notes holds the Sticky Notes document format: a note's body is a
// small structured document (never HTML), so whatever a client sends can only
// ever be drawn as text, bold, italic, links, bullet lists and checklists.
//
//	[
//	  {"type": "p",    "spans": [{"text": "Buy "}, {"text": "milk", "bold": true}]},
//	  {"type": "li",   "spans": [{"text": "a bullet"}]},
//	  {"type": "todo", "done": true, "spans": [{"text": "a checked item"}]},
//	  {"type": "p",    "spans": [{"text": "docs", "href": "https://example.org"}]}
//	]
package notes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Limits for one note and one person.
const (
	MaxNoteBytes  = 100 << 10 // title + body, as stored
	MaxNotes      = 5000      // per person, trash included
	MaxTitleRunes = 300
	MaxBlocks     = 5000
	MaxSpans      = 200 // per block
	MaxHrefLen    = 2048
)

// Block kinds.
const (
	Paragraph = "p"
	Bullet    = "li"
	Todo      = "todo"
)

// Colors a note can have (the clients draw them in NoCap's palette).
var Colors = []string{"glass", "red", "ember", "sun", "mint", "sky", "grape", "ink"}

// ValidColor reports whether c is one of Colors.
func ValidColor(c string) bool {
	for _, x := range Colors {
		if x == c {
			return true
		}
	}
	return false
}

// Span is a run of text with the same marks.
type Span struct {
	Text   string `json:"text"`
	Bold   bool   `json:"bold,omitempty"`
	Italic bool   `json:"italic,omitempty"`
	Href   string `json:"href,omitempty"`
}

// Block is one line: a paragraph, a bullet or a checklist item.
type Block struct {
	Type  string `json:"type"`
	Done  bool   `json:"done,omitempty"`
	Spans []Span `json:"spans"`
}

var (
	ErrBadBody  = errors.New("bad note body")
	ErrTooLarge = errors.New("note too large")
)

// Parse checks a body sent by a client and returns it cleaned up: unknown
// fields dropped, empty spans removed, neighbouring spans with the same marks
// merged, links limited to http(s) and mailto. An empty or null body is an
// empty document.
func Parse(raw []byte) ([]Block, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return []Block{}, nil
	}
	if len(raw) > MaxNoteBytes*2 { // escapes can make JSON bigger than its text
		return nil, ErrTooLarge
	}
	var in []Block
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadBody, err)
	}
	if len(in) > MaxBlocks {
		return nil, fmt.Errorf("%w: more than %d lines", ErrTooLarge, MaxBlocks)
	}
	out := make([]Block, 0, len(in))
	for i, b := range in {
		switch b.Type {
		case Paragraph, Bullet, Todo:
		case "":
			b.Type = Paragraph
		default:
			return nil, fmt.Errorf("%w: line %d has unknown type %q", ErrBadBody, i+1, b.Type)
		}
		if b.Type != Todo {
			b.Done = false
		}
		if len(b.Spans) > MaxSpans {
			return nil, fmt.Errorf("%w: line %d has too many pieces", ErrTooLarge, i+1)
		}
		spans := make([]Span, 0, len(b.Spans))
		for _, s := range b.Spans {
			if !utf8.ValidString(s.Text) {
				return nil, fmt.Errorf("%w: text isn't UTF-8", ErrBadBody)
			}
			s.Text = cleanText(s.Text)
			if s.Text == "" {
				continue
			}
			if s.Href != "" {
				h, ok := SafeHref(s.Href)
				if !ok {
					return nil, fmt.Errorf("%w: links must start with http://, https:// or mailto:", ErrBadBody)
				}
				s.Href = h
			}
			if n := len(spans); n > 0 && sameMarks(spans[n-1], s) {
				spans[n-1].Text += s.Text
				continue
			}
			spans = append(spans, s)
		}
		b.Spans = spans
		out = append(out, b)
	}
	return out, nil
}

func sameMarks(a, b Span) bool { return a.Bold == b.Bold && a.Italic == b.Italic && a.Href == b.Href }

// cleanText drops control characters (a line is one line: no newlines).
func cleanText(s string) string {
	if strings.IndexFunc(s, isControl) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }

// SafeHref returns a cleaned link if it is http(s) or mailto.
func SafeHref(h string) (string, bool) {
	h = strings.TrimSpace(h)
	if h == "" || len(h) > MaxHrefLen || strings.IndexFunc(h, isControl) >= 0 {
		return "", false
	}
	u, err := url.Parse(h)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return "", false
		}
	case "mailto":
		if u.Opaque == "" {
			return "", false
		}
	default:
		return "", false
	}
	return h, true
}

// CleanTitle trims a title to one line of at most MaxTitleRunes.
func CleanTitle(t string) (string, error) {
	if !utf8.ValidString(t) {
		return "", fmt.Errorf("%w: title isn't UTF-8", ErrBadBody)
	}
	t = strings.TrimSpace(cleanText(t))
	if utf8.RuneCountInString(t) > MaxTitleRunes {
		return "", fmt.Errorf("%w: titles are at most %d characters", ErrTooLarge, MaxTitleRunes)
	}
	return t, nil
}

// Encode is the stored form of a body.
func Encode(blocks []Block) string {
	if blocks == nil {
		blocks = []Block{}
	}
	b, _ := json.Marshal(blocks)
	return string(b)
}

// PlainText is a body as plain lines ("- " bullets, "[ ] " / "[x] " todos),
// used for search, phone widgets and notification text.
func PlainText(blocks []Block) string {
	var sb strings.Builder
	for i, b := range blocks {
		if i > 0 {
			sb.WriteByte('\n')
		}
		switch b.Type {
		case Bullet:
			sb.WriteString("- ")
		case Todo:
			if b.Done {
				sb.WriteString("[x] ")
			} else {
				sb.WriteString("[ ] ")
			}
		}
		for _, s := range b.Spans {
			sb.WriteString(s.Text)
		}
	}
	return sb.String()
}

// Snippet is the first non-empty line of text, at most n characters.
func Snippet(title string, blocks []Block, n int) string {
	if title != "" {
		return cut(title, n)
	}
	for _, b := range blocks {
		var sb strings.Builder
		for _, s := range b.Spans {
			sb.WriteString(s.Text)
		}
		if t := strings.TrimSpace(sb.String()); t != "" {
			return cut(t, n)
		}
	}
	return ""
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Desk is where a note sticks on the web desktop (pixels from the top left).
type Desk struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Clamp keeps a desk spot sensible: on screen-ish and a usable size.
func (d Desk) Clamp() Desk {
	d.X = min(max(d.X, -2000), 20000)
	d.Y = min(max(d.Y, 0), 20000)
	d.W = min(max(d.W, 160), 900)
	d.H = min(max(d.H, 120), 900)
	return d
}
