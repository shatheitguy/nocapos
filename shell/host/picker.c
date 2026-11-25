/* Picking files: an <input type=file> (or anything else that asks WebKit for
 * files) opens NoCap OS's own file picker in the page that asked, never the
 * toolkit's dialog.
 *
 * WebKit's run-file-chooser comes here; the page gets `files.choose`
 * `{request, multiple, accept}` and shows the picker (the same locations,
 * icons and look as Files). It answers with `files.pick` (the picked files)
 * or `files.cancel`. A picked file the signed-in account can read where it
 * is goes to the page as is; anything else (a file only NoCap OS itself may
 * read) is fetched from alfad first, into a private folder in the account's
 * cache, and handed over from there. The sign-in screen and the admin
 * password prompt never pick files. */
#include "shell.h"

#include <errno.h>
#include <glib/gstdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#define RAW_PREFIX "/api/v1/files/raw?"
#define STAGED_MAX_AGE (24 * 60 * 60) /* seconds */

typedef struct {
  guint id;
  SurfaceId surface;
  gboolean multiple;
  WebKitFileChooserRequest *req;
  /* a pick in progress: files still to fetch from alfad */
  ShellReply *reply;
  GPtrArray *files;  /* char*: the paths handed to the page, in order */
  GPtrArray *urls;   /* char*: alfad links, NULL for files used in place */
  guint next;       /* the next file to look at */
  char *dir;         /* where fetched files go */
  SoupMessage *msg;
  GCancellable *cancel;
} Pick;

static Pick *pending[SURF_COUNT];
static guint last_id;
static SoupSession *session;

static void fetch_next(Pick *p);

static void
pick_free(Pick *p)
{
  if (p == NULL)
    return;
  if (p->cancel != NULL) {
    g_cancellable_cancel(p->cancel);
    g_object_unref(p->cancel);
  }
  g_clear_object(&p->msg);
  if (p->reply != NULL)
    shell_reply_fail(p->reply, "the file request is over");
  g_clear_object(&p->req);
  if (p->files != NULL)
    g_ptr_array_unref(p->files);
  if (p->urls != NULL)
    g_ptr_array_unref(p->urls);
  g_free(p->dir);
  g_free(p);
}

/* The filter WebKit made from the input's accept attribute: MIME types
 * (image/png, image/ followed by a star) and file name patterns (.pub). */
static JsonArray *
accept_of(WebKitFileChooserRequest *req)
{
  JsonArray *a = json_array_new();
  GHashTable *seen = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
  const char *const *mimes = webkit_file_chooser_request_get_mime_types(req);
  for (int i = 0; mimes != NULL && mimes[i] != NULL; i++)
    if (g_hash_table_add(seen, g_ascii_strdown(mimes[i], -1)))
      json_array_add_string_element(a, mimes[i]);
  GtkFileFilter *f = webkit_file_chooser_request_get_mime_types_filter(req);
  if (f != NULL) {
    GVariant *v = gtk_file_filter_to_gvariant(f);
    GVariant *rules = g_variant_get_child_value(v, 1);
    GVariantIter it;
    guint32 kind;
    const char *value;
    g_variant_iter_init(&it, rules);
    while (g_variant_iter_next(&it, "(u&s)", &kind, &value))
      if (value != NULL && *value && strlen(value) <= 128 && g_hash_table_add(seen, g_ascii_strdown(value, -1)))
        json_array_add_string_element(a, value);
    g_variant_unref(rules);
    g_variant_unref(v);
  }
  g_hash_table_unref(seen);
  return a;
}

void
picker_drop(SurfaceId surface)
{
  Pick *p = pending[surface];
  if (p == NULL)
    return;
  pending[surface] = NULL;
  if (p->req != NULL)
    webkit_file_chooser_request_cancel(p->req);
  pick_free(p);
}

gboolean
picker_run(SurfaceId surface, WebKitFileChooserRequest *req)
{
  if (surface == SURF_GREETER || surface == SURF_POLKIT) {
    webkit_file_chooser_request_cancel(req);
    return TRUE;
  }
  picker_drop(surface);
  Pick *p = g_new0(Pick, 1);
  p->id = ++last_id;
  if (p->id == 0)
    p->id = last_id = 1;
  p->surface = surface;
  p->multiple = webkit_file_chooser_request_get_select_multiple(req);
  p->req = g_object_ref(req);
  pending[surface] = p;
  JsonObject *o = json_object_new();
  json_object_set_int_member(o, "request", p->id);
  json_object_set_boolean_member(o, "multiple", p->multiple);
  json_object_set_array_member(o, "accept", accept_of(req));
  if (shell.debug)
    g_message("surface %s: file request %u (multiple: %d)", surface_name(surface), p->id, p->multiple);
  shell_emit(SM(surface), "files.choose", jobj_node(o));
  return TRUE; /* never the toolkit's dialog */
}

static Pick *
pick_for(SurfaceId surface, gint64 request)
{
  Pick *p = pending[surface];
  return p != NULL && (gint64)p->id == request ? p : NULL;
}

