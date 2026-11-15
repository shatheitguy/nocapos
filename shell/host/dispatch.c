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
  shell_emit(SM(SURF_CENTER) | SM(SURF_BAR) | SM(SURF_OSD), "audio.changed",
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
  shell_emit(SM(SURF_CENTER) | SM(SURF_OSD), "brightness.changed", brightness_json(available, percent));
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

static SurfaceId
named_surface(const BridgeCall *c)
{
  int id = surface_from_name(bridge_param_str(c, "name"));
  return id < 0 ? SURF_COUNT : (SurfaceId)id;
}

static void
reply_error(ShellReply *r, GError *err, const char *fallback)
{
  shell_reply_fail(r, err != NULL ? err->message : fallback);
}

static void
open_nocapos(const char *app, ShellReply *r)
{
  const char *argv[] = {shell.self_exe, "--app", app != NULL ? app : "home", NULL};
  GError *err = NULL;
  if (spawn_detached(argv, &err))
    shell_reply_ok(r, NULL);
  else
    reply_error(r, err, "couldn't open NoCapOS");
  g_clear_error(&err);
  surface_hide(SURF_CENTER);
  surface_hide(SURF_LAUNCHER);
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
  } else if (strcmp(m, "surface.show") == 0 || strcmp(m, "surface.hide") == 0 ||
             strcmp(m, "surface.toggle") == 0) {
    SurfaceId t = named_surface(call);
    if (t >= SURF_COUNT) {
      shell_reply_fail(r, "unknown surface");
      return;
    }
    if (m[8] == 's')
      surface_show(t);
    else if (m[8] == 'h')
      surface_hide(t);
    else
      surface_toggle(t);
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "surface.size") == 0) {
    int h = (int)bridge_param_int(call, "height", 0);
    if (h <= 0) {
      surface_hide(surface);
    } else {
      surface_set_height(surface, h);
      if (!surface_is_visible(surface))
        surface_show(surface);
    }
    shell_reply_ok(r, NULL);
  } else if (strcmp(m, "apps.list") == 0) {
    shell_reply_ok(r, apps_list_json());
  } else if (strcmp(m, "apps.launch") == 0) {
    if (apps_launch(bridge_param_str(call, "id"), &err)) {
      shell_reply_ok(r, NULL);
      surface_hide(SURF_LAUNCHER);
    } else {
      reply_error(r, err, "couldn't open the app");
    }
  } else if (strcmp(m, "dock.get") == 0) {
    shell_reply_ok(r, dock_get_json());
  } else if (strcmp(m, "dock.set") == 0) {
    JsonNode *res = dock_set(bridge_param_array(call, "pinned"), &err);
    if (res != NULL) {
      shell_emit(SM(SURF_DOCK) | SM(SURF_LAUNCHER), "dock.changed", json_node_copy(res));
      shell_reply_ok(r, res);
    } else {
      reply_error(r, err, "couldn't save the dock");
    }
  } else if (strcmp(m, "windows.list") == 0) {
    shell_reply_ok(r, toplevel_list_json());
  } else if (g_str_has_prefix(m, "windows.")) {
    if (toplevel_action(bridge_param_str(call, "id"), m + strlen("windows."), &err))
      shell_reply_ok(r, NULL);
    else
      reply_error(r, err, "window action failed");
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
    else {
      surface_hide(SURF_CENTER);
      power_action(action, done_reply, r);
    }
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
  } else if (strcmp(m, "nocapos.open") == 0) {
    open_nocapos(bridge_param_has(call, "app") ? bridge_param_str(call, "app") : NULL, r);
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
