/* Open windows through wlr-foreign-toplevel-management on GDK's display. */
#include "shell.h"

#include <gdk/gdkwayland.h>
#include <stdlib.h>
#include <string.h>
#include <wayland-client.h>

#include "wlr-foreign-toplevel-management-unstable-v1-client-protocol.h"

typedef struct {
  struct zwlr_foreign_toplevel_handle_v1 *handle;
  guint id;
  char *app_id;
  char *title;
  gboolean active, minimized, maximized, fullscreen;
  gboolean p_active, p_minimized, p_maximized, p_fullscreen;
} Win;

static struct wl_registry *registry;
static struct zwlr_foreign_toplevel_manager_v1 *manager;
static GList *wins; /* Win*, in creation order */
static guint next_id = 1;
static guint changed_id;

static gboolean
emit_changed(gpointer u)
{
  changed_id = 0;
  shell_emit(SM(SURF_DOCK) | SM(SURF_BAR), "windows.changed", toplevel_list_json());
  return G_SOURCE_REMOVE;
}

static void
schedule_changed(void)
{
  if (changed_id == 0)
    changed_id = g_timeout_add(100, emit_changed, NULL);
}

static void
handle_title(void *data, struct zwlr_foreign_toplevel_handle_v1 *h, const char *title)
{
  Win *w = data;
  g_free(w->title);
  w->title = utf8_truncate(title, 1024);
}

static void
handle_app_id(void *data, struct zwlr_foreign_toplevel_handle_v1 *h, const char *app_id)
{
  Win *w = data;
  g_free(w->app_id);
  w->app_id = utf8_truncate(app_id, 256);
}

static void
handle_output_enter(void *data, struct zwlr_foreign_toplevel_handle_v1 *h, struct wl_output *o)
{
}

static void
handle_output_leave(void *data, struct zwlr_foreign_toplevel_handle_v1 *h, struct wl_output *o)
{
}

static void
handle_state(void *data, struct zwlr_foreign_toplevel_handle_v1 *h, struct wl_array *state)
{
  Win *w = data;
  w->p_active = w->p_minimized = w->p_maximized = w->p_fullscreen = FALSE;
  uint32_t *s;
  wl_array_for_each(s, state)
  {
    switch (*s) {
    case ZWLR_FOREIGN_TOPLEVEL_HANDLE_V1_STATE_ACTIVATED:
      w->p_active = TRUE;
      break;
    case ZWLR_FOREIGN_TOPLEVEL_HANDLE_V1_STATE_MINIMIZED:
      w->p_minimized = TRUE;
      break;
    case ZWLR_FOREIGN_TOPLEVEL_HANDLE_V1_STATE_MAXIMIZED:
      w->p_maximized = TRUE;
      break;
    case ZWLR_FOREIGN_TOPLEVEL_HANDLE_V1_STATE_FULLSCREEN:
      w->p_fullscreen = TRUE;
      break;
    default:
      break;
    }
  }
}

static void
handle_done(void *data, struct zwlr_foreign_toplevel_handle_v1 *h)
{
  Win *w = data;
  w->active = w->p_active;
  w->minimized = w->p_minimized;
  w->maximized = w->p_maximized;
  w->fullscreen = w->p_fullscreen;
  schedule_changed();
}

static void
win_free(Win *w)
{
  if (w->handle != NULL)
    zwlr_foreign_toplevel_handle_v1_destroy(w->handle);
  g_free(w->app_id);
  g_free(w->title);
  g_free(w);
}

static void
handle_closed(void *data, struct zwlr_foreign_toplevel_handle_v1 *h)
{
  Win *w = data;
  wins = g_list_remove(wins, w);
  win_free(w);
  schedule_changed();
}

static void
handle_parent(void *data, struct zwlr_foreign_toplevel_handle_v1 *h,
              struct zwlr_foreign_toplevel_handle_v1 *parent)
{
}

static const struct zwlr_foreign_toplevel_handle_v1_listener handle_listener = {
  .title = handle_title,
  .app_id = handle_app_id,
  .output_enter = handle_output_enter,
  .output_leave = handle_output_leave,
  .state = handle_state,
  .done = handle_done,
  .closed = handle_closed,
  .parent = handle_parent,
};

