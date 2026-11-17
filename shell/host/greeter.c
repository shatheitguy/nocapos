/* The sign-in screen's side of greetd: its IPC (native-endian u32 length +
 * JSON over $GREETD_SOCK) and the account list. The greeter account can do
 * nothing else: no apps, no windows, no settings. */
#include "shell.h"

#include <gio/gunixsocketaddress.h>
#include <pwd.h>
#include <stdio.h>
#include <string.h>

#define MAX_MSG (64 * 1024)
#define SESSION_CMD "/usr/libexec/nocapos/nocap-session"

static GSocketConnection *conn; /* one greetd conversation */
static gboolean busy;

/* ---------- accounts ---------- */

static guint
uid_min(void)
{
  guint min = 1000;
  FILE *f = fopen("/etc/login.defs", "re");
  if (f == NULL)
    return min;
  char line[256];
  while (fgets(line, sizeof line, f) != NULL) {
    unsigned v;
    if (sscanf(line, " UID_MIN %u", &v) == 1 && v > 0 && v < 60000)
      min = v;
  }
  fclose(f);
  return min;
}

JsonNode *
greeter_users_json(void)
{
  JsonArray *arr = json_array_new();
  guint min = uid_min();
  setpwent();
  struct passwd *pw;
  int count = 0;
  while ((pw = getpwent()) != NULL && count < 200) {
    if (pw->pw_uid < min || pw->pw_uid >= 60000 || pw->pw_name == NULL)
      continue;
    const char *sh = pw->pw_shell ? pw->pw_shell : "";
    if (g_str_has_suffix(sh, "nologin") || g_str_has_suffix(sh, "false") || *sh == '\0')
      continue;
    if (strlen(pw->pw_name) > 32)
      continue;
    char *real = NULL;
    if (pw->pw_gecos != NULL && pw->pw_gecos[0] != '\0') {
      char **parts = g_strsplit(pw->pw_gecos, ",", 2);
      if (parts[0] != NULL && parts[0][0] != '\0')
        real = utf8_truncate(parts[0], 128);
      g_strfreev(parts);
    }
    JsonObject *o = json_object_new();
    json_object_set_string_member(o, "name", pw->pw_name);
    json_object_set_string_member(o, "realName", real != NULL ? real : pw->pw_name);
    g_free(real);
    json_array_add_object_element(arr, o);
    count++;
  }
  endpwent();
  return jarr_node(arr);
}

/* ---------- IPC (runs in a worker thread) ---------- */

static gboolean
write_all(GOutputStream *out, const void *buf, gsize len, GError **err)
{
  gsize done = 0;
  return g_output_stream_write_all(out, buf, len, &done, NULL, err);
}

static gboolean
read_all(GInputStream *in, void *buf, gsize len, GError **err)
{
  gsize done = 0;
  if (!g_input_stream_read_all(in, buf, len, &done, NULL, err))
    return FALSE;
  if (done != len) {
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_CLOSED, "greetd closed the connection");
    return FALSE;
  }
  return TRUE;
}

/* Sends one request, returns greetd's reply object (new ref) or NULL. */
static JsonObject *
exchange(JsonObject *req, GError **err)
{
  if (conn == NULL) {
    const char *path = g_getenv("GREETD_SOCK");
    if (path == NULL || *path == '\0') {
      g_set_error(err, G_IO_ERROR, G_IO_ERROR_NOT_FOUND, "the sign-in service isn't running");
      return NULL;
    }
    GSocketClient *client = g_socket_client_new();
    GSocketAddress *addr = g_unix_socket_address_new(path);
    conn = g_socket_client_connect(client, G_SOCKET_CONNECTABLE(addr), NULL, err);
    g_object_unref(addr);
    g_object_unref(client);
    if (conn == NULL)
      return NULL;
  }
  JsonNode *node = jobj_node(json_object_ref(req));
  JsonGenerator *gen = json_generator_new();
  json_generator_set_root(gen, node);
  gsize len = 0;
  char *text = json_generator_to_data(gen, &len);
  g_object_unref(gen);
  json_node_unref(node);

  guint32 n = (guint32)len;
  GOutputStream *out = g_io_stream_get_output_stream(G_IO_STREAM(conn));
  gboolean ok = write_all(out, &n, sizeof n, err) && write_all(out, text, len, err);
  secure_wipe(text, len);
  g_free(text);
  if (!ok)
    goto broken;

  GInputStream *in = g_io_stream_get_input_stream(G_IO_STREAM(conn));
  guint32 rlen = 0;
  if (!read_all(in, &rlen, sizeof rlen, err))
    goto broken;
  if (rlen == 0 || rlen > MAX_MSG) {
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_INVALID_DATA, "bad reply from the sign-in service");
    goto broken;
  }
  char *buf = g_malloc0(rlen + 1);
  if (!read_all(in, buf, rlen, err)) {
    g_free(buf);
    goto broken;
  }
  JsonParser *p = json_parser_new();
  JsonObject *reply = NULL;
  if (json_parser_load_from_data(p, buf, rlen, NULL) && JSON_NODE_HOLDS_OBJECT(json_parser_get_root(p)))
    reply = json_object_ref(json_node_get_object(json_parser_get_root(p)));
  g_object_unref(p);
  g_free(buf);
  if (reply == NULL)
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_INVALID_DATA, "bad reply from the sign-in service");
  return reply;

