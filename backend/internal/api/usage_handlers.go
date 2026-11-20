package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/hostaccess"
	"alfaos/alfad/internal/hostctl"
	"alfaos/alfad/internal/osimage"
)

// "How will you use NoCapOS?": one choice over the two switches of This
// Screen & Access.
//
//	desktop-web  NoCap Desktop on the screen, NoCapOS from other devices too
//	desktop      NoCap Desktop only: other devices are kept out (NoCapOS
//	             still runs for the desktop's own Settings, Glow Up and apps,
//	             answering this machine only)
//	web          no screen: the desktop isn't started; NoCapOS from other
//	             devices only

// usageOf derives the mode from the two switches.
func usageOf(remote bool, desk hostaccess.DesktopState) string {
	switch {
	case !remote:
		return osimage.UsageDesktop
	case desk.Available && desk.Mode != hostaccess.ModeOff:
		return osimage.UsageDesktopWeb
	default:
		return osimage.UsageWeb
	}
}

// applyUsage sets both switches for a mode.
func (s *Server) applyUsage(ctx context.Context, mode string) error {
	if !osimage.ValidUsage(mode) {
		return errors.New("say mode: desktop-web, desktop or web")
	}
	desk := hostaccess.ModeFull
	if mode == osimage.UsageWeb {
		desk = hostaccess.ModeOff
	}
	err := s.desktop.SetMode(ctx, desk)
	if err != nil && !(mode == osimage.UsageWeb && errors.Is(err, hostaccess.ErrDesktopUnavailable)) {
		return err
	}
	rerr := hostaccess.SetRemote(s.Config.DataDir, mode != osimage.UsageDesktop)
	s.localGate.Refresh()
	return rerr
}

func (s *Server) usageSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
		// Confirm: choosing "desktop" from another device locks that browser out.
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !hostSwitchesNative() {
		writeError(w, http.StatusNotImplemented, "unsupported", "This needs NoCapOS installed straight on Linux (not in Docker).")
		return
	}
	if req.Mode == osimage.UsageDesktop && !hostaccess.LoopbackPeer(r.RemoteAddr) && !req.Confirm {
		writeError(w, http.StatusConflict, "confirm", "This browser isn't on the machine itself, so \"Desktop only\" locks it out. Confirm to go ahead.")
		return
	}
	err := s.applyUsage(r.Context(), req.Mode)
	s.audit(r, userFrom(r.Context()).ID, "system.usage", req.Mode, err == nil, errText(err))
	if err != nil {
		switch {
		case errors.Is(err, hostaccess.ErrDesktopUnavailable):
			writeError(w, http.StatusNotImplemented, "unsupported", s.desktop.State(r.Context()).Reason)
		default:
			writeError(w, http.StatusBadRequest, "usage_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, s.hostAccessState(r))
}

// ---- the installer's seed -------------------------------------------------------

// applySeed makes what the installer asked for on the first start of a
// freshly installed image: the owner's account (an administrator), the
// machine's name, time zone, keyboard and usage mode. The seed (it holds the
// password) is destroyed afterwards, whatever happened.
func (s *Server) applySeed(ctx context.Context) {
	seed, err := osimage.ReadSeed()
	if seed == nil && err == nil {
		return
	}
	defer func() {
		if err := osimage.DestroySeed(); err != nil {
			s.Log.Error("installer seed: couldn't remove it", "err", err)
		}
	}()
	if err != nil {
		s.Log.Error("installer seed: unreadable, ignored", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if fa, ok := s.Accounts.(auth.FirstAccounts); ok {
		has, herr := fa.HasPeople(ctx)
		switch {
		case herr != nil:
			s.Log.Error("installer seed: accounts", "err", herr)
		case has:
			s.Log.Warn("installer seed: accounts already exist, the account isn't made again")
		default:
			if err := fa.CreateFirstAdmin(ctx, seed.Username, seed.FullName, seed.Password); err != nil && !errors.Is(err, accounts.ErrExists) {
				s.Log.Error("installer seed: the account wasn't made", "err", err)
			} else {
				s.Log.Info("installer seed: administrator account made", "username", seed.Username)
			}
		}
	}
	if seed.Hostname != "" {
		if _, err := hostctl.SetHostname(ctx, seed.Hostname); err != nil {
			s.Log.Warn("installer seed: hostname", "err", err)
		}
	}
	if seed.TimeZone != "" {
		if _, err := hostctl.SetTimeZone(ctx, seed.TimeZone); err != nil {
			s.Log.Warn("installer seed: time zone", "err", err)
		}
	}
	if seed.Keyboard != "" {
		if err := os.WriteFile("/etc/default/keyboard", []byte(osimage.KeyboardFile(seed.Keyboard, seed.Variant)), 0o644); err != nil {
			s.Log.Warn("installer seed: keyboard", "err", err)
		}
	}
	if seed.Usage != "" && hostSwitchesNative() {
		if err := s.applyUsage(ctx, seed.Usage); err != nil {
			s.Log.Warn("installer seed: usage", "mode", seed.Usage, "err", err)
		}
	}
	// The machine part of the first-run wizard is done: the installer asked it.
	if err := s.Store.SetSetting(ctx, settingMachineSetup, time.Now().UTC().Format(time.RFC3339)+" installer"); err != nil {
		s.Log.Warn("installer seed: setup state", "err", err)
	}
}
