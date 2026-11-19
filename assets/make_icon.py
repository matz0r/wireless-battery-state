#!/usr/bin/env python3
"""Generates the battery-state logo, a battery with wireless waves, as SVG and PNG from one set of shapes.

Usage: python3 assets/make_icon.py   (needs Pillow; writes into assets/)
"""
import math
from pathlib import Path

from PIL import Image, ImageDraw

OUT = Path(__file__).resolve().parent
SIZE = 512

BG = "#1E2A38"
BG_RING = "#2C3D52"
SHADOW = "#121A23"
SHELL = "#E6EDF3"
WAVE = "#3D8BD9"
# Charge segments from left to right: low to full.
SEGMENTS = ["#E05A47", "#F3B23C", "#5BB55B"]

# Battery: a rounded outline with a terminal on the right and three charge segments.
BAT = (112, 268, 384, 404)  # outer box x0, y0, x1, y1
BAT_RADIUS = 30
BAT_STROKE = 22
NUB = (392, 308, 420, 364)  # terminal
SEG_GAP = 14
SEG_INSET = BAT_STROKE / 2 + 16
SEG_RADIUS = 10

# Wireless waves above the battery: arcs around one center, plus a dot.
WAVE_CENTER = (256, 226)
WAVE_RADII = (52, 100)
WAVE_WIDTH = 26
WAVE_SPAN = (-140, -40)  # degrees, 0 = right, counter-clockwise negative (screen coordinates)
DOT_RADIUS = 20
SHADOW_OFFSET = 10


def segments():
    x0, y0, x1, y1 = BAT
    ix0, iy0, ix1, iy1 = x0 + SEG_INSET, y0 + SEG_INSET, x1 - SEG_INSET, y1 - SEG_INSET
    w = (ix1 - ix0 - SEG_GAP * (len(SEGMENTS) - 1)) / len(SEGMENTS)
    return [(ix0 + i * (w + SEG_GAP), iy0, ix0 + i * (w + SEG_GAP) + w, iy1) for i in range(len(SEGMENTS))]


def arc_points(r, a0, a1):
    a0, a1 = math.radians(a0), math.radians(a1)
    cx, cy = WAVE_CENTER
    return (cx + r * math.cos(a0), cy + r * math.sin(a0)), (cx + r * math.cos(a1), cy + r * math.sin(a1))


def write_svg():
    def wave_paths(dy=0):
        out = []
        for r in WAVE_RADII:
            (sx, sy), (ex, ey) = arc_points(r, *WAVE_SPAN)
            out.append(f'M{sx:.1f},{sy + dy:.1f} A{r},{r} 0 0 1 {ex:.1f},{ey + dy:.1f}')
        return " ".join(out)

    x0, y0, x1, y1 = BAT
    half = BAT_STROKE / 2
    cx, cy = WAVE_CENTER
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {SIZE} {SIZE}">',
        f'<rect x="8" y="8" width="496" height="496" rx="116" fill="{BG}" stroke="{BG_RING}" stroke-width="10"/>',
    ]
    for dy, shell, wave in ((SHADOW_OFFSET, SHADOW, SHADOW), (0, SHELL, WAVE)):
        parts += [
            f'<rect x="{x0 + half}" y="{y0 + half + dy}" width="{x1 - x0 - BAT_STROKE}" height="{y1 - y0 - BAT_STROKE}" '
            f'rx="{BAT_RADIUS - half}" fill="none" stroke="{shell}" stroke-width="{BAT_STROKE}"/>',
            f'<rect x="{NUB[0]}" y="{NUB[1] + dy}" width="{NUB[2] - NUB[0]}" height="{NUB[3] - NUB[1]}" rx="10" fill="{shell}"/>',
            f'<path d="{wave_paths(dy)}" fill="none" stroke="{wave}" stroke-width="{WAVE_WIDTH}" stroke-linecap="round"/>',
            f'<circle cx="{cx}" cy="{cy + dy}" r="{DOT_RADIUS}" fill="{wave}"/>',
        ]
    for (sx0, sy0, sx1, sy1), color in zip(segments(), SEGMENTS):
        parts.append(f'<rect x="{sx0:.1f}" y="{sy0:.1f}" width="{sx1 - sx0:.1f}" height="{sy1 - sy0:.1f}" rx="{SEG_RADIUS}" fill="{color}"/>')
    parts.append("</svg>")
    (OUT / "icon.svg").write_text("\n".join(parts) + "\n")


def render_png(size):
    ss = 4  # supersampling factor for anti-aliasing
    full = size * ss
    k = full / SIZE
    img = Image.new("RGBA", (full, full), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    def box(b, dy=0):
        return [b[0] * k, (b[1] + dy) * k, b[2] * k, (b[3] + dy) * k]

    d.rounded_rectangle(box((8, 8, 504, 504)), radius=116 * k, fill=BG, outline=BG_RING, width=round(10 * k))

    def round_cap(x, y, color, dy):
        r = WAVE_WIDTH / 2
        d.ellipse(box((x - r, y - r, x + r, y + r), dy), fill=color)

    for dy, shell, wave in ((SHADOW_OFFSET, SHADOW, SHADOW), (0, SHELL, WAVE)):
        d.rounded_rectangle(box(BAT, dy), radius=BAT_RADIUS * k, outline=shell, width=round(BAT_STROKE * k))
        d.rounded_rectangle(box(NUB, dy), radius=10 * k, fill=shell)
        cx, cy = WAVE_CENTER
        for r in WAVE_RADII:
            outer = r + WAVE_WIDTH / 2
            d.arc(box((cx - outer, cy - outer, cx + outer, cy + outer), dy), WAVE_SPAN[0], WAVE_SPAN[1],
                  fill=wave, width=round(WAVE_WIDTH * k))
            for p in arc_points(r, *WAVE_SPAN):
                round_cap(*p, wave, dy)
        d.ellipse(box((cx - DOT_RADIUS, cy - DOT_RADIUS, cx + DOT_RADIUS, cy + DOT_RADIUS), dy), fill=wave)
    for b, color in zip(segments(), SEGMENTS):
        d.rounded_rectangle(box(b), radius=SEG_RADIUS * k, fill=color)
    return img.resize((size, size), Image.LANCZOS)


if __name__ == "__main__":
    write_svg()
    for size in (256, 512):
        render_png(size).save(OUT / f"icon-{size}.png")
    print("wrote", OUT)
