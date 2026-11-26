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

# desktop_packages FAMILY: the packages NoCap Desktop itself needs, one per
# line, each with the reason it's there. "a|b" means the first of them the
# distribution has (newer names first). A line starting with "?" is
# optional: the desktop works without it. There are no third-party default
# apps: Files, Terminal, Text Editor, Photos, the players, Settings and the
# App Store are NoCap OS's own, and a web browser or anything else comes
# from the App Store (Flatpak) when someone wants one. Installs skip
# "recommended" packages, so everything the screen needs to light up is
# listed. labwc comes from the distribution: Debian 13, Raspberry Pi OS and
# Ubuntu 24.04 have it; Debian 12 and Ubuntu 22.04 don't, so NoCap Desktop
# needs Debian 13 there (desk_install_packages says so; the rest of NoCapOS
# works as it is).
desktop_packages() {
  desktop_package_list "$1" | sed -e 's/[[:space:]]*#.*$//' -e '/^$/d'
}

desktop_package_list() {
  case "$1" in
    debian | ubuntu)
      cat <<'EOF2'
labwc                                   # the compositor: windows, input, screens
?wayfire                                # the compositor with real blur and animated windows, like the web OS (labwc runs where it can't)
greetd                                  # the sign-in service (NoCap's sign-in screen runs in it)
xwayland                                # apps that only speak X11 open in the session
wayvnc                                  # "Open screen" in NoCapOS shows this screen in the browser
swayidle                                # locks the screen when idle and before sleep
libgtk-3-0t64|libgtk-3-0                # NoCap Shell's windows
libgtk-layer-shell0                     # NoCap Shell's desktop, top bar and dock layers
libwebkit2gtk-4.1-0                     # draws NoCap OS natively, from local files
libjson-glib-1.0-0                      # NoCap Shell's checked messages
libpolkit-agent-1-0                     # NoCap's admin password prompt
libxkbcommon0                           # the lock screen's keyboard
libpangocairo-1.0-0                     # the lock screen's text
libgdk-pixbuf-2.0-0|libgdk-pixbuf2.0-0  # the lock screen's wallpaper
libpam0g                                # the lock screen's password check
polkitd|policykit-1                     # who may do admin things
xdg-desktop-portal                      # file open/save and screen sharing for installed apps
xdg-desktop-portal-wlr                  # screen sharing and screenshots in labwc
xdg-desktop-portal-gtk                  # the open/save dialog for installed apps
xdg-user-dirs                           # Documents, Pictures, Downloads in each home
gnome-keyring                           # the passwords installed apps save (unlocks at sign-in)
libpam-gnome-keyring                    # unlocks it with the sign-in password
dbus-user-session                       # each person's session bus and services
pipewire                                # sound and screen capture
wireplumber|pipewire-media-session      # connects sound devices
?pipewire-pulse                         # sound for apps that use PulseAudio
wl-clipboard                            # the clipboard shared with your phones
?grim                                   # screenshots (the Print key)
?brightnessctl                          # screen brightness on laptops
adwaita-icon-theme                      # cursors and icons for installed apps
fonts-dejavu-core                       # a font every app can fall back to
flatpak                                 # the App Store's Linux apps
?guacd                                  # "Open screen": the browser view (else a container)
?libguac-client-vnc0|libguac-client-vnc # "Open screen": the VNC client inside it
libgl1-mesa-dri                         # graphics drivers, with the software renderer
libegl-mesa0                            # graphics drivers (EGL)
libgbm1                                 # graphics buffers for the compositor
libpam-systemd                          # a seat session for the screen
?firmware-amd-graphics                  # AMD graphics firmware
?firmware-misc-nonfree                  # Intel and other graphics firmware
?systemd-zram-generator|zram-tools      # compressed swap on small machines
?plymouth                               # the NoCapOS boot splash: start, shutdown, restart, disk password
?plymouth-themes                        # the splash's graphics plugins (the NoCapOS splash is a script theme)
?plymouth-label                         # the splash's text: the status line and the disk password question
EOF2
      ;;
    fedora)
      cat <<'EOF2'
labwc                       # the compositor
greetd                      # the sign-in service
xorg-x11-server-Xwayland    # X11 apps
wayvnc                      # "Open screen"
swayidle                    # idle lock
gtk3                        # NoCap Shell's windows
gtk-layer-shell             # NoCap Shell's layers
webkit2gtk4.1               # draws NoCap OS natively
json-glib                   # NoCap Shell's messages
polkit                      # admin prompts
xdg-desktop-portal          # file dialogs for installed apps
xdg-desktop-portal-wlr      # screen sharing
xdg-desktop-portal-gtk      # open/save dialog
xdg-user-dirs               # home folders
gnome-keyring               # saved passwords
pipewire                    # sound
wireplumber                 # sound devices
wl-clipboard                # shared clipboard
?grim                       # screenshots
?brightnessctl              # brightness
adwaita-icon-theme          # cursors and icons
dejavu-sans-fonts           # fallback font
flatpak                     # App Store Linux apps
?guacd                      # "Open screen"
?libguac-client-vnc         # "Open screen"
mesa-dri-drivers            # graphics drivers
mesa-libEGL                 # graphics drivers
mesa-libgbm                 # graphics buffers
?zram-generator             # compressed swap
EOF2
      ;;
    arch)
      cat <<'EOF2'
