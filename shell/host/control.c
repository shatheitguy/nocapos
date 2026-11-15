/* io.nocapos.Shell on the session bus: lets labwc's keybindings (through
 * `nocap-shell --toggle launcher`, `--osd volume-up`, ...) drive the
 * running shell. Only fixed actions and surface names are accepted. */
#include "shell.h"

#include <string.h>

#define BUS_NAME "io.nocapos.Shell"
#define OBJ_PATH "/io/nocapos/Shell"

static const char introspection[] =
    "<node><interface name='io.nocapos.Shell'>"
    "<method name='Control'><arg type='s' name='action' direction='in'/>"
    "<arg type='s' name='arg' direction='in'/></method>"
    "</interface></node>";

static const char *const CONTROL_SURFACES[] = {"launcher", "center", "osd", NULL};
static const char *const OSD_ACTIONS[] = {"volume-up", "volume-down", "volume-mute", "brightness-up",
                                          "brightness-down", NULL};

static gboolean
in_list(const char *s, const char *const *list)
{
  for (int i = 0; s != NULL && list[i] != NULL; i++)
    if (strcmp(s, list[i]) == 0)
      return TRUE;
  return FALSE;
}

/* ---------- OSD ---------- */

static void
osd_emit(const char *kind, int value, gboolean muted)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "kind", kind);
  json_object_set_int_member(o, "value", value);
  json_object_set_boolean_member(o, "muted", muted);
  shell_emit(SM(SURF_OSD), "osd.show", jobj_node(o));
  surface_show_for(SURF_OSD, 1500);
}

static void
audio_after(gboolean available, int volume, gboolean muted, gpointer data)
{
  if (!available)
    return;
  shell_emit(SM(SURF_CENTER) | SM(SURF_BAR) | SM(SURF_OSD), "audio.changed",
             audio_json(available, volume, muted));
  osd_emit("volume", volume, muted);
}

static void
audio_step(gboolean available, int volume, gboolean muted, gpointer data)
{
  int step = GPOINTER_TO_INT(data);
  if (!available)
    return;
  if (step == 0)
    audio_set(-1, muted ? 0 : 1, audio_after, NULL);
  else
    audio_set(CLAMP(volume + step, 0, 100), step > 0 ? 0 : -1, audio_after, NULL);
}

static void
brightness_after(gboolean available, int percent, gpointer data)
{
  if (!available)
    return;
  shell_emit(SM(SURF_CENTER) | SM(SURF_OSD), "brightness.changed", brightness_json(available, percent));
  osd_emit("brightness", percent, FALSE);
}

static void
brightness_step(gboolean available, int percent, gpointer data)
{
  if (!available)
    return;
  brightness_set(CLAMP(percent + GPOINTER_TO_INT(data), 1, 100), brightness_after, NULL);
}

void
control_osd(const char *what)
{
  if (g_strcmp0(what, "volume-up") == 0)
    audio_get(audio_step, GINT_TO_POINTER(5));
  else if (g_strcmp0(what, "volume-down") == 0)
    audio_get(audio_step, GINT_TO_POINTER(-5));
  else if (g_strcmp0(what, "volume-mute") == 0)
    audio_get(audio_step, GINT_TO_POINTER(0));
  else if (g_strcmp0(what, "brightness-up") == 0)
    brightness_get(brightness_step, GINT_TO_POINTER(5));
  else if (g_strcmp0(what, "brightness-down") == 0)
    brightness_get(brightness_step, GINT_TO_POINTER(-5));
}

/* ---------- server ---------- */

static void
on_method(GDBusConnection *c, const char *sender, const char *path, const char *iface,
          const char *method, GVariant *params, GDBusMethodInvocation *inv, gpointer u)
{
  if (g_strcmp0(method, "Control") != 0) {
    g_dbus_method_invocation_return_error(inv, G_DBUS_ERROR, G_DBUS_ERROR_UNKNOWN_METHOD, "unknown");
    return;
  }
  const char *action = NULL, *arg = NULL;
  g_variant_get(params, "(&s&s)", &action, &arg);
  if (strcmp(action, "osd") == 0 && in_list(arg, OSD_ACTIONS)) {
    control_osd(arg);
  } else if ((strcmp(action, "toggle") == 0 || strcmp(action, "show") == 0 ||
              strcmp(action, "hide") == 0) &&
             in_list(arg, CONTROL_SURFACES)) {
    SurfaceId id = (SurfaceId)surface_from_name(arg);
    if (action[0] == 't')
      surface_toggle(id);
    else if (action[0] == 's')
      surface_show(id);
    else
      surface_hide(id);
  } else {
    g_dbus_method_invocation_return_error(inv, G_DBUS_ERROR, G_DBUS_ERROR_INVALID_ARGS, "not allowed");
    return;
  }
  g_dbus_method_invocation_return_value(inv, NULL);
}

static const GDBusInterfaceVTable vtable = {on_method, NULL, NULL};

static void
on_bus(GDBusConnection *c, const char *name, gpointer u)
{
  GDBusNodeInfo *info = g_dbus_node_info_new_for_xml(introspection, NULL);
  GError *err = NULL;
  if (g_dbus_connection_register_object(c, OBJ_PATH, info->interfaces[0], &vtable, NULL, NULL, &err) == 0) {
    g_warning("control: %s", err->message);
    g_clear_error(&err);
  }
  g_dbus_node_info_unref(info);
}

static void
on_lost(GDBusConnection *c, const char *name, gpointer u)
{
  g_message("control: %s is owned by another shell; keybindings go there", BUS_NAME);
}

void
control_own(void)
{
  g_bus_own_name(G_BUS_TYPE_SESSION, BUS_NAME, G_BUS_NAME_OWNER_FLAGS_REPLACE |
                                                   G_BUS_NAME_OWNER_FLAGS_ALLOW_REPLACEMENT,
                 on_bus, NULL, on_lost, NULL, NULL);
}

/* ---------- client ---------- */

int
control_client(const char *action, const char *arg)
{
  gboolean ok = strcmp(action, "osd") == 0 ? in_list(arg, OSD_ACTIONS) : in_list(arg, CONTROL_SURFACES);
  if (!ok) {
    g_printerr("nocap-shell: unknown %s target '%s'\n", action, arg ? arg : "");
    return 2;
  }
  GError *err = NULL;
  GDBusConnection *bus = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, &err);
  if (bus == NULL) {
    g_printerr("nocap-shell: no session bus: %s\n", err->message);
    g_error_free(err);
    return 1;
  }
  GVariant *r = g_dbus_connection_call_sync(bus, BUS_NAME, OBJ_PATH, BUS_NAME, "Control",
                                            g_variant_new("(ss)", action, arg), NULL,
                                            G_DBUS_CALL_FLAGS_NO_AUTO_START, 3000, NULL, &err);
  g_object_unref(bus);
  if (r == NULL) {
    g_printerr("nocap-shell: the shell isn't running: %s\n", err->message);
    g_error_free(err);
    return 1;
  }
  g_variant_unref(r);
  return 0;
}
