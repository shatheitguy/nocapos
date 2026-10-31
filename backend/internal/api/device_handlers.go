package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// Devices: phones running the NoCap OS app link to this server with a
// 6-digit code shown in Settings. A linked device gets its own token
// ("Authorization: Device <token>"), only good for the /api/v1/device/*
// endpoints, and belongs to the person who made the code.

const deviceKey ctxKey = 10

// seenEvery: how often a device's "last seen" is written at most.
const seenEvery = time.Minute

type pairingState struct {
	once  sync.Once
	codes *devices.Codes
	idMu  sync.Mutex
	id    string
}

func (s *Server) pairCodes() *devices.Codes {
	s.pairing.once.Do(func() {
		if s.pairing.codes == nil {
			s.pairing.codes = devices.NewCodes()
		}
	})
	return s.pairing.codes
}

// nocapID is this server's NoCap ID (made once, kept in the data directory).
func (s *Server) nocapID() (string, error) {
	s.pairing.idMu.Lock()
	defer s.pairing.idMu.Unlock()
	if s.pairing.id == "" {
		id, err := devices.LoadID(s.Config.DataDir)
		if err != nil {
			return "", err
		}
		s.pairing.id = id
	}
	return s.pairing.id, nil
}

func (s *Server) serverName() string {
	if s.Sampler != nil {
		if h := s.Sampler.HostInfo().Hostname; h != "" {
			return h
		}
	}
	h, _ := os.Hostname()
	return h
}

// requestOrigin is scheme://host of the request, as the browser or phone
// reached this server.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// requestHostname is the request's host without its port; IPv6 keeps its
// brackets so it can go straight into a URL.
func requestHostname(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}

func deviceFrom(ctx context.Context) *store.Device {
	d, _ := ctx.Value(deviceKey).(*store.Device)
	return d
}

// ---------- public ----------

type pairInfo struct {
	NocapID string `json:"nocap_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *Server) pairInfo(w http.ResponseWriter, r *http.Request) {
	id, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pairInfo{NocapID: id, Name: s.serverName(), Version: s.Version})
}

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

type claimRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
	Platform   string `json:"platform"`
}

type claimResponse struct {
	DeviceID   string            `json:"device_id"`
	Token      string            `json:"token"`
	NocapID    string            `json:"nocap_id"`
	ServerName string            `json:"server_name"`
	User       map[string]string `json:"user"`
}

func (s *Server) pairClaim(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	ip := clientIP(r, s.Config.TrustedProxies)
	codes := s.pairCodes()
	if codes.Blocked(ip) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many wrong codes. Chill for 15 minutes, then try again.")
		return
	}
	var req claimRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	req.DeviceName = strings.TrimSpace(req.DeviceName)
	if !sixDigits.MatchString(req.Code) {
		writeError(w, http.StatusBadRequest, "validation", "The code is 6 digits. Check Settings → Devices on your NoCapOS and try again.")
		return
	}
	if !devices.ValidName(req.DeviceName) {
		writeError(w, http.StatusBadRequest, "validation", "Give your device a name (up to 64 characters).")
		return
	}
	if !devices.ValidPlatform(req.Platform) {
		writeError(w, http.StatusBadRequest, "validation", "platform must be android, ios or other")
		return
	}
	nocapID, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	userID, err := codes.Claim(req.Code, ip)
	switch {
	case errors.Is(err, devices.ErrBlocked):
		s.audit(r, "", "devices.claim", truncate(req.DeviceName, 64), false, "blocked: too many wrong codes")
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many wrong codes. Chill for 15 minutes, then try again.")
		return
	case errors.Is(err, devices.ErrExpired):
		s.audit(r, "", "devices.claim", truncate(req.DeviceName, 64), false, "expired or used code")
		writeError(w, http.StatusBadRequest, "code_expired", "That code's expired. Grab a fresh one in Settings → Devices.")
		return
	case err != nil:
		s.audit(r, "", "devices.claim", truncate(req.DeviceName, 64), false, "wrong code")
		writeError(w, http.StatusBadRequest, "wrong_code", "That code doesn't match. Double-check the 6 digits on your NoCapOS screen.")
		return
	}

	// The person who made the code must still be allowed in.
	u, err := s.Store.UserByID(r.Context(), userID)
	if err == nil {
		err = s.Auth.DesktopAllowed(r.Context(), u)
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, auth.ErrFilesOnly) {
		s.audit(r, userID, "devices.claim", truncate(req.DeviceName, 64), false, "account can't sign in")
		writeError(w, http.StatusBadRequest, "code_expired", "That code's no longer valid. Grab a fresh one in Settings → Devices.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	token, hash := devices.NewToken()
	now := time.Now()
	d := &store.Device{
		ID: store.NewID(), UserID: u.ID, Name: req.DeviceName, Platform: req.Platform,
		Battery: -1, CreatedAt: now, LastSeenAt: now,
	}
	if err := s.Store.CreateDevice(r.Context(), d, hash); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "devices.claim", d.Name, true, d.Platform)
	writeJSON(w, http.StatusCreated, claimResponse{
		DeviceID: d.ID, Token: token, NocapID: nocapID, ServerName: s.serverName(),
		User: map[string]string{"username": u.Username},
	})
}

// ---------- signed-in people ----------

type pairCodeResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	NocapID   string    `json:"nocap_id"`
	PairURL   string    `json:"pair_url"`
}

func (s *Server) devicesPairCode(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	id, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	code, exp := s.pairCodes().Issue(u.ID)
	s.audit(r, u.ID, "devices.pair_code", u.Username, true, "")
	pairURL := "nocap://pair?server=" + url.QueryEscape(requestOrigin(r)) + "&code=" + code + "&id=" + url.QueryEscape(id)
	writeJSON(w, http.StatusOK, pairCodeResponse{Code: code, ExpiresAt: exp.UTC().Truncate(time.Second), NocapID: id, PairURL: pairURL})
}

type deviceJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	AppVersion string     `json:"app_version"`
	Battery    *int       `json:"battery"`
	Username   string     `json:"username,omitempty"`
}

func toDeviceJSON(d store.Device, withUser bool) deviceJSON {
	out := deviceJSON{ID: d.ID, Name: d.Name, Platform: d.Platform, CreatedAt: d.CreatedAt, AppVersion: d.AppVersion}
	if !d.LastSeenAt.IsZero() {
		t := d.LastSeenAt
		out.LastSeenAt = &t
	}
	if d.Battery >= 0 {
		b := d.Battery
		out.Battery = &b
	}
	if withUser {
		out.Username = d.Username
	}
	return out
}

func (s *Server) devicesList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	admin := u.Role == store.RoleAdmin
	owner := u.ID
	if admin {
		owner = ""
	}
	list, err := s.Store.ListDevices(r.Context(), owner)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]deviceJSON, 0, len(list))
	for _, d := range list {
		out = append(out, toDeviceJSON(d, admin))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) devicesRevoke(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	owner := u.ID
	if u.Role == store.RoleAdmin {
		owner = ""
	}
	d, err := s.Store.RevokeDevice(r.Context(), r.PathValue("id"), owner, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That device isn't linked (anymore).")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "devices.revoke", d.Name, true, "owner "+d.Username)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- device auth ----------

// device requires "Authorization: Device <token>" from a linked, not
// revoked device whose owner may still sign in.
func (s *Server) device(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Device ")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			w.Header().Set("WWW-Authenticate", `Device realm="nocap"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing device token")
			return
		}
		ctx := r.Context()
		d, err := s.Store.DeviceByTokenHash(ctx, devices.HashToken(tok))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid_token", "This device isn't linked anymore. Link it again from Settings → Devices.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		u, err := s.Store.UserByID(ctx, d.UserID)
		if err == nil {
			err = s.Auth.DesktopAllowed(ctx, u)
		}
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, auth.ErrFilesOnly) {
			writeError(w, http.StatusUnauthorized, "invalid_token", "The account this device belongs to can't sign in right now.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if now := time.Now(); now.Sub(d.LastSeenAt) >= seenEvery {
			if err := s.Store.TouchDevice(ctx, d.ID, now, nil, nil); err != nil {
				s.Log.Warn("device last seen not saved", "device", d.ID, "err", err)
			} else {
				d.LastSeenAt = now.UTC()
			}
		}
		ctx = context.WithValue(context.WithValue(ctx, deviceKey, d), userKey, u)
		h(w, r.WithContext(ctx))
	})
}

