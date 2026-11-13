#!/usr/bin/env python3
"""Generate agentsmd hero visual (GitHub dark palette, no-cringe flat design)."""
from PIL import Image, ImageDraw, ImageFont

BG = (13, 17, 23)          # #0d1117
FG = (230, 237, 243)       # #e6edf3
MUTED = (139, 148, 158)    # #8b949e
GO = (0, 173, 216)         # Go cyan #00ADD8
RED = (248, 81, 73)        # #f85149
GREEN = (63, 185, 80)      # #3fb950
BORDER = (48, 54, 61)      # #30363d

SANS = "/usr/share/fonts/truetype/noto/NotoSans-Bold.ttf"
SANS_R = "/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf"
MONO = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"


def font(path, size):
    return ImageFont.truetype(path, size)


def doc(d, x, y, w, h):
    """An AGENTS.md document with one dead reference struck through."""
    d.rounded_rectangle([x, y, x + w, y + h], radius=14, outline=BORDER, width=4, fill=(22, 27, 34))
    d.text((x + 28, y + 22), "AGENTS.md", font=font(MONO, 30), fill=FG)
    lines = [
        ("make release", RED, True),
        ("make build", GREEN, False),
        ("src/old_parser.py", RED, True),
        ("internal/auth/", GREEN, False),
    ]
    ly = y + 80
    for text, color, dead in lines:
        d.text((x + 28, ly), text, font=font(MONO, 26), fill=color)
        if dead:
            tw = d.textlength(text, font=font(MONO, 26))
            d.line([x + 28, ly + 18, x + 28 + tw, ly + 18], fill=RED, width=4)
        else:
            d.ellipse([x + w - 46, ly + 8, x + w - 30, ly + 24], outline=GREEN, width=3)
            d.line([x + w - 43, ly + 16, x + w - 39, ly + 21], fill=GREEN, width=3)
            d.line([x + w - 39, ly + 21, x + w - 32, ly + 10], fill=GREEN, width=3)
        ly += 52


def hero():
    W, H = 1280, 640
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    for gx in range(0, W, 64):
        d.line([gx, 0, gx, H], fill=(20, 25, 32))
    for gy in range(0, H, 64):
        d.line([0, gy, W, gy], fill=(20, 25, 32))
    doc(d, 830, 160, 380, 330)
    d.text((90, 220), "agentsmd", font=font(SANS, 120), fill=FG)
    d.text((94, 380), "CI fails the day your AGENTS.md", font=font(SANS_R, 44), fill=MUTED)
    d.text((94, 440), "starts lying.", font=font(SANS_R, 44), fill=MUTED)
    img.save("assets/hero.png")


hero()
print("wrote assets/hero.png")
