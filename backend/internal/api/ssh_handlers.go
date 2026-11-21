package api

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/firewall"
	"alfaos/alfad/internal/sshd"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/syspkg"
)

// Remote login (SSH): Settings → Security. One switch installs (if needed),
// configures and starts the system's OpenSSH server and adds Shield's
// local-network rule for its port; off stops it and takes the rule away.
// Each person manages their own keys; admins can see and remove anyone's.
// Every change is audited.

func (s *Server) sshRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/ssh", s.admin(s.sshGet))
	mux.Handle("PUT /api/v1/ssh", s.admin(s.sshPut))
	mux.Handle("POST /api/v1/ssh/firewall/fix", s.admin(s.sshFirewallFix))
	mux.Handle("GET /api/v1/ssh/keys", s.authed(s.sshKeysGet))
	mux.Handle("POST /api/v1/ssh/keys", s.authed(s.sshKeyAdd))
	mux.Handle("POST /api/v1/ssh/keys/delete", s.authed(s.sshKeyRemove))
}

// sshSupported: NoCapOS runs straight on Linux as root, with the machine's
// own accounts (SSH logs in to Linux accounts).
func (s *Server) sshSupported() (bool, string) {
	if !hostSwitchesNative() {
		return false, "Remote login needs NoCapOS installed straight on Linux (not in Docker)."
	}
	if s.Accounts == nil || s.Accounts.Mode() != accounts.ModeSystem {
		return false, "Remote login needs NoCapOS accounts to be this machine's Linux accounts."
	}
	return true, ""
}

type sshFirewall struct {
	Supported bool `json:"supported"`
	Installed bool `json:"installed"`
	Active    bool `json:"active"`
	// Rule: Shield's local-network rule for SSH is there.
	Rule bool `json:"rule"`
	// Blocked: SSH is on but Shield keeps the local network out.
	Blocked bool              `json:"blocked"`
	Pending *firewall.Pending `json:"pending,omitempty"`
}

type sshView struct {
	Supported  bool   `json:"supported"`
	Reason     string `json:"reason,omitempty"`
	Installed  bool   `json:"installed"`
	CanInstall bool   `json:"can_install"`
	Enabled    bool   `json:"enabled"`
	Running    bool   `json:"running"`
	Port       int    `json:"port"`
	sshd.Settings
	// Penalties: sshd's own brute-force protection; otherwise Shield
	// rate-limits the port.
	Penalties bool     `json:"penalties"`
	Error     string   `json:"error,omitempty"`
	Overrides []string `json:"overrides,omitempty"`
	Addresses []string `json:"addresses"`
	User      string   `json:"user"`
	LocalOnly bool     `json:"local_only"`
	// HasKeys: some account has an SSH key.
	HasKeys  bool        `json:"has_keys"`
	Firewall sshFirewall `json:"firewall"`
}

func (s *Server) sshView(r *http.Request) sshView {
	ctx := r.Context()
	v := sshView{Addresses: hostAddresses(), User: userFrom(ctx).Username, Port: 22, Settings: s.ssh.Settings(), LocalOnly: s.localGate.LocalOnly()}
	v.Supported, v.Reason = s.sshSupported()
	if !v.Supported {
		return v
	}
	v.Installed = s.ssh.Installed()
	v.CanInstall = syspkg.CanInstall()
	if !v.Installed {
		if syspkg.ImageOS() {
			v.Reason = "The SSH server isn't part of this system image."
		}
		return v
	}
	unit := s.ssh.Unit(ctx)
	v.Enabled, v.Running = s.ssh.ServiceState(ctx, unit)
	v.Port = s.ssh.Port(ctx)
	v.Penalties = s.ssh.Caps(ctx).Penalties
	if v.Enabled && !v.Running {
		v.Error = s.ssh.StartError(ctx)
		if v.Error == "" {
			v.Error = "The SSH server is switched on but isn't running."
		}
	}
	if v.Enabled {
		v.Overrides = s.ssh.Effective(ctx, v.Settings)
	}
	v.HasKeys = s.anySSHKeys(ctx)
	v.Firewall = s.sshFirewallState(ctx, v.Enabled, v.Port)
	return v
}

// hostAddresses are this machine's IPv4 addresses other devices can use,
// private (home network) ones first.
func hostAddresses() []string {
	var priv, other []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := p.Addr().Unmap()
		if !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.IsPrivate() {
			priv = append(priv, ip.String())
		} else {
			other = append(other, ip.String())
		}
	}
	out := append(priv, other...)
	if out == nil {
		out = []string{}
	}
	return out
}

