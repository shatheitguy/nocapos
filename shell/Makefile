# NoCap Shell native programs: nocap-shell (host) and nocap-lock.
#   make                 build both
#   make test            build and run the bridge unit tests (glib + json-glib only)
#   make install DESTDIR= PREFIX=/usr/local/lib/nocapos/shell

PKG_CONFIG ?= pkg-config
CC ?= cc
PREFIX ?= /usr/local/lib/nocapos/shell
DESTDIR ?=
BUILD ?= build
NOCAP_VERSION ?= $(shell cat ../VERSION 2>/dev/null || echo dev)

HOST_PKGS := gtk+-3.0 gtk-layer-shell-0 webkit2gtk-4.1 libsoup-3.0 json-glib-1.0 gio-unix-2.0 wayland-client polkit-agent-1
LOCK_PKGS := wayland-client xkbcommon cairo pangocairo gdk-pixbuf-2.0
TEST_PKGS := glib-2.0 json-glib-1.0

WARN := -Wall -Wextra -Wno-unused-parameter -Wno-missing-field-initializers -Wno-deprecated-declarations
HARDEN := -fstack-protector-strong -D_FORTIFY_SOURCE=2 -fPIE
CFLAGS ?= -O2 -g
ALL_CFLAGS := -std=gnu11 -D_GNU_SOURCE $(WARN) $(HARDEN) $(CFLAGS) -DNOCAP_VERSION='"$(NOCAP_VERSION)"'
ALL_LDFLAGS := -pie -Wl,-z,relro -Wl,-z,now -Wl,--as-needed $(LDFLAGS)

WAYLAND_SCANNER := $(shell $(PKG_CONFIG) --variable=wayland_scanner wayland-scanner 2>/dev/null)
ifeq ($(WAYLAND_SCANNER),)
WAYLAND_SCANNER := wayland-scanner
endif
WL_PROTOCOLS_DIR := $(shell $(PKG_CONFIG) --variable=pkgdatadir wayland-protocols 2>/dev/null)
SESSION_LOCK_XML := $(firstword $(wildcard $(WL_PROTOCOLS_DIR)/staging/ext-session-lock/ext-session-lock-v1.xml) protocols/ext-session-lock-v1.xml)
TOPLEVEL_XML := protocols/wlr-foreign-toplevel-management-unstable-v1.xml

PROTO := $(BUILD)/proto
TOPLEVEL_H := $(PROTO)/wlr-foreign-toplevel-management-unstable-v1-client-protocol.h
TOPLEVEL_C := $(PROTO)/wlr-foreign-toplevel-management-unstable-v1-protocol.c
LOCK_H := $(PROTO)/ext-session-lock-v1-client-protocol.h
LOCK_C := $(PROTO)/ext-session-lock-v1-protocol.c

HOST_SRC := $(wildcard host/*.c)
LOCK_SRC := $(wildcard lock/*.c)
HOST_OBJ := $(patsubst host/%.c,$(BUILD)/host/%.o,$(HOST_SRC)) $(BUILD)/proto/toplevel-protocol.o
LOCK_OBJ := $(patsubst lock/%.c,$(BUILD)/lock/%.o,$(LOCK_SRC)) $(BUILD)/proto/lock-protocol.o

.PHONY: all test install clean

all: $(BUILD)/nocap-shell $(BUILD)/nocap-lock

$(PROTO):
	mkdir -p $@

$(TOPLEVEL_H): $(TOPLEVEL_XML) | $(PROTO)
	$(WAYLAND_SCANNER) client-header $< $@
$(TOPLEVEL_C): $(TOPLEVEL_XML) | $(PROTO)
	$(WAYLAND_SCANNER) private-code $< $@
$(LOCK_H): $(SESSION_LOCK_XML) | $(PROTO)
	$(WAYLAND_SCANNER) client-header $< $@
$(LOCK_C): $(SESSION_LOCK_XML) | $(PROTO)
	$(WAYLAND_SCANNER) private-code $< $@

$(BUILD)/proto/toplevel-protocol.o: $(TOPLEVEL_C)
	$(CC) $(ALL_CFLAGS) $$($(PKG_CONFIG) --cflags wayland-client) -c $< -o $@
$(BUILD)/proto/lock-protocol.o: $(LOCK_C)
	$(CC) $(ALL_CFLAGS) $$($(PKG_CONFIG) --cflags wayland-client) -c $< -o $@

$(BUILD)/host/%.o: host/%.c host/*.h $(TOPLEVEL_H)
	@mkdir -p $(dir $@)
	$(CC) $(ALL_CFLAGS) -I$(PROTO) $$($(PKG_CONFIG) --cflags $(HOST_PKGS)) -c $< -o $@

$(BUILD)/lock/%.o: lock/%.c lock/*.h $(LOCK_H)
	@mkdir -p $(dir $@)
	$(CC) $(ALL_CFLAGS) -I$(PROTO) $$($(PKG_CONFIG) --cflags $(LOCK_PKGS)) -c $< -o $@

$(BUILD)/nocap-shell: $(HOST_OBJ)
	$(CC) $(ALL_LDFLAGS) -o $@ $^ $$($(PKG_CONFIG) --libs $(HOST_PKGS))

$(BUILD)/nocap-lock: $(LOCK_OBJ)
	$(CC) $(ALL_LDFLAGS) -o $@ $^ $$($(PKG_CONFIG) --libs $(LOCK_PKGS)) -lpam

$(BUILD)/test/test_bridge: test/test_bridge.c host/bridge.c host/bridge.h
	@mkdir -p $(dir $@)
	$(CC) $(ALL_CFLAGS) $(ALL_LDFLAGS) -o $@ test/test_bridge.c host/bridge.c \
		$$($(PKG_CONFIG) --cflags --libs $(TEST_PKGS))

test: $(BUILD)/test/test_bridge
	$(BUILD)/test/test_bridge

install: all
	install -d $(DESTDIR)$(PREFIX)
	install -m 0755 $(BUILD)/nocap-shell $(DESTDIR)$(PREFIX)/nocap-shell
	install -m 0755 $(BUILD)/nocap-lock $(DESTDIR)$(PREFIX)/nocap-lock

clean:
	rm -rf $(BUILD)
