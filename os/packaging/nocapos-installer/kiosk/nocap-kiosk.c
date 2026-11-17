/*
 * nocap-kiosk: one full-screen web view, nothing else. The NoCapOS USB
 * stick's setup runs in it (cage -s -- nocap-kiosk URL): no address bar,
 * no menus, no way out but the setup's own buttons.
 *
 *   nocap-kiosk URL
 *
 * Built against WebKitGTK 4.1 and GTK 3 (os/packaging/build-debs.sh).
 */
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

static gboolean on_context_menu(WebKitWebView *v, WebKitContextMenu *m, GdkEvent *e, WebKitHitTestResult *h, gpointer d) {
    return TRUE; /* no right-click menu */
}

static gboolean on_decide_policy(WebKitWebView *v, WebKitPolicyDecision *dec, WebKitPolicyDecisionType type, gpointer origin) {
    if (type == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) {
        webkit_policy_decision_ignore(dec); /* no pop-up windows */
        return TRUE;
    }
    if (type == WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION) {
        WebKitNavigationAction *a = webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(dec));
        const char *uri = webkit_uri_request_get_uri(webkit_navigation_action_get_request(a));
        if (uri == NULL || !g_str_has_prefix(uri, (const char *)origin)) {
            webkit_policy_decision_ignore(dec); /* stays on the setup */
            return TRUE;
        }
    }
    return FALSE;
}

static gboolean on_load_failed(WebKitWebView *v, WebKitLoadEvent ev, const char *uri, GError *err, gpointer d) {
    /* The setup's server may still be starting: try again shortly. */
    g_timeout_add_seconds(1, (GSourceFunc)webkit_web_view_reload, v);
    return TRUE;
}

int main(int argc, char **argv) {
    gtk_init(&argc, &argv);
    const char *url = argc > 1 ? argv[1] : "http://127.0.0.1:8790/installer";

    /* Only this origin may be navigated to. */
    gchar *origin = NULL;
    const char *p = strstr(url, "://");
    if (p != NULL) {
        const char *slash = strchr(p + 3, '/');
        origin = slash ? g_strndup(url, slash - url) : g_strdup(url);
    } else {
        origin = g_strdup(url);
    }

    GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(win), "NoCapOS");
    gtk_window_set_decorated(GTK_WINDOW(win), FALSE);
    GdkRGBA black = {0.03, 0.03, 0.04, 1.0};

    WebKitSettings *settings = webkit_settings_new();
    webkit_settings_set_enable_developer_extras(settings, FALSE);
    webkit_settings_set_enable_back_forward_navigation_gestures(settings, FALSE);
    webkit_settings_set_hardware_acceleration_policy(settings, WEBKIT_HARDWARE_ACCELERATION_POLICY_NEVER);
    WebKitWebView *view = WEBKIT_WEB_VIEW(webkit_web_view_new_with_settings(settings));
    webkit_web_view_set_background_color(view, &black);
    g_signal_connect(view, "context-menu", G_CALLBACK(on_context_menu), NULL);
    g_signal_connect(view, "decide-policy", G_CALLBACK(on_decide_policy), origin);
    g_signal_connect(view, "load-failed", G_CALLBACK(on_load_failed), NULL);

    gtk_container_add(GTK_CONTAINER(win), GTK_WIDGET(view));
    g_signal_connect(win, "destroy", G_CALLBACK(gtk_main_quit), NULL);
    gtk_window_fullscreen(GTK_WINDOW(win));
    gtk_widget_show_all(win);
    webkit_web_view_load_uri(view, url);
    gtk_main();
    g_free(origin);
    return 0;
}
