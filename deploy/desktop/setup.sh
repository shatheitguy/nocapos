# NoCap Desktop, the full desktop: sourced by install.sh after lib.sh. Uses
# install.sh's helpers (info, warn, has, pkg_*) and settings (BUNDLE_DIR,
# ETC, DATA, ARCH, BRANDING). Every file changed outside NoCapOS's own
# folders is recorded in $DESK_BACKUP, and uninstall-desktop.sh (run by
# uninstall.sh) puts it back.
# shellcheck shell=bash

DESK_SRC="$BUNDLE_DIR/desktop"
DESK_LIB=/usr/local/lib/nocapos/desktop
DESK_SHARE=/usr/share/nocapos/desktop
DESK_CONF="$ETC/desktop.conf"
DESK_BACKUP="$DATA/desktop-backup"
DESK_UNITS=/etc/systemd/system
BRAND_UUID="$NOCAP_BRAND_UUID"

desk_record() { mkdir -p "$DESK_BACKUP" && chmod 700 "$DESK_BACKUP" && printf '%s\n' "$*" >>"$DESK_BACKUP/edits"; }
desk_recorded() { grep -qsxF "$*" "$DESK_BACKUP/edits"; }

# desk_keep FILE: the first time NoCapOS changes FILE, keep the original
# (or note that there was none) so uninstall can put it back.
desk_keep() {
  local name
  name="$(printf '%s' "$1" | sed 's|/|%|g')"
  mkdir -p "$DESK_BACKUP/files"
  chmod 700 "$DESK_BACKUP"
  if [ -e "$DESK_BACKUP/files/$name" ] || [ -e "$DESK_BACKUP/files/$name.absent" ]; then return 0; fi
  if [ -e "$1" ]; then cp -pP "$1" "$DESK_BACKUP/files/$name"; else : >"$DESK_BACKUP/files/$name.absent"; fi
}

# Installs without the "recommended" extras (on apt: other distributions'
# wallpapers, themes and branding come in that way).
pkg_install_lean() {
  local mgr="$1"
  shift
  case "$mgr" in
    apt) DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a apt-get install -y -q --no-install-recommends \
      -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@" >/dev/null 2>&1 ;;
    *) pkg_install "$mgr" "$@" ;;
  esac
}

# Package installs mustn't start the sign-in screen on their own (it would
# take this console away halfway through): Debian's invoke-rc.d asks
# /usr/sbin/policy-rc.d, which says "not now" while it's in place.
desk_hold_services() {
  [ -e /usr/sbin/policy-rc.d ] && return 1
  printf '#!/bin/sh\n# NoCapOS install.sh: no service starts while the desktop installs.\nexit 101\n' >/usr/sbin/policy-rc.d
  chmod 755 /usr/sbin/policy-rc.d
}
desk_release_services() { if grep -qs 'NoCapOS install.sh' /usr/sbin/policy-rc.d; then rm -f /usr/sbin/policy-rc.d; fi; }

desk_install_packages() { # $1 = family, $2 = manager
  local line alt want=() missing=() skipped=() held=0
  # The same choice on every architecture (amd64, arm64, armhf): whatever
  # this distribution's repositories have, newer apps first.
  while IFS= read -r line; do
    case "$line" in
      '') ;;
      \!*) missing+=("${line#\!}") ;;
      \?*) skipped+=("${line#\?}") ;;
      *) want+=("$line") ;;
    esac
  done < <(
    desktop_packages "$1" | tr -d '?' | tr '|' '\n' | while IFS= read -r alt; do
      [ -n "$alt" ] && pkg_available "$2" "$alt" && echo "$alt"
    done | desktop_select "$1"
  )
  if [ "${#missing[@]}" -gt 0 ]; then
    warn "full desktop: these packages aren't available here: ${missing[*]}"
    return 1
  fi
  [ "${#skipped[@]}" -eq 0 ] || warn "full desktop: optional packages not available here, skipped: ${skipped[*]}"
  printf '    desktop: %s\n' "${want[*]}"
  desk_hold_services && held=1
  if ! pkg_install_lean "$2" "${want[@]}"; then
    local p
    for p in "${want[@]}"; do pkg_install_lean "$2" "$p" || warn "full desktop: couldn't install $p"; done
  fi
  [ "$held" -eq 0 ] || desk_release_services
  has gnome-shell || { warn "full desktop: GNOME Shell didn't install"; return 1; }
  [ -x /usr/sbin/gdm3 ] || [ -x /usr/sbin/gdm ] || has gdm || has gdm3 || { warn "full desktop: the sign-in screen (GDM) didn't install"; return 1; }
}

# The helpers uninstall.sh needs to undo what's recorded in $DESK_BACKUP.
desk_install_lib() {
  mkdir -p "$DESK_LIB"
  install -m 0644 "$DESK_SRC/lib.sh" "$DESK_LIB/lib.sh"
  install -m 0755 "$DESK_SRC/uninstall-desktop.sh" "$DESK_SRC/os-release-sync" "$DESK_SRC/nocapos-accent" "$DESK_LIB/"
}

