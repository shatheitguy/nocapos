/* NoCap OS on the physical screen. The desktop is the same NoCap OS build
 * as in the browser, from local files (the nocap-shell:// scheme), shown in
 * locked-down web views that share one web process:
 *
 *   desktop  bottom layer, full screen: wallpaper, widgets, desktop menu
 *   chrome   top layer, full screen and see-through: the menu bar, dock,
 *            launcher, panels and notifications. Only the parts the page
 *            reports (surface.input) take clicks; the rest go to the windows
 *            below. Two invisible spacer surfaces keep maximized windows
 *            clear of the menu bar and dock (surface.zones).
 *   polkit   overlay: the admin password prompt
 *   greeter  the sign-in screen (nocap-shell --greeter)
 *   app      a normal window with one NoCap OS app (nocap-shell --app ID);
 *            the page draws the title bar (the same one as in the browser)
 *
 * Nothing from the network loads: only the scheme's files and alfad on the
 * loopback address (every other address goes to a dead proxy, and the CSP
 * says the same). */
#include "shell.h"

#include <gtk-layer-shell.h>
#include <string.h>

typedef struct {
  const char *name;
  gboolean layer; /* a layer-shell surface (else a normal window) */
  GtkLayerShellLayer level;
  GtkLayerShellKeyboardMode keyboard;
  gboolean on_demand; /* hidden until asked for */
  gboolean opaque;    /* paints the whole surface (else see-through) */
} SurfaceConf;

static const SurfaceConf conf[SURF_COUNT] = {
  [SURF_DESKTOP] = {"desktop", TRUE, GTK_LAYER_SHELL_LAYER_BOTTOM, GTK_LAYER_SHELL_KEYBOARD_MODE_ON_DEMAND, FALSE, TRUE},
  [SURF_CHROME] = {"chrome", TRUE, GTK_LAYER_SHELL_LAYER_TOP, GTK_LAYER_SHELL_KEYBOARD_MODE_ON_DEMAND, FALSE, FALSE},
  [SURF_POLKIT] = {"polkit", TRUE, GTK_LAYER_SHELL_LAYER_OVERLAY, GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE, TRUE, FALSE},
  [SURF_GREETER] = {"greeter", TRUE, GTK_LAYER_SHELL_LAYER_TOP, GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE, FALSE, TRUE},
  [SURF_APP] = {"app", FALSE, 0, 0, FALSE, FALSE},
};

typedef struct {
  SurfaceId id;
  GtkWidget *win;
  WebKitWebView *view;
  gboolean want_visible;
  gboolean loaded;
  gboolean shown_once;
  gboolean info_called;
  gboolean ready_printed;
  guint restarts;
  gint64 restart_window;
  /* the last pointer press on the view (window moves and resizes) */
  double press_x, press_y;
  guint press_button;
  guint32 press_time;
} Surface;

static Surface surf[SURF_COUNT];
static WebKitWebContext *web_ctx;
static WebKitSettings *web_settings;
static WebKitWebView *first_view;
static GtkWidget *zone_win[4]; /* top, bottom, left, right */
static guint replace_id;

#define SHELL_PAGE_PREFIX "nocap-shell://shell/"
#define BASE_COLOR "#050508"

const char *
surface_name(SurfaceId id)
{
  return id < SURF_COUNT ? conf[id].name : "";
}

int
surface_from_name(const char *name)
{
  for (int i = 0; i < SURF_COUNT; i++)
    if (name != NULL && strcmp(conf[i].name, name) == 0)
      return i;
  return -1;
}

/* ---------- replies and events ---------- */

static void
run_script(Surface *s, const char *script)
{
  if (s->view == NULL)
    return;
  const char *uri = webkit_web_view_get_uri(s->view);
  if (uri == NULL || !g_str_has_prefix(uri, SHELL_PAGE_PREFIX))
    return;
#if WEBKIT_CHECK_VERSION(2, 40, 0)
  webkit_web_view_evaluate_javascript(s->view, script, -1, NULL, NULL, NULL, NULL, NULL);
#else
  webkit_web_view_run_javascript(s->view, script, NULL, NULL, NULL);
#endif
}

ShellReply *
shell_reply_new(SurfaceId surface, gint64 id)
{
  ShellReply *r = g_new0(ShellReply, 1);
  r->surface = surface;
  r->id = id;
  return r;
}

