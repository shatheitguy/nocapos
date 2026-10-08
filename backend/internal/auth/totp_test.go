package auth

import "testing"

func TestTOTPRFCVector(t *testing.T) {
	// RFC 6238 SHA-1 test: secret "12345678901234567890" at T=59s (counter 1)
	// -> 8-digit 94287082, 6-digit 287082.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	got, ok := totpCode(secret, 1)
	if !ok || got != "287082" {
		t.Fatalf("totpCode = %q ok=%v, want 287082", got, ok)
	}
}

func TestTOTPRoundTrip(t *testing.T) {
	secret := NewTOTPSecret()
	// The code for "now" must verify.
	counter := currentCounter()
	code, _ := totpCode(secret, counter)
	if !VerifyTOTP(secret, code) {
		t.Fatal("current code should verify")
	}
	if VerifyTOTP(secret, "000000") && code != "000000" {
		t.Skip("rare: random secret matched 000000")
	}
	if VerifyTOTP(secret, "12345") {
		t.Fatal("wrong-length code must fail")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("NoCapOS", "admin", "ABCDEF")
	if uri == "" || uri[:10] != "otpauth://" {
		t.Fatalf("bad uri: %q", uri)
	}
}