labwc                       # the compositor
greetd                      # the sign-in service
xorg-xwayland               # X11 apps
wayvnc                      # "Open screen"
swayidle                    # idle lock
gtk3                        # NoCap Shell's windows
gtk-layer-shell             # NoCap Shell's layers
webkit2gtk-4.1              # draws NoCap OS natively
json-glib                   # NoCap Shell's messages
polkit                      # admin prompts
xdg-desktop-portal          # file dialogs for installed apps
xdg-desktop-portal-wlr      # screen sharing
xdg-desktop-portal-gtk      # open/save dialog
xdg-user-dirs               # home folders
gnome-keyring               # saved passwords
pipewire                    # sound
wireplumber                 # sound devices
wl-clipboard                # shared clipboard
?grim                       # screenshots
?brightnessctl              # brightness
adwaita-icon-theme          # cursors and icons
ttf-dejavu                  # fallback font
flatpak                     # App Store Linux apps
mesa                        # graphics drivers
?zram-generator             # compressed swap
EOF2
      ;;
  esac
}

# OLD_DESKTOP_PACKAGES: what earlier versions' desktop ran on. Updates move
# the screen off it (NoCap Desktop is labwc + NoCap Shell now) and leave the
# packages installed: nothing anyone saved goes away. The image builds
# leave them out.
OLD_DESKTOP_PACKAGES="gdm3 gdm gnome-shell gnome-session gnome-remote-desktop"

# OLD_DISPLAY_MANAGERS: sign-in services that step aside for greetd (the
# earlier versions' one and the distributions' own).
OLD_DISPLAY_MANAGERS="gdm3.service gdm.service lightdm.service sddm.service lxdm.service slim.service xdm.service nodm.service"

# shell_build_packages FAMILY: what building NoCap Shell's two programs on
# the machine itself needs (the release bundle carries them built for
# amd64, arm64 and armv7; this is the fallback when those can't run here).
shell_build_packages() {
  case "$1" in
    debian | ubuntu) echo "make gcc pkg-config libgtk-3-dev libgtk-layer-shell-dev libwebkit2gtk-4.1-dev libjson-glib-dev libpolkit-agent-1-dev libwayland-dev wayland-protocols libxkbcommon-dev libcairo2-dev libpango1.0-dev libgdk-pixbuf-2.0-dev|libgdk-pixbuf2.0-dev libpam0g-dev" ;;
    fedora) echo "make gcc pkgconf-pkg-config gtk3-devel gtk-layer-shell-devel webkit2gtk4.1-devel json-glib-devel polkit-devel wayland-devel wayland-protocols-devel libxkbcommon-devel cairo-devel pango-devel gdk-pixbuf2-devel pam-devel" ;;
    arch) echo "make gcc pkgconf gtk3 gtk-layer-shell webkit2gtk-4.1 json-glib polkit wayland wayland-protocols libxkbcommon cairo pango gdk-pixbuf2 pam" ;;
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

