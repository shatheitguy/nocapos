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
# means the first of them the distribution has (newer names first, the
# older ones after). A line starting with "?" is optional: the desktop
# works without it. Installs skip "recommended" packages, so everything the
# screen needs to light up is listed: the compositor (labwc) and Xwayland,
# the Mesa drivers (with llvmpipe, the software renderer for VMs and boards
# without a GPU driver), the sign-in service (greetd) and logind's PAM
# module, and the libraries NoCap Shell's own programs use (GTK 3 with
# layer-shell, WebKitGTK 4.1). Apps are the light ones every target has:
# Thunar (files), foot (terminal), Mousepad (text), a web browser
# (desk_install_browser). Debian 12 has no labwc: NoCapOS brings its own
# build there, which needs wlroots 0.16 (libwlroots11).
desktop_packages() {
  case "$1" in
    debian | ubuntu)
      cat <<'EOF'
labwc|libwlroots11
greetd
xwayland
wayvnc
swayidle
libgtk-3-0t64|libgtk-3-0
libgtk-layer-shell0
libwebkit2gtk-4.1-0
libjson-glib-1.0-0
libpolkit-agent-1-0
libxkbcommon0
libpangocairo-1.0-0
libgdk-pixbuf-2.0-0|libgdk-pixbuf2.0-0
libpam0g
polkitd|policykit-1
xdg-desktop-portal-wlr
xdg-desktop-portal-gtk
xdg-user-dirs
gnome-keyring
libpam-gnome-keyring
dbus-user-session
pipewire
wireplumber|pipewire-media-session
?pipewire-pulse
thunar|pcmanfm
foot
mousepad
?ristretto
?grim
?libnotify-bin
?brightnessctl
adwaita-icon-theme
fonts-dejavu-core
?fonts-cantarell
flatpak
?libsecret-tools
?guacd
?libguac-client-rdp0|libguac-client-rdp
?libguac-client-vnc0|libguac-client-vnc
libgl1-mesa-dri
libegl-mesa0
libgbm1
libpam-systemd
?firmware-amd-graphics
?firmware-misc-nonfree
?wl-clipboard
?xclip
?systemd-zram-generator|zram-tools
EOF
      ;;
    fedora)
      cat <<'EOF'
labwc
greetd
xorg-x11-server-Xwayland
wayvnc
swayidle
gtk3
gtk-layer-shell
webkit2gtk4.1
json-glib
polkit
xdg-desktop-portal-wlr
xdg-desktop-portal-gtk
xdg-user-dirs
gnome-keyring
pipewire
wireplumber
Thunar
foot
mousepad
?grim
?brightnessctl
adwaita-icon-theme
dejavu-sans-fonts
flatpak
?guacd
?libguac-client-rdp
?libguac-client-vnc
mesa-dri-drivers
mesa-libEGL
mesa-libgbm
?wl-clipboard
?zram-generator
EOF
      ;;
    arch)
      cat <<'EOF'
labwc
greetd
xorg-xwayland
wayvnc
swayidle
gtk3
gtk-layer-shell
webkit2gtk-4.1
json-glib
polkit
xdg-desktop-portal-wlr
xdg-desktop-portal-gtk
xdg-user-dirs
gnome-keyring
pipewire
wireplumber
thunar
foot
mousepad
?grim
?brightnessctl
adwaita-icon-theme
ttf-dejavu
flatpak
mesa
?wl-clipboard
?zram-generator
EOF
      ;;
  esac
}

# OLD_DESKTOP_PACKAGES: what earlier versions' desktop ran on. Updates move
# the screen off it (NoCap Desktop is labwc + NoCap Shell now) and leave the
# packages installed: nothing anyone saved goes away. The image builds
# leave them out.
OLD_DESKTOP_PACKAGES="gdm3 gdm gnome-shell gnome-session gnome-remote-desktop"

# desktop_select FAMILY: picks the desktop's packages. stdin lists the
# package names this distribution has (one per line). Prints the picks, one
# per line, "!LINE" for each required line it has none of, and "?LINE" for
# each optional line it has none of.
desktop_select() {
  awk '
    FILENAME == ARGV[1] { gsub(/[ \t\r]/, ""); if ($0 != "") have[$0] = 1; next }
    $0 == "" { next }
    {
      line = $0; opt = 0
      if (substr(line, 1, 1) == "?") { opt = 1; line = substr(line, 2) }
      m = split(line, alt, "|"); pick = ""
      for (i = 1; i <= m; i++) if (alt[i] in have) { pick = alt[i]; break }
      if (pick != "") print pick
      else if (opt) print "?" line
      else print "!" line
    }
  ' /dev/stdin <(desktop_packages "$1")
}

