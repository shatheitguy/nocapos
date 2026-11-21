# NoCap Desktop, the full desktop: sourced by install.sh after lib.sh. Uses
# install.sh's helpers (info, warn, has, pkg_*, guacd_note, display_connected,
# tty1_in_use, tokens_missing) and settings (BUNDLE_DIR, ETC, DATA, ARCH,
# BRANDING, VERSION). Every file changed outside NoCapOS's own folders is
# recorded in $DESK_BACKUP, and uninstall-desktop.sh (run by uninstall.sh)
# puts it back.
#
# What the screen runs: greetd (the sign-in service) shows NoCap's own
# sign-in screen, then the NoCap Desktop session starts for whoever signed
# in: labwc (windows) and NoCap Shell (top bar, dock, launcher, control
# center, notifications, admin prompts, lock screen). See shell/PROTOCOL.md.
# shellcheck shell=bash

DESK_SRC="$BUNDLE_DIR/desktop"
SHELL_SRC="$BUNDLE_DIR/shell"
DESK_LIB=/usr/local/lib/nocapos/desktop
DESK_SHARE=/usr/share/nocapos/desktop
DESK_CONF="$ETC/desktop.conf"
DESK_BACKUP="$DATA/desktop-backup"
DESK_UNITS=/etc/systemd/system
DESK_DM=greetd.service
# Earlier versions' shell extension (removed on update).
OLD_BRAND_UUID=nocapos-brand@shatheitguy.github.io

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

# desk_write FILE: stdin into FILE, keeping the original for uninstall.
desk_write() { desk_keep "$1"; mkdir -p "$(dirname "$1")"; cat >"$1"; }

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

desk_have_greetd() { has greetd || [ -x /usr/sbin/greetd ] || [ -x /usr/bin/greetd ]; }

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
    if [[ " ${missing[*]} " == *" labwc "* ]]; then
      warn "NoCap Desktop needs labwc, which $(os_field "" PRETTY_NAME) doesn't have (Debian 12 and Ubuntu 22.04 don't; Debian 13, Raspberry Pi OS and Ubuntu 24.04 do). Upgrade this machine to Debian 13 to get NoCap Desktop; everything else in NoCapOS works as it is."
    fi
    pkg_not_available desktop "${missing[@]//|/ or }"
    return 1
  fi
  # guacd isn't packaged everywhere; NoCapOS runs it as a container then.
  local s opt=() guac=0
  for s in ${skipped[@]+"${skipped[@]}"}; do
    case "$s" in guacd* | libguac*) guac=1 ;; *) opt+=("${s//|/ or }") ;; esac
  done
  [ "${#opt[@]}" -eq 0 ] || printf '    desktop: optional packages not in this system'"'"'s repositories, skipped: %s\n' "${opt[*]}"
  [ "$guac" -eq 0 ] || guacd_note desktop
  printf '    desktop: %s\n' "${want[*]}"
  desk_hold_services && held=1
  pkg_install_group "$2" desktop lean "${want[@]}" || true
  [ "$held" -eq 0 ] || desk_release_services
  has labwc || { warn "full desktop: labwc (the compositor) didn't install (the package tool's error is above)"; return 1; }
  desk_have_greetd || { warn "full desktop: greetd (the sign-in service) didn't install (the package tool's error is above)"; return 1; }
}

# The helpers uninstall.sh needs to undo what's recorded in $DESK_BACKUP.
desk_install_lib() {
  mkdir -p "$DESK_LIB"
  install -m 0644 "$DESK_SRC/lib.sh" "$DESK_LIB/lib.sh"
  install -m 0755 "$DESK_SRC/uninstall-desktop.sh" "$DESK_SRC/os-release-sync" "$DESK_SRC/nocapos-accent" "$DESK_LIB/"
  # The silent boot, and Settings → Show boot details.
  install -m 0755 "$DESK_SRC/nocap-boot" "$DESK_LIB/nocap-boot"
  install -m 0644 "$DESK_SRC/grub-nocapos" "$DESK_LIB/grub-nocapos"
}

