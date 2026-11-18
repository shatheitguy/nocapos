/* Notification server (org.freedesktop.Notifications, spec 1.2) plus NoCapOS
 * notifications from alfad, kept in one in-memory list. */
#include "shell.h"

#include <gio/gdesktopappinfo.h>
#include <string.h>

#define MAX_NOTES 100
#define MAX_ACTIONS 8
#define ICON_SIZE 48
#define BANNER_MS 6000
#define BANNER_FOREVER G_MAXINT64

typedef struct {
  char *id;        /* "a-<n>" for apps, "n-<alfad id>" for NoCapOS */
  guint32 dbus_id; /* apps only */
  gboolean remote;
  JsonNode *remote_id; /* the id as alfad sent it */
  char *app, *icon, *title, *body;
  JsonNode *time; /* number (ms) or alfad's value */
  int urgency;
  GPtrArray *actions; /* key, label, key, label ... */
  gboolean read;
  gboolean resident;
  gint64 banner_until; /* monotonic us; 0 = no banner */
} Note;

static GQueue notes = G_QUEUE_INIT; /* newest first */
static guint32 next_dbus_id = 1;
static GDBusConnection *bus;
static guint reg_id;
static guint banner_timer;

/* NoCap OS's own notifications reach the desktop through its live
 * connection to alfad (the web OS's notification center); only apps'
 * desktop notifications come through here, so there's nothing to mark. */
static void
alfad_mark_read(JsonArray *ids, gboolean all)
{
  (void)ids;
  (void)all;
}
static gboolean remote_initialised;

enum { CLOSE_EXPIRED = 1, CLOSE_DISMISSED = 2, CLOSE_CALLED = 3 };

static const char introspection_xml[] =
    "<node><interface name='org.freedesktop.Notifications'>"
    "<method name='GetCapabilities'><arg type='as' name='caps' direction='out'/></method>"
    "<method name='Notify'>"
    "<arg type='s' name='app_name' direction='in'/><arg type='u' name='replaces_id' direction='in'/>"
    "<arg type='s' name='app_icon' direction='in'/><arg type='s' name='summary' direction='in'/>"
    "<arg type='s' name='body' direction='in'/><arg type='as' name='actions' direction='in'/>"
    "<arg type='a{sv}' name='hints' direction='in'/><arg type='i' name='expire_timeout' direction='in'/>"
    "<arg type='u' name='id' direction='out'/></method>"
    "<method name='CloseNotification'><arg type='u' name='id' direction='in'/></method>"
    "<method name='GetServerInformation'><arg type='s' name='name' direction='out'/>"
    "<arg type='s' name='vendor' direction='out'/><arg type='s' name='version' direction='out'/>"
    "<arg type='s' name='spec_version' direction='out'/></method>"
    "<signal name='NotificationClosed'><arg type='u' name='id'/><arg type='u' name='reason'/></signal>"
    "<signal name='ActionInvoked'><arg type='u' name='id'/><arg type='s' name='action_key'/></signal>"
    "</interface></node>";

static void
note_free(Note *n)
{
  g_free(n->id);
  if (n->remote_id)
    json_node_unref(n->remote_id);
  g_free(n->app);
  g_free(n->icon);
  g_free(n->title);
  g_free(n->body);
  if (n->time)
    json_node_unref(n->time);
  if (n->actions)
    g_ptr_array_unref(n->actions);
  g_free(n);
}

static JsonNode *
note_json(Note *n)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "id", n->id);
  json_object_set_string_member(o, "source", n->remote ? "nocapos" : "app");
  json_object_set_string_member(o, "app", n->app);
  json_object_set_string_member(o, "icon", n->icon);
  json_object_set_string_member(o, "title", n->title);
  json_object_set_string_member(o, "body", n->body);
  json_object_set_member(o, "time", json_node_copy(n->time));
  json_object_set_int_member(o, "urgency", n->urgency);
  JsonArray *acts = json_array_new();
  for (guint i = 0; n->actions && i + 1 < n->actions->len; i += 2) {
    JsonObject *a = json_object_new();
    json_object_set_string_member(a, "key", g_ptr_array_index(n->actions, i));
    json_object_set_string_member(a, "label", g_ptr_array_index(n->actions, i + 1));
    json_array_add_object_element(acts, a);
  }
  json_object_set_array_member(o, "actions", acts);
  json_object_set_boolean_member(o, "read", n->read);
  return jobj_node(o);
}

JsonNode *
notify_list_json(void)
{
  JsonArray *arr = json_array_new();
  for (GList *l = notes.head; l != NULL; l = l->next)
    json_array_add_element(arr, note_json(l->data));
  return jarr_node(arr);
}