desk_install_assets() {
  local s src="$DESK_SRC" brand="$BUNDLE_DIR/brand" wp="$BUNDLE_DIR/wallpapers"
  mkdir -p "$DESK_SHARE" "$DESK_LIB" /usr/share/backgrounds/nocapos /usr/share/gnome-background-properties /usr/local/share/applications
  install -m 0644 "$src/art/login-background.jpg" "$src/art/login-logo.png" "$DESK_SHARE/"
  install -m 0644 "$brand/mark-128.png" "$DESK_SHARE/logo.png"
  desk_install_lib
  install -m 0755 "$src/gdm-theme" "$src/remote-guard" "$DESK_LIB/"
  # Wallpapers: NoCapOS's own, also listed in Settings → Appearance.
  {
    echo '<?xml version="1.0" encoding="UTF-8"?>'
    echo '<!DOCTYPE wallpapers SYSTEM "gnome-wp-list.dtd">'
    echo '<wallpapers>'
    for s in "$wp"/*.jpg; do
      case "$s" in *-thumb.jpg) continue ;; esac
      [ -f "$s" ] || continue
      install -m 0644 "$s" /usr/share/backgrounds/nocapos/
      n="$(basename "$s" .jpg)"
      printf '  <wallpaper deleted="false"><name>NoCapOS %s</name><filename>/usr/share/backgrounds/nocapos/%s.jpg</filename><filename-dark>/usr/share/backgrounds/nocapos/%s.jpg</filename-dark><options>zoom</options><pcolor>#050508</pcolor><scolor>#050508</scolor></wallpaper>\n' "${n^}" "$n" "$n"
    done
    echo '</wallpapers>'
  } >/usr/share/gnome-background-properties/nocapos.xml
  # The NoCapOS icon (apps, the About page's logo via os-release LOGO=nocapos).
  for s in 16 32 48 64 128 256 512; do
    [ -f "$brand/mark-$s.png" ] || continue
    mkdir -p "/usr/share/icons/hicolor/${s}x$s/apps"
    install -m 0644 "$brand/mark-$s.png" "/usr/share/icons/hicolor/${s}x$s/apps/nocapos.png"
  done
  mkdir -p /usr/share/pixmaps
  install -m 0644 "$brand/mark-256.png" /usr/share/pixmaps/nocapos.png
  if has gtk-update-icon-cache; then gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor >/dev/null 2>&1 || true; fi
  if has update-desktop-database; then update-desktop-database -q /usr/local/share/applications >/dev/null 2>&1 || true; fi
}

# The small NoCapOS shell extension: the logo in the top bar and the accent.
desk_install_extension() {
  local major variant dir="/usr/share/gnome-shell/extensions/$BRAND_UUID"
  major="$(gnome_major "$(gnome-shell --version 2>/dev/null)")"
  variant=esm
  if [ "$major" -gt 0 ] && [ "$major" -lt 45 ]; then variant=legacy; fi
  rm -rf "$dir"
  mkdir -p "$dir"
  install -m 0644 "$DESK_SRC/extension/$variant/extension.js" "$DESK_SRC/extension/$variant/metadata.json" "$DESK_SRC/extension/stylesheet.css" "$dir/"
}

desk_extension_uuids() {
  local u
  echo "$BRAND_UUID"
  for u in dash-to-dock@micxgx.gmail.com ubuntu-dock@ubuntu.com; do
    if [ -d "/usr/share/gnome-shell/extensions/$u" ]; then echo "$u"; break; fi
  done
  for u in appindicatorsupport@rgcjonas.gmail.com ubuntu-appindicators@ubuntu.com; do
    if [ -d "/usr/share/gnome-shell/extensions/$u" ]; then echo "$u"; break; fi
  done
}

desk_settings() {
  local favs exts=()
  favs="$(gv_list $(dock_apps ""))"
  mapfile -t exts < <(desk_extension_uuids)
  # GSettings defaults (every account, every GNOME since 43).
  render_gschema_override "$favs" "${exts[@]}" >/usr/share/glib-2.0/schemas/90_nocapos.gschema.override
  glib-compile-schemas /usr/share/glib-2.0/schemas >/dev/null 2>&1 || warn "full desktop: glib-compile-schemas failed; GNOME keeps its own defaults"
  # dconf system database: the rest, plus the few locked keys.
  mkdir -p /etc/dconf/profile /etc/dconf/db/local.d/locks /etc/dconf/db/gdm.d
  if [ ! -f /etc/dconf/profile/user ]; then
    desk_keep /etc/dconf/profile/user
    printf 'user-db:user\nsystem-db:local\n' >/etc/dconf/profile/user
  elif ! grep -qx 'system-db:local' /etc/dconf/profile/user; then
    desk_keep /etc/dconf/profile/user
    echo 'system-db:local' >>/etc/dconf/profile/user
  fi
  render_dconf_local "$favs" "${exts[@]}" >/etc/dconf/db/local.d/00-nocapos
  render_dconf_locks >/etc/dconf/db/local.d/locks/nocapos
  # The sign-in screen.
  if [ ! -f /etc/dconf/profile/gdm ]; then
    desk_keep /etc/dconf/profile/gdm
    printf 'user-db:user\nsystem-db:gdm\nfile-db:/usr/share/gdm/greeter-dconf-defaults\n' >/etc/dconf/profile/gdm
  fi
  render_dconf_gdm >/etc/dconf/db/gdm.d/90-nocapos
  has dconf && { dconf update >/dev/null 2>&1 || warn "full desktop: dconf update failed"; }
}

desk_units() {
  cat >"$DESK_UNITS/nocapos-gdm-theme.service" <<EOF
# Keeps the NoCapOS sign-in screen theme in step with GNOME Shell updates.
# Written by NoCapOS's install.sh.
[Unit]
Description=NoCapOS sign-in screen theme
Before=display-manager.service
ConditionPathExists=/usr/share/gnome-shell/gnome-shell-theme.gresource

[Service]
Type=oneshot
ExecStart=$DESK_LIB/gdm-theme --if-stale

[Install]
WantedBy=graphical.target
EOF
  cat >"$DESK_UNITS/nocapos-desktop-guard.service" <<EOF
# Keeps NoCap Desktop's remote screen (RDP/VNC) reachable from this machine
# only; NoCapOS opens it in the browser for signed-in admins.
# Written by NoCapOS's install.sh.
[Unit]
Description=NoCap Desktop remote screen guard (loopback only)
Before=display-manager.service gnome-remote-desktop.service
After=network-pre.target nftables.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$DESK_LIB/remote-guard start
ExecStop=$DESK_LIB/remote-guard stop

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable nocapos-gdm-theme.service >/dev/null 2>&1 || true
  # Restarted, so an update's port list applies straight away.
  { systemctl enable nocapos-desktop-guard.service && systemctl restart nocapos-desktop-guard.service; } >/dev/null 2>&1 || warn "full desktop: the remote screen guard didn't start"
  "$DESK_LIB/gdm-theme" || warn "full desktop: the sign-in screen theme couldn't be built; it keeps its own look"
}

# Other updaters stay out of sight: updates come from NoCapOS (Settings →
# Software Update), never a second updater with its own branding. Software
# itself stays: it's the desktop's app store (Flatpak apps), with its own
# updates switched off (render_dconf_locks).
SOFTWARE_FILES="/etc/xdg/autostart/gnome-software-service.desktop /etc/xdg/autostart/org.gnome.Software.desktop /usr/share/applications/org.gnome.Software.desktop"
desk_hide_updaters() {
  local f
  has dpkg-divert || return 0
  # Earlier versions hid Software too: bring it back.
  for f in $SOFTWARE_FILES; do
    if desk_recorded "divert|$f"; then
      dpkg-divert --local --rename --remove "$f" >/dev/null 2>&1 || true
      grep -vxF "divert|$f" "$DESK_BACKUP/edits" >"$DESK_BACKUP/edits.tmp" && cat "$DESK_BACKUP/edits.tmp" >"$DESK_BACKUP/edits"
      rm -f "$DESK_BACKUP/edits.tmp"
    fi
  done
  for f in /etc/xdg/autostart/update-notifier.desktop \
    /usr/share/applications/update-manager.desktop /usr/share/applications/software-properties-gtk.desktop; do
    [ -e "$f" ] || dpkg-divert --list "$f" 2>/dev/null | grep -q . || continue
    if ! dpkg-divert --list "$f" 2>/dev/null | grep -q nocapos; then
      dpkg-divert --local --rename --divert "$f.nocapos-hidden" --add "$f" >/dev/null 2>&1 && desk_record "divert|$f"
    fi
  done
  return 0
}

# Identity: the machine's pretty name and, on apt systems installed from a
# plain distribution, /etc/os-release (the About page, the sign-in screen).
desk_identity() {
  [ "$BRANDING" -eq 1 ] || return 0
  if has hostnamectl; then
    local pretty
    pretty="$(hostnamectl --pretty 2>/dev/null || true)"
    if [ "$pretty" != NoCapOS ]; then
      [ -f "$DESK_BACKUP/pretty-hostname" ] || { mkdir -p "$DESK_BACKUP"; printf '%s\n' "$pretty" >"$DESK_BACKUP/pretty-hostname"; }
      hostnamectl set-hostname --pretty NoCapOS >/dev/null 2>&1 || true
    fi
  fi
  # The NoCapOS images keep os-release themselves.
  [ -x /usr/lib/nocapos/image/os-release-sync ] && return 0
  has dpkg-divert || return 0
  if "$DESK_LIB/os-release-sync" --divert; then
    printf 'DPkg::Post-Invoke { "if [ -x %s/os-release-sync ]; then %s/os-release-sync || true; fi"; };\n' "$DESK_LIB" "$DESK_LIB" \
      >/etc/apt/apt.conf.d/99nocapos-desktop-os-release
    desk_recorded "os-release" || desk_record "os-release"
  fi
}

# Other sign-in screens (lightdm on Raspberry Pi OS, sddm, ...) step aside
# for GDM: disabled, and Debian's choice of display manager says GDM, so a
# later package update doesn't bring them back. One that has the screen
# right now keeps it until the next restart (switching live can leave the
# screen black), and DESK_DM_SWITCHED says so.
DESK_DM_SWITCHED=0
desk_take_display_manager() { # $1 = GDM unit
  local cur u
  cur="$(readlink -f /etc/systemd/system/display-manager.service 2>/dev/null || true)"
  cur="$(basename "${cur:-none}")"
  if [ ! -f "$DESK_BACKUP/display-manager" ]; then
    mkdir -p "$DESK_BACKUP"
    echo "$cur" >"$DESK_BACKUP/display-manager"
    systemctl get-default >"$DESK_BACKUP/default-target" 2>/dev/null || echo multi-user.target >"$DESK_BACKUP/default-target"
  fi
  if [ -f /etc/X11/default-display-manager ] || [ -d /etc/X11 ]; then
    desk_keep /etc/X11/default-display-manager
    if [ -x /usr/sbin/gdm3 ]; then echo /usr/sbin/gdm3 >/etc/X11/default-display-manager; fi
  fi
  if has debconf-set-selections && [ -x /usr/sbin/gdm3 ]; then
    for u in gdm3 lightdm sddm; do
      echo "$u shared/default-x-display-manager select gdm3" | debconf-set-selections >/dev/null 2>&1 || true
    done
  fi
  for u in "$cur" lightdm.service sddm.service lxdm.service slim.service xdm.service nodm.service; do
    case "$u" in none | "$1" | gdm.service | gdm3.service) continue ;; esac
    if systemctl is-active --quiet "$u" 2>/dev/null; then DESK_DM_SWITCHED=1; fi
    systemctl disable "$u" >/dev/null 2>&1 || true
  done
  if [ "$DESK_DM_SWITCHED" -eq 1 ]; then printf '    sign-in screen: GDM from the next restart (was %s)\n' "$cur"; fi
}

# desk_session: NoCapOS is the session GDM starts. Every account's saved
# session (AccountsService, ~/.dmrc) becomes NoCapOS's, and other desktops'
# sessions (Raspberry Pi OS's own desktop, plain GNOME, ...) leave the
# sign-in screen's list. Sets DESK_SESSION_CHANGED when an account's session
# changed, which applies from its next sign-in.
DESK_SESSION_CHANGED=0
desk_session() { # $1 = GDM config
  local f u home
  mkdir -p /usr/share/wayland-sessions /usr/share/xsessions
  render_session_desktop wayland >"/usr/share/wayland-sessions/$NOCAP_SESSION.desktop"
  render_session_desktop x11 >"/usr/share/xsessions/$NOCAP_SESSION.desktop"
  if has dpkg-divert; then
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      dpkg-divert --list "$f" 2>/dev/null | grep -q nocapos && continue
      if dpkg-divert --local --rename --divert "$f.nocapos-hidden" --add "$f" >/dev/null 2>&1; then
        desk_record "divert|$f"
        printf '    hidden session: %s\n' "$(basename "$f" .desktop)"
      fi
    done < <(other_sessions "")
  fi
  # GDM's own default (GDM 40+; older ones ignore it).
  if [ -n "$1" ] && [ -f "$1" ]; then
    desk_keep "$1"
    ini_set daemon DefaultSession "$NOCAP_SESSION.desktop" <"$1" >"$1.nocap.tmp" && cat "$1.nocap.tmp" >"$1"
    rm -f "$1.nocap.tmp"
  fi
  for f in /var/lib/AccountsService/users/*; do
    [ -f "$f" ] || continue
    if session_file_wants_change <"$f"; then
      desk_keep "$f"
      session_file_with User <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f"
      rm -f "$f.nocap.tmp"
      DESK_SESSION_CHANGED=1
    fi
  done
  getent passwd | awk -F: '$3 >= 1000 && $3 < 60000 {print $1 ":" $6}' | while IFS=: read -r u home; do
    f="$home/.dmrc"
    [ -f "$f" ] && [ ! -L "$f" ] || continue
    if session_file_wants_change <"$f"; then
      runuser -u "$u" -- sh -c 'tmp="$(mktemp)" && cat >"$tmp" && cat "$tmp" >"$1"; rm -f "$tmp"' sh "$f" \
        < <(session_file_with Desktop <"$f") >/dev/null 2>&1 || true
    fi
  done
  # Accounts that changed: GDM has to read them again.
  if [ "$DESK_SESSION_CHANGED" -eq 1 ]; then
    systemctl try-restart accounts-daemon.service >/dev/null 2>&1 || true
  fi
}

# ---------------------------------------------------------------- earlier screens
# Earlier versions could show NoCapOS's web desktop on this screen: a
# browser in kiosk mode on tty1 (nocapos-desktop.service, the nocap-desktop
# account) and, with the full desktop, NoCap Hub launchers that opened it in
# app windows. The screen is a native desktop now: all of that goes.
# Sets DESK_KIOSK_WAS to on, off or none (how it was before).
DESK_KIOSK_WAS=none
desk_remove_kiosk() {
  local unit=/etc/systemd/system/nocapos-desktop.service shell f
  if [ -f "$unit" ] || id nocap-desktop >/dev/null 2>&1; then
    DESK_KIOSK_WAS=off
    if systemctl is-enabled --quiet nocapos-desktop.service 2>/dev/null; then DESK_KIOSK_WAS=on; fi
    info "Removing NoCapOS full screen (the web desktop on this screen); the screen gets the native NoCap Desktop"
    systemctl disable --now nocapos-desktop.service >/dev/null 2>&1 || true
    systemctl stop nocapos-desktop-vnc.service >/dev/null 2>&1 || true
    rm -f "$unit"
    rm -f /usr/local/lib/nocapos/nocap-desktop /usr/local/lib/nocapos/nocap-desktop-session
    # desktop.env is the kiosk's unless alfad wrote it.
    if [ -f /run/nocapos/desktop.env ] && ! grep -qs '^# NoCap Desktop session (written by NoCapOS)' /run/nocapos/desktop.env; then
      rm -f /run/nocapos/desktop.env
    fi
    if id nocap-desktop >/dev/null 2>&1; then
      pkill -u nocap-desktop >/dev/null 2>&1 || true
      shell="$(getent passwd nocap-desktop | cut -d: -f7)"
      case "$shell" in */nologin | */false) userdel -r nocap-desktop >/dev/null 2>&1 || userdel nocap-desktop >/dev/null 2>&1 || warn "couldn't remove the nocap-desktop account" ;; esac
    fi
    rm -rf /var/lib/nocap-desktop
    systemctl daemon-reload >/dev/null 2>&1 || true
    # The text console gets tty1 back until the desktop starts.
    systemctl --no-block start getty@tty1.service >/dev/null 2>&1 || true
  fi
  # NoCap Hub: the web desktop in app windows, its launchers and the
  # browser policy that let it show notifications.
  for f in nocap-hub nocap-appstore nocap-settings nocap-files; do
    rm -f "/usr/local/share/applications/$f.desktop"
  done
  rm -f /usr/local/lib/nocapos/nocap-hub
  for f in /etc/brave/policies/managed/nocapos.json /etc/chromium/policies/managed/nocapos.json /etc/opt/chrome/policies/managed/nocapos.json; do
    if grep -qs '127.0.0.1:8780' "$f"; then rm -f "$f"; fi
  done
  if has update-desktop-database; then update-desktop-database -q /usr/local/share/applications >/dev/null 2>&1 || true; fi
  if [ "$(cat "$ETC/desktop-mode" 2>/dev/null)" = kiosk ]; then echo full >"$ETC/desktop-mode"; fi
  return 0
}