void
picker_cancel(SurfaceId surface, gint64 request, ShellReply *r)
{
  if (pick_for(surface, request) != NULL)
    picker_drop(surface);
  shell_reply_ok(r, NULL);
}

/* A file the signed-in account can read where it is. */
static gboolean
usable_here(const char *path)
{
  if (path == NULL || !g_path_is_absolute(path) || strstr(path, "/../") != NULL || g_str_has_suffix(path, "/.."))
    return FALSE;
  struct stat st;
  return stat(path, &st) == 0 && S_ISREG(st.st_mode) && access(path, R_OK) == 0;
}

/* A plain file name for a fetched file (never a path). */
static char *
safe_name(const char *name)
{
  char *base = g_path_get_basename(name != NULL && *name ? name : "file");
  if (strcmp(base, ".") == 0 || strcmp(base, "..") == 0 || strcmp(base, "/") == 0 || *base == 0) {
    g_free(base);
    base = g_strdup("file");
  }
  return base;
}

/* Removes a fetched pick: its folders (never following links) and files. */
static void
remove_tree(const char *dir, int depth)
{
  GDir *d = g_dir_open(dir, 0, NULL);
  if (d != NULL) {
    const char *n;
    while ((n = g_dir_read_name(d)) != NULL) {
      char *p = g_build_filename(dir, n, NULL);
      struct stat st;
      if (lstat(p, &st) == 0 && S_ISDIR(st.st_mode)) {
        if (depth > 1)
          remove_tree(p, depth - 1);
      } else {
        g_unlink(p);
      }
      g_free(p);
    }
    g_dir_close(d);
  }
  g_rmdir(dir);
}

/* Fetched files from earlier picks go after a day (an upload reads them
 * long before that). */
static void
clean_staged(const char *top)
{
  GDir *d = g_dir_open(top, 0, NULL);
  if (d == NULL)
    return;
  const char *n;
  gint64 now = g_get_real_time() / G_USEC_PER_SEC;
  while ((n = g_dir_read_name(d)) != NULL) {
    if (!g_str_has_prefix(n, "pick-"))
      continue;
    char *dir = g_build_filename(top, n, NULL);
    struct stat st;
    if (lstat(dir, &st) == 0 && S_ISDIR(st.st_mode) && now - st.st_mtime > STAGED_MAX_AGE)
      remove_tree(dir, 2);
    g_free(dir);
  }
  g_dir_close(d);
}

static char *
staging_dir(GError **error)
{
  char *top = g_build_filename(g_get_user_cache_dir(), "nocapos", "picked", NULL);
  if (g_mkdir_with_parents(top, 0700) != 0) {
    g_set_error(error, G_IO_ERROR, g_io_error_from_errno(errno), "couldn't make a folder for the files");
    g_free(top);
    return NULL;
  }
  clean_staged(top);
  char *tmpl = g_build_filename(top, "pick-XXXXXX", NULL);
  g_free(top);
  if (g_mkdtemp_full(tmpl, 0700) == NULL) {
    g_set_error(error, G_IO_ERROR, g_io_error_from_errno(errno), "couldn't make a folder for the files");
    g_free(tmpl);
    return NULL;
  }
  return tmpl;
}

/* Hands the picked files to the page and ends the request. */
static void
finish_pick(Pick *p)
{
  g_ptr_array_add(p->files, NULL);
  if (shell.debug)
    g_message("surface %s: file request %u: %u file(s)", surface_name(p->surface), p->id, p->files->len - 1);
  webkit_file_chooser_request_select_files(p->req, (const char *const *)p->files->pdata);
  ShellReply *r = p->reply;
  p->reply = NULL;
  pending[p->surface] = NULL;
  JsonObject *o = json_object_new();
  json_object_set_int_member(o, "count", p->files->len - 1);
  shell_reply_ok(r, jobj_node(o));
  pick_free(p);
}

static void
fail_pick(Pick *p, const char *message)
{
  ShellReply *r = p->reply;
  p->reply = NULL;
  /* The request stays open: the page can pick again or cancel. */
  if (p->cancel != NULL) {
    g_object_unref(p->cancel);
    p->cancel = NULL;
  }
  g_clear_object(&p->msg);
  g_ptr_array_set_size(p->files, 0);
  g_ptr_array_set_size(p->urls, 0);
  shell_reply_fail(r, message);
}

static void
on_spliced(GObject *src, GAsyncResult *res, gpointer data)
{
  Pick *p = data;
  GError *err = NULL;
  gssize n = g_output_stream_splice_finish(G_OUTPUT_STREAM(src), res, &err);
  if (g_error_matches(err, G_IO_ERROR, G_IO_ERROR_CANCELLED)) {
    g_error_free(err);
    return; /* the request ended; p is gone */
  }
  if (n < 0) {
    g_warning("file request: couldn't fetch a file: %s", err ? err->message : "?");
    g_clear_error(&err);
    fail_pick(p, "couldn't get the file");
    return;
  }
  p->next++;
  fetch_next(p);
}

