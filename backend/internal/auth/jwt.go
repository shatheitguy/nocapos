package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"alfaos/alfad/internal/store"
)

const (
	tokenIssuer   = "alfad"
	tokenAudience = "alfa-api"
)

type Claims struct {
	Username  string `json:"usr"`
	Role      string `json:"role"`
	SessionID string `json:"sid"` // refresh-token family; revoked on logout
	jwt.RegisteredClaims
}

type Issuer struct {
	key []byte
	ttl time.Duration
}

func NewIssuer(key []byte, ttl time.Duration) *Issuer {
	return &Issuer{key: key, ttl: ttl}
}

func (i *Issuer) Issue(u *store.User, sessionID string, now time.Time) (string, time.Time, error) {
	exp := now.Add(i.ttl)
	claims := Claims{
		Username:  u.Username,
		Role:      u.Role,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   u.ID,
			Audience:  jwt.ClaimStrings{tokenAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        store.NewID(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.key)
	return signed, exp, err
}

func (i *Issuer) Verify(token string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c,
		func(*jwt.Token) (any, error) { return i.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

// LoadOrCreateSecret reads the signing key at path, generating a 512-bit key
// with 0600 permissions on first run.
func LoadOrCreateSecret(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) < 32 {
			return nil, fmt.Errorf("%s: key too short", path)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key = make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}