# desk_install_browser: a web browser for the dock (a native app, like the
# rest), unless one is installed already.
desk_install_browser() { # $1 = manager
  local p
  for p in firefox-esr firefox chromium chromium-browser brave-browser google-chrome-stable; do
    if has "$p"; then printf '    browser: %s\n' "$p"; return 0; fi
  done
  for p in firefox-esr firefox chromium chromium-browser; do
    if pkg_available "$1" "$p" && pkg_install_lean "$1" "$p"; then printf '    browser: %s\n' "$p"; return 0; fi
  done
  warn "full desktop: no web browser could be installed; the dock goes without one"
}

# desk_restart_needed WHY: this machine has to restart for the screen to
# show NoCap Desktop. Debian's /run/reboot-required is what Settings →
# Software Update and the update result read (it's gone after the restart).
DESK_RESTART=0
desk_restart_needed() {
  DESK_RESTART=1
  [ -f /run/reboot-required ] || echo '*** System restart required ***' >/run/reboot-required
  grep -qsx nocapos-desktop /run/reboot-required.pkgs || echo nocapos-desktop >>/run/reboot-required.pkgs
  info "Restart needed: $1"
}

desk_write_conf() { # $1 = GDM unit, $2 = GDM config
  cat >"$DESK_CONF" <<EOF
# NoCap Desktop on this machine (written by install.sh; NoCapOS reads it).
FULL=1
DM_UNIT=$1
GDM_CONF=$2
EOF
  chmod 644 "$DESK_CONF"
}