static void
reply_send(ShellReply *r, gboolean ok, JsonNode *data)
{
  if (r != NULL && r->surface < SURF_COUNT && r->id >= 0) {
    char *json = json_for_script(data);
    char *script = g_strdup_printf("window.__nocap&&window.__nocap.reply(%" G_GINT64_FORMAT ",%s,%s);",
                                   r->id, ok ? "true" : "false", json);
    run_script(&surf[r->surface], script);
    secure_wipe(script, strlen(script));
    secure_wipe(json, strlen(json));
    g_free(script);
    g_free(json);
  }
  if (data != NULL)
    json_node_unref(data);
  g_free(r);
}

void
shell_reply_ok(ShellReply *r, JsonNode *data)
{
  if (data == NULL)
    data = jobj_node(json_object_new());
  reply_send(r, TRUE, data);
}

void
shell_reply_fail(ShellReply *r, const char *message)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "error", message ? message : "failed");
  reply_send(r, FALSE, jobj_node(o));
}

void
shell_emit(guint mask, const char *event, JsonNode *payload)
{
  JsonNode *name = json_str_node(event);
  char *ename = json_for_script(name);
  char *data = json_for_script(payload);
  char *script = g_strdup_printf("window.__nocap&&window.__nocap.event(%s,%s);", ename, data);
  for (int i = 0; i < SURF_COUNT; i++)
    if ((mask & SM(i)) && surf[i].view != NULL)
      run_script(&surf[i], script);
  g_free(script);
  g_free(data);
  g_free(ename);
  json_node_unref(name);
  if (payload != NULL)
    json_node_unref(payload);
}

/* ---------- test readiness ---------- */

static void
check_ready(Surface *s)
{
  if (!shell.test || s->ready_printed || !s->loaded || !s->info_called)
    return;
  s->ready_printed = TRUE;
  g_print("nocap-shell: ready %s\n", conf[s->id].name);
  /* Surfaces shown on demand (the admin prompt) load when first shown. */
  for (int i = 0; i < SURF_COUNT; i++)
    if (surf[i].view != NULL && !surf[i].ready_printed && !conf[i].on_demand)
      return;
  g_print("nocap-shell: all ready\n");
}

void
surfaces_info_called(SurfaceId id)
{
  if (id >= SURF_COUNT)
    return;
  surf[id].info_called = TRUE;
  check_ready(&surf[id]);
}

/* ---------- show / hide ---------- */

static void
present(Surface *s)
{
  /* Never before the page has drawn once: no blank or white flash. */
  if (!s->loaded)
    return;
  if (!gtk_widget_get_visible(s->win))
    gtk_widget_show_all(s->win);
  s->shown_once = TRUE;
}

void
surface_show(SurfaceId id)
{
  if (id >= SURF_COUNT || surf[id].win == NULL)
    return;
  Surface *s = &surf[id];
  s->want_visible = TRUE;
  present(s);
  if (conf[id].keyboard == GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE)
    gtk_widget_grab_focus(GTK_WIDGET(s->view));
}

void
surface_hide(SurfaceId id)
{
  if (id >= SURF_COUNT || surf[id].win == NULL || !conf[id].on_demand)
    return;
  surf[id].want_visible = FALSE;
  gtk_widget_hide(surf[id].win);
}

void
surface_toggle(SurfaceId id)
{
  if (surface_is_visible(id))
    surface_hide(id);
  else
    surface_show(id);
}

gboolean
surface_is_visible(SurfaceId id)
{
  return id < SURF_COUNT && surf[id].win != NULL && gtk_widget_get_visible(surf[id].win);
}

/* ---------- input region, zones, keyboard ---------- */

void
surface_set_input(SurfaceId id, JsonArray *rects, gboolean full)
{
  if (id >= SURF_COUNT || surf[id].win == NULL)
    return;
  GdkWindow *gw = gtk_widget_get_window(surf[id].win);
  if (gw == NULL)
    return;
  if (full) {
    gdk_window_input_shape_combine_region(gw, NULL, 0, 0);
    return;
  }
  cairo_region_t *region = cairo_region_create();
  guint n = rects ? json_array_get_length(rects) : 0;
  for (guint i = 0; i + 3 < n; i += 4) {
    cairo_rectangle_int_t r = {(int)json_array_get_int_element(rects, i), (int)json_array_get_int_element(rects, i + 1),
                               (int)json_array_get_int_element(rects, i + 2),
                               (int)json_array_get_int_element(rects, i + 3)};
    if (r.width > 0 && r.height > 0)
      cairo_region_union_rectangle(region, &r);
  }
  gdk_window_input_shape_combine_region(gw, region, 0, 0);
  cairo_region_destroy(region);
}