desk_install_assets() {
  local s n src="$DESK_SRC" brand="$BUNDLE_DIR/brand" wp="$BUNDLE_DIR/wallpapers"
  mkdir -p "$DESK_SHARE" "$DESK_LIB" /usr/share/backgrounds/nocapos /usr/local/share/applications
  install -m 0644 "$src/art/login-background.jpg" "$src/art/login-logo.png" "$DESK_SHARE/"
  install -m 0644 "$brand/mark-128.png" "$DESK_SHARE/logo.png"
  desk_install_lib
  install -m 0755 "$src/remote-guard" "$DESK_LIB/"
  # Wallpapers: NoCapOS's own (the desktop's default is tide.jpg).
  for s in "$wp"/*.jpg; do
    case "$s" in *-thumb.jpg) continue ;; esac
    [ -f "$s" ] || continue
    n="$(basename "$s")"
    install -m 0644 "$s" "/usr/share/backgrounds/nocapos/$n"
  done
  # The NoCapOS icon (apps, the About page's logo via os-release LOGO=nocapos).
  for s in 16 32 48 64 128 256 512; do
    [ -f "$brand/mark-$s.png" ] || continue
    mkdir -p "/usr/share/icons/hicolor/${s}x$s/apps"
    install -m 0644 "$brand/mark-$s.png" "/usr/share/icons/hicolor/${s}x$s/apps/nocapos.png"
  done
  mkdir -p /usr/share/pixmaps
  install -m 0644 "$brand/mark-256.png" /usr/share/pixmaps/nocapos.png
  if has gtk-update-icon-cache; then gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor >/dev/null 2>&1 || true; fi
}

# ---------------------------------------------------------------- NoCap Shell
# desk_shell_runs BIN: the program has every library it needs here (and
# nocap-shell answers --version).
desk_shell_runs() {
  [ -f "$1" ] && [ -x "$1" ] || return 1
  if has ldd && ldd "$1" 2>/dev/null | grep -q 'not found'; then return 1; fi
  case "$1" in */nocap-shell) "$1" --version >/dev/null 2>&1 || return 1 ;; esac
  return 0
}