# ---------------------------------------------------------------- small machines
desk_mem_kb() { awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null; }

# desk_write FILE: stdin into FILE, keeping the original for uninstall.
desk_write() { desk_keep "$1"; mkdir -p "$(dirname "$1")"; cat >"$1"; }

# Compressed swap in memory on every machine, plus a swap file and the
# low-memory profile (no indexing, no calendar store, no animations) on
# machines with less than 2 GB. Settings already made on this machine (an
# existing zram or swap setup) are kept.
desk_tune_memory() {
  local mem profile mb f fs
  mem="$(desk_mem_kb)"
  profile="$(memory_profile "$mem")"
  printf '    memory: %s MB (%s profile)\n' "$((${mem:-0} / 1024))" "$profile"
  if grep -qs '^/dev/zram' /proc/swaps; then
    : # zram is already on (Raspberry Pi OS and Fedora have their own)
  elif [ -d /usr/lib/systemd/system-generators ] && [ -x /usr/lib/systemd/system-generators/zram-generator ]; then
    if [ ! -f /etc/systemd/zram-generator.conf ] || grep -qs 'written by install.sh' /etc/systemd/zram-generator.conf; then
      render_zram_generator "$mem" | desk_write /etc/systemd/zram-generator.conf
      systemctl daemon-reload >/dev/null 2>&1 || true
      systemctl start systemd-zram-setup@zram0.service >/dev/null 2>&1 || true
    fi
  elif [ -f /etc/default/zramswap ]; then
    if ! grep -qs '^[[:space:]]*\(SIZE\|PERCENT\)=' /etc/default/zramswap || grep -qs 'written by install.sh' /etc/default/zramswap; then
      render_zramswap "$mem" | desk_write /etc/default/zramswap
      systemctl restart zramswap.service >/dev/null 2>&1 || true
    fi
  fi
  if [ "$profile" != low ]; then
    if [ -f /etc/dconf/db/local.d/10-nocapos-lowmem ]; then rm -f /etc/dconf/db/local.d/10-nocapos-lowmem; has dconf && { dconf update >/dev/null 2>&1 || true; }; fi
    return 0
  fi
  render_lowmem_sysctl | desk_write /etc/sysctl.d/90-nocapos-lowmem.conf
  sysctl -q -p /etc/sysctl.d/90-nocapos-lowmem.conf >/dev/null 2>&1 || true
  # A swap file when there's no disk swap at all (not on copy-on-write
  # file systems, where swap files need special care).
  mb="$(swapfile_mb "$mem")"
  fs="$(findmnt -n -o FSTYPE / 2>/dev/null || true)"
  if [ "$mb" -gt 0 ] && ! grep -v '^/dev/zram' /proc/swaps 2>/dev/null | awk 'NR > 1 {f = 1} END {exit !f}' &&
    ! has dphys-swapfile && [ ! -e /var/swap.nocapos ]; then
    case "$fs" in ext4 | ext3 | xfs)
      if fallocate -l "${mb}M" /var/swap.nocapos 2>/dev/null || dd if=/dev/zero of=/var/swap.nocapos bs=1M count="$mb" status=none 2>/dev/null; then
        chmod 600 /var/swap.nocapos
        if mkswap /var/swap.nocapos >/dev/null 2>&1 && swapon -p 10 /var/swap.nocapos 2>/dev/null; then
          echo '/var/swap.nocapos none swap sw,pri=10 0 0' >>/etc/fstab
          desk_record "swapfile|/var/swap.nocapos"
        else
          rm -f /var/swap.nocapos
        fi
      fi
      ;;
    esac
  fi
  # The heavy background services, for every account.
  mkdir -p /etc/systemd/user
  # shellcheck disable=SC2086 # a list of units
  for f in $LOWMEM_USER_UNITS; do
    if [ ! -e "/etc/systemd/user/$f" ]; then
      ln -s /dev/null "/etc/systemd/user/$f" 2>/dev/null && desk_record "usermask|$f"
    fi
  done
  mkdir -p /etc/xdg/autostart
  # shellcheck disable=SC2086 # a list of files
  for f in $LOWMEM_AUTOSTART; do
    if [ ! -e "/etc/xdg/autostart/$f" ] || grep -qs 'Hidden by NoCapOS' "/etc/xdg/autostart/$f"; then
      [ -e "/etc/xdg/autostart/$f" ] || desk_record "autostart-hidden|/etc/xdg/autostart/$f"
      render_autostart_hidden >"/etc/xdg/autostart/$f"
    fi
  done
  render_dconf_lowmem >/etc/dconf/db/local.d/10-nocapos-lowmem
  has dconf && { dconf update >/dev/null 2>&1 || true; }
}