static void
zone_set(int i, int size)
{
  if (zone_win[i] == NULL)
    return;
  GtkWindow *w = GTK_WINDOW(zone_win[i]);
  gtk_layer_set_exclusive_zone(w, size);
  if (i < 2)
    gtk_widget_set_size_request(zone_win[i], -1, MAX(size, 1));
  else
    gtk_widget_set_size_request(zone_win[i], MAX(size, 1), -1);
  if (size > 0)
    gtk_widget_show_all(zone_win[i]);
  else
    gtk_widget_hide(zone_win[i]);
}

void
surface_set_zones(int top, int bottom, int left, int right)
{
  if (top >= 0)
    zone_set(0, top);
  if (bottom >= 0)
    zone_set(1, bottom);
  if (left >= 0)
    zone_set(2, left);
  if (right >= 0)
    zone_set(3, right);
}

void
surface_set_keyboard(SurfaceId id, const char *mode)
{
  if (id >= SURF_COUNT || surf[id].win == NULL || !conf[id].layer)
    return;
  GtkLayerShellKeyboardMode m = GTK_LAYER_SHELL_KEYBOARD_MODE_ON_DEMAND;
  if (g_strcmp0(mode, "none") == 0)
    m = GTK_LAYER_SHELL_KEYBOARD_MODE_NONE;
  else if (g_strcmp0(mode, "exclusive") == 0)
    m = GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE;
  gtk_layer_set_keyboard_mode(GTK_WINDOW(surf[id].win), m);
  if (m != GTK_LAYER_SHELL_KEYBOARD_MODE_NONE)
    gtk_widget_grab_focus(GTK_WIDGET(surf[id].view));
}

/* ---------- app window actions ---------- */

static GdkWindowEdge
edge_for(const char *a)
{
  if (strcmp(a, "resize-n") == 0)
    return GDK_WINDOW_EDGE_NORTH;
  if (strcmp(a, "resize-s") == 0)
    return GDK_WINDOW_EDGE_SOUTH;
  if (strcmp(a, "resize-e") == 0)
    return GDK_WINDOW_EDGE_EAST;
  if (strcmp(a, "resize-w") == 0)
    return GDK_WINDOW_EDGE_WEST;
  if (strcmp(a, "resize-ne") == 0)
    return GDK_WINDOW_EDGE_NORTH_EAST;
  if (strcmp(a, "resize-nw") == 0)
    return GDK_WINDOW_EDGE_NORTH_WEST;
  if (strcmp(a, "resize-se") == 0)
    return GDK_WINDOW_EDGE_SOUTH_EAST;
  return GDK_WINDOW_EDGE_SOUTH_WEST;
}

gboolean
app_window_action(const char *action, GError **error)
{
  Surface *s = &surf[SURF_APP];
  GtkWindow *w = s->win ? GTK_WINDOW(s->win) : NULL;
  if (w == NULL || action == NULL) {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_NOT_FOUND, "no window");
    return FALSE;
  }
  if (strcmp(action, "minimize") == 0)
    gtk_window_iconify(w);
  else if (strcmp(action, "maximize") == 0)
    gtk_window_maximize(w);
  else if (strcmp(action, "restore") == 0)
    gtk_window_unmaximize(w);
  else if (strcmp(action, "close") == 0)
    gtk_window_close(w);
  else if (strcmp(action, "fullscreen") == 0)
    gtk_window_fullscreen(w);
  else if (strcmp(action, "unfullscreen") == 0)
    gtk_window_unfullscreen(w);
  else if (strcmp(action, "move") == 0 || g_str_has_prefix(action, "resize-")) {
    /* Moves and resizes start from the press that asked for them. */
    if (s->press_button == 0) {
      g_set_error(error, G_IO_ERROR, G_IO_ERROR_FAILED, "no pointer press");
      return FALSE;
    }
    if (action[0] == 'm')
      gtk_window_begin_move_drag(w, (gint)s->press_button, (gint)s->press_x, (gint)s->press_y, s->press_time);
    else
      gtk_window_begin_resize_drag(w, edge_for(action), (gint)s->press_button, (gint)s->press_x, (gint)s->press_y,
                                   s->press_time);
    s->press_button = 0;
  } else {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_INVALID_ARGUMENT, "unknown action");
    return FALSE;
  }
  return TRUE;
}

void
app_window_title(const char *title)
{
  if (surf[SURF_APP].win != NULL && title != NULL)
    gtk_window_set_title(GTK_WINDOW(surf[SURF_APP].win), *title ? title : "NoCap OS");
}

void
app_window_props(const char *props)
{
  if (surf[SURF_APP].win == NULL)
    return;
  gtk_window_present(GTK_WINDOW(surf[SURF_APP].win));
  if (props != NULL && *props)
    shell_emit(SM(SURF_APP), "app.props", json_str_node(props));
}

