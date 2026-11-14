package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/dnsblock"
	"alfaos/alfad/internal/firewall"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/syspkg"
)

// Shield: the firewall (Linux's ufw does the work) and the DNS ad and
// tracker blocker. Everything here is admin-only and audited.

type shieldState struct {
	fw  *firewall.Engine
	dns *dnsblock.Service
}

// newShield starts the DNS blocker (it stays quiet unless it was turned on).
func newShield(ctx context.Context, dataDir string, log *slog.Logger, onRevert func(string, error)) *shieldState {
	sh := &shieldState{fw: firewall.New(), dns: dnsblock.New(filepath.Join(dataDir, "dns"), log)}
	sh.fw.OnRevert = onRevert
	if err := sh.dns.Start(ctx); err != nil {
		log.Warn("dns blocker didn't start", "err", err)
	}
	return sh
}

func (s *Server) shieldRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/shield", s.admin(s.shieldOverview))
	mux.Handle("GET /api/v1/shield/firewall", s.admin(s.fwGet))
	mux.Handle("POST /api/v1/shield/firewall/install", s.admin(s.fwInstall))
	mux.Handle("POST /api/v1/shield/firewall/enable", s.admin(s.fwEnable))
	mux.Handle("POST /api/v1/shield/firewall/disable", s.admin(s.fwDisable))
	mux.Handle("PUT /api/v1/shield/firewall/defaults", s.admin(s.fwDefaults))
	mux.Handle("PUT /api/v1/shield/firewall/logging", s.admin(s.fwLogging))
	mux.Handle("PUT /api/v1/shield/firewall/ipv6", s.admin(s.fwIPv6))
	mux.Handle("POST /api/v1/shield/firewall/rules", s.admin(s.fwAdd))
	mux.Handle("PUT /api/v1/shield/firewall/rules", s.admin(s.fwUpdate))
	mux.Handle("POST /api/v1/shield/firewall/rules/delete", s.admin(s.fwDelete))
	mux.Handle("POST /api/v1/shield/firewall/rules/move", s.admin(s.fwMove))
	mux.Handle("POST /api/v1/shield/firewall/presets/{id}", s.admin(s.fwPreset))
	mux.Handle("GET /api/v1/shield/firewall/exposed", s.admin(s.fwExposed))
	mux.Handle("PUT /api/v1/shield/firewall/exposed", s.admin(s.fwSetExposed))
	mux.Handle("GET /api/v1/shield/firewall/blocked", s.admin(s.fwBlocked))
	mux.Handle("POST /api/v1/shield/firewall/keep", s.admin(s.fwKeep))
	mux.Handle("POST /api/v1/shield/firewall/undo", s.admin(s.fwUndo))
	mux.Handle("GET /api/v1/shield/dns", s.admin(s.dnsGet))
	mux.Handle("PUT /api/v1/shield/dns", s.admin(s.dnsPut))
	mux.Handle("GET /api/v1/shield/dns/stats", s.admin(s.dnsStats))
	mux.Handle("GET /api/v1/shield/dns/log", s.admin(s.dnsLog))
	mux.Handle("DELETE /api/v1/shield/dns/log", s.admin(s.dnsClearLog))
	mux.Handle("POST /api/v1/shield/dns/pause", s.admin(s.dnsPause))
	mux.Handle("POST /api/v1/shield/dns/refresh", s.admin(s.dnsRefresh))
	mux.Handle("POST /api/v1/shield/dns/port53", s.admin(s.dnsPort53))
}

// shieldReverted tells admins a firewall change was undone because nobody
// kept it (usually: the change cut the browser off).
func (s *Server) shieldReverted(what string, err error) {
	body := "Nobody pressed “Keep”, so the firewall went back to how it was."
	if err != nil {
		body = "Nobody pressed “Keep”, and putting the firewall back failed: " + err.Error()
	}
	_, _ = s.notify.Add(context.Background(), notify.Notification{
		Key: "shield:revert:" + strconv.FormatInt(time.Now().UnixNano(), 36), Kind: notify.KindSecurity, Level: "warning",
		Title: "Firewall change undone (" + what + ")", Body: body,
		Icon: "shield", Action: &notify.Action{App: "shield"}, CreatedAt: time.Now(),
	})
	s.auditCtx(context.Background(), "", "firewall.revert", what, "", err == nil, errText(err))
}