func (s *Server) sshFirewallState(ctx context.Context, enabled bool, port int) sshFirewall {
	st, err := s.shield.fw.Status(ctx)
	f := sshFirewall{Supported: st.Supported, Installed: st.Installed, Active: st.Active, Pending: st.Pending}
	if err != nil || !st.Installed {
		return f
	}
	f.Rule = len(firewall.Tagged(st, sshd.CommentLAN)) > 0
	if enabled && st.Active && !f.Rule {
		// Maybe another rule lets the local network in.
		f.Blocked = true
		for _, a := range hostAddresses() {
			if ip, err := netip.ParseAddr(a); err == nil && ip.IsPrivate() {
				f.Blocked = !firewall.Reachable(st.Incoming, st.IPv6, st.Rules, ip, port)
				break
			}
		}
	}
	return f
}

// sshAllow is who sshd lets in for the "who can log in" choice.
func (s *Server) sshAllow(ctx context.Context, who string) sshd.Allow {
	if who == sshd.WhoEveryone {
		var users []string
		if list, err := s.Accounts.List(ctx); err == nil {
			for _, a := range list {
				if !a.Root && !a.Disabled {
					users = append(users, a.Username)
				}
			}
		}
		return sshd.Allow{Users: users}
	}
	return sshd.Allow{Groups: adminGroups("/etc/group")}
}

// adminGroups are the administrator groups this machine has.
func adminGroups(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	have := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if name, _, ok := strings.Cut(sc.Text(), ":"); ok {
			have[name] = true
		}
	}
	var out []string
	for _, g := range []string{"sudo", "wheel", "admin"} {
		if have[g] {
			out = append(out, g)
		}
	}
	return out
}

// sshKeep is who stays connected through a firewall change, leaving out
// SSH itself (the change is about SSH).
func (s *Server) sshKeep(r *http.Request) firewall.Keep {
	k, info := s.fwKeepFor(r)
	ports := k.Ports[:0:0]
	for _, p := range k.Ports {
		if p != info.SSH {
			ports = append(ports, p)
		}
	}
	k.Ports = ports
	return k
}

// sshSyncFirewall puts Shield's rules for SSH in step with the switch: the
// local networks (rate-limited when sshd can't protect itself), and, when
// chosen, everyone (always rate-limited).
func (s *Server) sshSyncFirewall(ctx context.Context, on bool, set sshd.Settings, port int, penalties bool, k firewall.Keep) error {
	st, err := s.shield.fw.Status(ctx)
	if err != nil || !st.Supported || !st.Installed {
		return nil // no firewall: nothing to keep in step
	}
	action := "allow"
	if !penalties {
		action = "limit"
	}
	var lan, outside []firewall.Rule
	if on {
		for _, n := range localNets() {
			lan = append(lan, firewall.Rule{Action: action, Direction: "in", Proto: "tcp", Port: strconv.Itoa(port), From: n, Comment: sshd.CommentLAN})
		}
		if set.Outside {
			outside = append(outside, firewall.Rule{Action: "limit", Direction: "in", Proto: "tcp", Port: strconv.Itoa(port), Comment: sshd.CommentOutside})
		}
	}
	if _, err := s.shield.fw.SetTagged(ctx, sshd.CommentLAN, lan, k); err != nil {
		return err
	}
	_, err = s.shield.fw.SetTagged(ctx, sshd.CommentOutside, outside, k)
	return err
}

func (s *Server) sshError(w http.ResponseWriter, r *http.Request, err error) {
	var ce *sshd.CheckError
	switch {
	case errors.As(err, &ce):
		writeError(w, http.StatusUnprocessableEntity, "ssh_check", ce.Error())
	case errors.Is(err, sshd.ErrInvalid), errors.Is(err, firewall.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, sshd.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicate", err.Error())
	case errors.Is(err, sshd.ErrNoKey):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, sshd.ErrNotInstalled):
		writeError(w, http.StatusConflict, "not_installed", err.Error())
	case errors.Is(err, sshd.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "unsupported", err.Error())
	default:
		s.Log.Warn("remote login change failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadGateway, "ssh_failed", err.Error())
	}
}

func (s *Server) sshGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sshView(r))
}

