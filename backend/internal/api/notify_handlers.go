package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/backup"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/storage"
	"alfaos/alfad/internal/store"
)

// Notifications (admin): the Notification Center's list, its settings, and
// the producers that turn updates, app crashes, storage alerts and failed
// backups into notifications.

func (s *Server) notifyRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/notifications", s.admin(s.notifyList))
	mux.Handle("POST /api/v1/notifications/read", s.admin(s.notifyRead))
	mux.Handle("POST /api/v1/notifications/test", s.admin(s.notifyTest))
	mux.Handle("GET /api/v1/notifications/settings", s.admin(s.notifySettings))
	mux.Handle("PUT /api/v1/notifications/settings", s.admin(s.notifySetSettings))
	mux.Handle("DELETE /api/v1/notifications/{id}", s.admin(s.notifyDelete))
	mux.Handle("DELETE /api/v1/notifications", s.admin(s.notifyClear))
}

func (s *Server) notifyList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, unread, err := s.notify.List(r.Context(), limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "unread": unread})
}

func (s *Server) notifyRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) > 1000 {
		writeError(w, http.StatusBadRequest, "bad_request", "too many ids")
		return
	}
	if err := s.notify.MarkRead(r.Context(), req.IDs, req.All); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"unread": s.notify.Unread(r.Context())})
}

func (s *Server) notifyDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if err := s.notify.Delete(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such notification")
			return
		}
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notifyClear(w http.ResponseWriter, r *http.Request) {
	if err := s.notify.Clear(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notifySettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.notify.Settings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) notifySetSettings(w http.ResponseWriter, r *http.Request) {
	set := notify.DefaultSettings() // fields left out stay on
	if !decodeJSON(w, r, &set) {
		return
	}
	if err := s.notify.SetSettings(r.Context(), set); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "notifications.settings", "", true, "")
	writeJSON(w, http.StatusOK, set)
}