# ---------------------------------------------------------------- the accent
# NoCap red in apps: the GNOME 47+ accent setting (dconf) where it exists,
# plus gtk.css blocks for GTK 3 apps everywhere and libadwaita apps on older
# GNOME. New accounts get them from /etc/skel, existing ones right away, and
# nocapos-accent runs at every sign-in to keep them current (after a GNOME
# upgrade, say).
desk_accent() {
  local u home min
  mkdir -p /etc/xdg/autostart
  cat >/etc/xdg/autostart/nocapos-accent.desktop <<EOF
[Desktop Entry]
# NoCapOS: keeps the NoCap red accent in this account's apps.
Type=Application
Name=NoCapOS accent
Exec=$DESK_LIB/nocapos-accent
NoDisplay=true
X-GNOME-Autostart-Phase=Initialization
X-GNOME-AutoRestart=false
EOF
  # /etc/skel: new accounts start with it.
  HOME=/etc/skel XDG_CONFIG_HOME=/etc/skel/.config "$DESK_LIB/nocapos-accent" || true
  # Existing accounts, each as itself (never root writing in their home).
  min="$(awk '$1 == "UID_MIN" {print $2}' /etc/login.defs 2>/dev/null)"
  getent passwd | awk -F: -v min="${min:-1000}" '$3 >= min && $3 < 60000 && $7 !~ /(nologin|false)$/ {print $1 ":" $6}' |
    while IFS=: read -r u home; do
      [ -d "$home" ] || continue
      runuser -u "$u" -- env HOME="$home" "$DESK_LIB/nocapos-accent" >/dev/null 2>&1 || true
    done
}

