# NoCap Desktop helpers, sourced by install.sh (and by the image builds).
# Everything here is pure: it reads files under a root folder ($1 where
# given, "" = this machine) or stdin, and prints. Nothing is installed or
# changed. backend/internal/deploytest runs these against fixtures.
# shellcheck shell=bash

# os_field ROOT KEY: a value from ROOT/etc/os-release (quotes removed).
os_field() {
  local f="$1/etc/os-release"
  [ -f "$f" ] || f="$1/usr/lib/os-release"
  [ -f "$f" ] || return 0
  sed -n "s/^$2=//p" "$f" | head -1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'\$/\1/"
}

# desktop_family ROOT: which package set this distribution gets.
#   debian    Debian 12/13 and Raspberry Pi OS (supported)
#   ubuntu    Ubuntu 22.04+ (supported)
#   fedora    Fedora (best effort)
#   arch      Arch Linux and derivatives (best effort)
#   none      anything else (not supported yet)
desktop_family() {
  local id like
  id="$(os_field "$1" ID)"
  like=" $(os_field "$1" ID_LIKE) "
  case "$id" in
    debian | raspbian) echo debian; return ;;
    ubuntu) echo ubuntu; return ;;
    fedora) echo fedora; return ;;
    arch | archarm | manjaro | endeavouros) echo arch; return ;;
  esac
  case "$like" in
    *" ubuntu "*) echo ubuntu ;;
    *" debian "*) echo debian ;;
    *" fedora "* | *" rhel "*) echo fedora ;;
    *" arch "*) echo arch ;;
    *) echo none ;;
  esac
}

# desktop_packages FAMILY: the desktop's packages, one per line. "a|b"
# means the first of them the distribution has (newer apps first, the
# older ones they replaced after). A line starting with "?" is optional:
# the desktop works without it. Only what the desktop needs: no games,
# office suite, mail, maps or other extras.
desktop_packages() {
  case "$1" in
    debian | ubuntu)
      cat <<'EOF'
gdm3
gnome-session
gnome-shell
gnome-control-center
nautilus
gnome-console|gnome-terminal
gnome-text-editor|gedit
loupe|eog
papers|evince
file-roller
gnome-system-monitor
xdg-desktop-portal-gnome
xdg-desktop-portal-gtk
xdg-user-dirs-gtk
gnome-keyring
libpam-gnome-keyring
dconf-cli
libglib2.0-bin
libsecret-tools
adwaita-icon-theme
fonts-cantarell
fonts-dejavu-core
xwayland
flatpak
?gnome-shell-extension-dashtodock|gnome-shell-extension-ubuntu-dock
?gnome-shell-extension-appindicator
?gnome-remote-desktop
?libglib2.0-dev-bin
?guacd
?libguac-client-rdp0|libguac-client-rdp
?libguac-client-vnc0|libguac-client-vnc
?xserver-xorg-core
?xserver-xorg-input-libinput
?wayvnc
?x11vnc
EOF
      ;;
    fedora)
      cat <<'EOF'
gdm
gnome-session
gnome-shell
gnome-control-center
nautilus
gnome-console|gnome-terminal
gnome-text-editor|gedit
loupe|eog
papers|evince
file-roller
gnome-system-monitor
xdg-desktop-portal-gnome
xdg-user-dirs-gtk
gnome-keyring
dconf
glib2
libsecret
adwaita-icon-theme
abattis-cantarell-fonts
xorg-x11-server-Xwayland
flatpak
?gnome-shell-extension-dash-to-dock
?gnome-shell-extension-appindicator
?gnome-remote-desktop
?guacd
?libguac-client-rdp
?libguac-client-vnc
?wayvnc
?x11vnc
EOF
      ;;
    arch)
      cat <<'EOF'
gdm
gnome-session
gnome-shell
gnome-control-center
nautilus
gnome-console|gnome-terminal
gnome-text-editor|gedit
loupe|eog
papers|evince
file-roller
gnome-system-monitor
xdg-desktop-portal-gnome
xdg-user-dirs-gtk
gnome-keyring
dconf
glib2
libsecret
adwaita-icon-theme
cantarell-fonts
xorg-xwayland
flatpak
?gnome-shell-extension-appindicator
?gnome-remote-desktop
?wayvnc
?x11vnc
EOF
      ;;
  esac
}

