package hostctl

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- Saved Wi-Fi networks ----------
//
// Every Wi-Fi connection profile NetworkManager keeps, with the actions
// Settings → Network → Wi-Fi offers on it. Secrets (Wi-Fi and EAP passwords,
// private-key passwords) never go on a command line: edits rewrite the
// profile's root-only keyfile and ask NetworkManager to load it again.

// SavedWiFi is one saved Wi-Fi profile.
type SavedWiFi struct {
	UUID          string   `json:"uuid"`
	Name          string   `json:"name"` // the profile name (usually the SSID)
	SSID          string   `json:"ssid"`
	Security      string   `json:"security"` // open | wep | wpa-psk | sae | owe | wpa-eap | wpa-eap-suite-b-192 | ieee8021x
	Enterprise    bool     `json:"enterprise"`
	EAP           *EAPInfo `json:"eap,omitempty"`
	AutoConnect   bool     `json:"autoconnect"`
	Priority      int      `json:"priority"`
	LastConnected int64    `json:"last_connected"` // unix seconds; 0 = never (or NetworkManager doesn't know)
	Active        bool     `json:"active"`
	Device        string   `json:"device,omitempty"`
	Hidden        bool     `json:"hidden"`
	Shareable     bool     `json:"shareable"` // personal (or open) network: a join QR code can be made
	Editable      bool     `json:"editable"`  // its keyfile is in NetworkManager's own folder, so secrets can be changed
}

// EAPInfo is the non-secret part of an enterprise profile.
type EAPInfo struct {
	Method            string `json:"method"` // peap | ttls | tls | pwd | leap | fast | …
	Phase2            string `json:"phase2,omitempty"`
	Identity          string `json:"identity,omitempty"`
	AnonymousIdentity string `json:"anonymous_identity,omitempty"`
	DomainSuffixMatch string `json:"domain_suffix_match,omitempty"`
	CA                string `json:"ca"` // system | file | none
	ClientCert        bool   `json:"client_cert"`
	PrivateKey        bool   `json:"private_key"`
}

// EAPConfig is a requested enterprise sign-in. Certificates are base64 file
// contents (PEM or DER; a client certificate without a separate key is taken
// as PKCS#12). On an edit, empty secrets and certificates keep what's saved.
type EAPConfig struct {
	Method             string `json:"method"`
	Phase2             string `json:"phase2"`
	Identity           string `json:"identity"`
	AnonymousIdentity  string `json:"anonymous_identity"`
	Password           string `json:"password"`
	DomainSuffixMatch  string `json:"domain_suffix_match"`
	CA                 string `json:"ca"` // system | file | none
	CACert             string `json:"ca_cert"`
	ClientCert         string `json:"client_cert"`
	PrivateKey         string `json:"private_key"`
	PrivateKeyPassword string `json:"private_key_password"`
}

// WiFiShare is what a phone needs to join: the standard QR code's fields.
type WiFiShare struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Type     string `json:"type"` // WPA | nopass
	Hidden   bool   `json:"hidden"`
}

// SavedSettings changes non-secret options; nil leaves one as it is.
type SavedSettings struct {
	AutoConnect *bool `json:"autoconnect"`
	Priority    *int  `json:"priority"`
}

// The EAP methods and inner (phase 2) methods NoCapOS sets up.
var (
	eapMethods   = map[string]bool{"peap": true, "ttls": true, "tls": true}
	eapPhase2    = map[string]map[string]bool{"peap": {"mschapv2": true, "gtc": true, "md5": true}, "ttls": {"mschapv2": true, "pap": true, "gtc": true, "md5": true}}
	ttlsNonEAP   = map[string]bool{"pap": true, "mschapv2": true} // TTLS inner methods that aren't EAP (phase2-auth)
	usesPassword = map[string]bool{"peap": true, "ttls": true}
	uuidRe       = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)
	domainRe     = regexp.MustCompile(`^[A-Za-z0-9.;*-]*$`)
)

// maxCertBytes bounds one uploaded certificate or key.
const maxCertBytes = 64 << 10

