/* NoCap Shell host: shared declarations. See shell/PROTOCOL.md. */
#ifndef NOCAP_SHELL_H
#define NOCAP_SHELL_H

#include <gtk/gtk.h>
#include <json-glib/json-glib.h>
#include <webkit2/webkit2.h>

#include "bridge.h"

#ifndef NOCAP_VERSION
#define NOCAP_VERSION "dev"
#endif

typedef enum {
  SURF_DESKTOP, /* NoCap OS's desktop: wallpaper, widgets, icons (bottom layer) */
  SURF_CHROME,  /* NoCap OS's menu bar, dock, launcher, panels (top layer) */
  SURF_POLKIT,  /* the admin password prompt (overlay) */
  SURF_GREETER, /* the sign-in screen */
  SURF_APP,     /* one NoCap OS app in its own window (nocap-shell --app) */
  SURF_COUNT
} SurfaceId;

#define SM(s) (1u << (s))
#define SM_ALL ((1u << SURF_COUNT) - 1)

typedef struct {
  GtkApplication *app;
  gboolean greeter; /* --greeter mode */
  gboolean test;    /* NOCAP_SHELL_TEST=1 */
  gboolean debug;   /* NOCAP_SHELL_DEBUG=1 */
  char *shell_dir;  /* resolved bundle directory */
  char *bin_dir;    /* directory holding this executable */
  char *self_exe;
  char *socket_path; /* alfad socket */

  /* account and preferences (alfad, falling back to the Linux account) */
  char *user;
  char *real_name;
  char *voice;
  char *theme;
  char *web_url;
  char *api_origin; /* alfad's loopback listener, e.g. http://127.0.0.1:8780 */
  gboolean admin;
  /* --app mode: the NoCap OS app this window shows */
  char *app_id;
  char *app_props;
  GtkWidget *app_window;
} Shell;

extern Shell shell;

/* ---- replies and events (surfaces.c) ---- */
typedef struct {
  SurfaceId surface;
  gint64 id;
} ShellReply;

ShellReply *shell_reply_new(SurfaceId surface, gint64 id);
void shell_reply_ok(ShellReply *r, JsonNode *data);       /* takes data, frees r; r may be NULL */
void shell_reply_fail(ShellReply *r, const char *message); /* frees r; r may be NULL */
void shell_emit(guint surface_mask, const char *event, JsonNode *payload); /* takes payload */

/* ---- surfaces (surfaces.c) ---- */
void surfaces_create(void);
const char *surface_name(SurfaceId id);
int surface_from_name(const char *name);
void surface_show(SurfaceId id);
void surface_hide(SurfaceId id);
void surface_toggle(SurfaceId id);
gboolean surface_is_visible(SurfaceId id);
void surfaces_info_called(SurfaceId id); /* test-mode readiness */
void surfaces_create_app(void);           /* --app mode: one app window */
void surface_set_input(SurfaceId id, JsonArray *rects, gboolean full);
void surface_set_zones(int top, int bottom, int left, int right);
void surface_set_keyboard(SurfaceId id, const char *mode);
gboolean app_window_action(const char *action, GError **error);
void app_window_title(const char *title);
void app_window_props(const char *props); /* a second launch asked for these */

/* ---- uri scheme (scheme.c) ---- */
void scheme_register(WebKitWebContext *ctx);
char *icon_url_for_gicon(GIcon *icon, int size);
char *icon_url_for_name(const char *name_or_path, int size);

/* ---- dispatch (dispatch.c) ---- */
void dispatch_call(SurfaceId surface, BridgeCall *call);
JsonNode *shell_info_json(SurfaceId surface);
void shell_settings_changed(void);

/* ---- apps and dock (apps.c) ---- */
void apps_init(void);
JsonNode *apps_list_json(void);
gboolean apps_launch(const char *id, GError **error);
JsonNode *dock_get_json(void);
JsonNode *dock_set(JsonArray *pinned, GError **error);

/* ---- windows (toplevel.c) ---- */
void toplevel_init(void);
gboolean toplevel_available(void);
JsonNode *toplevel_list_json(void);
gboolean toplevel_action(const char *id, const char *action, GError **error);

/* ---- system: audio, brightness, network, power (system.c) ---- */
typedef void (*AudioCb)(gboolean available, int volume, gboolean muted, gpointer data);
typedef void (*BrightnessCb)(gboolean available, int percent, gpointer data);
typedef void (*DoneCb)(gboolean ok, const char *error, gpointer data);
void system_init(void);
void audio_get(AudioCb cb, gpointer data);
void audio_set(int volume, int muted, AudioCb cb, gpointer data); /* -1 = leave */
JsonNode *audio_json(gboolean available, int volume, gboolean muted);
void brightness_get(BrightnessCb cb, gpointer data);
void brightness_set(int percent, BrightnessCb cb, gpointer data);
JsonNode *brightness_json(gboolean available, int percent);
JsonNode *network_json(void);
void power_action(const char *action, DoneCb cb, gpointer data);

/* ---- notifications (notify.c) ---- */
void notify_init(void);
JsonNode *notify_list_json(void);
gboolean notify_dismiss(const char *id);
gboolean notify_invoke(const char *id, const char *key);
void notify_clear(void);
void notify_read(const char *id); /* NULL = all */
void notify_merge_remote(JsonArray *items); /* items from alfad */

/* ---- alfad client (alfad.c) ---- */
typedef void (*AlfadSessionCb)(JsonObject *session, gpointer data); /* session may be NULL */
void alfad_start(void);
void alfad_session(gboolean refresh, AlfadSessionCb cb, gpointer data);

/* ---- polkit agent (polkit.c) ---- */
void polkit_agent_start(void);
gboolean polkit_respond(const char *cookie, const char *password, GError **error);
gboolean polkit_cancel(const char *cookie, GError **error);

/* ---- greeter (greeter.c) ---- */
JsonNode *greeter_users_json(void);
void greeter_login(const char *username, ShellReply *r);
void greeter_respond(const char *response, gboolean has_response, ShellReply *r);
void greeter_cancel(ShellReply *r);
void greeter_power(const char *action, ShellReply *r);

/* ---- control (control.c) ---- */
void control_own(void);
int control_client(const char *action, const char *arg);
void control_osd(const char *what);

/* ---- app windows (appwin.c) ---- */
int appwin_run(const char *app, const char *props, gboolean single, int argc, char **argv);
gboolean app_id_valid(const char *id);

/* ---- util (util.c) ---- */
typedef void (*CmdCb)(char *output, gboolean success, gpointer data); /* output owned by callee */
void run_command(const char *const *argv, guint timeout_ms, CmdCb cb, gpointer data);
const char *find_tool(const char *name);
gboolean spawn_detached(const char *const *argv, GError **error);
char *json_for_script(JsonNode *node);
JsonNode *json_str_node(const char *s);
JsonNode *jobj_node(JsonObject *o); /* takes o */
JsonNode *jarr_node(JsonArray *a);  /* takes a */
void secure_wipe(void *p, gsize n);
char *utf8_truncate(const char *s, gsize max_bytes);

#endif
