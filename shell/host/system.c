/* Audio (wpctl/pactl), brightness (sysfs + logind/brightnessctl), network
 * (NetworkManager over D-Bus) and power (logind). All optional: missing
 * services report "unavailable" instead of failing. */
#include "shell.h"

#include <dirent.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define LOGIN1 "org.freedesktop.login1"
#define LOGIN1_PATH "/org/freedesktop/login1"
/* logind resolves "auto" to the caller's own session. */
#define LOGIN1_SESSION "/org/freedesktop/login1/session/auto"

static GDBusConnection *system_bus;

static GDBusConnection *
get_system_bus(void)
{
  if (system_bus == NULL) {
    GError *err = NULL;
    system_bus = g_bus_get_sync(G_BUS_TYPE_SYSTEM, NULL, &err);
    if (system_bus == NULL) {
      g_message("system bus unavailable: %s", err->message);
      g_clear_error(&err);
    }
  }
  return system_bus;
}

/* ---------------- audio ---------------- */

JsonNode *
audio_json(gboolean available, int volume, gboolean muted)
{
  JsonObject *o = json_object_new();
  json_object_set_boolean_member(o, "available", available);
  json_object_set_int_member(o, "volume", CLAMP(volume, 0, 150));
  json_object_set_boolean_member(o, "muted", muted);
  return jobj_node(o);
}

typedef struct {
  AudioCb cb;
  gpointer data;
  int volume, muted; /* pending set, -1 = none */
  gboolean use_pactl;
  int got_volume;
} AudioJob;

static void audio_job_get(AudioJob *j);

static void
audio_job_finish(AudioJob *j, gboolean ok, int volume, gboolean muted)
{
  j->cb(ok, ok ? volume : 0, ok ? muted : FALSE, j->data);
  g_free(j);
}

/* "Volume: 0.40" or "Volume: 0.40 [MUTED]" */
static gboolean
parse_wpctl(const char *out, int *volume, gboolean *muted)
{
  const char *p = out ? strstr(out, "Volume:") : NULL;
  if (p == NULL)
    return FALSE;
  char *end = NULL;
  double v = g_ascii_strtod(p + 7, &end);
  if (end == p + 7)
    return FALSE;
  *volume = (int)(v * 100.0 + 0.5);
  *muted = strstr(p, "[MUTED]") != NULL;
  return TRUE;
}

static void
pactl_mute_done(char *out, gboolean ok, gpointer data)
{
  AudioJob *j = data;
  gboolean muted = out != NULL && strstr(out, "yes") != NULL;
  g_free(out);
  audio_job_finish(j, ok, j->got_volume, muted);
}

static void
pactl_volume_done(char *out, gboolean ok, gpointer data)
{
  AudioJob *j = data;
  const char *pct = (ok && out) ? strchr(out, '%') : NULL;
  if (pct == NULL) {
    g_free(out);
    audio_job_finish(j, FALSE, 0, FALSE);
    return;
  }
  const char *start = pct;
  while (start > out && g_ascii_isdigit(start[-1]))
    start--;
  j->got_volume = (int)g_ascii_strtoll(start, NULL, 10);
  g_free(out);
  const char *argv[] = {find_tool("pactl"), "get-sink-mute", "@DEFAULT_SINK@", NULL};
  run_command(argv, 3000, pactl_mute_done, j);
}

static void
wpctl_get_done(char *out, gboolean ok, gpointer data)
{
  AudioJob *j = data;
  int volume = 0;
  gboolean muted = FALSE;
  gboolean parsed = ok && parse_wpctl(out, &volume, &muted);
  g_free(out);
  if (parsed) {
    audio_job_finish(j, TRUE, volume, muted);
  } else if (find_tool("pactl") != NULL) {
    j->use_pactl = TRUE;
    audio_job_get(j);
  } else {
    audio_job_finish(j, FALSE, 0, FALSE);
  }
}

static void
audio_job_get(AudioJob *j)
{
  if (!j->use_pactl && find_tool("wpctl") != NULL) {
    const char *argv[] = {find_tool("wpctl"), "get-volume", "@DEFAULT_AUDIO_SINK@", NULL};
    run_command(argv, 3000, wpctl_get_done, j);
  } else if (find_tool("pactl") != NULL) {
    j->use_pactl = TRUE;
    const char *argv[] = {find_tool("pactl"), "get-sink-volume", "@DEFAULT_SINK@", NULL};
    run_command(argv, 3000, pactl_volume_done, j);
  } else {
    audio_job_finish(j, FALSE, 0, FALSE);
  }
}