// nmCertsDir holds enterprise certificates and keys, one root-only folder per
// profile UUID. A var so tests can use a temp dir.
var nmCertsDir = "/etc/NetworkManager/nocap-certs"

func validUUID(id string) bool { return uuidRe.MatchString(id) }

func plainText(s string, max int) bool {
	return len(s) <= max && !strings.ContainsAny(s, "\x00\n\r")
}

// Validate checks an enterprise sign-in; existing is the saved one when
// editing (then blank secrets and certificates mean "keep").
func (c *EAPConfig) Validate(existing *EAPInfo) error {
	c.Method = strings.ToLower(strings.TrimSpace(c.Method))
	c.Phase2 = strings.ToLower(strings.TrimSpace(c.Phase2))
	c.CA = strings.ToLower(strings.TrimSpace(c.CA))
	c.Identity = strings.TrimSpace(c.Identity)
	c.AnonymousIdentity = strings.TrimSpace(c.AnonymousIdentity)
	c.DomainSuffixMatch = strings.TrimSpace(c.DomainSuffixMatch)
	if !eapMethods[c.Method] {
		return errors.New("choose an EAP method: PEAP, TTLS or TLS")
	}
	if allowed := eapPhase2[c.Method]; allowed != nil {
		if c.Phase2 == "" {
			c.Phase2 = "mschapv2"
		}
		if !allowed[c.Phase2] {
			return fmt.Errorf("%s can't use %s as its inner sign-in", strings.ToUpper(c.Method), strings.ToUpper(c.Phase2))
		}
	} else {
		c.Phase2 = ""
	}
	if c.Identity == "" || !plainText(c.Identity, 255) {
		return errors.New("enter the user name (identity), up to 255 characters")
	}
	if !plainText(c.AnonymousIdentity, 255) {
		return errors.New("the anonymous identity is too long")
	}
	if !plainText(c.Password, 256) || !plainText(c.PrivateKeyPassword, 256) {
		return errors.New("passwords can't contain line breaks (256 characters at most)")
	}
	keep := existing != nil && existing.Method == c.Method
	if usesPassword[c.Method] && c.Password == "" && !keep {
		return errors.New("enter the password for this sign-in")
	}
	if len(c.DomainSuffixMatch) > 255 || !domainRe.MatchString(c.DomainSuffixMatch) {
		return errors.New("the domain should look like radius.example.com")
	}
	switch c.CA {
	case "":
		c.CA = "system"
	case "system", "none":
	case "file":
		if c.CACert == "" && (existing == nil || existing.CA != "file") {
			return errors.New("choose the CA certificate file")
		}
	default:
		return errors.New("CA certificate must be system, file or none")
	}
	for _, f := range []struct{ name, v string }{{"CA certificate", c.CACert}, {"client certificate", c.ClientCert}, {"private key", c.PrivateKey}} {
		if f.v == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(f.v)
		if err != nil || len(b) == 0 {
			return fmt.Errorf("the %s file couldn't be read", f.name)
		}
		if len(b) > maxCertBytes {
			return fmt.Errorf("the %s file is too big (64 KB at most)", f.name)
		}
	}
	if c.Method == "tls" {
		hadTLS := existing != nil && existing.Method == "tls"
		if c.ClientCert == "" && !(hadTLS && existing.ClientCert) {
			return errors.New("EAP-TLS needs a client certificate")
		}
		p12 := c.ClientCert != "" && c.PrivateKey == "" && !isPEM(c.ClientCert)
		if c.PrivateKey == "" && !p12 && !(hadTLS && existing.PrivateKey) {
			return errors.New("EAP-TLS needs the private key for the client certificate")
		}
	}
	return nil
}

func isPEM(b64 string) bool {
	b, err := base64.StdEncoding.DecodeString(b64)
	return err == nil && strings.Contains(string(b), "-----BEGIN ")
}

// certPaths are where a profile's certificate files live.
type certPaths struct{ CA, Client, Key string }

