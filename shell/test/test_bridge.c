/* Unit tests for the bridge whitelist (shell/host/bridge.c). */
#include <string.h>

#include "../host/bridge.h"

static gboolean
run(const char *surface, const char *json, BridgeCall *call, char **err)
{
  return bridge_validate(surface, json, strlen(json), call, err);
}

static void
expect_ok(const char *surface, const char *json)
{
  BridgeCall c;
  char *err = NULL;
  gboolean ok = run(surface, json, &c, &err);
  if (!ok)
    g_test_message("unexpected refusal for %s: %s (%s)", surface, json, err);
  g_assert_true(ok);
  g_assert_null(err);
  g_assert_nonnull(c.method);
  g_assert_nonnull(c.params);
  bridge_call_clear(&c);
}

static void
expect_err(const char *surface, const char *json, const char *fragment)
{
  BridgeCall c;
  char *err = NULL;
  gboolean ok = run(surface, json, &c, &err);
  if (ok)
    g_test_message("unexpected acceptance for %s: %s", surface, json);
  g_assert_false(ok);
  g_assert_nonnull(err);
  if (fragment != NULL && strstr(err, fragment) == NULL)
    g_test_message("error '%s' lacks '%s'", err, fragment);
  if (fragment != NULL)
    g_assert_nonnull(strstr(err, fragment));
  g_free(err);
  bridge_call_clear(&c);
}

static void
test_unknown_method(void)
{
  expect_err("chrome", "{\"id\":1,\"method\":\"shell.exec\",\"params\":{}}", "unknown method");
  expect_err("chrome", "{\"id\":1,\"method\":\"\"}", "unknown method");
  /* The earlier separate surfaces' calls are gone. */
  expect_err("chrome", "{\"id\":1,\"method\":\"dock.set\",\"params\":{\"pinned\":[]}}", "unknown method");
  expect_err("chrome", "{\"id\":1,\"method\":\"nocapos.open\"}", "unknown method");
}

static void
test_not_allowed(void)
{
  expect_err("greeter", "{\"id\":1,\"method\":\"apps.list\"}", "not allowed");
  expect_err("greeter", "{\"id\":1,\"method\":\"session.get\"}", "not allowed");
  expect_err("polkit", "{\"id\":1,\"method\":\"session.get\"}", "not allowed");
  expect_err("app", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"volume\":3}}", "not allowed");
  expect_err("desktop", "{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":[]}}", "not allowed");
  expect_err("chrome", "{\"id\":1,\"method\":\"window.action\",\"params\":{\"action\":\"close\"}}", "not allowed");
  expect_err("chrome", "{\"id\":1,\"method\":\"polkit.respond\",\"params\":{\"cookie\":\"c\",\"password\":\"x\"}}",
             "not allowed");
  expect_err("chrome", "{\"id\":1,\"method\":\"greeter.users\"}", "not allowed");
  expect_err("app", "{\"id\":1,\"method\":\"windows.close\",\"params\":{\"id\":3}}", "not allowed");
}

static void
test_unknown_surface(void)
{
  expect_err("bar", "{\"id\":1,\"method\":\"shell.info\"}", "unknown surface");
  expect_err("wallpaper", "{\"id\":1,\"method\":\"shell.info\"}", "unknown surface");
  expect_err(NULL, "{\"id\":1,\"method\":\"shell.info\"}", "unknown surface");
}

static void
test_missing_param(void)
{
  expect_err("chrome", "{\"id\":1,\"method\":\"apps.launch\",\"params\":{}}", "missing parameter: id");
  expect_err("chrome", "{\"id\":1,\"method\":\"power.action\"}", "missing parameter: action");
  expect_err("chrome", "{\"id\":1,\"method\":\"brightness.set\",\"params\":{\"percent\":null}}", "missing parameter");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.input\",\"params\":{}}", "missing parameter: rects");
  expect_err("app", "{\"id\":1,\"method\":\"nocap.open\",\"params\":{}}", "missing parameter: app");
}

static void
test_wrong_type(void)
{
  expect_err("chrome", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"volume\":\"50\"}}", "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"muted\":1}}", "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"volume\":50.5}}", "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":\"0,0,1,1\"}}", "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":[1,\"2\"]}}", "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":[1.5]}}", "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"windows.focus\",\"params\":{\"id\":true}}", "wrong type");
  expect_err("app", "{\"id\":1,\"method\":\"nocap.open\",\"params\":{\"app\":\"files\",\"single\":\"yes\"}}",
             "wrong type");
  expect_err("chrome", "{\"id\":1,\"method\":\"shell.info\",\"params\":[]}", "invalid params");
  expect_err("chrome", "{\"id\":\"1\",\"method\":\"shell.info\"}", "invalid id");
  expect_err("chrome", "{\"id\":1,\"method\":5}", "invalid method");
  expect_err("chrome", "[1,2]", "invalid message");
  expect_err("chrome", "{\"id\":1,", "invalid JSON");
}