// ---------- who must stay connected ----------

// fwKeep returns who must stay connected through a firewall change: the
// browser's address, the port it reached NoCapOS on, NoCapOS's own port and
// SSH.
func (s *Server) fwKeepFor(r *http.Request) (firewall.Keep, keepInfo) {
	info := keepInfo{Client: clientIP(r, s.Config.TrustedProxies), SSH: 22}
	ports := []int{}
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if ap, err := netip.ParseAddrPort(la.String()); err == nil {
			ports = append(ports, int(ap.Port()))
		}
	}
	if _, p, err := net.SplitHostPort(s.Config.ListenAddr); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			ports = append(ports, n)
		}
	}
	for _, l := range s.shield.fw.Listeners(r.Context()) {
		if l.Proto == "tcp" && (l.Process == "sshd" || l.Process == "ssh") && !l.Loopback() {
			info.SSH = l.Port
			break
		}
	}
	ports = append(ports, info.SSH)
	// The packet filter sees the TCP peer: behind a reverse proxy on another
	// machine that's the proxy, not the browser, so keep both.
	k := firewall.KeepFor(info.Client, ports...).WithPeer(tcpPeer(r))
	info.Ports = k.Ports
	if info.Ports == nil {
		info.Ports = []int{}
	}
	return k, info
}

type keepInfo struct {
	Client string `json:"client"`
	Ports  []int  `json:"ports"`
	SSH    int    `json:"ssh"`
}

type fwView struct {
	firewall.State
	Keep       keepInfo          `json:"keep"`
	Presets    []firewall.Preset `json:"presets"`
	LocalNets  []string          `json:"local_nets"`
	CanInstall bool              `json:"can_install"`
}

func (s *Server) fwView(r *http.Request, st firewall.State) fwView {
	_, info := s.fwKeepFor(r)
	return fwView{State: st, Keep: info, Presets: firewall.Presets, LocalNets: localNets(), CanInstall: syspkg.CanInstall()}
}

// localNets are the private networks this machine is on ("LAN only").
func localNets() []string {
	out := []string{}
	addrs, _ := net.InterfaceAddrs()
	seen := map[string]bool{}
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil || !p.Addr().IsPrivate() {
			continue
		}
		n := p.Masked().String()
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) fwError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, firewall.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, firewall.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "unsupported", err.Error())
	case errors.Is(err, firewall.ErrNotInstalled):
		writeError(w, http.StatusConflict, "not_installed", err.Error())
	case errors.Is(err, firewall.ErrStale), errors.Is(err, firewall.ErrNoPending):
		writeError(w, http.StatusConflict, "stale", err.Error())
	default:
		s.Log.Warn("firewall change failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadGateway, "firewall_failed", err.Error())
	}
}

// fwDone audits a change and answers with the new state.
func (s *Server) fwDone(w http.ResponseWriter, r *http.Request, action, target string, st firewall.State, err error) {
	s.audit(r, userFrom(r.Context()).ID, "firewall."+action, target, err == nil, errText(err))
	if err != nil {
		s.fwError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.fwView(r, st))
}

func (s *Server) fwGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.shield.fw.Status(r.Context())
	if err != nil {
		s.fwError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.fwView(r, st))
}

func (s *Server) fwInstall(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	err := syspkg.Install(ctx, syspkg.Names{"apt": {"ufw"}, "dnf": {"ufw"}, "pacman": {"ufw"}, "zypper": {"ufw"}, "apk": {"ufw"}})
	s.audit(r, userFrom(r.Context()).ID, "firewall.install", "ufw", err == nil, errText(err))
	if err != nil {
		writeError(w, http.StatusBadGateway, "install_failed", err.Error())
		return
	}
	s.fwGet(w, r)
}