NOCAP_RED=#e8232b
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
  background-image: none; background-color: $NOCAP_RED; border-color: #b51b22; color: #ffffff;
}
switch:checked, progressbar progress, scale highlight, levelbar block.filled, check:checked, radio:checked, check:indeterminate, radio:indeterminate {
  background-image: none; background-color: $NOCAP_RED; border-color: #b51b22; color: #ffffff;
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
# Plymouth draw; the rest hide the kernel's and systemd's boot text (also
# in the initramfs), the blinking console cursor and the kernel's logo.
# Settings → Show boot details takes them out again (nocap-boot).
SPLASH_TOKENS="quiet splash loglevel=3 rd.systemd.show_status=false systemd.show_status=false rd.udev.log_level=3 vt.global_cursor_default=0 logo.nologo"

# Where NoCapOS's GRUB files live: the theme, its art and the plain
# background GRUB shows while the menu stays hidden.
GRUB_THEME_DIR=/usr/share/grub/themes/nocapos

# render_grub_dropin silent|details: /etc/default/grub.d/nocapos.cfg, which
# update-grub reads after /etc/default/grub (that file stays as it is).
# silent: the menu stays hidden unless Shift (or Esc) is held at power-on,
# NoCap's plain background replaces any distribution wallpaper, Linux keeps
# GRUB's graphics mode (no flicker), and the kernel line gets the quiet
# tokens. details (Settings → Show boot details): the menu shows for five
# seconds and the quiet tokens come out, even ones /etc/default/grub has.
# Each token ends up there once, however often this is applied.
render_grub_dropin() {
  local mode="${1:-silent}" add="" t strip=""
  for t in $SPLASH_TOKENS; do strip="$strip|$t"; done
  strip="${strip#|}"
  [ "$mode" = silent ] && add=" $SPLASH_TOKENS"
  cat <<EOF
# NoCapOS boot (written by NoCapOS; Settings → Screen Real Estate → Show boot
# details switches it). update-grub reads it after /etc/default/grub.
GRUB_DISTRIBUTOR="NoCapOS"
GRUB_THEME="$GRUB_THEME_DIR/theme.txt"
GRUB_BACKGROUND="$GRUB_THEME_DIR/boot.png"
GRUB_GFXMODE=auto
GRUB_GFXPAYLOAD_LINUX=keep
EOF
  if [ "$mode" = silent ]; then
    cat <<'EOF'
# The menu stays hidden: hold Shift (or Esc) at power-on to see it.
GRUB_TIMEOUT_STYLE=hidden
GRUB_TIMEOUT=0
EOF
  else
    cat <<'EOF'
# Boot details on: the menu shows for a few seconds.
GRUB_TIMEOUT_STYLE=menu
GRUB_TIMEOUT=5
EOF
  fi
  cat <<EOF
# The kernel line: what /etc/default/grub has without NoCap's quiet tokens,
# then NoCap's quiet tokens once (none with boot details on).
nocap_line=""
case \$- in *f*) nocap_glob=off ;; *) nocap_glob=on; set -f ;; esac
for nocap_w in \$GRUB_CMDLINE_LINUX_DEFAULT; do
  case "\$nocap_w" in $strip) ;; *) nocap_line="\$nocap_line \$nocap_w" ;; esac
done
[ "\$nocap_glob" = off ] || set +f
GRUB_CMDLINE_LINUX_DEFAULT="\${nocap_line# }$add"
GRUB_CMDLINE_LINUX_DEFAULT="\${GRUB_CMDLINE_LINUX_DEFAULT# }"
unset nocap_line nocap_w nocap_glob
EOF
}

# grub_reads_dropins ROOT: this GRUB reads /etc/default/grub.d/*.cfg
# (Debian, Ubuntu and their derivatives do).
grub_reads_dropins() {
  grep -qs 'default/grub\.d' "$1/usr/sbin/grub-mkconfig" || [ -d "$1/etc/default/grub.d" ]
}

# grub_dropin_mode: the mode of a nocapos.cfg on stdin (silent or details;
# empty for an older one, written before the menu could hide).
grub_dropin_mode() {
  awk '
    /^GRUB_TIMEOUT_STYLE=hidden/ { m = "silent" }
    /^GRUB_TIMEOUT_STYLE=menu/ { m = "details" }
    END { if (m != "") print m }
  '
}

