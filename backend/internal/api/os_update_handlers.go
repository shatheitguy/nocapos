package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/osimage"
)

// Glow Up on an image install of NoCapOS: the whole system updates as one
// image into the spare slot, then "Restart to finish" boots it once and its
// health check keeps it or goes back (os/README.md). System packages never
// update on their own here (no apt): they come with these releases.

type osState struct {
	upd  *osimage.Updater
	info osimage.Info

	mu      sync.Mutex
	latest  *osimage.Manifest
	checked time.Time
	err     string
}

// osImage is the image install's update state, nil on a script install.
func (s *Server) initOSImage(ctx context.Context) {
	info, ok := osimage.Detect()
	if !ok {
		return
	}
	// First start after the installer: make what it asked for.
	s.applySeed(ctx)
	s.osImg = &osState{upd: osimage.NewUpdater(s.Config.DataDir, info), info: info}
	go s.osAutoLoop(ctx)
	go s.osNotifyOutcome(ctx)
}

// imageOS reports whether this is an image install.
func (s *Server) imageOS() bool { return s.osImg != nil }

const imageOSMessage = "On NoCapOS, the system updates as a whole, from Glow Up. Single packages aren't installed or updated here."

type osStatus struct {
	Image     bool               `json:"image"`
	Info      *osimage.Info      `json:"info,omitempty"`
	Slots     *osimage.Slots     `json:"slots,omitempty"`
	Settings  *osimage.Settings  `json:"settings,omitempty"`
	Latest    *osimage.Manifest  `json:"latest,omitempty"`
	Available bool               `json:"available"`
	Security  bool               `json:"security"`
	Size      int64              `json:"size,omitempty"`
	Checked   *time.Time         `json:"checked,omitempty"`
	CheckErr  string             `json:"check_error,omitempty"`
	State     *osimage.State     `json:"state,omitempty"`
	Running   bool               `json:"running"`
	Outcome   string             `json:"outcome,omitempty"`
	Reason    string             `json:"reason,omitempty"`
	BootCheck *osimage.BootCheck `json:"boot_check,omitempty"`
}

func (s *Server) osStatus(ctx context.Context) osStatus {
	if s.osImg == nil {
		return osStatus{Image: false}
	}
	o := s.osImg
	info := o.info
	set := osimage.LoadSettings(s.Config.DataDir, info)
	st := osStatus{Image: true, Info: &info, Settings: &set, State: o.upd.State(), Running: o.upd.Running()}
	if sl, err := o.upd.Ctrl.Slots(ctx); err == nil {
		st.Slots = &sl
	}
	o.mu.Lock()
	if o.latest != nil {
		m := *o.latest
		st.Latest = &m
		st.Available = osimage.Newer(m.Version, info.Version)
		st.Security = m.Security
		if t, ok := m.Targets[info.Target]; ok {
			st.Size = t.Size
		}
	}
	if !o.checked.IsZero() {
		c := o.checked
		st.Checked = &c
	}
	st.CheckErr = o.err
	o.mu.Unlock()
	// An install that this process isn't running any more (NoCapOS restarted
	// mid-way) didn't finish.
	if st.State != nil && st.State.Phase == osimage.PhaseInstalling && !st.Running {
		st.State.Phase, st.State.Error = osimage.PhaseFailed, "NoCapOS restarted while the update was being written. Nothing changed; try again."
	}
	slots := osimage.Slots{}
	if st.Slots != nil {
		slots = *st.Slots
	}
	st.BootCheck = o.upd.BootCheck()
	st.Outcome, st.Reason = osimage.Outcome(st.State, info.Version, slots, st.BootCheck)
	return st
}

// osCheck asks GitHub for the newest release on the chosen channel.
func (s *Server) osCheck(ctx context.Context) {
	o := s.osImg
	set := osimage.LoadSettings(s.Config.DataDir, o.info)
	m, err := osimage.Latest(ctx, set.Channel, o.info.Target, "NoCapOS/"+o.info.Version)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.checked = time.Now().UTC()
	if err != nil {
		if errors.Is(err, osimage.ErrNoRelease) {
			o.latest, o.err = nil, ""
			return
		}
		o.err = "Couldn't check for updates: " + err.Error()
		return
	}
	o.latest, o.err = m, ""
}

