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
?gnome-software
?gnome-software-plugin-flatpak
?gnome-shell-extension-dashtodock|gnome-shell-extension-ubuntu-dock
?gnome-shell-extension-appindicator
gnome-remote-desktop
pipewire
wireplumber|pipewire-media-session
dbus-user-session
?libglib2.0-dev-bin
?guacd
?libguac-client-rdp0|libguac-client-rdp
?libguac-client-vnc0|libguac-client-vnc
?xserver-xorg-core
?xserver-xorg-input-libinput
?wayvnc
x11vnc
?systemd-zram-generator|zram-tools
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
?gnome-software
?gnome-shell-extension-dash-to-dock
?gnome-shell-extension-appindicator
gnome-remote-desktop
pipewire
wireplumber
?guacd
?libguac-client-rdp
?libguac-client-vnc
?wayvnc
x11vnc
?zram-generator
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
?gnome-software
?gnome-shell-extension-appindicator
gnome-remote-desktop
pipewire
wireplumber
?wayvnc
x11vnc
?zram-generator
EOF
      ;;
  esac
}

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

# LOWMEM_USER_UNITS: the per-person background services the low-memory
# profile masks: file indexing (tracker, called localsearch from GNOME 47)
# and the calendar and contacts store. GNOME works without them: search
# still finds apps and settings, and the clock menu shows no events.
LOWMEM_USER_UNITS="tracker-miner-fs-3.service tracker-extract-3.service tracker-miner-fs-control-3.service tracker-miner-rss-3.service tracker-writeback-3.service localsearch-3.service localsearch-control-3.service localsearch-extractor-3.service localsearch-writeback-3.service evolution-source-registry.service evolution-calendar-factory.service evolution-addressbook-factory.service evolution-user-prompter.service"

# LOWMEM_AUTOSTART: autostart entries hidden in the low-memory profile.
LOWMEM_AUTOSTART="tracker-miner-fs-3.desktop localsearch-3.desktop org.gnome.Evolution-alarm-notify.desktop evolution-alarm-notify.desktop gnome-software-service.desktop org.gnome.Software.desktop"

# render_dconf_lowmem: /etc/dconf/db/local.d/10-nocapos-lowmem.
render_dconf_lowmem() {
  cat <<'EOF'
# NoCapOS low-memory profile (written by install.sh).
[org/gnome/desktop/interface]
enable-animations=false

[org/gnome/desktop/search-providers]
disable-external=true

[org/freedesktop/tracker/miner/files]
enable-monitors=false
crawling-interval=-2
index-recursive-directories=@as []
index-single-directories=@as []

[org/freedesktop/localsearch/miner/files]
enable-monitors=false
crawling-interval=-2
index-recursive-directories=@as []
index-single-directories=@as []
EOF
}

# render_autostart_hidden: an autostart entry that hides the system one.
render_autostart_hidden() {
  printf '[Desktop Entry]\n# Hidden by NoCapOS (low-memory profile).\nType=Application\nName=Hidden\nHidden=true\n'
}

# ---------------------------------------------------------------- the accent
# NoCap red in apps on every GNOME version. GNOME 47 and newer have an
# accent setting (accent-color='red' in render_dconf_local). Older GNOME
# (Debian 12 is 43) and every GTK 3 app don't, so each person's
# ~/.config/gtk-4.0/gtk.css and gtk-3.0/gtk.css get a NoCapOS block. It's a
# marked block, so a person's own CSS around it stays as it is.

NOCAP_RED=#e5383b
ACCENT_BEGIN='/* NoCapOS accent: begin (kept up to date by NoCapOS) */'
ACCENT_END='/* NoCapOS accent: end */'

# render_gtk4_accent: libadwaita's accent colours (GNOME 43-46).
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

# gtk4_accent_wanted GNOME_MAJOR: libadwaita needs the CSS block (GNOME 46
# and older; 0 = unknown, which gets it too). GNOME 47+ uses the setting,
# so a person's own accent choice in Settings works there.
gtk4_accent_wanted() { [ "$1" -lt 47 ]; }

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