#define NOTIFY_SURFACES SM(SURF_CHROME)

static void
emit_unread(void)
{
  int count = 0;
  for (GList *l = notes.head; l != NULL; l = l->next)
    if (!((Note *)l->data)->read)
      count++;
  JsonObject *o = json_object_new();
  json_object_set_int_member(o, "count", count);
  shell_emit(NOTIFY_SURFACES, "notify.unread", jobj_node(o));
}

static void
emit_closed(Note *n)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "id", n->id);
  shell_emit(NOTIFY_SURFACES, "notify.closed", jobj_node(o));
}

/* ---------- banners ---------- */

static void banners_update(void);

static gboolean
banner_timeout(gpointer u)
{
  banner_timer = 0;
  banners_update();
  return G_SOURCE_REMOVE;
}

/* Keeps the banners surface up while any note still wants a banner. */
static void
banners_update(void)
{
  gint64 now = g_get_monotonic_time();
  gint64 next = 0;
  gboolean any = FALSE;
  for (GList *l = notes.head; l != NULL; l = l->next) {
    Note *n = l->data;
    if (n->banner_until > now) {
      any = TRUE;
      if (n->banner_until != BANNER_FOREVER && (next == 0 || n->banner_until < next))
        next = n->banner_until;
    } else {
      n->banner_until = 0;
    }
  }
  if (banner_timer) {
    g_source_remove(banner_timer);
    banner_timer = 0;
  }
  if (!any)
    return;
  if (next != 0)
    banner_timer = g_timeout_add((guint)MAX(50, (next - now) / 1000 + 20), banner_timeout, NULL);
}

static void
banner_start(Note *n, int expire_ms)
{
  if (n->read)
    return;
  if (n->urgency >= 2)
    n->banner_until = BANNER_FOREVER;
  else
    n->banner_until = g_get_monotonic_time() +
                      (gint64)(expire_ms > 0 ? CLAMP(expire_ms, 2000, 30000) : BANNER_MS) * 1000;
}

/* ---------- list management ---------- */

static Note *
find_note(const char *id)
{
  for (GList *l = notes.head; l != NULL; l = l->next)
    if (strcmp(((Note *)l->data)->id, id) == 0)
      return l->data;
  return NULL;
}

static Note *
find_dbus(guint32 id)
{
  for (GList *l = notes.head; l != NULL; l = l->next) {
    Note *n = l->data;
    if (!n->remote && n->dbus_id == id)
      return n;
  }
  return NULL;
}

static void
signal_closed(Note *n, guint32 reason)
{
  if (bus == NULL || n->remote || reg_id == 0)
    return;
  g_dbus_connection_emit_signal(bus, NULL, "/org/freedesktop/Notifications",
                                "org.freedesktop.Notifications", "NotificationClosed",
                                g_variant_new("(uu)", n->dbus_id, reason), NULL);
}

static void
remove_note(Note *n, guint32 reason)
{
  g_queue_remove(&notes, n);
  signal_closed(n, reason);
  emit_closed(n);
  note_free(n);
}

static void
push_note(Note *n)
{
  g_queue_push_head(&notes, n);
  while (g_queue_get_length(&notes) > MAX_NOTES) {
    Note *old = g_queue_pop_tail(&notes);
    signal_closed(old, CLOSE_EXPIRED);
    emit_closed(old);
    note_free(old);
  }
}

static void
remote_mark_read(Note *n)
{
  if (!n->remote || n->remote_id == NULL)
    return;
  JsonArray *ids = json_array_new();
  json_array_add_element(ids, json_node_copy(n->remote_id));
  alfad_mark_read(ids, FALSE);
  json_array_unref(ids);
}

gboolean
notify_dismiss(const char *id)
{
  Note *n = find_note(id);
  if (n == NULL)
    return FALSE;
  if (!n->read)
    remote_mark_read(n);
  remove_note(n, CLOSE_DISMISSED);
  banners_update();
  emit_unread();
  return TRUE;
}

static gboolean
note_has_action(Note *n, const char *key)
{
  for (guint i = 0; n->actions && i + 1 < n->actions->len; i += 2)
    if (strcmp(g_ptr_array_index(n->actions, i), key) == 0)
      return TRUE;
  return FALSE;
}

