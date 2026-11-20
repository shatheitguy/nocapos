package sshd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestRender(t *testing.T) {
	got := Render(Settings{Who: WhoAdmins, Password: false}, Allow{Groups: []string{"wheel", "sudo", "root", "bad name"}}, Caps{Penalties: true, KbdInteractive: true})
	for _, want := range []string{"PermitRootLogin no\n", "PasswordAuthentication no\n", "KbdInteractiveAuthentication no\n", "MaxAuthTries 3\n", "PerSourcePenalties yes\n", "AllowGroups sudo wheel\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "AllowUsers") || strings.Contains(got, "bad name") {
		t.Errorf("unexpected:\n%s", got)
	}

	got = Render(Settings{Who: WhoEveryone, Password: true}, Allow{Users: []string{"bo", "al", "al", "root", "x\nPermitRootLogin yes"}}, Caps{})
	if !strings.Contains(got, "AllowUsers al bo\n") || !strings.Contains(got, "PasswordAuthentication yes\n") {
		t.Errorf("everyone:\n%s", got)
	}
	if strings.Contains(got, "PerSourcePenalties") || strings.Contains(got, "KbdInteractive") || strings.Contains(got, "PermitRootLogin yes") {
		t.Errorf("old OpenSSH or injection:\n%s", got)
	}

	// Nobody left: nobody gets in.
	if got := Render(Settings{Who: WhoAdmins}, Allow{}, Caps{}); !strings.Contains(got, "DenyUsers *\n") {
		t.Errorf("empty allow:\n%s", got)
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]Caps{
		"OpenSSH_9.6p1 Ubuntu-3ubuntu13, OpenSSL 3.0.13": {Major: 9, Minor: 6, KbdInteractive: true},
		"OpenSSH_10.0p2 Debian-7, OpenSSL 3.5.1":         {Major: 10, Minor: 0, Penalties: true, KbdInteractive: true},
		"OpenSSH_9.8p1":                                  {Major: 9, Minor: 8, Penalties: true, KbdInteractive: true},
		"OpenSSH_8.4p1 Debian-5":                         {Major: 8, Minor: 4},
		"nothing":                                        {},
	}
	for in, want := range cases {
		if got := ParseVersion(in); got != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
}

func edKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
}

func rsaKey(t *testing.T, bits int) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ssh.NewPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
}