// sshTurnOn starts the server and opens the local network in Shield.
func (s *Server) sshTurnOn(ctx context.Context, set sshd.Settings, k firewall.Keep) error {
	if err := s.ssh.Enable(ctx, set, s.sshAllow(ctx, set.Who)); err != nil {
		return err
	}
	return s.sshSyncFirewall(ctx, true, set, s.ssh.Port(ctx), s.ssh.Caps(ctx).Penalties, k)
}

func (s *Server) sshPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  *bool   `json:"enabled"`
		Who      *string `json:"who"`
		Password *bool   `json:"password"`
		Outside  *bool   `json:"outside"`
		// Confirm: the warning about outside access was shown.
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if ok, why := s.sshSupported(); !ok {
		writeError(w, http.StatusNotImplemented, "unsupported", why)
		return
	}
	ctx := r.Context()
	uid := userFrom(ctx).ID
	old := s.ssh.Settings()
	set := old
	if req.Who != nil {
		if *req.Who != sshd.WhoAdmins && *req.Who != sshd.WhoEveryone {
			writeError(w, http.StatusBadRequest, "invalid", "who must be admins or everyone")
			return
		}
		set.Who = *req.Who
	}
	if req.Password != nil {
		set.Password = *req.Password
	}
	if req.Outside != nil {
		if *req.Outside && !req.Confirm {
			writeError(w, http.StatusConflict, "confirm", "Letting the whole internet try to log in is risky. Confirm to go ahead.")
			return
		}
		if *req.Outside && s.localGate.LocalOnly() {
			writeError(w, http.StatusConflict, "local_only", "This server is set to let only its own screen in (Settings, General, This Screen & Access). Turn other devices back on first.")
			return
		}
		set.Outside = *req.Outside
	}
	k := s.sshKeep(r)

	if req.Enabled != nil && *req.Enabled {
		if err := s.ssh.SaveSettings(set); err != nil {
			s.internalError(w, r, err)
			return
		}
		if !s.ssh.Installed() {
			if !syspkg.CanInstall() {
				msg := "The SSH server (openssh-server) isn't installed, and NoCapOS can't install packages here."
				if syspkg.ImageOS() {
					msg = "The SSH server isn't part of this system image."
				}
				writeError(w, http.StatusConflict, "not_installed", msg)
				return
			}
			s.sshInstallThenOn(w, r, set, k)
			return
		}
		err := s.sshTurnOn(ctx, set, k)
		s.audit(r, uid, "ssh.enable", "", err == nil, errText(err))
		if err != nil {
			s.sshError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, s.sshView(r))
		return
	}
	if req.Enabled != nil && !*req.Enabled {
		err := s.ssh.Disable(ctx)
		if err == nil {
			err = s.sshSyncFirewall(ctx, false, set, 22, true, k)
		}
		if err == nil {
			err = s.ssh.SaveSettings(set)
		}
		s.audit(r, uid, "ssh.disable", "", err == nil, errText(err))
		if err != nil {
			s.sshError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, s.sshView(r))
		return
	}

	// Only the settings changed.
	var what []string
	if set.Who != old.Who {
		what = append(what, "who="+set.Who)
	}
	if set.Password != old.Password {
		what = append(what, "password="+onOffWord(set.Password))
	}
	if set.Outside != old.Outside {
		what = append(what, "outside="+onOffWord(set.Outside))
	}
	action := "ssh.settings"
	if set.Who != old.Who {
		action = "ssh.who"
	}
	var err error
	if s.ssh.Installed() {
		err = s.ssh.Apply(ctx, set, s.sshAllow(ctx, set.Who))
	}
	if err == nil {
		err = s.ssh.SaveSettings(set)
	}
	if err == nil && s.ssh.Installed() {
		if on, _ := s.ssh.ServiceState(ctx, s.ssh.Unit(ctx)); on {
			err = s.sshSyncFirewall(ctx, true, set, s.ssh.Port(ctx), s.ssh.Caps(ctx).Penalties, k)
		}
	}
	s.audit(r, uid, action, strings.Join(what, " "), err == nil, errText(err))
	if err != nil {
		s.sshError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sshView(r))
}

