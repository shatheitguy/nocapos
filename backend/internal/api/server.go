// Package api exposes alfad's REST and WebSocket endpoints.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/ai"
	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/backup"
	"alfaos/alfad/internal/cloudimport"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/hardware"
	"alfaos/alfad/internal/netdrive"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/photos"
	"alfaos/alfad/internal/rdp"
	"alfaos/alfad/internal/scripts"
	"alfaos/alfad/internal/stacks"
	"alfaos/alfad/internal/storage"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/terminal"
	"alfaos/alfad/internal/webapps"
	"alfaos/alfad/internal/ws"
	"alfaos/alfad/web"
)

type Deps struct {
	Config    *config.Config
	Log       *slog.Logger
	Store     *store.Store
	Auth      *auth.Service
	Docker    *docker.Client
	Sampler   *hardware.Sampler
	Files     *files.Service
	FileIndex *files.Index
	FileJobs  *files.Jobs
	AI        *ai.Service
	Terminal  *terminal.Service
	Brave     *webapps.Brave
	Guacd     *rdp.Guacd
	Accounts  accounts.Directory
	Scripts   *scripts.Runner
	AppStore  *appstore.Manager
	Photos    *photos.Library
	Backup    *backup.Manager
	NetDrives *netdrive.Manager
	Cloud     *cloudimport.Manager
	Stacks    *stacks.Manager
	Storage   *storage.Manager
	Version   string
}

type Server struct {
	Deps
	hub             *ws.Hub
	tickets         *auth.TicketStore
	fileTickets     fileTicketStore
	terminalTickets terminalTicketStore
	rdpTickets      rdpTicketStore
	loginLimiter    *auth.Limiter
	brave           *webapps.Brave
	braveKey        []byte
	notify          *notify.Service
	crashes         *notify.CrashWatch
}

