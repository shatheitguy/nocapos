package backup

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// NoCapOS signs in to SFTP destinations with its own key. The public half is
// shown in Backups, to add to the other server's ~/.ssh/authorized_keys.

func (m *Manager) sshKeyPath() (string, error) {
	p := filepath.Join(m.dataDir, "secrets", "backup_ed25519")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "nocapos-backup")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " nocapos-backup\n"
	if err := os.WriteFile(p+".pub", []byte(line), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// SSHPublicKey returns the authorized_keys line for this server's backup key.
func (m *Manager) SSHPublicKey() (string, error) {
	p, err := m.sshKeyPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
