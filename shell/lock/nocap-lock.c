/* nocap-lock: NoCap lock screen (ext-session-lock-v1 client).
 *
 * Exit codes: 0 unlocked, 1 lock refused/finished or no display,
 * 2 compositor lacks ext_session_lock_manager_v1, 3 already running. */
#include <cairo.h>
#include <errno.h>
#include <fcntl.h>
#include <gdk-pixbuf/gdk-pixbuf.h>
#include <locale.h>
#include <pango/pangocairo.h>
#include <poll.h>
#include <pwd.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
#include <wayland-client.h>
#include <xkbcommon/xkbcommon.h>

#include "auth.h"
#include "ext-session-lock-v1-client-protocol.h"

#define PW_MAX 512
#define DEFAULT_WALLPAPER "/usr/share/backgrounds/nocapos/tide.jpg"

struct output {
	struct wl_list link;
	uint32_t name;
	struct wl_output *wl_output;
	struct wl_surface *surface;
	struct ext_session_lock_surface_v1 *lock_surface;
	int32_t scale;
	uint32_t width, height; /* logical, from configure */
	bool configured;
};

struct seat {
	struct wl_list link;
	uint32_t name;
	struct wl_seat *wl_seat;
	struct wl_keyboard *keyboard;
	struct xkb_keymap *keymap;
	struct xkb_state *xkb;
};

struct buffer {
	struct wl_buffer *wl_buffer;
	void *data;
	size_t size;
};

enum hint { HINT_NONE, HINT_WRONG, HINT_CHECKING, HINT_ERROR };

static struct {
	struct wl_display *display;
	struct wl_registry *registry;
	struct wl_compositor *compositor;
	struct wl_shm *shm;
	struct ext_session_lock_manager_v1 *manager;
	struct ext_session_lock_v1 *lock;
	struct wl_list outputs;
	struct wl_list seats;
	struct xkb_context *xkb_ctx;

	bool locked;
	bool done;
	int exit_code;

	char password[PW_MAX];
	size_t pw_len;
	size_t shown_dots; /* dots kept visible while checking */
	bool caps;
	enum hint hint;

	int auth_fd;
	pid_t auth_pid;
	int64_t fail_until_ms;

	const char *user;
	char real_name[256];
	cairo_surface_t *wallpaper;
} st = { .auth_fd = -1 };

static int64_t now_ms(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static void clear_password(void)
{
	explicit_bzero(st.password, sizeof(st.password));
	st.pw_len = 0;
}

/* ---- wallpaper ---------------------------------------------------------- */

static cairo_surface_t *pixbuf_to_surface(GdkPixbuf *pb)
{
	int w = gdk_pixbuf_get_width(pb), h = gdk_pixbuf_get_height(pb);
	int nch = gdk_pixbuf_get_n_channels(pb);
	int sstride = gdk_pixbuf_get_rowstride(pb);
	bool alpha = gdk_pixbuf_get_has_alpha(pb);

	if (gdk_pixbuf_get_bits_per_sample(pb) != 8 || nch < 3)
		return NULL;
	cairo_surface_t *s = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, w, h);
	if (cairo_surface_status(s) != CAIRO_STATUS_SUCCESS) {
		cairo_surface_destroy(s);
		return NULL;
	}
	cairo_surface_flush(s);
	const guchar *src = gdk_pixbuf_read_pixels(pb);
	unsigned char *dst = cairo_image_surface_get_data(s);
	int dstride = cairo_image_surface_get_stride(s);
	for (int y = 0; y < h; y++) {
		const guchar *sp = src + (size_t)y * sstride;
		uint32_t *dp = (uint32_t *)(void *)(dst + (size_t)y * dstride);
		for (int x = 0; x < w; x++, sp += nch) {
			uint32_t a = alpha ? sp[3] : 255;
			uint32_t r = sp[0] * a / 255, g = sp[1] * a / 255, b = sp[2] * a / 255;
			dp[x] = (a << 24) | (r << 16) | (g << 8) | b;
		}
	}
	cairo_surface_mark_dirty(s);
	return s;
}

