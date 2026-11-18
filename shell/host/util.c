/* Small helpers: async commands with fixed argv, detached spawns, JSON. */
#include "shell.h"

#include <string.h>

typedef struct {
  GSubprocess *proc;
  GCancellable *cancel;
  guint timeout_id;
  CmdCb cb;
  gpointer data;
} Cmd;

static gboolean
cmd_timeout(gpointer user)
{
  Cmd *c = user;
  c->timeout_id = 0;
  g_cancellable_cancel(c->cancel);
  g_subprocess_force_exit(c->proc);
  return G_SOURCE_REMOVE;
}

static void
cmd_done(GObject *src, GAsyncResult *res, gpointer user)
{
  Cmd *c = user;
  char *out = NULL;
  GError *err = NULL;
  gboolean ok = g_subprocess_communicate_utf8_finish(G_SUBPROCESS(src), res, &out, NULL, &err);
  if (ok)
    ok = g_subprocess_get_if_exited(c->proc) && g_subprocess_get_exit_status(c->proc) == 0;
  g_clear_error(&err);
  if (c->timeout_id)
    g_source_remove(c->timeout_id);
  c->cb(out, ok, c->data);
  g_object_unref(c->cancel);
  g_object_unref(c->proc);
  g_free(c);
}

/* Runs argv (no shell), collects stdout, calls cb(output, success). */
void
run_command(const char *const *argv, guint timeout_ms, CmdCb cb, gpointer data)
{
  GError *err = NULL;
  GSubprocess *proc = g_subprocess_newv(
      argv, G_SUBPROCESS_FLAGS_STDOUT_PIPE | G_SUBPROCESS_FLAGS_STDERR_SILENCE, &err);
  if (proc == NULL) {
    g_clear_error(&err);
    cb(NULL, FALSE, data);
    return;
  }
  Cmd *c = g_new0(Cmd, 1);
  c->proc = proc;
  c->cancel = g_cancellable_new();
  c->cb = cb;
  c->data = data;
  c->timeout_id = g_timeout_add(timeout_ms ? timeout_ms : 3000, cmd_timeout, c);
  g_subprocess_communicate_utf8_async(proc, NULL, c->cancel, cmd_done, c);
}

/* Absolute path of a tool in a fixed set of system directories. */
const char *
find_tool(const char *name)
{
  static GHashTable *cache;
  static const char *const dirs[] = {"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin", NULL};
  if (cache == NULL)
    cache = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, g_free);
  gpointer hit;
  if (g_hash_table_lookup_extended(cache, name, NULL, &hit))
    return hit;
  char *found = NULL;
  for (int i = 0; dirs[i] != NULL && found == NULL; i++) {
    char *p = g_build_filename(dirs[i], name, NULL);
    if (g_file_test(p, G_FILE_TEST_IS_EXECUTABLE) && !g_file_test(p, G_FILE_TEST_IS_DIR))
      found = p;
    else
      g_free(p);
  }
  g_hash_table_insert(cache, g_strdup(name), found);
  return found;
}

/* Spawns a process we do not wait for (GLib reaps it via a double fork). */
gboolean
spawn_detached(const char *const *argv, GError **error)
{
  return g_spawn_async(NULL, (char **)argv, NULL, G_SPAWN_STDIN_FROM_DEV_NULL, NULL, NULL, NULL,
                       error);
}

/* Serialises a JSON node so it can be embedded as a JS expression. */
char *
json_for_script(JsonNode *node)
{
  if (node == NULL)
    return g_strdup("null");
  JsonGenerator *gen = json_generator_new();
  json_generator_set_root(gen, node);
  char *text = json_generator_to_data(gen, NULL);
  g_object_unref(gen);
  /* U+2028/U+2029 are valid in JSON but were line terminators in older JS. */
  if (strstr(text, "\xe2\x80\xa8") == NULL && strstr(text, "\xe2\x80\xa9") == NULL)
    return text;
  GString *s = g_string_sized_new(strlen(text) + 16);
  for (const char *p = text; *p; p++) {
    if ((unsigned char)p[0] == 0xe2 && (unsigned char)p[1] == 0x80 &&
        ((unsigned char)p[2] == 0xa8 || (unsigned char)p[2] == 0xa9)) {
      g_string_append(s, (unsigned char)p[2] == 0xa8 ? "\\u2028" : "\\u2029");
      p += 2;
    } else {
      g_string_append_c(s, *p);
    }
  }
  g_free(text);
  return g_string_free(s, FALSE);
}

JsonNode *
json_str_node(const char *s)
{
  JsonNode *n = json_node_new(JSON_NODE_VALUE);
  json_node_set_string(n, s ? s : "");
  return n;
}

JsonNode *
jobj_node(JsonObject *o)
{
  JsonNode *n = json_node_new(JSON_NODE_OBJECT);
  json_node_take_object(n, o);
  return n;
}

JsonNode *
jarr_node(JsonArray *a)
{
  JsonNode *n = json_node_new(JSON_NODE_ARRAY);
  json_node_take_array(n, a);
  return n;
}

void
secure_wipe(void *p, gsize n)
{
  if (p == NULL || n == 0)
    return;
  volatile unsigned char *v = p;
  while (n--)
    *v++ = 0;
}

/* Copies at most max_bytes of valid UTF-8, never splitting a character. */
char *
utf8_truncate(const char *s, gsize max_bytes)
{
  if (s == NULL)
    return g_strdup("");
  char *valid = g_utf8_make_valid(s, -1);
  if (strlen(valid) <= max_bytes)
    return valid;
  const char *end = valid;
  while (*end) {
    const char *next = g_utf8_next_char(end);
    if ((gsize)(next - valid) > max_bytes)
      break;
    end = next;
  }
  char *out = g_strndup(valid, end - valid);
  g_free(valid);
  return out;
}