// writeCerts saves newly uploaded certificates for a profile under
// nmCertsDir/<uuid> (folder 0700, files 0600) and returns the paths to use,
// keeping the old ones for anything not uploaded again.
func writeCerts(uuid string, c EAPConfig, old certPaths) (certPaths, error) {
	p := old
	if !validUUID(uuid) {
		return p, errors.New("invalid connection")
	}
	dir := filepath.Join(nmCertsDir, uuid)
	save := func(name, b64 string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		_ = os.Chmod(dir, 0o700)
		path := filepath.Join(dir, name)
		return path, writeFileAtomic(path, b)
	}
	var err error
	if c.CACert != "" {
		ext := ".der"
		if isPEM(c.CACert) {
			ext = ".pem"
		}
		if p.CA, err = save("ca"+ext, c.CACert); err != nil {
			return p, err
		}
	}
	if c.ClientCert != "" {
		ext := ".der"
		switch {
		case isPEM(c.ClientCert):
			ext = ".pem"
		case c.PrivateKey == "":
			ext = ".p12" // a PKCS#12 bundle carries the key too
		}
		if p.Client, err = save("client"+ext, c.ClientCert); err != nil {
			return p, err
		}
		if ext == ".p12" {
			p.Key = p.Client
		}
	}
	if c.PrivateKey != "" {
		ext := ".p12"
		if isPEM(c.PrivateKey) {
			ext = ".pem"
		}
		if p.Key, err = save("key"+ext, c.PrivateKey); err != nil {
			return p, err
		}
	}
	return p, nil
}

func removeCerts(uuid string) {
	if validUUID(uuid) {
		_ = os.RemoveAll(filepath.Join(nmCertsDir, uuid))
	}
}

// kv is one keyfile key: v nil removes it.
type kv struct {
	k string
	v *string
}

func sp(s string) *string { return &s }

// eapKeys are the [802-1x] keyfile keys for a sign-in. Keys left out of the
// list stay as they are on an edit (a blank password keeps the saved one);
// fresh is a brand-new profile.
func eapKeys(c EAPConfig, certs certPaths, fresh bool) []kv {
	opt := func(s string) *string {
		if s == "" {
			return nil
		}
		return sp(keyfileEscape(s))
	}
	out := []kv{{"eap", sp(c.Method + ";")}, {"identity", sp(keyfileEscape(c.Identity))}}
	if c.Method == "peap" || c.Method == "ttls" {
		out = append(out, kv{"anonymous-identity", opt(c.AnonymousIdentity)})
	} else {
		out = append(out, kv{"anonymous-identity", nil})
	}
	switch {
	case !usesPassword[c.Method]:
		out = append(out, kv{"password", nil}, kv{"password-flags", nil})
	case c.Password != "":
		out = append(out, kv{"password", sp(keyfileEscape(c.Password))}, kv{"password-flags", sp("0")})
	case fresh:
		// nothing to write
	}
	auth, autheap := "", ""
	switch {
	case c.Method == "peap":
		auth = c.Phase2
	case c.Method == "ttls" && ttlsNonEAP[c.Phase2]:
		auth = c.Phase2
	case c.Method == "ttls":
		autheap = c.Phase2
	}
	out = append(out, kv{"phase2-auth", opt(auth)}, kv{"phase2-autheap", opt(autheap)})
	if c.CA == "file" && certs.CA != "" {
		out = append(out, kv{"ca-cert", sp(keyfileEscape(certs.CA))})
	} else {
		out = append(out, kv{"ca-cert", nil})
	}
	if c.CA == "system" {
		out = append(out, kv{"system-ca-certs", sp("true")})
	} else {
		out = append(out, kv{"system-ca-certs", nil})
	}
	out = append(out, kv{"domain-suffix-match", opt(c.DomainSuffixMatch)})
	if c.Method == "tls" {
		out = append(out, kv{"client-cert", opt(certs.Client)}, kv{"private-key", opt(certs.Key)})
		switch {
		case c.PrivateKeyPassword != "":
			out = append(out, kv{"private-key-password", sp(keyfileEscape(c.PrivateKeyPassword))}, kv{"private-key-password-flags", sp("0")})
		case c.PrivateKey != "" || c.ClientCert != "" || fresh:
			// a new key without a password: drop the old key's password
			out = append(out, kv{"private-key-password", nil}, kv{"private-key-password-flags", nil})
		}
	} else {
		out = append(out, kv{"client-cert", nil}, kv{"private-key", nil}, kv{"private-key-password", nil}, kv{"private-key-password-flags", nil})
	}
	return out
}