# desktop_feasible ARCH MEM_KB: prints why the full desktop isn't a good
# idea on this hardware (and returns 1), or nothing (returns 0).
desktop_feasible() {
  case "$1" in
    amd64 | arm64) ;;
    *) echo "the full desktop needs a 64-bit system (this one is $1)"; return 1 ;;
  esac
  case "$2" in '' | *[!0-9]*) return 0 ;; esac
  if [ "$2" -lt 1800000 ]; then
    echo "the full desktop needs at least 2 GB of memory (this machine has $(($2 / 1024)) MB)"
    return 1
  fi
  return 0
}

# boot_methods ROOT: how this machine boots, one per line (more than one is
# possible): pi (Raspberry Pi firmware: cmdline.txt/config.txt), grub,
# systemd-boot, uboot-menu (Debian's u-boot-menu: /etc/default/u-boot),
# extlinux (a hand-kept extlinux.conf), armbian (armbianEnv.txt).
boot_methods() {
  local r="$1" f
  for f in "$r/boot/firmware/cmdline.txt" "$r/boot/cmdline.txt"; do
    if [ -f "$f" ]; then echo pi; break; fi
  done
  if [ -f "$r/etc/default/grub" ]; then echo grub; fi
  for f in "$r/boot/efi/loader/loader.conf" "$r/efi/loader/loader.conf" "$r/boot/loader/loader.conf"; do
    if [ -f "$f" ]; then echo systemd-boot; break; fi
  done
  if [ -f "$r/etc/default/u-boot" ]; then
    echo uboot-menu
  elif [ -f "$r/boot/extlinux/extlinux.conf" ]; then
    echo extlinux
  fi
  if [ -f "$r/boot/armbianEnv.txt" ]; then echo armbian; fi
  return 0
}

# Kernel command line tokens for a quiet, branded boot. "splash" makes
# Plymouth draw; the rest hide boot text and firmware logos.
SPLASH_TOKENS="quiet splash loglevel=3 logo.nologo"

# var_tokens_with VAR TOKEN...: reads a shell-style KEY=VALUE file on stdin
# (/etc/default/u-boot, armbianEnv.txt) and prints it with the tokens added
# once to VAR's value, keeping its quoting; adds VAR= at the end if missing.
var_tokens_with() {
  local var="$1"
  shift
  awk -v var="$var" -v toks="$*" '
    function addtoks(v,   n, t, i) {
      n = split(toks, t, " ")
      for (i = 1; i <= n; i++) if ((" " v " ") !~ (" " t[i] " ")) v = (v == "" ? t[i] : v " " t[i])
      return v
    }
    index($0, var "=") == 1 && !done {
      v = substr($0, length(var) + 2); q = substr(v, 1, 1)
      if (q == "\"" || q == "\047") { v = substr(v, 2); sub(q "[[:space:]]*$", "", v) } else q = ""
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)
      v = addtoks(v)
      if (q == "" && v ~ / /) q = "\""
      print var "=" q v q; done = 1; next
    }
    { print }
    END { if (!done) { v = addtoks(""); print var "=" (v ~ / / ? "\"" v "\"" : v) } }
  '
}

# var_tokens_without VAR TOKEN...: the opposite, for uninstalling.
var_tokens_without() {
  local var="$1"
  shift
  awk -v var="$var" -v toks="$*" '
    index($0, var "=") == 1 {
      v = substr($0, length(var) + 2); q = substr(v, 1, 1)
      if (q == "\"" || q == "\047") { v = substr(v, 2); sub(q "[[:space:]]*$", "", v) } else q = ""
      n = split(v, w, " "); m = split(toks, t, " "); out = ""
      for (i = 1; i <= n; i++) {
        drop = 0
        for (j = 1; j <= m; j++) if (w[i] == t[j]) drop = 1
        if (!drop && w[i] != "") out = (out == "" ? w[i] : out " " w[i])
      }
      print var "=" q out q; next
    }
    { print }
  '
}

