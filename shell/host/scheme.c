/* nocap-shell:// scheme: shell bundle files and icons, nothing else. */
#include "shell.h"

#include <errno.h>
#include <limits.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

/* Pages may run only their own scripts and reach only alfad on the loopback
 * address (its API and live connections; NoCap apps' own local web pages in
 * frames). Nothing else. */
static char *
csp(void)
{
  const char *api = shell.api_origin && *shell.api_origin ? shell.api_origin : "http://127.0.0.1:8780";
  char *ws = g_str_has_prefix(api, "https://") ? g_strdup_printf("wss://%s", api + 8)
                                               : g_strdup_printf("ws://%s", api + (g_str_has_prefix(api, "http://") ? 7 : 0));
  char *p = g_strdup_printf("default-src 'self' nocap-shell:; script-src 'self' nocap-shell:; "
                            "img-src 'self' nocap-shell: data: blob: %s; media-src 'self' nocap-shell: blob: %s; "
                            "style-src 'self' nocap-shell: 'unsafe-inline'; font-src 'self' nocap-shell: data:; "
                            "connect-src 'self' nocap-shell: %s %s; worker-src 'self' blob:; "
                            "frame-src http://127.0.0.1:* https://127.0.0.1:*; object-src 'none'; base-uri 'none'; "
                            "form-action 'none'",
                            api, api, api, ws);
  g_free(ws);
  return p;
}

#define MAX_FILE (32 * 1024 * 1024)
#define ICON_CACHE_MAX 512

static char *bundle_real;
static GHashTable *icon_cache; /* "size\nname" -> GBytes (png) */

static const struct {
  const char *ext;
  const char *mime;
} mime_types[] = {
  {"html", "text/html"},       {"htm", "text/html"},          {"js", "text/javascript"},
  {"mjs", "text/javascript"},  {"css", "text/css"},           {"json", "application/json"},
  {"map", "application/json"}, {"svg", "image/svg+xml"},      {"png", "image/png"},
  {"jpg", "image/jpeg"},       {"jpeg", "image/jpeg"},        {"gif", "image/gif"},
  {"webp", "image/webp"},      {"avif", "image/avif"},        {"ico", "image/x-icon"},
  {"woff", "font/woff"},       {"woff2", "font/woff2"},       {"ttf", "font/ttf"},
  {"otf", "font/otf"},         {"wasm", "application/wasm"},  {"txt", "text/plain"},
  {"mp3", "audio/mpeg"},       {"ogg", "audio/ogg"},          {"oga", "audio/ogg"},
  {"wav", "audio/wav"},        {"webm", "video/webm"},        {"mp4", "video/mp4"},
};

static const char *
mime_for(const char *path)
{
  const char *dot = strrchr(path, '.');
  if (dot != NULL && strchr(dot, '/') == NULL)
    for (gsize i = 0; i < G_N_ELEMENTS(mime_types); i++)
      if (g_ascii_strcasecmp(dot + 1, mime_types[i].ext) == 0)
        return mime_types[i].mime;
  return "application/octet-stream";
}

static void
fail(WebKitURISchemeRequest *req, int code, const char *msg)
{
  GError *err = g_error_new_literal(G_IO_ERROR, code, msg);
  webkit_uri_scheme_request_finish_error(req, err);
  g_error_free(err);
}

static void
finish(WebKitURISchemeRequest *req, GBytes *bytes, const char *mime)
{
  gsize len = g_bytes_get_size(bytes);
  GInputStream *stream = g_memory_input_stream_new_from_bytes(bytes);
#if WEBKIT_CHECK_VERSION(2, 36, 0)
  WebKitURISchemeResponse *resp = webkit_uri_scheme_response_new(stream, (gint64)len);
  webkit_uri_scheme_response_set_content_type(resp, mime);
  webkit_uri_scheme_response_set_status(resp, 200, NULL);
  SoupMessageHeaders *h = soup_message_headers_new(SOUP_MESSAGE_HEADERS_RESPONSE);
  char *policy = csp();
  soup_message_headers_append(h, "Content-Security-Policy", policy);
  g_free(policy);
  soup_message_headers_append(h, "X-Content-Type-Options", "nosniff");
  soup_message_headers_append(h, "Referrer-Policy", "no-referrer");
  webkit_uri_scheme_response_set_http_headers(resp, h);
  webkit_uri_scheme_request_finish_with_response(req, resp);
  g_object_unref(resp);
#else
#warning "WebKitGTK < 2.36: the CSP response header cannot be set"
  webkit_uri_scheme_request_finish(req, stream, (gint64)len, mime);
#endif
  g_object_unref(stream);
}