# install_full_desktop: the whole native desktop, the same on every machine
# (small and 32-bit ones are tuned, see desk_tune_memory). Returns 1 (after
# saying why) only when this distribution's packages can't provide it.
install_full_desktop() {
  local mgr family dm conf
  mgr="$(pkg_manager)"
  family="$(desktop_family "")"
  case "$family" in
    debian | ubuntu) ;;
    fedora | arch) info "NoCap Desktop on $(os_field "" PRETTY_NAME): best effort, Debian, Ubuntu and Raspberry Pi OS are what it's tested on" ;;
    *) warn "NoCap Desktop isn't supported on $(os_field "" PRETTY_NAME) yet; the screen is left as it is"; return 1 ;;
  esac
  info "Installing NoCap Desktop: the native desktop (sign-in screen, dock, apps; a few minutes)"
  pkg_refresh "$mgr" || true
  if [ -x /usr/sbin/lightdm ] && has debconf-set-selections; then
    # Installing GDM next to lightdm asks which one to use: GDM.
    echo "gdm3 shared/default-x-display-manager select gdm3" | debconf-set-selections >/dev/null 2>&1 || true
  fi
  desk_install_packages "$family" "$mgr" || { warn "NoCap Desktop: the desktop's packages couldn't be installed; the screen is left as it is"; return 1; }
  desk_install_browser "$mgr"
  desk_install_assets
  desk_install_extension
  desk_settings
  desk_tune_memory
  desk_accent
  desk_units
  desk_hide_updaters
  desk_identity
  dm="$(gdm_unit "")"
  conf="$(gdm_config "")"
  desk_take_display_manager "$dm"
  desk_session "$conf"
  # NVIDIA's own driver without kernel modesetting can't run the Wayland
  # session; GDM falls back to Xorg, which needs the modeset switch too.
  if nvidia_proprietary "" && [ "$(cat /sys/module/nvidia_drm/parameters/modeset 2>/dev/null)" != Y ]; then
    EXTRA_BOOT_TOKENS="${EXTRA_BOOT_TOKENS:-} nvidia-drm.modeset=1"
  fi
  desk_write_conf "$dm" "$conf"
  first_user_hint
  return 0
}