gboolean
notify_invoke(const char *id, const char *key)
{
  Note *n = find_note(id);
  if (n == NULL)
    return FALSE;
  if (n->remote) {
    if (!n->read)
      remote_mark_read(n);
    n->read = TRUE;
    n->banner_until = 0;
    const char *argv[] = {shell.self_exe, "--app", "home", NULL};
    if (shell.self_exe != NULL)
      spawn_detached(argv, NULL);
    banners_update();
    emit_unread();
    return TRUE;
  }
  if (!note_has_action(n, key) && strcmp(key, "default") != 0)
    return FALSE;
  if (bus != NULL && reg_id != 0)
    g_dbus_connection_emit_signal(bus, NULL, "/org/freedesktop/Notifications",
                                  "org.freedesktop.Notifications", "ActionInvoked",
                                  g_variant_new("(us)", n->dbus_id, key), NULL);
  if (n->resident) {
    n->read = TRUE;
    n->banner_until = 0;
  } else {
    remove_note(n, CLOSE_DISMISSED);
  }
  banners_update();
  emit_unread();
  return TRUE;
}

void
notify_clear(void)
{
  gboolean any_remote = FALSE;
  while (!g_queue_is_empty(&notes)) {
    Note *n = g_queue_peek_head(&notes);
    if (n->remote && !n->read)
      any_remote = TRUE;
    remove_note(n, CLOSE_DISMISSED);
  }
  if (any_remote)
    alfad_mark_read(NULL, TRUE);
  banners_update();
  emit_unread();
}

void
notify_read(const char *id)
{
  if (id != NULL) {
    Note *n = find_note(id);
    if (n == NULL)
      return;
    if (!n->read)
      remote_mark_read(n);
    n->read = TRUE;
    n->banner_until = 0;
  } else {
    gboolean any_remote = FALSE;
    for (GList *l = notes.head; l != NULL; l = l->next) {
      Note *n = l->data;
      if (n->remote && !n->read)
        any_remote = TRUE;
      n->read = TRUE;
      n->banner_until = 0;
    }
    if (any_remote)
      alfad_mark_read(NULL, TRUE);
  }
  banners_update();
  emit_unread();
}

/* ---------- D-Bus server ---------- */

static char *
icon_from_notify(const char *app_icon, GVariant *hints)
{
  const char *icon = (app_icon && *app_icon) ? app_icon : NULL;
  const char *image_path = NULL, *desktop = NULL;
  if (hints != NULL) {
    g_variant_lookup(hints, "image-path", "&s", &image_path);
    if (image_path == NULL)
      g_variant_lookup(hints, "image_path", "&s", &image_path);
    g_variant_lookup(hints, "desktop-entry", "&s", &desktop);
  }
  if (icon == NULL && image_path != NULL && *image_path)
    icon = image_path;
  if (icon != NULL) {
    if (g_str_has_prefix(icon, "file://")) {
      char *path = g_filename_from_uri(icon, NULL, NULL);
      char *url = icon_url_for_name(path, ICON_SIZE);
      g_free(path);
      return url;
    }
    return icon_url_for_name(icon, ICON_SIZE);
  }
  if (desktop != NULL && *desktop && strlen(desktop) < 200 && strchr(desktop, '/') == NULL) {
    char *id = g_str_has_suffix(desktop, ".desktop") ? g_strdup(desktop)
                                                      : g_strconcat(desktop, ".desktop", NULL);
    GDesktopAppInfo *info = g_desktop_app_info_new(id);
    g_free(id);
    if (info != NULL) {
      char *url = icon_url_for_gicon(g_app_info_get_icon(G_APP_INFO(info)), ICON_SIZE);
      g_object_unref(info);
      return url;
    }
  }
  return g_strdup("");
}

static JsonNode *
now_ms_node(void)
{
  JsonNode *t = json_node_new(JSON_NODE_VALUE);
  json_node_set_int(t, g_get_real_time() / 1000);
  return t;
}