# ---------------------------------------------------------------- small machines
# NoCap Desktop is the same full desktop on every machine: 64-bit or 32-bit
# (armhf), 1 GB or 64 GB. Small machines are tuned instead of getting a
# different desktop: compressed swap in memory (zram), a swap file when
# there's little memory and no swap, the heavy background services (file
# indexing, the calendar and contacts store, the software updater) off, and
# no animations.

# LOW_MEM_KB: below this (2 GB; boards sold as 2 GB report a little less)
# the desktop runs in its low-memory profile.
LOW_MEM_KB=2097152

# memory_profile MEM_KB: low or normal (unknown = normal).
memory_profile() {
  case "$1" in '' | *[!0-9]*) echo normal; return ;; esac
  if [ "$1" -lt "$LOW_MEM_KB" ]; then echo low; else echo normal; fi
}

# zram_size_mb MEM_KB: the compressed swap's size: as big as the memory on
# low-memory machines (it compresses about 3:1), half of it otherwise; 256
# MB to 4 GB.
zram_size_mb() {
  local mb
  case "$1" in '' | *[!0-9]*) echo 1024; return ;; esac
  mb=$(($1 / 1024))
  [ "$(memory_profile "$1")" = low ] || mb=$((mb / 2))
  [ "$mb" -le 4096 ] || mb=4096
  [ "$mb" -ge 256 ] || mb=256
  echo "$mb"
}

# render_zram_generator MEM_KB: /etc/systemd/zram-generator.conf.
render_zram_generator() {
  cat <<EOF
# NoCapOS: compressed swap in memory (written by install.sh).
[zram0]
zram-size = $(zram_size_mb "$1")
compression-algorithm = zstd
swap-priority = 100
EOF
}

# render_zramswap MEM_KB: /etc/default/zramswap (zram-tools).
render_zramswap() {
  cat <<EOF
# NoCapOS: compressed swap in memory (written by install.sh).
ALGO=zstd
SIZE=$(zram_size_mb "$1")
PRIORITY=100
EOF
}

# render_lowmem_sysctl: swap to zram early instead of dropping file cache.
render_lowmem_sysctl() {
  cat <<'EOF'
# NoCapOS low-memory profile (written by install.sh).
vm.swappiness=100
vm.page-cluster=0
EOF
}

# swapfile_mb MEM_KB: the disk swap file a machine gets when it has no disk
# swap (0 = none): low-memory machines only.
swapfile_mb() {
  if [ "$(memory_profile "$1")" = low ]; then echo 1024; else echo 0; fi
}

# render_clipboard_unit CAP: the desktop session's clipboard agent, a user
# service started with every graphical sign-in. It shares this account's
# clipboard with the person's phones through alfad's local socket (cap
# clipboard agent picks wl-clipboard or xclip for the session).
render_clipboard_unit() {
  cat <<EOF
# NoCapOS: shares the desktop clipboard with your own phones (written by install.sh).
[Unit]
Description=NoCap Desktop clipboard
PartOf=graphical-session.target
After=graphical-session.target

[Service]
ExecStart=$1 clipboard agent
Restart=on-failure
RestartSec=10

[Install]
WantedBy=graphical-session.target
EOF
}

# ---------------------------------------------------------------- the accent
# NoCap red in apps: each person's ~/.config/gtk-4.0/gtk.css and
# gtk-3.0/gtk.css get a NoCapOS block. It's a marked block, so a person's
# own CSS around it stays as it is.

NOCAP_RED=#e5383b
ACCENT_BEGIN='/* NoCapOS accent: begin (kept up to date by NoCapOS) */'
ACCENT_END='/* NoCapOS accent: end */'

# render_gtk4_accent: GTK 4 (libadwaita) accent colours.
render_gtk4_accent() {
  cat <<EOF
$ACCENT_BEGIN
@define-color accent_color #ff6b6e;
@define-color accent_bg_color $NOCAP_RED;
@define-color accent_fg_color #ffffff;
$ACCENT_END
EOF
}

