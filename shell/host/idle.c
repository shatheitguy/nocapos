/* Keeping the screen awake while something is playing.
 *
 * Apps ask over the session bus (org.freedesktop.ScreenSaver, directly or
 * through the desktop portal's Inhibit, whose backend calls it) or over
 * Wayland (idle-inhibit, which labwc handles itself). The idle lock
 * (swayidle, nocap-idle.service) watches labwc's ext-idle-notify, which
 * labwc holds back while any Wayland idle inhibitor exists. So while any
 * app holds a ScreenSaver inhibit, NoCap Shell keeps one Wayland idle
 * inhibitor of its own: on a tiny invisible surface, mapped only then.
 *
 * Each inhibit belongs to the bus client that asked: only it can release
 * it, and all of them go when it leaves the bus (crashed, closed).
 * Sleeping before suspend isn't affected: the lock still happens then. */
#include "shell.h"

#include <gdk/gdkwayland.h>
#include <glib/gstdio.h>
#include <gtk-layer-shell.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <wayland-client.h>

#include "idle-inhibit-unstable-v1-client-protocol.h"

#define SS_NAME "org.freedesktop.ScreenSaver"
#define MAX_INHIBITS 64
#define MAX_PER_CLIENT 16

static const char introspection[] =
    "<node><interface name='org.freedesktop.ScreenSaver'>"
    "<method name='Inhibit'><arg type='s' name='application_name' direction='in'/>"
    "<arg type='s' name='reason_for_inhibit' direction='in'/><arg type='u' name='cookie' direction='out'/></method>"
    "<method name='UnInhibit'><arg type='u' name='cookie' direction='in'/></method>"
    "<method name='GetActive'><arg type='b' name='active' direction='out'/></method>"
    "<method name='GetActiveTime'><arg type='u' name='seconds' direction='out'/></method>"
    "<method name='GetSessionIdleTime'><arg type='u' name='seconds' direction='out'/></method>"
    "<method name='SimulateUserActivity'/>"
    "<method name='Lock'/>"
    "<signal name='ActiveChanged'><arg type='b' name='active'/></signal>"
    "</interface></node>";

/* Both paths apps use (the spec's and the older one). */
static const char *const PATHS[] = {"/org/freedesktop/ScreenSaver", "/ScreenSaver"};

typedef struct {
  guint32 cookie;
  char *sender;
  char *app;
  char *reason;
} Inhibit;

typedef struct {
  char *sender;
  guint watch;
  guint count;
} Client;

static GHashTable *inhibits; /* cookie -> Inhibit */
static GHashTable *clients;  /* unique name -> Client */
static guint32 next_cookie;
static guint reg_ids[G_N_ELEMENTS(PATHS)];

/* Wayland side */
static struct zwp_idle_inhibit_manager_v1 *wl_manager;
static struct zwp_idle_inhibitor_v1 *wl_inhibitor;
static GtkWidget *anchor; /* 1x1, background layer, no input */
static guint remap_id;

/* ---------- Wayland inhibitor ---------- */

static void
inhibitor_drop(void)
{
  if (wl_inhibitor != NULL) {
    zwp_idle_inhibitor_v1_destroy(wl_inhibitor);
    wl_inhibitor = NULL;
    GdkDisplay *d = gdk_display_get_default();
    if (d != NULL && GDK_IS_WAYLAND_DISPLAY(d))
      wl_display_flush(gdk_wayland_display_get_wl_display(d));
  }
}

static void
inhibitor_make(void)
{
  if (wl_inhibitor != NULL || wl_manager == NULL || anchor == NULL || !gtk_widget_get_mapped(anchor))
    return;
  GdkWindow *gw = gtk_widget_get_window(anchor);
  struct wl_surface *s = gw != NULL ? gdk_wayland_window_get_wl_surface(gw) : NULL;
  if (s == NULL)
    return;
  wl_inhibitor = zwp_idle_inhibit_manager_v1_create_inhibitor(wl_manager, s);
  wl_display_flush(gdk_wayland_display_get_wl_display(gdk_display_get_default()));
  if (shell.test || shell.debug)
    g_print("nocap-shell: wayland idle inhibitor on\n");
}

/* GTK gives the window a new wl_surface each time it's mapped: the
 * inhibitor goes with the old one and is made again on the new one. */
