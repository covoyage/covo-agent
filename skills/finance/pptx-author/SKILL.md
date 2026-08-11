---
name: pptx-author
description: "Build PowerPoint decks programmatically: slides, charts, tables, and professional layouts."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [powerpoint, pptx, presentation, slide, deck, report]
  related_skills: [excel-author]
---

# PPTX Author — Headless PowerPoint Generation

Build professional PowerPoint decks using python-pptx. Use for automated
reporting, pitch decks, or data presentations.

## Installation

```bash
pip install python-pptx
```

## When to Use

- "make a presentation"
- "create a slide deck"
- "generate a report in PowerPoint"
- "automate a monthly deck"

## Slide Layouts

```python
from pptx import Presentation
from pptx.util import Inches, Pt, Emu
from pptx.dml.color import RGBColor
from pptx.enum.text import PP_ALIGN

prs = Presentation()
prs.slide_width = Inches(13.333)  # 16:9 widescreen
prs.slide_height = Inches(7.5)
```

### Title Slide
```python
slide = prs.slides.add_slide(prs.slide_layouts[0])  # Title layout
slide.shapes.title.text = "Quarterly Review"
slide.placeholders[1].text = "Q4 2024 | Prepared by Agent"
```

### Content Slide with Bullets
```python
slide = prs.slides.add_slide(prs.slide_layouts[1])  # Title + Content
slide.shapes.title.text = "Key Highlights"
body = slide.placeholders[1]

tf = body.text_frame
tf.text = "Revenue grew 15% YoY"
p = tf.add_paragraph()
p.text = "Customer base expanded to 10K+"
p.level = 1  # indent
p = tf.add_paragraph()
p.text = "Launched 3 new products"
```

### Custom Text Box
```python
from pptx.util import Inches, Pt

left, top, width, height = Inches(1), Inches(2), Inches(5), Inches(3)
txBox = slide.shapes.add_textbox(left, top, width, height)
tf = txBox.text_frame

p = tf.paragraphs[0]
p.text = "Executive Summary"
p.font.size = Pt(24)
p.font.bold = True
p.font.color.rgb = RGBColor(0x1F, 0x4E, 0x79)

p = tf.add_paragraph()
p.text = "Key metrics and strategic direction for the coming quarter."
p.font.size = Pt(14)
p.font.color.rgb = RGBColor(0x33, 0x33, 0x33)
p.space_before = Pt(12)
```

### Table Slide
```python
rows, cols = 5, 4
table_shape = slide.shapes.add_table(rows, cols, Inches(1), Inches(2), Inches(10), Inches(4))
table = table_shape.table

# Header
for i, h in enumerate(["Metric", "Q1", "Q2", "Change"]):
    cell = table.cell(0, i)
    cell.text = h
    for p in cell.text_frame.paragraphs:
        p.font.bold = True
        p.font.size = Pt(12)

# Data
data = [
    ["Revenue", "$1.2M", "$1.4M", "+15%"],
    ["Users", "8,500", "10,200", "+20%"],
    ["NPS", "72", "78", "+6pts"],
    ["Churn", "2.1%", "1.8%", "-0.3ppt"],
]
for r, row in enumerate(data, 1):
    for c, val in enumerate(row):
        table.cell(r, c).text = val
```

### Chart Slide
```python
from pptx.chart.data import CategoryChartData

chart_data = CategoryChartData()
chart_data.categories = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun']
chart_data.add_series('Revenue', (150, 165, 180, 190, 210, 240))
chart_data.add_series('Cost', (120, 130, 135, 140, 150, 160))

x, y, cx, cy = Inches(2), Inches(2), Inches(9), Inches(4.5)
chart = slide.shapes.add_chart(
    type=1,  # XL_CHART_TYPE.COLUMN_CLUSTERED
    x, y, cx, cy, chart_data
).chart
chart.has_legend = True
chart.chart_title.text_frame.paragraphs[0].text = "Monthly Revenue vs Cost"
```

### Image Insertion
```python
from pptx.util import Inches
img_path = "chart.png"
slide.shapes.add_picture(img_path, Inches(1), Inches(2), width=Inches(8))
```

## Design System

```python
# Color palette
DARK_BLUE = RGBColor(0x1F, 0x4E, 0x79)
MEDIUM_BLUE = RGBColor(0x44, 0x72, 0xC4)
LIGHT_BLUE = RGBColor(0xD6, 0xE4, 0xF0)
ACCENT_RED = RGBColor(0xC0, 0x00, 0x00)
DARK_TEXT = RGBColor(0x33, 0x33, 0x33)
LIGHT_TEXT = RGBColor(0x66, 0x66, 0x66)

# Background
background = slide.background
fill = background.fill
fill.solid()
fill.fore_color.rgb = RGBColor(0xFF, 0xFF, 0xFF)
```

## Save

```python
prs.save("presentation.pptx")
```
