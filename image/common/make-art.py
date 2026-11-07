#!/usr/bin/env python3
"""Draws the NoCapOS boot and installer artwork from the brand files.

    python3 image/common/make-art.py OUT_DIR

Writes, all on the brand's near-black:
  isolinux-splash.png       640x480   BIOS boot menu of the ISO (syslinux)
  grub-live-splash.png      800x600   UEFI boot menu of the ISO (GRUB)
  grub-background.png       1280x720  GRUB menu of installed systems
  installer-banner.png      800x75    debian-installer banner (light theme)
  installer-banner-dark.png 800x75    debian-installer banner (dark theme)
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


def banner(mark, dark):
    w, h = 800, 75
    bg = BG if dark else (16, 16, 22)
    canvas = Image.new("RGBA", (w, h), bg + (255,))
    m = fit(mark, 60, 60)
    canvas.alpha_composite(m, (16, (h - m.height) // 2))
    d = ImageDraw.Draw(canvas)
    d.text((16 + m.width + 14, h // 2), "NoCapOS", font=font(30), fill=(255, 255, 255), anchor="lm")
    d.text((w - 16, h // 2), "Installer", font=font(16), fill=(229, 56, 59), anchor="rm")
    d.line([(0, h - 2), (w, h - 2)], fill=(229, 56, 59), width=2)
    return canvas.convert("RGB")


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else "art"
    os.makedirs(out, exist_ok=True)
    lockup = Image.open(os.path.join(BRAND, "nocapos-lockup.png")).convert("RGBA")
    mark = Image.open(os.path.join(BRAND, "mark-512.png")).convert("RGBA")
    # syslinux draws its menu from row ~12 (16px rows), so the art stays above.
    splash(lockup, (640, 480), 0.36, 0.03).save(os.path.join(out, "isolinux-splash.png"))
    splash(lockup, (800, 600), 0.44, 0.04).save(os.path.join(out, "grub-live-splash.png"))
    splash(lockup, (1280, 720), 0.52, 0.08).save(os.path.join(out, "grub-background.png"))
    banner(mark, False).save(os.path.join(out, "installer-banner.png"))
    banner(mark, True).save(os.path.join(out, "installer-banner-dark.png"))
    print("artwork written to", out)


if __name__ == "__main__":
    main()
