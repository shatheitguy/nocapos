#!/bin/bash
# Points apt at one snapshot.debian.org date (main, contrib for ZFS,
# non-free-firmware) plus the debian-security snapshot of the same date,
# and keeps every later install small: no recommends, no suggests, no docs,
# man pages or translations NoCapOS doesn't use.
set -euo pipefail

SNAP="${RECIPE_PARAM_SNAPSHOT}"
case "$SNAP" in
  [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z) ;;
  *) echo "bad snapshot date: $SNAP" >&2; exit 1 ;;
esac

cat >/etc/apt/apt.conf.d/90nocapos <<'CONF'
// NoCapOS image build: snapshot sources, smallest installs.
APT::Install-Recommends "false";
APT::Install-Suggests "false";
Acquire::Check-Valid-Until "false";
Acquire::Retries "8";
Acquire::http::Timeout "60";
Acquire::Languages "none";
Dpkg::Use-Pty "false";
CONF

mkdir -p /etc/dpkg/dpkg.cfg.d
cat >/etc/dpkg/dpkg.cfg.d/90nocapos-slim <<'CONF'
# NoCapOS: no documentation, man pages or translations in the image.
# Copyright files stay (licences travel with the packages).
path-exclude=/usr/share/doc/*
path-include=/usr/share/doc/*/copyright
path-exclude=/usr/share/man/*
path-exclude=/usr/share/info/*
path-exclude=/usr/share/lintian/*
path-exclude=/usr/share/linda/*
path-exclude=/usr/share/locale/*
path-include=/usr/share/locale/en/*
path-include=/usr/share/locale/en_GB/*
path-include=/usr/share/locale/locale.alias
path-exclude=/usr/share/help/*
path-exclude=/usr/share/gtk-doc/*
path-exclude=/usr/share/gnome/help/*
CONF

base="https://snapshot.debian.org/archive"
rm -f /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources
cat >/etc/apt/sources.list <<LIST
# NoCapOS image build: Debian 13 (trixie) as of ${SNAP}.
deb ${base}/debian/${SNAP}/ trixie main contrib non-free non-free-firmware
deb ${base}/debian/${SNAP}/ trixie-updates main contrib non-free non-free-firmware
deb ${base}/debian-security/${SNAP}/ trixie-security main contrib non-free non-free-firmware
LIST
mkdir -p /usr/lib/nocapos
cp /etc/apt/sources.list /usr/lib/nocapos/build-sources.list

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
