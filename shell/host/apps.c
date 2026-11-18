/* Installed apps (desktop entries) and the pinned dock list. */
#include "shell.h"

#include <gio/gdesktopappinfo.h>
#include <string.h>

#define ICON_SIZE 64
#define DOCK_MAX 24

static GHashTable *apps;  /* id -> GDesktopAppInfo */
static GPtrArray *sorted; /* GDesktopAppInfo, by name */
static guint changed_id;

static gint
by_name(gconstpointer a, gconstpointer b)
{
  GAppInfo *x = *(GAppInfo *const *)a, *y = *(GAppInfo *const *)b;
  return g_utf8_collate(g_app_info_get_name(x), g_app_info_get_name(y));
}

static void
apps_reload(void)
{
  if (apps != NULL)
    g_hash_table_unref(apps);
  if (sorted != NULL)
    g_ptr_array_unref(sorted);
  apps = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, g_object_unref);
  sorted = g_ptr_array_new();

  GList *all = g_app_info_get_all();
  for (GList *l = all; l != NULL; l = l->next) {
    GAppInfo *info = l->data;
    const char *id = g_app_info_get_id(info);
    if (!G_IS_DESKTOP_APP_INFO(info) || id == NULL || !g_app_info_should_show(info) ||
        g_app_info_get_name(info) == NULL || g_hash_table_contains(apps, id))
      continue;
    g_hash_table_insert(apps, g_strdup(id), g_object_ref(info));
    g_ptr_array_add(sorted, info);
  }
  g_list_free_full(all, g_object_unref);
  g_ptr_array_sort(sorted, by_name);
}

static gboolean
emit_changed(gpointer u)
{
  changed_id = 0;
  apps_reload();
  shell_emit(SM(SURF_CHROME) | SM(SURF_DESKTOP), "apps.changed", jobj_node(json_object_new()));
  return G_SOURCE_REMOVE;
}

static void
on_monitor_changed(GAppInfoMonitor *m, gpointer u)
{
  if (changed_id == 0)
    changed_id = g_timeout_add(500, emit_changed, NULL);
}

void
apps_init(void)
{
  apps_reload();
  g_signal_connect(g_app_info_monitor_get(), "changed", G_CALLBACK(on_monitor_changed), NULL);
}

static JsonArray *
split_list(const char *s)
{
  JsonArray *a = json_array_new();
  if (s == NULL)
    return a;
  char **parts = g_strsplit(s, ";", -1);
  for (int i = 0; parts[i] != NULL; i++)
    if (*parts[i])
      json_array_add_string_element(a, parts[i]);
  g_strfreev(parts);
  return a;
}

JsonNode *
apps_list_json(void)
{
  JsonArray *arr = json_array_new();
  for (guint i = 0; i < sorted->len; i++) {
    GDesktopAppInfo *info = g_ptr_array_index(sorted, i);
    JsonObject *o = json_object_new();
    json_object_set_string_member(o, "id", g_app_info_get_id(G_APP_INFO(info)));
    json_object_set_string_member(o, "name", g_app_info_get_name(G_APP_INFO(info)));
    const char *desc = g_app_info_get_description(G_APP_INFO(info));
    json_object_set_string_member(o, "comment", desc ? desc : "");
    char *icon = icon_url_for_gicon(g_app_info_get_icon(G_APP_INFO(info)), ICON_SIZE);
    json_object_set_string_member(o, "icon", icon);
    g_free(icon);
    json_object_set_array_member(o, "categories", split_list(g_desktop_app_info_get_categories(info)));
    JsonArray *kw = json_array_new();
    const char *const *keywords = g_desktop_app_info_get_keywords(info);
    for (int k = 0; keywords != NULL && keywords[k] != NULL; k++)
      json_array_add_string_element(kw, keywords[k]);
    json_object_set_array_member(o, "keywords", kw);
    json_array_add_object_element(arr, o);
  }
  return jarr_node(arr);
}

