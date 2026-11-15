/* Bridge whitelist: which method each surface may call, and the exact shape
 * of its parameters. Anything not described here is refused. */
#include "bridge.h"

#include <string.h>

enum {
  S_BAR = 1 << 0,
  S_DOCK = 1 << 1,
  S_LAUNCHER = 1 << 2,
  S_CENTER = 1 << 3,
  S_BANNERS = 1 << 4,
  S_OSD = 1 << 5,
  S_POLKIT = 1 << 6,
  S_GREETER = 1 << 7,
};
#define S_SESSION (S_BAR | S_DOCK | S_LAUNCHER | S_CENTER | S_BANNERS | S_OSD | S_POLKIT)
#define S_ALL (S_SESSION | S_GREETER)

static const struct {
  const char *name;
  guint bit;
} surfaces[] = {
  {"bar", S_BAR},         {"dock", S_DOCK}, {"launcher", S_LAUNCHER},
  {"center", S_CENTER},   {"banners", S_BANNERS}, {"osd", S_OSD},
  {"polkit", S_POLKIT},   {"greeter", S_GREETER},
};

typedef enum {
  PT_STRING,
  PT_INT,
  PT_BOOL,
  PT_ID,   /* integer 0..2^32-1 or short token string; normalised to string */
  PT_STRV, /* array of strings */
} PType;

enum {
  PF_DESKTOP_ID = 1 << 0,
  PF_USERNAME = 1 << 1,
  PF_PRINTABLE = 1 << 2,
};

typedef struct {
  const char *name;
  PType type;
  gboolean required;
  gint64 min; /* int: minimum value; string: minimum bytes */
  gint64 max; /* int: maximum value; string: maximum bytes */
  const char *const *choices;
  guint max_items;
  guint flags;
} ParamSpec;

typedef struct {
  const char *name;
  guint surfaces;
  const ParamSpec *params;
} MethodSpec;

static const char *const SURFACE_NAMES[] = {"launcher", "center", "osd", NULL};
static const char *const POWER_SESSION[] = {"lock", "logout", "suspend", "reboot", "poweroff", NULL};
static const char *const POWER_GREETER[] = {"reboot", "poweroff", NULL};
static const char *const OPEN_APPS[] = {"settings", "appstore", "files", "home", NULL};

static const ParamSpec P_NONE[] = {{NULL}};
static const ParamSpec P_SURFACE[] = {
  {"name", PT_STRING, TRUE, 1, 32, SURFACE_NAMES, 0, 0}, {NULL}};
static const ParamSpec P_SIZE[] = {{"height", PT_INT, TRUE, 1, 2000, NULL, 0, 0}, {NULL}};
static const ParamSpec P_APP[] = {{"id", PT_STRING, TRUE, 1, 255, NULL, 0, PF_DESKTOP_ID}, {NULL}};
static const ParamSpec P_DOCK_SET[] = {
  {"pinned", PT_STRV, TRUE, 1, 255, NULL, 24, PF_DESKTOP_ID}, {NULL}};
static const ParamSpec P_WINDOW[] = {{"id", PT_ID, TRUE, 0, 0, NULL, 0, 0}, {NULL}};
static const ParamSpec P_AUDIO_SET[] = {
  {"volume", PT_INT, FALSE, 0, 150, NULL, 0, 0},
  {"muted", PT_BOOL, FALSE, 0, 0, NULL, 0, 0},
  {NULL}};
static const ParamSpec P_BRIGHTNESS_SET[] = {{"percent", PT_INT, TRUE, 1, 100, NULL, 0, 0}, {NULL}};
static const ParamSpec P_POWER[] = {{"action", PT_STRING, TRUE, 1, 16, POWER_SESSION, 0, 0}, {NULL}};
static const ParamSpec P_POWER_GREETER[] = {
  {"action", PT_STRING, TRUE, 1, 16, POWER_GREETER, 0, 0}, {NULL}};
static const ParamSpec P_NOTIFY_ID[] = {{"id", PT_ID, TRUE, 0, 0, NULL, 0, 0}, {NULL}};
static const ParamSpec P_NOTIFY_INVOKE[] = {
  {"id", PT_ID, TRUE, 0, 0, NULL, 0, 0},
  {"key", PT_STRING, TRUE, 1, 256, NULL, 0, 0},
  {NULL}};