# desk_build_shell MANAGER FAMILY OUT_DIR: builds nocap-shell and nocap-lock
# from the bundle's sources on this machine (when the bundle's own builds
# can't run here: an older or different system library set).
desk_build_shell() {
  local mgr="$1" family="$2" out="$3" p alt pick work deps=()
  [ -f "$SHELL_SRC/src/Makefile" ] || { warn "NoCap Shell: this bundle has no sources to build from"; return 1; }
  info "Building NoCap Shell for this system (its libraries differ from the release build; a few minutes)"
  for p in $(shell_build_packages "$family"); do
    pick=""
    for alt in ${p//|/ }; do
      if pkg_available "$mgr" "$alt"; then pick="$alt"; break; fi
    done
    [ -n "$pick" ] && deps+=("$pick")
  done
  [ "${#deps[@]}" -eq 0 ] || pkg_install_group "$mgr" desktop lean "${deps[@]}" || true
  work="$(mktemp -d)"
  cp -R "$SHELL_SRC/src/." "$work/"
  if ! make -C "$work" -j"$(nproc 2>/dev/null || echo 2)" NOCAP_VERSION="${VERSION:-dev}" >"$work/build.log" 2>&1; then
    warn "NoCap Shell didn't build here; the last lines of the build log:"
    tail -n 15 "$work/build.log" >&2
    rm -rf "$work"
    return 1
  fi
  mkdir -p "$out"
  install -m 0755 "$work/build/nocap-shell" "$work/build/nocap-lock" "$out/"
  rm -rf "$work"
}

# desk_install_shell MANAGER FAMILY: NoCap Shell's programs, pages, session
# scripts and labwc settings.
desk_install_shell() {
  local mgr="$1" family="$2" bin="$SHELL_SRC/bin" built="" f
  mkdir -p "$SHELL_LIB"
  # The programs: the bundle's build for this architecture when it runs
  # here, else one built on this machine.
  if ! desk_shell_runs "$bin/nocap-shell" || ! desk_shell_runs "$bin/nocap-lock"; then
    built="$(mktemp -d)"
    if desk_build_shell "$mgr" "$family" "$built"; then
      bin="$built"
    elif desk_shell_runs "$SHELL_LIB/nocap-shell" && desk_shell_runs "$SHELL_LIB/nocap-lock"; then
      warn "NoCap Shell: keeping the programs already installed"
      bin=""
    else
      rm -rf "$built"
      return 1
    fi
  fi
  if [ -n "$bin" ]; then
    # Replaced in one step each (a running session keeps its old copy).
    for f in nocap-shell nocap-lock; do
      install -m 0755 "$bin/$f" "$SHELL_LIB/.$f.new" && mv -f "$SHELL_LIB/.$f.new" "$SHELL_LIB/$f"
    done
  fi
  [ -z "$built" ] || rm -rf "$built"
  for f in nocap-session nocap-greeter-session nocap-lock-start nocap-open; do
    install -m 0755 "$DESK_SRC/session/$f" "$SHELL_LIB/$f"
  done
  # The pages: local files only (the host refuses anything else).
  [ -f "$SHELL_SRC/ui/shell.html" ] || { warn "NoCap Shell: this bundle has no shell pages"; return 1; }
  rm -rf "$SHELL_UI.new"
  mkdir -p "$SHELL_UI.new"
  cp -R "$SHELL_SRC/ui/." "$SHELL_UI.new/"
  chmod -R a+rX,go-w "$SHELL_UI.new"
  rm -rf "$SHELL_UI"
  mv "$SHELL_UI.new" "$SHELL_UI"
  # labwc: NoCap Desktop's settings for sessions and for the sign-in screen,
  # and the window decorations.
  mkdir -p "$SHELL_CONF" "$GREETER_CONF" /usr/share/themes/NoCap/openbox-3
  install -m 0644 "$DESK_SRC/labwc/rc.xml" "$DESK_SRC/labwc/menu.xml" "$DESK_SRC/labwc/environment" "$SHELL_CONF/"
  install -m 0755 "$DESK_SRC/labwc/autostart" "$SHELL_CONF/autostart"
  install -m 0644 "$DESK_SRC/greeter/rc.xml" "$DESK_SRC/labwc/environment" "$GREETER_CONF/"
  install -m 0644 "$DESK_SRC/theme/openbox-3/themerc" /usr/share/themes/NoCap/openbox-3/themerc
}

# desk_session_files FAMILY: the session's services, its entry for tools
# that list sessions, the lock screen's password check and the portals
# (installed apps open and save files through them).
desk_session_files() {
  local family="$1" f
  # User services every account's session starts (nocap-session.target).
  mkdir -p /etc/systemd/user
  for f in nocap-session.target nocap-shell.service nocap-idle.service nocapos-accent.service; do
    install -m 0644 "$DESK_SRC/session/$f" "/etc/systemd/user/$f"
  done
  mkdir -p /usr/share/wayland-sessions
  render_session_desktop >"/usr/share/wayland-sessions/$NOCAP_SESSION.desktop"
  render_pam_lock "$family" >/etc/pam.d/nocap-lock
  mkdir -p /usr/share/xdg-desktop-portal
  render_portals_conf >/usr/share/xdg-desktop-portal/nocap-portals.conf
  # Earlier builds of this desktop listed NoCapOS's apps as separate launchers.
  rm -f /usr/local/share/applications/nocapos-settings.desktop /usr/local/share/applications/nocapos-appstore.desktop /etc/nocapos/dock-default.json
}

# desk_greetd FAMILY: the sign-in service shows NoCap's sign-in screen as
# its own unprivileged account. Automatic sign-in set up before (in
# Settings, or on the earlier sign-in screen) is kept.
desk_greetd() {
  local gu auto="" g
  gu="$(greeter_user "")"
  if ! id "$gu" >/dev/null 2>&1; then
    useradd --system --no-create-home --home-dir /var/lib/greetd --shell /usr/sbin/nologin "$gu" >/dev/null 2>&1 ||
      { warn "NoCap Desktop: couldn't create the sign-in screen's account $gu"; return 1; }
    desk_record "user|$gu"
  fi
  # Its labwc needs the graphics and input devices.
  for g in video render input; do
    getent group "$g" >/dev/null 2>&1 && usermod -aG "$g" "$gu" >/dev/null 2>&1
  done
  if [ -f "$GREETD_CONF" ]; then auto="$(greetd_autologin <"$GREETD_CONF")"; fi
  if [ -z "$auto" ]; then auto="$(desk_old_autologin)"; fi
  if [ -n "$auto" ] && ! id "$auto" >/dev/null 2>&1; then auto=""; fi
  render_greetd_config "$gu" "$auto" | desk_write "$GREETD_CONF"
  chmod 644 "$GREETD_CONF"
  # The keyring unlocks with the sign-in password.
  if [ -f /etc/pam.d/greetd ] && ! grep -qs pam_gnome_keyring /etc/pam.d/greetd; then
    desk_keep /etc/pam.d/greetd
    pam_with_keyring </etc/pam.d/greetd >/etc/pam.d/greetd.nocap.tmp && cat /etc/pam.d/greetd.nocap.tmp >/etc/pam.d/greetd
    rm -f /etc/pam.d/greetd.nocap.tmp
  fi
  # The sign-in screen takes the first console over from the boot splash,
  # which leaves its last frame up until the sign-in screen draws.
  mkdir -p "$DESK_UNITS/greetd.service.d" "$DESK_UNITS/plymouth-quit.service.d"
  render_greetd_dropin >"$DESK_UNITS/greetd.service.d/nocapos.conf"
  render_plymouth_quit_dropin >"$DESK_UNITS/plymouth-quit.service.d/nocapos.conf"
  systemctl daemon-reload >/dev/null 2>&1 || true
}

# desk_old_autologin: the account the earlier sign-in screen signed in
# automatically, if any.
desk_old_autologin() {
  local f
  for f in /etc/gdm3/daemon.conf /etc/gdm3/custom.conf /etc/gdm/custom.conf; do
    [ -f "$f" ] || continue
    awk '
      { s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s) }
      s ~ /^\[/ { d = (s == "[daemon]"); next }
      d && s ~ /^AutomaticLoginEnable[ \t]*=/ { v = s; sub(/^[^=]*=[ \t]*/, "", v); on = (tolower(v) == "true") }
      d && s ~ /^AutomaticLogin[ \t]*=/ { v = s; sub(/^[^=]*=[ \t]*/, "", v); who = v }
      END { if (on && who ~ /^[a-z_][a-z0-9_.-]*$/) print who }
    ' "$f"
    return 0
  done
}

# ---------------------------------------------------------------- earlier desktops
# Earlier versions' desktop ran on other packages, with NoCapOS's look added
# by a theme service, a shell extension and settings databases. Those
# NoCapOS pieces go; the packages themselves stay installed (nothing anyone
# saved is touched) and the installer says which ones could be removed.
desk_migrate_old_desktop() {
  local p left=() changed=0 f
  if [ -f "$DESK_UNITS/nocapos-gdm-theme.service" ]; then
    systemctl disable nocapos-gdm-theme.service >/dev/null 2>&1 || true
    rm -f "$DESK_UNITS/nocapos-gdm-theme.service"
    changed=1
  fi
  rm -f "$DESK_LIB/gdm-theme"
  # Earlier builds of this desktop kept its programs here.
  rm -rf /usr/local/lib/nocapos/shell
  if has update-alternatives && [ -f "$DESK_SHARE/gdm-theme.gresource" ]; then
    update-alternatives --remove gdm-theme.gresource "$DESK_SHARE/gdm-theme.gresource" >/dev/null 2>&1 || true
  fi
  rm -f "$DESK_SHARE/gdm-theme.gresource"
  if [ -d "/usr/share/gnome-shell/extensions/$OLD_BRAND_UUID" ]; then rm -rf "/usr/share/gnome-shell/extensions/$OLD_BRAND_UUID"; changed=1; fi
  if [ -f /usr/share/glib-2.0/schemas/90_nocapos.gschema.override ]; then
    rm -f /usr/share/glib-2.0/schemas/90_nocapos.gschema.override
    if has glib-compile-schemas; then glib-compile-schemas /usr/share/glib-2.0/schemas >/dev/null 2>&1 || true; fi
  fi
  for f in /etc/dconf/db/local.d/00-nocapos /etc/dconf/db/local.d/10-nocapos-lowmem /etc/dconf/db/local.d/locks/nocapos /etc/dconf/db/gdm.d/90-nocapos; do
    if [ -f "$f" ]; then rm -f "$f"; changed=1; fi
  done
  if [ "$changed" -eq 1 ] && has dconf; then dconf update >/dev/null 2>&1 || true; fi
  rm -f /usr/share/xsessions/"$NOCAP_SESSION".desktop /etc/xdg/autostart/nocapos-accent.desktop /usr/share/gnome-background-properties/nocapos.xml
  systemctl daemon-reload >/dev/null 2>&1 || true
  # shellcheck disable=SC2086 # a list of packages
  for p in $OLD_DESKTOP_PACKAGES; do
    if has dpkg-query && dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'ok installed'; then left+=("$p")
    elif has rpm && rpm -q "$p" >/dev/null 2>&1; then left+=("$p")
    elif has pacman && pacman -Q "$p" >/dev/null 2>&1; then left+=("$p")
    fi
  done
  if [ "${#left[@]}" -gt 0 ]; then
    printf '    earlier desktop: its packages stay installed and unused (%s); nobody'"'"'s files or settings change.\n' "${left[*]}"
    printf '    To free the space, remove them with the package tool (they are not needed by NoCap Desktop).\n'
  fi
  return 0
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
  desk_remove_old_browser
  return 0
}

# desk_remove_old_browser: earlier versions installed a web browser
# themselves (from its maker's package repository, for a streamed browser
# app). NoCapOS ships no browser any more: when NoCapOS installed it (its
# notes are in the data folder), the package, its repository and key, and
# NoCapOS's launchers and policies for it go. Everyone's own browser
# profile in their home folder stays. A browser someone installed
# themselves is left alone.
desk_remove_old_browser() {
  local mine=0 f
  for f in "$DATA"/brave.log "$DATA"/brave-run.sh "$DATA"/webapps/brave* "$DATA"/brave; do
    if [ -e "$f" ]; then mine=1; break; fi
  done
  [ "$mine" -eq 1 ] || return 0
  if has dpkg-query && dpkg-query -W -f='${Status}' brave-browser 2>/dev/null | grep -q 'ok installed'; then
    info "Removing the web browser earlier NoCapOS versions installed (brave-browser); browser profiles in home folders stay"
    DEBIAN_FRONTEND=noninteractive apt-get remove -y -q brave-browser >/dev/null 2>&1 || warn "brave-browser couldn't be removed; remove it with the package tool"
  elif has rpm && rpm -q brave-browser >/dev/null 2>&1; then
    info "Removing the web browser earlier NoCapOS versions installed (brave-browser); browser profiles in home folders stay"
    dnf remove -y -q brave-browser >/dev/null 2>&1 || warn "brave-browser couldn't be removed; remove it with the package tool"
  fi
  if grep -qs 'brave-browser-apt-release.s3.brave.com' /etc/apt/sources.list.d/brave-browser-release.list; then
    rm -f /etc/apt/sources.list.d/brave-browser-release.list /usr/share/keyrings/brave-browser-archive-keyring.gpg
  fi
  if grep -qs 'brave-browser-rpm-release' /etc/yum.repos.d/brave-browser.repo; then rm -f /etc/yum.repos.d/brave-browser.repo; fi
  rm -f /etc/brave/policies/managed/nocapos.json /usr/local/share/applications/nocap-brave.desktop
  rm -f "$DATA"/brave.log "$DATA"/brave-run.sh
  return 0
}

desk_units() {
  cat >"$DESK_UNITS/nocapos-desktop-guard.service" <<EOF
# Keeps NoCap Desktop's remote screen (VNC) reachable from this machine
# only; NoCapOS opens it in the browser for signed-in admins.
# Written by NoCapOS's install.sh.
[Unit]
Description=NoCap Desktop remote screen guard (loopback only)
Before=display-manager.service
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
  # Restarted, so an update's port list applies straight away.
  { systemctl enable nocapos-desktop-guard.service && systemctl restart nocapos-desktop-guard.service; } >/dev/null 2>&1 || warn "full desktop: the remote screen guard didn't start"
}

# Other updaters stay out of sight: updates come from NoCapOS (Settings →
# Software Update), never a second updater with its own branding.
desk_hide_updaters() {
  local f
  has dpkg-divert || return 0
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
# plain distribution, /etc/os-release (the About page).
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

# Other sign-in services (the earlier versions' one, a distribution's own)
# step aside for greetd: disabled, and Debian's record of the
# display manager says greetd, so a later package update doesn't bring them
# back. One that has the screen right now keeps it until NoCapOS can switch
# without signing anyone out (DESK_DM_SWITCHED says so; apply_desktop_mode
# decides).
DESK_DM_SWITCHED=0
DESK_DM_OLD=""
desk_take_display_manager() {
  local cur u
  cur="$(readlink -f /etc/systemd/system/display-manager.service 2>/dev/null || true)"
  cur="$(basename "${cur:-none}")"
  if [ ! -f "$DESK_BACKUP/display-manager" ]; then
    mkdir -p "$DESK_BACKUP"
    echo "$cur" >"$DESK_BACKUP/display-manager"
    systemctl get-default >"$DESK_BACKUP/default-target" 2>/dev/null || echo multi-user.target >"$DESK_BACKUP/default-target"
  fi
  if [ -d /etc/X11 ]; then
    desk_keep /etc/X11/default-display-manager
    echo /usr/sbin/greetd >/etc/X11/default-display-manager
  fi
  # shellcheck disable=SC2086 # a list of units
  for u in "$cur" $OLD_DISPLAY_MANAGERS; do
    case "$u" in none | "$DESK_DM") continue ;; esac
    if systemctl is-active --quiet "$u" 2>/dev/null; then
      DESK_DM_SWITCHED=1
      case " $DESK_DM_OLD " in *" $u "*) ;; *) DESK_DM_OLD="$DESK_DM_OLD $u" ;; esac
    fi
    systemctl disable "$u" >/dev/null 2>&1 || true
  done
  return 0
}

