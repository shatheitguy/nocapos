/* alfad client: HTTP/1.1 over the local socket /run/nocapos/shell.sock
 * (alfad knows which Linux account is calling from the socket itself; a
 * per-session token from POST /v1/shell/session goes with every other
 * request). Runs in worker threads; results reach the main loop through
 * idle callbacks. alfad being away never breaks the shell. */
#include "shell.h"

#include <gio/gunixsocketaddress.h>
#include <stdlib.h>
#include <string.h>

#define MAX_BODY (4 * 1024 * 1024)

static GMutex lock;
static char *token; /* guarded by lock */

typedef struct {
  int status;
  char *body;
  gsize len;
} HttpResult;

static void
http_result_clear(HttpResult *r)
{
  g_free(r->body);
  r->body = NULL;
  r->len = 0;
}

/* One request on a fresh connection (Connection: close). */
static gboolean
http(const char *method, const char *path, const char *body, guint timeout_s, HttpResult *res, GError **err)
{
  memset(res, 0, sizeof *res);
  GSocketClient *client = g_socket_client_new();
  g_socket_client_set_timeout(client, timeout_s);
  GSocketAddress *addr = g_unix_socket_address_new(shell.socket_path);
  GSocketConnection *c = g_socket_client_connect(client, G_SOCKET_CONNECTABLE(addr), NULL, err);
  g_object_unref(addr);
  g_object_unref(client);
  if (c == NULL)
    return FALSE;

  g_mutex_lock(&lock);
  char *auth = token ? g_strdup_printf("X-NoCap-Session: %s\r\n", token) : g_strdup("");
  g_mutex_unlock(&lock);
  gsize blen = body ? strlen(body) : 0;
  char *req = g_strdup_printf("%s %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n%s"
                              "Content-Type: application/json\r\nContent-Length: %" G_GSIZE_FORMAT "\r\n\r\n",
                              method, path, auth, blen);
  g_free(auth);
  GOutputStream *out = g_io_stream_get_output_stream(G_IO_STREAM(c));
  gsize w;
  gboolean ok = g_output_stream_write_all(out, req, strlen(req), &w, NULL, err) &&
                (blen == 0 || g_output_stream_write_all(out, body, blen, &w, NULL, err));
  g_free(req);
  if (!ok) {
    g_object_unref(c);
    return FALSE;
  }

  /* Read everything (the server closes), bounded. */
  GInputStream *in = g_io_stream_get_input_stream(G_IO_STREAM(c));
  GByteArray *buf = g_byte_array_new();
  guint8 chunk[8192];
  for (;;) {
    gssize n = g_input_stream_read(in, chunk, sizeof chunk, NULL, err);
    if (n < 0) {
      g_byte_array_unref(buf);
      g_object_unref(c);
      return FALSE;
    }
    if (n == 0)
      break;
    g_byte_array_append(buf, chunk, (guint)n);
    if (buf->len > MAX_BODY + 16384) {
      g_set_error(err, G_IO_ERROR, G_IO_ERROR_MESSAGE_TOO_LARGE, "reply too large");
      g_byte_array_unref(buf);
      g_object_unref(c);
      return FALSE;
    }
  }
  g_object_unref(c);
  g_byte_array_append(buf, (const guint8 *)"", 1);
  char *text = (char *)buf->data;
  char *hdr_end = strstr(text, "\r\n\r\n");
  int status = 0;
  if (hdr_end == NULL || sscanf(text, "HTTP/1.%*d %d", &status) != 1) {
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_INVALID_DATA, "bad reply");
    g_byte_array_unref(buf);
    return FALSE;
  }
  *hdr_end = '\0';
  char *payload = hdr_end + 4;
  gsize plen = buf->len - 1 - (gsize)(payload - text);
  /* Chunked replies (Go uses them without Content-Length). */
  if (g_strrstr(text, "Transfer-Encoding: chunked") != NULL || g_strrstr(text, "transfer-encoding: chunked") != NULL) {
    GString *dec = g_string_new(NULL);
    char *p = payload, *end = payload + plen;
    while (p < end) {
      char *nl = strstr(p, "\r\n");
      if (nl == NULL)
        break;
      gsize size = strtoul(p, NULL, 16);
      p = nl + 2;
      if (size == 0 || p + size > end)
        break;
      g_string_append_len(dec, p, (gssize)size);
      p += size + 2;
    }
    res->len = dec->len;
    res->body = g_string_free(dec, FALSE);
  } else {
    res->body = g_strndup(payload, plen);
    res->len = plen;
  }
  res->status = status;
  g_byte_array_unref(buf);
  return TRUE;
}

