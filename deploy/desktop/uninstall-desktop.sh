#!/bin/bash
# Takes NoCap Desktop's full desktop back out: NoCapOS's settings, theme,
# shell extension, app entries, boot branding and units, and puts back what
# install.sh changed (recorded in the data folder's desktop-backup). The
# desktop packages themselves stay installed (remove them with your package
# manager); the sign-in screen goes back to whatever was there before.
# Run by uninstall.sh:  uninstall-desktop.sh DATA_DIR
set -u
DATA="${1:-/var/lib/nocapos}"
BK="$DATA/desktop-backup"
LIB=/usr/local/lib/nocapos/desktop
# shellcheck source=/dev/null
. "$LIB/lib.sh" 2>/dev/null || true
has() { command -v "$1" >/dev/null 2>&1; }
UUID="${NOCAP_BRAND_UUID:-nocapos-brand@shatheitguy.github.io}"

conf=/etc/nocapos/desktop.conf
dm="$(sed -n 's/^DM_UNIT=//p' "$conf" 2>/dev/null)"

# The sign-in screen: back to the one there was before (or the console).
if [ -f "$BK/display-manager" ]; then
  prev="$(cat "$BK/display-manager")"
  target="$(cat "$BK/default-target" 2>/dev/null || echo multi-user.target)"
  if [ -n "$dm" ] && [ "$prev" != "$dm" ]; then
    systemctl disable "$dm" >/dev/null 2>&1 || true
    # GDM's link (Debian's gdm3 has no [Install], so install.sh made it).
    link=/etc/systemd/system/display-manager.service
    if [ -L "$link" ] && [ "$(basename "$(readlink -f "$link")")" = "$dm" ]; then rm -f "$link"; fi
  fi
  case "$prev" in
    none | '') ;;
    *) systemctl enable --force "$prev" >/dev/null 2>&1 || true ;;
  esac
  systemctl set-default "$target" >/dev/null 2>&1 || true
  if [ "$prev" = none ] && [ -n "$dm" ]; then systemctl stop "$dm" >/dev/null 2>&1 || true; fi
fi

# Units.
for u in nocapos-desktop-guard nocapos-gdm-theme; do
  systemctl disable --now "$u.service" >/dev/null 2>&1 || true
  rm -f "/etc/systemd/system/$u.service"
done
systemctl daemon-reload >/dev/null 2>&1 || true

# The sign-in screen theme.
if has update-alternatives; then
  update-alternatives --remove gdm-theme.gresource /usr/share/nocapos/desktop/gdm-theme.gresource >/dev/null 2>&1 || true
fi

# Settings, extension, app entries, artwork.
rm -f /usr/share/glib-2.0/schemas/90_nocapos.gschema.override
has glib-compile-schemas && glib-compile-schemas /usr/share/glib-2.0/schemas >/dev/null 2>&1
rm -f /etc/dconf/db/local.d/00-nocapos /etc/dconf/db/local.d/10-nocapos-lowmem /etc/dconf/db/local.d/locks/nocapos /etc/dconf/db/gdm.d/90-nocapos
# The NoCap red accent: out of every account's gtk.css and /etc/skel.
if [ -x "$LIB/nocapos-accent" ]; then
  rm -f /etc/xdg/autostart/nocapos-accent.desktop
  HOME=/etc/skel XDG_CONFIG_HOME=/etc/skel/.config "$LIB/nocapos-accent" --remove || true
  getent passwd | awk -F: '$3 >= 1000 && $3 < 60000 && $7 !~ /(nologin|false)$/ {print $1 ":" $6}' |
    while IFS=: read -r u home; do
      [ -d "$home" ] || continue
      runuser -u "$u" -- env HOME="$home" "$LIB/nocapos-accent" --remove >/dev/null 2>&1 || true
    done
fi
# The remote screen follower's leftovers.
systemctl stop nocapos-screen.service >/dev/null 2>&1 || true
rm -rf /run/nocapos/screen
rm -rf "/usr/share/gnome-shell/extensions/$UUID"
rm -f /usr/local/share/applications/nocap-hub.desktop /usr/local/share/applications/nocap-appstore.desktop \
  /usr/local/share/applications/nocap-settings.desktop /usr/local/share/applications/nocap-files.desktop
rm -f /usr/local/lib/nocapos/nocap-hub
rm -f /usr/share/wayland-sessions/nocapos.desktop /usr/share/xsessions/nocapos.desktop
rm -rf /usr/share/backgrounds/nocapos /usr/share/nocapos
rm -f /usr/share/gnome-background-properties/nocapos.xml
rm -f /etc/brave/policies/managed/nocapos.json /etc/chromium/policies/managed/nocapos.json /etc/opt/chrome/policies/managed/nocapos.json
rm -f /etc/apt/apt.conf.d/99nocapos-desktop-os-release
# The NoCapOS icon stays when the image owns it.
if [ ! -d /usr/lib/nocapos/image ]; then
  for s in 16 32 48 64 128 256 512; do rm -f "/usr/share/icons/hicolor/${s}x$s/apps/nocapos.png"; done
  rm -f /usr/share/pixmaps/nocapos.png