func (s *Server) osGet(w http.ResponseWriter, r *http.Request) {
	if s.osImg != nil && r.URL.Query().Get("refresh") != "" {
		s.osCheck(r.Context())
	}
	writeJSON(w, http.StatusOK, s.osStatus(r.Context()))
}

func (s *Server) osInstall(w http.ResponseWriter, r *http.Request) {
	if s.osImg == nil {
		writeError(w, http.StatusNotFound, "not_image", "This NoCapOS isn't an image install.")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}
	o := s.osImg
	o.mu.Lock()
	m := o.latest
	o.mu.Unlock()
	if m == nil || (req.Version != "" && req.Version != m.Version) {
		s.osCheck(r.Context())
		o.mu.Lock()
		m = o.latest
		o.mu.Unlock()
	}
	switch {
	case m == nil:
		writeError(w, http.StatusConflict, "no_update", "There's no system update to install.")
		return
	case req.Version != "" && req.Version != m.Version:
		writeError(w, http.StatusConflict, "release_changed", "There's a newer release now ("+m.Version+"). Check again, then update.")
		return
	case !osimage.Newer(m.Version, o.info.Version):
		writeError(w, http.StatusConflict, "up_to_date", "You're already on the latest version.")
		return
	}
	if st := o.upd.State(); st != nil && st.Phase == osimage.PhaseReady {
		writeError(w, http.StatusConflict, "restart_pending", osimage.ErrRestartPending.Error())
		return
	}
	err := o.upd.Start(m)
	s.audit(r, userFrom(r.Context()).ID, "system.os_update", o.info.Version+" -> "+m.Version, err == nil, errText(err))
	if err != nil {
		if errors.Is(err, osimage.ErrBusy) {
			writeError(w, http.StatusConflict, "already_running", err.Error())
			return
		}
		writeError(w, http.StatusConflict, "update_refused", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.osStatus(r.Context()))
}

func (s *Server) osRestart(w http.ResponseWriter, r *http.Request) {
	if s.osImg == nil {
		writeError(w, http.StatusNotFound, "not_image", "This NoCapOS isn't an image install.")
		return
	}
	st := s.osImg.upd.State()
	to := ""
	if st != nil {
		to = st.To
	}
	s.audit(r, userFrom(r.Context()).ID, "system.os_restart", to, true, "")
	// Answer first: the machine goes down right after.
	writeJSON(w, http.StatusAccepted, map[string]any{"restarting": true, "to": to})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(time.Second)
		if err := s.osImg.upd.Restart(context.Background()); err != nil {
			s.Log.Error("restart into the new system", "err", err)
		}
	}()
}

func (s *Server) osDismiss(w http.ResponseWriter, r *http.Request) {
	if s.osImg == nil {
		writeError(w, http.StatusNotFound, "not_image", "This NoCapOS isn't an image install.")
		return
	}
	if err := s.osImg.upd.Dismiss(); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.osStatus(r.Context()))
}

func (s *Server) osSettingsSet(w http.ResponseWriter, r *http.Request) {
	if s.osImg == nil {
		writeError(w, http.StatusNotFound, "not_image", "This NoCapOS isn't an image install.")
		return
	}
	var req osimage.Settings
	if !decodeJSON(w, r, &req) {
		return
	}
	req.AutoTime = strings.TrimSpace(req.AutoTime)
	if req.AutoTime == "" {
		req.AutoTime = osimage.DefaultAutoTime
	}
	if err := osimage.SaveSettings(s.Config.DataDir, req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_settings", err.Error())
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "system.os_update_settings", req.Channel, true, "")
	s.osImg.mu.Lock()
	s.osImg.latest = nil // the channel may have changed
	s.osImg.mu.Unlock()
	s.osCheck(r.Context())
	writeJSON(w, http.StatusOK, s.osStatus(r.Context()))
}

// ---- background: checks, notifications, automatic security updates ----------

const (
	osFirstCheck = 2 * time.Minute
	osCheckEvery = 6 * time.Hour
	osAutoTick   = 10 * time.Minute
)