static void
test_out_of_range(void)
{
  expect_err("chrome", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"volume\":151}}", "out of range");
  expect_err("chrome", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"volume\":-1}}", "out of range");
  expect_err("chrome", "{\"id\":1,\"method\":\"brightness.set\",\"params\":{\"percent\":0}}", "out of range");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.zones\",\"params\":{\"top\":601}}", "out of range");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":[0,0,-1,4]}}", "out of range");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":[0,0,40000,4]}}", "out of range");
  expect_err("chrome", "{\"id\":1,\"method\":\"windows.close\",\"params\":{\"id\":4294967296}}", "out of range");
  expect_err("chrome", "{\"id\":-3,\"method\":\"shell.info\"}", "invalid id");
}

static void
test_unknown_param(void)
{
  expect_err("chrome", "{\"id\":1,\"method\":\"shell.info\",\"params\":{\"x\":1}}", "unknown parameter: x");
  expect_err("chrome", "{\"id\":1,\"method\":\"audio.set\",\"params\":{\"volume\":5,\"sink\":\"x\"}}",
             "unknown parameter: sink");
  expect_err("chrome", "{\"id\":1,\"method\":\"shell.info\",\"extra\":true}", "invalid message");
}

static void
test_oversize(void)
{
  GString *s = g_string_new("{\"id\":77,\"method\":\"notify.dismiss\",\"params\":{\"id\":\"");
  while (s->len <= BRIDGE_MAX_MESSAGE)
    g_string_append(s, "aaaaaaaaaaaaaaaa");
  g_string_append(s, "\"}}");
  BridgeCall c;
  char *err = NULL;
  g_assert_false(bridge_validate("chrome", s->str, s->len, &c, &err));
  g_assert_cmpstr(err, ==, "message too large");
  g_assert_cmpint(c.id, ==, 77); /* still answerable */
  g_free(err);
  bridge_call_clear(&c);
  g_string_free(s, TRUE);
}

static void
test_too_long(void)
{
  GString *s = g_string_new("{\"id\":1,\"method\":\"polkit.respond\",\"params\":{\"cookie\":\"c1\",\"password\":\"");
  for (int i = 0; i < 513; i++)
    g_string_append_c(s, 'x');
  g_string_append(s, "\"}}");
  expect_err("polkit", s->str, "too long: password");
  g_string_free(s, TRUE);

  GString *a = g_string_new("{\"id\":1,\"method\":\"surface.input\",\"params\":{\"rects\":[");
  for (int i = 0; i < 129; i++)
    g_string_append_printf(a, "%s%d", i ? "," : "", i);
  g_string_append(a, "]}}");
  expect_err("chrome", a->str, "too many items");
  g_string_free(a, TRUE);

  GString *p = g_string_new("{\"id\":1,\"method\":\"nocap.open\",\"params\":{\"app\":\"files\",\"props\":\"");
  for (int i = 0; i < 8193; i++)
    g_string_append_c(p, 'a');
  g_string_append(p, "\"}}");
  expect_err("chrome", p->str, "too long: props");
  g_string_free(p, TRUE);
}

static void
test_enum(void)
{
  expect_ok("greeter", "{\"id\":1,\"method\":\"power.action\",\"params\":{\"action\":\"poweroff\"}}");
  expect_ok("greeter", "{\"id\":1,\"method\":\"power.action\",\"params\":{\"action\":\"reboot\"}}");
  expect_err("greeter", "{\"id\":1,\"method\":\"power.action\",\"params\":{\"action\":\"logout\"}}",
             "not allowed: action");
  expect_ok("chrome", "{\"id\":1,\"method\":\"power.action\",\"params\":{\"action\":\"logout\"}}");
  expect_err("chrome", "{\"id\":1,\"method\":\"power.action\",\"params\":{\"action\":\"halt\"}}",
             "not allowed: action");
  expect_err("desktop", "{\"id\":1,\"method\":\"ui.command\",\"params\":{\"name\":\"terminal\"}}",
             "not allowed: name");
  expect_err("chrome", "{\"id\":1,\"method\":\"surface.keyboard\",\"params\":{\"mode\":\"grab\"}}",
             "not allowed: mode");
  expect_err("app", "{\"id\":1,\"method\":\"window.action\",\"params\":{\"action\":\"raise-all\"}}",
             "not allowed: action");
}