static const ParamSpec P_NOTIFY_READ[] = {{"id", PT_ID, FALSE, 0, 0, NULL, 0, 0}, {NULL}};
static const ParamSpec P_OPEN[] = {{"app", PT_STRING, FALSE, 1, 16, OPEN_APPS, 0, 0}, {NULL}};
static const ParamSpec P_POLKIT_RESPOND[] = {
  {"cookie", PT_STRING, TRUE, 1, 512, NULL, 0, PF_PRINTABLE},
  {"password", PT_STRING, TRUE, 0, 512, NULL, 0, 0},
  {NULL}};
static const ParamSpec P_POLKIT_CANCEL[] = {
  {"cookie", PT_STRING, TRUE, 1, 512, NULL, 0, PF_PRINTABLE}, {NULL}};
static const ParamSpec P_LOGIN[] = {{"username", PT_STRING, TRUE, 1, 64, NULL, 0, PF_USERNAME}, {NULL}};
static const ParamSpec P_RESPOND[] = {{"response", PT_STRING, FALSE, 0, 1024, NULL, 0, 0}, {NULL}};

/* A method may appear more than once with different surfaces and params
 * (power.action is narrower on the greeter). */
static const MethodSpec methods[] = {
  {"shell.info", S_ALL, P_NONE},
  {"surface.show", S_SESSION, P_SURFACE},
  {"surface.hide", S_SESSION, P_SURFACE},
  {"surface.toggle", S_SESSION, P_SURFACE},
  {"surface.size", S_BANNERS, P_SIZE},
  {"apps.list", S_LAUNCHER | S_DOCK, P_NONE},
  {"apps.launch", S_LAUNCHER | S_DOCK | S_CENTER, P_APP},
  {"dock.get", S_DOCK | S_LAUNCHER, P_NONE},
  {"dock.set", S_DOCK | S_LAUNCHER, P_DOCK_SET},
  {"windows.list", S_DOCK | S_BAR, P_NONE},
  {"windows.focus", S_DOCK | S_BAR, P_WINDOW},
  {"windows.minimize", S_DOCK | S_BAR, P_WINDOW},
  {"windows.close", S_DOCK | S_BAR, P_WINDOW},
  {"windows.maximize", S_DOCK | S_BAR, P_WINDOW},
  {"audio.get", S_CENTER | S_BAR | S_OSD, P_NONE},
  {"audio.set", S_CENTER, P_AUDIO_SET},
  {"brightness.get", S_CENTER | S_OSD, P_NONE},
  {"brightness.set", S_CENTER, P_BRIGHTNESS_SET},
  {"network.get", S_CENTER | S_BAR, P_NONE},
  {"power.action", S_CENTER | S_BAR, P_POWER},
  {"power.action", S_GREETER, P_POWER_GREETER},
  {"notify.list", S_CENTER | S_BANNERS | S_BAR, P_NONE},
  {"notify.dismiss", S_CENTER | S_BANNERS, P_NOTIFY_ID},
  {"notify.invoke", S_CENTER | S_BANNERS, P_NOTIFY_INVOKE},
  {"notify.clear", S_CENTER | S_BANNERS, P_NONE},
  {"notify.read", S_CENTER | S_BANNERS, P_NOTIFY_READ},
  {"nocapos.open", S_CENTER | S_BAR | S_LAUNCHER | S_DOCK, P_OPEN},
  {"polkit.respond", S_POLKIT, P_POLKIT_RESPOND},
  {"polkit.cancel", S_POLKIT, P_POLKIT_CANCEL},
  {"greeter.users", S_GREETER, P_NONE},
  {"greeter.login", S_GREETER, P_LOGIN},
  {"greeter.respond", S_GREETER, P_RESPOND},
  {"greeter.cancel", S_GREETER, P_NONE},
};

#define MAX_SAFE_INT G_GINT64_CONSTANT(9007199254740991)

static guint
surface_bit(const char *surface)
{
  if (surface == NULL)
    return 0;
  for (gsize i = 0; i < G_N_ELEMENTS(surfaces); i++)
    if (strcmp(surfaces[i].name, surface) == 0)
      return surfaces[i].bit;
  return 0;
}

gboolean
bridge_surface_known(const char *surface)
{
  return surface_bit(surface) != 0;
}

gboolean
bridge_method_known(const char *method)
{
  if (method == NULL)
    return FALSE;
  for (gsize i = 0; i < G_N_ELEMENTS(methods); i++)
    if (strcmp(methods[i].name, method) == 0)
      return TRUE;
  return FALSE;
}

void
bridge_call_clear(BridgeCall *call)
{
  if (call == NULL)
    return;
  if (call->root != NULL)
    json_node_unref(call->root);
  call->root = NULL;
  call->params = NULL;
  call->method = NULL;
  call->id = -1;
}

/* Reads the id of an oversize message only when it is the exact prefix a
 * page produces: {"id":<digits>, */