# dock_apps ROOT: the dock's apps, one desktop file name per line, all of
# them native: Files, the browser, Software, the terminal, Text Editor and
# Settings (whichever of each this machine has under ROOT; Files and
# Settings always, they're required packages).
dock_apps() {
  local d="$1/usr/share/applications" a
  first() { for a in "$@"; do if [ -f "$d/$a" ]; then echo "$a"; return 0; fi; done; return 0; }
  echo org.gnome.Nautilus.desktop
  first firefox-esr.desktop firefox.desktop org.mozilla.firefox.desktop chromium.desktop chromium-browser.desktop \
    brave-browser.desktop com.brave.Browser.desktop google-chrome.desktop
  first org.gnome.Software.desktop
  first org.gnome.Console.desktop org.gnome.Terminal.desktop
  first org.gnome.TextEditor.desktop org.gnome.gedit.desktop
  if [ -f "$d/gnome-control-center.desktop" ] && [ ! -f "$d/org.gnome.Settings.desktop" ]; then
    echo gnome-control-center.desktop
  else
    echo org.gnome.Settings.desktop
  fi
}

# render_gschema_override DOCK_APPS EXTENSION_UUID...: DOCK_APPS is a
# GVariant list (gv_list of dock_apps).
render_gschema_override() {
  local favs="$1"
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
favorite-apps=$favs
enabled-extensions=$(gv_list "$@")
EOF
}

# render_dconf_local DOCK_APPS EXTENSION_UUID...: /etc/dconf/db/local.d/00-nocapos
render_dconf_local() {
  local favs="$1" exts
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
favorite-apps=$favs
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
folder-children=['Utilities', 'System']

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

# ---------------------------------------------------------------- the session
# The physical screen runs one session: NoCapOS (GNOME Shell with NoCapOS's
# look). GDM starts it by default, every account is moved to it, and other
# desktops' sessions (a distribution's own desktop, say) are taken out of
# the sign-in screen's list.

# NOCAP_SESSION: the session's name (its file is NOCAP_SESSION.desktop in
# /usr/share/wayland-sessions and /usr/share/xsessions).
NOCAP_SESSION=nocapos

# render_session_desktop wayland|x11: the session's .desktop file.
render_session_desktop() {
  local name=NoCapOS
  [ "$1" = x11 ] && name="NoCapOS (X11)"
  cat <<EOF
[Desktop Entry]
# The NoCapOS desktop (written by NoCapOS's install.sh).
Name=$name
Comment=The NoCapOS desktop
Exec=gnome-session
TryExec=gnome-session
Type=Application
DesktopNames=GNOME
X-GDM-SessionRegisters=true
EOF
}

# other_sessions ROOT: the session files under ROOT that aren't NoCapOS's,
# one path (without ROOT) per line.
other_sessions() {
  local d f
  for d in /usr/share/wayland-sessions /usr/share/xsessions; do
    for f in "$1$d"/*.desktop; do
      [ -f "$f" ] || continue
      [ "$(basename "$f")" = "$NOCAP_SESSION.desktop" ] && continue
      echo "$d/$(basename "$f")"
    done
  done
  return 0
}

# session_file_wants_change: an AccountsService user file (or ~/.dmrc) on
# stdin names a session other than NoCapOS's.
session_file_wants_change() {
  awk -v want="$NOCAP_SESSION" '
    { s = $0; sub(/^[ \t]+/, "", s); sub(/[ \t\r]+$/, "", s) }
    s ~ /^(Session|XSession)=/ { v = s; sub(/^[^=]*=/, "", v); if (v != want) bad = 1 }
    END { exit !bad }
  '
}

# session_file_with SECTION: the file on stdin with Session and XSession set
# to NoCapOS's in SECTION (User for AccountsService, Desktop for ~/.dmrc).
session_file_with() {
  ini_set "$1" Session "$NOCAP_SESSION" | ini_set "$1" XSession "$NOCAP_SESSION"
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
