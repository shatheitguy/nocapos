package auth

import (
	"testing"
	"time"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("correct horse battery", h)
	if err != nil || !ok {
		t.Fatalf("expected match, got ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong password!!", h)
	if err != nil || ok {
		t.Fatalf("expected mismatch, got ok=%v err=%v", ok, err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, h := range []string{
		"",
		"$bcrypt$x",
		"$argon2id$v=19$m=99999999,t=3,p=2$c2FsdA$aGFzaA", // memory above cap
		"$argon2id$v=19$m=65536,t=3,p=2$!!$aGFzaA",
	} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("expected error for %q", h)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(time.Hour, 3) // no meaningful refill during the test
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatalf("request %d should pass", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("4th request should be limited")
	}
	if !l.Allow("b") {
		t.Fatal("other keys are independent")
	}
}