static void
manager_toplevel(void *data, struct zwlr_foreign_toplevel_manager_v1 *m,
                 struct zwlr_foreign_toplevel_handle_v1 *handle)
{
  Win *w = g_new0(Win, 1);
  w->handle = handle;
  w->id = next_id++;
  w->app_id = g_strdup("");
  w->title = g_strdup("");
  wins = g_list_append(wins, w);
  zwlr_foreign_toplevel_handle_v1_add_listener(handle, &handle_listener, w);
}

static void
manager_finished(void *data, struct zwlr_foreign_toplevel_manager_v1 *m)
{
  zwlr_foreign_toplevel_manager_v1_destroy(m);
  if (manager == m)
    manager = NULL;
}

static const struct zwlr_foreign_toplevel_manager_v1_listener manager_listener = {
  .toplevel = manager_toplevel,
  .finished = manager_finished,
};

static void
registry_global(void *data, struct wl_registry *r, uint32_t name, const char *iface, uint32_t version)
{
  if (manager == NULL && strcmp(iface, zwlr_foreign_toplevel_manager_v1_interface.name) == 0) {
    manager = wl_registry_bind(r, name, &zwlr_foreign_toplevel_manager_v1_interface,
                               version < 3 ? version : 3);
    zwlr_foreign_toplevel_manager_v1_add_listener(manager, &manager_listener, NULL);
  }
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
toplevel_init(void)
{
  GdkDisplay *d = gdk_display_get_default();
  if (d == NULL || !GDK_IS_WAYLAND_DISPLAY(d))
    return;
  struct wl_display *wl = gdk_wayland_display_get_wl_display(d);
  registry = wl_display_get_registry(wl);
  wl_registry_add_listener(registry, &registry_listener, NULL);
  wl_display_roundtrip(wl);
  if (manager == NULL)
    g_message("compositor has no foreign toplevel manager: window list disabled");
}

gboolean
toplevel_available(void)
{
  return manager != NULL;
}

JsonNode *
toplevel_list_json(void)
{
  JsonArray *arr = json_array_new();
  for (GList *l = wins; l != NULL; l = l->next) {
    Win *w = l->data;
    JsonObject *o = json_object_new();
    json_object_set_int_member(o, "id", w->id);
    json_object_set_string_member(o, "appId", w->app_id);
    json_object_set_string_member(o, "title", w->title);
    json_object_set_boolean_member(o, "active", w->active);
    json_object_set_boolean_member(o, "minimized", w->minimized);
    json_object_set_boolean_member(o, "maximized", w->maximized);
    json_array_add_object_element(arr, o);
  }
  return jarr_node(arr);
}

static Win *
find(const char *id)
{
  char *end = NULL;
  guint64 v = g_ascii_strtoull(id, &end, 10);
  if (end == id || *end != '\0')
    return NULL;
  for (GList *l = wins; l != NULL; l = l->next)
    if (((Win *)l->data)->id == v)
      return l->data;
  return NULL;
}

gboolean
toplevel_action(const char *id, const char *action, GError **error)
{
  Win *w = find(id);
  if (w == NULL) {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_NOT_FOUND, "unknown window");
    return FALSE;
  }
  if (strcmp(action, "focus") == 0) {
    GdkSeat *seat = gdk_display_get_default_seat(gdk_display_get_default());
    struct wl_seat *wl_seat = seat ? gdk_wayland_seat_get_wl_seat(seat) : NULL;
    if (wl_seat == NULL) {
      g_set_error(error, G_IO_ERROR, G_IO_ERROR_NOT_SUPPORTED, "no seat");
      return FALSE;
    }
    if (w->minimized)
      zwlr_foreign_toplevel_handle_v1_unset_minimized(w->handle);
    zwlr_foreign_toplevel_handle_v1_activate(w->handle, wl_seat);
  } else if (strcmp(action, "minimize") == 0) {
    zwlr_foreign_toplevel_handle_v1_set_minimized(w->handle);
  } else if (strcmp(action, "maximize") == 0) {
    if (w->maximized)
      zwlr_foreign_toplevel_handle_v1_unset_maximized(w->handle);
    else
      zwlr_foreign_toplevel_handle_v1_set_maximized(w->handle);
  } else if (strcmp(action, "close") == 0) {
    zwlr_foreign_toplevel_handle_v1_close(w->handle);
  } else {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_INVALID_ARGUMENT, "unknown action");
    return FALSE;
  }
  wl_display_flush(gdk_wayland_display_get_wl_display(gdk_display_get_default()));
  return TRUE;
}