static guint32
handle_notify(GVariant *params)
{
  const char *app_name, *app_icon, *summary, *body;
  guint32 replaces;
  GVariant *actions, *hints;
  gint32 expire;
  g_variant_get(params, "(&su&s&s&s@as@a{sv}i)", &app_name, &replaces, &app_icon, &summary, &body,
                &actions, &hints, &expire);

  Note *n = replaces ? find_dbus(replaces) : NULL;
  gboolean replacing = n != NULL;
  if (!replacing) {
    n = g_new0(Note, 1);
    n->dbus_id = next_dbus_id++;
    if (next_dbus_id == 0)
      next_dbus_id = 1;
    n->id = g_strdup_printf("a-%u", n->dbus_id);
  } else {
    g_free(n->app);
    g_free(n->icon);
    g_free(n->title);
    g_free(n->body);
    json_node_unref(n->time);
    g_ptr_array_unref(n->actions);
  }
  n->app = utf8_truncate(app_name, 128);
  n->icon = icon_from_notify(app_icon, hints);
  n->title = utf8_truncate(summary, 1024);
  n->body = utf8_truncate(body, 8192);
  n->time = now_ms_node();
  n->read = FALSE;

  guchar urgency = 1;
  if (g_variant_lookup(hints, "urgency", "y", &urgency))
    n->urgency = MIN(urgency, 2);
  else
    n->urgency = 1;
  gboolean resident = FALSE;
  g_variant_lookup(hints, "resident", "b", &resident);
  n->resident = resident;

  n->actions = g_ptr_array_new_with_free_func(g_free);
  gsize na = 0;
  const char **acts = g_variant_get_strv(actions, &na);
  for (gsize i = 0; i + 1 < na && n->actions->len / 2 < MAX_ACTIONS; i += 2) {
    if (acts[i][0] == '\0')
      continue;
    g_ptr_array_add(n->actions, utf8_truncate(acts[i], 64));
    g_ptr_array_add(n->actions, utf8_truncate(acts[i + 1], 128));
  }
  g_free(acts);
  g_variant_unref(actions);
  g_variant_unref(hints);

  if (!replacing)
    push_note(n);
  banner_start(n, expire);
  shell_emit(NOTIFY_SURFACES, "notify.added", note_json(n));
  banners_update();
  emit_unread();
  return n->dbus_id;
}

static void
on_method(GDBusConnection *c, const char *sender, const char *path, const char *iface,
          const char *method, GVariant *params, GDBusMethodInvocation *inv, gpointer u)
{
  if (strcmp(method, "GetCapabilities") == 0) {
    const char *caps[] = {"body", "actions", "icon-static", "persistence", NULL};
    g_dbus_method_invocation_return_value(inv, g_variant_new("(^as)", caps));
  } else if (strcmp(method, "Notify") == 0) {
    guint32 id = handle_notify(params);
    g_dbus_method_invocation_return_value(inv, g_variant_new("(u)", id));
  } else if (strcmp(method, "CloseNotification") == 0) {
    guint32 id;
    g_variant_get(params, "(u)", &id);
    Note *n = find_dbus(id);
    if (n != NULL) {
      remove_note(n, CLOSE_CALLED);
      banners_update();
      emit_unread();
    }
    g_dbus_method_invocation_return_value(inv, NULL);
  } else if (strcmp(method, "GetServerInformation") == 0) {
    g_dbus_method_invocation_return_value(
        inv, g_variant_new("(ssss)", "NoCap Shell", "NoCapOS", NOCAP_VERSION, "1.2"));
  } else {
    g_dbus_method_invocation_return_error(inv, G_DBUS_ERROR, G_DBUS_ERROR_UNKNOWN_METHOD,
                                          "unknown method");
  }
}

static const GDBusInterfaceVTable vtable = {on_method, NULL, NULL};

static void
on_bus_acquired(GDBusConnection *c, const char *name, gpointer u)
{
  GDBusNodeInfo *info = g_dbus_node_info_new_for_xml(introspection_xml, NULL);
  GError *err = NULL;
  bus = c;
  reg_id = g_dbus_connection_register_object(c, "/org/freedesktop/Notifications",
                                             info->interfaces[0], &vtable, NULL, NULL, &err);
  if (reg_id == 0) {
    g_message("notifications: cannot export object: %s", err->message);
    g_clear_error(&err);
  }
  g_dbus_node_info_unref(info);
}

static void
on_name_lost(GDBusConnection *c, const char *name, gpointer u)
{
  if (c == NULL)
    g_message("notifications: no session bus, app notifications disabled");
  else
    g_message("notifications: %s is owned by another program, app notifications disabled", name);
  if (c != NULL && reg_id != 0) {
    g_dbus_connection_unregister_object(c, reg_id);
    reg_id = 0;
  }
}

void
notify_init(void)
{
  g_bus_own_name(G_BUS_TYPE_SESSION, "org.freedesktop.Notifications",
                 G_BUS_NAME_OWNER_FLAGS_DO_NOT_QUEUE, on_bus_acquired, NULL, on_name_lost, NULL, NULL);
}

/* ---------- NoCapOS notifications (alfad) ---------- */