static void
on_anchor_map(GtkWidget *w, gpointer u)
{
  if (inhibits != NULL && g_hash_table_size(inhibits) > 0)
    inhibitor_make();
}

static void
on_anchor_unmap(GtkWidget *w, gpointer u)
{
  inhibitor_drop();
}

static void
anchor_realize(GtkWidget *w, gpointer u)
{
  /* Takes no clicks anywhere. */
  cairo_region_t *none = cairo_region_create();
  gdk_window_input_shape_combine_region(gtk_widget_get_window(w), none, 0, 0);
  cairo_region_destroy(none);
}

static gboolean
anchor_show_again(gpointer u)
{
  remap_id = 0;
  if (anchor != NULL && g_hash_table_size(inhibits) > 0 && !gtk_widget_get_visible(anchor))
    gtk_widget_show_all(anchor);
  return G_SOURCE_REMOVE;
}

/* Its screen went away (unplugged): put it back shortly, on whichever
 * screen there is then. */
static gboolean
on_anchor_delete(GtkWidget *w, GdkEvent *ev, gpointer u)
{
  gtk_widget_hide(w);
  if (remap_id == 0)
    remap_id = g_timeout_add(1000, anchor_show_again, NULL);
  return TRUE;
}

static void
anchor_create(void)
{
  if (anchor != NULL || !gtk_layer_is_supported())
    return;
  GtkWidget *w = gtk_window_new(GTK_WINDOW_TOPLEVEL);
  gtk_window_set_application(GTK_WINDOW(w), shell.app);
  gtk_window_set_title(GTK_WINDOW(w), "nocap-awake");
  gtk_window_set_decorated(GTK_WINDOW(w), FALSE);
  gtk_window_set_default_size(GTK_WINDOW(w), 1, 1);
  gtk_widget_set_size_request(w, 1, 1);
  gtk_widget_set_app_paintable(w, TRUE);
  GdkVisual *vis = gdk_screen_get_rgba_visual(gtk_widget_get_screen(w));
  if (vis != NULL)
    gtk_widget_set_visual(w, vis);
  gtk_style_context_add_class(gtk_widget_get_style_context(w), "nocap-surface");
  gtk_layer_init_for_window(GTK_WINDOW(w));
  gtk_layer_set_namespace(GTK_WINDOW(w), "nocap-awake");
  gtk_layer_set_layer(GTK_WINDOW(w), GTK_LAYER_SHELL_LAYER_BACKGROUND);
  gtk_layer_set_anchor(GTK_WINDOW(w), GTK_LAYER_SHELL_EDGE_TOP, TRUE);
  gtk_layer_set_anchor(GTK_WINDOW(w), GTK_LAYER_SHELL_EDGE_LEFT, TRUE);
  gtk_layer_set_keyboard_mode(GTK_WINDOW(w), GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);
  gtk_layer_set_exclusive_zone(GTK_WINDOW(w), 0);
  g_signal_connect(w, "realize", G_CALLBACK(anchor_realize), NULL);
  g_signal_connect_after(w, "map", G_CALLBACK(on_anchor_map), NULL);
  g_signal_connect(w, "unmap", G_CALLBACK(on_anchor_unmap), NULL);
  g_signal_connect(w, "delete-event", G_CALLBACK(on_anchor_delete), NULL);
  anchor = w;
}

/* Called whenever the number of inhibits changes. */
static void
inhibit_state_changed(void)
{
  guint n = g_hash_table_size(inhibits);
  if (shell.test || shell.debug)
    g_print("nocap-shell: idle inhibits %u\n", n);
  if (n > 0) {
    anchor_create();
    if (anchor != NULL && !gtk_widget_get_visible(anchor))
      gtk_widget_show_all(anchor);
    else
      inhibitor_make();
  } else if (anchor != NULL) {
    inhibitor_drop();
    gtk_widget_hide(anchor);
  }
}

/* ---------- the bus side ---------- */

static void
inhibit_free(Inhibit *i)
{
  g_free(i->sender);
  g_free(i->app);
  g_free(i->reason);
  g_free(i);
}

static void
client_free(Client *c)
{
  if (c->watch != 0)
    g_bus_unwatch_name(c->watch);
  g_free(c->sender);
  g_free(c);
}

