#!/usr/bin/env python3
"""Writes the yastationd logo files (docs/design.md): a deltoid (the three-cusped hypocycloid) on
a disc. Run from the repo root: python3 docs/brand/make-logo.py"""
import math

DEEP, SKY = "#0A4B9E", "#2FB8F2"  # the brand gradient: deep blue (top right) to sky (bottom left)
SKY_ON_LIGHT = "#28A2E1"          # the sky end on a light disc: a touch darker to stay visible
DEEP_ON_DARK = "#156CB7"          # the deep end on a dark disc: lighter to stay visible
LIGHT_BG, DARK_BG = "#F0FAFE", "#14171C"
C, R = (256.0, 262.0), 44.0       # the deltoid's centre (optically low) and rolling-circle radius
STROKE = 40


def deltoid(cx, cy, r, n=96):
    """The deltoid x = 2r cos t + r cos 2t, y = 2r sin t - r sin 2t, turned so a cusp points up."""
    pts = []
    for i in range(n):
        t = 2 * math.pi * i / n
        x, y = 2 * r * math.cos(t) + r * math.cos(2 * t), 2 * r * math.sin(t) - r * math.sin(2 * t)
        a = -math.pi / 2  # cusp at t=0 points along +x: turn it to -y (up)
        pts.append((cx + x * math.cos(a) - y * math.sin(a), cy + x * math.sin(a) + y * math.cos(a)))
    return "M" + "L".join(f"{x:.1f} {y:.1f}" for x, y in pts) + "Z"


def svg(disc, stroke, grad=(448, 64, 64, 448), deep=DEEP, sky=SKY, title="yastationd", r=R, width=STROKE):
    d = deltoid(C[0], C[1] + (r - R) * 0.15, r)
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" role="img" aria-label="{title}">
<title>{title}</title>
<defs><linearGradient id="g" x1="{grad[0]}" y1="{grad[1]}" x2="{grad[2]}" y2="{grad[3]}" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="{deep}"/><stop offset="1" stop-color="{sky}"/></linearGradient></defs>
<circle cx="256" cy="256" r="256" fill="{disc}"/>
<path d="{d}" fill="none" stroke="{stroke}" stroke-width="{width}" stroke-linejoin="round"/>
</svg>
'''


files = {
    "docs/brand/logo.svg": svg("url(#g)", "#fff"),
    "docs/brand/logo-light.svg": svg(LIGHT_BG, "url(#g)", (358, 154, 154, 358), sky=SKY_ON_LIGHT),
    "docs/brand/logo-dark.svg": svg(DARK_BG, "url(#g)", (358, 154, 154, 358), deep=DEEP_ON_DARK),
    # the favicon: a bigger deltoid with a heavier stroke, to read at 16 px
    "web/public/favicon.svg": svg("url(#g)", "#fff", r=54, width=56),
}
for path, text in files.items():
    with open(path, "w") as f:
        f.write(text)
    print(path, len(text))