// osAutoLoop checks for system updates now and then, says so in a
// notification, and installs security refreshes by itself when that's
// turned on (at the chosen time; the machine restarts to finish).
func (s *Server) osAutoLoop(ctx context.Context) {
	timer := time.NewTimer(osFirstCheck)
	defer timer.Stop()
	var lastCheck time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(osAutoTick)
		set := osimage.LoadSettings(s.Config.DataDir, s.osImg.info)
		auto := set.AutoSecurity && set.InWindow(time.Now())
		if time.Since(lastCheck) < osCheckEvery && !auto {
			continue
		}
		lastCheck = time.Now()
		s.osCheck(ctx)
		st := s.osStatus(ctx)
		if !st.Available || st.Latest == nil {
			continue
		}
		s.add(ctx, osUpdateNotification(st.Latest, s.osImg.info.Version))
		if !auto || !st.Latest.Security || !osimage.SameRelease(st.Latest.Version, s.osImg.info.Version) {
			continue
		}
		s.osAutoInstall(ctx, st.Latest)
	}
}

func (s *Server) osAutoInstall(ctx context.Context, m *osimage.Manifest) {
	u := s.osImg.upd
	if cur := u.State(); cur != nil && cur.Phase == osimage.PhaseReady && cur.To == m.Version {
		_ = u.Restart(ctx)
		return
	}
	if err := u.Start(m); err != nil {
		s.Log.Warn("automatic security update", "err", err)
		return
	}
	s.Log.Info("installing a security update by itself", "to", m.Version)
	for u.Running() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	if st := u.State(); st != nil && st.Phase == osimage.PhaseReady {
		s.Log.Info("restarting to finish the security update", "to", m.Version)
		if err := u.Restart(ctx); err != nil {
			s.Log.Warn("automatic security update restart", "err", err)
		}
	}
}

// osNotifyOutcome tells everyone once how an update went after its restart.
func (s *Server) osNotifyOutcome(ctx context.Context) {
	// Give the health check its time first.
	for i := 0; i < 60; i++ {
		st := s.osStatus(ctx)
		switch st.Outcome {
		case osimage.OutcomeChecking:
		case osimage.OutcomeUpdated:
			if st.State != nil && !st.State.Seen {
				s.add(ctx, notify.Notification{
					Key: "os-updated:" + st.State.To, Kind: notify.KindUpdate, Level: "success",
					Title: "NoCapOS " + st.State.To + " is installed", Body: "The update finished and everything checked out.",
					GenZ: &notify.Variant{Title: "NoCapOS " + st.State.To + " is in", Body: "Glow up done and it all checks out."},
					Icon: "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "update"}},
				})
			}
			return
		case osimage.OutcomeRolledBack:
			if st.State != nil && !st.State.Seen {
				s.add(ctx, notify.Notification{
					Key: "os-rolled-back:" + st.State.To, Kind: notify.KindUpdate, Level: "warning",
					Title: "The update to " + st.State.To + " was undone",
					Body:  "The new system didn't start properly, so NoCapOS went back to " + s.osImg.info.Version + ". Nothing was lost.",
					GenZ: &notify.Variant{Title: "Update to " + st.State.To + " got undone",
						Body: "The new system didn't come up right, so NoCapOS went back to " + s.osImg.info.Version + ". Nothing's lost."},
					Icon: "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "update"}},
				})
			}
			return
		default:
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func osUpdateNotification(m *osimage.Manifest, current string) notify.Notification {
	n := notify.Notification{
		Key: "os-update:" + m.Version, Kind: notify.KindUpdate, Level: "info",
		Title: "NoCapOS " + m.Version + " is available",
		Body:  "You're on " + current + ". Open Glow Up to install it.",
		GenZ: &notify.Variant{Title: "NoCapOS " + m.Version + " just dropped",
			Body: "You're on " + current + ". Open Glow Up to install it."},
		Icon: "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "update"}},
	}
	if m.Security {
		n.Title = "Security update for NoCapOS"
		n.Body = "Security fixes from Debian are ready (" + m.Version + "). Open Glow Up to install them."
		n.GenZ = &notify.Variant{Title: "Security patches dropped", Body: "Fresh security fixes are ready (" + m.Version + "). Hit Glow Up to install them."}
		n.Level = "warning"
	}
	return n
}