# GDM signs in with this machine's Linux accounts (the same ones NoCapOS
# uses). With none yet, say where the first one comes from.
first_user_hint() {
  local min
  min="$(awk '$1 == "UID_MIN" {print $2}' /etc/login.defs 2>/dev/null)"
  if ! getent passwd | awk -F: -v min="${min:-1000}" '$3 >= min && $3 < 60000 && $7 !~ /(nologin|false)$/ {f=1} END {exit !f}'; then
    echo "   The desktop signs in with this machine's Linux accounts. There's none yet: create the first one in"
    echo "   NoCapOS's first-run setup (it becomes a Linux account), then sign in with it on the screen."
  fi
}

# apply_desktop_mode full|off: the same switches Settings uses. An update
# never restarts a sign-in screen that's already up (that would sign out
# whoever is using the desktop); the new look applies from the next sign-in.
apply_desktop_mode() {
  local dm
  dm="$(sed -n 's/^DM_UNIT=//p' "$DESK_CONF" 2>/dev/null)"
  dm="${dm:-gdm.service}"
  case "$1" in
    full)
      systemctl set-default graphical.target >/dev/null 2>&1 || true
      systemctl enable --force "$dm" >/dev/null 2>&1 || warn "NoCap Desktop: couldn't enable the sign-in screen"
      if systemctl is-active --quiet "$dm"; then
        if [ "$DESK_SESSION_CHANGED" -eq 1 ]; then
          desk_restart_needed "the screen moves to the NoCapOS desktop after a restart (or sign out and back in)"
        fi
      elif [ "$DESK_DM_SWITCHED" -eq 1 ]; then
        desk_restart_needed "restart this machine to switch its screen to NoCap Desktop"
      elif tty1_in_use; then
        desk_restart_needed "NoCap Desktop starts on the next boot (someone is signed in on tty1 right now)"
      else
        systemctl start "$dm" >/dev/null 2>&1 || desk_restart_needed "NoCap Desktop didn't start right away; it starts after a restart (details: journalctl -u ${dm%.service} -e)"
      fi
      ;;
    off)
      systemctl disable --now "$dm" >/dev/null 2>&1 || true
      systemctl set-default multi-user.target >/dev/null 2>&1 || true
      systemctl --no-block start getty@tty1.service >/dev/null 2>&1 || true
      ;;
  esac
}

