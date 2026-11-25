/* Runs a validated bridge call (bridge.c already checked the method, the
 * surface and every parameter). */
#include "shell.h"

#include <string.h>

JsonNode *
shell_info_json(SurfaceId surface)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "surface", surface_name(surface));
  json_object_set_string_member(o, "user", shell.greeter ? "" : shell.user);
  json_object_set_string_member(o, "realName", shell.greeter ? "" : shell.real_name);
  json_object_set_string_member(o, "voice", shell.voice ? shell.voice : "genz");
  json_object_set_string_member(o, "theme", shell.theme ? shell.theme : "dark");
  json_object_set_string_member(o, "version", NOCAP_VERSION);
  if (surface == SURF_APP) {
    json_object_set_string_member(o, "app", shell.app_id ? shell.app_id : "");
    json_object_set_string_member(o, "props", shell.app_props ? shell.app_props : "");
  }
  return jobj_node(o);
}

void
shell_settings_changed(void)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "voice", shell.voice ? shell.voice : "genz");
  json_object_set_string_member(o, "theme", shell.theme ? shell.theme : "dark");
  shell_emit(SM_ALL, "settings.changed", jobj_node(o));
}

static void
audio_reply(gboolean available, int volume, gboolean muted, gpointer data)
{
  shell_reply_ok(data, audio_json(available, volume, muted));
}

static void
audio_set_reply(gboolean available, int volume, gboolean muted, gpointer data)
{
  shell_emit(SM(SURF_CHROME), "audio.changed",
             audio_json(available, volume, muted));
  shell_reply_ok(data, audio_json(available, volume, muted));
}

static void
brightness_reply(gboolean available, int percent, gpointer data)
{
  shell_reply_ok(data, brightness_json(available, percent));
}

static void
brightness_set_reply(gboolean available, int percent, gpointer data)
{
  shell_emit(SM(SURF_CHROME), "brightness.changed", brightness_json(available, percent));
  shell_reply_ok(data, brightness_json(available, percent));
}

static void
done_reply(gboolean ok, const char *error, gpointer data)
{
  if (ok)
    shell_reply_ok(data, NULL);
  else
    shell_reply_fail(data, error);
}

static void
reply_error(ShellReply *r, GError *err, const char *fallback)
{
  shell_reply_fail(r, err != NULL ? err->message : fallback);
}

/* The page's part of the web session (never the refresh token). */
static void
session_reply(JsonObject *session, gpointer data)
{
  ShellReply *r = data;
  if (session == NULL) {
    shell_reply_fail(r, "no desktop session (is NoCapOS running?)");
    return;
  }
  JsonObject *o = json_object_new();
  static const char *const keep[] = {"access_token", "token_type", "expires_at", "apiOrigin",
                                     "user",         "realName",   "admin",      "account", NULL};
  for (int i = 0; keep[i] != NULL; i++) {
    JsonNode *n = json_object_get_member(session, keep[i]);
    if (n != NULL)
      json_object_set_member(o, keep[i], json_node_copy(n));
  }
  shell_reply_ok(r, jobj_node(o));
}

/* NoCap OS apps open in windows of their own: another nocap-shell. */
static void
open_app(const BridgeCall *call, ShellReply *r)
{
  const char *app = bridge_param_str(call, "app");
  const char *props = bridge_param_has(call, "props") ? bridge_param_str(call, "props") : "";
  gboolean single = bridge_param_bool(call, "single", FALSE);
  const char *argv[8];
  int n = 0;
  argv[n++] = shell.self_exe;
  argv[n++] = "--app";
  argv[n++] = app;
  if (props != NULL && *props) {
    argv[n++] = "--props";
    argv[n++] = props;
  }
  if (single)
    argv[n++] = "--single";
  argv[n] = NULL;
  GError *err = NULL;
  if (spawn_detached(argv, &err))
    shell_reply_ok(r, NULL);
  else
    reply_error(r, err, "couldn't open the app");
  g_clear_error(&err);
}