static gint64
peek_id(const char *text, gsize len)
{
  static const char prefix[] = "{\"id\":";
  gsize p = sizeof(prefix) - 1;
  gint64 v = 0;
  gsize digits = 0;

  if (len < p + 2 || strncmp(text, prefix, p) != 0)
    return -1;
  while (p < len && text[p] >= '0' && text[p] <= '9' && digits < 16) {
    v = v * 10 + (text[p] - '0');
    p++;
    digits++;
  }
  if (digits == 0 || p >= len || text[p] != ',')
    return -1;
  return v;
}

static gboolean
str_is_desktop_id(const char *s)
{
  gsize n = strlen(s);
  if (n < 9 || !g_str_has_suffix(s, ".desktop") || s[0] == '.' || s[0] == '-')
    return FALSE;
  if (strstr(s, "..") != NULL)
    return FALSE;
  for (const char *p = s; *p; p++) {
    char c = *p;
    if (!(g_ascii_isalnum(c) || c == '.' || c == '_' || c == '-' || c == '+'))
      return FALSE;
  }
  return TRUE;
}

static gboolean
str_is_username(const char *s)
{
  gsize n = strlen(s);
  if (n == 0 || s[0] == '-' || s[0] == '.')
    return FALSE;
  for (gsize i = 0; i < n; i++) {
    char c = s[i];
    if (g_ascii_isalnum(c) || c == '_' || c == '.' || c == '-')
      continue;
    if (c == '$' && i == n - 1 && n > 1)
      continue;
    return FALSE;
  }
  return TRUE;
}

static gboolean
str_is_printable(const char *s)
{
  for (const unsigned char *p = (const unsigned char *)s; *p; p++)
    if (*p < 0x21 || *p > 0x7e)
      return FALSE;
  return TRUE;
}

static gboolean
str_is_id_token(const char *s)
{
  gsize n = strlen(s);
  if (n == 0 || n > 128)
    return FALSE;
  for (const char *p = s; *p; p++) {
    char c = *p;
    if (!(g_ascii_isalnum(c) || c == ':' || c == '.' || c == '_' || c == '-'))
      return FALSE;
  }
  return TRUE;
}

static gboolean
check_string(const ParamSpec *spec, const char *s, char **error)
{
  gsize n = strlen(s);
  if ((gint64)n > spec->max) {
    *error = g_strdup_printf("too long: %s", spec->name);
    return FALSE;
  }
  if ((gint64)n < spec->min) {
    *error = g_strdup_printf("too short: %s", spec->name);
    return FALSE;
  }
  if (spec->choices != NULL && !g_strv_contains(spec->choices, s)) {
    *error = g_strdup_printf("not allowed: %s", spec->name);
    return FALSE;
  }
  if ((spec->flags & PF_DESKTOP_ID) && !str_is_desktop_id(s)) {
    *error = g_strdup_printf("invalid value: %s", spec->name);
    return FALSE;
  }
  if ((spec->flags & PF_USERNAME) && !str_is_username(s)) {
    *error = g_strdup_printf("invalid value: %s", spec->name);
    return FALSE;
  }
  if ((spec->flags & PF_PRINTABLE) && !str_is_printable(s)) {
    *error = g_strdup_printf("invalid value: %s", spec->name);
    return FALSE;
  }
  return TRUE;
}

/* Returns TRUE and stores an integer if node is a JSON integer (or an
 * integral double, as some pages produce). */
static gboolean
node_get_integer(JsonNode *node, gint64 *out)
{
  if (!JSON_NODE_HOLDS_VALUE(node))
    return FALSE;
  GType t = json_node_get_value_type(node);
  if (t == G_TYPE_INT64) {
    *out = json_node_get_int(node);
    return TRUE;
  }
  if (t == G_TYPE_DOUBLE) {
    double d = json_node_get_double(node);
    if (!(d >= -9007199254740991.0 && d <= 9007199254740991.0))
      return FALSE;
    if ((double)(gint64)d != d)
      return FALSE;
    *out = (gint64)d;
    return TRUE;
  }
  return FALSE;
}