static gboolean
on_button_press(GtkWidget *w, GdkEventButton *ev, gpointer data)
{
  Surface *s = data;
  s->press_x = ev->x_root;
  s->press_y = ev->y_root;
  s->press_button = ev->button;
  s->press_time = ev->time;
  return FALSE;
}

static gboolean
on_window_state(GtkWidget *w, GdkEventWindowState *ev, gpointer data)
{
  JsonObject *o = json_object_new();
  json_object_set_boolean_member(o, "maximized", (ev->new_window_state & GDK_WINDOW_STATE_MAXIMIZED) != 0);
  json_object_set_boolean_member(o, "fullscreen", (ev->new_window_state & GDK_WINDOW_STATE_FULLSCREEN) != 0);
  json_object_set_boolean_member(o, "focused", (ev->new_window_state & GDK_WINDOW_STATE_FOCUSED) != 0);
  json_object_set_boolean_member(o, "tiled", (ev->new_window_state & GDK_WINDOW_STATE_TILED) != 0);
  shell_emit(SM(SURF_APP), "window.state", jobj_node(o));
  return FALSE;
}

/* ---------- monitors ---------- */

static GdkMonitor *
pick_monitor(void)
{
  GdkDisplay *d = gdk_display_get_default();
  if (d == NULL || gdk_display_get_n_monitors(d) <= 0)
    return NULL;
  GdkMonitor *m = gdk_display_get_primary_monitor(d);
  return m != NULL ? m : gdk_display_get_monitor(d, 0);
}

static gboolean
replace_all(gpointer unused)
{
  replace_id = 0;
  GdkMonitor *m = pick_monitor();
  if (m == NULL)
    return G_SOURCE_REMOVE; /* wait for monitor-added */
  for (int i = 0; i < 4; i++)
    if (zone_win[i] != NULL)
      gtk_layer_set_monitor(GTK_WINDOW(zone_win[i]), m);
  for (int i = 0; i < SURF_COUNT; i++) {
    Surface *s = &surf[i];
    if (s->win == NULL || !conf[i].layer)
      continue;
    gtk_layer_set_monitor(GTK_WINDOW(s->win), m);
    if (s->want_visible)
      present(s);
  }
  return G_SOURCE_REMOVE;
}

static void
schedule_replace(void)
{
  if (replace_id == 0)
    replace_id = g_timeout_add(300, replace_all, NULL);
}

static void
on_monitors_changed(GdkDisplay *d, GdkMonitor *m, gpointer unused)
{
  schedule_replace();
}

/* The compositor closed a layer surface (its output went away): keep the
 * window, unmap it and place it again on the first monitor. */
static gboolean
on_delete(GtkWidget *w, GdkEvent *ev, gpointer unused)
{
  gtk_widget_hide(w);
  schedule_replace();
  return TRUE;
}

/* ---------- web views ---------- */

static gboolean
on_decide_policy(WebKitWebView *v, WebKitPolicyDecision *d, WebKitPolicyDecisionType type, gpointer u)
{
  const char *uri = NULL;
  if (type == WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION) {
    WebKitNavigationAction *a =
        webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(d));
    uri = webkit_uri_request_get_uri(webkit_navigation_action_get_request(a));
    /* Downloads and links to elsewhere never navigate the shell away. */
    if (uri != NULL && g_str_has_prefix(uri, SHELL_PAGE_PREFIX) &&
        webkit_navigation_action_get_navigation_type(a) == WEBKIT_NAVIGATION_TYPE_LINK_CLICKED) {
      webkit_policy_decision_ignore(d);
      return TRUE;
    }
  } else if (type == WEBKIT_POLICY_DECISION_TYPE_RESPONSE) {
    WebKitResponsePolicyDecision *rd = WEBKIT_RESPONSE_POLICY_DECISION(d);
    uri = webkit_uri_request_get_uri(webkit_response_policy_decision_get_request(rd));
    /* Subresources (fetches to alfad on loopback) are checked by the CSP and
     * the proxy; only the main page must be ours. */
    if (!webkit_response_policy_decision_is_main_frame_main_resource(rd)) {
      webkit_policy_decision_use(d);
      return TRUE;
    }
  }
  if (uri != NULL && g_str_has_prefix(uri, SHELL_PAGE_PREFIX))
    webkit_policy_decision_use(d);
  else
    webkit_policy_decision_ignore(d);
  return TRUE;
}