static JsonObject *
parse_object(const HttpResult *r)
{
  if (r->body == NULL)
    return NULL;
  JsonParser *p = json_parser_new();
  JsonObject *o = NULL;
  if (json_parser_load_from_data(p, r->body, (gssize)r->len, NULL) && JSON_NODE_HOLDS_OBJECT(json_parser_get_root(p)))
    o = json_object_ref(json_node_get_object(json_parser_get_root(p)));
  g_object_unref(p);
  return o;
}

static const char *
str_member(JsonObject *o, const char *name)
{
  JsonNode *n = o ? json_object_get_member(o, name) : NULL;
  if (n == NULL || !JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_STRING)
    return NULL;
  return json_node_get_string(n);
}

/* POST /v1/shell/session: a new token and the account's details. */
static JsonObject *
new_session(guint timeout_s, GError **err)
{
  g_mutex_lock(&lock);
  g_clear_pointer(&token, g_free);
  g_mutex_unlock(&lock);
  HttpResult r;
  if (!http("POST", "/v1/shell/session", "{}", timeout_s, &r, err))
    return NULL;
  JsonObject *o = r.status == 200 ? parse_object(&r) : NULL;
  if (o == NULL && err != NULL && *err == NULL)
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_FAILED, "alfad said %d", r.status);
  http_result_clear(&r);
  const char *t = str_member(o, "token");
  if (o != NULL && (t == NULL || strlen(t) < 16 || strlen(t) > 256)) {
    json_object_unref(o);
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_FAILED, "no session token");
    return NULL;
  }
  if (o != NULL) {
    g_mutex_lock(&lock);
    token = g_strdup(t);
    g_mutex_unlock(&lock);
  }
  return o;
}

/* ---------- main-loop side ---------- */

static gboolean
valid_choice(const char *v, const char *a, const char *b)
{
  return g_strcmp0(v, a) == 0 || g_strcmp0(v, b) == 0;
}

static gboolean
apply_session(gpointer data)
{
  JsonObject *o = data;
  gboolean changed = FALSE;
  const char *voice = str_member(o, "voice"), *theme = str_member(o, "theme");
  const char *real = str_member(o, "realName"), *url = str_member(o, "webURL");
  if (valid_choice(voice, "genz", "standard") && g_strcmp0(voice, shell.voice) != 0) {
    g_free(shell.voice);
    shell.voice = g_strdup(voice);
    changed = TRUE;
  }
  if (valid_choice(theme, "dark", "light") && g_strcmp0(theme, shell.theme) != 0) {
    g_free(shell.theme);
    shell.theme = g_strdup(theme);
    changed = TRUE;
  }
  if (real != NULL && *real && strlen(real) < 128 && g_strcmp0(real, shell.real_name) != 0) {
    g_free(shell.real_name);
    shell.real_name = g_strdup(real);
  }
  if (url != NULL && (g_str_has_prefix(url, "https://127.0.0.1") || g_str_has_prefix(url, "http://127.0.0.1"))) {
    g_free(shell.web_url);
    shell.web_url = g_strdup(url);
  }
  JsonNode *admin = json_object_get_member(o, "admin");
  shell.admin = admin && JSON_NODE_HOLDS_VALUE(admin) && json_node_get_boolean(admin);
  if (changed)
    shell_settings_changed();
  json_object_unref(o);
  return G_SOURCE_REMOVE;
}

