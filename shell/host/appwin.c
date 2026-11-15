/* `nocap-shell --app NAME`: NoCapOS (this machine's own web UI on the
 * loopback address) in a normal app window: NoCapOS Settings, the App
 * Store, Files. Only that loopback origin is ever loaded in the window;
 * other links open in the default browser. */
#include "shell.h"

#include <string.h>

typedef struct {
  const char *name;
  const char *title;
  const char *route;
  const char *icon;
} AppDef;

static const AppDef defs[] = {
  {"settings", "NoCapOS Settings", "#/app/settings", "nocapos-settings"},
  {"appstore", "NoCapOS App Store", "#/app/appstore", "nocapos-appstore"},
  {"files", "NoCapOS Files", "#/app/files", "nocapos"},
  {"home", "NoCapOS", "", "nocapos"},
};

static char *origin; /* scheme://host:port */
static GtkWidget *win;
static WebKitWebView *view;
static gboolean tls_allowed;

static char *
origin_of(const char *uri)
{
  GUri *u = g_uri_parse(uri, G_URI_FLAGS_NONE, NULL);
  if (u == NULL)
    return NULL;
  const char *host = g_uri_get_host(u);
  char *o = NULL;
  if (host != NULL)
    o = g_strdup_printf("%s://%s:%d", g_uri_get_scheme(u), host,
                        g_uri_get_port(u) > 0 ? g_uri_get_port(u)
                                              : (g_strcmp0(g_uri_get_scheme(u), "https") == 0 ? 443 : 80));
  g_uri_unref(u);
  return o;
}

static gboolean
same_origin(const char *uri)
{
  char *o = uri ? origin_of(uri) : NULL;
  gboolean ok = o != NULL && g_strcmp0(o, origin) == 0;
  g_free(o);
  return ok;
}

static gboolean
loopback_host(const char *host)
{
  return g_strcmp0(host, "127.0.0.1") == 0 || g_strcmp0(host, "::1") == 0 || g_strcmp0(host, "[::1]") == 0;
}

static gboolean
on_decide(WebKitWebView *v, WebKitPolicyDecision *d, WebKitPolicyDecisionType type, gpointer u)
{
  if (type == WEBKIT_POLICY_DECISION_TYPE_RESPONSE) {
    webkit_policy_decision_use(d);
    return TRUE;
  }
  WebKitNavigationAction *a =
      webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(d));
  const char *uri = webkit_uri_request_get_uri(webkit_navigation_action_get_request(a));
  if (type == WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION && same_origin(uri)) {
    webkit_policy_decision_use(d);
    return TRUE;
  }
  if (uri != NULL && (g_str_has_prefix(uri, "https://") || g_str_has_prefix(uri, "http://")) &&
      webkit_navigation_action_is_user_gesture(a) && !same_origin(uri))
    g_app_info_launch_default_for_uri(uri, NULL, NULL);
  else if (uri != NULL && type == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION && same_origin(uri))
    webkit_web_view_load_uri(v, uri);
  webkit_policy_decision_ignore(d);
  return TRUE;
}

/* The loopback address's self-signed certificate is accepted, nothing else. */
static gboolean
on_tls_error(WebKitWebView *v, const char *failing_uri, GTlsCertificate *cert, GTlsCertificateFlags errors,
             gpointer u)
{
  GUri *uri = g_uri_parse(failing_uri, G_URI_FLAGS_NONE, NULL);
  gboolean ok = uri != NULL && loopback_host(g_uri_get_host(uri)) && same_origin(failing_uri) && !tls_allowed;
  if (ok) {
    tls_allowed = TRUE;
    webkit_web_context_allow_tls_certificate_for_host(webkit_web_view_get_context(v), cert,
                                                      g_uri_get_host(uri));
    webkit_web_view_load_uri(v, failing_uri);
  }
  if (uri)
    g_uri_unref(uri);
  return ok;
}