static void
test_value_checks(void)
{
  expect_err("chrome", "{\"id\":1,\"method\":\"apps.launch\",\"params\":{\"id\":\"../../x.desktop\"}}",
             "invalid value");
  expect_err("chrome", "{\"id\":1,\"method\":\"apps.launch\",\"params\":{\"id\":\"firefox\"}}", "invalid value");
  expect_err("greeter", "{\"id\":1,\"method\":\"greeter.login\",\"params\":{\"username\":\"a b\"}}", "invalid value");
  expect_err("greeter", "{\"id\":1,\"method\":\"greeter.login\",\"params\":{\"username\":\"-root\"}}",
             "invalid value");
  expect_err("polkit", "{\"id\":1,\"method\":\"polkit.cancel\",\"params\":{\"cookie\":\"a b\"}}", "invalid value");
  expect_err("chrome", "{\"id\":1,\"method\":\"notify.dismiss\",\"params\":{\"id\":\"a/b\"}}", "invalid value");
  /* App ids: lowercase letters and digits only (they become an argument). */
  expect_err("chrome", "{\"id\":1,\"method\":\"nocap.open\",\"params\":{\"app\":\"--greeter\"}}", "invalid value");
  expect_err("chrome", "{\"id\":1,\"method\":\"nocap.open\",\"params\":{\"app\":\"Files\"}}", "invalid value");
  expect_err("chrome", "{\"id\":1,\"method\":\"nocap.open\",\"params\":{\"app\":\"a/b\"}}", "invalid value");
  expect_err("chrome", "{\"id\":1,\"method\":\"nocap.open\",\"params\":{\"app\":\"files\",\"props\":\"a\\nb\"}}",
             "invalid value");
  expect_err("app", "{\"id\":1,\"method\":\"window.title\",\"params\":{\"title\":\"a\\u0007\"}}", "invalid value");
}

static void
test_valid_calls(void)
{
  static const struct {
    const char *surface;
    const char *json;
  } ok[] = {
    {"chrome", "{\"id\":0,\"method\":\"shell.info\"}"},
    {"greeter", "{\"id\":1,\"method\":\"shell.info\",\"params\":{}}"},
    {"polkit", "{\"id\":1,\"method\":\"shell.info\",\"params\":null}"},
    {"desktop", "{\"id\":1,\"method\":\"session.get\"}"},
    {"app", "{\"id\":1,\"method\":\"session.refresh\"}"},
    {"chrome", "{\"id\":2,\"method\":\"surface.input\",\"params\":{\"rects\":[0,0,1920,32,600,1000,720,76]}}"},
    {"chrome", "{\"id\":2,\"method\":\"surface.input\",\"params\":{\"rects\":[],\"full\":true}}"},
    {"chrome", "{\"id\":2,\"method\":\"surface.zones\",\"params\":{\"top\":32,\"bottom\":84}}"},
    {"chrome", "{\"id\":2,\"method\":\"surface.keyboard\",\"params\":{\"mode\":\"exclusive\"}}"},
    {"desktop", "{\"id\":2,\"method\":\"surface.keyboard\",\"params\":{\"mode\":\"on-demand\"}}"},
    {"desktop", "{\"id\":3,\"method\":\"ui.command\",\"params\":{\"name\":\"launcher\"}}"},
    {"app", "{\"id\":3,\"method\":\"ui.command\",\"params\":{\"name\":\"spotlight\"}}"},
    {"chrome", "{\"id\":3,\"method\":\"nocap.open\",\"params\":{\"app\":\"files\"}}"},
    {"desktop", "{\"id\":3,\"method\":\"nocap.open\",\"params\":{\"app\":\"settings\",\"props\":\"{\\\"section\\\":\\\"wallpaper\\\"}\",\"single\":true}}"},
    {"chrome", "{\"id\":4,\"method\":\"apps.list\"}"},
    {"chrome", "{\"id\":4,\"method\":\"apps.launch\",\"params\":{\"id\":\"org.example.App.desktop\"}}"},
    {"chrome", "{\"id\":6,\"method\":\"windows.list\"}"},
    {"chrome", "{\"id\":6,\"method\":\"windows.focus\",\"params\":{\"id\":3}}"},
    {"chrome", "{\"id\":6,\"method\":\"windows.minimize\",\"params\":{\"id\":\"3\"}}"},
    {"chrome", "{\"id\":6,\"method\":\"windows.close\",\"params\":{\"id\":3}}"},
    {"chrome", "{\"id\":6,\"method\":\"windows.maximize\",\"params\":{\"id\":3}}"},
    {"app", "{\"id\":6,\"method\":\"window.action\",\"params\":{\"action\":\"move\"}}"},
    {"app", "{\"id\":6,\"method\":\"window.action\",\"params\":{\"action\":\"resize-se\"}}"},
    {"app", "{\"id\":6,\"method\":\"window.title\",\"params\":{\"title\":\"Files \\u2014 Documents\"}}"},
    {"chrome", "{\"id\":7,\"method\":\"audio.get\"}"},
    {"chrome", "{\"id\":7,\"method\":\"audio.set\",\"params\":{\"volume\":150,\"muted\":false}}"},
    {"chrome", "{\"id\":7,\"method\":\"audio.set\",\"params\":{\"volume\":40.0}}"},
    {"chrome", "{\"id\":8,\"method\":\"brightness.get\"}"},
    {"chrome", "{\"id\":8,\"method\":\"brightness.set\",\"params\":{\"percent\":1}}"},
    {"chrome", "{\"id\":9,\"method\":\"network.get\"}"},
    {"chrome", "{\"id\":10,\"method\":\"power.action\",\"params\":{\"action\":\"lock\"}}"},
    {"chrome", "{\"id\":11,\"method\":\"notify.list\"}"},
    {"chrome", "{\"id\":11,\"method\":\"notify.dismiss\",\"params\":{\"id\":\"a12\"}}"},
    {"chrome", "{\"id\":11,\"method\":\"notify.invoke\",\"params\":{\"id\":12,\"key\":\"default\"}}"},
    {"chrome", "{\"id\":11,\"method\":\"notify.clear\"}"},
    {"chrome", "{\"id\":11,\"method\":\"notify.read\",\"params\":{\"id\":\"n:55\"}}"},
    {"polkit", "{\"id\":13,\"method\":\"polkit.respond\",\"params\":{\"cookie\":\"3-abc-1\",\"password\":\"\"}}"},
    {"polkit", "{\"id\":13,\"method\":\"polkit.cancel\",\"params\":{\"cookie\":\"3-abc-1\"}}"},
    {"greeter", "{\"id\":14,\"method\":\"greeter.users\"}"},
    {"greeter", "{\"id\":14,\"method\":\"greeter.login\",\"params\":{\"username\":\"sam\"}}"},
    {"greeter", "{\"id\":14,\"method\":\"greeter.respond\",\"params\":{\"response\":\"pw\"}}"},
    {"greeter", "{\"id\":14,\"method\":\"greeter.respond\",\"params\":{\"response\":null}}"},
    {"greeter", "{\"id\":14,\"method\":\"greeter.cancel\"}"},
  };
  for (gsize i = 0; i < G_N_ELEMENTS(ok); i++)
    expect_ok(ok[i].surface, ok[i].json);
}

