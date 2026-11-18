/* alfad client: HTTP/1.1 over the local socket /run/nocapos/shell.sock.
 * alfad knows which Linux account (and program) is calling from the socket
 * itself and answers POST /v1/shell/session with that person's NoCap OS web
 * session. The pages get the access token (session.get); the refresh token
 * stays here and is traded for a new session before the old one ends
 * (session.refresh). Runs in worker threads; results reach the main loop
 * through GTask callbacks. alfad being away never breaks the shell. */
#include "shell.h"

#include <gio/gunixsocketaddress.h>
#include <stdlib.h>
#include <string.h>

#define MAX_BODY (4 * 1024 * 1024)

static GMutex lock;
static char *refresh_token; /* guarded by lock; never given to a page */

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

  gsize blen = body ? strlen(body) : 0;
  char *req = g_strdup_printf("%s %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n"
                              "Content-Type: application/json\r\nContent-Length: %" G_GSIZE_FORMAT "\r\n\r\n",
                              method, path, blen);
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

/* POST /v1/shell/session: the person's web session (a fresh one, or the
 * next one for the refresh token we hold). Returns the page's part. */
static JsonObject *
fetch_session(gboolean refresh, guint timeout_s, GError **err)
{
  JsonObject *req = json_object_new();
  g_mutex_lock(&lock);
  if (refresh && refresh_token != NULL)
    json_object_set_string_member(req, "refresh", refresh_token);
  g_mutex_unlock(&lock);
  JsonNode *rn = jobj_node(req);
  char *body = json_for_script(rn);
  json_node_unref(rn);
  HttpResult r;
  gboolean ok = http("POST", "/v1/shell/session", body, timeout_s, &r, err);
  secure_wipe(body, strlen(body));
  g_free(body);
  if (!ok)
    return NULL;
  JsonObject *o = r.status == 200 ? parse_object(&r) : NULL;
  int status = r.status;
  secure_wipe(r.body, r.len);
  http_result_clear(&r);
  if (o == NULL) {
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_FAILED, "alfad said %d", status);
    /* The refresh token ended: the next call starts over. */
    if (status == 401) {
      g_mutex_lock(&lock);
      g_clear_pointer(&refresh_token, g_free);
      g_mutex_unlock(&lock);
    }
    return NULL;
  }
  const char *access = str_member(o, "access_token"), *rt = str_member(o, "refresh_token");
  const char *origin = str_member(o, "apiOrigin");
  if (access == NULL || strlen(access) < 16 || origin == NULL ||
      !(g_str_has_prefix(origin, "http://127.0.0.1:") || g_str_has_prefix(origin, "https://127.0.0.1:"))) {
    json_object_unref(o);
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_FAILED, "alfad's answer isn't a desktop session");
    return NULL;
  }
  g_mutex_lock(&lock);
  if (rt != NULL) {
    if (refresh_token != NULL)
      secure_wipe(refresh_token, strlen(refresh_token));
    g_free(refresh_token);
    refresh_token = g_strdup(rt);
  }
  g_mutex_unlock(&lock);
  /* The page never sees the refresh token. */
  if (rt != NULL) {
    secure_wipe((char *)rt, strlen(rt));
    json_object_remove_member(o, "refresh_token");
  }
  return o;
}

/* ---------- main-loop side ---------- */

static void
apply_session(JsonObject *o)
{
  const char *real = str_member(o, "realName"), *origin = str_member(o, "apiOrigin");
  if (real != NULL && *real && strlen(real) < 128 && g_strcmp0(real, shell.real_name) != 0) {
    g_free(shell.real_name);
    shell.real_name = g_strdup(real);
  }
  if (origin != NULL && g_strcmp0(origin, shell.api_origin) != 0) {
    g_free(shell.api_origin);
    shell.api_origin = g_strdup(origin);
  }
  JsonNode *admin = json_object_get_member(o, "admin");
  shell.admin = admin && JSON_NODE_HOLDS_VALUE(admin) && json_node_get_value_type(admin) == G_TYPE_BOOLEAN &&
                json_node_get_boolean(admin);
}

typedef struct {
  gboolean refresh;
  AlfadSessionCb cb;
  gpointer data;
} SessionJob;

static void
session_thread(GTask *task, gpointer src, gpointer data, GCancellable *c)
{
  SessionJob *j = data;
  GError *err = NULL;
  JsonObject *o = fetch_session(j->refresh, 10, &err);
  if (o == NULL && j->refresh) {
    g_clear_error(&err);
    o = fetch_session(FALSE, 10, &err);
  }
  if (o == NULL) {
    g_debug("alfad: %s", err ? err->message : "no answer");
    g_clear_error(&err);
    g_task_return_pointer(task, NULL, NULL);
    return;
  }
  g_task_return_pointer(task, o, (GDestroyNotify)json_object_unref);
}

static void
session_done(GObject *src, GAsyncResult *res, gpointer data)
{
  SessionJob *j = data;
  JsonObject *o = g_task_propagate_pointer(G_TASK(res), NULL);
  if (o != NULL)
    apply_session(o);
  if (j->cb)
    j->cb(o, j->data);
  if (o != NULL)
    json_object_unref(o);
  g_free(j);
}

void
alfad_session(gboolean refresh, AlfadSessionCb cb, gpointer data)
{
  SessionJob *j = g_new0(SessionJob, 1);
  j->refresh = refresh;
  j->cb = cb;
  j->data = data;
  GTask *t = g_task_new(NULL, NULL, session_done, j);
  g_task_set_task_data(t, j, NULL);
  g_task_run_in_thread(t, session_thread);
  g_object_unref(t);
}

void
alfad_start(void)
{
  alfad_session(FALSE, NULL, NULL);
}