// ---------- keyfile editing ----------

// keyfile section names have long and short spellings.
var sectionAlias = map[string]string{"802-11-wireless": "wifi", "802-11-wireless-security": "wifi-security"}

func canonSection(s string) string {
	if a, ok := sectionAlias[s]; ok {
		return a
	}
	return s
}

// editKeyfile changes keys in a keyfile's sections and keeps everything else
// (other keys, comments, order) as it was. A missing section is added.
func editKeyfile(content string, edits map[string][]kv) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	done := map[string]map[string]bool{}
	var out []string
	cur := ""
	flush := func(section string) {
		for _, e := range edits[section] {
			if e.v != nil && !done[section][e.k] {
				out = append(out, e.k+"="+*e.v)
			}
		}
	}
	seen := map[string]bool{}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			if cur != "" {
				// keep a blank line between sections
				if n := len(out); n > 0 && out[n-1] == "" {
					out = out[:n-1]
					flush(cur)
					out = append(out, "")
				} else {
					flush(cur)
				}
			}
			cur = canonSection(strings.TrimSpace(t[1 : len(t)-1]))
			seen[cur] = true
			if done[cur] == nil {
				done[cur] = map[string]bool{}
			}
			out = append(out, line)
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok && cur != "" && !strings.HasPrefix(t, "#") {
			k = strings.TrimSpace(k)
			if e, hit := findKV(edits[cur], k); hit {
				if done[cur][k] || e.v == nil {
					continue // removed (or a duplicate)
				}
				done[cur][k] = true
				out = append(out, k+"="+*e.v)
				continue
			}
		}
		out = append(out, line)
	}
	if cur != "" {
		flush(cur)
	}
	names := make([]string, 0, len(edits))
	for s := range edits {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		if seen[s] {
			continue
		}
		var add []string
		for _, e := range edits[s] {
			if e.v != nil {
				add = append(add, e.k+"="+*e.v)
			}
		}
		if len(add) > 0 {
			out = append(out, "", "["+s+"]")
			out = append(out, add...)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func findKV(list []kv, k string) (kv, bool) {
	for _, e := range list {
		if e.k == k {
			return e, true
		}
	}
	return kv{}, false
}

// writeFileAtomic writes a root-only (0600) file through a temp file in the
// same folder, so NetworkManager never reads half of one. The temp name
// starts with a dot, which NetworkManager's keyfile reader skips.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".nocap-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_ = f.Chmod(0o600)
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ---------- reading profiles ----------

// unescapeTerse undoes nmcli's terse escaping (`\:` and `\\`).
func unescapeTerse(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	esc := false
	for _, r := range s {
		if !esc && r == '\\' {
			esc = true
			continue
		}
		esc = false
		b.WriteRune(r)
	}
	return b.String()
}

// savedRow is one line of the connection list, plus the keyfile it lives in.
type savedRow struct {
	SavedWiFi
	file string
}

func savedListArgs() []string {
	return []string{"-t", "-f", "NAME,UUID,TYPE,AUTOCONNECT,AUTOCONNECT-PRIORITY,TIMESTAMP,ACTIVE,DEVICE,FILENAME", "connection", "show"}
}

// parseSavedList reads `nmcli -t -f NAME,UUID,TYPE,AUTOCONNECT,
// AUTOCONNECT-PRIORITY,TIMESTAMP,ACTIVE,DEVICE,FILENAME connection show`,
// keeping Wi-Fi profiles only.
func parseSavedList(out string) []savedRow {
	var rows []savedRow
	for _, line := range strings.Split(out, "\n") {
		f := splitTerse(line)
		if len(f) < 6 || f[2] != "802-11-wireless" && f[2] != "wifi" {
			continue
		}
		r := savedRow{SavedWiFi: SavedWiFi{Name: f[0], UUID: f[1], AutoConnect: f[3] == "yes", SSID: f[0]}}
		r.Priority, _ = strconv.Atoi(f[4])
		r.LastConnected, _ = strconv.ParseInt(f[5], 10, 64)
		if len(f) > 6 {
			r.Active = f[6] == "yes"
		}
		if len(f) > 7 && f[7] != "--" {
			r.Device = f[7]
		}
		if len(f) > 8 {
			r.file = f[8]
		}
		if validUUID(r.UUID) {
			rows = append(rows, r)
		}
	}
	return rows
}

// fillDetails adds what `nmcli -t connection show uuid <uuid>` says about a
// profile (no secrets: that needs -s). It returns the certificate paths and
// whether the profile is an access point (hotspot) rather than a network.
func fillDetails(r *savedRow, out string) (certs certPaths, hotspot bool) {
	kv := keyValues(out)
	first := func(k string) string {
		if v := kv[k]; len(v) > 0 {
			return unescapeTerse(v[0]) // keyValues undid `\:` only; this undoes `\\`
		}
		return ""
	}
	if s := first("802-11-wireless.ssid"); s != "" {
		r.SSID = s
	}
	r.Hidden = first("802-11-wireless.hidden") == "yes"
	hotspot = first("802-11-wireless.mode") == "ap"
	km := first("802-11-wireless-security.key-mgmt")
	switch {
	case km == "":
		r.Security = "open"
	case km == "none":
		r.Security = "wep"
	default:
		r.Security = km
	}
	r.Enterprise = strings.HasPrefix(km, "wpa-eap") || km == "ieee8021x"
	r.Shareable = km == "" || km == "wpa-psk" || km == "sae"
	if r.Enterprise {
		path := func(k string) string {
			v := strings.TrimPrefix(first(k), "file://")
			if !strings.HasPrefix(v, "/") {
				return "" // a blob or nothing
			}
			return v
		}
		certs = certPaths{CA: path("802-1x.ca-cert"), Client: path("802-1x.client-cert"), Key: path("802-1x.private-key")}
		e := &EAPInfo{
			Method:            strings.Split(first("802-1x.eap"), ",")[0],
			Phase2:            first("802-1x.phase2-auth"),
			Identity:          first("802-1x.identity"),
			AnonymousIdentity: first("802-1x.anonymous-identity"),
			DomainSuffixMatch: first("802-1x.domain-suffix-match"),
			CA:                "none",
			ClientCert:        first("802-1x.client-cert") != "",
			PrivateKey:        first("802-1x.private-key") != "",
		}
		if e.Phase2 == "" {
			e.Phase2 = first("802-1x.phase2-autheap")
		}
		switch {
		case first("802-1x.ca-cert") != "":
			e.CA = "file"
		case first("802-1x.system-ca-certs") == "yes":
			e.CA = "system"
		}
		r.EAP = e
	}
	return certs, hotspot
}

// keyfileEditable says whether a profile's file is a keyfile NoCapOS may
// rewrite: a plain file directly in NetworkManager's own folder.
func keyfileEditable(file string) bool {
	if file == "" || !strings.HasSuffix(file, ".nmconnection") {
		return false
	}
	if filepath.Clean(filepath.Dir(file)) != filepath.Clean(nmConnectionsDir) {
		return false
	}
	st, err := os.Lstat(file)
	return err == nil && st.Mode().IsRegular()
}

func savedRows(ctx context.Context) ([]savedRow, error) {
	out, err := runCmd(ctx, "nmcli", savedListArgs()...)
	if err != nil {
		return nil, err
	}
	return parseSavedList(out), nil
}

// SavedWiFiList lists saved Wi-Fi networks: connected first, then the most
// recently used.
func SavedWiFiList(ctx context.Context) ([]SavedWiFi, error) {
	if err := canControl(); err != nil {
		return nil, err
	}
	rows, err := savedRows(ctx)
	if err != nil {
		return nil, err
	}
	list := make([]SavedWiFi, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		if out, err := runCmd(ctx, "nmcli", "-t", "connection", "show", "uuid", r.UUID); err == nil {
			if _, hotspot := fillDetails(r, out); hotspot {
				continue
			}
		}
		r.Editable = keyfileEditable(r.file)
		list = append(list, r.SavedWiFi)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Active != list[j].Active {
			return list[i].Active
		}
		if list[i].LastConnected != list[j].LastConnected {
			return list[i].LastConnected > list[j].LastConnected
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

// ErrNoSuchProfile means the UUID isn't a saved Wi-Fi network.
var ErrNoSuchProfile = errors.New("that saved network isn't there anymore")

// savedOne finds one saved Wi-Fi profile with its details.
func savedOne(ctx context.Context, uuid string) (savedRow, certPaths, error) {
	if err := canControl(); err != nil {
		return savedRow{}, certPaths{}, err
	}
	if !validUUID(uuid) {
		return savedRow{}, certPaths{}, ErrNoSuchProfile
	}
	rows, err := savedRows(ctx)
	if err != nil {
		return savedRow{}, certPaths{}, err
	}
	for _, r := range rows {
		if r.UUID != uuid {
			continue
		}
		out, err := runCmd(ctx, "nmcli", "-t", "connection", "show", "uuid", uuid)
		if err != nil {
			return savedRow{}, certPaths{}, err
		}
		certs, _ := fillDetails(&r, out)
		r.Editable = keyfileEditable(r.file)
		return r, certs, nil
	}
	return savedRow{}, certPaths{}, ErrNoSuchProfile
}

// SavedWiFiName is a saved network's display name (for audit entries).
func SavedWiFiName(ctx context.Context, uuid string) string {
	if r, _, err := savedOne(ctx, uuid); err == nil {
		return r.SSID
	}
	return uuid
}

// ---------- actions ----------

// SavedWiFiConnect brings a saved network up.
func SavedWiFiConnect(ctx context.Context, uuid string) error {
	r, _, err := savedOne(ctx, uuid)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, wifiConnectTimeout)
	defer cancel()
	out, err := runCmd(cctx, "nmcli", savedConnectArgs(uuid)...)
	if err != nil {
		j := WiFiJoin{SSID: r.SSID, Password: "saved"}
		if r.Enterprise {
			j.EAP = &EAPConfig{}
		}
		return wifiError(j, out, err)
	}
	return nil
}

func savedConnectArgs(uuid string) []string {
	return []string{"--wait", strconv.Itoa(int(wifiConnectTimeout.Seconds()) - 5), "connection", "up", "uuid", uuid}
}

// SavedWiFiForget deletes a saved network (and its certificates).
func SavedWiFiForget(ctx context.Context, uuid string) error {
	if _, _, err := savedOne(ctx, uuid); err != nil {
		return err
	}
	if _, err := runCmd(ctx, "nmcli", "connection", "delete", "uuid", uuid); err != nil {
		return err
	}
	removeCerts(uuid)
	return nil
}

// savedModifyArgs builds `nmcli connection modify` for non-secret options.
func savedModifyArgs(uuid string, s SavedSettings) ([]string, error) {
	args := []string{"connection", "modify", "uuid", uuid}
	if s.AutoConnect != nil {
		args = append(args, "connection.autoconnect", map[bool]string{true: "yes", false: "no"}[*s.AutoConnect])
	}
	if s.Priority != nil {
		if *s.Priority < -999 || *s.Priority > 999 {
			return nil, errors.New("priority goes from -999 to 999")
		}
		args = append(args, "connection.autoconnect-priority", strconv.Itoa(*s.Priority))
	}
	if len(args) == 4 {
		return nil, errors.New("nothing to change")
	}
	return args, nil
}

// SavedWiFiSet changes auto-connect and/or priority.
func SavedWiFiSet(ctx context.Context, uuid string, s SavedSettings) error {
	if _, _, err := savedOne(ctx, uuid); err != nil {
		return err
	}
	args, err := savedModifyArgs(uuid, s)
	if err != nil {
		return err
	}
	_, err = runCmd(ctx, "nmcli", args...)
	return err
}

// ErrNotEditable means the profile's file isn't a keyfile NoCapOS can
// rewrite (it was made by another tool, e.g. a cloud-init or netplan file).
var ErrNotEditable = errors.New("this network was set up outside NetworkManager's own folder, so its password can't be changed here — forget it and join again instead")

// rewriteKeyfile applies edits to a profile's keyfile and reloads it.
func rewriteKeyfile(ctx context.Context, r savedRow, edits map[string][]kv) error {
	if !keyfileEditable(r.file) {
		return ErrNotEditable
	}
	data, err := os.ReadFile(r.file)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(r.file, []byte(editKeyfile(string(data), edits))); err != nil {
		return err
	}
	_, err = runCmd(ctx, "nmcli", "connection", "load", r.file)
	return err
}

// validPSK checks a WPA personal password: 8–63 characters or 64 hex digits.
func validPSK(pw string) error {
	if strings.ContainsAny(pw, "\x00\n\r") {
		return errors.New("the password can't contain line breaks")
	}
	if len(pw) == 64 {
		for _, c := range pw {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return errors.New("a 64-character key must be hex digits")
			}
		}
		return nil
	}
	if len(pw) < 8 || len(pw) > 63 {
		return errors.New("Wi-Fi passwords are 8 to 63 characters")
	}
	return nil
}

// SavedWiFiSetPassword changes a personal network's saved password.
func SavedWiFiSetPassword(ctx context.Context, uuid, pw string) error {
	r, _, err := savedOne(ctx, uuid)
	if err != nil {
		return err
	}
	if r.Security != "wpa-psk" && r.Security != "sae" {
		return errors.New("only WPA personal networks have a single password to change")
	}
	if err := validPSK(pw); err != nil {
		return err
	}
	return rewriteKeyfile(ctx, r, map[string][]kv{"wifi-security": {{"psk", sp(keyfileEscape(pw))}, {"psk-flags", nil}}})
}

// SavedWiFiSetEAP changes an enterprise network's sign-in.
func SavedWiFiSetEAP(ctx context.Context, uuid string, c EAPConfig) error {
	r, certs, err := savedOne(ctx, uuid)
	if err != nil {
		return err
	}
	if !r.Enterprise {
		return errors.New("this isn't an enterprise network")
	}
	if !keyfileEditable(r.file) {
		return ErrNotEditable
	}
	if err := c.Validate(r.EAP); err != nil {
		return err
	}
	certs, err = writeCerts(uuid, c, certs)
	if err != nil {
		return err
	}
	return rewriteKeyfile(ctx, r, map[string][]kv{
		"wifi-security": {{"key-mgmt", sp("wpa-eap")}},
		"802-1x":        eapKeys(c, certs, false),
	})
}

// SavedWiFiShare reads what a phone needs to join a personal or open
// network. The secret comes back on nmcli's output, never its arguments.
func SavedWiFiShare(ctx context.Context, uuid string) (WiFiShare, error) {
	r, _, err := savedOne(ctx, uuid)
	if err != nil {
		return WiFiShare{}, err
	}
	if !r.Shareable {
		return WiFiShare{}, errors.New("enterprise networks can't be shared with a QR code — each person signs in with their own account")
	}
	sh := WiFiShare{SSID: r.SSID, Hidden: r.Hidden, Type: "nopass"}
	if r.Security == "open" {
		return sh, nil
	}
	out, err := runCmd(ctx, "nmcli", "-s", "-g", "802-11-wireless-security.psk", "connection", "show", "uuid", uuid)
	if err != nil {
		return WiFiShare{}, err
	}
	sh.Password = unescapeTerse(strings.TrimRight(out, "\r\n"))
	if sh.Password == "" {
		return WiFiShare{}, errors.New("the password isn't stored on the server (it's kept in a signed-in user's keyring)")
	}
	sh.Type = "WPA"
	return sh, nil
}