void
audio_get(AudioCb cb, gpointer data)
{
  AudioJob *j = g_new0(AudioJob, 1);
  j->cb = cb;
  j->data = data;
  j->volume = j->muted = -1;
  audio_job_get(j);
}

static void audio_job_set_step(AudioJob *j);

static void
audio_set_step_done(char *out, gboolean ok, gpointer data)
{
  g_free(out);
  audio_job_set_step(data);
}

/* Applies the pending volume, then mute, then reads the result back. */
static void
audio_job_set_step(AudioJob *j)
{
  gboolean pactl = find_tool("wpctl") == NULL;
  const char *tool = find_tool(pactl ? "pactl" : "wpctl");
  if (tool == NULL) {
    audio_job_finish(j, FALSE, 0, FALSE);
    return;
  }
  if (j->volume >= 0) {
    char pct[16];
    g_snprintf(pct, sizeof pct, "%d%%", CLAMP(j->volume, 0, 150));
    j->volume = -1;
    const char *argv[] = {tool, pactl ? "set-sink-volume" : "set-volume",
                          pactl ? "@DEFAULT_SINK@" : "@DEFAULT_AUDIO_SINK@", pct, NULL};
    run_command(argv, 3000, audio_set_step_done, j);
    return;
  }
  if (j->muted >= 0) {
    const char *m = j->muted ? "1" : "0";
    j->muted = -1;
    const char *argv[] = {tool, pactl ? "set-sink-mute" : "set-mute",
                          pactl ? "@DEFAULT_SINK@" : "@DEFAULT_AUDIO_SINK@", m, NULL};
    run_command(argv, 3000, audio_set_step_done, j);
    return;
  }
  audio_job_get(j);
}

void
audio_set(int volume, int muted, AudioCb cb, gpointer data)
{
  AudioJob *j = g_new0(AudioJob, 1);
  j->cb = cb;
  j->data = data;
  j->volume = volume;
  j->muted = muted;
  audio_job_set_step(j);
}

/* ---------------- brightness ---------------- */

JsonNode *
brightness_json(gboolean available, int percent)
{
  JsonObject *o = json_object_new();
  json_object_set_boolean_member(o, "available", available);
  json_object_set_int_member(o, "percent", CLAMP(percent, 0, 100));
  return jobj_node(o);
}

static gint64
read_int_file(const char *dir, const char *name)
{
  char *path = g_build_filename(dir, name, NULL);
  char *text = NULL;
  gint64 v = -1;
  if (g_file_get_contents(path, &text, NULL, NULL))
    v = g_ascii_strtoll(text, NULL, 10);
  g_free(text);
  g_free(path);
  return v;
}

/* Picks the best backlight: firmware > platform > raw. Returns its name. */
static char *
find_backlight(void)
{
  DIR *d = opendir("/sys/class/backlight");
  if (d == NULL)
    return NULL;
  char *best = NULL;
  int best_rank = -1;
  struct dirent *e;
  while ((e = readdir(d)) != NULL) {
    if (e->d_name[0] == '.')
      continue;
    gboolean safe = TRUE;
    for (const char *p = e->d_name; *p; p++)
      if (!(g_ascii_isalnum(*p) || *p == '_' || *p == '-' || *p == '.' || *p == ':'))
        safe = FALSE;
    if (!safe)
      continue;
    char *dir = g_build_filename("/sys/class/backlight", e->d_name, NULL);
    char *type_path = g_build_filename(dir, "type", NULL);
    char *type = NULL;
    int rank = 0;
    if (g_file_get_contents(type_path, &type, NULL, NULL)) {
      if (g_str_has_prefix(type, "firmware"))
        rank = 3;
      else if (g_str_has_prefix(type, "platform"))
        rank = 2;
      else
        rank = 1;
    }
    if (read_int_file(dir, "max_brightness") > 0 && rank > best_rank) {
      g_free(best);
      best = g_strdup(e->d_name);
      best_rank = rank;
    }
    g_free(type);
    g_free(type_path);
    g_free(dir);
  }
  closedir(d);
  return best;
}

static gboolean
read_brightness(int *percent, char **name_out, gint64 *max_out)
{
  char *name = find_backlight();
  if (name == NULL)
    return FALSE;
  char *dir = g_build_filename("/sys/class/backlight", name, NULL);
  gint64 max = read_int_file(dir, "max_brightness");
  gint64 cur = read_int_file(dir, "actual_brightness");
  if (cur < 0)
    cur = read_int_file(dir, "brightness");
  g_free(dir);
  if (max <= 0 || cur < 0) {
    g_free(name);
    return FALSE;
  }
  *percent = (int)CLAMP((cur * 100 + max / 2) / max, 0, 100);
  if (name_out)
    *name_out = name;
  else
    g_free(name);
  if (max_out)
    *max_out = max;
  return TRUE;
}