// New builds the API server. ctx bounds the lifetime of WebSocket feeds.
func New(ctx context.Context, d Deps) *Server {
	s := &Server{
		Deps:    d,
		tickets: auth.NewTicketStore(30 * time.Second),
		// 5 attempts burst, then one every 12s per client IP.
		loginLimiter: auth.NewLimiter(12*time.Second, 5),
		brave:        d.Brave,
		braveKey:     randomKey(32),
	}
	s.hub = ws.NewHub(ctx, s.resolveTopic, d.Log)
	s.notify = notify.New(d.Store, d.Log)
	s.crashes = notify.NewCrashWatch()
	if d.AppStore != nil {
		s.crashes.Busy = d.AppStore.Busy
	}
	if d.Storage != nil {
		d.Storage.InUse = s.storageInUse
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)

	// Auth
	mux.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/v1/auth/setup", s.authSetup)
	mux.HandleFunc("POST /api/v1/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.authRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.authLogout)
	mux.Handle("GET /api/v1/auth/me", s.authed(s.authMe))
	mux.HandleFunc("POST /api/v1/auth/reset", s.authReset)
	mux.Handle("POST /api/v1/auth/password", s.authed(s.changePassword))
	mux.Handle("GET /api/v1/auth/totp", s.authed(s.totpStatus))
	mux.Handle("POST /api/v1/auth/totp/setup", s.authed(s.totpSetup))
	mux.Handle("POST /api/v1/auth/totp/enable", s.authed(s.totpEnable))
	mux.Handle("POST /api/v1/auth/totp/disable", s.authed(s.totpDisable))
	mux.Handle("GET /api/v1/auth/avatar", s.authed(s.avatarGet))
	mux.Handle("PUT /api/v1/auth/avatar", s.authed(s.avatarPut))
	mux.Handle("DELETE /api/v1/auth/avatar", s.authed(s.avatarDelete))

	// Real-time
	mux.Handle("POST /api/v1/ws/ticket", s.authed(s.wsTicket))
	mux.HandleFunc("GET /ws", s.wsConnect)

	// System
	mux.Handle("GET /api/v1/system/info", s.authed(s.systemInfo))
	mux.Handle("GET /api/v1/system/metrics", s.authed(s.systemMetrics))
	mux.Handle("GET /api/v1/system/backup", s.admin(s.systemBackup))
	mux.Handle("GET /api/v1/system/logs", s.admin(s.systemLogs))
	mux.Handle("GET /api/v1/system/update", s.admin(s.systemUpdate))
	// Host control: network radios and power (admin only, audited).
	mux.Handle("GET /api/v1/system/network", s.admin(s.networkState))
	mux.Handle("POST /api/v1/system/network", s.admin(s.networkSet))
	mux.Handle("GET /api/v1/system/wifi", s.admin(s.wifiList))
	mux.Handle("POST /api/v1/system/wifi/connect", s.admin(s.wifiConnect))
	mux.Handle("POST /api/v1/system/wifi/disconnect", s.admin(s.wifiDisconnect))
	mux.Handle("POST /api/v1/system/network/ipv4", s.admin(s.ipv4Set))
	mux.Handle("POST /api/v1/system/network/ipv4/keep", s.admin(s.ipv4Keep))
	mux.Handle("POST /api/v1/system/network/ipv4/revert", s.admin(s.ipv4Revert))
	mux.Handle("GET /api/v1/system/power", s.authed(s.powerInfo))

	// Users & roles (admin). Real Linux accounts on native Linux installs.
	mux.Handle("GET /api/v1/users", s.admin(s.usersList))
	mux.Handle("POST /api/v1/users", s.admin(s.usersCreate))
	mux.Handle("PATCH /api/v1/users/{name}", s.admin(s.usersUpdate))
	mux.Handle("DELETE /api/v1/users/{name}", s.admin(s.usersDelete))
	mux.Handle("POST /api/v1/system/power", s.admin(s.powerAction))

	// Containers (admin only: controlling containers is root-equivalent)
	mux.Handle("GET /api/v1/containers", s.admin(s.listContainers))
	mux.Handle("GET /api/v1/containers/{id}", s.admin(s.inspectContainer))
	mux.Handle("POST /api/v1/containers/{id}/{action}", s.admin(s.containerAction))
	mux.Handle("POST /api/v1/containers", s.admin(s.containerCreate))
	mux.Handle("GET /api/v1/containers/{id}/spec", s.admin(s.containerSpec))
	mux.Handle("PUT /api/v1/containers/{id}/spec", s.admin(s.containerEdit))
	mux.Handle("DELETE /api/v1/containers/{id}", s.admin(s.containerRemove))
	mux.Handle("GET /api/v1/docker/networks", s.admin(s.dockerNetworks))
	mux.Handle("POST /api/v1/docker/networks", s.admin(s.dockerNetworkCreate))
	mux.Handle("DELETE /api/v1/docker/networks/{name}", s.admin(s.dockerNetworkDelete))
	mux.Handle("GET /api/v1/docker/volumes", s.admin(s.dockerVolumes))
	mux.Handle("GET /api/v1/docker/hostpath", s.admin(s.dockerHostPath))
	mux.Handle("POST /api/v1/docker/compose/install", s.admin(s.installCompose))
	mux.Handle("GET /api/v1/stacks", s.admin(s.listStacks))
	mux.Handle("POST /api/v1/stacks", s.admin(s.createStack))
	mux.Handle("GET /api/v1/stacks/{name}", s.admin(s.getStack))
	mux.Handle("PUT /api/v1/stacks/{name}", s.admin(s.updateStack))
	mux.Handle("DELETE /api/v1/stacks/{name}", s.admin(s.deleteStack))
	mux.Handle("GET /api/v1/stacks/{name}/logs", s.admin(s.stackLogs))
	mux.Handle("POST /api/v1/stacks/{name}/{action}", s.admin(s.stackAction))
	mux.Handle("GET /api/v1/images", s.admin(s.listImages))

	// App Store: one-click installs (admin, audited).
	mux.Handle("GET /api/v1/appstore", s.admin(s.appstoreList))
	mux.Handle("GET /api/v1/appstore/jobs/{job}", s.admin(s.appstoreJob))
	mux.Handle("POST /api/v1/appstore/{id}/{action}", s.admin(s.appstoreAction))

	// Quick Script Launcher: saved scripts run on the host (admin, audited).
	mux.Handle("GET /api/v1/scripts", s.admin(s.scriptsList))
	mux.Handle("POST /api/v1/scripts", s.admin(s.scriptsCreate))
	mux.Handle("PUT /api/v1/scripts/{id}", s.admin(s.scriptsUpdate))
	mux.Handle("DELETE /api/v1/scripts/{id}", s.admin(s.scriptsDelete))
	mux.Handle("POST /api/v1/scripts/{id}/run", s.admin(s.scriptsRun))
	mux.Handle("GET /api/v1/scripts/runs/{run}", s.admin(s.scriptsRunGet))
	mux.Handle("POST /api/v1/scripts/runs/{run}/cancel", s.admin(s.scriptsRunCancel))

	// Terminal: PTY shells into containers / the host (admin only). The WS is
	// authorized by a single-use ticket since it can't carry a bearer header.
	mux.Handle("GET /api/v1/terminal/info", s.admin(s.terminalInfo))
	mux.Handle("POST /api/v1/terminal/ticket", s.admin(s.terminalTicket))
	mux.HandleFunc("GET /ws/terminal", s.terminalConnect)

	// Brave: a real Brave container streamed into a window. Start/status are
	// admin+JWT; the proxy (and its VNC WebSocket) is gated by a path-scoped
	// cookie the iframe sends automatically.
	mux.Handle("POST /api/v1/apps/brave/start", s.admin(s.braveStart))
	mux.Handle("GET /api/v1/apps/brave/status", s.admin(s.braveStatus))
	mux.HandleFunc("GET /apps/brave", s.braveRedirect)
	mux.HandleFunc("/apps/brave/", s.braveProxy)

	// Remote Desktop: real RDP through guacd. Credentials ride in a single-use
	// ticket; the WebSocket speaks the Guacamole protocol (admin only).
	mux.Handle("GET /api/v1/rdp/status", s.admin(s.rdpStatus))
	mux.Handle("POST /api/v1/rdp/ticket", s.admin(s.rdpTicket))
	mux.HandleFunc("GET /ws/rdp", s.rdpConnect)

	// Files (admin only for now; per-user shares come with multi-user storage)
	mux.Handle("GET /api/v1/files/roots", s.admin(s.filesRoots))
	mux.Handle("GET /api/v1/files/list", s.admin(s.filesList))
	mux.Handle("GET /api/v1/files/search", s.admin(s.filesSearch))
	mux.Handle("POST /api/v1/files/conflicts", s.admin(s.filesConflicts))
	mux.Handle("GET /api/v1/files/jobs", s.admin(s.filesJobs))
	mux.Handle("GET /api/v1/files/jobs/{id}", s.admin(s.filesJob))
	mux.Handle("POST /api/v1/files/jobs/{id}/cancel", s.admin(s.filesJobCancel))
	mux.Handle("GET /api/v1/files/text", s.admin(s.filesReadText))
	mux.Handle("PUT /api/v1/files/text", s.admin(s.touchIndex(s.filesWriteText)))
	mux.Handle("POST /api/v1/files/mkdir", s.admin(s.touchIndex(s.filesMkdir)))
	mux.Handle("POST /api/v1/files/rename", s.admin(s.touchIndex(s.filesRename)))
	mux.Handle("POST /api/v1/files/delete", s.admin(s.touchIndex(s.filesDelete)))
	mux.Handle("POST /api/v1/files/transfer", s.admin(s.touchIndex(s.filesTransfer)))
	mux.Handle("POST /api/v1/files/upload", s.admin(s.touchIndex(s.filesUpload)))
	mux.Handle("POST /api/v1/files/ticket", s.admin(s.filesTicket))
	mux.Handle("GET /api/v1/files/recent", s.admin(s.filesRecent))
	mux.Handle("GET /api/v1/files/favorites", s.admin(s.filesFavorites))
	mux.Handle("PUT /api/v1/files/favorites", s.admin(s.filesSetFavorites))
	mux.Handle("GET /api/v1/files/external", s.admin(s.filesExternal))
	mux.Handle("POST /api/v1/files/compress", s.admin(s.filesCompress))
	mux.Handle("POST /api/v1/files/extract", s.admin(s.filesExtract))
	mux.HandleFunc("GET /api/v1/files/raw", s.filesRaw) // authorized by a scoped ticket

	mux.Handle("GET /api/v1/cloud", s.admin(s.cloudOverview))
	mux.Handle("POST /api/v1/cloud/install", s.admin(s.cloudInstall))
	mux.Handle("POST /api/v1/cloud/accounts", s.admin(s.cloudAddAccount))
	mux.Handle("POST /api/v1/cloud/signin/start", s.admin(s.cloudSignInStart))
	mux.Handle("POST /api/v1/cloud/signin/finish", s.admin(s.cloudSignInFinish))
	mux.Handle("DELETE /api/v1/cloud/accounts/{id}", s.admin(s.cloudDeleteAccount))
	mux.Handle("GET /api/v1/cloud/accounts/{id}/folders", s.admin(s.cloudFolders))
	mux.Handle("POST /api/v1/cloud/imports", s.admin(s.cloudSaveImport))
	mux.Handle("PUT /api/v1/cloud/imports/{id}", s.admin(s.cloudSaveImport))
	mux.Handle("DELETE /api/v1/cloud/imports/{id}", s.admin(s.cloudDeleteImport))
	mux.Handle("POST /api/v1/cloud/imports/{id}/run", s.admin(s.cloudRunImport))
	mux.Handle("GET /api/v1/cloud/jobs/{job}", s.admin(s.cloudJob))
	mux.Handle("POST /api/v1/cloud/jobs/{job}/cancel", s.admin(s.cloudCancel))

	mux.Handle("GET /api/v1/netdrives", s.admin(s.netOverview))
	mux.Handle("POST /api/v1/netdrives", s.admin(s.netAdd))
	mux.Handle("POST /api/v1/netdrives/install", s.admin(s.netInstall))
	mux.Handle("GET /api/v1/netdrives/exports", s.admin(s.netExports))
	mux.Handle("POST /api/v1/netdrives/{id}/{action}", s.admin(s.netConnect))
	mux.Handle("PATCH /api/v1/netdrives/{id}", s.admin(s.netUpdate))
	mux.Handle("DELETE /api/v1/netdrives/{id}", s.admin(s.netDelete))
	mux.Handle("PUT /api/v1/sharing/password", s.admin(s.sharePassword))
	mux.Handle("PUT /api/v1/sharing", s.admin(s.shareProtocols))
	mux.Handle("POST /api/v1/sharing/shares", s.admin(s.shareAdd))
	mux.Handle("PATCH /api/v1/sharing/shares/{id}", s.admin(s.shareUpdate))
	mux.Handle("DELETE /api/v1/sharing/shares/{id}", s.admin(s.shareDelete))
	// WebDAV for other devices: its own Basic-auth sharing account (any method).
	dav := s.NetDrives.DAVHandler(func(r *http.Request) string { return clientIP(r, s.Config.TrustedProxies) })
	mux.Handle(netdrive.DAVPrefix+"/", dav)
	mux.Handle(netdrive.DAVPrefix, http.RedirectHandler(netdrive.DAVPrefix+"/", http.StatusMovedPermanently))

	mux.Handle("GET /api/v1/backup", s.admin(s.backupOverview))
	mux.Handle("POST /api/v1/backup/install", s.admin(s.backupInstall))
	mux.Handle("POST /api/v1/backup/repos", s.admin(s.backupAddRepo))
	mux.Handle("DELETE /api/v1/backup/repos/{id}", s.admin(s.backupDeleteRepo))
	mux.Handle("POST /api/v1/backup/repos/{id}/key", s.admin(s.backupRepoKey))
	mux.Handle("POST /api/v1/backup/plans", s.admin(s.backupSavePlan))
	mux.Handle("PUT /api/v1/backup/plans/{id}", s.admin(s.backupSavePlan))
	mux.Handle("DELETE /api/v1/backup/plans/{id}", s.admin(s.backupDeletePlan))
	mux.Handle("POST /api/v1/backup/plans/{id}/run", s.admin(s.backupRunPlan))
	mux.Handle("GET /api/v1/backup/plans/{id}/snapshots", s.admin(s.backupSnapshots))
	mux.Handle("GET /api/v1/backup/plans/{id}/snapshots/{snap}/ls", s.admin(s.backupBrowse))
	mux.Handle("GET /api/v1/backup/plans/{id}/snapshots/{snap}/dump", s.admin(s.backupDump))
	mux.Handle("POST /api/v1/backup/restore", s.admin(s.touchIndex(s.backupRestore)))
	mux.Handle("GET /api/v1/backup/jobs/{job}", s.admin(s.backupJob))
	mux.Handle("POST /api/v1/backup/jobs/{job}/cancel", s.admin(s.backupCancel))

	mux.Handle("GET /api/v1/photos", s.admin(s.photosList))
	mux.Handle("POST /api/v1/photos/tickets", s.admin(s.photosTickets))
	mux.HandleFunc("GET /api/v1/photos/thumb", s.photosThumb) // authorized by a Photos ticket
	mux.Handle("POST /api/v1/photos/favorite", s.admin(s.photosFavorite))
	mux.Handle("POST /api/v1/photos/delete", s.admin(s.touchIndex(s.photosDelete)))
	mux.Handle("GET /api/v1/photos/albums", s.admin(s.photosAlbums))
	mux.Handle("POST /api/v1/photos/albums", s.admin(s.photosAlbumCreate))
	mux.Handle("PATCH /api/v1/photos/albums/{id}", s.admin(s.photosAlbumRename))
	mux.Handle("DELETE /api/v1/photos/albums/{id}", s.admin(s.photosAlbumDelete))
	mux.Handle("POST /api/v1/photos/albums/{id}/items", s.admin(s.photosAlbumItems))
	mux.Handle("PUT /api/v1/photos/albums/{id}/cover", s.admin(s.photosAlbumCover))
	mux.Handle("GET /api/v1/photos/info", s.admin(s.photosInfo))
	mux.Handle("GET /api/v1/photos/trash", s.admin(s.photosTrash))
	mux.Handle("POST /api/v1/photos/trash/restore", s.admin(s.touchIndex(s.photosRestore)))
	mux.Handle("POST /api/v1/photos/trash/purge", s.admin(s.touchIndex(s.photosPurge)))

	// AI Assistant. Providers hold API keys, so configuring them is admin-only;
	// chatting and personal memory are per-user.
	mux.Handle("GET /api/v1/ai/providers", s.authed(s.aiListProviders))
	mux.Handle("POST /api/v1/ai/providers", s.admin(s.aiCreateProvider))
	mux.Handle("PUT /api/v1/ai/providers/{id}", s.admin(s.aiUpdateProvider))
	mux.Handle("DELETE /api/v1/ai/providers/{id}", s.admin(s.aiDeleteProvider))
	mux.Handle("GET /api/v1/ai/models", s.authed(s.aiModels))
	mux.Handle("GET /api/v1/ai/running", s.authed(s.aiRunning))
	mux.Handle("POST /api/v1/ai/load", s.authed(s.aiLoadModel))
	mux.Handle("POST /api/v1/ai/unload", s.authed(s.aiUnloadModel))

	mux.Handle("GET /api/v1/ai/conversations", s.authed(s.aiListConversations))
	mux.Handle("POST /api/v1/ai/conversations", s.authed(s.aiCreateConversation))
	mux.Handle("GET /api/v1/ai/conversations/{id}", s.authed(s.aiGetConversation))
	mux.Handle("PATCH /api/v1/ai/conversations/{id}", s.authed(s.aiPatchConversation))
	mux.Handle("DELETE /api/v1/ai/conversations/{id}", s.authed(s.aiDeleteConversation))
	mux.Handle("POST /api/v1/ai/conversations/{id}/send", s.authed(s.aiSend))

	mux.Handle("GET /api/v1/ai/memory", s.authed(s.aiListMemory))
	mux.Handle("POST /api/v1/ai/memory", s.authed(s.aiAddMemory))
	mux.Handle("PUT /api/v1/ai/memory/{id}", s.authed(s.aiUpdateMemory))
	mux.Handle("DELETE /api/v1/ai/memory/{id}", s.authed(s.aiDeleteMemory))
	mux.Handle("PUT /api/v1/ai/memory-enabled", s.authed(s.aiSetMemoryEnabled))

	// Storage: disks, SMART, ZFS pools, datasets and snapshots (admin, audited).
	s.storageRoutes(mux)

	// Notification Center (admin).
	s.notifyRoutes(mux)

	// Everything else: JSON 404 under /api, the embedded UI for GET/HEAD.
	// A single catch-all avoids "GET /" vs "/api/" pattern conflicts.
	ui := staticHandler(web.Assets())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			ui.ServeHTTP(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	})

	return s.recoverer(s.requestLogger(securityHeaders(mux)))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "database": "down"})
		return
	}
	dockerState := "up"
	if err := s.Docker.Ping(ctx); err != nil {
		dockerState = "down" // degraded, but restarting alfad would not fix it
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "docker": dockerState})
}

func (s *Server) audit(r *http.Request, userID, action, target string, ok bool, detail string) {
	err := s.Store.Audit(r.Context(), store.AuditEntry{
		UserID: userID, Action: action, Target: target,
		IP: clientIP(r, s.Config.TrustedProxies), Success: ok, Detail: detail,
	})
	if err != nil {
		s.Log.Error("audit write failed", "action", action, "err", err)
	}
}
