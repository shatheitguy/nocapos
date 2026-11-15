/* Layer-shell surfaces: native wallpaper plus one locked-down web view per
 * surface, all sharing one web process. */
#include "shell.h"

#include <gtk-layer-shell.h>
#include <string.h>

typedef struct {
  const char *name;
  GtkLayerShellLayer layer;
  gboolean top, bottom, left, right;
  int width, height; /* -1 = stretched by anchors */
  int zone;
  GtkLayerShellKeyboardMode keyboard;
  gboolean on_demand;
  int margin_bottom;
} SurfaceConf;

static const SurfaceConf conf[SURF_COUNT] = {
  [SURF_BAR] = {"bar", GTK_LAYER_SHELL_LAYER_TOP, TRUE, FALSE, TRUE, TRUE, -1, 32, 32,
                GTK_LAYER_SHELL_KEYBOARD_MODE_NONE, FALSE, 0},
  [SURF_DOCK] = {"dock", GTK_LAYER_SHELL_LAYER_TOP, FALSE, TRUE, TRUE, TRUE, -1, 76, 76,
                 GTK_LAYER_SHELL_KEYBOARD_MODE_NONE, FALSE, 0},
  [SURF_LAUNCHER] = {"launcher", GTK_LAYER_SHELL_LAYER_OVERLAY, TRUE, TRUE, TRUE, TRUE, -1, -1, -1,
                     GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE, TRUE, 0},
  [SURF_CENTER] = {"center", GTK_LAYER_SHELL_LAYER_OVERLAY, TRUE, TRUE, FALSE, TRUE, 380, -1, 0,
                   GTK_LAYER_SHELL_KEYBOARD_MODE_ON_DEMAND, TRUE, 0},
  [SURF_BANNERS] = {"banners", GTK_LAYER_SHELL_LAYER_OVERLAY, TRUE, FALSE, FALSE, TRUE, 380, 1, 0,
                    GTK_LAYER_SHELL_KEYBOARD_MODE_NONE, TRUE, 0},
  [SURF_OSD] = {"osd", GTK_LAYER_SHELL_LAYER_OVERLAY, FALSE, TRUE, FALSE, FALSE, 260, 72, 0,
                GTK_LAYER_SHELL_KEYBOARD_MODE_NONE, TRUE, 24},
  [SURF_POLKIT] = {"polkit", GTK_LAYER_SHELL_LAYER_OVERLAY, TRUE, TRUE, TRUE, TRUE, -1, -1, -1,
                   GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE, TRUE, 0},
  [SURF_GREETER] = {"greeter", GTK_LAYER_SHELL_LAYER_TOP, TRUE, TRUE, TRUE, TRUE, -1, -1, -1,
                    GTK_LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE, FALSE, 0},
};

typedef struct {
  SurfaceId id;
  GtkWidget *win;
  WebKitWebView *view;
  gboolean want_visible;
  gboolean loaded;
  gboolean info_called;
  gboolean ready_printed;
  guint hide_timer;
  guint restarts;
  gint64 restart_window;
} Surface;

static Surface surf[SURF_COUNT];
static WebKitWebContext *web_ctx;
static WebKitSettings *web_settings;
static WebKitWebView *first_view;
static GtkWidget *wallpaper_win;
static guint replace_id;

#define SHELL_PAGE_PREFIX "nocap-shell://shell/"

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
  for (int i = 0; i < SURF_COUNT; i++)
    if (surf[i].view != NULL && !surf[i].ready_printed)
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
emit_visible(Surface *s, gboolean visible)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "name", conf[s->id].name);
  json_object_set_boolean_member(o, "visible", visible);
  shell_emit(SM_ALL, "surface.visible", jobj_node(o));
}