static void
on_fetched(GObject *src, GAsyncResult *res, gpointer data)
{
  Pick *p = data;
  GError *err = NULL;
  GInputStream *in = soup_session_send_finish(SOUP_SESSION(src), res, &err);
  if (g_error_matches(err, G_IO_ERROR, G_IO_ERROR_CANCELLED)) {
    g_error_free(err);
    if (in != NULL)
      g_object_unref(in);
    return;
  }
  if (in == NULL || soup_message_get_status(p->msg) != SOUP_STATUS_OK) {
    g_warning("file request: alfad couldn't send a file (%u): %s", soup_message_get_status(p->msg),
              err ? err->message : "");
    g_clear_error(&err);
    if (in != NULL)
      g_object_unref(in);
    fail_pick(p, "couldn't get the file");
    return;
  }
  const char *dest = g_ptr_array_index(p->files, p->next);
  GFile *f = g_file_new_for_path(dest);
  GFileOutputStream *out = g_file_replace(f, NULL, FALSE, G_FILE_CREATE_PRIVATE, p->cancel, &err);
  g_object_unref(f);
  if (out == NULL) {
    g_warning("file request: %s", err->message);
    g_clear_error(&err);
    g_object_unref(in);
    fail_pick(p, "couldn't get the file");
    return;
  }
  g_output_stream_splice_async(G_OUTPUT_STREAM(out), in,
                               G_OUTPUT_STREAM_SPLICE_CLOSE_SOURCE | G_OUTPUT_STREAM_SPLICE_CLOSE_TARGET,
                               G_PRIORITY_DEFAULT, p->cancel, on_spliced, p);
  g_object_unref(out);
  g_object_unref(in);
}

static void
fetch_next(Pick *p)
{
  while (p->next < p->files->len && g_ptr_array_index(p->urls, p->next) == NULL)
    p->next++;
  if (p->next >= p->files->len) {
    finish_pick(p);
    return;
  }
  if (session == NULL) {
    session = soup_session_new();
    soup_session_set_timeout(session, 0);
    soup_session_set_idle_timeout(session, 60);
    soup_session_set_proxy_resolver(session, NULL); /* straight to loopback */
  }
  const char *api = shell.api_origin && *shell.api_origin ? shell.api_origin : "http://127.0.0.1:8780";
  char *target = g_strconcat(api, (const char *)g_ptr_array_index(p->urls, p->next), NULL);
  g_clear_object(&p->msg);
  p->msg = soup_message_new("GET", target);
  g_free(target);
  if (p->msg == NULL) {
    fail_pick(p, "couldn't get the file");
    return;
  }
  if (p->cancel == NULL)
    p->cancel = g_cancellable_new();
  soup_session_send_async(session, p->msg, G_PRIORITY_DEFAULT, p->cancel, on_fetched, p);
}

static const char *
str_at(JsonArray *a, guint i)
{
  if (a == NULL || i >= json_array_get_length(a))
    return "";
  const char *s = json_array_get_string_element(a, i);
  return s ? s : "";
}

void
picker_pick(SurfaceId surface, gint64 request, JsonArray *paths, JsonArray *urls, JsonArray *names, ShellReply *r)
{
  Pick *p = pick_for(surface, request);
  if (p == NULL) {
    shell_reply_fail(r, "the file request is over");
    return;
  }
  if (p->reply != NULL) {
    shell_reply_fail(r, "still getting the files");
    return;
  }
  guint n = paths ? json_array_get_length(paths) : 0;
  if (n == 0 || (urls && json_array_get_length(urls) != n) || (names && json_array_get_length(names) != n)) {
    shell_reply_fail(r, "no files");
    return;
  }
  if (!p->multiple && n > 1) {
    shell_reply_fail(r, "pick one file");
    return;
  }
  if (p->files == NULL) {
    p->files = g_ptr_array_new_with_free_func(g_free);
    p->urls = g_ptr_array_new_with_free_func(g_free);
  }
  g_ptr_array_set_size(p->files, 0);
  g_ptr_array_set_size(p->urls, 0);
  p->next = 0;
  for (guint i = 0; i < n; i++) {
    const char *path = str_at(paths, i);
    const char *url = str_at(urls, i);
    if (usable_here(path)) {
      g_ptr_array_add(p->files, g_strdup(path));
      g_ptr_array_add(p->urls, NULL);
      continue;
    }
    if (!g_str_has_prefix(url, RAW_PREFIX)) {
      shell_reply_fail(r, "can't open that file");
      return;
    }
    if (p->dir == NULL) {
      GError *err = NULL;
      p->dir = staging_dir(&err);
      if (p->dir == NULL) {
        shell_reply_fail(r, err->message);
        g_error_free(err);
        return;
      }
    }
    /* Each file in a folder of its own: same-named files from different
     * folders keep their names. */
    char *sub = g_strdup_printf("%s/%u", p->dir, i);
    if (g_mkdir(sub, 0700) != 0 && errno != EEXIST) {
      g_free(sub);
      shell_reply_fail(r, "couldn't make a folder for the files");
      return;
    }
    char *name = safe_name(str_at(names, i));
    g_ptr_array_add(p->files, g_build_filename(sub, name, NULL));
    g_ptr_array_add(p->urls, g_strdup(url));
    g_free(sub);
    g_free(name);
  }
  p->reply = r;
  fetch_next(p);
}