gboolean
apps_launch(const char *id, GError **error)
{
  GDesktopAppInfo *info = apps ? g_hash_table_lookup(apps, id) : NULL;
  if (info == NULL) {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_NOT_FOUND, "unknown app");
    return FALSE;
  }
  GdkAppLaunchContext *ctx = gdk_display_get_app_launch_context(gdk_display_get_default());
  gboolean ok = g_desktop_app_info_launch_uris_as_manager(info, NULL, G_APP_LAUNCH_CONTEXT(ctx),
                                                          G_SPAWN_SEARCH_PATH, NULL, NULL, NULL,
                                                          NULL, error);
  g_object_unref(ctx);
  return ok;
}

/* ---------- dock ---------- */

static char *
dock_path(void)
{
  return g_build_filename(g_get_user_config_dir(), "nocapos", "dock.json", NULL);
}

/* Reads a JSON array of desktop ids, keeping installed, unique ones. */
static JsonArray *
read_ids(const char *path)
{
  char *text = NULL;
  gsize len = 0;
  if (path == NULL || !g_file_get_contents(path, &text, &len, NULL))
    return NULL;
  if (len > 64 * 1024) {
    g_free(text);
    return NULL;
  }
  JsonParser *p = json_parser_new();
  JsonArray *out = NULL;
  if (json_parser_load_from_data(p, text, (gssize)len, NULL)) {
    JsonNode *root = json_parser_get_root(p);
    if (root != NULL && JSON_NODE_HOLDS_ARRAY(root)) {
      out = json_array_new();
      JsonArray *in = json_node_get_array(root);
      GHashTable *seen = g_hash_table_new(g_str_hash, g_str_equal);
      for (guint i = 0; i < json_array_get_length(in) && json_array_get_length(out) < DOCK_MAX; i++) {
        JsonNode *n = json_array_get_element(in, i);
        if (!JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_STRING)
          continue;
        const char *id = json_node_get_string(n);
        if (!g_hash_table_contains(apps, id) || g_hash_table_contains(seen, id))
          continue;
        g_hash_table_add(seen, (gpointer)id);
        json_array_add_string_element(out, id);
      }
      g_hash_table_unref(seen);
    }
  }
  g_object_unref(p);
  g_free(text);
  return out;
}

JsonNode *
dock_get_json(void)
{
  char *path = dock_path();
  JsonArray *ids = read_ids(path);
  g_free(path);
  if (ids == NULL) {
    const char *def = g_getenv("NOCAP_DOCK_DEFAULT");
    ids = read_ids(def && *def ? def : "/etc/nocapos/dock-default.json");
  }
  if (ids == NULL)
    ids = json_array_new();
  JsonObject *o = json_object_new();
  json_object_set_array_member(o, "pinned", ids);
  return jobj_node(o);
}

JsonNode *
dock_set(JsonArray *pinned, GError **error)
{
  JsonArray *keep = json_array_new();
  GHashTable *seen = g_hash_table_new(g_str_hash, g_str_equal);
  for (guint i = 0; i < json_array_get_length(pinned) && i < DOCK_MAX; i++) {
    const char *id = json_array_get_string_element(pinned, i);
    if (id == NULL || !g_hash_table_contains(apps, id) || g_hash_table_contains(seen, id))
      continue;
    g_hash_table_add(seen, (gpointer)id);
    json_array_add_string_element(keep, id);
  }
  g_hash_table_unref(seen);

  JsonNode *node = jarr_node(keep);
  JsonGenerator *gen = json_generator_new();
  json_generator_set_root(gen, node);
  char *text = json_generator_to_data(gen, NULL);
  g_object_unref(gen);

  char *path = dock_path();
  char *dir = g_path_get_dirname(path);
  gboolean ok = g_mkdir_with_parents(dir, 0700) == 0 && g_file_set_contents(path, text, -1, error);
  if (!ok && error != NULL && *error == NULL)
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_FAILED, "cannot save the dock");
  g_free(dir);
  g_free(path);
  g_free(text);

  if (!ok) {
    json_node_unref(node);
    return NULL;
  }
  JsonObject *o = json_object_new();
  json_object_set_array_member(o, "pinned", json_array_ref(json_node_get_array(node)));
  json_node_unref(node);
  /* Lets the other dock/launcher page refresh its pins. */
  shell_emit(SM(SURF_CHROME) | SM(SURF_DESKTOP), "apps.changed", jobj_node(json_object_new()));
  return jobj_node(o);
}