void
surface_show(SurfaceId id)
{
  if (id >= SURF_COUNT || surf[id].win == NULL)
    return;
  Surface *s = &surf[id];
  s->want_visible = TRUE;
  if (!gtk_widget_get_visible(s->win)) {
    gtk_widget_show_all(s->win);
    emit_visible(s, TRUE);
  }
  if (conf[id].keyboard != GTK_LAYER_SHELL_KEYBOARD_MODE_NONE)
    gtk_widget_grab_focus(GTK_WIDGET(s->view));
}

void
surface_hide(SurfaceId id)
{
  if (id >= SURF_COUNT || surf[id].win == NULL)
    return;
  Surface *s = &surf[id];
  if (s->hide_timer) {
    g_source_remove(s->hide_timer);
    s->hide_timer = 0;
  }
  if (!conf[id].on_demand)
    return; /* bar, dock and greeter always stay */
  s->want_visible = FALSE;
  if (gtk_widget_get_visible(s->win)) {
    gtk_widget_hide(s->win);
    emit_visible(s, FALSE);
  }
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

static gboolean
hide_timeout(gpointer data)
{
  Surface *s = data;
  s->hide_timer = 0;
  surface_hide(s->id);
  return G_SOURCE_REMOVE;
}

void
surface_show_for(SurfaceId id, guint ms)
{
  if (id >= SURF_COUNT || surf[id].win == NULL)
    return;
  surface_show(id);
  Surface *s = &surf[id];
  if (s->hide_timer)
    g_source_remove(s->hide_timer);
  s->hide_timer = g_timeout_add(ms, hide_timeout, s);
}

void
surface_set_height(SurfaceId id, int height)
{
  if (id >= SURF_COUNT || surf[id].win == NULL || height < 1)
    return;
  int w = conf[id].width > 0 ? conf[id].width : -1;
  gtk_widget_set_size_request(surf[id].win, w, height);
  gtk_window_resize(GTK_WINDOW(surf[id].win), w > 0 ? w : 1, height);
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
  if (wallpaper_win != NULL) {
    gtk_layer_set_monitor(GTK_WINDOW(wallpaper_win), m);
    if (!gtk_widget_get_visible(wallpaper_win))
      gtk_widget_show_all(wallpaper_win);
  }
  for (int i = 0; i < SURF_COUNT; i++) {
    Surface *s = &surf[i];
    if (s->win == NULL)
      continue;
    gtk_layer_set_monitor(GTK_WINDOW(s->win), m);
    if (s->want_visible && !gtk_widget_get_visible(s->win))
      gtk_widget_show_all(s->win);
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
  } else if (type == WEBKIT_POLICY_DECISION_TYPE_RESPONSE) {
    uri = webkit_uri_request_get_uri(
        webkit_response_policy_decision_get_request(WEBKIT_RESPONSE_POLICY_DECISION(d)));
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

static gboolean
on_context_menu(WebKitWebView *v, WebKitContextMenu *menu, GdkEvent *ev, WebKitHitTestResult *hit,
                gpointer u)
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
    check_ready(s);
  }
}

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
  WebKitWebContext *ctx = webkit_web_context_new_ephemeral();
  webkit_web_context_set_cache_model(ctx, WEBKIT_CACHE_MODEL_DOCUMENT_VIEWER);
  webkit_web_context_set_spell_checking_enabled(ctx, FALSE);

  /* Every http(s) load goes to a closed local port and fails. */
  WebKitNetworkProxySettings *proxy = webkit_network_proxy_settings_new("http://127.0.0.1:9", NULL);
  webkit_website_data_manager_set_network_proxy_settings(
      webkit_web_context_get_website_data_manager(ctx), WEBKIT_NETWORK_PROXY_MODE_CUSTOM, proxy);
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
  webkit_settings_set_javascript_can_access_clipboard(st, FALSE);
  webkit_settings_set_allow_file_access_from_file_urls(st, FALSE);
  webkit_settings_set_allow_universal_access_from_file_urls(st, FALSE);
  webkit_settings_set_enable_write_console_messages_to_stdout(st, shell.debug);
  webkit_settings_set_enable_page_cache(st, FALSE);
  webkit_settings_set_enable_media_stream(st, FALSE);
  webkit_settings_set_enable_encrypted_media(st, FALSE);
  webkit_settings_set_enable_back_forward_navigation_gestures(st, FALSE);
  webkit_settings_set_media_playback_requires_user_gesture(st, TRUE);
#if WEBKIT_CHECK_VERSION(2, 38, 0)
  webkit_settings_set_enable_webrtc(st, FALSE);
#endif
  return st;
}