# desk_link_display_manager UNIT: graphical.target starts whatever
# /etc/systemd/system/display-manager.service points at. It's made here
# (an earlier display manager's link is in the way of "systemctl enable").
desk_link_display_manager() {
  local unit="$1" link=/etc/systemd/system/display-manager.service path d
  path="$(systemctl show -p FragmentPath --value "$unit" 2>/dev/null || true)"
  if [ -z "$path" ] || [ ! -f "$path" ]; then
    path=""
    for d in /usr/lib/systemd/system /lib/systemd/system /etc/systemd/system; do
      if [ -f "$d/$unit" ] && [ ! -L "$d/$unit" ]; then path="$d/$unit"; break; fi
    done
  fi
  [ -n "$path" ] || { warn "NoCap Desktop: $unit isn't installed"; return 1; }
  if [ "$(readlink -f "$link" 2>/dev/null)" != "$(readlink -f "$path")" ]; then
    ln -sfn "$path" "$link"
    printf '    sign-in screen: %s starts at boot\n' "$unit"
  fi
  if [ -d /etc/X11 ] && [ "$unit" = "$DESK_DM" ]; then
    echo /usr/sbin/greetd >/etc/X11/default-display-manager
  fi
  systemctl daemon-reload >/dev/null 2>&1 || true
}