static gboolean
check_param(const ParamSpec *spec, JsonObject *params, JsonNode *node, char **error)
{
  switch (spec->type) {
  case PT_STRING:
    if (!JSON_NODE_HOLDS_VALUE(node) || json_node_get_value_type(node) != G_TYPE_STRING) {
      *error = g_strdup_printf("wrong type: %s", spec->name);
      return FALSE;
    }
    return check_string(spec, json_node_get_string(node), error);

  case PT_INT: {
    gint64 v;
    if (!node_get_integer(node, &v)) {
      *error = g_strdup_printf("wrong type: %s", spec->name);
      return FALSE;
    }
    if (v < spec->min || v > spec->max) {
      *error = g_strdup_printf("out of range: %s", spec->name);
      return FALSE;
    }
    json_object_set_int_member(params, spec->name, v);
    return TRUE;
  }

  case PT_BOOL:
    if (!JSON_NODE_HOLDS_VALUE(node) || json_node_get_value_type(node) != G_TYPE_BOOLEAN) {
      *error = g_strdup_printf("wrong type: %s", spec->name);
      return FALSE;
    }
    return TRUE;

  case PT_ID: {
    gint64 v;
    if (node_get_integer(node, &v)) {
      if (v < 0 || v > G_MAXUINT32) {
        *error = g_strdup_printf("out of range: %s", spec->name);
        return FALSE;
      }
      char buf[24];
      g_snprintf(buf, sizeof buf, "%" G_GINT64_FORMAT, v);
      json_object_set_string_member(params, spec->name, buf);
      return TRUE;
    }
    if (JSON_NODE_HOLDS_VALUE(node) && json_node_get_value_type(node) == G_TYPE_STRING) {
      if (!str_is_id_token(json_node_get_string(node))) {
        *error = g_strdup_printf("invalid value: %s", spec->name);
        return FALSE;
      }
      return TRUE;
    }
    *error = g_strdup_printf("wrong type: %s", spec->name);
    return FALSE;
  }

  case PT_STRV: {
    if (!JSON_NODE_HOLDS_ARRAY(node)) {
      *error = g_strdup_printf("wrong type: %s", spec->name);
      return FALSE;
    }
    JsonArray *arr = json_node_get_array(node);
    guint n = json_array_get_length(arr);
    if (n > spec->max_items) {
      *error = g_strdup_printf("too many items: %s", spec->name);
      return FALSE;
    }
    for (guint i = 0; i < n; i++) {
      JsonNode *el = json_array_get_element(arr, i);
      if (!JSON_NODE_HOLDS_VALUE(el) || json_node_get_value_type(el) != G_TYPE_STRING) {
        *error = g_strdup_printf("wrong type: %s", spec->name);
        return FALSE;
      }
      if (!check_string(spec, json_node_get_string(el), error))
        return FALSE;
    }
    return TRUE;
  }
  }
  *error = g_strdup("internal error");
  return FALSE;
}

static gboolean
check_params(const MethodSpec *m, JsonObject *params, char **error)
{
  /* Unknown members first. */
  GList *names = json_object_get_members(params);
  for (GList *l = names; l != NULL; l = l->next) {
    const char *name = l->data;
    gboolean known = FALSE;
    for (const ParamSpec *s = m->params; s->name != NULL; s++)
      if (strcmp(s->name, name) == 0)
        known = TRUE;
    if (!known) {
      *error = g_strdup_printf("unknown parameter: %.64s", name);
      g_list_free(names);
      return FALSE;
    }
  }
  g_list_free(names);

  for (const ParamSpec *s = m->params; s->name != NULL; s++) {
    JsonNode *node = json_object_get_member(params, s->name);
    if (node != NULL && JSON_NODE_HOLDS_NULL(node) && !s->required) {
      json_object_remove_member(params, s->name);
      node = NULL;
    }
    if (node == NULL || JSON_NODE_HOLDS_NULL(node)) {
      if (s->required) {
        *error = g_strdup_printf("missing parameter: %s", s->name);
        return FALSE;
      }
      continue;
    }
    if (!check_param(s, params, node, error))
      return FALSE;
  }
  return TRUE;
}

