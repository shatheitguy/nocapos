#!/usr/bin/env python3
"""Draws the NoCapOS boot and installer artwork from the brand files.

    python3 image/common/make-art.py OUT_DIR

Writes, all on the brand's near-black:
  isolinux-splash.png       640x480   BIOS boot menu of the ISO (syslinux)
  grub-live-splash.png      800x600   UEFI boot menu of the ISO (GRUB)
  calamares-welcome.png     720x300   graphical installer's welcome page
  calamares-logo.png        256x256   graphical installer's logo and window icon
The installed system's GRUB art comes from scripts/make-boot-art.py.
Needs Pillow (Debian: python3-pil) and fonts-dejavu-core.
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BRAND = os.path.join(ROOT, "frontend", "public", "brand")
BG = (5, 5, 8)
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"


def font(size):
    try:
        return ImageFont.truetype(FONT, size)
    except OSError:
        return ImageFont.load_default()


def fit(img, max_w, max_h):
    scale = min(max_w / img.width, max_h / img.height)
    return img.resize((max(1, int(img.width * scale)), max(1, int(img.height * scale))), Image.LANCZOS)


def splash(lockup, size, box_h, top):
    """The lockup (mark + wordmark) centred in the top part of the screen;
    the boot menu sits below it."""
    w, h = size
    canvas = Image.new("RGBA", size, BG + (255,))
    art = fit(lockup, int(w * 0.6), int(h * box_h))
    canvas.alpha_composite(art, ((w - art.width) // 2, int(h * top)))
    return canvas.convert("RGB")


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else "art"
    os.makedirs(out, exist_ok=True)
    lockup = Image.open(os.path.join(BRAND, "nocapos-lockup.png")).convert("RGBA")
    mark = Image.open(os.path.join(BRAND, "mark-512.png")).convert("RGBA")
    # syslinux draws its menu from row ~12 (16px rows), so the art stays above.
    splash(lockup, (640, 480), 0.36, 0.03).save(os.path.join(out, "isolinux-splash.png"))
    splash(lockup, (800, 600), 0.44, 0.04).save(os.path.join(out, "grub-live-splash.png"))
    welcome = Image.new("RGBA", (720, 300), (0, 0, 0, 0))
    art = fit(lockup, 640, 220)
    welcome.alpha_composite(art, ((720 - art.width) // 2, (300 - art.height) // 2))
    welcome.save(os.path.join(out, "calamares-welcome.png"))
    fit(mark, 256, 256).save(os.path.join(out, "calamares-logo.png"))
    print("artwork written to", out)


if __name__ == "__main__":
    main()