void
dispatch_call(SurfaceId surface, BridgeCall *call)
{
  const char *m = call->method;
  ShellReply *r = shell_reply_new(surface, call->id);
  GError *err = NULL;

  if (strcmp(m, "shell.info") == 0) {
    surfaces_info_called(surface);
    shell_reply_ok(r, shell_info_json(surface));
  } else if (strcmp(m, "session.get") == 0 || strcmp(m, "session.refresh") == 0) {
    alfad_session(m[8] == 'r', session_reply, r);
  } else if (strcmp(m, "surface.input") == 0) {
    surface_set_input(surface, bridge_param_array(call, "rects"), bridge_param_bool(call, "full", FALSE));
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "surface.zones") == 0) {
    surface_set_zones((int)bridge_param_int(call, "top", -1), (int)bridge_param_int(call, "bottom", -1),
                      (int)bridge_param_int(call, "left", -1), (int)bridge_param_int(call, "right", -1));
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "surface.keyboard") == 0) {
    surface_set_keyboard(surface, bridge_param_str(call, "mode"));
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "ui.command") == 0) {
    /* The desktop's menu (or an app) asks the chrome for a panel. */
    JsonObject *o = json_object_new();
    json_object_set_string_member(o, "name", bridge_param_str(call, "name"));
    json_object_set_string_member(o, "action", "show");
    shell_emit(SM(SURF_CHROME), "ui.command", jobj_node(o));
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "nocap.open") == 0) {
    open_app(call, r);
  } else if (strcmp(m, "apps.list") == 0) {
    shell_reply_ok(r, apps_list_json());
  } else if (strcmp(m, "apps.launch") == 0) {
    if (apps_launch(bridge_param_str(call, "id"), &err))
      shell_reply_ok(r, NULL);
    else
      reply_error(r, err, "couldn't open the app");
  } else if (strcmp(m, "windows.list") == 0) {
    shell_reply_ok(r, toplevel_list_json());
  } else if (g_str_has_prefix(m, "windows.")) {
    if (toplevel_action(bridge_param_str(call, "id"), m + strlen("windows."), &err))
      shell_reply_ok(r, NULL);
    else
      reply_error(r, err, "window action failed");
  } else if (strcmp(m, "window.action") == 0) {
    if (app_window_action(bridge_param_str(call, "action"), &err))
      shell_reply_ok(r, NULL);
    else
      reply_error(r, err, "window action failed");
  } else if (strcmp(m, "window.title") == 0) {
    app_window_title(bridge_param_str(call, "title"));
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "files.pick") == 0) {
    picker_pick(surface, bridge_param_int(call, "request", 0), bridge_param_array(call, "paths"),
                bridge_param_array(call, "urls"), bridge_param_array(call, "names"), r);
  } else if (strcmp(m, "files.cancel") == 0) {
    picker_cancel(surface, bridge_param_int(call, "request", 0), r);
  } else if (strcmp(m, "audio.get") == 0) {
    audio_get(audio_reply, r);
  } else if (strcmp(m, "audio.set") == 0) {
    int vol = bridge_param_has(call, "volume") ? (int)bridge_param_int(call, "volume", -1) : -1;
    int mute = bridge_param_has(call, "muted") ? (bridge_param_bool(call, "muted", FALSE) ? 1 : 0) : -1;
    audio_set(vol, mute, audio_set_reply, r);
  } else if (strcmp(m, "brightness.get") == 0) {
    brightness_get(brightness_reply, r);
  } else if (strcmp(m, "brightness.set") == 0) {
    brightness_set((int)bridge_param_int(call, "percent", 100), brightness_set_reply, r);
  } else if (strcmp(m, "network.get") == 0) {
    shell_reply_ok(r, network_json());
  } else if (strcmp(m, "power.action") == 0) {
    const char *action = bridge_param_str(call, "action");
    if (shell.greeter)
      greeter_power(action, r);
    else
      power_action(action, done_reply, r);
  } else if (strcmp(m, "notify.list") == 0) {
    shell_reply_ok(r, notify_list_json());
  } else if (strcmp(m, "notify.dismiss") == 0) {
    if (notify_dismiss(bridge_param_str(call, "id")))
      shell_reply_ok(r, NULL);
    else
      shell_reply_fail(r, "unknown notification");
  } else if (strcmp(m, "notify.invoke") == 0) {
    if (notify_invoke(bridge_param_str(call, "id"), bridge_param_str(call, "key")))
      shell_reply_ok(r, NULL);
    else
      shell_reply_fail(r, "unknown notification or action");
  } else if (strcmp(m, "notify.clear") == 0) {
    notify_clear();
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "notify.read") == 0) {
    notify_read(bridge_param_has(call, "id") ? bridge_param_str(call, "id") : NULL);
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "polkit.respond") == 0) {
    gboolean ok = polkit_respond(bridge_param_str(call, "cookie"), bridge_param_str(call, "password"), &err);
    bridge_param_wipe(call, "password");
    if (ok)
      shell_reply_ok(r, NULL);
    else
      reply_error(r, err, "no such request");
  } else if (strcmp(m, "polkit.cancel") == 0) {
    if (polkit_cancel(bridge_param_str(call, "cookie"), &err))
      shell_reply_ok(r, NULL);
    else
      reply_error(r, err, "no such request");
  } else if (strcmp(m, "greeter.users") == 0) {
    shell_reply_ok(r, greeter_users_json());
  } else if (strcmp(m, "greeter.login") == 0) {
    greeter_login(bridge_param_str(call, "username"), r);
  } else if (strcmp(m, "greeter.respond") == 0) {
    gboolean has = bridge_param_has(call, "response");
    greeter_respond(has ? bridge_param_str(call, "response") : NULL, has, r);
    bridge_param_wipe(call, "response");
  } else if (strcmp(m, "greeter.cancel") == 0) {
    greeter_cancel(r);
  } else {
    shell_reply_fail(r, "not implemented");
  }
  g_clear_error(&err);
}