/* Joins URL path segments after refusing ".", ".." and backslashes. */
static char *
safe_relative(const char *decoded)
{
  GString *out = g_string_new(NULL);
  char **parts = g_strsplit(decoded, "/", -1);
  gboolean ok = TRUE;
  for (int i = 0; parts[i] != NULL; i++) {
    const char *p = parts[i];
    if (*p == '\0')
      continue;
    if (strcmp(p, ".") == 0 || strcmp(p, "..") == 0 || strchr(p, '\\') != NULL) {
      ok = FALSE;
      break;
    }
    if (out->len)
      g_string_append_c(out, '/');
    g_string_append(out, p);
  }
  g_strfreev(parts);
  if (!ok) {
    g_string_free(out, TRUE);
    return NULL;
  }
  if (out->len == 0)
    g_string_append(out, "shell.html");
  return g_string_free(out, FALSE);
}

static gboolean
path_inside(const char *real, const char *dir_real)
{
  gsize n = strlen(dir_real);
  return strncmp(real, dir_real, n) == 0 && (real[n] == '/' || (n == 1 && dir_real[0] == '/'));
}

static void
serve_file(WebKitURISchemeRequest *req, const char *encoded_path)
{
  if (bundle_real == NULL) {
    fail(req, G_IO_ERROR_NOT_FOUND, "shell bundle missing");
    return;
  }
  char *decoded = g_uri_unescape_string(encoded_path, "/");
  if (decoded == NULL) {
    fail(req, G_IO_ERROR_INVALID_ARGUMENT, "bad path");
    return;
  }
  char *rel = safe_relative(decoded);
  g_free(decoded);
  if (rel == NULL) {
    fail(req, G_IO_ERROR_PERMISSION_DENIED, "bad path");
    return;
  }
  char *full = g_build_filename(bundle_real, rel, NULL);
  g_free(rel);
  char *real = realpath(full, NULL);
  g_free(full);
  struct stat st;
  if (real == NULL || !path_inside(real, bundle_real) || stat(real, &st) != 0 ||
      !S_ISREG(st.st_mode) || st.st_size > MAX_FILE) {
    free(real);
    fail(req, G_IO_ERROR_NOT_FOUND, "not found");
    return;
  }
  char *data = NULL;
  gsize len = 0;
  if (!g_file_get_contents(real, &data, &len, NULL)) {
    free(real);
    fail(req, G_IO_ERROR_NOT_FOUND, "not found");
    return;
  }
  GBytes *bytes = g_bytes_new_take(data, len);
  finish(req, bytes, mime_for(real));
  g_bytes_unref(bytes);
  free(real);
}

/* ---------- icons ---------- */

static gboolean
icon_dir_allowed(const char *real)
{
  const char *const fixed[] = {"/usr/share/icons", "/usr/share/pixmaps", "/usr/local/share/icons",
                               "/var/lib/flatpak/exports/share/icons", NULL};
  gboolean ok = FALSE;
  for (int i = 0; fixed[i] != NULL && !ok; i++) {
    char *d = realpath(fixed[i], NULL);
    if (d != NULL)
      ok = path_inside(real, d);
    free(d);
  }
  if (!ok) {
    char *home_icons = g_build_filename(g_get_home_dir(), ".local", "share", "icons", NULL);
    char *d = realpath(home_icons, NULL);
    if (d != NULL)
      ok = path_inside(real, d);
    free(d);
    g_free(home_icons);
  }
  return ok;
}