# desk_unlink_display_manager UNIT: the link goes when it's UNIT's (off).
desk_unlink_display_manager() {
  local link=/etc/systemd/system/display-manager.service
  if [ -L "$link" ] && [ "$(basename "$(readlink -f "$link")")" = "$1" ]; then
    rm -f "$link"
    systemctl daemon-reload >/dev/null 2>&1 || true
  fi
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

desk_write_conf() {
  cat >"$DESK_CONF" <<EOF
# NoCap Desktop on this machine (written by install.sh; NoCapOS reads it).
FULL=1
DM_UNIT=$DESK_DM
GREETD_CONF=$GREETD_CONF
SHELL=nocap
EOF
  chmod 644 "$DESK_CONF"
}

# ---------------------------------------------------------------- small machines
desk_mem_kb() { awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null; }

# Compressed swap in memory on every machine, plus a swap file and the
# low-memory profile on machines with less than 2 GB. Settings already made
# on this machine (an existing zram or swap setup) are kept.
desk_tune_memory() {
  local mem profile mb fs
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
  [ "$profile" = low ] || return 0
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
}

# ---------------------------------------------------------------- the accent
# NoCap red in apps: gtk.css blocks for GTK 3 and GTK 4 apps. New accounts
# get them from /etc/skel, existing ones right away, and every session runs
# nocapos-accent (nocapos-accent.service) to keep them current.
desk_accent() {
  local u home min
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

# ---------------------------------------------------------------- the clipboard
# Copy on the phone, paste on NoCap Desktop (and the other way round): a
# user service in every graphical session, for every account. Switched on
# with a link, the same as "systemctl --global enable" (that needs no
# running systemd, so it works inside the image build too).
desk_clipboard() {
  local cap=/usr/local/bin/cap
  mkdir -p /etc/systemd/user/graphical-session.target.wants
  render_clipboard_unit "$cap" >/etc/systemd/user/nocapos-clipboard.service
  ln -sf ../nocapos-clipboard.service /etc/systemd/user/graphical-session.target.wants/nocapos-clipboard.service
}

# install_full_desktop: the whole native desktop, the same on every machine
# (small and 32-bit ones are tuned, see desk_tune_memory). Returns 1 (after
# saying why) only when this distribution's packages can't provide it.
install_full_desktop() {
  local mgr family
  mgr="$(pkg_manager)"
  family="$(desktop_family "")"
  case "$family" in
    debian | ubuntu) ;;
    fedora | arch) info "NoCap Desktop on $(os_field "" PRETTY_NAME): best effort, Debian 13 and Raspberry Pi OS are what it's tested on" ;;
    *) warn "NoCap Desktop isn't supported on $(os_field "" PRETTY_NAME) yet; the screen is left as it is"; return 1 ;;
  esac
  info "Installing NoCap Desktop: the native desktop (sign-in screen, dock, apps; a few minutes)"
  pkg_refresh "$mgr" || true
  desk_install_packages "$family" "$mgr" || { warn "NoCap Desktop: the desktop's packages couldn't be installed; the screen is left as it is"; return 1; }
  desk_install_assets
  desk_install_shell "$mgr" "$family" || { warn "NoCap Desktop: NoCap Shell couldn't be installed; the screen is left as it is"; return 1; }
  desk_session_files "$family"
  desk_migrate_old_desktop
  desk_tune_memory
  desk_accent
  desk_clipboard
  desk_units
  desk_hide_updaters
  desk_identity
  desk_take_display_manager
  desk_greetd "$family" || return 1
  desk_pi_kms
  # NVIDIA's own driver needs kernel modesetting for the compositor.
  if nvidia_proprietary "" && [ "$(cat /sys/module/nvidia_drm/parameters/modeset 2>/dev/null)" != Y ]; then
    EXTRA_BOOT_TOKENS="${EXTRA_BOOT_TOKENS:-} nvidia-drm.modeset=1"
  fi
  desk_write_conf
  first_user_hint
  return 0
}