static gboolean
on_permission(WebKitWebView *v, WebKitPermissionRequest *req, gpointer u)
{
  webkit_permission_request_deny(req);
  return TRUE;
}

/* NoCap OS draws its own menus; WebKit's never shows. */
static gboolean
on_context_menu(WebKitWebView *v, WebKitContextMenu *menu, GdkEvent *ev, WebKitHitTestResult *hit, gpointer u)
{
  return !shell.debug;
}

static GtkWidget *
on_create(WebKitWebView *v, WebKitNavigationAction *a, gpointer u)
{
  return NULL;
}

static gboolean
on_block1(gpointer instance, gpointer user)
{
  return TRUE;
}

static gboolean
on_block2(gpointer instance, gpointer arg, gpointer user)
{
  return TRUE;
}

static void
on_load_changed(WebKitWebView *v, WebKitLoadEvent ev, gpointer data)
{
  Surface *s = data;
  if (ev == WEBKIT_LOAD_STARTED) {
    s->loaded = FALSE;
    s->info_called = FALSE;
  } else if (ev == WEBKIT_LOAD_FINISHED) {
    s->loaded = TRUE;
    if (s->want_visible)
      present(s);
    check_ready(s);
  }
}

static gboolean
reload_surface(gpointer data);

static gboolean
on_load_failed(WebKitWebView *v, WebKitLoadEvent ev, char *uri, GError *err, gpointer data)
{
  Surface *s = data;
  g_warning("surface %s: load of %s failed: %s", conf[s->id].name, uri, err ? err->message : "?");
  return FALSE;
}

static char *
surface_uri(SurfaceId id)
{
  if (id == SURF_APP) {
    char *app = g_uri_escape_string(shell.app_id ? shell.app_id : "", NULL, FALSE);
    char *props = g_uri_escape_string(shell.app_props ? shell.app_props : "", NULL, FALSE);
    char *u = g_strdup_printf(SHELL_PAGE_PREFIX "shell.html?surface=app&app=%s&props=%s", app, props);
    g_free(app);
    g_free(props);
    return u;
  }
  return g_strdup_printf(SHELL_PAGE_PREFIX "shell.html?surface=%s", conf[id].name);
}

static gboolean
reload_surface(gpointer data)
{
  Surface *s = data;
  char *uri = surface_uri(s->id);
  webkit_web_view_load_uri(s->view, uri);
  g_free(uri);
  return G_SOURCE_REMOVE;
}

static void
on_web_process_terminated(WebKitWebView *v, WebKitWebProcessTerminationReason reason, gpointer data)
{
  Surface *s = data;
  gint64 now = g_get_monotonic_time();
  if (now - s->restart_window > 60 * G_USEC_PER_SEC) {
    s->restart_window = now;
    s->restarts = 0;
  }
  if (++s->restarts > 5) {
    g_warning("surface %s: web process keeps failing, giving up", conf[s->id].name);
    return;
  }
  g_warning("surface %s: web process ended (%d), reloading", conf[s->id].name, (int)reason);
  g_timeout_add(1000, reload_surface, s);
}

static void
on_script_message(WebKitUserContentManager *ucm, WebKitJavascriptResult *res, gpointer data)
{
  Surface *s = data;
  const char *uri = webkit_web_view_get_uri(s->view);
  if (uri == NULL || !g_str_has_prefix(uri, SHELL_PAGE_PREFIX))
    return;
  JSCValue *value = webkit_javascript_result_get_js_value(res);
  if (value == NULL || !jsc_value_is_string(value)) {
    g_warning("surface %s: bridge message is not a string", conf[s->id].name);
    return;
  }
  char *text = jsc_value_to_string(value);
  if (text == NULL)
    return;
  gsize len = strlen(text);

  BridgeCall call;
  char *err = NULL;
  if (bridge_validate(conf[s->id].name, text, len, &call, &err)) {
    dispatch_call(s->id, &call);
  } else {
    if (shell.debug)
      g_message("surface %s: refused call: %s", conf[s->id].name, err);
    if (call.id >= 0)
      shell_reply_fail(shell_reply_new(s->id, call.id), err);
  }
  g_free(err);
  bridge_call_clear(&call);
  secure_wipe(text, len); /* may carry a password */
  g_free(text);
}