static gboolean
icon_name_ok(const char *s)
{
  gsize n = strlen(s);
  if (n == 0 || n > 200 || s[0] == '.')
    return FALSE;
  for (const char *p = s; *p; p++)
    if (!(g_ascii_isalnum(*p) || *p == '.' || *p == '_' || *p == '-' || *p == '+'))
      return FALSE;
  return TRUE;
}

static GdkPixbuf *
load_icon(const char *name, int size)
{
  GError *err = NULL;
  if (name[0] == '/') {
    char *real = realpath(name, NULL);
    struct stat st;
    GdkPixbuf *pb = NULL;
    if (real != NULL && icon_dir_allowed(real) && stat(real, &st) == 0 && S_ISREG(st.st_mode) &&
        st.st_size <= 8 * 1024 * 1024 &&
        (g_str_has_suffix(real, ".png") || g_str_has_suffix(real, ".svg") ||
         g_str_has_suffix(real, ".svgz") || g_str_has_suffix(real, ".xpm")))
      pb = gdk_pixbuf_new_from_file_at_scale(real, size, size, TRUE, &err);
    g_clear_error(&err);
    free(real);
    return pb;
  }
  if (!icon_name_ok(name))
    return NULL;
  GtkIconTheme *theme = gtk_icon_theme_get_default();
  GdkPixbuf *pb = gtk_icon_theme_load_icon(theme, name, size, GTK_ICON_LOOKUP_FORCE_SIZE, &err);
  g_clear_error(&err);
  return pb;
}

static void
serve_icon(WebKitURISchemeRequest *req, const char *encoded_path, const char *query)
{
  int size = 48;
  if (query != NULL) {
    GHashTable *params = g_uri_parse_params(query, -1, "&", G_URI_PARAMS_NONE, NULL);
    if (params != NULL) {
      const char *v = g_hash_table_lookup(params, "size");
      if (v != NULL) {
        gint64 n = g_ascii_strtoll(v, NULL, 10);
        size = (int)CLAMP(n, 8, 512);
      }
      g_hash_table_unref(params);
    }
  }
  /* The name is one escaped path segment (a theme name or an absolute path). */
  const char *seg = encoded_path[0] == '/' ? encoded_path + 1 : encoded_path;
  char *name = g_uri_unescape_string(seg, NULL);
  if (name == NULL || *name == '\0') {
    g_free(name);
    fail(req, G_IO_ERROR_NOT_FOUND, "no icon");
    return;
  }

  char *key = g_strdup_printf("%d\n%s", size, name);
  GBytes *png = icon_cache ? g_hash_table_lookup(icon_cache, key) : NULL;
  if (png != NULL) {
    finish(req, png, "image/png");
    g_free(key);
    g_free(name);
    return;
  }

  GdkPixbuf *pb = load_icon(name, size);
  g_free(name);
  if (pb == NULL) {
    g_free(key);
    fail(req, G_IO_ERROR_NOT_FOUND, "no icon");
    return;
  }
  char *buf = NULL;
  gsize len = 0;
  gboolean saved = gdk_pixbuf_save_to_buffer(pb, &buf, &len, "png", NULL, NULL);
  g_object_unref(pb);
  if (!saved) {
    g_free(key);
    fail(req, G_IO_ERROR_FAILED, "icon encode failed");
    return;
  }
  png = g_bytes_new_take(buf, len);
  if (icon_cache == NULL)
    icon_cache = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, (GDestroyNotify)g_bytes_unref);
  if (g_hash_table_size(icon_cache) >= ICON_CACHE_MAX)
    g_hash_table_remove_all(icon_cache);
  g_hash_table_insert(icon_cache, key, g_bytes_ref(png));
  finish(req, png, "image/png");
  g_bytes_unref(png);
}

static void
on_icon_theme_changed(GtkIconTheme *t, gpointer u)
{
  if (icon_cache != NULL)
    g_hash_table_remove_all(icon_cache);
}

/* ---------- alfad on the loopback address ----------
 * The pages are NoCap OS's own build and ask for /api/..., /dav/... and
 * /healthz as they do in a browser; those requests go on to alfad's
 * loopback-only listener (shell.api_origin), headers, body and all, and
 * the answer streams back. Nothing else is ever proxied. */