static void load_wallpaper(void)
{
	const char *path = getenv("NOCAP_WALLPAPER");
	if (!path || !*path)
		path = DEFAULT_WALLPAPER;
	GError *err = NULL;
	GdkPixbuf *pb = gdk_pixbuf_new_from_file(path, &err);
	if (!pb) {
		if (err)
			g_error_free(err);
		return;
	}
	GdkPixbuf *rot = gdk_pixbuf_apply_embedded_orientation(pb);
	g_object_unref(pb);
	if (!rot)
		return;
	st.wallpaper = pixbuf_to_surface(rot);
	g_object_unref(rot);
}

/* ---- shm buffers -------------------------------------------------------- */

static int create_shm_fd(size_t size)
{
	int fd = -1;
#ifdef MFD_CLOEXEC
	fd = memfd_create("nocap-lock", MFD_CLOEXEC);
#endif
	if (fd < 0) {
		char name[64];
		for (int i = 0; i < 16 && fd < 0; i++) {
			snprintf(name, sizeof(name), "/nocap-lock-%ld-%ld",
				 (long)getpid(), (long)(now_ms() + i * 7919));
			fd = shm_open(name, O_RDWR | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
			if (fd >= 0)
				shm_unlink(name);
			else if (errno != EEXIST)
				break;
		}
	}
	if (fd < 0)
		return -1;
	int rc;
	do {
		rc = ftruncate(fd, (off_t)size);
	} while (rc < 0 && errno == EINTR);
	if (rc < 0) {
		close(fd);
		return -1;
	}
	return fd;
}

static void buffer_release(void *data, struct wl_buffer *wl_buffer)
{
	struct buffer *b = data;
	wl_buffer_destroy(wl_buffer);
	munmap(b->data, b->size);
	free(b);
}

static const struct wl_buffer_listener buffer_listener = {
	.release = buffer_release,
};

static struct buffer *buffer_create(int w, int h)
{
	int stride = w * 4;
	size_t size = (size_t)stride * (size_t)h;
	int fd = create_shm_fd(size);
	if (fd < 0)
		return NULL;
	void *data = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	if (data == MAP_FAILED) {
		close(fd);
		return NULL;
	}
	struct buffer *b = calloc(1, sizeof(*b));
	if (!b) {
		munmap(data, size);
		close(fd);
		return NULL;
	}
	struct wl_shm_pool *pool = wl_shm_create_pool(st.shm, fd, (int32_t)size);
	b->wl_buffer = wl_shm_pool_create_buffer(pool, 0, w, h, stride,
						 WL_SHM_FORMAT_ARGB8888);
	wl_shm_pool_destroy(pool);
	close(fd);
	b->data = data;
	b->size = size;
	wl_buffer_add_listener(b->wl_buffer, &buffer_listener, b);
	return b;
}

/* ---- drawing ------------------------------------------------------------ */

static void rounded_rect(cairo_t *cr, double x, double y, double w, double h, double r)
{
	cairo_new_sub_path(cr);
	cairo_arc(cr, x + w - r, y + r, r, -G_PI / 2, 0);
	cairo_arc(cr, x + w - r, y + h - r, r, 0, G_PI / 2);
	cairo_arc(cr, x + r, y + h - r, r, G_PI / 2, G_PI);
	cairo_arc(cr, x + r, y + r, r, G_PI, 3 * G_PI / 2);
	cairo_close_path(cr);
}

/* Draw text centred horizontally at y (top). Returns the text height. */
static double draw_text(cairo_t *cr, double cx, double y, const char *text,
			const char *family, int weight, double size,
			double r, double g, double b, double a)
{
	PangoLayout *layout = pango_cairo_create_layout(cr);
	PangoFontDescription *fd = pango_font_description_from_string(family);
	pango_font_description_set_weight(fd, (PangoWeight)weight);
	pango_font_description_set_absolute_size(fd, size * PANGO_SCALE);
	pango_layout_set_font_description(layout, fd);
	pango_layout_set_text(layout, text, -1);
	int tw, th;
	pango_layout_get_pixel_size(layout, &tw, &th);
	cairo_set_source_rgba(cr, r, g, b, a);
	cairo_move_to(cr, cx - tw / 2.0, y);
	pango_cairo_show_layout(cr, layout);
	pango_font_description_free(fd);
	g_object_unref(layout);
	return th;
}

static size_t utf8_count(const char *s, size_t len)
{
	size_t n = 0;
	for (size_t i = 0; i < len; i++)
		if (((unsigned char)s[i] & 0xC0) != 0x80)
			n++;
	return n;
}

static void draw(cairo_t *cr, int pw, int ph, int scale)
{
	/* background */
	cairo_set_source_rgb(cr, 5 / 255.0, 5 / 255.0, 8 / 255.0);
	cairo_paint(cr);
	if (st.wallpaper) {
		int iw = cairo_image_surface_get_width(st.wallpaper);
		int ih = cairo_image_surface_get_height(st.wallpaper);
		if (iw > 0 && ih > 0) {
			double s = MAX((double)pw / iw, (double)ph / ih);
			cairo_save(cr);
			cairo_translate(cr, (pw - iw * s) / 2.0, (ph - ih * s) / 2.0);
			cairo_scale(cr, s, s);
			cairo_set_source_surface(cr, st.wallpaper, 0, 0);
			cairo_pattern_set_filter(cairo_get_source(cr), CAIRO_FILTER_GOOD);
			cairo_paint(cr);
			cairo_restore(cr);
		}
		/* darken for legibility */
		cairo_pattern_t *grad = cairo_pattern_create_linear(0, 0, 0, ph);
		cairo_pattern_add_color_stop_rgba(grad, 0, 5 / 255.0, 5 / 255.0, 8 / 255.0, 0.35);
		cairo_pattern_add_color_stop_rgba(grad, 1, 5 / 255.0, 5 / 255.0, 8 / 255.0, 0.75);
		cairo_set_source(cr, grad);
		cairo_paint(cr);
		cairo_pattern_destroy(grad);
	}

	cairo_scale(cr, scale, scale);
	double w = (double)pw / scale, h = (double)ph / scale;
	double cx = w / 2.0;
	const char *family = "Inter, Sans";

	time_t t = time(NULL);
	struct tm tm;
	localtime_r(&t, &tm);
	char clock_buf[16], date_buf[128];
	strftime(clock_buf, sizeof(clock_buf), "%H:%M", &tm);
	if (strftime(date_buf, sizeof(date_buf), "%A, %-d %B", &tm) == 0)
		date_buf[0] = '\0';

	double clock_size = MIN(140.0, MAX(64.0, h * 0.14));
	double y = h * 0.22;
	y += draw_text(cr, cx, y, clock_buf, family, PANGO_WEIGHT_LIGHT, clock_size,
		       1, 1, 1, 0.96);
	draw_text(cr, cx, y, date_buf, family, PANGO_WEIGHT_NORMAL, 22, 1, 1, 1, 0.72);

	/* user + password pill */
	double pill_w = MIN(340.0, w - 48.0), pill_h = 48.0;
	double py = h * 0.62;
	draw_text(cr, cx, py - 40, st.real_name, family, PANGO_WEIGHT_SEMIBOLD, 20,
		  1, 1, 1, 0.95);

	double px = cx - pill_w / 2.0;
	rounded_rect(cr, px, py, pill_w, pill_h, pill_h / 2.0);
	cairo_set_source_rgba(cr, 1, 1, 1, 0.10);
	cairo_fill_preserve(cr);
	cairo_set_line_width(cr, 2.0);
	if (st.hint == HINT_CHECKING)
		cairo_set_source_rgba(cr, 1, 1, 1, 0.45);
	else
		cairo_set_source_rgb(cr, 0xe5 / 255.0, 0x38 / 255.0, 0x3b / 255.0);
	cairo_stroke(cr);

	size_t dots = st.hint == HINT_CHECKING ? st.shown_dots
					       : utf8_count(st.password, st.pw_len);
	if (dots == 0) {
		draw_text(cr, cx, py + 13, "Password", family, PANGO_WEIGHT_NORMAL, 17,
			  1, 1, 1, 0.40);
	} else {
		double spacing = 16.0, radius = 5.0;
		size_t max_dots = (size_t)((pill_w - 48.0) / spacing);
		if (dots > max_dots)
			dots = max_dots;
		double total = (dots - 1) * spacing;
		cairo_set_source_rgba(cr, 1, 1, 1, 0.92);
		for (size_t i = 0; i < dots; i++) {
			cairo_new_sub_path(cr);
			cairo_arc(cr, cx - total / 2.0 + i * spacing, py + pill_h / 2.0,
				  radius, 0, 2 * G_PI);
		}
		cairo_fill(cr);
	}

	/* hint line */
	double hy = py + pill_h + 16;
	switch (st.hint) {
	case HINT_WRONG:
		draw_text(cr, cx, hy, "Wrong password", family, PANGO_WEIGHT_SEMIBOLD, 16,
			  0xff / 255.0, 0x6b / 255.0, 0x6e / 255.0, 1);
		break;
	case HINT_ERROR:
		draw_text(cr, cx, hy, "Could not check the password", family,
			  PANGO_WEIGHT_SEMIBOLD, 16, 0xff / 255.0, 0x6b / 255.0,
			  0x6e / 255.0, 1);
		break;
	case HINT_CHECKING:
		draw_text(cr, cx, hy, "Checking\xe2\x80\xa6", family, PANGO_WEIGHT_NORMAL,
			  16, 1, 1, 1, 0.6);
		break;
	case HINT_NONE:
		if (st.caps)
			draw_text(cr, cx, hy, "Caps Lock is on", family,
				  PANGO_WEIGHT_SEMIBOLD, 16, 1, 0.78, 0.35, 1);
		else
			draw_text(cr, cx, hy, "Enter your password to unlock", family,
				  PANGO_WEIGHT_NORMAL, 15, 1, 1, 1, 0.5);
		break;
	}
	if (st.hint != HINT_NONE && st.caps)
		draw_text(cr, cx, hy + 24, "Caps Lock is on", family,
			  PANGO_WEIGHT_SEMIBOLD, 15, 1, 0.78, 0.35, 1);
}

static void render_output(struct output *o)
{
	if (!o->configured || !o->surface || o->width == 0 || o->height == 0)
		return;
	int scale = o->scale > 0 ? o->scale : 1;
	int pw = (int)o->width * scale, ph = (int)o->height * scale;
	if (pw <= 0 || ph <= 0 || pw > 16384 || ph > 16384)
		return;
	struct buffer *b = buffer_create(pw, ph);
	if (!b)
		return;
	cairo_surface_t *cs = cairo_image_surface_create_for_data(
		b->data, CAIRO_FORMAT_ARGB32, pw, ph, pw * 4);
	cairo_t *cr = cairo_create(cs);
	draw(cr, pw, ph, scale);
	cairo_destroy(cr);
	cairo_surface_flush(cs);
	cairo_surface_destroy(cs);

	wl_surface_set_buffer_scale(o->surface, scale);
	wl_surface_attach(o->surface, b->wl_buffer, 0, 0);
	wl_surface_damage(o->surface, 0, 0, INT32_MAX, INT32_MAX);
	wl_surface_commit(o->surface);
}

static void render_all(void)
{
	struct output *o;
	wl_list_for_each(o, &st.outputs, link)
		render_output(o);
}

/* ---- lock surfaces ------------------------------------------------------ */

static void lock_surface_configure(void *data, struct ext_session_lock_surface_v1 *ls,
				   uint32_t serial, uint32_t width, uint32_t height)
{
	struct output *o = data;
	o->width = width;
	o->height = height;
	o->configured = true;
	ext_session_lock_surface_v1_ack_configure(ls, serial);
	render_output(o);
}

static const struct ext_session_lock_surface_v1_listener lock_surface_listener = {
	.configure = lock_surface_configure,
};

static void output_create_lock_surface(struct output *o)
{
	if (!st.lock || o->lock_surface)
		return;
	o->surface = wl_compositor_create_surface(st.compositor);
	o->lock_surface = ext_session_lock_v1_get_lock_surface(st.lock, o->surface,
								o->wl_output);
	ext_session_lock_surface_v1_add_listener(o->lock_surface,
						 &lock_surface_listener, o);
}

static void output_destroy(struct output *o)
{
	if (o->lock_surface)
		ext_session_lock_surface_v1_destroy(o->lock_surface);
	if (o->surface)
		wl_surface_destroy(o->surface);
	if (wl_output_get_version(o->wl_output) >= WL_OUTPUT_RELEASE_SINCE_VERSION)
		wl_output_release(o->wl_output);
	else
		wl_output_destroy(o->wl_output);
	wl_list_remove(&o->link);
	free(o);
}

static void lock_locked(void *data, struct ext_session_lock_v1 *lock)
{
	(void)data;
	(void)lock;
	st.locked = true;
	printf("nocap-lock: locked\n");
	fflush(stdout);
}

static void lock_finished(void *data, struct ext_session_lock_v1 *lock)
{
	(void)data;
	(void)lock;
	fprintf(stderr, "nocap-lock: the compositor refused or ended the lock\n");
	st.exit_code = 1;
	st.done = true;
}

static const struct ext_session_lock_v1_listener lock_listener = {
	.locked = lock_locked,
	.finished = lock_finished,
};

/* ---- output ------------------------------------------------------------- */

static void output_geometry(void *data, struct wl_output *wl_output, int32_t x,
			    int32_t y, int32_t pw, int32_t ph, int32_t subpixel,
			    const char *make, const char *model, int32_t transform)
{
	(void)data; (void)wl_output; (void)x; (void)y; (void)pw; (void)ph;
	(void)subpixel; (void)make; (void)model; (void)transform;
}

static void output_mode(void *data, struct wl_output *wl_output, uint32_t flags,
			int32_t w, int32_t h, int32_t refresh)
{
	(void)data; (void)wl_output; (void)flags; (void)w; (void)h; (void)refresh;
}

static void output_done(void *data, struct wl_output *wl_output)
{
	(void)wl_output;
	render_output(data);
}

static void output_scale(void *data, struct wl_output *wl_output, int32_t factor)
{
	(void)wl_output;
	struct output *o = data;
	o->scale = factor > 0 && factor <= 8 ? factor : 1;
}

static const struct wl_output_listener output_listener = {
	.geometry = output_geometry,
	.mode = output_mode,
	.done = output_done,
	.scale = output_scale,
};

/* ---- keyboard ----------------------------------------------------------- */

static void submit(void)
{
	if (!st.locked || st.auth_fd >= 0 || st.pw_len == 0)
		return;
	if (now_ms() < st.fail_until_ms)
		return;
	st.auth_fd = auth_start(st.user, st.password, st.pw_len, &st.auth_pid);
	st.shown_dots = utf8_count(st.password, st.pw_len);
	clear_password();
	st.hint = st.auth_fd >= 0 ? HINT_CHECKING : HINT_ERROR;
}

static void handle_key(struct seat *s, uint32_t key)
{
	if (st.auth_fd >= 0)
		return; /* ignore typing while checking */
	xkb_keycode_t code = key + 8;
	xkb_keysym_t sym = xkb_state_key_get_one_sym(s->xkb, code);

	switch (sym) {
	case XKB_KEY_Escape:
		clear_password();
		break;
	case XKB_KEY_BackSpace:
		while (st.pw_len > 0) {
			unsigned char c = (unsigned char)st.password[--st.pw_len];
			st.password[st.pw_len] = '\0';
			if ((c & 0xC0) != 0x80)
				break;
		}
		break;
	case XKB_KEY_Return:
	case XKB_KEY_KP_Enter:
		submit();
		break;
	default: {
		if (xkb_state_mod_name_is_active(s->xkb, XKB_MOD_NAME_CTRL,
						 XKB_STATE_MODS_EFFECTIVE) > 0) {
			if (sym == XKB_KEY_u || sym == XKB_KEY_U)
				clear_password();
			break;
		}
		char buf[64];
		int n = xkb_state_key_get_utf8(s->xkb, code, buf, sizeof(buf));
		if (n <= 0 || (size_t)n >= sizeof(buf))
			break;
		if ((unsigned char)buf[0] < 0x20 || buf[0] == 0x7f)
			break;
		if (st.pw_len + (size_t)n < PW_MAX) {
			memcpy(st.password + st.pw_len, buf, (size_t)n);
			st.pw_len += (size_t)n;
		}
		explicit_bzero(buf, sizeof(buf));
		break;
	}
	}
	if (st.hint == HINT_WRONG || st.hint == HINT_ERROR)
		st.hint = HINT_NONE;
	render_all();
}

static void kb_keymap(void *data, struct wl_keyboard *kb, uint32_t format,
		      int32_t fd, uint32_t size)
{
	(void)kb;
	struct seat *s = data;
	if (format != WL_KEYBOARD_KEYMAP_FORMAT_XKB_V1 || size == 0) {
		close(fd);
		return;
	}
	char *map = mmap(NULL, size, PROT_READ, MAP_PRIVATE, fd, 0);
	close(fd);
	if (map == MAP_FAILED)
		return;
	struct xkb_keymap *km = xkb_keymap_new_from_buffer(
		st.xkb_ctx, map, strnlen(map, size), XKB_KEYMAP_FORMAT_TEXT_V1,
		XKB_KEYMAP_COMPILE_NO_FLAGS);
	munmap(map, size);
	if (!km)
		return;
	struct xkb_state *xs = xkb_state_new(km);
	if (!xs) {
		xkb_keymap_unref(km);
		return;
	}
	xkb_state_unref(s->xkb);
	xkb_keymap_unref(s->keymap);
	s->keymap = km;
	s->xkb = xs;
}

static void kb_enter(void *data, struct wl_keyboard *kb, uint32_t serial,
		     struct wl_surface *surface, struct wl_array *keys)
{
	(void)data; (void)kb; (void)serial; (void)surface; (void)keys;
}

static void kb_leave(void *data, struct wl_keyboard *kb, uint32_t serial,
		     struct wl_surface *surface)
{
	(void)data; (void)kb; (void)serial; (void)surface;
}

static void kb_key(void *data, struct wl_keyboard *kb, uint32_t serial,
		   uint32_t time, uint32_t key, uint32_t state)
{
	(void)kb; (void)serial; (void)time;
	struct seat *s = data;
	if (s->xkb && state == WL_KEYBOARD_KEY_STATE_PRESSED)
		handle_key(s, key);
}

static void kb_modifiers(void *data, struct wl_keyboard *kb, uint32_t serial,
			 uint32_t depressed, uint32_t latched, uint32_t locked,
			 uint32_t group)
{
	(void)kb; (void)serial;
	struct seat *s = data;
	if (!s->xkb)
		return;
	xkb_state_update_mask(s->xkb, depressed, latched, locked, 0, 0, group);
	bool caps = xkb_state_mod_name_is_active(s->xkb, XKB_MOD_NAME_CAPS,
						 XKB_STATE_MODS_LOCKED) > 0;
	if (caps != st.caps) {
		st.caps = caps;
		render_all();
	}
}

static void kb_repeat_info(void *data, struct wl_keyboard *kb, int32_t rate,
			   int32_t delay)
{
	(void)data; (void)kb; (void)rate; (void)delay;
}

static const struct wl_keyboard_listener keyboard_listener = {
	.keymap = kb_keymap,
	.enter = kb_enter,
	.leave = kb_leave,
	.key = kb_key,
	.modifiers = kb_modifiers,
	.repeat_info = kb_repeat_info,
};

static void seat_release_keyboard(struct seat *s)
{
	if (!s->keyboard)
		return;
	if (wl_keyboard_get_version(s->keyboard) >= WL_KEYBOARD_RELEASE_SINCE_VERSION)
		wl_keyboard_release(s->keyboard);
	else
		wl_keyboard_destroy(s->keyboard);
	s->keyboard = NULL;
}

static void seat_capabilities(void *data, struct wl_seat *wl_seat, uint32_t caps)
{
	struct seat *s = data;
	if ((caps & WL_SEAT_CAPABILITY_KEYBOARD) && !s->keyboard) {
		s->keyboard = wl_seat_get_keyboard(wl_seat);
		wl_keyboard_add_listener(s->keyboard, &keyboard_listener, s);
	} else if (!(caps & WL_SEAT_CAPABILITY_KEYBOARD)) {
		seat_release_keyboard(s);
	}
}

static void seat_name(void *data, struct wl_seat *wl_seat, const char *name)
{
	(void)data; (void)wl_seat; (void)name;
}

static const struct wl_seat_listener seat_listener = {
	.capabilities = seat_capabilities,
	.name = seat_name,
};

static void seat_destroy(struct seat *s)
{
	seat_release_keyboard(s);
	xkb_state_unref(s->xkb);
	xkb_keymap_unref(s->keymap);
	if (wl_seat_get_version(s->wl_seat) >= WL_SEAT_RELEASE_SINCE_VERSION)
		wl_seat_release(s->wl_seat);
	else
		wl_seat_destroy(s->wl_seat);
	wl_list_remove(&s->link);
	free(s);
}

/* ---- registry ----------------------------------------------------------- */

static uint32_t min_u32(uint32_t a, uint32_t b)
{
	return a < b ? a : b;
}

static void registry_global(void *data, struct wl_registry *reg, uint32_t name,
			    const char *iface, uint32_t version)
{
	(void)data;
	if (strcmp(iface, wl_compositor_interface.name) == 0) {
		st.compositor = wl_registry_bind(reg, name, &wl_compositor_interface,
						 min_u32(version, 4));
	} else if (strcmp(iface, wl_shm_interface.name) == 0) {
		st.shm = wl_registry_bind(reg, name, &wl_shm_interface, 1);
	} else if (strcmp(iface, ext_session_lock_manager_v1_interface.name) == 0) {
		st.manager = wl_registry_bind(reg, name,
					      &ext_session_lock_manager_v1_interface, 1);
	} else if (strcmp(iface, wl_output_interface.name) == 0) {
		struct output *o = calloc(1, sizeof(*o));
		if (!o)
			return;
		o->name = name;
		o->scale = 1;
		o->wl_output = wl_registry_bind(reg, name, &wl_output_interface,
						min_u32(version, 3));
		wl_output_add_listener(o->wl_output, &output_listener, o);
		wl_list_insert(&st.outputs, &o->link);
		output_create_lock_surface(o);
	} else if (strcmp(iface, wl_seat_interface.name) == 0) {
		struct seat *s = calloc(1, sizeof(*s));
		if (!s)
			return;
		s->name = name;
		s->wl_seat = wl_registry_bind(reg, name, &wl_seat_interface,
					      min_u32(version, 5));
		wl_seat_add_listener(s->wl_seat, &seat_listener, s);
		wl_list_insert(&st.seats, &s->link);
	}
}

static void registry_global_remove(void *data, struct wl_registry *reg, uint32_t name)
{
	(void)data; (void)reg;
	struct output *o, *otmp;
	wl_list_for_each_safe(o, otmp, &st.outputs, link) {
		if (o->name == name) {
			output_destroy(o);
			return;
		}
	}
	struct seat *s, *stmp;
	wl_list_for_each_safe(s, stmp, &st.seats, link) {
		if (s->name == name) {
			seat_destroy(s);
			return;
		}
	}
}

static const struct wl_registry_listener registry_listener = {
	.global = registry_global,
	.global_remove = registry_global_remove,
};

/* ---- setup -------------------------------------------------------------- */

static int single_instance(void)
{
	const char *dir = getenv("XDG_RUNTIME_DIR");
	if (!dir || dir[0] != '/')
		return 0; /* no safe place for the lock file; carry on */
	char path[4096];
	if ((size_t)snprintf(path, sizeof(path), "%s/nocap-lock.pid", dir) >= sizeof(path))
		return 0;
	int fd = open(path, O_RDWR | O_CREAT | O_CLOEXEC | O_NOFOLLOW, 0600);
	if (fd < 0)
		return 0;
	if (flock(fd, LOCK_EX | LOCK_NB) < 0) {
		close(fd);
		return errno == EWOULDBLOCK ? -1 : 0;
	}
	if (ftruncate(fd, 0) == 0)
		dprintf(fd, "%ld\n", (long)getpid());
	return 0; /* fd stays open (and locked) for the process lifetime */
}

static void load_user(void)
{
	struct passwd *pw = getpwuid(getuid());
	if (!pw || !pw->pw_name) {
		fprintf(stderr, "nocap-lock: cannot determine the current user\n");
		exit(1);
	}
	st.user = strdup(pw->pw_name);
	if (!st.user)
		exit(1);
	const char *gecos = pw->pw_gecos ? pw->pw_gecos : "";
	size_t n = strcspn(gecos, ",");
	if (n == 0 || n >= sizeof(st.real_name)) {
		snprintf(st.real_name, sizeof(st.real_name), "%s", st.user);
	} else {
		memcpy(st.real_name, gecos, n);
		st.real_name[n] = '\0';
	}
}

static int next_timeout(void)
{
	time_t t = time(NULL);
	struct tm tm;
	localtime_r(&t, &tm);
	int64_t ms = (int64_t)(60 - tm.tm_sec) * 1000 + 50;
	if (st.fail_until_ms > 0) {
		int64_t left = st.fail_until_ms - now_ms();
		if (left > 0 && left < ms)
			ms = left + 5;
	}
	return (int)ms;
}

int main(int argc, char **argv)
{
	(void)argc;
	(void)argv;
	setlocale(LC_ALL, "");
	signal(SIGPIPE, SIG_IGN);

	if (single_instance() < 0) {
		fprintf(stderr, "nocap-lock: already running\n");
		return 3;
	}
	load_user();

	wl_list_init(&st.outputs);
	wl_list_init(&st.seats);

	st.display = wl_display_connect(NULL);
	if (!st.display) {
		fprintf(stderr, "nocap-lock: cannot connect to the Wayland display\n");
		return 1;
	}
	st.xkb_ctx = xkb_context_new(XKB_CONTEXT_NO_FLAGS);
	if (!st.xkb_ctx) {
		fprintf(stderr, "nocap-lock: cannot create the keyboard context\n");
		return 1;
	}
	st.registry = wl_display_get_registry(st.display);
	wl_registry_add_listener(st.registry, &registry_listener, NULL);
	if (wl_display_roundtrip(st.display) < 0) {
		fprintf(stderr, "nocap-lock: Wayland roundtrip failed\n");
		return 1;
	}
	if (!st.manager) {
		fprintf(stderr, "nocap-lock: the compositor does not support "
				"ext_session_lock_manager_v1\n");
		return 2;
	}
	if (!st.compositor || !st.shm) {
		fprintf(stderr, "nocap-lock: the compositor lacks wl_compositor or wl_shm\n");
		return 1;
	}

	load_wallpaper();

	st.lock = ext_session_lock_manager_v1_lock(st.manager);
	ext_session_lock_v1_add_listener(st.lock, &lock_listener, NULL);
	struct output *o;
	wl_list_for_each(o, &st.outputs, link)
		output_create_lock_surface(o);

	int wl_fd = wl_display_get_fd(st.display);
	time_t last_minute = time(NULL) / 60;
	bool dirty = false;
	while (!st.done) {
		while (wl_display_prepare_read(st.display) != 0) {
			if (wl_display_dispatch_pending(st.display) < 0)
				goto disconnected;
		}
		if (st.done) {
			wl_display_cancel_read(st.display);
			break;
		}
		if (wl_display_flush(st.display) < 0 && errno != EAGAIN) {
			wl_display_cancel_read(st.display);
			goto disconnected;
		}

		struct pollfd fds[2] = {
			{ .fd = wl_fd, .events = POLLIN },
			{ .fd = st.auth_fd, .events = POLLIN },
		};
		int rc = poll(fds, st.auth_fd >= 0 ? 2 : 1, next_timeout());
		if (rc < 0 && errno != EINTR) {
			wl_display_cancel_read(st.display);
			goto disconnected;
		}
		if (rc > 0 && (fds[0].revents & POLLIN)) {
			if (wl_display_read_events(st.display) < 0)
				goto disconnected;
		} else {
			wl_display_cancel_read(st.display);
			if (rc > 0 && (fds[0].revents & (POLLERR | POLLHUP)))
				goto disconnected;
		}
		if (wl_display_dispatch_pending(st.display) < 0)
			goto disconnected;

		if (st.auth_fd >= 0 && rc > 0 && (fds[1].revents & (POLLIN | POLLHUP | POLLERR))) {
			int ok = auth_finish(st.auth_fd, st.auth_pid);
			st.auth_fd = -1;
			st.shown_dots = 0;
			if (ok == 1) {
				ext_session_lock_v1_unlock_and_destroy(st.lock);
				st.lock = NULL;
				wl_display_roundtrip(st.display);
				return 0;
			}
			st.hint = ok == 0 ? HINT_WRONG : HINT_ERROR;
			st.fail_until_ms = now_ms() + 1000;
			dirty = true;
		}
		if (st.fail_until_ms > 0 && now_ms() >= st.fail_until_ms)
			st.fail_until_ms = 0;
		/* the clock changes once a minute; input redraws on its own */
		time_t minute = time(NULL) / 60;
		if (minute != last_minute) {
			last_minute = minute;
			dirty = true;
		}
		if (dirty) {
			dirty = false;
			render_all();
		}
	}
	clear_password();
	return st.exit_code;

disconnected:
	clear_password();
	fprintf(stderr, "nocap-lock: lost the Wayland connection\n");
	return 1;
}