static gboolean
apply_items(gpointer data)
{
  JsonArray *items = data;
  notify_merge_remote(items);
  json_array_unref(items);
  return G_SOURCE_REMOVE;
}

/* ---------- the long-poll loop (its own thread) ---------- */

static gpointer
poll_thread(gpointer u)
{
  guint backoff = 5;
  gint64 cursor = 0;
  gboolean have_session = FALSE;
  for (;;) {
    GError *err = NULL;
    if (!have_session) {
      JsonObject *s = new_session(10, &err);
      if (s == NULL) {
        g_clear_error(&err);
        g_usleep((gulong)backoff * G_USEC_PER_SEC);
        backoff = MIN(backoff * 2, 60);
        continue;
      }
      g_idle_add(apply_session, s);
      have_session = TRUE;
      backoff = 5;
      cursor = 0;
    }
    char *path = g_strdup_printf("/v1/shell/notifications?after=%" G_GINT64_FORMAT "&wait=%d", cursor,
                                 cursor == 0 ? 0 : 25);
    HttpResult r;
    gboolean ok = http("GET", path, NULL, 40, &r, &err);
    g_free(path);
    if (!ok) {
      g_clear_error(&err);
      have_session = FALSE;
      g_usleep((gulong)backoff * G_USEC_PER_SEC);
      backoff = MIN(backoff * 2, 60);
      continue;
    }
    if (r.status == 401 || r.status == 403) {
      http_result_clear(&r);
      have_session = FALSE;
      g_usleep(2 * G_USEC_PER_SEC);
      continue;
    }
    JsonObject *o = r.status == 200 ? parse_object(&r) : NULL;
    http_result_clear(&r);
    if (o == NULL) {
      g_usleep((gulong)backoff * G_USEC_PER_SEC);
      backoff = MIN(backoff * 2, 60);
      continue;
    }
    backoff = 5;
    JsonNode *cn = json_object_get_member(o, "cursor");
    if (cn && JSON_NODE_HOLDS_VALUE(cn) && json_node_get_value_type(cn) == G_TYPE_INT64)
      cursor = MAX(json_node_get_int(cn), 1);
    else
      cursor = MAX(cursor, 1);
    JsonNode *items = json_object_get_member(o, "items");
    if (items && JSON_NODE_HOLDS_ARRAY(items))
      g_idle_add(apply_items, json_array_ref(json_node_get_array(items)));
    /* Voice/theme changes in Settings come with every reply. */
    if (json_object_has_member(o, "voice") || json_object_has_member(o, "theme"))
      g_idle_add(apply_session, json_object_ref(o));
    json_object_unref(o);
  }
  return NULL;
}

void
alfad_start(void)
{
  GThread *t = g_thread_new("alfad", poll_thread, NULL);
  g_thread_unref(t);
}

JsonObject *
alfad_session_sync(guint timeout_s)
{
  GError *err = NULL;
  JsonObject *o = new_session(timeout_s, &err);
  if (o == NULL) {
    g_message("alfad: %s", err ? err->message : "no answer");
    g_clear_error(&err);
  }
  return o;
}

static void
mark_thread(GTask *task, gpointer src, gpointer data, GCancellable *c)
{
  char *body = data;
  HttpResult r;
  GError *err = NULL;
  if (http("POST", "/v1/shell/notifications/read", body, 10, &r, &err))
    http_result_clear(&r);
  g_clear_error(&err);
  g_task_return_boolean(task, TRUE);
}

void
alfad_mark_read(JsonArray *ids, gboolean all)
{
  JsonObject *o = json_object_new();
  if (all)
    json_object_set_boolean_member(o, "all", TRUE);
  else if (ids != NULL)
    json_object_set_array_member(o, "ids", json_array_ref(ids));
  else {
    json_object_unref(o);
    return;
  }
  JsonNode *node = jobj_node(o);
  char *body = json_for_script(node);
  json_node_unref(node);
  GTask *t = g_task_new(NULL, NULL, NULL, NULL);
  g_task_set_task_data(t, body, g_free);
  g_task_run_in_thread(t, mark_thread);
  g_object_unref(t);
}