static SoupSession *api_session;

static gboolean
api_path(const char *path)
{
  return path != NULL && (g_str_has_prefix(path, "/api/") || g_str_has_prefix(path, "/dav/") ||
                          strcmp(path, "/healthz") == 0);
}

/* Request headers that go on to alfad (never the page's own origin or
 * cookies: the session is the bearer token). */
static gboolean
forward_request_header(const char *name)
{
  static const char *const keep[] = {"Authorization", "Content-Type", "Range", "Accept", "Accept-Language",
                                     "If-None-Match", "If-Modified-Since", "If-Range", "X-Alfa-Request",
                                     "X-Requested-With", "Depth", "Destination", "Overwrite", NULL};
  for (int i = 0; keep[i] != NULL; i++)
    if (g_ascii_strcasecmp(name, keep[i]) == 0)
      return TRUE;
  return g_ascii_strncasecmp(name, "X-File-", 7) == 0 || g_ascii_strncasecmp(name, "X-Upload-", 9) == 0;
}

static gboolean
forward_response_header(const char *name)
{
  static const char *const drop[] = {"Connection", "Keep-Alive", "Transfer-Encoding", "Set-Cookie",
                                     "Content-Security-Policy", "Strict-Transport-Security", NULL};
  for (int i = 0; drop[i] != NULL; i++)
    if (g_ascii_strcasecmp(name, drop[i]) == 0)
      return FALSE;
  return TRUE;
}

typedef struct {
  WebKitURISchemeRequest *req;
  SoupMessage *msg;
} ProxyJob;

static void
proxy_done(GObject *src, GAsyncResult *res, gpointer data)
{
  ProxyJob *j = data;
  GError *err = NULL;
  GInputStream *body = soup_session_send_finish(SOUP_SESSION(src), res, &err);
  if (body == NULL) {
    fail(j->req, G_IO_ERROR_HOST_UNREACHABLE, err ? err->message : "NoCapOS is not reachable");
    g_clear_error(&err);
  } else {
    SoupMessageHeaders *in = soup_message_get_response_headers(j->msg);
    goffset len = soup_message_headers_get_content_length(in);
    WebKitURISchemeResponse *resp = webkit_uri_scheme_response_new(body, len > 0 ? len : -1);
    webkit_uri_scheme_response_set_status(resp, soup_message_get_status(j->msg), soup_message_get_reason_phrase(j->msg));
    const char *ct = soup_message_headers_get_content_type(in, NULL);
    if (ct != NULL)
      webkit_uri_scheme_response_set_content_type(resp, ct);
    SoupMessageHeaders *out = soup_message_headers_new(SOUP_MESSAGE_HEADERS_RESPONSE);
    SoupMessageHeadersIter it;
    const char *name, *value;
    soup_message_headers_iter_init(&it, in);
    while (soup_message_headers_iter_next(&it, &name, &value))
      if (forward_response_header(name))
        soup_message_headers_append(out, name, value);
    webkit_uri_scheme_response_set_http_headers(resp, out);
    webkit_uri_scheme_request_finish_with_response(j->req, resp);
    g_object_unref(resp);
    g_object_unref(body);
  }
  g_object_unref(j->msg);
  g_object_unref(j->req);
  g_free(j);
}

