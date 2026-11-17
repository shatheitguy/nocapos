#!/usr/bin/env python3
"""security-check.py: does Debian have fixes for packages in our images?

  security-check.py PACKAGES.tsv... > fixes.json

Each PACKAGES.tsv is a released image's package list (Package, Version,
Source, Source version, Architecture: usr/share/nocapos/packages.tsv).
Compares every package with Debian 13's security archive and point-release
updates (deb.debian.org) and prints the fixes, grouped by source package,
with the CVE IDs from the fixed version's Debian changelog. Exit code 0 with
"[]" when nothing needs fixing.
"""
import json
import lzma
import re
import subprocess
import sys
import urllib.request

SUITES = [
    ("https://deb.debian.org/debian-security", "trixie-security"),
    ("https://deb.debian.org/debian", "trixie-updates"),
    ("https://deb.debian.org/debian", "trixie"),  # point releases
]
COMPONENTS = ["main", "contrib", "non-free-firmware", "non-free"]
UA = {"User-Agent": "nocapos-security-check"}


def fetch(url, binary=True):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=120) as r:
        data = r.read()
    return data if binary else data.decode("utf-8", "replace")


def index(base, suite, comp, arch):
    url = f"{base}/dists/{suite}/{comp}/binary-{arch}/Packages.xz"
    try:
        text = lzma.decompress(fetch(url)).decode("utf-8", "replace")
    except Exception as e:  # noqa: BLE001 - a missing component is fine
        print(f"skipped {url}: {e}", file=sys.stderr)
        return {}
    out = {}
    for stanza in text.split("\n\n"):
        f = dict(re.findall(r"^([A-Za-z-]+): (.*)$", stanza, re.M))
        if "Package" in f and "Version" in f:
            src = f.get("Source", f["Package"]).split(" ")[0]
            out[f["Package"]] = (f["Version"], src, comp)
    return out


def newer(a, b):
    return subprocess.run(["dpkg", "--compare-versions", a, "gt", b]).returncode == 0


def changelog_cves(src, version, comp):
    v = version.split(":", 1)[-1]
    prefix = src[:4] if src.startswith("lib") else src[0]
    url = f"https://metadata.ftp-master.debian.org/changelogs//{comp}/{prefix}/{src}/{src}_{v}_changelog"
    try:
        text = fetch(url, binary=False)
    except Exception:  # noqa: BLE001 - security uploads may not be there yet
        return [], ""
    top = text.split("\n -- ", 1)[0]
    cves = sorted(set(re.findall(r"CVE-\d{4}-\d{4,7}", top)))
    lines = [ln.strip(" *") for ln in top.splitlines()[1:] if ln.strip().startswith("*")]
    summary = lines[0][:160] if lines else ""
    return cves, summary


def main():
    have = {}
    archs = set()
    for path in sys.argv[1:]:
        for line in open(path, encoding="utf-8"):
            parts = line.rstrip("\n").split("\t")
            if len(parts) < 5:
                continue
            pkg, ver, src, _srcver, arch = parts[:5]
            have[(pkg, arch)] = (ver, src or pkg)
            if arch != "all":
                archs.add(arch)
    best = {}
    for arch in sorted(archs):
        for base, suite in SUITES:
            for comp in COMPONENTS:
                for pkg, (ver, src, c) in index(base, suite, comp, arch).items():
                    for a in (arch, "all"):
                        if (pkg, a) not in have:
                            continue
                        cur = have[(pkg, a)][0]
                        if newer(ver, cur) and ((pkg, a) not in best or newer(ver, best[(pkg, a)][0])):
                            best[(pkg, a)] = (ver, src, c, cur, suite)
    by_src = {}
    for (pkg, _a), (ver, src, comp, cur, suite) in best.items():
        e = by_src.setdefault(src, {"package": src, "from": cur, "to": ver, "comp": comp, "suite": suite, "binaries": set()})
        e["binaries"].add(pkg)
        if newer(ver, e["to"]):
            e["to"] = ver
    fixes = []
    for src, e in sorted(by_src.items()):
        cves, summary = changelog_cves(src, e["to"], e["comp"])
        fix = {"package": src, "from": e["from"], "to": e["to"], "cves": cves, "summary": summary,
               "security": e["suite"] == "trixie-security" or bool(cves)}
        fixes.append(fix)
    json.dump(fixes, sys.stdout, indent=2)
    sys.stdout.write("\n")
    print(f"{len(fixes)} source package(s) with updates, "
          f"{sum(1 for f in fixes if f['security'])} with security fixes", file=sys.stderr)


if __name__ == "__main__":
    main()