// notifyTest sends a dated test notification (Settings → Notifications).
func (s *Server) notifyTest(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	_, err := s.notify.Add(r.Context(), notify.Notification{
		Key: "test:" + strconv.FormatInt(now.UnixNano(), 10), Kind: notify.KindTest, Level: "info",
		Title: "Test notification",
		Body:  "Sent " + now.Format("Mon 2 Jan at 15:04:05") + ". This is how NoCapOS notifications look.",
		Icon:  "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "notifications"}},
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notificationsSource feeds the notifications topic: the unread count now,
// then every new notification (and the count after reads and deletes).
func (s *Server) notificationsSource(ctx context.Context, emit func(any)) error {
	ch, cancel := s.notify.Subscribe()
	defer cancel()
	emit(notify.Event{Type: "sync", Unread: s.notify.Unread(ctx)})
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-ch:
			emit(e)
		}
	}
}

// ---------- producers ----------

const (
	releaseFirstCheck = time.Minute
	releaseEvery      = 6 * time.Hour
	dockerRetry       = 30 * time.Second
)

// RunNotifications starts the notification producers until ctx ends.
func (s *Server) RunNotifications(ctx context.Context) {
	if s.Backup != nil {
		s.Backup.OnResult(func(res backup.Result) { s.notifyBackup(ctx, res) })
	}
	if s.Storage != nil {
		go s.watchStorageAlerts(ctx)
	}
	if s.Docker != nil && s.AppStore != nil {
		go s.watchAppCrashes(ctx)
	}
	go s.watchUpdates(ctx)
}

func (s *Server) add(ctx context.Context, n notify.Notification) {
	if _, err := s.notify.Add(ctx, n); err != nil && ctx.Err() == nil {
		s.Log.Warn("notification", "key", n.Key, "err", err)
	}
}

// watchUpdates checks for a new NoCapOS and App Store app updates about a
// minute after start, then every 6 hours.
func (s *Server) watchUpdates(ctx context.Context) {
	t := time.NewTimer(releaseFirstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.checkNoCapOSUpdate(ctx)
		s.checkAppUpdates(ctx)
		t.Reset(releaseEvery)
	}
}

func (s *Server) checkNoCapOSUpdate(ctx context.Context) {
	info := checkRelease(ctx, s.Version)
	if info.Error != "" {
		return
	}
	updateCache.Lock()
	updateCache.info = &info
	updateCache.Unlock()
	if info.Available {
		s.add(ctx, releaseNotification(info))
	}
}

func releaseNotification(info updateInfo) notify.Notification {
	v := strings.TrimPrefix(info.Latest, "v")
	return notify.Notification{
		Key: "update:" + info.Latest, Kind: notify.KindUpdate, Level: "info",
		Title: "NoCapOS " + v + " is available",
		Body:  "You have " + strings.TrimPrefix(info.Current, "v") + ". Open Software Update to see what's new and install it.",
		Icon:  "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "update"}},
	}
}

func (s *Server) checkAppUpdates(ctx context.Context) {
	if s.AppStore == nil {
		return
	}
	status, err := s.AppStore.Status(ctx)
	if err != nil {
		return
	}
	for id, in := range status {
		if !in.UpdateAvailable {
			continue
		}
		a, err := s.AppStore.App(id)
		if err != nil {
			continue
		}
		s.add(ctx, appUpdateNotification(id, a.Name, a.Version, in.Version))
	}
}

func appUpdateNotification(id, name, latest, current string) notify.Notification {
	return notify.Notification{
		Key: "appupdate:" + id + ":" + latest, Kind: notify.KindAppUpdate, Level: "info",
		Title: name + " " + latest + " is available",
		Body:  "You have " + current + ". Update it in the App Store; its data and settings are kept.",
		Icon:  "store:" + id, Action: storeAction(id),
	}
}

func storeAction(app string) *notify.Action {
	return &notify.Action{App: "appcenter", Props: map[string]any{"app": app}}
}

func (s *Server) appName(id string) string {
	if s.AppStore != nil {
		if a, err := s.AppStore.App(id); err == nil {
			return a.Name
		}
	}
	return id
}

// watchAppCrashes follows Docker events for App Store containers that die
// with an error, reconnecting while Docker is down.
func (s *Server) watchAppCrashes(ctx context.Context) {
	for {
		_ = s.Docker.Events(ctx, func(e docker.Event) error {
			if e.Type != "container" {
				return nil
			}
			c, ok := s.crashes.Observe(notify.ContainerExit{Action: e.Action, ID: e.ID, Name: e.Name, App: e.App, ExitCode: e.ExitCode, Time: e.Time})
			if ok {
				s.add(ctx, crashNotification(c, s.appName(c.App), e.Name))
			}
			return nil
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(dockerRetry):
		}
	}
}

func crashNotification(c notify.Crash, appName, container string) notify.Notification {
	return notify.Notification{
		Key: c.Key, Kind: notify.KindAppError, Level: "error",
		Title: appName + " stopped unexpectedly",
		Body:  "Its container " + strings.TrimPrefix(container, "/") + " exited with code " + c.ExitCode + ". Open it in the App Store to start it again.",
		Icon:  "store:" + c.App, Action: storeAction(c.App),
	}
}

// notifyAppJob reports an App Store install, update or uninstall that failed.
func (s *Server) notifyAppJob(app, action string, err error) {
	if err == nil || (action != "install" && action != "update" && action != "uninstall") {
		return
	}
	s.add(context.Background(), notify.Notification{
		Key:  fmt.Sprintf("appjob:%s:%s:%d", app, action, time.Now().UnixNano()),
		Kind: notify.KindAppError, Level: "error",
		Title: "Couldn't " + action + " " + s.appName(app),
		Body:  errText(err),
		Icon:  "store:" + app, Action: storeAction(app),
	})
}

// watchStorageAlerts turns each new disk or pool health alert into a notification.
func (s *Server) watchStorageAlerts(ctx context.Context) {
	ch, cancel := s.Storage.SubscribeAlerts()
	defer cancel()
	handle := func(list []storage.Alert) {
		for _, a := range list {
			s.add(ctx, storageNotification(a))
		}
	}
	handle(s.Storage.Alerts())
	for {
		select {
		case <-ctx.Done():
			return
		case list := <-ch:
			handle(list)
		}
	}
}

func storageNotification(a storage.Alert) notify.Notification {
	level := a.Level
	if level != "error" {
		level = "warning"
	}
	return notify.Notification{
		Key: "storage:" + a.Key, Kind: notify.KindStorage, Level: level, Title: a.Title, Body: a.Body,
		Icon: "storage", Action: &notify.Action{App: "storage"},
	}
}

func (s *Server) notifyBackup(ctx context.Context, res backup.Result) {
	if res.Status != "failed" {
		return
	}
	s.add(ctx, notify.Notification{
		Key:  fmt.Sprintf("backup:%d:%d", res.PlanID, res.Started.Unix()),
		Kind: notify.KindBackup, Level: "error",
		Title: "Backup “" + res.Plan + "” failed",
		Body:  res.Error,
		Icon:  "backups", Action: &notify.Action{App: "backups"},
	})
}
