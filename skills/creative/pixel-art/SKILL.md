---
name: pixel-art
description: "Generate pixel art with era-authentic palettes: NES, Game Boy, PICO-8, and more."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [pixel-art, retro, game, nes, gameboy, pico8, creative]
  related_skills: [ascii-art, claude-design]
---

# Pixel Art — Retro Pixel Graphics

Generate pixel art using era-authentic color palettes: NES (56 colors),
Game Boy (4 grays), PICO-8 (16 colors), MSX (15), and custom palettes.

## When to Use

- "draw a pixel art character"
- "create a retro game sprite"
- "pixel art of a castle"
- "make a PICO-8 style scene"
- "8-bit version of..."

## Color Palettes

### NES (Nintendo, 56 colors)
```
#7C7C7C #000000 #0018F8 #7848F8 #F83800 #F87858 #58D854 #D800CC
#FFFFFF #A8A8A8 #6888FC #B880FC #F80078 #F8A8C0 #B8F818 #F8F8F8
#3CBCFC #0078F8 #D8C0F8 #F81858 #F8E000 #F8D878 #58F898 #F8B800
#6888FC #3868FC #A8A8FC #F80078 #F838B8 #F8C058 #F8F858 #B8A8FC
```

### Game Boy (DMG)
```
#0f380f #306230 #8bac0f #9bbc0f   (original green)
#0a0a0a #3a3a3a #7a7a7a #aaaaaa   (gray tint)
```

### PICO-8
```
#000000 #1D2B53 #7E2553 #008751 #AB5236 #5F574F #C2C3C7 #FFF1E8
#FF004D #FFA300 #FFEC27 #00E436 #29ADFF #83769C #FF77A8 #FFCCAA
```

## Generation Approach

### Method 1: HTML Canvas
Generate pixel art as HTML with CSS grid and colored divs:
```html
<style>
.pixel-grid {
  display: grid;
  grid-template-columns: repeat(WIDTH, 8px);
  gap: 0;
  width: fit-content;
}
.pixel { width: 8px; height: 8px; }
</style>
<div class="pixel-grid">
  <div class="pixel" style="background: #000000"></div>
  <!-- ... -->
</div>
```

### Method 2: SVG Rect Grid
```svg
<svg viewBox="0 0 WIDTH HEIGHT" width="WIDTH" height="HEIGHT">
  <rect x="0" y="0" width="1" height="1" fill="#7C7C7C"/>
  <!-- ... -->
</svg>
```

### Method 3: Python with PIL
```python
from PIL import Image
img = Image.new('RGB', (16, 16))
pixels = img.load()
# Set pixels with palette colors
pixels[0, 0] = (0, 0, 0)  # black
img.save('sprite.png')
img = img.resize((256, 256), Image.NEAREST)  # scale up
img.save('sprite_2x.png')
```

## Design Tips

- **Start with silhouette:** Block out the shape in the darkest color
- **Add mid-tones:** Fill main areas with medium colors
- **Highlight edges:** Use lightest color for top/left edges (light source assumption)
- **Limit palette:** Stick to 4-8 colors per sprite for authenticity
- **Grid size:** 16×16 for sprites, 32×32 for characters, 128×128 for scenes
- **No anti-aliasing:** Hard edges are the point of pixel art
- **Dithering:** For gradients, alternate two colors in checkerboard pattern

## Common Subjects

| Subject | Grid | Palette | Key shapes |
|---------|------|---------|------------|
| Character | 16×16 | NES | Head circle, body rect, arm/leg lines |
| Item/Icon | 8×8 | PICO-8 | Simple geometric shapes |
| Platform | 128×64 | Game Boy | Horizontal layers, platforms, sky |
| Scene | 128×128 | NES | Background + foreground + character |
| Logo/Text | 32×8 | Custom | Monospace grid with color per letter |

## Delivery

Use `canvas` tool with mode=html to render pixel art in browser.