void
brightness_get(BrightnessCb cb, gpointer data)
{
  int pct = 0;
  gboolean ok = read_brightness(&pct, NULL, NULL);
  cb(ok, pct, data);
}

typedef struct {
  BrightnessCb cb;
  gpointer data;
  char *name;
  int percent;
} BrightJob;

static void
bright_finish(BrightJob *j)
{
  int pct = 0;
  gboolean ok = read_brightness(&pct, NULL, NULL);
  j->cb(ok, pct, j->data);
  g_free(j->name);
  g_free(j);
}

static void
brightnessctl_done(char *out, gboolean ok, gpointer data)
{
  g_free(out);
  bright_finish(data);
}

static void
logind_brightness_done(GObject *src, GAsyncResult *res, gpointer data)
{
  BrightJob *j = data;
  GError *err = NULL;
  GVariant *r = g_dbus_connection_call_finish(G_DBUS_CONNECTION(src), res, &err);
  if (r != NULL) {
    g_variant_unref(r);
    bright_finish(j);
    return;
  }
  g_clear_error(&err);
  const char *tool = find_tool("brightnessctl");
  if (tool == NULL) {
    bright_finish(j);
    return;
  }
  char pct[16];
  g_snprintf(pct, sizeof pct, "%d%%", j->percent);
  const char *argv[] = {tool, "-q", "-d", j->name, "set", pct, NULL};
  run_command(argv, 3000, brightnessctl_done, j);
}

void
brightness_set(int percent, BrightnessCb cb, gpointer data)
{
  BrightJob *j = g_new0(BrightJob, 1);
  j->cb = cb;
  j->data = data;
  j->percent = CLAMP(percent, 1, 100);
  int cur;
  gint64 max = 0;
  if (!read_brightness(&cur, &j->name, &max)) {
    cb(FALSE, 0, data);
    g_free(j);
    return;
  }
  guint32 value = (guint32)MAX(1, (max * j->percent + 50) / 100);
  GDBusConnection *bus = get_system_bus();
  if (bus == NULL) {
    /* Same fallback path as a logind failure. */
    const char *tool = find_tool("brightnessctl");
    if (tool == NULL) {
      bright_finish(j);
      return;
    }
    char pct[16];
    g_snprintf(pct, sizeof pct, "%d%%", j->percent);
    const char *argv[] = {tool, "-q", "-d", j->name, "set", pct, NULL};
    run_command(argv, 3000, brightnessctl_done, j);
    return;
  }
  g_dbus_connection_call(bus, LOGIN1, LOGIN1_SESSION, "org.freedesktop.login1.Session",
                         "SetBrightness", g_variant_new("(ssu)", "backlight", j->name, value), NULL,
                         G_DBUS_CALL_FLAGS_NONE, 3000, NULL, logind_brightness_done, j);
}

/* ---------------- network ---------------- */

static GDBusProxy *nm, *nm_active, *nm_ap;
static char *last_network;
static guint nm_watch;

static guint32
prop_u(GDBusProxy *p, const char *name)
{
  GVariant *v = p ? g_dbus_proxy_get_cached_property(p, name) : NULL;
  guint32 r = 0;
  if (v != NULL) {
    if (g_variant_is_of_type(v, G_VARIANT_TYPE_UINT32))
      r = g_variant_get_uint32(v);
    else if (g_variant_is_of_type(v, G_VARIANT_TYPE_BYTE))
      r = g_variant_get_byte(v);
    g_variant_unref(v);
  }
  return r;
}

static char *
prop_s(GDBusProxy *p, const char *name)
{
  GVariant *v = p ? g_dbus_proxy_get_cached_property(p, name) : NULL;
  char *r = NULL;
  if (v != NULL) {
    if (g_variant_is_of_type(v, G_VARIANT_TYPE_STRING) ||
        g_variant_is_of_type(v, G_VARIANT_TYPE_OBJECT_PATH))
      r = g_variant_dup_string(v, NULL);
    g_variant_unref(v);
  }
  return r;
}

