package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupAccount(t *testing.T) {
	passwd := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/bash\nalex:x:1000:1000:Alex,,,:/home/alex:/bin/bash\nsvc:x:998:998::/var/lib/svc:/usr/sbin/nologin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := lookupAccount(passwd, "alex")
	if err != nil {
		t.Fatal(err)
	}
	if a.UID != 1000 || a.GID != 1000 || a.Home != "/home/alex" || a.Shell != "/bin/bash" || a.Root() {
		t.Errorf("alex: %+v", a)
	}
	if r, _ := lookupAccount(passwd, "root"); !r.Root() {
		t.Error("root not recognised")
	}
	if _, err := lookupAccount(passwd, "nobody-here"); !errors.Is(err, ErrNoLinuxAccount) {
		t.Errorf("unknown account: %v", err)
	}
	if s, _ := lookupAccount(passwd, "svc"); !noLogin(s.Shell) {
		t.Error("nologin shell not spotted")
	}
}