# kms_modules ROOT: the graphics drivers a PC loads first thing in its
# initramfs, so the boot splash comes up early and at the screen's own
# resolution: one per kind of graphics card in ROOT/sys (Intel: i915, AMD:
# amdgpu, NVIDIA without its own driver: nouveau). Virtual machines and
# anything else use the firmware's framebuffer (simpledrm, built into the
# kernel), which needs nothing.
kms_modules() {
  local d out=""
  for d in "$1"/sys/bus/pci/devices/*; do
    [ -f "$d/class" ] && [ -f "$d/vendor" ] || continue
    case "$(cat "$d/class")" in 0x03*) ;; *) continue ;; esac
    case "$(cat "$d/vendor")" in
      0x8086) out="$out i915" ;;
      0x1002) out="$out amdgpu" ;;
      0x10de) nvidia_proprietary "$1" || out="$out nouveau" ;;
    esac
  done
  # shellcheck disable=SC2086 # a list of names
  [ -z "$out" ] || printf '%s\n' $out | sort -u
}

# render_kms_modules MODULE...: initramfs-tools' module list
# (/usr/share/initramfs-tools/modules.d/nocapos-kms) that loads them early.
render_kms_modules() {
  echo "# NoCapOS (written by NoCapOS): graphics drivers loaded first thing, so the"
  echo "# boot splash shows early and sharp. Rewritten on every NoCapOS update."
  printf '%s\n' "$@"
}

# render_greetd_dropin: greetd.service.d/nocapos.conf. The sign-in screen
# takes the first console over from the boot splash (vt 1, see
# render_greetd_config): no text login there while it runs, and the text
# login comes back if the sign-in screen keeps failing. It never runs next
# to another sign-in service either: starting it stops them first, so two
# never fight over the screen. alfad's Repair writes the same file
# (hostaccess.GreetdDropIn).
render_greetd_dropin() {
  cat <<EOF
# NoCap Desktop (written by NoCapOS): the sign-in screen takes the first
# console over from the boot splash, and never runs next to another sign-in
# service (starting it stops them first).
[Unit]
After=plymouth-quit-wait.service systemd-user-sessions.service getty@tty1.service
Conflicts=getty@tty1.service
OnFailure=getty@tty1.service
Conflicts=$OLD_DISPLAY_MANAGERS
After=$OLD_DISPLAY_MANAGERS
EOF
}

# render_plymouth_quit_dropin: plymouth-quit.service.d/nocapos.conf. When
# the sign-in screen comes next, the boot splash leaves its last frame on
# the screen (--retain-splash) until the sign-in screen draws over it: no
# console, no black flash in between. Without it (no NoCap Desktop on this
# screen), the splash quits as usual and the text console shows.
render_plymouth_quit_dropin() {
  cat <<'EOF'
# NoCap Desktop (written by NoCapOS): the splash stays until the sign-in
# screen draws over it.
[Service]
ExecStart=
ExecStart=-/bin/sh -c 'if [ "$$(systemctl get-default)" = graphical.target ] && systemctl -q is-enabled greetd.service; then exec plymouth quit --retain-splash; else exec plymouth quit; fi'
EOF
}

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

# tokens_missing LINE TOKEN...: the tokens LINE doesn't have yet (the same
# as install.sh's, for nocap-boot, which runs without it).
tokens_missing() (
  set -f
  local line=" $1 " tok out=""
  shift
  for tok in "$@"; do case "$line" in *" $tok "*) ;; *) out="$out $tok" ;; esac; done
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
# labwc (windows) + NoCap Shell, which shows NoCap OS itself on the screen:
# the same menu bar, dock, launcher, panels, widgets and apps as in the
# browser, from local files, plus the lock screen. Files live in:
#   /usr/libexec/nocapos             the programs (nocap-shell, nocap-lock,
#                                    nocap-session, nocap-greeter-session)
#   /usr/share/nocapos/shell         NoCap OS's pages for the screen
#   /usr/share/nocapos/labwc         labwc settings for every account's
#                                    session (labwc -C: a person's own or a
#                                    distribution's labwc settings never
#                                    replace NoCap Desktop's)
#   /usr/share/nocapos/greeter/labwc labwc settings for the sign-in screen
#   /etc/systemd/user                the session's services (shell, idle lock)
#   /usr/share/themes/NoCap          window decorations
#   /etc/greetd/config.toml          the sign-in service

NOCAP_SESSION=nocapos
SHELL_LIB=/usr/libexec/nocapos
SHELL_UI=/usr/share/nocapos/shell
SHELL_CONF=/usr/share/nocapos/labwc
WAYFIRE_CONF_DIR=/usr/share/nocapos/wayfire # the compositor with blur and animations (labwc when it can't start)
GREETER_CONF=/usr/share/nocapos/greeter/labwc
GREETD_CONF=/etc/greetd/config.toml
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
# (Debian and Raspberry Pi OS: _greetd; Fedora: greetd; others: greeter).
greeter_user() {
  if grep -qs '^_greetd:' "$1/etc/passwd"; then echo _greetd
  elif grep -qs '^greetd:' "$1/etc/passwd"; then echo greetd
  elif grep -qs '^greeter:' "$1/etc/passwd"; then echo greeter
  else echo _greetd
  fi
}

# render_greetd_config GREETER_USER [AUTOLOGIN_USER]: /etc/greetd/config.toml.
# With an AUTOLOGIN_USER the desktop starts for that account once per boot
# without the sign-in screen (off unless someone turns it on in Settings).
# It runs on the first console (vt 1), where the boot splash is: there's no
# switch to another console, so nothing flashes in between.
render_greetd_config() {
  cat <<EOF
# NoCap Desktop's sign-in service (written by NoCapOS; Settings changes it).
[terminal]
vt = 1

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

# render_portals_conf: which portal answers what in the NoCap session
# (screen sharing and screenshots: wlr; files and the rest: gtk). Inhibit
# (an app keeping the screen awake) goes to gtk, which hands it on to
# org.freedesktop.ScreenSaver: NoCap Shell answers that.
render_portals_conf() {
  cat <<'EOF'
# NoCap Desktop (written by NoCapOS).
[preferred]
default=gtk
org.freedesktop.impl.portal.ScreenCast=wlr
org.freedesktop.impl.portal.Screenshot=wlr
org.freedesktop.impl.portal.Secret=gnome-keyring
org.freedesktop.impl.portal.Inhibit=gtk
EOF
}