func (s *Server) fwEnable(w http.ResponseWriter, r *http.Request) {
	k, _ := s.fwKeepFor(r)
	st, err := s.shield.fw.Enable(r.Context(), k)
	s.fwDone(w, r, "enable", "", st, err)
}

func (s *Server) fwDisable(w http.ResponseWriter, r *http.Request) {
	st, err := s.shield.fw.Disable(r.Context())
	s.fwDone(w, r, "disable", "", st, err)
}

func (s *Server) fwDefaults(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Incoming string `json:"incoming"`
		Outgoing string `json:"outgoing"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	k, _ := s.fwKeepFor(r)
	var st firewall.State
	var err error
	if req.Incoming != "" {
		st, err = s.shield.fw.SetDefault(r.Context(), "incoming", req.Incoming, k)
	}
	if err == nil && req.Outgoing != "" {
		st, err = s.shield.fw.SetDefault(r.Context(), "outgoing", req.Outgoing, k)
	}
	if req.Incoming == "" && req.Outgoing == "" {
		st, err = s.shield.fw.Status(r.Context())
	}
	s.fwDone(w, r, "defaults", "in="+req.Incoming+" out="+req.Outgoing, st, err)
}

func (s *Server) fwLogging(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level string `json:"level"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	st, err := s.shield.fw.SetLogging(r.Context(), req.Level)
	s.fwDone(w, r, "logging", req.Level, st, err)
}

func (s *Server) fwIPv6(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	st, err := s.shield.fw.SetIPv6(r.Context(), req.Enabled)
	s.fwDone(w, r, "ipv6", onOffWord(req.Enabled), st, err)
}

func ruleTarget(r firewall.Rule) string { return strings.Join(r.Args(), " ") }

func (s *Server) fwAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rule firewall.Rule `json:"rule"`
		At   *int          `json:"at"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	at := -1
	if req.At != nil {
		at = *req.At
	}
	k, _ := s.fwKeepFor(r)
	st, err := s.shield.fw.Add(r.Context(), req.Rule, at, k)
	s.fwDone(w, r, "rule.add", ruleTarget(req.Rule), st, err)
}

func (s *Server) fwUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Raw  string        `json:"raw"`
		Rule firewall.Rule `json:"rule"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	k, _ := s.fwKeepFor(r)
	st, err := s.shield.fw.Update(r.Context(), req.Raw, req.Rule, k)
	s.fwDone(w, r, "rule.edit", req.Raw+" → "+ruleTarget(req.Rule), st, err)
}

func (s *Server) fwDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Raw string `json:"raw"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	k, _ := s.fwKeepFor(r)
	st, err := s.shield.fw.Delete(r.Context(), req.Raw, k)
	s.fwDone(w, r, "rule.delete", req.Raw, st, err)
}

func (s *Server) fwMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Raw string `json:"raw"`
		To  int    `json:"to"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.To < 0 {
		writeError(w, http.StatusBadRequest, "invalid", "position must be 0 or more")
		return
	}
	k, _ := s.fwKeepFor(r)
	st, err := s.shield.fw.Move(r.Context(), req.Raw, req.To, k)
	s.fwDone(w, r, "rule.move", req.Raw+" → "+strconv.Itoa(req.To), st, err)
}

func (s *Server) fwPreset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"` // "" = anywhere, else an address or network
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	rules, err := firewall.PresetRules(id, req.Source)
	if err != nil {
		s.fwDone(w, r, "preset", id, firewall.State{}, err)
		return
	}
	st, err := s.shield.fw.AddMany(r.Context(), rules)
	s.fwDone(w, r, "preset", id+" from "+orAnywhere(req.Source), st, err)
}

func orAnywhere(s string) string {
	if s == "" {
		return "anywhere"
	}
	return s
}