# keyword_tokens_with KEYWORD TOKEN...: reads a boot menu file on stdin
# (extlinux.conf: "append", systemd-boot entries: "options") and adds the
# tokens to every line starting with KEYWORD (case and indentation kept).
keyword_tokens_with() {
  local kw="$1"
  shift
  awk -v kw="$kw" -v toks="$*" '
    {
      line = $0; sub(/\r$/, "", line); t = line; sub(/^[ \t]+/, "", t)
      if (tolower(substr(t, 1, length(kw) + 1)) == kw " " || tolower(t) == kw) {
        n = split(toks, k, " ")
        for (i = 1; i <= n; i++) if ((" " line " ") !~ (" " k[i] " ")) line = line " " k[i]
      }
      print line
    }
  '
}

# keyword_tokens_without KEYWORD TOKEN...: takes them out again.
keyword_tokens_without() {
  local kw="$1"
  shift
  awk -v kw="$kw" -v toks="$*" '
    {
      line = $0; t = line; sub(/^[ \t]+/, "", t)
      if (tolower(substr(t, 1, length(kw) + 1)) == kw " ") {
        ind = substr(line, 1, length(line) - length(t))
        n = split(t, w, /[ \t]+/); m = split(toks, k, " "); out = ""
        for (i = 1; i <= n; i++) {
          drop = 0
          for (j = 1; j <= m; j++) if (w[i] == k[j]) drop = 1
          if (!drop) out = (out == "" ? w[i] : out " " w[i])
        }
        line = ind out
      }
      print line
    }
  '
}

# tokens_line_with TOKEN...: a one-line kernel command line on stdin
# (/etc/kernel/cmdline, Pi cmdline.txt) with each token added once.
tokens_line_with() (
  set -f
  local out="" w tok
  for w in $(tr '\r\n' '  '); do out="$out $w"; done
  for tok in "$@"; do
    case " $out " in *" $tok "*) ;; *) out="$out $tok" ;; esac
  done
  printf '%s\n' "${out# }"
)

# tokens_line_without TOKEN...: the opposite.
tokens_line_without() (
  set -f
  local out="" w tok keep
  for w in $(tr '\r\n' '  '); do
    keep=1
    for tok in "$@"; do [ "$w" = "$tok" ] && keep=0; done
    [ "$keep" -eq 0 ] || out="$out $w"
  done
  printf '%s\n' "${out# }"
)

# config_txt_with LINE: reads a Raspberry Pi config.txt on stdin and adds
# LINE (e.g. disable_splash=1) under a final [all] section unless the same
# setting is already there with that value. A different value is left
# alone (someone set it on purpose).
config_txt_with() {
  awk -v want="$1" '
    BEGIN { split(want, kv, "="); key = kv[1] }
    { lines[++n] = $0; s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s)
      if (index(s, key "=") == 1) has = 1
      if (s ~ /^\[/) last = s }
    END {
      for (i = 1; i <= n; i++) print lines[i]
      if (has) exit
      if (last != "" && last != "[all]") print "[all]"
      print want
    }
  '
}

# config_txt_without LINE: removes exactly that line again.
config_txt_without() {
  awk -v want="$1" '{ s = $0; sub(/[ \t\r]+$/, "", s); if (s != want) print }'
}

# gdm_config ROOT: GDM's settings file on this distribution.
gdm_config() {
  local f
  for f in /etc/gdm3/daemon.conf /etc/gdm3/custom.conf /etc/gdm/custom.conf; do
    if [ -f "$1$f" ]; then echo "$f"; return 0; fi
  done
  if [ -d "$1/etc/gdm3" ]; then echo /etc/gdm3/daemon.conf; else echo /etc/gdm/custom.conf; fi
}

