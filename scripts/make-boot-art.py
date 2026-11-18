#!/usr/bin/env python3
"""Draws NoCapOS's boot artwork from the brand files (run it after the
logo changes and commit the result):

    python3 scripts/make-boot-art.py

deploy/desktop/art/
  grub-background.png  1280x720  the boot menu (shown while Shift or Esc is
                                 held): the NoCap logo at a medium size, a
                                 little above the middle, on near-black
  grub-boot.png        16x16     the plain near-black GRUB shows while the
                                 menu stays hidden (in place of any
                                 distribution wallpaper)
deploy/plymouth/                 the boot splash (nocapos.script)
  logo.png             the same logo, drawn at 4K size (scaled down on load)
  shine-0..N.png       the light sweep across the logo's chrome, one frame
                       each, already cut to the logo's shape
  halo.png             the soft red glow behind the logo
  shadow.png           the red shadow the logo floats over
  entry.png, bullet.png, lock.png   the disk password prompt
Needs Pillow.
"""
import math
import os

from PIL import Image, ImageChops, ImageDraw, ImageFilter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BRAND = os.path.join(ROOT, "frontend", "public", "brand")
ART = os.path.join(ROOT, "deploy", "desktop", "art")
SPLASH = os.path.join(ROOT, "deploy", "plymouth")
BG = (5, 5, 8)
RED = (229, 56, 59)

# The logo's height as a share of the screen's, and where its middle sits
# (from the top). nocapos.script uses the same two numbers, so the splash
# lines up with the boot menu.
LOGO_H = 0.2
LOGO_Y = 0.42
SHINE_FRAMES = 20


def fit_h(img, h):
    w = max(1, round(img.width * h / img.height))
    return img.resize((w, h), Image.LANCZOS)


def save(img, path):
    img.save(path, optimize=True)
    print("  ", os.path.relpath(path, ROOT), img.size)


def grub_background(lockup):
    w, h = 1280, 720
    canvas = Image.new("RGBA", (w, h), BG + (255,))
    art = fit_h(lockup, round(h * LOGO_H))
    canvas.alpha_composite(art, ((w - art.width) // 2, round(h * LOGO_Y - art.height / 2)))
    return canvas.convert("RGB")


def shine_frame(alpha, k):
    """The light sweep at step k of SHINE_FRAMES: a soft white band at 105
    degrees (as on the update and restart screens), only where the logo is."""
    w, h = alpha.size
    t = k / (SHINE_FRAMES - 1)
    # The band's centre runs from well left of the logo to well right of it.
    centre = -0.22 * w + t * 1.44 * w
    half = 0.11 * w
    slope = math.tan(math.radians(15))  # 105deg CSS gradient: tilted 15deg off vertical
    band = Image.new("L", (w, h), 0)
    px = band.load()
    for y in range(h):
        shift = (y - h / 2) * slope
        for x in range(w):
            d = abs(x - (centre - shift)) / half
            if d < 1:
                px[x, y] = round(255 * 0.75 * (1 - d) ** 1.6)
    a = ImageChops.multiply(band, alpha)
    out = Image.new("RGBA", (w, h), (255, 255, 255, 0))
    out.putalpha(a)
    return out


def radial(size, color, peak, squash=1.0, power=2.0):
    w, h = size
    img = Image.new("RGBA", size, color + (0,))
    px = img.load()
    for y in range(h):
        for x in range(w):
            dx = (x - w / 2 + 0.5) / (w / 2)
            dy = (y - h / 2 + 0.5) / (h / 2) * squash
            d = math.sqrt(dx * dx + dy * dy)
            if d < 1:
                px[x, y] = color + (round(255 * peak * (1 - d) ** power),)
    return img


def entry_box():
    w, h, s = 380, 46, 4  # drawn 4x and scaled down: smooth corners
    big = Image.new("RGBA", (w * s, h * s), (0, 0, 0, 0))
    d = ImageDraw.Draw(big)
    d.rounded_rectangle([0, 0, w * s - 1, h * s - 1], radius=12 * s, fill=(16, 16, 22, 235),
                        outline=RED + (150,), width=2 * s)
    return big.resize((w, h), Image.LANCZOS)


def bullet():
    s = 4
    big = Image.new("RGBA", (12 * s, 12 * s), (0, 0, 0, 0))
    ImageDraw.Draw(big).ellipse([s, s, 11 * s - 1, 11 * s - 1], fill=(240, 240, 245, 255))
    return big.resize((12, 12), Image.LANCZOS)


def lock():
    s = 4
    w, h = 22, 28
    big = Image.new("RGBA", (w * s, h * s), (0, 0, 0, 0))
    d = ImageDraw.Draw(big)
    d.rounded_rectangle([0, 11 * s, w * s - 1, h * s - 1], radius=4 * s, fill=(236, 236, 242, 255))
    d.arc([4 * s, 1 * s, 18 * s, 21 * s], 180, 360, fill=(236, 236, 242, 255), width=3 * s)
    d.line([4 * s + s, 11 * s, 4 * s + s, 9 * s], fill=(236, 236, 242, 255), width=3 * s)
    d.line([18 * s - 2 * s, 11 * s, 18 * s - 2 * s, 9 * s], fill=(236, 236, 242, 255), width=3 * s)
    d.ellipse([9 * s, 16 * s, 13 * s, 20 * s], fill=RED + (255,))
    return big.resize((w, h), Image.LANCZOS)


def main():
    lockup = Image.open(os.path.join(BRAND, "nocapos-lockup.png")).convert("RGBA")
    lockup = lockup.crop(lockup.getbbox())
    print("boot art:")
    save(grub_background(lockup), os.path.join(ART, "grub-background.png"))
    save(Image.new("RGB", (16, 16), BG), os.path.join(ART, "grub-boot.png"))

    # Old splash files (the dots and the progress bar).
    for f in ("dot.png", "fill.png", "track.png", "glow.png"):
        p = os.path.join(SPLASH, f)
        if os.path.exists(p):
            os.remove(p)
    for f in os.listdir(SPLASH):
        if f.startswith("shine-") and f.endswith(".png"):
            os.remove(os.path.join(SPLASH, f))

    logo = fit_h(lockup, round(2160 * LOGO_H))
    save(logo, os.path.join(SPLASH, "logo.png"))
    alpha = logo.getchannel("A")
    for k in range(SHINE_FRAMES):
        save(shine_frame(alpha, k), os.path.join(SPLASH, f"shine-{k}.png"))
    save(radial((640, 640), RED, 0.32, power=2.2).filter(ImageFilter.GaussianBlur(6)), os.path.join(SPLASH, "halo.png"))
    save(radial((360, 72), RED, 0.55, power=1.6).filter(ImageFilter.GaussianBlur(3)), os.path.join(SPLASH, "shadow.png"))
    save(entry_box(), os.path.join(SPLASH, "entry.png"))
    save(bullet(), os.path.join(SPLASH, "bullet.png"))
    save(lock(), os.path.join(SPLASH, "lock.png"))


if __name__ == "__main__":
    main()