static WebKitWebContext *
make_context(void)
{
  /* The person's own NoCap OS keeps what it keeps in a browser (its local
   * storage: wallpapers they added, window and widget state) in their home;
   * the sign-in screen keeps nothing. */
  WebKitWebContext *ctx;
  if (shell.greeter) {
    ctx = webkit_web_context_new_ephemeral();
  } else {
    char *data = g_build_filename(g_get_user_data_dir(), "nocapos", "shell", NULL);
    char *cache = g_build_filename(g_get_user_cache_dir(), "nocapos", "shell", NULL);
    WebKitWebsiteDataManager *dm =
        webkit_website_data_manager_new("base-data-directory", data, "base-cache-directory", cache, NULL);
    ctx = webkit_web_context_new_with_website_data_manager(dm);
    g_object_unref(dm);
    g_free(data);
    g_free(cache);
  }
  webkit_web_context_set_cache_model(ctx, WEBKIT_CACHE_MODEL_DOCUMENT_VIEWER);
  webkit_web_context_set_spell_checking_enabled(ctx, FALSE);

  /* Every network load except alfad's loopback address goes to a closed
   * local port and fails: nothing remote ever loads. */
  const char *const direct[] = {"127.0.0.1", NULL};
  WebKitNetworkProxySettings *proxy = webkit_network_proxy_settings_new("http://127.0.0.1:9", direct);
  webkit_website_data_manager_set_network_proxy_settings(webkit_web_context_get_website_data_manager(ctx),
                                                         WEBKIT_NETWORK_PROXY_MODE_CUSTOM, proxy);
  webkit_network_proxy_settings_free(proxy);

  if (g_strcmp0(g_getenv("NOCAP_SHELL_SANDBOX"), "1") == 0)
    webkit_web_context_set_sandbox_enabled(ctx, TRUE);

  scheme_register(ctx);
  WebKitSecurityManager *sm = webkit_web_context_get_security_manager(ctx);
  webkit_security_manager_register_uri_scheme_as_secure(sm, "nocap-shell");
  webkit_security_manager_register_uri_scheme_as_cors_enabled(sm, "nocap-shell");
  return ctx;
}

static WebKitSettings *
make_settings(void)
{
  WebKitSettings *st = webkit_settings_new();
  webkit_settings_set_enable_developer_extras(st, shell.debug);
  webkit_settings_set_javascript_can_open_windows_automatically(st, FALSE);
  webkit_settings_set_javascript_can_access_clipboard(st, TRUE); /* copy/paste in NoCap apps */
  webkit_settings_set_allow_file_access_from_file_urls(st, FALSE);
  webkit_settings_set_allow_universal_access_from_file_urls(st, FALSE);
  webkit_settings_set_enable_write_console_messages_to_stdout(st, shell.debug);
  webkit_settings_set_enable_page_cache(st, FALSE);
  webkit_settings_set_enable_media_stream(st, FALSE);
  webkit_settings_set_enable_encrypted_media(st, FALSE);
  webkit_settings_set_enable_back_forward_navigation_gestures(st, FALSE);
  webkit_settings_set_media_playback_requires_user_gesture(st, FALSE);
  webkit_settings_set_enable_smooth_scrolling(st, TRUE);
  /* The graphics card draws the pages, except when the screen itself is
   * drawn by the processor (the software rendering fallback): then WebKit
   * paints in software too, which always works. */
  const char *renderer = g_getenv("WLR_RENDERER");
  gboolean software = g_strcmp0(renderer, "pixman") == 0 || g_strcmp0(g_getenv("LIBGL_ALWAYS_SOFTWARE"), "1") == 0 ||
                      g_file_test("/etc/nocapos/desktop-software-rendering", G_FILE_TEST_EXISTS);
  webkit_settings_set_hardware_acceleration_policy(
      st, software ? WEBKIT_HARDWARE_ACCELERATION_POLICY_NEVER : WEBKIT_HARDWARE_ACCELERATION_POLICY_ALWAYS);
#if WEBKIT_CHECK_VERSION(2, 38, 0)
  webkit_settings_set_enable_webrtc(st, FALSE);
#endif
  return st;
}

/* Native feel for the shell's own parts: no text selection or image and
 * link dragging on the desktop and its chrome (NoCap apps keep selection in
 * their content), no zoom. */
static const char NATIVE_CSS[] =
    "html[data-native-surface=desktop] *, html[data-native-surface=chrome] *"
    " { -webkit-user-select: none; user-select: none; }"
    "html[data-native-surface] input, html[data-native-surface] textarea,"
    " html[data-native-surface] [contenteditable=true] { -webkit-user-select: text; user-select: text; }"
    "html[data-native-surface] img, html[data-native-surface] a { -webkit-user-drag: none; }";

