/* The session's polkit authentication agent: an admin password request
 * shows the NoCap-styled `polkit` surface; the password the page sends
 * back goes straight into a PolkitAgentSession and is wiped. */
#define POLKIT_AGENT_I_KNOW_API_IS_SUBJECT_TO_CHANGE
#include "shell.h"

#include <polkitagent/polkitagent.h>
#include <pwd.h>
#include <string.h>
#include <unistd.h>

#define MAX_TRIES 3

typedef struct {
  PolkitAgentListener parent;
} NocapListener;
typedef struct {
  PolkitAgentListenerClass parent_class;
} NocapListenerClass;

GType nocap_listener_get_type(void);
G_DEFINE_TYPE(NocapListener, nocap_listener, POLKIT_AGENT_TYPE_LISTENER)

typedef struct {
  char *cookie;
  char *message;
  char *icon;
  PolkitIdentity *identity;
  char *user;
  GTask *task;
  GCancellable *cancellable;
  gulong cancel_id;
  PolkitAgentSession *session;
  char *password; /* only between respond and the session's request */
  int tries;
} Request;

static Request *current;
static GQueue waiting = G_QUEUE_INIT;

static void
request_free(Request *r)
{
  if (r->session) {
    g_signal_handlers_disconnect_by_data(r->session, r);
    polkit_agent_session_cancel(r->session);
    g_object_unref(r->session);
  }
  if (r->password) {
    secure_wipe(r->password, strlen(r->password));
    g_free(r->password);
  }
  if (r->cancellable && r->cancel_id)
    g_cancellable_disconnect(r->cancellable, r->cancel_id);
  g_clear_object(&r->cancellable);
  g_clear_object(&r->identity);
  g_clear_object(&r->task);
  g_free(r->cookie);
  g_free(r->message);
  g_free(r->icon);
  g_free(r->user);
  g_free(r);
}

static void
emit_done(Request *r, gboolean ok, gboolean retry)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "cookie", r->cookie);
  json_object_set_boolean_member(o, "ok", ok);
  json_object_set_boolean_member(o, "retry", retry);
  shell_emit(SM(SURF_POLKIT), "polkit.done", jobj_node(o));
}

static void show_next(void);

/* Ends the current request: ok, cancelled or failed. */
static void
finish(Request *r, gboolean ok, const char *why)
{
  if (r != current)
    return;
  current = NULL;
  emit_done(r, ok, FALSE);
  if (ok)
    g_task_return_boolean(r->task, TRUE);
  else
    g_task_return_new_error(r->task, POLKIT_ERROR, POLKIT_ERROR_CANCELLED, "%s", why ? why : "cancelled");
  request_free(r);
  surface_hide(SURF_POLKIT);
  show_next();
}

static void
emit_request(Request *r)
{
  JsonObject *o = json_object_new();
  json_object_set_string_member(o, "cookie", r->cookie);
  json_object_set_string_member(o, "message", r->message);
  json_object_set_string_member(o, "icon", r->icon);
  json_object_set_string_member(o, "user", r->user);
  shell_emit(SM(SURF_POLKIT), "polkit.request", jobj_node(o));
  surface_show(SURF_POLKIT);
}

static void
show_next(void)
{
  if (current != NULL || g_queue_is_empty(&waiting))
    return;
  current = g_queue_pop_head(&waiting);
  emit_request(current);
}

/* ---------- one authentication attempt ---------- */

static void
on_session_request(PolkitAgentSession *s, const char *prompt, gboolean echo_on, gpointer data)
{
  Request *r = data;
  if (r->password == NULL) {
    polkit_agent_session_cancel(s);
    return;
  }
  polkit_agent_session_response(s, r->password);
  secure_wipe(r->password, strlen(r->password));
  g_clear_pointer(&r->password, g_free);
}

static void
on_session_completed(PolkitAgentSession *s, gboolean gained, gpointer data)
{
  Request *r = data;
  g_signal_handlers_disconnect_by_data(s, r);
  g_clear_object(&r->session);
  if (gained) {
    finish(r, TRUE, NULL);
    return;
  }
  if (++r->tries >= MAX_TRIES) {
    finish(r, FALSE, "too many tries");
    return;
  }
  emit_done(r, FALSE, TRUE); /* wrong password: the page asks again */
}

static void
on_session_message(PolkitAgentSession *s, const char *text, gpointer data)
{
  if (shell.debug)
    g_message("polkit: %s", text);
}

gboolean
polkit_respond(const char *cookie, const char *password, GError **error)
{
  Request *r = current;
  if (r == NULL || g_strcmp0(r->cookie, cookie) != 0) {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_NOT_FOUND, "no such request");
    return FALSE;
  }
  if (r->session != NULL) {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_BUSY, "checking");
    return FALSE;
  }
  r->password = g_strdup(password);
  r->session = polkit_agent_session_new(r->identity, r->cookie);
  g_signal_connect(r->session, "request", G_CALLBACK(on_session_request), r);
  g_signal_connect(r->session, "completed", G_CALLBACK(on_session_completed), r);
  g_signal_connect(r->session, "show-error", G_CALLBACK(on_session_message), r);
  g_signal_connect(r->session, "show-info", G_CALLBACK(on_session_message), r);
  polkit_agent_session_initiate(r->session);
  return TRUE;
}