/* Drops every inhibit of sender (it left the bus). */
static void
release_sender(const char *sender)
{
  GHashTableIter it;
  gpointer k, v;
  guint dropped = 0;
  g_hash_table_iter_init(&it, inhibits);
  while (g_hash_table_iter_next(&it, &k, &v)) {
    if (g_strcmp0(((Inhibit *)v)->sender, sender) == 0) {
      g_hash_table_iter_remove(&it);
      dropped++;
    }
  }
  g_hash_table_remove(clients, sender);
  if (dropped > 0)
    inhibit_state_changed();
}

static void
on_client_vanished(GDBusConnection *c, const char *name, gpointer u)
{
  release_sender(name);
}

static guint32
new_cookie(void)
{
  do {
    next_cookie++;
    if (next_cookie == 0)
      next_cookie = 1;
  } while (g_hash_table_contains(inhibits, GUINT_TO_POINTER(next_cookie)));
  return next_cookie;
}

/* The lock screen is up (for GetActive). */
static gboolean
lock_running(void)
{
  GDir *d = g_dir_open("/proc", 0, NULL);
  if (d == NULL)
    return FALSE;
  gboolean found = FALSE;
  uid_t me = getuid();
  const char *e;
  while (!found && (e = g_dir_read_name(d)) != NULL) {
    if (e[0] < '0' || e[0] > '9')
      continue;
    char *p = g_build_filename("/proc", e, "comm", NULL);
    char *comm = NULL;
    if (g_file_get_contents(p, &comm, NULL, NULL) && g_str_has_prefix(comm, "nocap-lock\n")) {
      char *dir = g_build_filename("/proc", e, NULL);
      GStatBuf st;
      if (g_stat(dir, &st) == 0 && st.st_uid == me)
        found = TRUE;
      g_free(dir);
    }
    g_free(comm);
    g_free(p);
  }
  g_dir_close(d);
  return found;
}

static void
lock_done(gboolean ok, const char *error, gpointer data)
{
  if (!ok)
    g_warning("screensaver: %s", error ? error : "couldn't lock");
}

static void
on_method(GDBusConnection *c, const char *sender, const char *path, const char *iface, const char *method,
          GVariant *params, GDBusMethodInvocation *inv, gpointer u)
{
  if (strcmp(method, "Inhibit") == 0) {
    const char *app = NULL, *reason = NULL;
    g_variant_get(params, "(&s&s)", &app, &reason);
    Client *cl = g_hash_table_lookup(clients, sender);
    if (g_hash_table_size(inhibits) >= MAX_INHIBITS || (cl != NULL && cl->count >= MAX_PER_CLIENT)) {
      g_dbus_method_invocation_return_error(inv, G_DBUS_ERROR, G_DBUS_ERROR_LIMITS_EXCEEDED, "too many inhibits");
      return;
    }
    if (cl == NULL) {
      cl = g_new0(Client, 1);
      cl->sender = g_strdup(sender);
      g_hash_table_insert(clients, cl->sender, cl);
      /* Fires at once when the client already left. */
      cl->watch = g_bus_watch_name_on_connection(c, sender, G_BUS_NAME_WATCHER_FLAGS_NONE, NULL,
                                                 on_client_vanished, NULL, NULL);
    }
    Inhibit *i = g_new0(Inhibit, 1);
    i->cookie = new_cookie();
    i->sender = g_strdup(sender);
    i->app = utf8_truncate(app, 128);
    i->reason = utf8_truncate(reason, 256);
    g_hash_table_insert(inhibits, GUINT_TO_POINTER(i->cookie), i);
    cl->count++;
    if (shell.debug)
      g_message("screensaver: %s keeps the screen awake (%s)", i->app, i->reason);
    g_dbus_method_invocation_return_value(inv, g_variant_new("(u)", i->cookie));
    inhibit_state_changed();
  } else if (strcmp(method, "UnInhibit") == 0) {
    guint32 cookie = 0;
    g_variant_get(params, "(u)", &cookie);
    Inhibit *i = g_hash_table_lookup(inhibits, GUINT_TO_POINTER(cookie));
    if (i == NULL || g_strcmp0(i->sender, sender) != 0) {
      g_dbus_method_invocation_return_error(inv, G_DBUS_ERROR, G_DBUS_ERROR_INVALID_ARGS, "unknown cookie");
      return;
    }
    Client *cl = g_hash_table_lookup(clients, sender);
    g_hash_table_remove(inhibits, GUINT_TO_POINTER(cookie));
    if (cl != NULL && cl->count > 0 && --cl->count == 0)
      g_hash_table_remove(clients, sender);
    g_dbus_method_invocation_return_value(inv, NULL);
    inhibit_state_changed();
  } else if (strcmp(method, "GetActive") == 0) {
    g_dbus_method_invocation_return_value(inv, g_variant_new("(b)", lock_running()));
  } else if (strcmp(method, "GetActiveTime") == 0 || strcmp(method, "GetSessionIdleTime") == 0) {
    g_dbus_method_invocation_return_value(inv, g_variant_new("(u)", 0u));
  } else if (strcmp(method, "SimulateUserActivity") == 0) {
    g_dbus_method_invocation_return_value(inv, NULL);
  } else if (strcmp(method, "Lock") == 0) {
    power_action("lock", lock_done, NULL);
    g_dbus_method_invocation_return_value(inv, NULL);
  } else {
    g_dbus_method_invocation_return_error(inv, G_DBUS_ERROR, G_DBUS_ERROR_UNKNOWN_METHOD, "unknown method");
  }
}

