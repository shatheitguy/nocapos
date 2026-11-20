package sshd

import (
	"bytes"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

// MinRSABits is the smallest RSA key accepted.
const MinRSABits = 3072

// MaxKeyName is the longest name a key may have.
const MaxKeyName = 64

// maxAuthorizedKeys is the largest authorized_keys file NoCapOS reads.
const maxAuthorizedKeys = 1 << 20

// keyTypes are the public key types accepted, by their name in the key.
var keyTypes = map[string]bool{
	ssh.KeyAlgoED25519:   true,
	ssh.KeyAlgoECDSA256:  true,
	ssh.KeyAlgoECDSA384:  true,
	ssh.KeyAlgoECDSA521:  true,
	ssh.KeyAlgoRSA:       true,
	ssh.KeyAlgoSKED25519: true,
	ssh.KeyAlgoSKECDSA256: true,
}

var (
	// ErrDuplicate: that key is already there.
	ErrDuplicate = errors.New("that key is already added")
	// ErrNoKey: there's no key with that fingerprint.
	ErrNoKey = errors.New("there's no such key")
)

// Key is one public key in a person's authorized_keys.
type Key struct {
	Type        string    `json:"type"`
	Bits        int       `json:"bits,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	Name        string    `json:"name"`
	Added       time.Time `json:"added,omitempty"`
	// Options: the line carries options (added outside NoCapOS).
	Options bool `json:"options,omitempty"`
}

// NewKey is a pasted key, checked and ready to add.
type NewKey struct {
	Key
	line string // "type base64 name"
}

// ParseKey checks one pasted public key: a single line of
// "type base64 [comment]" with an accepted type, no options, and an RSA key
// of at least MinRSABits. name overrides the key's own comment.
func ParseKey(text, name string) (NewKey, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, string(rune(0xFEFF))))
	if text == "" {
		return NewKey{}, invalid("paste a public key (it starts with ssh-ed25519, ecdsa-sha2 or ssh-rsa)")
	}
	if strings.ContainsAny(text, "\r\n\x00") {
		return NewKey{}, invalid("paste one key at a time, on one line")
	}
	if strings.Contains(text, "PRIVATE KEY") {
		return NewKey{}, invalid("that's a private key: never share it. Paste the .pub file instead")
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return NewKey{}, invalid("that doesn't look like a public key")
	}
	typ, data := fields[0], fields[1]
	if !keyTypes[typ] {
		if typ == ssh.KeyAlgoDSA {
			return NewKey{}, invalid("DSA keys are too weak; make an ed25519 key instead")
		}
		return NewKey{}, invalid("the key must start with ssh-ed25519, ecdsa-sha2-nistp256/384/521 or ssh-rsa (no options in front)")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return NewKey{}, invalid("the key's data is damaged (it isn't valid base64)")
	}
	pk, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return NewKey{}, invalid("the key's data is damaged")
	}
	if pk.Type() != typ {
		return NewKey{}, invalid("the key says it's %s but holds a %s key", typ, pk.Type())
	}
	bits := 0
	if typ == ssh.KeyAlgoRSA {
		ck, ok := pk.(ssh.CryptoPublicKey)
		if !ok {
			return NewKey{}, invalid("the key's data is damaged")
		}
		rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey)
		if !ok {
			return NewKey{}, invalid("the key's data is damaged")
		}
		bits = rk.N.BitLen()
		if bits < MinRSABits {
			return NewKey{}, invalid("this RSA key is %d bits; use %d or more, or better an ed25519 key", bits, MinRSABits)
		}
	}
	if strings.TrimSpace(name) == "" && len(fields) > 2 {
		name = strings.Join(fields[2:], " ")
	}
	name, err = cleanName(name)
	if err != nil {
		return NewKey{}, err
	}
	if name == "" {
		name = "key"
	}
	k := Key{Type: typ, Bits: bits, Fingerprint: ssh.FingerprintSHA256(pk), Name: name}
	return NewKey{Key: k, line: typ + " " + base64.StdEncoding.EncodeToString(pk.Marshal()) + " " + name}, nil
}

// cleanName checks a key's name: printable, one line, MaxKeyName at most.
func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if len([]rune(name)) > MaxKeyName {
		return "", invalid("the name is too long (%d characters at most)", MaxKeyName)
	}
	for _, c := range name {
		if !unicode.IsPrint(c) {
			return "", invalid("the name can't contain control characters")
		}
	}
	return name, nil
}

// parsedLine is one line of an authorized_keys file.
type parsedLine struct {
	text string
	key  *Key // nil: a comment, a blank line or something unreadable
}

func parseFile(b []byte) []parsedLine {
	var out []parsedLine
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if len(b) == 0 {
			break
		}
		pl := parsedLine{text: strings.TrimRight(l, "\r")}
		t := strings.TrimSpace(pl.text)
		if t != "" && !strings.HasPrefix(t, "#") {
			if pk, comment, opts, _, err := ssh.ParseAuthorizedKey([]byte(t)); err == nil {
				k := Key{Type: pk.Type(), Fingerprint: ssh.FingerprintSHA256(pk), Name: comment, Options: len(opts) > 0}
				if ck, ok := pk.(ssh.CryptoPublicKey); ok {
					if rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey); ok {
						k.Bits = rk.N.BitLen()
					}
				}
				pl.key = &k
			}
		}
		out = append(out, pl)
	}
	return out
}

// ListKeys reads the keys in an authorized_keys file's contents.
func ListKeys(b []byte) []Key {
	out := []Key{}
	for _, l := range parseFile(b) {
		if l.key != nil {
			out = append(out, *l.key)
		}
	}
	return out
}

// AddKey returns the file's contents with k added at the end. Every other
// line stays as it was.
func AddKey(b []byte, k NewKey) ([]byte, error) {
	if k.line == "" || strings.ContainsAny(k.line, "\r\n") {
		return nil, invalid("not a checked key")
	}
	for _, l := range parseFile(b) {
		if l.key != nil && l.key.Fingerprint == k.Fingerprint {
			return nil, ErrDuplicate
		}
	}
	out := append([]byte(nil), b...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, k.line+"\n"...), nil
}

// RemoveKey returns the file's contents without the key with fingerprint
// fp (every line holding it).
func RemoveKey(b []byte, fp string) ([]byte, error) {
	var keep []string
	found := false
	for _, l := range parseFile(b) {
		if l.key != nil && l.key.Fingerprint == fp {
			found = true
			continue
		}
		keep = append(keep, l.text)
	}
	if !found {
		return nil, ErrNoKey
	}
	if len(keep) == 0 {
		return []byte{}, nil
	}
	return []byte(strings.Join(keep, "\n") + "\n"), nil
}

// ---------- the files ----------

// Account is a Linux account's numbers and home folder.
type Account struct {
	UID, GID int
	Home     string
}

func lookupAccount(username string) (Account, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return Account{}, err
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return Account{}, errors.New("that account has no Linux user ID")
	}
	return Account{UID: uid, GID: gid, Home: u.HomeDir}, nil
}

// paths finds a person's .ssh folder and authorized_keys, refusing links
// (root writing through a link the person made could change any file).
func (m *Manager) paths(username string) (Account, string, string, error) {
	if !nameRe.MatchString(username) {
		return Account{}, "", "", invalid("not an account name")
	}
	if username == "root" {
		return Account{}, "", "", invalid("root can't log in over SSH, so it has no keys here")
	}
	a, err := m.Lookup(username)
	if err != nil {
		return Account{}, "", "", fmt.Errorf("the Linux account %s: %w", username, err)
	}
	if a.UID == 0 {
		return Account{}, "", "", invalid("root can't log in over SSH, so it has no keys here")
	}
	if a.Home == "" || !filepath.IsAbs(a.Home) {
		return Account{}, "", "", errors.New("that account has no home folder")
	}
	st, err := os.Lstat(a.Home)
	if err != nil {
		return Account{}, "", "", fmt.Errorf("that account's home folder: %w", err)
	}
	if !st.IsDir() {
		return Account{}, "", "", errors.New("that account's home folder isn't a folder")
	}
	dir := filepath.Join(a.Home, ".ssh")
	return a, dir, filepath.Join(dir, "authorized_keys"), nil
}

func notALink(p string) error {
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a link; NoCapOS won't follow it", p)
	}
	return nil
}

// readKeys reads a person's authorized_keys ("" when there is none).
func (m *Manager) readKeys(file string) ([]byte, error) {
	if err := notALink(filepath.Dir(file)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err := notALink(file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxAuthorizedKeys+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxAuthorizedKeys {
		return nil, errors.New("that authorized_keys file is too big")
	}
	return b, nil
}

// writeKeys replaces a person's authorized_keys: .ssh is theirs with 0700,
// the file theirs with 0600, written to a new file and renamed into place.
func (m *Manager) writeKeys(a Account, dir, file string, b []byte) error {
	if st, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("%s isn't a plain folder; NoCapOS won't write through it", dir)
	}
	if err := m.Chown(dir, a.UID, a.GID); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := notALink(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".authorized_keys.nocap-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	if err := m.Chown(name, a.UID, a.GID); err != nil {
		return err
	}
	return os.Rename(name, file)
}

// ---------- key notes: when each key was added ----------

type keyNote struct {
	Name  string    `json:"name"`
	Added time.Time `json:"added"`
}

func (m *Manager) notesPath() string { return filepath.Join(m.Data, "ssh-keys.json") }

func (m *Manager) readNotes() map[string]map[string]keyNote {
	out := map[string]map[string]keyNote{}
	if b, err := os.ReadFile(m.notesPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (m *Manager) writeNotes(n map[string]map[string]keyNote) error {
	if err := os.MkdirAll(m.Data, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(n, "", "  ")
	return writeAtomic(m.notesPath(), b, 0o600)
}

// Keys lists a person's keys, with the names and dates NoCapOS noted.
func (m *Manager) Keys(username string) ([]Key, error) {
	_, _, file, err := m.paths(username)
	if err != nil {
		return nil, err
	}
	b, err := m.readKeys(file)
	if err != nil {
		return nil, err
	}
	keys := ListKeys(b)
	notes := m.readNotes()[username]
	for i, k := range keys {
		if n, ok := notes[k.Fingerprint]; ok {
			if n.Name != "" {
				keys[i].Name = n.Name
			}
			keys[i].Added = n.Added
		}
	}
	return keys, nil
}

// AddKey adds a checked key to a person's authorized_keys.
func (m *Manager) AddKey(username string, k NewKey, now time.Time) (Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, dir, file, err := m.paths(username)
	if err != nil {
		return Key{}, err
	}
	b, err := m.readKeys(file)
	if err != nil {
		return Key{}, err
	}
	nb, err := AddKey(b, k)
	if err != nil {
		return Key{}, err
	}
	if err := m.writeKeys(a, dir, file, nb); err != nil {
		return Key{}, err
	}
	notes := m.readNotes()
	if notes[username] == nil {
		notes[username] = map[string]keyNote{}
	}
	notes[username][k.Fingerprint] = keyNote{Name: k.Name, Added: now.UTC()}
	_ = m.writeNotes(notes)
	out := k.Key
	out.Added = now.UTC()
	return out, nil
}

// RemoveKey takes the key with fingerprint fp out of a person's
// authorized_keys.
func (m *Manager) RemoveKey(username, fp string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, dir, file, err := m.paths(username)
	if err != nil {
		return err
	}
	b, err := m.readKeys(file)
	if err != nil {
		return err
	}
	nb, err := RemoveKey(b, fp)
	if err != nil {
		return err
	}
	if bytes.Equal(nb, b) {
		return nil
	}
	if err := m.writeKeys(a, dir, file, nb); err != nil {
		return err
	}
	notes := m.readNotes()
	if notes[username] != nil {
		delete(notes[username], fp)
		_ = m.writeNotes(notes)
	}
	return nil
}