gboolean
bridge_validate(const char *surface, const char *json_text, gsize len, BridgeCall *out,
                char **error)
{
  g_return_val_if_fail(out != NULL && error != NULL, FALSE);
  out->id = -1;
  out->method = NULL;
  out->root = NULL;
  out->params = NULL;
  *error = NULL;

  guint bit = surface_bit(surface);
  if (bit == 0) {
    *error = g_strdup("unknown surface");
    return FALSE;
  }
  if (json_text == NULL) {
    *error = g_strdup("invalid message");
    return FALSE;
  }
  if (len > BRIDGE_MAX_MESSAGE) {
    out->id = peek_id(json_text, len);
    *error = g_strdup("message too large");
    return FALSE;
  }
  if (memchr(json_text, '\0', len) != NULL) {
    *error = g_strdup("invalid message");
    return FALSE;
  }

  JsonParser *parser = json_parser_new();
  GError *perr = NULL;
  if (!json_parser_load_from_data(parser, json_text, (gssize)len, &perr)) {
    g_clear_error(&perr);
    g_object_unref(parser);
    *error = g_strdup("invalid JSON");
    return FALSE;
  }
  JsonNode *root = json_parser_get_root(parser);
  if (root == NULL || !JSON_NODE_HOLDS_OBJECT(root)) {
    g_object_unref(parser);
    *error = g_strdup("invalid message");
    return FALSE;
  }
  /* Keeps a reference to the parsed object after the parser goes away;
   * params are normalised in place below. */
  out->root = json_node_copy(root);
  g_object_unref(parser);
  JsonObject *msg = json_node_get_object(out->root);

  JsonNode *idn = json_object_get_member(msg, "id");
  gint64 id;
  if (idn == NULL || !node_get_integer(idn, &id) || id < 0 || id > MAX_SAFE_INT) {
    *error = g_strdup("invalid id");
    return FALSE;
  }
  out->id = id;

  GList *keys = json_object_get_members(msg);
  for (GList *l = keys; l != NULL; l = l->next) {
    const char *k = l->data;
    if (strcmp(k, "id") != 0 && strcmp(k, "method") != 0 && strcmp(k, "params") != 0) {
      g_list_free(keys);
      *error = g_strdup("invalid message");
      return FALSE;
    }
  }
  g_list_free(keys);

  JsonNode *mn = json_object_get_member(msg, "method");
  if (mn == NULL || !JSON_NODE_HOLDS_VALUE(mn) || json_node_get_value_type(mn) != G_TYPE_STRING) {
    *error = g_strdup("invalid method");
    return FALSE;
  }
  const char *method = json_node_get_string(mn);

  const MethodSpec *spec = NULL;
  gboolean exists = FALSE;
  for (gsize i = 0; i < G_N_ELEMENTS(methods); i++) {
    if (strcmp(methods[i].name, method) != 0)
      continue;
    exists = TRUE;
    if (methods[i].surfaces & bit) {
      spec = &methods[i];
      break;
    }
  }
  if (!exists) {
    *error = g_strdup("unknown method");
    return FALSE;
  }
  if (spec == NULL) {
    *error = g_strdup("method not allowed for this surface");
    return FALSE;
  }

  JsonNode *pn = json_object_get_member(msg, "params");
  if (pn == NULL || JSON_NODE_HOLDS_NULL(pn)) {
    json_object_set_object_member(msg, "params", json_object_new());
    pn = json_object_get_member(msg, "params");
  }
  if (!JSON_NODE_HOLDS_OBJECT(pn)) {
    *error = g_strdup("invalid params");
    return FALSE;
  }
  JsonObject *params = json_node_get_object(pn);
  if (!check_params(spec, params, error))
    return FALSE;

  out->method = spec->name;
  out->params = params;
  return TRUE;
}

gboolean
bridge_param_has(const BridgeCall *c, const char *name)
{
  return c->params != NULL && json_object_has_member(c->params, name);
}

const char *
bridge_param_str(const BridgeCall *c, const char *name)
{
  if (!bridge_param_has(c, name))
    return NULL;
  JsonNode *n = json_object_get_member(c->params, name);
  if (!JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_STRING)
    return NULL;
  return json_node_get_string(n);
}

gint64
bridge_param_int(const BridgeCall *c, const char *name, gint64 def)
{
  if (!bridge_param_has(c, name))
    return def;
  JsonNode *n = json_object_get_member(c->params, name);
  if (!JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_INT64)
    return def;
  return json_node_get_int(n);
}

gboolean
bridge_param_bool(const BridgeCall *c, const char *name, gboolean def)
{
  if (!bridge_param_has(c, name))
    return def;
  JsonNode *n = json_object_get_member(c->params, name);
  if (!JSON_NODE_HOLDS_VALUE(n) || json_node_get_value_type(n) != G_TYPE_BOOLEAN)
    return def;
  return json_node_get_boolean(n);
}

JsonArray *
bridge_param_array(const BridgeCall *c, const char *name)
{
  if (!bridge_param_has(c, name))
    return NULL;
  JsonNode *n = json_object_get_member(c->params, name);
  return JSON_NODE_HOLDS_ARRAY(n) ? json_node_get_array(n) : NULL;
}

void
bridge_param_wipe(const BridgeCall *c, const char *name)
{
  const char *s = bridge_param_str(c, name);
  if (s == NULL)
    return;
  /* The node owns this heap buffer; wipe it before it is freed. */
  volatile char *p = (volatile char *)s;
  while (*p)
    *p++ = 0;
}
