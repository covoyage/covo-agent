---
name: nano-pdf
description: "Edit PDFs with natural language instructions using the nano-pdf CLI."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [pdf, edit, natural-language, document]
  related_skills: [pdf]
---

# Nano-PDF — Natural Language PDF Editing

Edit PDFs using natural language instructions. Fix typos, change text,
update metadata, or restructure pages.

## Installation

```bash
pip install nano-pdf
```

## When to Use

- "fix the typo on page 3"
- "change the title on this PDF"
- "remove page 5"
- "add my name to the header"
- "rotate page 2"

## CLI Usage

```bash
# Fix typos
nano-pdf input.pdf -e "fix all typos and grammar issues" -o fixed.pdf

# Change text
nano-pdf input.pdf -e "change the title from 'Draft' to 'Final Report' on page 1" -o output.pdf

# Edit metadata
nano-pdf input.pdf -e "set title to 'Q4 Report', author to 'Covo Agent'" -o output.pdf

# Page operations
nano-pdf input.pdf -e "remove pages 3-5" -o trimmed.pdf
nano-pdf input.pdf -e "rotate page 2 90 degrees clockwise" -o rotated.pdf
```

## Python API

```python
from nano_pdf import NanoPDF

pdf = NanoPDF("input.pdf")

# Edit with natural language
pdf.edit("change all instances of 'teh' to 'the'")
pdf.edit("make the heading on page 1 bold and centered")

# Page operations
pdf.remove_pages([3, 4, 5])
pdf.rotate_page(2, 90)

# Save
pdf.save("output.pdf")
```

## Manual PDF Operations (No nano-pdf)

If nano-pdf is not available, use these alternatives:

### Extract Text
```bash
pip install pymupdf
python3 -c "
import fitz
doc = fitz.open('input.pdf')
for page in doc:
    print(page.get_text())
"
```

### Merge PDFs
```bash
pip install pypdf
python3 -c "
from pypdf import PdfMerger
merger = PdfMerger()
for pdf in ['a.pdf', 'b.pdf', 'c.pdf']:
    merger.append(pdf)
merger.write('merged.pdf')
"
```

### Split PDF
```bash
python3 -c "
from pypdf import PdfReader, PdfWriter
reader = PdfReader('input.pdf')
for i, page in enumerate(reader.pages):
    writer = PdfWriter()
    writer.add_page(page)
    writer.write(f'page_{i+1}.pdf')
"
```

### Compress PDF
```bash
# Reduce file size
gs -sDEVICE=pdfwrite -dCompatibilityLevel=1.4 -dPDFSETTINGS=/ebook \
  -dNOPAUSE -dQUIET -dBATCH -sOutputFile=compressed.pdf input.pdf
```