broken:
  g_clear_object(&conn);
  return NULL;
}

static const char *
str_member(JsonObject *o, const char *name)
{
  JsonNode *n = o ? json_object_get_member(o, name) : NULL;
  if (n == NULL || !JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_STRING)
    return NULL;
  return json_node_get_string(n);
}

typedef enum { OP_LOGIN, OP_RESPOND, OP_CANCEL } Op;

typedef struct {
  Op op;
  char *username;
  char *response;
  gboolean has_response;
  ShellReply *reply;
  /* result */
  char *state;
  char *message;
} Job;

static void
job_free(Job *j)
{
  g_free(j->username);
  if (j->response)
    secure_wipe(j->response, strlen(j->response));
  g_free(j->response);
  g_free(j->state);
  g_free(j->message);
  g_free(j);
}

static void
cancel_session_quietly(void)
{
  if (conn == NULL)
    return;
  JsonObject *req = json_object_new();
  json_object_set_string_member(req, "type", "cancel_session");
  JsonObject *r = exchange(req, NULL);
  if (r)
    json_object_unref(r);
  json_object_unref(req);
}

static gboolean
start_session(GError **err)
{
  JsonObject *req = json_object_new();
  json_object_set_string_member(req, "type", "start_session");
  JsonArray *cmd = json_array_new();
  json_array_add_string_element(cmd, SESSION_CMD);
  json_object_set_array_member(req, "cmd", cmd);
  JsonArray *env = json_array_new();
  json_array_add_string_element(env, "XDG_SESSION_TYPE=wayland");
  json_array_add_string_element(env, "XDG_CURRENT_DESKTOP=NoCap:wlroots");
  json_array_add_string_element(env, "XDG_SESSION_DESKTOP=nocap");
  json_object_set_array_member(req, "env", env);
  JsonObject *r = exchange(req, err);
  json_object_unref(req);
  if (r == NULL)
    return FALSE;
  gboolean ok = g_strcmp0(str_member(r, "type"), "success") == 0;
  if (!ok) {
    const char *d = str_member(r, "description");
    g_set_error(err, G_IO_ERROR, G_IO_ERROR_FAILED, "%s", d ? d : "the desktop couldn't start");
  }
  json_object_unref(r);
  return ok;
}

/* Turns greetd's reply into the page's state. */
static void
interpret(Job *j, JsonObject *r)
{
  const char *type = str_member(r, "type");
  if (g_strcmp0(type, "auth_message") == 0) {
    const char *t = str_member(r, "auth_message_type");
    const char *msg = str_member(r, "auth_message");
    if (g_strcmp0(t, "secret") == 0 || g_strcmp0(t, "visible") == 0 || g_strcmp0(t, "info") == 0 ||
        g_strcmp0(t, "error") == 0)
      j->state = g_strdup(t);
    else
      j->state = g_strdup("info");
    j->message = utf8_truncate(msg ? msg : "", 512);
  } else if (g_strcmp0(type, "success") == 0) {
    GError *err = NULL;
    if (start_session(&err)) {
      j->state = g_strdup("done");
      j->message = g_strdup("");
    } else {
      cancel_session_quietly();
      j->state = g_strdup("failed");
      j->message = utf8_truncate(err ? err->message : "", 512);
    }
    g_clear_error(&err);
  } else {
    const char *etype = str_member(r, "error_type");
    const char *d = str_member(r, "description");
    cancel_session_quietly();
    j->state = g_strdup("failed");
    j->message = g_strdup(g_strcmp0(etype, "auth_error") == 0 ? "" : (d ? d : ""));
  }
}