# gdm_unit ROOT: GDM's systemd unit name.
gdm_unit() {
  local d u
  for u in gdm3.service gdm.service; do
    for d in /etc/systemd/system /usr/lib/systemd/system /lib/systemd/system; do
      if [ -f "$1$d/$u" ] && [ ! -L "$1$d/$u" ]; then echo "$u"; return 0; fi
    done
  done
  echo gdm.service
}

# gnome_major VERSION_TEXT: "GNOME Shell 48.4" -> 48 (0 when unknown).
gnome_major() {
  local v
  v="$(printf '%s\n' "$1" | sed -n 's/.*[^0-9]\([0-9][0-9]*\)\.[0-9].*/\1/p' | head -1)"
  echo "${v:-0}"
}

# nvidia_proprietary SYSROOT: the NVIDIA driver (not nouveau) is in use.
nvidia_proprietary() { [ -d "$1/sys/module/nvidia_drm" ] || [ -d "$1/sys/module/nvidia" ]; }

# ---------------------------------------------------------------- settings files
# NoCapOS's look for every account on this machine: GSettings defaults
# (/usr/share/glib-2.0/schemas/90_nocapos.gschema.override) for the keys
# every GNOME since 43 has, and the dconf system database
# (/etc/dconf/db/local.d) for the rest (newer keys, the dock, app folders),
# which never fails over a key an older GNOME doesn't know. People can
# change all of it; only the few keys in render_dconf_locks are locked.

NOCAP_WALLPAPER=/usr/share/backgrounds/nocapos/tide.jpg
NOCAP_BRAND_UUID=nocapos-brand@shatheitguy.github.io

# gv_list ITEM...: a GVariant string list, ['a', 'b'] (empty items skipped).
gv_list() {
  local out="" i
  for i in "$@"; do [ -n "$i" ] && out="$out, '$i'"; done
  printf '[%s]\n' "${out#, }"
}

# render_gschema_override TERMINAL_DESKTOP EXTENSION_UUID...
render_gschema_override() {
  local term="$1"
  shift
  cat <<EOF
# NoCapOS defaults for every account (written by NoCapOS's install.sh).
[org.gnome.desktop.background]
picture-uri='file://$NOCAP_WALLPAPER'
picture-uri-dark='file://$NOCAP_WALLPAPER'
picture-options='zoom'
primary-color='#050508'

[org.gnome.desktop.screensaver]
picture-uri='file://$NOCAP_WALLPAPER'
picture-options='zoom'
primary-color='#050508'

[org.gnome.desktop.interface]
color-scheme='prefer-dark'
gtk-theme='Adwaita-dark'

[org.gnome.desktop.wm.preferences]
button-layout='appmenu:minimize,maximize,close'

[org.gnome.shell]
favorite-apps=$(gv_list nocap-hub.desktop org.gnome.Nautilus.desktop "$term" nocap-appstore.desktop nocap-settings.desktop)
enabled-extensions=$(gv_list "$@")
EOF
}

