package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

type argonParams struct {
	memory     uint32 // KiB
	iterations uint32
	threads    uint8
	keyLen     uint32
	saltLen    uint32
}

// 64 MiB / 3 passes follows OWASP guidance while staying usable on 1 GB ARM boards.
var params = argonParams{memory: 64 * 1024, iterations: 3, threads: 2, keyLen: 32, saltLen: 16}

// hashSlots bounds concurrent argon2 derivations so a login burst cannot
// exhaust memory on small devices.
var hashSlots = make(chan struct{}, 2)

var (
	b64        = base64.RawStdEncoding
	errBadHash = errors.New("malformed password hash")

	ErrWeakPassword    = errors.New("password must be 10-256 characters")
	ErrInvalidUsername = errors.New("username must be 3-32 characters: letters, digits, '.', '_' or '-'")

	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,31}$`)
)

// dummyHash is verified against when a username does not exist, so response
// timing does not reveal which accounts exist.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("alfa-timing-equalizer")
	return h
})

func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return ErrInvalidUsername
	}
	return nil
}

func ValidatePassword(p string) error {
	if len(p) < 10 || len(p) > 256 {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, params.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := derive(password, salt, params)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.memory, params.iterations, params.threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.threads); err != nil {
		return false, errBadHash
	}
	// Reject absurd parameters so a tampered row cannot DoS the host.
	if p.memory == 0 || p.memory > 1<<20 || p.iterations == 0 || p.iterations > 16 || p.threads == 0 {
		return false, errBadHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	p.keyLen = uint32(len(want))
	got := derive(password, salt, p)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func derive(password string, salt []byte, p argonParams) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.threads, p.keyLen)
}