JsonNode *
network_json(void)
{
  const char *state = "offline", *kind = "none";
  char *name = NULL;
  int strength = 0;

  if (nm != NULL && g_dbus_proxy_get_name_owner(nm) != NULL) {
    guint32 nm_state = prop_u(nm, "State");
    guint32 ac_state = prop_u(nm_active, "State");
    char *type = prop_s(nm_active, "Type");
    if (nm_active != NULL && ac_state == 2 && nm_state >= 50)
      state = "online";
    else if (nm_state == 40 || (nm_active != NULL && ac_state == 1))
      state = "connecting";
    if (nm_active != NULL) {
      if (g_strcmp0(type, "802-11-wireless") == 0)
        kind = "wifi";
      else if (g_strcmp0(type, "802-3-ethernet") == 0)
        kind = "ethernet";
      else
        kind = "other";
      name = prop_s(nm_active, "Id");
    }
    if (nm_ap != NULL && g_strcmp0(kind, "wifi") == 0)
      strength = (int)prop_u(nm_ap, "Strength");
    g_free(type);
  }
  if (g_strcmp0(state, "offline") == 0)
    kind = "none";

  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "state", state);
  json_object_set_string_member(o, "kind", kind);
  json_object_set_string_member(o, "name", name ? name : "");
  json_object_set_int_member(o, "strength", CLAMP(strength, 0, 100));
  g_free(name);
  return jobj_node(o);
}

static void
network_maybe_emit(void)
{
  JsonNode *n = network_json();
  char *text = json_to_string(n, FALSE);
  if (g_strcmp0(text, last_network) != 0) {
    g_free(last_network);
    last_network = text;
    shell_emit(SM(SURF_CENTER) | SM(SURF_BAR), "network.changed", n);
  } else {
    g_free(text);
    json_node_unref(n);
  }
}

static void network_rebind(void);
static char *bound_active;

static void
on_ap_props(GDBusProxy *p, GVariant *changed, GStrv inv, gpointer u)
{
  network_maybe_emit();
}

static void
on_ap_ready(GObject *src, GAsyncResult *res, gpointer u)
{
  GDBusProxy *p = g_dbus_proxy_new_finish(res, NULL);
  if (p == NULL)
    return;
  g_clear_object(&nm_ap);
  nm_ap = p;
  g_signal_connect(nm_ap, "g-properties-changed", G_CALLBACK(on_ap_props), NULL);
  network_maybe_emit();
}

static void
on_active_props(GDBusProxy *p, GVariant *changed, GStrv inv, gpointer u)
{
  GVariant *ap = g_variant_lookup_value(changed, "SpecificObject", NULL);
  if (ap != NULL) {
    g_variant_unref(ap);
    network_rebind();
  }
  network_maybe_emit();
}

static void
bind_ap(void)
{
  g_clear_object(&nm_ap);
  char *type = prop_s(nm_active, "Type");
  char *ap = prop_s(nm_active, "SpecificObject");
  if (g_strcmp0(type, "802-11-wireless") == 0 && ap != NULL && strcmp(ap, "/") != 0)
    g_dbus_proxy_new(g_dbus_proxy_get_connection(nm), G_DBUS_PROXY_FLAGS_DO_NOT_AUTO_START, NULL,
                     "org.freedesktop.NetworkManager", ap,
                     "org.freedesktop.NetworkManager.AccessPoint", NULL, on_ap_ready, NULL);
  g_free(type);
  g_free(ap);
}

static void
on_active_ready(GObject *src, GAsyncResult *res, gpointer u)
{
  GDBusProxy *p = g_dbus_proxy_new_finish(res, NULL);
  if (p == NULL) {
    network_maybe_emit();
    return;
  }
  g_clear_object(&nm_active);
  nm_active = p;
  g_signal_connect(nm_active, "g-properties-changed", G_CALLBACK(on_active_props), NULL);
  bind_ap();
  network_maybe_emit();
}

static void
network_rebind(void)
{
  char *path = prop_s(nm, "PrimaryConnection");
  if (g_strcmp0(path, bound_active) == 0 && nm_active != NULL) {
    bind_ap();
    g_free(path);
    return;
  }
  g_free(bound_active);
  bound_active = path;
  g_clear_object(&nm_active);
  g_clear_object(&nm_ap);
  if (path != NULL && strcmp(path, "/") != 0)
    g_dbus_proxy_new(g_dbus_proxy_get_connection(nm), G_DBUS_PROXY_FLAGS_DO_NOT_AUTO_START, NULL,
                     "org.freedesktop.NetworkManager", path,
                     "org.freedesktop.NetworkManager.Connection.Active", NULL, on_active_ready, NULL);
  network_maybe_emit();
}