static void
add_transparent_css(void)
{
  GtkCssProvider *css = gtk_css_provider_new();
  gtk_css_provider_load_from_data(css,
                                  "window.nocap-surface, window.nocap-surface > * {"
                                  " background-color: transparent; }",
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
create_surface(SurfaceId id)
{
  const SurfaceConf *c = &conf[id];
  Surface *s = &surf[id];
  s->id = id;

  char *ns = g_strdup_printf("nocap-%s", c->name);
  s->win = make_layer_window(ns, c->layer);
  g_free(ns);
  GtkWindow *w = GTK_WINDOW(s->win);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_TOP, c->top);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_BOTTOM, c->bottom);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_LEFT, c->left);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_RIGHT, c->right);
  gtk_layer_set_exclusive_zone(w, c->zone);
  gtk_layer_set_keyboard_mode(w, c->keyboard);
  if (c->margin_bottom)
    gtk_layer_set_margin(w, GTK_LAYER_SHELL_EDGE_BOTTOM, c->margin_bottom);
  gtk_widget_set_size_request(s->win, c->width > 0 ? c->width : -1, c->height > 0 ? c->height : -1);

  WebKitUserContentManager *ucm = webkit_user_content_manager_new();
  g_signal_connect(ucm, "script-message-received::nocap", G_CALLBACK(on_script_message), s);
  webkit_user_content_manager_register_script_message_handler(ucm, "nocap");

  if (first_view == NULL)
    s->view = WEBKIT_WEB_VIEW(g_object_new(WEBKIT_TYPE_WEB_VIEW, "web-context", web_ctx, "settings",
                                           web_settings, "user-content-manager", ucm, NULL));
  else
    s->view = WEBKIT_WEB_VIEW(g_object_new(WEBKIT_TYPE_WEB_VIEW, "related-view", first_view,
                                           "settings", web_settings, "user-content-manager", ucm,
                                           NULL));
  g_object_unref(ucm);
  if (first_view == NULL)
    first_view = s->view;

  GdkRGBA clear = {0, 0, 0, 0};
  webkit_web_view_set_background_color(s->view, &clear);
  g_signal_connect(s->view, "decide-policy", G_CALLBACK(on_decide_policy), s);
  g_signal_connect(s->view, "permission-request", G_CALLBACK(on_permission), s);
  g_signal_connect(s->view, "context-menu", G_CALLBACK(on_context_menu), s);
  g_signal_connect(s->view, "create", G_CALLBACK(on_create), s);
  g_signal_connect(s->view, "run-file-chooser", G_CALLBACK(on_block2), s);
  g_signal_connect(s->view, "show-notification", G_CALLBACK(on_block2), s);
  g_signal_connect(s->view, "enter-fullscreen", G_CALLBACK(on_block1), s);
  g_signal_connect(s->view, "print", G_CALLBACK(on_block2), s);
  g_signal_connect(s->view, "load-changed", G_CALLBACK(on_load_changed), s);
  g_signal_connect(s->view, "load-failed", G_CALLBACK(on_load_failed), s);
  g_signal_connect(s->view, "web-process-terminated", G_CALLBACK(on_web_process_terminated), s);

  gtk_container_add(GTK_CONTAINER(s->win), GTK_WIDGET(s->view));
  gtk_widget_show(GTK_WIDGET(s->view));

  char *uri = surface_uri(id);
  webkit_web_view_load_uri(s->view, uri);
  g_free(uri);

  s->want_visible = !c->on_demand;
  if (s->want_visible)
    gtk_widget_show_all(s->win);
}

