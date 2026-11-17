#!/usr/bin/env python3
"""manifest.py: writes a release's nocapos-os.json (the system update
manifest Glow Up reads, see backend/internal/osimage/manifest.go).

  manifest.py --version 0.14.0 --tag v0.14.0 --channel stable \\
      --snapshot 20261115T000000Z --dir dist/ [--security fixes.json] \\
      [--min-from 0.13.0] [--notes notes.md] > nocapos-os.json

--dir holds the release files: nocapos-<target>.rugixb (+ .hash and
.signed markers) and nocapos-<target>.img.xz for each target.
"""
import argparse
import hashlib
import json
import os
import sys
from datetime import datetime, timezone

REPO = "https://github.com/shatheitguy/nocapos"
TARGETS = ("pc", "pi")


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", required=True)
    ap.add_argument("--tag", required=True)
    ap.add_argument("--channel", default="stable", choices=["stable", "beta"])
    ap.add_argument("--snapshot", default="")
    ap.add_argument("--dir", required=True)
    ap.add_argument("--security", help="JSON list of fixes (security refresh)")
    ap.add_argument("--min-from", default="")
    ap.add_argument("--notes", help="release notes (Markdown file)")
    a = ap.parse_args()

    targets = {}
    for t in TARGETS:
        bundle = f"nocapos-{t}.rugixb"
        path = os.path.join(a.dir, bundle)
        if not os.path.exists(path):
            continue
        hash_file = path + ".hash"
        bundle_hash = open(hash_file).read().strip() if os.path.exists(hash_file) else ""
        if not bundle_hash:
            sys.exit(f"{hash_file} is missing: the device can't verify {bundle} without it")
        entry = {
            "bundle": bundle,
            "url": f"{REPO}/releases/download/{a.tag}/{bundle}",
            "size": os.path.getsize(path),
            "sha256": sha256(path),
            "bundle_hash": bundle_hash,
            "signed": os.path.exists(path + ".signed"),
        }
        image = f"nocapos-{t}.img.xz"
        if os.path.exists(os.path.join(a.dir, image)):
            entry["image"] = image
            entry["image_size"] = os.path.getsize(os.path.join(a.dir, image))
        targets[t] = entry
    if not targets:
        sys.exit(f"no update bundles in {a.dir}")

    m = {
        "version": a.version,
        "channel": a.channel,
        "published": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "security": bool(a.security),
        "snapshot": a.snapshot,
        "targets": targets,
    }
    if a.min_from:
        m["min_from"] = a.min_from
    if a.notes and os.path.exists(a.notes):
        m["notes"] = open(a.notes, encoding="utf-8").read()[:20000]
    if a.security:
        m["fixes"] = json.load(open(a.security, encoding="utf-8"))
    json.dump(m, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