// sshInstallThenOn installs the OpenSSH server as a package job (the
// progress shows in Settings), then turns remote login on.
func (s *Server) sshInstallThenOn(w http.ResponseWriter, r *http.Request, set sshd.Settings, k firewall.Keep) {
	mgr := syspkg.Manager()
	pkg := "openssh-server"
	if mgr == "pacman" {
		pkg = "openssh"
	}
	plan, err := syspkg.Plan(mgr, syspkg.ActInstall, pkg)
	if err != nil {
		writeError(w, http.StatusConflict, "not_installed", err.Error())
		return
	}
	uid := userFrom(r.Context()).ID
	ip := clientIP(r, s.Config.TrustedProxies)
	steps := append(syspkg.StepsFor(plan), syspkg.Step{Label: "Turn on remote login", Fn: func(ctx context.Context, out io.Writer) error {
		err := s.sshTurnOn(ctx, set, k)
		s.auditCtx(ctx, uid, "ssh.enable", "", ip, err == nil, errText(err))
		return err
	}})
	s.startPkgJob(w, r, syspkg.ActInstall, pkg, steps, nil)
}

func (s *Server) sshFirewallFix(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.ssh.Installed() {
		s.sshError(w, r, sshd.ErrNotInstalled)
		return
	}
	err := s.sshSyncFirewall(ctx, true, s.ssh.Settings(), s.ssh.Port(ctx), s.ssh.Caps(ctx).Penalties, s.sshKeep(r))
	s.audit(r, userFrom(ctx).ID, "ssh.firewall.fix", "", err == nil, errText(err))
	if err != nil {
		s.fwError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sshView(r))
}

// sshUsersChanged keeps "everyone with a desktop account" in step when
// accounts come and go.
func (s *Server) sshUsersChanged(ctx context.Context) {
	if s.ssh == nil || !s.ssh.Native || !s.ssh.Installed() {
		return
	}
	set := s.ssh.Settings()
	if set.Who != sshd.WhoEveryone {
		return
	}
	if err := s.ssh.Apply(ctx, set, s.sshAllow(ctx, set.Who)); err != nil {
		s.Log.Warn("remote login: update who can log in", "err", err)
	}
}

// ---------- keys ----------

type sshUserKeys struct {
	User string     `json:"user"`
	Keys []sshd.Key `json:"keys"`
}

func (s *Server) anySSHKeys(ctx context.Context) bool {
	list, err := s.Accounts.List(ctx)
	if err != nil {
		return false
	}
	for _, a := range list {
		if a.Root {
			continue
		}
		if ks, err := s.ssh.Keys(a.Username); err == nil && len(ks) > 0 {
			return true
		}
	}
	return false
}

func (s *Server) sshKeysGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	ok, why := s.sshSupported()
	out := map[string]any{"supported": ok, "user": me.Username, "keys": []sshd.Key{}}
	if !ok {
		out["reason"] = why
		writeJSON(w, http.StatusOK, out)
		return
	}
	if keys, err := s.ssh.Keys(me.Username); err == nil {
		out["keys"] = keys
	} else {
		out["error"] = err.Error()
	}
	if me.Role == store.RoleAdmin {
		others := []sshUserKeys{}
		if list, err := s.Accounts.List(ctx); err == nil {
			for _, a := range list {
				if a.Root || a.Username == me.Username {
					continue
				}
				if ks, err := s.ssh.Keys(a.Username); err == nil && len(ks) > 0 {
					others = append(others, sshUserKeys{User: a.Username, Keys: ks})
				}
			}
		}
		sort.Slice(others, func(i, j int) bool { return others[i].User < others[j].User })
		out["others"] = others
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) sshKeyAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if !decodeJSONLimit(w, r, &req, 64<<10) {
		return
	}
	if ok, why := s.sshSupported(); !ok {
		writeError(w, http.StatusNotImplemented, "unsupported", why)
		return
	}
	me := userFrom(r.Context())
	nk, err := sshd.ParseKey(req.Key, req.Name)
	if err == nil {
		_, err = s.ssh.AddKey(me.Username, nk, time.Now())
	}
	s.audit(r, me.ID, "ssh.key.add", me.Username+" "+nk.Fingerprint, err == nil, errText(err))
	if err != nil {
		s.sshError(w, r, err)
		return
	}
	s.sshKeysGet(w, r)
}

func (s *Server) sshKeyRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		User        string `json:"user"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if ok, why := s.sshSupported(); !ok {
		writeError(w, http.StatusNotImplemented, "unsupported", why)
		return
	}
	me := userFrom(r.Context())
	if req.User == "" {
		req.User = me.Username
	}
	if req.User != me.Username && me.Role != store.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "You can only remove your own keys.")
		return
	}
	err := s.ssh.RemoveKey(req.User, req.Fingerprint)
	s.audit(r, me.ID, "ssh.key.remove", req.User+" "+req.Fingerprint, err == nil, errText(err))
	if err != nil {
		s.sshError(w, r, err)
		return
	}
	s.sshKeysGet(w, r)
}