# render_dconf_local TERMINAL_DESKTOP EXTENSION_UUID...: /etc/dconf/db/local.d/00-nocapos
render_dconf_local() {
  local term="$1" exts
  shift
  exts="$(gv_list "$@")"
  cat <<EOF
# NoCapOS defaults for every account (written by NoCapOS's install.sh).
[org/gnome/desktop/interface]
color-scheme='prefer-dark'
gtk-theme='Adwaita-dark'
accent-color='red'

[org/gnome/desktop/background]
picture-uri='file://$NOCAP_WALLPAPER'
picture-uri-dark='file://$NOCAP_WALLPAPER'
picture-options='zoom'
primary-color='#050508'

[org/gnome/desktop/screensaver]
picture-uri='file://$NOCAP_WALLPAPER'
picture-options='zoom'
primary-color='#050508'

[org/gnome/desktop/wm/preferences]
button-layout='appmenu:minimize,maximize,close'

[org/gnome/shell]
favorite-apps=$(gv_list nocap-hub.desktop org.gnome.Nautilus.desktop "$term" nocap-appstore.desktop nocap-settings.desktop)
enabled-extensions=$exts
disable-user-extensions=false
welcome-dialog-last-shown-version='999'

[org/gnome/shell/extensions/dash-to-dock]
dock-fixed=true
autohide=false
intellihide=false
dock-position='BOTTOM'
extend-height=false
dash-max-icon-size=48
show-trash=false
show-mounts=false
show-show-apps-button=true
apply-custom-theme=false
custom-background-color=true
background-color='#050508'
transparency-mode='FIXED'
background-opacity=0.7
running-indicator-style='DOTS'
custom-theme-customize-running-dots=true
custom-theme-running-dots-color='#e5383b'

[org/gnome/desktop/app-folders]
folder-children=['NoCapOS', 'Utilities', 'System']

[org/gnome/desktop/app-folders/folders/NoCapOS]
name='NoCapOS'
apps=['nocap-hub.desktop', 'nocap-appstore.desktop', 'nocap-files.desktop', 'nocap-settings.desktop']

[org/gnome/desktop/app-folders/folders/Utilities]
name='Utilities'
categories=['Utility', 'X-GNOME-Utilities', 'Archiving', 'TextEditor']

[org/gnome/desktop/app-folders/folders/System]
name='System'
categories=['System', 'Settings', 'Monitor']

[org/gnome/settings-daemon/plugins/power]
sleep-inactive-ac-type='nothing'
sleep-inactive-battery-type='nothing'

[org/gnome/software]
allow-updates=false
download-updates=false
download-updates-notify=false
first-run=false

[org/gnome/desktop/privacy]
report-technical-problems=false
EOF
}

# render_dconf_locks: the keys people can't change. A server must not put
# itself to sleep, and updates come from NoCapOS, never a second updater.
render_dconf_locks() {
  cat <<'EOF'
# Locked by NoCapOS (written by NoCapOS's install.sh).
/org/gnome/settings-daemon/plugins/power/sleep-inactive-ac-type
/org/gnome/software/allow-updates
/org/gnome/software/download-updates
EOF
}

# render_dconf_gdm: the sign-in screen (/etc/dconf/db/gdm.d/90-nocapos).
render_dconf_gdm() {
  cat <<'EOF'
# NoCapOS sign-in screen (written by NoCapOS's install.sh).
[org/gnome/login-screen]
logo='/usr/share/nocapos/desktop/login-logo.png'
banner-message-enable=false

[org/gnome/desktop/interface]
color-scheme='prefer-dark'
accent-color='red'

[org/gnome/settings-daemon/plugins/power]
sleep-inactive-ac-type='nothing'
sleep-inactive-battery-type='nothing'
EOF
}

# render_gdm_css: added to the sign-in screen's theme (the gdm-theme.gresource
# NoCapOS builds from the system's own).
render_gdm_css() {
  cat <<'EOF'

/* NoCapOS sign-in screen */
#lockDialogGroup {
  background: #050508 url("resource:///org/gnome/shell/theme/nocapos-login.jpg");
  background-size: cover;
  background-position: center;
}
.login-dialog .login-dialog-button.default-button,
.unlock-dialog .login-dialog-button.default-button { background-color: #e5383b; color: #ffffff; }
.login-dialog-prompt-entry:focus { border-color: #e5383b; }
EOF
}

# ini_set SECTION KEY VALUE: reads an ini file (GDM's daemon.conf) on stdin
# and prints it with KEY=VALUE in [SECTION]: a commented-out or existing
# KEY line is replaced, else it's added at the end of that section; the
# section is added if it's missing.
ini_set() {
  awk -v sec="[$1]" -v key="$2" -v val="$3" '
    function emit() { if (insec && !done) { print key "=" val; done = 1 } }
    { s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s); c = s; sub(/^#[ \t]*/, "", c) }
    s ~ /^\[/ { emit(); insec = (s == sec); if (insec) seen = 1; print; next }
    insec && index(c, key "=") == 1 {
      if (!done) { print key "=" val; done = 1 }
      next
    }
    { print }
    END { emit(); if (!seen) { print ""; print sec; print key "=" val } }
  '
}