static char *
remote_id_string(JsonNode *id)
{
  if (id == NULL || !JSON_NODE_HOLDS_VALUE(id))
    return NULL;
  char *s = NULL;
  if (json_node_get_value_type(id) == G_TYPE_INT64)
    s = g_strdup_printf("%" G_GINT64_FORMAT, json_node_get_int(id));
  else if (json_node_get_value_type(id) == G_TYPE_STRING)
    s = g_strdup(json_node_get_string(id));
  if (s == NULL || *s == '\0' || strlen(s) > 100) {
    g_free(s);
    return NULL;
  }
  for (const char *p = s; *p; p++)
    if (!(g_ascii_isalnum(*p) || *p == '.' || *p == '_' || *p == '-' || *p == ':')) {
      g_free(s);
      return NULL;
    }
  return s;
}

static const char *
obj_str(JsonObject *o, const char *name)
{
  JsonNode *n = json_object_get_member(o, name);
  if (n == NULL || !JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_STRING)
    return NULL;
  return json_node_get_string(n);
}

void
notify_merge_remote(JsonArray *items)
{
  gboolean announce = remote_initialised;
  gboolean changed = FALSE;
  remote_initialised = TRUE;
  guint len = items ? json_array_get_length(items) : 0;
  for (guint i = 0; i < len && i < MAX_NOTES; i++) {
    JsonNode *el = json_array_get_element(items, i);
    if (!JSON_NODE_HOLDS_OBJECT(el))
      continue;
    JsonObject *o = json_node_get_object(el);
    JsonNode *idn = json_object_get_member(o, "id");
    char *rid = remote_id_string(idn);
    if (rid == NULL)
      continue;
    char *id = g_strconcat("n-", rid, NULL);
    g_free(rid);
    gboolean read = json_object_has_member(o, "read") && json_object_get_boolean_member(o, "read");
    Note *n = find_note(id);
    if (n != NULL) {
      if (read && !n->read) {
        n->read = TRUE;
        n->banner_until = 0;
        changed = TRUE;
      }
      g_free(id);
      continue;
    }
    n = g_new0(Note, 1);
    n->id = id;
    n->remote = TRUE;
    n->remote_id = json_node_copy(idn);
    n->app = utf8_truncate(obj_str(o, "app") ? obj_str(o, "app") : "NoCapOS", 128);
    const char *icon = obj_str(o, "icon");
    if (icon != NULL && g_str_has_prefix(icon, "nocap-shell://icon/"))
      n->icon = utf8_truncate(icon, 1024);
    else if (icon != NULL && icon[0] != '\0' && strstr(icon, "://") == NULL && icon[0] != '/')
      n->icon = icon_url_for_name(icon, ICON_SIZE);
    else
      n->icon = g_strdup("");
    n->title = utf8_truncate(obj_str(o, "title"), 1024);
    n->body = utf8_truncate(obj_str(o, "body"), 8192);
    JsonNode *t = json_object_get_member(o, "time");
    if (t != NULL && JSON_NODE_HOLDS_VALUE(t) &&
        (json_node_get_value_type(t) == G_TYPE_INT64 || json_node_get_value_type(t) == G_TYPE_DOUBLE ||
         (json_node_get_value_type(t) == G_TYPE_STRING && strlen(json_node_get_string(t)) < 64)))
      n->time = json_node_copy(t);
    else
      n->time = now_ms_node();
    JsonNode *u = json_object_get_member(o, "urgency");
    n->urgency = (u && JSON_NODE_HOLDS_VALUE(u) && json_node_get_value_type(u) == G_TYPE_INT64)
                     ? (int)CLAMP(json_node_get_int(u), 0, 2)
                     : 1;
    n->read = read;
    n->actions = g_ptr_array_new_with_free_func(g_free);
    JsonNode *an = json_object_get_member(o, "actions");
    if (an != NULL && JSON_NODE_HOLDS_ARRAY(an)) {
      JsonArray *acts = json_node_get_array(an);
      for (guint k = 0; k < json_array_get_length(acts) && n->actions->len / 2 < MAX_ACTIONS; k++) {
        JsonNode *a = json_array_get_element(acts, k);
        if (!JSON_NODE_HOLDS_OBJECT(a))
          continue;
        const char *key = obj_str(json_node_get_object(a), "key");
        const char *label = obj_str(json_node_get_object(a), "label");
        if (key == NULL || label == NULL || *key == '\0')
          continue;
        g_ptr_array_add(n->actions, utf8_truncate(key, 64));
        g_ptr_array_add(n->actions, utf8_truncate(label, 128));
      }
    }
    push_note(n);
    if (announce)
      banner_start(n, 0);
    shell_emit(NOTIFY_SURFACES, "notify.added", note_json(n));
    changed = TRUE;
  }
  if (changed) {
    banners_update();
    emit_unread();
  }
}