static const GDBusInterfaceVTable vtable = {on_method, NULL, NULL};

static void
on_bus(GDBusConnection *c, const char *name, gpointer u)
{
  GDBusNodeInfo *info = g_dbus_node_info_new_for_xml(introspection, NULL);
  for (guint i = 0; i < G_N_ELEMENTS(PATHS); i++) {
    GError *err = NULL;
    reg_ids[i] = g_dbus_connection_register_object(c, PATHS[i], info->interfaces[0], &vtable, NULL, NULL, &err);
    if (reg_ids[i] == 0) {
      g_warning("screensaver: %s", err->message);
      g_clear_error(&err);
    }
  }
  g_dbus_node_info_unref(info);
}

static void
on_acquired(GDBusConnection *c, const char *name, gpointer u)
{
  if (shell.test || shell.debug)
    g_print("nocap-shell: owns %s\n", name);
}

static void
on_lost(GDBusConnection *c, const char *name, gpointer u)
{
  /* Another program took the name (or there's no bus): its inhibits are
   * its own now; ours would never be released. */
  if (g_hash_table_size(inhibits) > 0) {
    g_hash_table_remove_all(inhibits);
    g_hash_table_remove_all(clients);
    inhibit_state_changed();
  }
  g_message("screensaver: %s is owned by another program", SS_NAME);
}

/* ---------- Wayland registry ---------- */

static void
registry_global(void *data, struct wl_registry *r, uint32_t name, const char *iface, uint32_t version)
{
  if (wl_manager == NULL && strcmp(iface, zwp_idle_inhibit_manager_v1_interface.name) == 0)
    wl_manager = wl_registry_bind(r, name, &zwp_idle_inhibit_manager_v1_interface, 1);
}

static void
registry_global_remove(void *data, struct wl_registry *r, uint32_t name)
{
}

static const struct wl_registry_listener registry_listener = {
  .global = registry_global,
  .global_remove = registry_global_remove,
};

void
idle_init(void)
{
  inhibits = g_hash_table_new_full(g_direct_hash, g_direct_equal, NULL, (GDestroyNotify)inhibit_free);
  clients = g_hash_table_new_full(g_str_hash, g_str_equal, NULL, (GDestroyNotify)client_free);
  next_cookie = (guint32)g_random_int_range(1, 1 << 20);
  GdkDisplay *d = gdk_display_get_default();
  if (d != NULL && GDK_IS_WAYLAND_DISPLAY(d)) {
    struct wl_display *wl = gdk_wayland_display_get_wl_display(d);
    struct wl_registry *reg = wl_display_get_registry(wl);
    wl_registry_add_listener(reg, &registry_listener, NULL);
    wl_display_roundtrip(wl);
  }
  if (wl_manager == NULL)
    g_message("screensaver: the compositor has no idle inhibit; apps can't keep the screen awake");
  g_bus_own_name(G_BUS_TYPE_SESSION, SS_NAME, G_BUS_NAME_OWNER_FLAGS_REPLACE | G_BUS_NAME_OWNER_FLAGS_ALLOW_REPLACEMENT,
                 on_bus, on_acquired, on_lost, NULL, NULL);
}

guint
idle_inhibit_count(void)
{
  return inhibits != NULL ? g_hash_table_size(inhibits) : 0;
}