static void
job_thread(GTask *task, gpointer src, gpointer data, GCancellable *c)
{
  Job *j = data;
  GError *err = NULL;
  JsonObject *req = json_object_new();
  if (j->op == OP_LOGIN) {
    /* A new attempt always starts a fresh conversation. */
    cancel_session_quietly();
    json_object_set_string_member(req, "type", "create_session");
    json_object_set_string_member(req, "username", j->username);
  } else if (j->op == OP_RESPOND) {
    json_object_set_string_member(req, "type", "post_auth_message_response");
    if (j->has_response)
      json_object_set_string_member(req, "response", j->response);
    else
      json_object_set_null_member(req, "response");
  } else {
    json_object_set_string_member(req, "type", "cancel_session");
  }
  JsonObject *r = exchange(req, &err);
  /* Wipe the password inside the request object too. */
  if (j->op == OP_RESPOND && j->has_response && json_object_has_member(req, "response")) {
    const char *s = str_member(req, "response");
    if (s)
      secure_wipe((char *)s, strlen(s));
  }
  json_object_unref(req);
  if (r == NULL) {
    j->state = g_strdup("failed");
    j->message = utf8_truncate(err ? err->message : "the sign-in service didn't answer", 512);
  } else if (j->op == OP_CANCEL) {
    j->state = g_strdup("failed");
    j->message = g_strdup("");
    json_object_unref(r);
  } else {
    interpret(j, r);
    json_object_unref(r);
  }
  g_clear_error(&err);
  g_task_return_boolean(task, TRUE);
}

static gboolean
quit_now(gpointer u)
{
  g_application_quit(G_APPLICATION(shell.app));
  return G_SOURCE_REMOVE;
}

static void
job_done(GObject *src, GAsyncResult *res, gpointer data)
{
  Job *j = data;
  busy = FALSE;
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "state", j->state ? j->state : "failed");
  json_object_set_string_member(o, "message", j->message ? j->message : "");
  shell_reply_ok(j->reply, jobj_node(o));
  j->reply = NULL;
  if (g_strcmp0(j->state, "done") == 0) {
    /* greetd starts the session once the greeter leaves; give the page a
     * moment for its "signing you in" animation. */
    g_clear_object(&conn);
    g_timeout_add(700, quit_now, NULL);
  }
  job_free(j);
}

static void
run_job(Job *j)
{
  if (busy) {
    shell_reply_fail(j->reply, "busy");
    j->reply = NULL;
    job_free(j);
    return;
  }
  busy = TRUE;
  GTask *t = g_task_new(NULL, NULL, job_done, j);
  g_task_set_task_data(t, j, NULL);
  g_task_run_in_thread(t, job_thread);
  g_object_unref(t);
}

/* Only the people's own accounts the sign-in screen lists can sign in:
 * never root or a system account. */
static gboolean
account_allowed(const char *username)
{
  struct passwd *pw = getpwnam(username);
  if (pw == NULL || pw->pw_uid == 0 || pw->pw_uid < uid_min() || pw->pw_uid >= 60000)
    return FALSE;
  const char *sh = pw->pw_shell ? pw->pw_shell : "";
  return sh[0] != 0 && !g_str_has_suffix(sh, "nologin") && !g_str_has_suffix(sh, "false");
}

void
greeter_login(const char *username, ShellReply *r)
{
  if (!account_allowed(username)) {
    JsonObject *o = json_object_new();
    json_object_set_string_member(o, "state", "failed");
    json_object_set_string_member(o, "message", "");
    shell_reply_ok(r, jobj_node(o));
    return;
  }
  Job *j = g_new0(Job, 1);
  j->op = OP_LOGIN;
  j->username = g_strdup(username);
  j->reply = r;
  run_job(j);
}

void
greeter_respond(const char *response, gboolean has_response, ShellReply *r)
{
  Job *j = g_new0(Job, 1);
  j->op = OP_RESPOND;
  j->has_response = has_response && response != NULL;
  j->response = g_strdup(j->has_response ? response : NULL);
  j->reply = r;
  run_job(j);
}

void
greeter_cancel(ShellReply *r)
{
  Job *j = g_new0(Job, 1);
  j->op = OP_CANCEL;
  j->reply = r;
  run_job(j);
}

static void
power_reply(gboolean ok, const char *error, gpointer data)
{
  if (ok)
    shell_reply_ok(data, NULL);
  else
    shell_reply_fail(data, error);
}

void
greeter_power(const char *action, ShellReply *r)
{
  if (g_strcmp0(action, "reboot") != 0 && g_strcmp0(action, "poweroff") != 0) {
    shell_reply_fail(r, "not allowed");
    return;
  }
  power_action(action, power_reply, r);
}
