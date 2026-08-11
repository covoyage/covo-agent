---
name: ascii-art
description: "Generate ASCII art: pyfiglet banners, cowsay messages, and image-to-ASCII conversion."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [ascii, art, text, banner, figlet, cowsay, creative]
  related_skills: [pixel-art]
---

# ASCII Art — Text-Based Graphics

Generate ASCII art in multiple styles: figlet banners, cowsay messages,
image-to-text conversion, and decorative text-box art.

## When to Use

- "make an ASCII banner"
- "cowsay a message"
- "turn this image into ASCII art"
- "draw something in ASCII"
- "add a retro text header"

## Styles

### Figlet Banner
```bash
# Install pyfiglet
pip install pyfiglet

# Generate banner
python3 -c "
import pyfiglet
print(pyfiglet.figlet_format('HELLO', font='slant'))
"
```

Available fonts: `slant`, `block`, `banner`, `standard`, `big`, `digital`, `isometric1`, `larry3d`, `doom`, `starwars`

### Cowsay
```bash
# Install cowsay
pip install cowsay

# Basic cow
python3 -c "
import cowsay
print(cowsay.cowsay.get_output_string('trex', 'Hello World'))
"
# Or use built-in:
echo 'Hello World' | cowsay -f tux
```

Available characters: `cow`, `tux`, `dragon`, `trex`, `turtle`, `kitty`, `stegosaurus`, `beavis`, `cheese`, `daemon`, `ghostbusters`

### Box Art (Manual)
Create decorative text boxes with ASCII borders:
```
╔══════════════════════════╗
║   IMPORTANT MESSAGE      ║
╠══════════════════════════╣
║ This is a boxed message  ║
║ with double-line border. ║
╚══════════════════════════╝
```

Border styles:
- Single: ┌─┐ │ └─┘
- Double: ╔═╗ ║ ╚═╝
- Rounded: ╭─╮ │ ╰─╯
- Bold: ▛▀▜ ▌ ▙▄▟
- Shadow: ░░ (add right and bottom)

### Image to ASCII
```bash
# Using Python with Pillow
pip install Pillow

python3 << 'EOF'
from PIL import Image
import sys

img = Image.open(sys.argv[1]).convert('L')  # grayscale
img = img.resize((80, int(80 * img.height / img.width * 0.5)))
chars = '@%#*+=-:. '  # dark to light

for y in range(img.height):
    for x in range(img.width):
        gray = img.getpixel((x, y))
        idx = gray * (len(chars) - 1) // 255
        print(chars[idx], end='')
    print()
EOF
```

### Decorative Dividers
```
═══════════════  double
────────────────  single
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  upper block
▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄  lower block
■■■■■■■■■■■■■■■  solid
◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆  diamond
◇◇◇◇◇◇◇◇◇◇◇◇◇◇◇  hollow diamond
```

### Progress Bar
```
[████████████░░░░░░░░] 60%  (12/20)
[■■■■■■■■■□□□□□□] 45%    (9/20)
```

## Delivery

Output ASCII directly in the response. For large images, use `canvas` tool
with pre-formatted text to preserve alignment.