static void
on_nm_props(GDBusProxy *p, GVariant *changed, GStrv inv, gpointer u)
{
  network_rebind();
}

static void
on_nm_ready(GObject *src, GAsyncResult *res, gpointer u)
{
  GDBusProxy *p = g_dbus_proxy_new_finish(res, NULL);
  if (p == NULL)
    return;
  nm = p;
  g_signal_connect(nm, "g-properties-changed", G_CALLBACK(on_nm_props), NULL);
  network_rebind();
}

static void
on_nm_appeared(GDBusConnection *c, const char *name, const char *owner, gpointer u)
{
  g_clear_object(&nm);
  g_free(bound_active);
  bound_active = NULL;
  g_dbus_proxy_new(c, G_DBUS_PROXY_FLAGS_DO_NOT_AUTO_START, NULL, "org.freedesktop.NetworkManager",
                   "/org/freedesktop/NetworkManager", "org.freedesktop.NetworkManager", NULL,
                   on_nm_ready, NULL);
}

static void
on_nm_vanished(GDBusConnection *c, const char *name, gpointer u)
{
  g_clear_object(&nm_ap);
  g_clear_object(&nm_active);
  g_clear_object(&nm);
  g_free(bound_active);
  bound_active = NULL;
  network_maybe_emit();
}

/* ---------------- power ---------------- */

typedef struct {
  DoneCb cb;
  gpointer data;
} PowerJob;

static void
power_done(GObject *src, GAsyncResult *res, gpointer data)
{
  PowerJob *j = data;
  GError *err = NULL;
  GVariant *r = g_dbus_connection_call_finish(G_DBUS_CONNECTION(src), res, &err);
  if (r != NULL) {
    g_variant_unref(r);
    j->cb(TRUE, NULL, j->data);
  } else {
    g_message("power action failed: %s", err->message);
    j->cb(FALSE, "the system refused the request", j->data);
    g_clear_error(&err);
  }
  g_free(j);
}

static char *
lock_path(void)
{
  char *p = g_build_filename(shell.bin_dir ? shell.bin_dir : "/", "nocap-lock", NULL);
  if (g_file_test(p, G_FILE_TEST_IS_EXECUTABLE))
    return p;
  g_free(p);
  return g_find_program_in_path("nocap-lock");
}

void
power_action(const char *action, DoneCb cb, gpointer data)
{
  if (strcmp(action, "lock") == 0) {
    char *lock = lock_path();
    GError *err = NULL;
    gboolean ok = FALSE;
    if (lock != NULL) {
      /* nocap-lock is single-instance itself (pid file). */
      const char *argv[] = {lock, NULL};
      ok = spawn_detached(argv, &err);
    }
    g_free(lock);
    cb(ok, ok ? NULL : "lock screen unavailable", data);
    g_clear_error(&err);
    return;
  }

  GDBusConnection *bus = get_system_bus();
  if (bus == NULL) {
    cb(FALSE, "system bus unavailable", data);
    return;
  }
  PowerJob *j = g_new0(PowerJob, 1);
  j->cb = cb;
  j->data = data;
  if (strcmp(action, "logout") == 0) {
    g_dbus_connection_call(bus, LOGIN1, LOGIN1_SESSION, "org.freedesktop.login1.Session", "Terminate",
                           NULL, NULL, G_DBUS_CALL_FLAGS_NONE, 10000, NULL, power_done, j);
    return;
  }
  const char *method = NULL;
  if (strcmp(action, "suspend") == 0)
    method = "Suspend";
  else if (strcmp(action, "reboot") == 0)
    method = "Reboot";
  else if (strcmp(action, "poweroff") == 0)
    method = "PowerOff";
  if (method == NULL) {
    g_free(j);
    cb(FALSE, "unknown action", data);
    return;
  }
  g_dbus_connection_call(bus, LOGIN1, LOGIN1_PATH, "org.freedesktop.login1.Manager", method,
                         g_variant_new("(b)", TRUE), NULL,
                         G_DBUS_CALL_FLAGS_ALLOW_INTERACTIVE_AUTHORIZATION, 30000, NULL, power_done, j);
}

/* ---------------- init ---------------- */

void
system_init(void)
{
  GDBusConnection *bus = get_system_bus();
  if (bus != NULL)
    nm_watch = g_bus_watch_name_on_connection(bus, "org.freedesktop.NetworkManager",
                                              G_BUS_NAME_WATCHER_FLAGS_NONE, on_nm_appeared,
                                              on_nm_vanished, NULL, NULL);
}