gboolean
polkit_cancel(const char *cookie, GError **error)
{
  Request *r = current;
  if (r == NULL || g_strcmp0(r->cookie, cookie) != 0) {
    g_set_error(error, G_IO_ERROR, G_IO_ERROR_NOT_FOUND, "no such request");
    return FALSE;
  }
  finish(r, FALSE, "cancelled");
  return TRUE;
}

/* ---------- the listener ---------- */

static gboolean
cancel_idle(gpointer data)
{
  char *cookie = data;
  if (current != NULL && g_strcmp0(current->cookie, cookie) == 0) {
    finish(current, FALSE, "cancelled");
  } else {
    for (GList *l = waiting.head; l != NULL; l = l->next) {
      Request *r = l->data;
      if (g_strcmp0(r->cookie, cookie) == 0) {
        g_queue_delete_link(&waiting, l);
        g_task_return_new_error(r->task, POLKIT_ERROR, POLKIT_ERROR_CANCELLED, "cancelled");
        request_free(r);
        break;
      }
    }
  }
  g_free(cookie);
  return G_SOURCE_REMOVE;
}

static void
on_cancelled(GCancellable *c, gpointer data)
{
  Request *r = data;
  g_idle_add(cancel_idle, g_strdup(r->cookie));
}

static char *
identity_user(PolkitIdentity *id)
{
  uid_t uid = (uid_t)polkit_unix_user_get_uid(POLKIT_UNIX_USER(id));
  struct passwd *pw = getpwuid(uid);
  if (pw == NULL)
    return g_strdup_printf("%u", (unsigned)uid);
  if (pw->pw_gecos && pw->pw_gecos[0]) {
    char **parts = g_strsplit(pw->pw_gecos, ",", 2);
    char *name = parts[0] && parts[0][0] ? utf8_truncate(parts[0], 128) : NULL;
    g_strfreev(parts);
    if (name)
      return name;
  }
  return g_strdup(pw->pw_name);
}

static void
initiate(PolkitAgentListener *listener, const char *action_id, const char *message, const char *icon_name,
         PolkitDetails *details, const char *cookie, GList *identities, GCancellable *cancellable,
         GAsyncReadyCallback callback, gpointer user_data)
{
  GTask *task = g_task_new(listener, cancellable, callback, user_data);
  PolkitIdentity *pick = NULL;
  for (GList *l = identities; l != NULL; l = l->next) {
    if (!POLKIT_IS_UNIX_USER(l->data))
      continue;
    if ((uid_t)polkit_unix_user_get_uid(POLKIT_UNIX_USER(l->data)) == getuid()) {
      pick = l->data;
      break;
    }
    if (pick == NULL)
      pick = l->data;
  }
  if (pick == NULL || cookie == NULL || strlen(cookie) > 512) {
    g_task_return_new_error(task, POLKIT_ERROR, POLKIT_ERROR_FAILED, "no usable identity");
    g_object_unref(task);
    return;
  }
  Request *r = g_new0(Request, 1);
  r->cookie = g_strdup(cookie);
  r->message = utf8_truncate(message ? message : "", 1024);
  r->icon = utf8_truncate(icon_name ? icon_name : "", 128);
  r->identity = g_object_ref(pick);
  r->user = identity_user(pick);
  r->task = task;
  if (cancellable != NULL) {
    r->cancellable = g_object_ref(cancellable);
    r->cancel_id = g_cancellable_connect(cancellable, G_CALLBACK(on_cancelled), r, NULL);
  }
  if (g_queue_get_length(&waiting) >= 8) {
    g_task_return_new_error(task, POLKIT_ERROR, POLKIT_ERROR_FAILED, "too many requests");
    r->task = NULL;
    g_object_unref(task);
    request_free(r);
    return;
  }
  g_queue_push_tail(&waiting, r);
  show_next();
}

static gboolean
initiate_finish(PolkitAgentListener *listener, GAsyncResult *res, GError **error)
{
  return g_task_propagate_boolean(G_TASK(res), error);
}

static void
nocap_listener_init(NocapListener *l)
{
}

static void
nocap_listener_class_init(NocapListenerClass *k)
{
  PolkitAgentListenerClass *lk = POLKIT_AGENT_LISTENER_CLASS(k);
  lk->initiate_authentication = initiate;
  lk->initiate_authentication_finish = initiate_finish;
}

void
polkit_agent_start(void)
{
  GError *err = NULL;
  PolkitSubject *subject = polkit_unix_session_new_for_process_sync(getpid(), NULL, &err);
  if (subject == NULL) {
    g_clear_error(&err);
    const char *sid = g_getenv("XDG_SESSION_ID");
    if (sid == NULL || *sid == '\0') {
      g_message("polkit: not in a login session; no password dialog");
      return;
    }
    subject = polkit_unix_session_new(sid);
  }
  PolkitAgentListener *l = g_object_new(nocap_listener_get_type(), NULL);
  gpointer handle = polkit_agent_listener_register(l, POLKIT_AGENT_REGISTER_FLAGS_NONE, subject,
                                                   "/io/nocapos/PolicyKit1/AuthenticationAgent", NULL, &err);
  if (handle == NULL) {
    g_message("polkit: agent not registered: %s", err ? err->message : "?");
    g_clear_error(&err);
    g_object_unref(l);
  }
  g_object_unref(subject);
}