static void
test_normalisation(void)
{
  BridgeCall c;
  char *err = NULL;
  const char *j = "{\"id\":9,\"method\":\"windows.focus\",\"params\":{\"id\":42}}";
  g_assert_true(bridge_validate("chrome", j, strlen(j), &c, &err));
  g_assert_cmpint(c.id, ==, 9);
  g_assert_cmpstr(c.method, ==, "windows.focus");
  g_assert_cmpstr(bridge_param_str(&c, "id"), ==, "42");
  bridge_call_clear(&c);

  j = "{\"id\":9,\"method\":\"audio.set\",\"params\":{\"volume\":40.0}}";
  g_assert_true(bridge_validate("chrome", j, strlen(j), &c, &err));
  g_assert_cmpint(bridge_param_int(&c, "volume", -1), ==, 40);
  g_assert_false(bridge_param_has(&c, "muted"));
  bridge_call_clear(&c);

  j = "{\"id\":9,\"method\":\"polkit.respond\",\"params\":{\"cookie\":\"k\",\"password\":\"secret\"}}";
  g_assert_true(bridge_validate("polkit", j, strlen(j), &c, &err));
  bridge_param_wipe(&c, "password");
  g_assert_cmpstr(bridge_param_str(&c, "password"), ==, "");
  bridge_call_clear(&c);

  j = "{\"id\":9,\"method\":\"surface.input\",\"params\":{\"rects\":[1,2,3,4]}}";
  g_assert_true(bridge_validate("chrome", j, strlen(j), &c, &err));
  g_assert_cmpint(json_array_get_length(bridge_param_array(&c, "rects")), ==, 4);
  bridge_call_clear(&c);
}

int
main(int argc, char **argv)
{
  g_test_init(&argc, &argv, NULL);
  g_test_add_func("/bridge/unknown-method", test_unknown_method);
  g_test_add_func("/bridge/not-allowed", test_not_allowed);
  g_test_add_func("/bridge/unknown-surface", test_unknown_surface);
  g_test_add_func("/bridge/missing-param", test_missing_param);
  g_test_add_func("/bridge/wrong-type", test_wrong_type);
  g_test_add_func("/bridge/out-of-range", test_out_of_range);
  g_test_add_func("/bridge/unknown-param", test_unknown_param);
  g_test_add_func("/bridge/oversize", test_oversize);
  g_test_add_func("/bridge/too-long", test_too_long);
  g_test_add_func("/bridge/enum", test_enum);
  g_test_add_func("/bridge/value-checks", test_value_checks);
  g_test_add_func("/bridge/valid", test_valid_calls);
  g_test_add_func("/bridge/normalisation", test_normalisation);
  return g_test_run();
}
