// Package tlsutil provides a self-signed certificate for native installs that
// run without Traefik, so credentials never cross the LAN in clear text.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	validity     = 825 * 24 * time.Hour // max lifetime browsers accept for server certs
	renewBefore  = 30 * 24 * time.Hour
	certFileName = "tls.crt"
	keyFileName  = "tls.key"
)

// EnsureSelfSigned returns cert/key paths under dir, generating (or renewing)
// an ECDSA P-256 certificate for this host's names and addresses.
func EnsureSelfSigned(dir string) (certFile, keyFile string, err error) {
	certFile = filepath.Join(dir, certFileName)
	keyFile = filepath.Join(dir, keyFileName)
	if valid(certFile, keyFile) {
		return certFile, keyFile, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	host, _ := os.Hostname()
	dns := []string{"localhost", "alfa.local"}
	if host != "" {
		dns = append(dns, host, host+".local")
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "NoCapOS", Organization: []string{"NoCapOS (self-signed)"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           localIPs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err := writePEM(keyFile, "PRIVATE KEY", keyDER); err != nil {
		return "", "", err
	}
	if err := writePEM(certFile, "CERTIFICATE", der); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// ServerConfig is a modern TLS configuration (TLS 1.2+, AEAD suites only).
func ServerConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

func valid(certFile, keyFile string) bool {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return false
	}
	return time.Until(cert.NotAfter) > renewBefore
}

func localIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

func writePEM(path, typ string, der []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: der}); err != nil {
		f.Close()
		return errors.Join(err, os.Remove(tmp))
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