// knownPorts names well-known services in "what's exposed".
var knownPorts = map[int]string{
	22: "SSH", 53: "DNS", 80: "Web (HTTP)", 443: "Web (HTTPS)", 139: "File sharing (SMB)", 445: "File sharing (SMB)",
	137: "File sharing names", 138: "File sharing names", 2049: "Network drives (NFS)", 111: "NFS port mapper",
	5353: "Local names (mDNS)", 3702: "Network discovery", 5357: "Network discovery", 3389: "Remote Desktop",
	5900: "VNC", 631: "Printing", 123: "Time (NTP)", 25: "Mail", 3306: "Database", 5432: "Database", 6379: "Cache",
}

func (s *Server) fwExposed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.shield.fw.Status(ctx)
	if err != nil {
		s.fwError(w, r, err)
		return
	}
	k, info := s.fwKeepFor(r)
	nocapPorts := map[int]bool{}
	for _, p := range info.Ports {
		if p != info.SSH {
			nocapPorts[p] = true
		}
	}
	type key struct {
		port  int
		proto string
	}
	byKey := map[key]*firewall.Exposed{}
	var list []*firewall.Exposed
	add := func(port int, proto, name, kind, process string, bypass bool) {
		kk := key{port, proto}
		if e, ok := byKey[kk]; ok {
			if bypass {
				e.Bypass, e.Kind, e.Name = true, kind, name
			}
			return
		}
		e := &firewall.Exposed{Port: port, Proto: proto, Name: name, Kind: kind, Process: process, Bypass: bypass}
		byKey[kk] = e
		list = append(list, e)
	}
	for _, l := range s.shield.fw.Listeners(ctx) {
		if l.Loopback() {
			continue
		}
		kind, name := "system", knownPorts[l.Port]
		switch {
		case nocapPorts[l.Port] && l.Proto == "tcp":
			kind, name = "nocap", "NoCapOS"
		case l.Port == info.SSH && l.Proto == "tcp":
			kind, name = "ssh", "SSH"
		case l.Process == "alfad" && l.Port == 53:
			kind, name = "nocap", "NoCapOS DNS blocker"
		case l.Process == "alfad":
			kind = "nocap"
			if name == "" {
				name = "NoCapOS"
			}
		}
		if name == "" {
			name = l.Process
		}
		if name == "" {
			name = "Port " + strconv.Itoa(l.Port)
		}
		add(l.Port, l.Proto, name, kind, l.Process, false)
	}
	if s.Docker != nil {
		if cts, err := s.Docker.ListContainers(ctx, false); err == nil {
			for _, c := range cts {
				for _, p := range c.Ports {
					if p.Public == 0 {
						continue
					}
					if a, err := netip.ParseAddr(p.IP); err == nil && a.IsLoopback() {
						continue
					}
					proto := p.Protocol
					if proto != "udp" {
						proto = "tcp"
					}
					add(int(p.Public), proto, strings.TrimPrefix(c.Name, "/"), "container", "", true)
				}
			}
		}
	}
	out := make([]firewall.Exposed, 0, len(list))
	for _, e := range list {
		e.Open = firewall.ReachableFromAnyone(st, e.Port, e.Proto)
		if e.Proto == "tcp" && k.Client.IsValid() && !k.Client.IsLoopback() {
			for _, p := range k.Ports {
				if p == e.Port {
					e.Locked = true
				}
			}
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	writeJSON(w, http.StatusOK, map[string]any{"active": st.Active, "services": out})
}

func (s *Server) fwSetExposed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Port  int    `json:"port"`
		Proto string `json:"proto"`
		Name  string `json:"name"`
		Open  bool   `json:"open"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	k, _ := s.fwKeepFor(r)
	st, err := s.shield.fw.SetExposed(r.Context(), req.Port, req.Proto, req.Name, req.Open, k)
	s.fwDone(w, r, "service", strconv.Itoa(req.Port)+"/"+req.Proto+" "+onOffWord(req.Open), st, err)
}

func (s *Server) fwBlocked(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"blocked": s.shield.fw.RecentBlocked(r.Context(), 300)})
}

func (s *Server) fwKeep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.shield.fw.KeepChanges(req.ID)
	s.audit(r, userFrom(r.Context()).ID, "firewall.keep", "", err == nil, errText(err))
	if err != nil {
		s.fwError(w, r, err)
		return
	}
	s.fwGet(w, r)
}

func (s *Server) fwUndo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.shield.fw.Undo(r.Context(), req.ID)
	s.audit(r, userFrom(r.Context()).ID, "firewall.undo", "", err == nil, errText(err))
	if err != nil {
		s.fwError(w, r, err)
		return
	}
	s.fwGet(w, r)
}

// ---------- DNS blocker ----------

func (s *Server) dnsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.shield.dns.Status())
}

func (s *Server) dnsPut(w http.ResponseWriter, r *http.Request) {
	var req dnsblock.Settings
	if !decodeJSONLimit(w, r, &req, 1<<20) { // allow and block lists can be long
		return
	}
	st, err := s.shield.dns.Apply(req)
	s.audit(r, userFrom(r.Context()).ID, "dns.settings", onOffWord(req.Enabled), err == nil, errText(err))
	if err != nil {
		if dnsblock.IsInvalid(err) {
			writeError(w, http.StatusBadRequest, "invalid", strings.TrimPrefix(err.Error(), "invalid: "))
			return
		}
		writeError(w, http.StatusBadGateway, "dns_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) dnsStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.shield.dns.Stats(r.Context()))
}

func (s *Server) dnsLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := dnsblock.LogFilter{Client: q.Get("client"), Search: q.Get("q"), Status: q.Get("status")}
	switch f.Status {
	case "", "blocked", "forwarded", "cached", "local", "error":
	default:
		writeError(w, http.StatusBadRequest, "invalid", "unknown status")
		return
	}
	if len(f.Search) > 253 || len(f.Client) > 64 {
		writeError(w, http.StatusBadRequest, "invalid", "too long")
		return
	}
	if b := q.Get("before"); b != "" {
		ms, err := strconv.ParseInt(b, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "before is a time in milliseconds")
			return
		}
		f.Before = time.UnixMilli(ms)
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = n
	}
	list, err := s.shield.dns.Log(r.Context(), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queries": list})
}

func (s *Server) dnsClearLog(w http.ResponseWriter, r *http.Request) {
	err := s.shield.dns.ClearLog()
	s.audit(r, userFrom(r.Context()).ID, "dns.log.clear", "", err == nil, errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dnsPause(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Minutes {
	case 0, 5, 30, 60:
	default:
		writeError(w, http.StatusBadRequest, "invalid", "pause for 5, 30 or 60 minutes, or 0 to resume")
		return
	}
	s.shield.dns.Pause(time.Duration(req.Minutes) * time.Minute)
	s.audit(r, userFrom(r.Context()).ID, "dns.pause", strconv.Itoa(req.Minutes)+"m", true, "")
	writeJSON(w, http.StatusOK, s.shield.dns.Status())
}

func (s *Server) dnsRefresh(w http.ResponseWriter, r *http.Request) {
	go s.shield.dns.Refresh(true)
	s.audit(r, userFrom(r.Context()).ID, "dns.lists.refresh", "", true, "")
	st := s.shield.dns.Status()
	st.Refreshing = true
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) dnsPort53(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Free bool `json:"free"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var st dnsblock.Status
	var err error
	if req.Free {
		st, err = s.shield.dns.FreePort53()
	} else {
		st, err = s.shield.dns.RestorePort53()
	}
	s.audit(r, userFrom(r.Context()).ID, "dns.port53", map[bool]string{true: "free", false: "restore"}[req.Free], err == nil, errText(err))
	if err != nil {
		writeError(w, http.StatusBadGateway, "dns_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// ---------- overview ----------

func (s *Server) shieldOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fw, err := s.shield.fw.Status(ctx)
	out := map[string]any{"dns": s.shield.dns.Status(), "stats": s.shield.dns.Stats(ctx)}
	if err != nil {
		out["firewall_error"] = err.Error()
	}
	out["firewall"] = s.fwView(r, fw)
	writeJSON(w, http.StatusOK, out)
}
