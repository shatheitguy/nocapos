package devices

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestLoadIDIsStable(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^NC-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`).MatchString(id) {
		t.Fatalf("id %q", id)
	}
	again, err := LoadID(dir)
	if err != nil || again != id {
		t.Fatalf("second load: %q %v, want %q", again, err, id)
	}
	// A damaged file is replaced, not trusted.
	if err := os.WriteFile(filepath.Join(dir, idFile), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fresh, err := LoadID(dir); err != nil || fresh == "garbage" || !idRe.MatchString(fresh) {
		t.Fatalf("after damage: %q %v", fresh, err)
	}
}

func TestCodesSingleUseAndTTL(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	codes := NewCodesWithClock(c.now)

	code, exp := codes.Issue("u1")
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) || !exp.Equal(c.t.Add(CodeTTL)) {
		t.Fatalf("issued %q %v", code, exp)
	}
	if user, err := codes.Claim(code, "10.0.0.1"); err != nil || user != "u1" {
		t.Fatalf("claim: %q %v", user, err)
	}
	if _, err := codes.Claim(code, "10.0.0.1"); !errors.Is(err, ErrExpired) {
		t.Fatalf("second claim: %v", err)
	}

	code, _ = codes.Issue("u1")
	c.t = c.t.Add(CodeTTL + time.Second)
	if _, err := codes.Claim(code, "10.0.0.2"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired claim: %v", err)
	}

	// A new code replaces the person's previous one.
	old, _ := codes.Issue("u2")
	fresh, _ := codes.Issue("u2")
	if _, err := codes.Claim(old, "10.0.0.3"); !errors.Is(err, ErrExpired) {
		t.Fatalf("replaced code: %v", err)
	}
	if user, err := codes.Claim(fresh, "10.0.0.3"); err != nil || user != "u2" {
		t.Fatalf("fresh code: %q %v", user, err)
	}
}

func TestCodesWrongGuesses(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	codes := NewCodesWithClock(c.now)
	code, _ := codes.Issue("u1")
	wrong := "000000"
	if wrong == code {
		wrong = "000001"
	}
	for i := range MaxWrongGuesses {
		if _, err := codes.Claim(wrong, "10.0.0.9"); !errors.Is(err, ErrWrongCode) {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	// Blocked now, even with the right code.
	if !codes.Blocked("10.0.0.9") {
		t.Fatal("not blocked after 5 wrong guesses")
	}
	if _, err := codes.Claim(code, "10.0.0.9"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("right code while blocked: %v", err)
	}
	// Other clients are unaffected.
	if user, err := codes.Claim(code, "10.0.0.10"); err != nil || user != "u1" {
		t.Fatalf("other client: %q %v", user, err)
	}
	// The block lifts.
	c.t = c.t.Add(BlockFor + time.Second)
	if codes.Blocked("10.0.0.9") {
		t.Fatal("still blocked")
	}

	// IPv6 clients in one /64 share a budget.
	for range MaxWrongGuesses {
		_, _ = codes.Claim(wrong, "2001:db8::1")
	}
	if !codes.Blocked("2001:db8::ffff") {
		t.Fatal("same /64 not blocked")
	}
	if codes.Blocked("2001:db8:0:1::1") {
		t.Fatal("other /64 blocked")
	}
}

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{
		"Pixel 8": true, "": false, "   ": false, "a\nb": false, "tab\there": false,
		"Ünïcødé 📱": true, string(make([]byte, 65)): false,
	} {
		if ValidName(name) != ok {
			t.Errorf("ValidName(%q) = %v", name, !ok)
		}
	}
	if !ValidPlatform("android") || !ValidPlatform("ios") || !ValidPlatform("other") || ValidPlatform("windows") {
		t.Error("platforms")
	}
}