# desk_pi_kms: a Raspberry Pi gets its KMS graphics driver (the compositor
# can't draw without it). Recorded, so uninstall takes it out again.
desk_pi_kms() {
  local f
  [ "$(boot_methods "" | grep -c '^pi$')" -gt 0 ] || return 0
  for f in /boot/firmware/config.txt /boot/config.txt; do
    [ -f "$f" ] || continue
    if pi_kms_missing <"$f"; then
      config_txt_append "$PI_KMS_OVERLAY" <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f"
      rm -f "$f.nocap.tmp"
      desk_record "configtxt|$f|$PI_KMS_OVERLAY"
      desk_restart_needed "the Raspberry Pi's graphics driver (KMS) was turned on for NoCap Desktop"
    fi
    break
  done
  return 0
}

# desk_verify: everything the screen needs is really there. Says what isn't
# (each of these ends in a black screen, or no sign-in screen).
desk_verify() {
  local ok=0 f found
  has labwc || { warn "NoCap Desktop: labwc (the compositor) is missing"; ok=1; }
  desk_have_greetd || { warn "NoCap Desktop: greetd (the sign-in service) is missing"; ok=1; }
  desk_shell_runs "$SHELL_LIB/nocap-shell" || { warn "NoCap Desktop: NoCap Shell can't run here"; ok=1; }
  desk_shell_runs "$SHELL_LIB/nocap-lock" || { warn "NoCap Desktop: the lock screen can't run here"; ok=1; }
  [ -f "$SHELL_UI/shell.html" ] || { warn "NoCap Desktop: NoCap Shell's pages are missing"; ok=1; }
  [ -x "$SHELL_LIB/nocap-session" ] && [ -x "$SHELL_LIB/nocap-greeter-session" ] || { warn "NoCap Desktop: the session scripts are missing"; ok=1; }
  [ -f "$SHELL_CONF/rc.xml" ] && [ -f "$SHELL_CONF/autostart" ] || { warn "NoCap Desktop: the desktop's labwc settings are missing"; ok=1; }
  grep -qs "nocap-greeter-session" "$GREETD_CONF" || { warn "NoCap Desktop: greetd isn't set to show NoCap's sign-in screen"; ok=1; }
  [ -f "/usr/share/wayland-sessions/$NOCAP_SESSION.desktop" ] || { warn "NoCap Desktop: the NoCapOS session file is missing"; ok=1; }
  has Xwayland || [ -x /usr/bin/Xwayland ] || warn "NoCap Desktop: Xwayland is missing, so older (X11) apps won't open"
  found=0
  for f in /usr/lib/*/dri/swrast_dri.so /usr/lib/dri/swrast_dri.so /usr/lib64/dri/swrast_dri.so /usr/lib/*/libgallium-*.so /usr/lib64/libgallium-*.so /usr/lib/libgallium-*.so; do
    if [ -e "$f" ]; then found=1; break; fi
  done
  [ "$found" -eq 1 ] || { warn "NoCap Desktop: the Mesa graphics drivers are missing"; ok=1; }
  [ "$(basename "$(readlink -f /etc/systemd/system/display-manager.service 2>/dev/null)")" = "$DESK_DM" ] ||
    { warn "NoCap Desktop: $DESK_DM isn't set to start at boot"; ok=1; }
  if grep -qs 'NoCapOS install.sh' /usr/sbin/policy-rc.d; then rm -f /usr/sbin/policy-rc.d; fi
  return "$ok"
}