/* ---------- wallpaper (native) ---------- */

static GdkPixbuf *wp_src;
static cairo_surface_t *wp_cache;
static int wp_w, wp_h, wp_scale;

static void
wallpaper_load(void)
{
  const char *env = g_getenv("NOCAP_WALLPAPER");
  const char *path = (env != NULL && *env) ? env : "/usr/share/backgrounds/nocapos/tide.jpg";
  if (!g_file_test(path, G_FILE_TEST_IS_REGULAR))
    return;
  int w = 0, h = 0;
  if (gdk_pixbuf_get_file_info(path, &w, &h) == NULL)
    return;
  GError *err = NULL;
  if ((gint64)w * h > 7680 * 4320)
    wp_src = gdk_pixbuf_new_from_file_at_scale(path, 7680, 4320, TRUE, &err);
  else
    wp_src = gdk_pixbuf_new_from_file(path, &err);
  if (wp_src == NULL) {
    g_warning("wallpaper %s: %s", path, err ? err->message : "unreadable");
    g_clear_error(&err);
  }
}

static gboolean
wallpaper_draw(GtkWidget *w, cairo_t *cr, gpointer u)
{
  int W = gtk_widget_get_allocated_width(w);
  int H = gtk_widget_get_allocated_height(w);
  int sf = gtk_widget_get_scale_factor(w);
  cairo_set_source_rgb(cr, 5 / 255.0, 5 / 255.0, 8 / 255.0);
  cairo_paint(cr);
  if (wp_src == NULL || W <= 0 || H <= 0)
    return TRUE;

  if (wp_cache == NULL || wp_w != W || wp_h != H || wp_scale != sf) {
    g_clear_pointer(&wp_cache, cairo_surface_destroy);
    int sw = gdk_pixbuf_get_width(wp_src), sh = gdk_pixbuf_get_height(wp_src);
    int pw = W * sf, ph = H * sf;
    double k = MAX((double)pw / sw, (double)ph / sh);
    int dw = MAX(pw, (int)(sw * k + 0.999));
    int dh = MAX(ph, (int)(sh * k + 0.999));
    GdkPixbuf *scaled = gdk_pixbuf_scale_simple(wp_src, dw, dh, GDK_INTERP_BILINEAR);
    if (scaled == NULL)
      return TRUE;
    GdkPixbuf *crop = gdk_pixbuf_new_subpixbuf(scaled, (dw - pw) / 2, (dh - ph) / 2, pw, ph);
    wp_cache = gdk_cairo_surface_create_from_pixbuf(crop, sf, gtk_widget_get_window(w));
    g_object_unref(crop);
    g_object_unref(scaled);
    wp_w = W;
    wp_h = H;
    wp_scale = sf;
  }
  cairo_set_source_surface(cr, wp_cache, 0, 0);
  cairo_paint(cr);
  return TRUE;
}

static void
create_wallpaper(void)
{
  wallpaper_load();
  wallpaper_win = make_layer_window("nocap-wallpaper", GTK_LAYER_SHELL_LAYER_BACKGROUND);
  GtkWindow *w = GTK_WINDOW(wallpaper_win);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_TOP, TRUE);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_BOTTOM, TRUE);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_LEFT, TRUE);
  gtk_layer_set_anchor(w, GTK_LAYER_SHELL_EDGE_RIGHT, TRUE);
  gtk_layer_set_exclusive_zone(w, -1);
  gtk_layer_set_keyboard_mode(w, GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);
  GtkWidget *area = gtk_drawing_area_new();
  g_signal_connect(area, "draw", G_CALLBACK(wallpaper_draw), NULL);
  gtk_container_add(GTK_CONTAINER(wallpaper_win), area);
  gtk_widget_show_all(wallpaper_win);
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

  create_wallpaper();
  if (shell.greeter) {
    create_surface(SURF_GREETER);
    return;
  }
  for (int i = 0; i < SURF_COUNT; i++)
    if (i != SURF_GREETER)
      create_surface(i);
}