static void
add_native_css(WebKitUserContentManager *ucm)
{
  WebKitUserStyleSheet *ss = webkit_user_style_sheet_new(NATIVE_CSS, WEBKIT_USER_CONTENT_INJECT_TOP_FRAME,
                                                         WEBKIT_USER_STYLE_LEVEL_USER, NULL, NULL);
  webkit_user_content_manager_add_style_sheet(ucm, ss);
  webkit_user_style_sheet_unref(ss);
}

static void
add_transparent_css(void)
{
  GtkCssProvider *css = gtk_css_provider_new();
  gtk_css_provider_load_from_data(css,
                                  "window.nocap-surface, window.nocap-surface > * {"
                                  " background-color: transparent; }"
                                  "window.nocap-opaque { background-color: " BASE_COLOR "; }",
                                  -1, NULL);
  gtk_style_context_add_provider_for_screen(gdk_screen_get_default(), GTK_STYLE_PROVIDER(css),
                                            GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
  g_object_unref(css);
}

static GtkWidget *
make_layer_window(const char *ns, GtkLayerShellLayer layer)
{
  GtkWidget *w = gtk_window_new(GTK_WINDOW_TOPLEVEL);
  gtk_window_set_application(GTK_WINDOW(w), shell.app);
  gtk_window_set_title(GTK_WINDOW(w), ns);
  gtk_window_set_decorated(GTK_WINDOW(w), FALSE);
  gtk_widget_set_app_paintable(w, TRUE);
  GdkVisual *vis = gdk_screen_get_rgba_visual(gtk_widget_get_screen(w));
  if (vis != NULL)
    gtk_widget_set_visual(w, vis);
  gtk_style_context_add_class(gtk_widget_get_style_context(w), "nocap-surface");
  gtk_layer_init_for_window(GTK_WINDOW(w));
  gtk_layer_set_namespace(GTK_WINDOW(w), ns);
  gtk_layer_set_layer(GTK_WINDOW(w), layer);
  GdkMonitor *m = pick_monitor();
  if (m != NULL)
    gtk_layer_set_monitor(GTK_WINDOW(w), m);
  g_signal_connect(w, "delete-event", G_CALLBACK(on_delete), NULL);
  return w;
}

static void
anchor_all(GtkWindow *w)
{
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_TOP, TRUE);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_BOTTOM, TRUE);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_LEFT, TRUE);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_RIGHT, TRUE);
}

/* An invisible strip along one edge that only reserves space. */
static GtkWidget *
make_zone(int edge)
{
  static const char *const names[] = {"nocap-zone-top", "nocap-zone-bottom", "nocap-zone-left", "nocap-zone-right"};
  GtkWidget *w = make_layer_window(names[edge], GTK_LAYER_SHELL_LAYER_BOTTOM);
  GtkWindow *gw = GTK_WINDOW(w);
  gboolean horizontal = edge < 2;
  gtk_layer_set_anchor(gw, GTK_LAYER_SHELL_EDGE_TOP, edge != 1);
  gtk_layer_set_anchor(gw, GTK_LAYER_SHELL_EDGE_BOTTOM, edge != 0);
  gtk_layer_set_anchor(gw, GTK_LAYER_SHELL_EDGE_LEFT, edge != 3);
  gtk_layer_set_anchor(gw, GTK_LAYER_SHELL_EDGE_RIGHT, edge != 2);
  if (horizontal) {
    gtk_layer_set_anchor(gw, GTK_LAYER_SHELL_EDGE_LEFT, TRUE);
    gtk_layer_set_anchor(gw, GTK_LAYER_SHELL_EDGE_RIGHT, TRUE);
  }
  gtk_layer_set_keyboard_mode(gw, GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);
  gtk_layer_set_exclusive_zone(gw, 0);
  return w;
}

