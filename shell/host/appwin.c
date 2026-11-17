/* `nocap-shell --app ID [--props JSON] [--single]`: one NoCap OS app (Files,
 * Terminal, Settings, the App Store, Photos, ...) in a native window of its
 * own. The page is the same NoCap OS build as everywhere (from local files),
 * showing just that app with the same title bar as in the browser; it talks
 * to alfad on the loopback address with the person's session, like the
 * desktop does. Apps that have one window (--single) raise it when opened
 * again, and take the new props (a Settings section, say). */
#include "shell.h"

#include <string.h>

gboolean
app_id_valid(const char *id)
{
  if (id == NULL || *id == 0 || strlen(id) > 32)
    return FALSE;
  for (const char *p = id; *p; p++)
    if (!((*p >= 'a' && *p <= 'z') || (*p >= '0' && *p <= '9')))
      return FALSE;
  return TRUE;
}

static gboolean props_valid(const char *props)
{
  if (props == NULL)
    return TRUE;
  if (strlen(props) > 8192 || !g_utf8_validate(props, -1, NULL))
    return FALSE;
  for (const unsigned char *p = (const unsigned char *)props; *p; p++)
    if (*p < 0x20 || *p == 0x7f)
      return FALSE;
  return TRUE;
}

static void
on_app_activate(GtkApplication *app, gpointer data)
{
  if (shell.app_window != NULL) {
    gtk_window_present(GTK_WINDOW(shell.app_window));
    return;
  }
  surfaces_create_app();
}

/* A second launch of a one-window app: its props come as the "file". */
static void
on_app_open(GApplication *app, GFile **files, gint n, const char *hint, gpointer data)
{
  if (shell.app_window == NULL)
    surfaces_create_app();
  for (gint i = 0; i < n; i++) {
    char *uri = g_file_get_uri(files[i]);
    const char *prefix = "nocap-app:props/";
    if (uri != NULL && g_str_has_prefix(uri, prefix)) {
      char *props = g_uri_unescape_string(uri + strlen(prefix), NULL);
      if (props_valid(props))
        app_window_props(props);
      g_free(props);
    }
    g_free(uri);
  }
  if (n == 0)
    app_window_props(NULL);
}

int
appwin_run(const char *app, const char *props, gboolean single, int argc, char **argv)
{
  if (!app_id_valid(app) || !props_valid(props)) {
    g_printerr("nocap-shell: unknown app '%s'\n", app ? app : "");
    return 2;
  }
  if (!gtk_init_check(&argc, &argv)) {
    g_printerr("nocap-shell: needs a Wayland session\n");
    return 1;
  }
  shell.app_id = g_strdup(app);
  shell.app_props = g_strdup(props ? props : "");
  /* The window's app id: labwc matches its window rules (NoCap OS draws
   * this window's title bar) and the dock its running apps on it. */
  char *id = g_strdup_printf("io.nocapos.App.%s", app);
  g_set_prgname(id);
  GApplicationFlags flags = single ? G_APPLICATION_HANDLES_OPEN : G_APPLICATION_NON_UNIQUE;
  GtkApplication *ga = gtk_application_new(id, flags);
  g_free(id);
  g_signal_connect(ga, "activate", G_CALLBACK(on_app_activate), NULL);
  g_signal_connect(ga, "open", G_CALLBACK(on_app_open), NULL);
  shell.app = ga;
  alfad_start();
  int status;
  if (single && props != NULL && *props) {
    char *esc = g_uri_escape_string(props, NULL, FALSE);
    char *uri = g_strdup_printf("nocap-app:props/%s", esc);
    char *fake_argv[] = {argv[0], uri, NULL};
    status = g_application_run(G_APPLICATION(ga), 2, fake_argv);
    g_free(uri);
    g_free(esc);
  } else {
    char *fake_argv[] = {argv[0], NULL};
    status = g_application_run(G_APPLICATION(ga), 1, fake_argv);
  }
  g_object_unref(ga);
  return status;
}