fi

# Files kept as they were (or noted as absent) before NoCapOS changed them.
if [ -d "$BK/files" ]; then
  for f in "$BK"/files/*; do
    [ -e "$f" ] || continue
    name="$(basename "$f")"
    case "$name" in
      *.absent) rm -f "$(printf '%s' "${name%.absent}" | sed 's|%|/|g')" ;;
      *) cp -pP "$f" "$(printf '%s' "$name" | sed 's|%|/|g')" ;;
    esac
  done
fi
has dconf && dconf update >/dev/null 2>&1

# Recorded edits: boot menus, kernel command lines, hidden updaters, os-release.
if [ -f "$BK/edits" ]; then
  grub=0
  while IFS='|' read -r kind a b c; do
    case "$kind" in
      configtxt) [ -f "$a" ] && config_txt_without "$b" <"$a" >"$a.nocap.tmp" && cat "$a.nocap.tmp" >"$a"; rm -f "$a.nocap.tmp" ;;
      # shellcheck disable=SC2086 # recorded token lists
      line) [ -f "$a" ] && tokens_line_without $b <"$a" >"$a.nocap.tmp" && cat "$a.nocap.tmp" >"$a"; rm -f "$a.nocap.tmp" ;;
      # shellcheck disable=SC2086
      keyword) [ -f "$a" ] && keyword_tokens_without "$b" $c <"$a" >"$a.nocap.tmp" && cat "$a.nocap.tmp" >"$a"; rm -f "$a.nocap.tmp" ;;
      # shellcheck disable=SC2086
      var) [ -f "$a" ] && var_tokens_without "$b" $c <"$a" >"$a.nocap.tmp" && cat "$a.nocap.tmp" >"$a"; rm -f "$a.nocap.tmp"
        [ "$a" = /etc/default/u-boot ] && has u-boot-update && u-boot-update >/dev/null 2>&1 ;;
      armbian-bootlogo) [ -f "$a" ] && sed -i 's/^bootlogo=false/bootlogo=true/' "$a" ;;
      grub-theme) rm -rf /usr/share/grub/themes/nocapos; grub=1 ;;
      grub-cfg) rm -f /etc/default/grub.d/nocapos.cfg; grub=1 ;;
      divert) if has dpkg-divert; then dpkg-divert --local --rename --remove "$a" >/dev/null 2>&1 || true; fi ;;
      os-release) [ -x "$LIB/os-release-sync" ] && "$LIB/os-release-sync" --undo ;;
      usermask) [ "$(readlink "/etc/systemd/user/$a" 2>/dev/null)" = /dev/null ] && rm -f "/etc/systemd/user/$a" ;;
      autostart-hidden) grep -qs 'Hidden by NoCapOS' "$a" && rm -f "$a" ;;
      swapfile)
        swapoff "$a" >/dev/null 2>&1 || true
        rm -f "$a"
        [ -f /etc/fstab ] && grep -v "^$a " /etc/fstab >/etc/fstab.nocap.tmp && cat /etc/fstab.nocap.tmp >/etc/fstab
        rm -f /etc/fstab.nocap.tmp
        ;;
    esac
  done <"$BK/edits"
  if [ "$grub" -eq 1 ]; then
    if has update-grub; then update-grub >/dev/null 2>&1 || true
    elif has grub2-mkconfig && [ -f /boot/grub2/grub.cfg ]; then grub2-mkconfig -o /boot/grub2/grub.cfg >/dev/null 2>&1 || true
    elif has grub-mkconfig && [ -f /boot/grub/grub.cfg ]; then grub-mkconfig -o /boot/grub/grub.cfg >/dev/null 2>&1 || true
    fi
  fi
fi
if [ -f "$BK/pretty-hostname" ] && has hostnamectl; then
  hostnamectl set-hostname --pretty "$(cat "$BK/pretty-hostname")" >/dev/null 2>&1 || true
fi
rm -rf "$DATA/desktop-remote"
# Repair fallbacks (Xorg, software rendering) alfad applied.
rm -f "$DATA/desktop-fallback"
if [ -f /etc/environment ] && grep -qs 'NoCap Desktop software rendering' /etc/environment; then
  grep -v -e 'NoCap Desktop software rendering' -e '^LIBGL_ALWAYS_SOFTWARE=1$' /etc/environment >/etc/environment.nocap.tmp && cat /etc/environment.nocap.tmp >/etc/environment
  rm -f /etc/environment.nocap.tmp
fi
rm -f "$conf" /etc/nocapos/desktop-mode
rm -rf "$BK"
exit 0