static void
setup_view(Surface *s)
{
  WebKitUserContentManager *ucm = webkit_user_content_manager_new();
  g_signal_connect(ucm, "script-message-received::nocap", G_CALLBACK(on_script_message), s);
  webkit_user_content_manager_register_script_message_handler(ucm, "nocap");
  add_native_css(ucm);

  if (first_view == NULL)
    s->view = WEBKIT_WEB_VIEW(g_object_new(WEBKIT_TYPE_WEB_VIEW, "web-context", web_ctx, "settings", web_settings,
                                           "user-content-manager", ucm, NULL));
  else
    s->view = WEBKIT_WEB_VIEW(g_object_new(WEBKIT_TYPE_WEB_VIEW, "related-view", first_view, "settings",
                                           web_settings, "user-content-manager", ucm, NULL));
  g_object_unref(ucm);
  if (first_view == NULL)
    first_view = s->view;

  GdkRGBA bg = {0, 0, 0, 0};
  if (conf[s->id].opaque)
    gdk_rgba_parse(&bg, BASE_COLOR);
  webkit_web_view_set_background_color(s->view, &bg);
  g_signal_connect(s->view, "decide-policy", G_CALLBACK(on_decide_policy), s);
  g_signal_connect(s->view, "permission-request", G_CALLBACK(on_permission), s);
  g_signal_connect(s->view, "context-menu", G_CALLBACK(on_context_menu), s);
  g_signal_connect(s->view, "create", G_CALLBACK(on_create), s);
  g_signal_connect(s->view, "show-notification", G_CALLBACK(on_block2), s);
  g_signal_connect(s->view, "enter-fullscreen", G_CALLBACK(on_block1), s);
  g_signal_connect(s->view, "print", G_CALLBACK(on_block2), s);
  g_signal_connect(s->view, "load-changed", G_CALLBACK(on_load_changed), s);
  g_signal_connect(s->view, "load-failed", G_CALLBACK(on_load_failed), s);
  g_signal_connect(s->view, "web-process-terminated", G_CALLBACK(on_web_process_terminated), s);
  g_signal_connect(s->view, "button-press-event", G_CALLBACK(on_button_press), s);
  gtk_container_add(GTK_CONTAINER(s->win), GTK_WIDGET(s->view));
  gtk_widget_show(GTK_WIDGET(s->view));

  char *uri = surface_uri(s->id);
  webkit_web_view_load_uri(s->view, uri);
  g_free(uri);
}

static void
create_surface(SurfaceId id)
{
  const SurfaceConf *c = &conf[id];
  Surface *s = &surf[id];
  s->id = id;

  char *ns = g_strdup_printf("nocap-%s", c->name);
  s->win = make_layer_window(ns, c->level);
  g_free(ns);
  if (c->opaque)
    gtk_style_context_add_class(gtk_widget_get_style_context(s->win), "nocap-opaque");
  GtkWindow *w = GTK_WINDOW(s->win);
  anchor_all(w);
  gtk_layer_set_exclusive_zone(w, -1); /* full screen, under or over the zones */
  gtk_layer_set_keyboard_mode(w, c->keyboard);
  setup_view(s);
  s->want_visible = !c->on_demand;
  /* The chrome takes no clicks until the page says where its parts are. */
  if (id == SURF_CHROME) {
    gtk_widget_realize(s->win);
    surface_set_input(id, NULL, FALSE);
  }
}

/* ---------- setup ---------- */

void
surfaces_create(void)
{
  add_transparent_css();
  web_ctx = make_context();
  web_settings = make_settings();

  GdkDisplay *d = gdk_display_get_default();
  g_signal_connect(d, "monitor-added", G_CALLBACK(on_monitors_changed), NULL);
  g_signal_connect(d, "monitor-removed", G_CALLBACK(on_monitors_changed), NULL);

  if (shell.greeter) {
    create_surface(SURF_GREETER);
    return;
  }
  for (int i = 0; i < 4; i++)
    zone_win[i] = make_zone(i);
  create_surface(SURF_DESKTOP);
  create_surface(SURF_CHROME);
  create_surface(SURF_POLKIT);
}

static gboolean
on_app_delete(GtkWidget *w, GdkEvent *ev, gpointer u)
{
  return FALSE; /* closes; the application quits with its last window */
}

void
surfaces_create_app(void)
{
  web_ctx = make_context();
  web_settings = make_settings();
  Surface *s = &surf[SURF_APP];
  s->id = SURF_APP;
  s->win = gtk_application_window_new(shell.app);
  shell.app_window = s->win;
  GtkWindow *w = GTK_WINDOW(s->win);
  /* NoCap OS draws the title bar itself, the same one as in the browser. */
  gtk_window_set_decorated(w, FALSE);
  gtk_window_set_title(w, "NoCap OS");
  gtk_window_set_icon_name(w, "nocapos");
  gtk_window_set_default_size(w, 1100, 720);
  GdkVisual *vis = gdk_screen_get_rgba_visual(gtk_widget_get_screen(s->win));
  if (vis != NULL)
    gtk_widget_set_visual(s->win, vis);
  gtk_widget_set_app_paintable(s->win, TRUE);
  add_transparent_css();
  gtk_style_context_add_class(gtk_widget_get_style_context(s->win), "nocap-surface");
  g_signal_connect(s->win, "delete-event", G_CALLBACK(on_app_delete), NULL);
  g_signal_connect(s->win, "window-state-event", G_CALLBACK(on_window_state), NULL);
  setup_view(s);
  s->want_visible = TRUE;
}