func (s *Server) deviceMe(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	id, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device": map[string]string{"id": d.ID, "name": d.Name, "platform": d.Platform},
		"user":   map[string]string{"username": u.Username},
		"server": pairInfo{NocapID: id, Name: s.serverName(), Version: s.Version},
	})
}

type deviceApp struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"icon_url"`
	URL     string `json:"url"`
}

// deviceAppList: NoCapOS itself, then each installed app with a web UI the
// person may use, with the address the App Store's Open button uses.
func deviceAppList(origin, host string, catalog []appstore.App, status map[string]*appstore.Installed, rules map[string]*store.AppAccess, v spaces.Viewer) []deviceApp {
	out := []deviceApp{{ID: "nocapos", Name: "NoCapOS", IconURL: origin + "/brand/mark-512.png", URL: origin + "/"}}
	for _, a := range catalog {
		in := status[a.ID]
		if in == nil || a.Web == nil || !spaces.AppAllowed(rules[a.ID], v) {
			continue
		}
		link := in.WebURL
		if link == "" {
			if in.WebPort == 0 {
				continue
			}
			path := a.Web.Path
			if path == "" {
				path = "/"
			}
			link = "http://" + host + ":" + strconv.Itoa(in.WebPort) + path
		}
		icon := ""
		if a.Logo != "" {
			icon = origin + a.Logo
		}
		out = append(out, deviceApp{ID: a.ID, Name: a.Name, IconURL: icon, URL: link})
	}
	return out
}

func (s *Server) deviceApps(w http.ResponseWriter, r *http.Request) {
	origin, host := requestOrigin(r), requestHostname(r)
	if s.AppStore == nil {
		writeJSON(w, http.StatusOK, deviceAppList(origin, host, nil, nil, nil, s.viewer(r)))
		return
	}
	status, err := s.AppStore.Status(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.fillAppWebURLs(r.Context(), status)
	var rules map[string]*store.AppAccess
	if s.Spaces != nil {
		if rules, err = s.Spaces.AppRules(r.Context()); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, deviceAppList(origin, host, s.AppStore.Catalog(), status, rules, s.viewer(r)))
}

type heartbeatRequest struct {
	Battery    *int    `json:"battery"`
	AppVersion *string `json:"app_version"`
}

func (s *Server) deviceHeartbeat(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r.Context())
	var req heartbeatRequest
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	if req.Battery != nil && (*req.Battery < 0 || *req.Battery > 100) {
		writeError(w, http.StatusBadRequest, "validation", "battery must be 0-100")
		return
	}
	if req.AppVersion != nil {
		v := strings.TrimSpace(*req.AppVersion)
		if !devices.ValidAppVersion(v) {
			writeError(w, http.StatusBadRequest, "validation", "app_version must be up to 32 printable characters")
			return
		}
		req.AppVersion = &v
	}
	if err := s.Store.TouchDevice(r.Context(), d.ID, time.Now(), req.Battery, req.AppVersion); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