static gboolean
on_permission(WebKitWebView *v, WebKitPermissionRequest *req, gpointer u)
{
  /* Notifications are shown by the desktop itself; nothing else is asked. */
  webkit_permission_request_deny(req);
  return TRUE;
}

static void
on_title(WebKitWebView *v, GParamSpec *p, gpointer data)
{
  const AppDef *def = data;
  gtk_window_set_title(GTK_WINDOW(win), def->title);
}

static void
on_app_activate(GtkApplication *app, gpointer data)
{
  const AppDef *def = data;
  if (win != NULL) {
    gtk_window_present(GTK_WINDOW(win));
    return;
  }
  JsonObject *s = alfad_session_sync(10);
  const char *url = NULL;
  if (s != NULL) {
    JsonNode *n = json_object_get_member(s, "webURL");
    if (n && JSON_NODE_HOLDS_VALUE(n) && json_node_get_value_type(n) == G_TYPE_STRING)
      url = json_node_get_string(n);
  }
  char *base = NULL;
  if (url != NULL && (g_str_has_prefix(url, "https://127.0.0.1") || g_str_has_prefix(url, "http://127.0.0.1")))
    base = g_strdup(url);
  else
    base = g_strdup("https://127.0.0.1/");
  if (s)
    json_object_unref(s);
  origin = origin_of(base);

  WebKitWebContext *ctx = webkit_web_context_new();
  WebKitSettings *st = webkit_settings_new();
  webkit_settings_set_enable_developer_extras(st, shell.debug);
  webkit_settings_set_javascript_can_open_windows_automatically(st, FALSE);
  webkit_settings_set_allow_file_access_from_file_urls(st, FALSE);
  webkit_settings_set_allow_universal_access_from_file_urls(st, FALSE);
  view = WEBKIT_WEB_VIEW(g_object_new(WEBKIT_TYPE_WEB_VIEW, "web-context", ctx, "settings", st, NULL));
  g_object_unref(st);
  g_object_unref(ctx);
  g_signal_connect(view, "decide-policy", G_CALLBACK(on_decide), NULL);
  g_signal_connect(view, "load-failed-with-tls-errors", G_CALLBACK(on_tls_error), NULL);
  g_signal_connect(view, "permission-request", G_CALLBACK(on_permission), NULL);
  g_signal_connect(view, "notify::title", G_CALLBACK(on_title), (gpointer)def);

  win = gtk_application_window_new(app);
  gtk_window_set_title(GTK_WINDOW(win), def->title);
  gtk_window_set_icon_name(GTK_WINDOW(win), def->icon);
  gtk_window_set_default_size(GTK_WINDOW(win), 1180, 760);
  gtk_container_add(GTK_CONTAINER(win), GTK_WIDGET(view));

  char *start = g_strdup_printf("%s%s%s", origin, "/", def->route);
  webkit_web_view_load_uri(view, start);
  g_free(start);
  g_free(base);
  gtk_widget_show_all(win);
}

int
appwin_run(const char *app, int argc, char **argv)
{
  const AppDef *def = NULL;
  for (gsize i = 0; i < G_N_ELEMENTS(defs); i++)
    if (g_strcmp0(defs[i].name, app) == 0)
      def = &defs[i];
  if (def == NULL) {
    g_printerr("nocap-shell: unknown app '%s'\n", app ? app : "");
    return 2;
  }
  if (!gtk_init_check(&argc, &argv)) {
    g_printerr("nocap-shell: needs a Wayland session\n");
    return 1;
  }
  char *id = g_strdup_printf("io.nocapos.App.%s", def->name);
  /* One window per app: a second launch raises the first. */
  GtkApplication *ga = gtk_application_new(id, G_APPLICATION_FLAGS_NONE);
  g_free(id);
  g_set_prgname(def->name);
  g_signal_connect(ga, "activate", G_CALLBACK(on_app_activate), (gpointer)def);
  shell.app = ga;
  char *fake_argv[] = {argv[0], NULL};
  int status = g_application_run(G_APPLICATION(ga), 1, fake_argv);
  g_object_unref(ga);
  return status;
}
