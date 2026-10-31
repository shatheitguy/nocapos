// Package devices links phones and other devices to a NoCapOS server: the
// server's stable NoCap ID, short-lived pairing codes and device tokens.
package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---------- NoCap ID ----------

// idAlphabet has no look-alikes (0/O, 1/I).
const idAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var idRe = regexp.MustCompile(`^NC-[` + idAlphabet + `]{4}-[` + idAlphabet + `]{4}$`)

const idFile = "nocap-id"

// LoadID returns the server's NoCap ID, kept in dataDir/nocap-id. The first
// call makes one; after that it never changes.
func LoadID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, idFile)
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); idRe.MatchString(id) {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	id := "NC-" + randString(idAlphabet, 4) + "-" + randString(idAlphabet, 4)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return id, nil
}

func randString(alphabet string, n int) string {
	var b strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

// ---------- tokens ----------

// NewToken returns a device token (32 random bytes, base64url) and the hash
// to store. The token itself is shown to the device once and never kept.
func NewToken() (raw, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw)
}

// HashToken is the SHA-256 (hex) of a device token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ---------- validation ----------

var platforms = map[string]bool{"android": true, "ios": true, "other": true}

// ValidPlatform reports whether p is android, ios or other.
func ValidPlatform(p string) bool { return platforms[p] }

// ValidName: 1-64 characters, valid UTF-8, no control characters.
func ValidName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 64 || !utf8.ValidString(name) || strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}

// ValidAppVersion: up to 32 printable characters (may be empty).
func ValidAppVersion(v string) bool {
	if len(v) > 32 || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ---------- pairing codes ----------

const (
	CodeTTL = 5 * time.Minute
	// MaxWrongGuesses from one client before it is blocked for BlockFor.
	MaxWrongGuesses = 5
	BlockFor        = 15 * time.Minute
	// Server-wide brake: this many wrong codes inside globalWindow pauses
	// pairing for everyone for globalWindow (spreading guesses over many
	// addresses doesn't get far either).
	globalMaxWrong = 200
	globalWindow   = 5 * time.Minute
	// Used and expired codes are remembered this long, so they get a clear
	// "expired" answer rather than "wrong".
	keepSpent = 30 * time.Minute
)

var (
	ErrWrongCode = errors.New("wrong pairing code")
	ErrExpired   = errors.New("pairing code expired or already used")
	ErrBlocked   = errors.New("too many wrong pairing codes")
)

type code struct {
	userID string
	exp    time.Time
	used   bool
}

type guesses struct {
	n       int
	first   time.Time
	blocked time.Time // blocked until
}

// Codes holds the live pairing codes, in memory only.
type Codes struct {
	mu        sync.Mutex
	now       func() time.Time
	codes     map[string]*code
	wrong     map[string]*guesses
	global    []time.Time // recent wrong guesses, server-wide
	pausedTil time.Time
}

func NewCodes() *Codes { return NewCodesWithClock(time.Now) }

// NewCodesWithClock is for tests.
func NewCodesWithClock(now func() time.Time) *Codes {
	return &Codes{now: now, codes: map[string]*code{}, wrong: map[string]*guesses{}}
}

// Issue makes a fresh 6-digit code for userID. Any earlier code of theirs
// that is still live stops working, so each person has at most one.
func (c *Codes) Issue(userID string) (string, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.sweep(now)
	for _, v := range c.codes {
		if v.userID == userID && !v.used && now.Before(v.exp) {
			v.used = true
		}
	}
	for {
		s := randString("0123456789", 6)
		if _, taken := c.codes[s]; taken {
			continue
		}
		exp := now.Add(CodeTTL)
		c.codes[s] = &code{userID: userID, exp: exp}
		return s, exp
	}
}

// Claim spends a code and returns the user it belongs to. client is the
// caller's address; wrong or spent codes count against it.
func (c *Codes) Claim(s, client string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.sweep(now)
	key := clientKey(client)
	if g := c.wrong[key]; g != nil && now.Before(g.blocked) {
		return "", ErrBlocked
	}
	if now.Before(c.pausedTil) {
		return "", ErrBlocked
	}
	v, ok := c.codes[s]
	switch {
	case ok && !v.used && now.Before(v.exp):
		v.used = true
		return v.userID, nil
	case ok:
		c.miss(key, now)
		return "", ErrExpired
	default:
		c.miss(key, now)
		return "", ErrWrongCode
	}
}

// Blocked reports whether a client may not try codes right now.
func (c *Codes) Blocked(client string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	g := c.wrong[clientKey(client)]
	return (g != nil && now.Before(g.blocked)) || now.Before(c.pausedTil)
}

func (c *Codes) miss(key string, now time.Time) {
	g := c.wrong[key]
	if g == nil || now.Sub(g.first) > BlockFor {
		g = &guesses{first: now}
		c.wrong[key] = g
	}
	g.n++
	if g.n >= MaxWrongGuesses {
		g.blocked = now.Add(BlockFor)
		g.n, g.first = 0, now
	}
	c.global = append(c.global, now)
	if len(c.global) >= globalMaxWrong {
		c.pausedTil = now.Add(globalWindow)
		c.global = c.global[:0]
	}
}

func (c *Codes) sweep(now time.Time) {
	for k, v := range c.codes {
		if now.Sub(v.exp) > keepSpent {
			delete(c.codes, k)
		}
	}
	for k, g := range c.wrong {
		if now.After(g.blocked) && now.Sub(g.first) > BlockFor {
			delete(c.wrong, k)
		}
	}
	i := 0
	for i < len(c.global) && now.Sub(c.global[i]) > globalWindow {
		i++
	}
	c.global = c.global[i:]
}

// clientKey groups IPv6 clients by /64, since one host usually holds a whole
// /64 and could otherwise rotate through addresses.
func clientKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Is4() || a.Is4In6() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}
