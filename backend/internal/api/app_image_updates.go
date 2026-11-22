package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/imageupdates"
	"alfaos/alfad/internal/notify"
)

// New images for the App Store's Docker apps: checked a few minutes after
// start and about every 6 hours (see internal/imageupdates), shown as an
// update in the App Store and in Glow Up, with a notification unless the
// administrator turned those off.

const appImageNoticesKey = "appstore.image_update_notices"

const (
	appImageFirstCheck = 2 * time.Minute
	appImageEvery      = 6 * time.Hour
)

// startAppImageUpdates wires the checker into the App Store and runs it.
func (s *Server) startAppImageUpdates(ctx context.Context) {
	if s.Docker == nil || s.AppStore == nil {
		return
	}
	c := imageupdates.New(s.appImages, s.Docker.ImageRepoDigests, imageupdates.NewRegistry())
	c.OnFound = func(app string, ups []imageupdates.Update) { s.notifyAppImage(ctx, app, ups) }
	s.imageUpdates = c
	s.AppStore.ImageUpdates = c.Has
	s.AppStore.AfterUpdate = c.Forget
	go c.Run(ctx, appImageFirstCheck, appImageEvery)
}

// appImages lists the images of every installed app's containers.
func (s *Server) appImages(ctx context.Context) ([]imageupdates.Image, error) {
	status, err := s.AppStore.Status(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(status))
	for id := range status {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []imageupdates.Image
	for _, id := range ids {
		cs, err := s.Docker.ContainersByLabel(ctx, appstore.LabelApp, id)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			out = append(out, imageupdates.Image{App: id, Ref: c.Image, ImageID: c.ImageID})
		}
	}
	return out, nil
}

// appImageNotices reports whether new app images send a notification (on unless turned off).
func (s *Server) appImageNotices(ctx context.Context) bool {
	v, err := s.Store.Setting(ctx, appImageNoticesKey)
	return err != nil || v != "0"
}

func (s *Server) notifyAppImage(ctx context.Context, app string, ups []imageupdates.Update) {
	if len(ups) == 0 || !s.appImageNotices(ctx) {
		return
	}
	s.add(ctx, appImageNotification(app, s.appName(app), ups[0].Digest))
}

func appImageNotification(id, name, digest string) notify.Notification {
	short := strings.TrimPrefix(digest, "sha256:")
	if len(short) > 12 {
		short = short[:12]
	}
	return notify.Notification{
		Key: "appimage:" + id + ":" + short, Kind: notify.KindAppUpdate, Level: "info",
		Title: "An update for " + name + " is available",
		Body:  "Its publisher has a newer image. Update it in the App Store; its data and settings are kept.",
		GenZ: &notify.Variant{Title: name + " got a fresh drop",
			Body: "There's a newer image out. Update it in the App Store — its data and settings stay put."},
		Icon: "store:" + id, Action: storeAction(id),
	}
}

// GET /api/v1/appstore/updates: when apps were last checked, and whether new images notify.
func (s *Server) appImageUpdatesGet(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"notify": s.appImageNotices(r.Context())}
	if s.imageUpdates != nil {
		if t := s.imageUpdates.Checked(); !t.IsZero() {
			out["checked_at"] = t.UTC()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /api/v1/appstore/updates {"notify": bool}
func (s *Server) appImageUpdatesSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Notify *bool `json:"notify"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Notify != nil {
		if err := s.Store.SetSetting(r.Context(), appImageNoticesKey, boolSetting(*req.Notify)); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.appImageUpdatesGet(w, r)
}

// POST /api/v1/appstore/updates/check: check now (registries that asked to
// slow down are still left alone).
func (s *Server) appImageUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	if s.imageUpdates == nil {
		writeError(w, http.StatusServiceUnavailable, "docker_unavailable", "Docker isn't running here.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.imageUpdates.Check(ctx); err != nil {
		writeError(w, http.StatusBadGateway, "check_failed", err.Error())
		return
	}
	s.appImageUpdatesGet(w, r)
}