# desk_screen_in_use: someone is signed in to a graphical session on this
# machine right now (switching the sign-in service would sign them out).
desk_screen_in_use() {
  local id p
  has loginctl || return 1
  for id in $(loginctl list-sessions --no-legend 2>/dev/null | awk '{print $1}'); do
    p="$(loginctl show-session "$id" -p Class -p Type -p Remote 2>/dev/null | tr '\n' ' ')"
    case "$p" in *Class=user\ * | *Class=user) ;; *) continue ;; esac
    case "$p" in *Remote=yes*) continue ;; esac
    case "$p" in *Type=wayland* | *Type=x11* | *Type=mir*) return 0 ;; esac
  done
  return 1
}

# The sign-in screen signs in with this machine's Linux accounts (the same
# ones NoCapOS uses). With none yet, say where the first one comes from.
first_user_hint() {
  local min
  min="$(awk '$1 == "UID_MIN" {print $2}' /etc/login.defs 2>/dev/null)"
  if ! getent passwd | awk -F: -v min="${min:-1000}" '$3 >= min && $3 < 60000 && $7 !~ /(nologin|false)$/ {f=1} END {exit !f}'; then
    echo "   The desktop signs in with this machine's Linux accounts. There's none yet: create the first one in"
    echo "   NoCapOS's first-run setup (it becomes a Linux account), then sign in with it on the screen."
  fi
}