# ---------------------------------------------------------------- boot
# desk_keyword_file KEYWORD FILE TOKENS: adds the tokens a boot menu's
# KEYWORD lines are missing (judged by the first such line) and records them.
desk_keyword_file() {
  local add first
  first="$(grep -m1 -i "^[[:space:]]*$1[[:space:]]" "$2" || true)"
  [ -n "$first" ] || return 0
  # shellcheck disable=SC2086 # a list of tokens
  add="$(tokens_missing "$first" $3)"
  [ -n "$add" ] || return 0
  # shellcheck disable=SC2086
  keyword_tokens_with "$1" $add <"$2" >"$2.nocap.tmp" && cat "$2.nocap.tmp" >"$2" && desk_record "keyword|$2|$1|$add"
  rm -f "$2.nocap.tmp"
}

# Brands every way this machine can boot: GRUB (menu theme and name),
# systemd-boot, U-Boot menus (extlinux, Debian's u-boot-menu), Armbian and
# Raspberry Pi firmware (no rainbow square, no firmware text). Kernel
# command line tokens added here are recorded and taken out by uninstall.
brand_boot() {
  local m f add tokens="$SPLASH_TOKENS ${EXTRA_BOOT_TOKENS:-}"
  desk_install_lib
  for m in $(boot_methods ""); do
    case "$m" in
      pi)
        for f in /boot/firmware/config.txt /boot/config.txt; do
          [ -f "$f" ] || continue
          if ! grep -qs '^[[:space:]]*disable_splash=' "$f"; then
            config_txt_with disable_splash=1 <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f"
            rm -f "$f.nocap.tmp"
            desk_record "configtxt|$f|disable_splash=1"
          fi
          break
        done
        ;;
      grub)
        if [ ! -f /usr/share/grub/themes/nocapos/theme.txt ]; then
          mkdir -p /usr/share/grub/themes/nocapos
          install -m 0644 "$DESK_SRC/grub-theme.txt" /usr/share/grub/themes/nocapos/theme.txt
          install -m 0644 "$DESK_SRC/art/grub-background.png" /usr/share/grub/themes/nocapos/background.png
          desk_record "grub-theme"; BOOT_GRUB_CHANGED=1
        fi
        if [ -d /etc/default/grub.d ] || grep -qs 'grub.d' /usr/sbin/grub-mkconfig; then
          if [ ! -f /etc/default/grub.d/nocapos.cfg ]; then
            mkdir -p /etc/default/grub.d
            printf '# NoCapOS boot menu (written by install.sh, removed by uninstall.sh).\nGRUB_DISTRIBUTOR="NoCapOS"\nGRUB_THEME="/usr/share/grub/themes/nocapos/theme.txt"\nGRUB_GFXMODE=auto\n' >/etc/default/grub.d/nocapos.cfg
            desk_record "grub-cfg"; BOOT_GRUB_CHANGED=1
          fi
        fi
        ;;
      systemd-boot)
        if [ -f /etc/kernel/cmdline ]; then
          # shellcheck disable=SC2086
          add="$(tokens_missing "$(cat /etc/kernel/cmdline)" $tokens)"
          if [ -n "$add" ]; then
            # shellcheck disable=SC2086
            tokens_line_with $add </etc/kernel/cmdline >/etc/kernel/cmdline.nocap.tmp && cat /etc/kernel/cmdline.nocap.tmp >/etc/kernel/cmdline
            rm -f /etc/kernel/cmdline.nocap.tmp
            desk_record "line|/etc/kernel/cmdline|$add"
          fi
        fi
        for f in /boot/efi/loader/entries/*.conf /efi/loader/entries/*.conf /boot/loader/entries/*.conf; do
          if [ -f "$f" ]; then desk_keyword_file options "$f" "$tokens"; fi
        done
        ;;
      uboot-menu)
        f=/etc/default/u-boot
        add="$(tokens_missing "$(sed -n 's/^U_BOOT_PARAMETERS=//p' "$f" | tr -d "\"'")" $tokens)"
        if [ -n "$add" ]; then
          # shellcheck disable=SC2086
          var_tokens_with U_BOOT_PARAMETERS $add <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f"
          rm -f "$f.nocap.tmp"
          desk_record "var|$f|U_BOOT_PARAMETERS|$add"
          if has u-boot-update; then u-boot-update >/dev/null 2>&1 || true; fi
        fi
        ;;
      extlinux)
        desk_keyword_file append /boot/extlinux/extlinux.conf "$tokens"
        ;;
      armbian)
        f=/boot/armbianEnv.txt
        add="$(tokens_missing "$(sed -n 's/^extraargs=//p' "$f" | tr -d "\"'")" $tokens)"
        if [ -n "$add" ]; then
          # shellcheck disable=SC2086
          var_tokens_with extraargs $add <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f"
          rm -f "$f.nocap.tmp"
          desk_record "var|$f|extraargs|$add"
        fi
        if grep -q '^bootlogo=true' "$f"; then
          sed -i 's/^bootlogo=true/bootlogo=false/' "$f" && desk_record "armbian-bootlogo|$f"
        fi
        ;;
    esac
  done
  # Plymouth draws the NoCapOS splash at shutdown and restart too.
  for f in plymouth-poweroff plymouth-reboot plymouth-halt plymouth-kexec; do
    if [ "$(systemctl is-enabled "$f.service" 2>/dev/null)" = masked ]; then systemctl unmask "$f.service" >/dev/null 2>&1 || true; fi
  done
  return 0
}