# render_gtk3_accent: GTK 3's Adwaita (its colours are built in, so the
# widgets that show the accent are styled directly).
render_gtk3_accent() {
  cat <<EOF
$ACCENT_BEGIN
@define-color theme_selected_bg_color $NOCAP_RED;
@define-color theme_selected_fg_color #ffffff;
@define-color accent_bg_color $NOCAP_RED;
button.suggested-action, button.suggested-action:hover, button.suggested-action:active, button.suggested-action:checked {
  background-image: none; background-color: $NOCAP_RED; border-color: #b81f22; color: #ffffff;
}
switch:checked, progressbar progress, scale highlight, levelbar block.filled, check:checked, radio:checked, check:indeterminate, radio:indeterminate {
  background-image: none; background-color: $NOCAP_RED; border-color: #b81f22; color: #ffffff;
}
row:selected, .view:selected, iconview:selected, treeview.view:selected, flowboxchild:selected, entry selection, textview text selection, label selection, calendar:selected {
  background-color: $NOCAP_RED; color: #ffffff;
}
entry:focus, spinbutton:focus { border-color: $NOCAP_RED; box-shadow: inset 0 0 0 1px $NOCAP_RED; }
link, *:link, button.link > label { color: #ff6b6e; }
$ACCENT_END
EOF
}

# css_without_accent: a gtk.css on stdin without NoCapOS's block.
css_without_accent() {
  awk -v b="$ACCENT_BEGIN" -v e="$ACCENT_END" '
    $0 == b { skip = 1; next }
    skip && $0 == e { skip = 0; next }
    !skip { print }
  '
}

# css_with_accent BLOCK_FILE: a gtk.css on stdin with NoCapOS's block (the
# file's contents) in place of any earlier one, at the end.
css_with_accent() {
  local rest
  rest="$(css_without_accent)"
  if [ -n "$rest" ]; then printf '%s\n\n' "$rest"; fi
  cat "$1"
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

# nvidia_proprietary SYSROOT: the NVIDIA driver (not nouveau) is in use.
nvidia_proprietary() { [ -d "$1/sys/module/nvidia_drm" ] || [ -d "$1/sys/module/nvidia" ]; }

# PI_KMS_OVERLAY: the Raspberry Pi's KMS graphics driver. The compositor
# needs KMS; without it the screen only has the firmware's framebuffer.
PI_KMS_OVERLAY=dtoverlay=vc4-kms-v3d

# pi_kms_missing: a Raspberry Pi config.txt on stdin turns on no KMS
# graphics driver (vc4-kms-v3d or vc4-fkms-v3d, any variant).
pi_kms_missing() {
  awk '
    { s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s) }
    s ~ /^dtoverlay=vc4-(f)?kms-v3d/ { found = 1 }
    END { exit found }
  '
}

# config_txt_append LINE: a config.txt on stdin with LINE added under a
# final [all] section (config_txt_with checks the key; dtoverlay= lines
# repeat, so this one doesn't).
config_txt_append() {
  awk -v want="$1" '
    { lines[++n] = $0; s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s); if (s ~ /^\[/) last = s }
    END {
      for (i = 1; i <= n; i++) print lines[i]
      if (last != "" && last != "[all]") print "[all]"
      print want
    }
  '
}

# ---------------------------------------------------------------- the session
# The physical screen: greetd shows NoCap's own sign-in screen (NoCap Shell
# in greeter mode, in a labwc of its own, as greetd's unprivileged greeter
# account), then starts the NoCap Desktop session for whoever signed in:
# labwc + NoCap Shell (top bar, dock, launcher, notifications, control
# center, lock screen). Files live in:
#   /usr/local/lib/nocapos/shell     the programs (nocap-shell, nocap-lock,
#                                    nocap-session, nocap-greeter-session)
#   /usr/share/nocapos/shell         NoCap Shell's pages (local files only)
#   /etc/xdg/labwc                   labwc settings for every account
#   /usr/share/themes/NoCap          window decorations
#   /etc/greetd/config.toml          the sign-in service

NOCAP_SESSION=nocapos
SHELL_LIB=/usr/local/lib/nocapos/shell
SHELL_UI=/usr/share/nocapos/shell
NOCAP_WALLPAPER=/usr/share/backgrounds/nocapos/tide.jpg

# render_session_desktop: /usr/share/wayland-sessions/nocapos.desktop (for
# tools that list sessions; greetd itself starts nocap-session directly).
render_session_desktop() {
  cat <<EOF
[Desktop Entry]
# The NoCapOS desktop (written by NoCapOS's install.sh).
Name=NoCapOS
Comment=The NoCapOS desktop
Exec=$SHELL_LIB/nocap-session
TryExec=$SHELL_LIB/nocap-session
Type=Application
DesktopNames=NoCap;wlroots
EOF
}

# greeter_user ROOT: greetd's own unprivileged account on this system
# (Debian and Raspberry Pi OS: _greetd; others: greeter).
greeter_user() {
  if grep -qs '^_greetd:' "$1/etc/passwd"; then echo _greetd
  elif grep -qs '^greeter:' "$1/etc/passwd"; then echo greeter
  else echo _greetd
  fi
}

# render_greetd_config GREETER_USER [AUTOLOGIN_USER]: /etc/greetd/config.toml.
# With an AUTOLOGIN_USER the desktop starts for that account once per boot
# without the sign-in screen (off unless someone turns it on in Settings).
render_greetd_config() {
  cat <<EOF
# NoCap Desktop's sign-in service (written by NoCapOS; Settings changes it).
[terminal]
vt = 7

[default_session]
command = "$SHELL_LIB/nocap-greeter-session"
user = "$1"
EOF
  if [ -n "${2:-}" ]; then
    cat <<EOF

[initial_session]
command = "$SHELL_LIB/nocap-session"
user = "$2"
EOF
  fi
}

# greetd_autologin: the account a greetd config on stdin signs in
# automatically (empty when none).
greetd_autologin() {
  awk '
    { s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s) }
    s ~ /^\[/ { sec = s; next }
    sec == "[initial_session]" && s ~ /^user[ \t]*=/ {
      v = s; sub(/^user[ \t]*=[ \t]*/, "", v); gsub(/"/, "", v); print v; exit
    }
  '
}

# render_pam_lock FAMILY: /etc/pam.d/nocap-lock, the lock screen's password
# check (the account's own password, as at sign-in).
render_pam_lock() {
  case "$1" in
    debian | ubuntu) printf '#%%PAM-1.0\n# NoCap Desktop lock screen (written by NoCapOS).\n@include common-auth\n@include common-account\n' ;;
    *) printf '#%%PAM-1.0\n# NoCap Desktop lock screen (written by NoCapOS).\nauth include system-auth\naccount include system-auth\n' ;;
  esac
}

