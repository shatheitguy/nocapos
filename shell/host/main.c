/* nocap-shell: the NoCap Desktop shell host (see shell/PROTOCOL.md).
 *
 *   nocap-shell                 NoCap OS on the screen (desktop and chrome)
 *   nocap-shell --greeter       the sign-in screen (greetd greeter)
 *   nocap-shell --app ID [--props JSON] [--single]
 *                               one NoCap OS app in its own window
 *   nocap-shell --toggle|--show|--hide launcher|spotlight|center|notifications,
 *               --osd WHAT      control the running shell (keybindings)
 */
#include "shell.h"

#include <gdk/gdkwayland.h>
#include <pwd.h>
#include <string.h>
#include <unistd.h>

Shell shell;

static const char *DEFAULT_SHELL_DIR = "/usr/share/nocapos/shell";

static void
usage(void)
{
  g_printerr("usage: nocap-shell [--greeter | --app ID [--props JSON] [--single] |\n"
             "                   --toggle|--show|--hide launcher|spotlight|center|notifications |\n"
             "                   --osd volume-up|volume-down|volume-mute|brightness-up|brightness-down]\n");
}

static void
resolve_paths(const char *argv0)
{
  char *exe = g_file_read_link("/proc/self/exe", NULL);
  if (exe == NULL)
    exe = g_strdup(argv0);
  shell.self_exe = exe;
  shell.bin_dir = g_path_get_dirname(exe);

  const char *dir = g_getenv("NOCAP_SHELL_DIR");
  if (dir == NULL || *dir == '\0')
    dir = DEFAULT_SHELL_DIR;
  char *real = realpath(dir, NULL);
  shell.shell_dir = g_strdup(real != NULL ? real : dir);
  free(real);

  const char *sock = g_getenv("NOCAP_SHELL_SOCKET");
  shell.socket_path = g_strdup(sock != NULL && *sock ? sock : "/run/nocapos/shell.sock");
}

/* Until alfad answers: the Linux account itself, Gen Z voice, dark. */
static void
account_defaults(void)
{
  struct passwd *pw = getpwuid(getuid());
  shell.user = g_strdup(pw != NULL ? pw->pw_name : g_get_user_name());
  char *real = NULL;
  if (pw != NULL && pw->pw_gecos != NULL && pw->pw_gecos[0] != '\0') {
    char **parts = g_strsplit(pw->pw_gecos, ",", 2);
    if (parts[0] != NULL && parts[0][0] != '\0')
      real = g_strdup(parts[0]);
    g_strfreev(parts);
  }
  shell.real_name = real != NULL ? real : g_strdup(shell.user);
  shell.voice = g_strdup("genz");
  shell.theme = g_strdup("dark");
  shell.web_url = NULL;
  shell.api_origin = g_strdup("http://127.0.0.1:8780");
}

static void
on_activate(GtkApplication *app, gpointer u)
{
  static gboolean started;
  if (started)
    return;
  started = TRUE;
  /* The surfaces keep the application alive (no windows close it). */
  g_application_hold(G_APPLICATION(app));

  surfaces_create();
  if (shell.greeter)
    return;
  alfad_start();
  apps_init();
  toplevel_init();
  system_init();
  notify_init();
  control_own();
  idle_init();
  polkit_agent_start();
}

int
main(int argc, char **argv)
{
  const char *mode = NULL, *arg = NULL, *props = NULL;
  gboolean single = FALSE;
  for (int i = 1; i < argc; i++) {
    const char *a = argv[i];
    if (a[0] == 0)
      continue;
    if (strcmp(a, "--greeter") == 0) {
      mode = "greeter";
    } else if (strcmp(a, "--props") == 0 && i + 1 < argc) {
      props = argv[++i];
    } else if (strcmp(a, "--single") == 0) {
      single = TRUE;
    } else if ((strcmp(a, "--app") == 0 || strcmp(a, "--toggle") == 0 || strcmp(a, "--show") == 0 ||
                strcmp(a, "--hide") == 0 || strcmp(a, "--osd") == 0) &&
               i + 1 < argc) {
      mode = a + 2;
      arg = argv[++i];
    } else if (strcmp(a, "--help") == 0 || strcmp(a, "-h") == 0) {
      usage();
      return 0;
    } else if (strcmp(a, "--version") == 0) {
      g_print("nocap-shell %s\n", NOCAP_VERSION);
      return 0;
    } else {
      g_printerr("nocap-shell: unknown option '%s'\n", a);
      usage();
      return 2;
    }
  }

  if (getuid() == 0 && g_strcmp0(g_getenv("NOCAP_SHELL_ALLOW_ROOT"), "1") != 0) {
    g_printerr("nocap-shell: refusing to run as root\n");
    return 1;
  }

  if (mode != NULL && (strcmp(mode, "toggle") == 0 || strcmp(mode, "show") == 0 ||
                       strcmp(mode, "hide") == 0 || strcmp(mode, "osd") == 0))
    return control_client(mode, arg);

  shell.test = g_strcmp0(g_getenv("NOCAP_SHELL_TEST"), "1") == 0;
  shell.debug = g_strcmp0(g_getenv("NOCAP_SHELL_DEBUG"), "1") == 0;
  resolve_paths(argv[0]);
  account_defaults();

  /* The shell is a Wayland client only (layer-shell). */
  gdk_set_allowed_backends("wayland");
  if (mode != NULL && strcmp(mode, "app") == 0)
    return appwin_run(arg, props, single, argc, argv);

  if (!gtk_init_check(&argc, &argv) || !GDK_IS_WAYLAND_DISPLAY(gdk_display_get_default())) {
    g_printerr("nocap-shell: needs a Wayland session (no Wayland display found)\n");
    return 1;
  }

  shell.greeter = mode != NULL && strcmp(mode, "greeter") == 0;
  /* NON_UNIQUE: a second copy (another session, a restart) never talks to
   * the first; control goes over io.nocapos.Shell instead. */
  shell.app = gtk_application_new(shell.greeter ? "io.nocapos.Greeter" : "io.nocapos.ShellHost",
                                  G_APPLICATION_NON_UNIQUE);
  g_signal_connect(shell.app, "activate", G_CALLBACK(on_activate), NULL);
  char *fake_argv[] = {argv[0], NULL};
  int status = g_application_run(G_APPLICATION(shell.app), 1, fake_argv);
  g_object_unref(shell.app);
  return status;
}