# apply_desktop_mode full|off: the same switches Settings uses. An update
# never restarts a sign-in service that someone is signed in through (that
# would sign them out); the switch then waits for the next restart.
apply_desktop_mode() {
  local dm="$DESK_DM" u
  case "$1" in
    full)
      systemctl set-default graphical.target >/dev/null 2>&1 || true
      systemctl enable --force "$dm" >/dev/null 2>&1 || true
      desk_link_display_manager "$dm" || warn "NoCap Desktop: couldn't enable the sign-in screen"
      desk_verify || warn "NoCap Desktop: some parts are missing (above); fix with Settings → General → NoCap Desktop → Repair, or: sudo cap desktop repair"
      if systemctl is-active --quiet "$dm"; then
        if display_connected && has pgrep && ! pgrep -x labwc >/dev/null 2>&1; then
          # greetd runs but nothing is on the screen (a black screen):
          # nobody can be signed in, so restarting it signs nobody out.
          systemctl restart "$dm" >/dev/null 2>&1 || desk_restart_needed "NoCap Desktop's sign-in screen didn't restart; it starts after a restart"
        fi
      elif [ "$DESK_DM_SWITCHED" -eq 1 ]; then
        if desk_screen_in_use; then
          desk_restart_needed "the screen moves to NoCap Desktop after a restart (someone is signed in on it right now)"
        else
          # Only an empty sign-in screen is showing: switch now.
          for u in $DESK_DM_OLD; do systemctl stop "$u" >/dev/null 2>&1 || true; done
          systemctl start "$dm" >/dev/null 2>&1 || desk_restart_needed "NoCap Desktop starts after a restart"
        fi
      elif tty1_in_use; then
        desk_restart_needed "NoCap Desktop starts on the next boot (someone is signed in on tty1 right now)"
      else
        systemctl start "$dm" >/dev/null 2>&1 || desk_restart_needed "NoCap Desktop didn't start right away; it starts after a restart (details: journalctl -u ${dm%.service} -e)"
      fi
      ;;
    off)
      systemctl disable --now "$dm" >/dev/null 2>&1 || true
      desk_unlink_display_manager "$dm"
      systemctl set-default multi-user.target >/dev/null 2>&1 || true
      systemctl --no-block start getty@tty1.service >/dev/null 2>&1 || true
      ;;
  esac
}


# ---------------------------------------------------------------- boot
# desk_put SRC DEST: DEST becomes a copy of SRC when it differs (returns 0
# when it changed).
desk_put() {
  if [ -f "$2" ] && cmp -s "$1" "$2"; then return 1; fi
  mkdir -p "$(dirname "$2")"
  install -m 0644 "$1" "$2"
}

# Brands every way this machine boots and makes it silent: the GRUB theme
# (the menu only shows while Shift or Esc is held), the kernel lines of
# GRUB, systemd-boot, U-Boot menus, Armbian and the Raspberry Pi (no boot
# text, the NoCapOS splash), firmware logos off, and the graphics driver
# loaded early for the splash. nocap-boot does the boot menus and kernel
# lines, the same way Settings → Show boot details does; it backs up and
# checks GRUB's files and puts them back if update-grub fails. What's
# changed outside NoCapOS's folders is recorded, and uninstall puts it back.
brand_boot() {
  local f changed=0
  desk_install_lib
  if boot_methods "" | grep -qx grub; then
    mkdir -p "$GRUB_THEME_DIR"
    desk_put "$DESK_SRC/grub-theme.txt" "$GRUB_THEME_DIR/theme.txt" && changed=1
    desk_put "$DESK_SRC/art/grub-background.png" "$GRUB_THEME_DIR/background.png" && changed=1
    desk_put "$DESK_SRC/art/grub-boot.png" "$GRUB_THEME_DIR/boot.png" && changed=1
    desk_recorded "grub-theme" || desk_record "grub-theme"
  fi
  for f in /boot/firmware/config.txt /boot/config.txt; do
    [ -f "$f" ] || continue
    # Raspberry Pi firmware: no rainbow square before the splash.
    if ! grep -qs '^[[:space:]]*disable_splash=' "$f"; then
      config_txt_with disable_splash=1 <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f"
      rm -f "$f.nocap.tmp"
      desk_record "configtxt|$f|disable_splash=1"
    fi
    break
  done
  f=/boot/armbianEnv.txt
  if [ -f "$f" ] && grep -q '^bootlogo=true' "$f"; then
    sed -i 's/^bootlogo=true/bootlogo=false/' "$f" && desk_record "armbian-bootlogo|$f"
  fi
  # Boot menus and kernel lines (a failure there leaves them as they were).
  NOCAP_GRUB_CHANGED="$changed" EXTRA_BOOT_TOKENS="${EXTRA_BOOT_TOKENS:-}" "$DESK_LIB/nocap-boot" apply |
    while IFS= read -r line; do info "$line"; done
  # Plymouth draws the NoCapOS splash at shutdown and restart too.
  for f in plymouth-poweroff plymouth-reboot plymouth-halt plymouth-kexec; do
    if [ "$(systemctl is-enabled "$f.service" 2>/dev/null)" = masked ]; then systemctl unmask "$f.service" >/dev/null 2>&1 || true; fi
  done
  return 0
}