# pam_with_keyring: a greetd PAM file on stdin with the keyring added (it
# unlocks with the sign-in password; apps keep their secrets in it). Lines
# already there stay as they are.
pam_with_keyring() {
  awk '
    { lines[++n] = $0; if ($0 ~ /pam_gnome_keyring\.so/) has = 1 }
    END {
      for (i = 1; i <= n; i++) print lines[i]
      if (!has) {
        print "# NoCapOS: the keyring unlocks with the sign-in password."
        print "auth       optional   pam_gnome_keyring.so"
        print "session    optional   pam_gnome_keyring.so auto_start"
      }
    }
  '
}

# dock_apps ROOT: the dock's apps, one desktop file name per line, all
# native: Files, the browser, the NoCapOS App Store, the terminal, Text
# Editor and NoCapOS Settings (whichever of each this machine has under
# ROOT; the NoCapOS ones always).
dock_apps() {
  local d="$1/usr/share/applications" a
  first() { for a in "$@"; do if [ -f "$d/$a" ]; then echo "$a"; return 0; fi; done; return 0; }
  first thunar.desktop Thunar.desktop pcmanfm.desktop
  first firefox-esr.desktop firefox.desktop org.mozilla.firefox.desktop chromium.desktop chromium-browser.desktop
  echo nocapos-appstore.desktop
  first org.codeberg.dnkl.foot.desktop foot.desktop lxterminal.desktop
  first org.xfce.mousepad.desktop mousepad.desktop
  echo nocapos-settings.desktop
}

# json_list ITEM...: a JSON array of strings (items are desktop file names:
# letters, digits, dots, dashes, underscores).
json_list() {
  local out="" i
  for i in "$@"; do [ -n "$i" ] && out="$out, \"$i\""; done
  printf '[%s]\n' "${out#, }"
}

# render_app_entry NAME: the NoCapOS apps in the launcher and dock
# (nocapos-settings.desktop, nocapos-appstore.desktop).
render_app_entry() {
  local title comment icon
  case "$1" in
    settings) title="NoCapOS Settings"; comment="Settings for this NoCapOS machine"; icon=nocapos ;;
    appstore) title="NoCapOS App Store"; comment="Apps for this NoCapOS machine"; icon=nocapos ;;
    *) return 1 ;;
  esac
  cat <<EOF
[Desktop Entry]
# Written by NoCapOS.
Type=Application
Name=$title
Comment=$comment
Exec=$SHELL_LIB/nocap-shell --app $1
Icon=$icon
Categories=System;Settings;
StartupWMClass=$1
EOF
}

# render_portals_conf: which portal answers what in the NoCap session
# (screen sharing and screenshots: wlr; files and the rest: gtk).
render_portals_conf() {
  cat <<'EOF'
# NoCap Desktop (written by NoCapOS).
[preferred]
default=gtk
org.freedesktop.impl.portal.ScreenCast=wlr
org.freedesktop.impl.portal.Screenshot=wlr
org.freedesktop.impl.portal.Secret=gnome-keyring
EOF
}

# ---------------------------------------------------------------- earlier desktops
# gdm_config and gdm_unit (above) find what earlier versions set up, so an
# update can move the screen off it.

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