func TestParseKey(t *testing.T) {
	ed := edKey(t)
	k, err := ParseKey(ed+" me@laptop", "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ssh-ed25519" || k.Name != "me@laptop" || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Errorf("parsed %+v", k)
	}
	if k, _ := ParseKey(ed+" me@laptop", "  Work   laptop "); k.Name != "Work laptop" || !strings.HasSuffix(k.line, " Work laptop") {
		t.Errorf("name: %+v", k)
	}
	if _, err := ParseKey(rsaKey(t, 3072), "big"); err != nil {
		t.Errorf("rsa 3072: %v", err)
	}
	bad := map[string]string{
		"rsa 2048":      rsaKey(t, 2048),
		"options":       `command="/bin/sh" ` + ed,
		"no-pty option": "no-pty " + ed,
		"two lines":     ed + "\n" + ed,
		"newline name":  ed + " a\rb",
		"dss":           "ssh-dss AAAAB3NzaC1kc3MAAACBAP",
		"damaged":       "ssh-ed25519 !!!notbase64",
		"wrong type":    "ssh-rsa " + strings.Fields(ed)[1],
		"private":       "-----BEGIN OPENSSH PRIVATE KEY-----",
		"empty":         "   ",
		"only type":     "ssh-ed25519",
	}
	for name, in := range bad {
		if _, err := ParseKey(in, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted (err %v)", name, err)
		}
	}
	if _, err := ParseKey(ed, "evil\nssh-ed25519 AAAA"); !errors.Is(err, ErrInvalid) {
		t.Errorf("name with a newline: %v", err)
	}
	if _, err := ParseKey(ed, strings.Repeat("n", MaxKeyName+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("long name: %v", err)
	}
}

func TestAddRemoveKey(t *testing.T) {
	a, _ := ParseKey(edKey(t), "a")
	b, _ := ParseKey(edKey(t), "b")
	file := []byte("# mine\n" + `from="10.0.0.1" ` + edKey(t) + " other") // no final newline
	out, err := AddKey(file, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddKey(out, a); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: %v", err)
	}
	out, _ = AddKey(out, b)
	keys := ListKeys(out)
	if len(keys) != 3 || !keys[0].Options || keys[1].Name != "a" || keys[2].Name != "b" {
		t.Fatalf("keys %+v\n%s", keys, out)
	}
	if !strings.HasPrefix(string(out), "# mine\n") || strings.Count(string(out), "\n") != 4 {
		t.Errorf("file:\n%s", out)
	}
	out, err = RemoveKey(out, a.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if ks := ListKeys(out); len(ks) != 2 || ks[1].Fingerprint != b.Fingerprint || !strings.Contains(string(out), "# mine") {
		t.Errorf("after remove: %+v\n%s", ks, out)
	}
	if _, err := RemoveKey(out, a.Fingerprint); !errors.Is(err, ErrNoKey) {
		t.Errorf("remove twice: %v", err)
	}
	if _, err := AddKey(nil, NewKey{Key: Key{Fingerprint: "x"}, line: "ssh-ed25519 AAAA\nssh-rsa B"}); err == nil {
		t.Error("a line break got through")
	}
}

func testManager(t *testing.T) (*Manager, *fakeRun, string) {
	t.Helper()
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	home := filepath.Join(root, "home", "sam")
	for _, d := range []string{filepath.Join(etc, "ssh"), home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeRun{}
	var chowns []string
	m := &Manager{
		Run: f.run, Etc: etc, RunDir: filepath.Join(root, "run"), Data: filepath.Join(root, "data"), Native: true,
		Have:   func(string) bool { return true },
		Lookup: func(u string) (Account, error) { return Account{UID: 1000, GID: 1000, Home: home}, nil },
		Chown: func(p string, uid, gid int) error {
			chowns = append(chowns, filepath.Base(p))
			return nil
		},
	}
	return m, f, home
}

type fakeRun struct {
	mu      sync.Mutex
	calls   []string
	checkOK bool
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, j)
	switch {
	case j == "sshd -t":
		if f.checkOK {
			return "", nil
		}
		return "/etc/ssh/sshd_config.d/50-nocapos.conf line 9: Bad configuration option: PerSourcePenalties", errors.New("exit status 255")
	case j == "ssh -V":
		return "OpenSSH_9.8p1", nil
	case strings.HasPrefix(j, "systemctl show"):
		return "loaded", nil
	case strings.HasPrefix(j, "systemctl is-active"):
		return "inactive", errors.New("exit status 3")
	}
	return "", nil
}

func TestWriteConfigRollsBack(t *testing.T) {
	m, f, _ := testManager(t)
	main := filepath.Join(m.Etc, "ssh", "sshd_config")
	if err := os.WriteFile(main, []byte("Port 22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drop := filepath.Join(m.Etc, "ssh", "sshd_config.d", DropIn)
	if err := os.MkdirAll(filepath.Dir(drop), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drop, []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := m.Apply(context.Background(), DefaultSettings(), Allow{Groups: []string{"sudo"}})
	var ce *CheckError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "doesn't understand") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(drop); string(b) != "# old\n" {
		t.Errorf("drop-in not restored: %q", b)
	}
	if b, _ := os.ReadFile(main); string(b) != "Port 22\n" {
		t.Errorf("main config not restored: %q", b)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "systemctl reload") {
			t.Errorf("reloaded a bad config: %v", f.calls)
		}
	}

	// A good check keeps it, and the main file gains the Include.
	f.checkOK = true
	if err := m.Apply(context.Background(), DefaultSettings(), Allow{Groups: []string{"sudo"}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(drop); !strings.Contains(string(b), "AllowGroups sudo") || !strings.Contains(string(b), "PerSourcePenalties yes") {
		t.Errorf("drop-in: %q", b)
	}
	if b, _ := os.ReadFile(main); !strings.HasPrefix(strings.SplitN(string(b), "\n", 2)[1], "Include /etc/ssh/sshd_config.d/*.conf") {
		t.Errorf("main: %q", b)
	}
}

func TestEnableMakesHostKeys(t *testing.T) {
	m, f, _ := testManager(t)
	f.checkOK = true
	if err := m.Enable(context.Background(), DefaultSettings(), Allow{Groups: []string{"sudo"}}); err != nil {
		t.Fatal(err)
	}
	order := strings.Join(f.calls, "|")
	kg, chk, start := strings.Index(order, "ssh-keygen -A"), strings.Index(order, "sshd -t"), strings.Index(order, "systemctl enable --now ssh.service")
	if kg < 0 || chk < kg || start < chk {
		t.Errorf("order: %v", f.calls)
	}
}

func TestExplain(t *testing.T) {
	if s := Explain("sshd: no hostkeys available -- exiting."); !strings.Contains(s, "host keys") {
		t.Error(s)
	}
	if s := Explain("Missing privilege separation directory: /run/sshd"); !strings.Contains(s, "/run/sshd") {
		t.Error(s)
	}
}

func TestKeysFile(t *testing.T) {
	m, _, home := testManager(t)
	k, _ := ParseKey(edKey(t), "laptop")
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	if _, err := m.AddKey("sam", k, now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddKey("sam", k, now); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: %v", err)
	}
	keys, err := m.Keys("sam")
	if err != nil || len(keys) != 1 || keys[0].Name != "laptop" || !keys[0].Added.Equal(now) {
		t.Fatalf("keys %+v %v", keys, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(home, ".ssh"))
		fst, _ := os.Stat(filepath.Join(home, ".ssh", "authorized_keys"))
		if st.Mode().Perm() != 0o700 || fst.Mode().Perm() != 0o600 {
			t.Errorf("modes %v %v", st.Mode(), fst.Mode())
		}
	}
	if _, err := m.Keys("root"); !errors.Is(err, ErrInvalid) {
		t.Errorf("root: %v", err)
	}
	if _, err := m.Keys("../etc"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad name: %v", err)
	}
	if err := m.RemoveKey("sam", k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if keys, _ := m.Keys("sam"); len(keys) != 0 {
		t.Errorf("left: %+v", keys)
	}
}

func TestKeysRefuseLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	m, _, home := testManager(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	k, _ := ParseKey(edKey(t), "x")
	if _, err := m.AddKey("sam", k, time.Now()); err == nil {
		t.Fatal("wrote through a linked .ssh")
	}
	if _, err := os.Stat(filepath.Join(target, "authorized_keys")); err == nil {
		t.Error("file appeared behind the link")
	}
}