static void
proxy_api(WebKitURISchemeRequest *req, const char *path, const char *query)
{
  const char *origin = shell.api_origin && *shell.api_origin ? shell.api_origin : "http://127.0.0.1:8780";
  if (!(g_str_has_prefix(origin, "http://127.0.0.1:") || g_str_has_prefix(origin, "https://127.0.0.1:"))) {
    fail(req, G_IO_ERROR_PERMISSION_DENIED, "not a loopback address");
    return;
  }
  char *target = g_strdup_printf("%s%s%s%s", origin, path, query ? "?" : "", query ? query : "");
  const char *method = webkit_uri_scheme_request_get_http_method(req);
  SoupMessage *msg = soup_message_new(method ? method : "GET", target);
  g_free(target);
  if (msg == NULL) {
    fail(req, G_IO_ERROR_INVALID_ARGUMENT, "bad request");
    return;
  }
  SoupMessageHeaders *in = webkit_uri_scheme_request_get_http_headers(req);
  SoupMessageHeaders *out = soup_message_get_request_headers(msg);
  if (in != NULL) {
    SoupMessageHeadersIter it;
    const char *name, *value;
    soup_message_headers_iter_init(&it, in);
    while (soup_message_headers_iter_next(&it, &name, &value))
      if (forward_request_header(name))
        soup_message_headers_append(out, name, value);
  }
#if WEBKIT_CHECK_VERSION(2, 40, 0)
  GInputStream *body = webkit_uri_scheme_request_get_http_body(req);
  if (body != NULL) {
    soup_message_set_request_body(msg, in ? soup_message_headers_get_content_type(in, NULL) : NULL, body, -1);
    g_object_unref(body);
  }
#endif
  if (api_session == NULL) {
    api_session = soup_session_new();
    soup_session_set_timeout(api_session, 0); /* long polls and downloads */
    soup_session_set_idle_timeout(api_session, 60);
    soup_session_set_proxy_resolver(api_session, NULL); /* straight to loopback */
  }
  ProxyJob *j = g_new0(ProxyJob, 1);
  j->req = g_object_ref(req);
  j->msg = msg;
  soup_session_send_async(api_session, msg, G_PRIORITY_DEFAULT, NULL, proxy_done, j);
}

static void
on_request(WebKitURISchemeRequest *req, gpointer u)
{
  const char *uri = webkit_uri_scheme_request_get_uri(req);
  GUri *g = uri ? g_uri_parse(uri, G_URI_FLAGS_ENCODED, NULL) : NULL;
  if (g == NULL) {
    fail(req, G_IO_ERROR_INVALID_ARGUMENT, "bad uri");
    return;
  }
  const char *host = g_uri_get_host(g);
  const char *path = g_uri_get_path(g);
  if (g_strcmp0(host, "shell") == 0 && api_path(path) && !shell.greeter)
    proxy_api(req, path, g_uri_get_query(g));
  else if (g_strcmp0(host, "shell") == 0)
    serve_file(req, path ? path : "/");
  else if (g_strcmp0(host, "icon") == 0)
    serve_icon(req, path ? path : "", g_uri_get_query(g));
  else
    fail(req, G_IO_ERROR_NOT_FOUND, "not found");
  g_uri_unref(g);
}

void
scheme_register(WebKitWebContext *ctx)
{
  bundle_real = shell.shell_dir ? realpath(shell.shell_dir, NULL) : NULL;
  if (bundle_real == NULL)
    g_warning("shell bundle %s not found: %s", shell.shell_dir ? shell.shell_dir : "(unset)",
              g_strerror(errno));
  webkit_web_context_register_uri_scheme(ctx, "nocap-shell", on_request, NULL, NULL);
  g_signal_connect(gtk_icon_theme_get_default(), "changed", G_CALLBACK(on_icon_theme_changed), NULL);
}

char *
icon_url_for_name(const char *name_or_path, int size)
{
  if (name_or_path == NULL || *name_or_path == '\0')
    return g_strdup("");
  char *esc = g_uri_escape_string(name_or_path, NULL, FALSE);
  char *url = g_strdup_printf("nocap-shell://icon/%s?size=%d", esc, size);
  g_free(esc);
  return url;
}

char *
icon_url_for_gicon(GIcon *icon, int size)
{
  if (icon == NULL)
    return g_strdup("");
  if (G_IS_THEMED_ICON(icon)) {
    const char *const *names = g_themed_icon_get_names(G_THEMED_ICON(icon));
    return icon_url_for_name(names && names[0] ? names[0] : NULL, size);
  }
  if (G_IS_FILE_ICON(icon)) {
    char *path = g_file_get_path(g_file_icon_get_file(G_FILE_ICON(icon)));
    char *url = icon_url_for_name(path, size);
    g_free(path);
    return url;
  }
  return g_strdup("");
}
