/* Bridge whitelist and validation. Pure module: glib + json-glib only. */
#ifndef NOCAP_BRIDGE_H
#define NOCAP_BRIDGE_H

#include <glib.h>
#include <json-glib/json-glib.h>

#define BRIDGE_MAX_MESSAGE (64 * 1024)

typedef struct {
  gint64 id;          /* request id, -1 when the message had no usable id */
  const char *method; /* points into the static method table */
  JsonNode *root;     /* parsed message (owned) */
  JsonObject *params; /* validated params (borrowed from root, never NULL on success) */
} BridgeCall;

/* Validates one page message for `surface`. On success fills `out` and
 * returns TRUE. On failure returns FALSE, sets *error (newly allocated) and
 * still sets out->id when the id could be read, so the caller can reply.
 * Always call bridge_call_clear() afterwards. */
gboolean bridge_validate(const char *surface, const char *json_text, gsize len,
                         BridgeCall *out, char **error);
void bridge_call_clear(BridgeCall *call);

gboolean bridge_surface_known(const char *surface);
gboolean bridge_method_known(const char *method);

/* Accessors for validated params. Ids are normalised to strings. */
gboolean bridge_param_has(const BridgeCall *c, const char *name);
const char *bridge_param_str(const BridgeCall *c, const char *name);
gint64 bridge_param_int(const BridgeCall *c, const char *name, gint64 def);
gboolean bridge_param_bool(const BridgeCall *c, const char *name, gboolean def);
JsonArray *bridge_param_array(const BridgeCall *c, const char *name);

/* Overwrites a string param in place (used to wipe passwords). */
void bridge_param_wipe(const BridgeCall *c, const char *name);

#endif
