---
name: excel-author
description: "Build auditable Excel workbooks with openpyxl: formulas, named ranges, formatting conventions."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [excel, spreadsheet, finance, openpyxl, model, report]
  related_skills: [pptx-author]
---

# Excel Author — Headless Workbook Generation

Build professional Excel workbooks programmatically using openpyxl.
Follow the "blue/black/green" cell convention for auditable models.

## Installation

```bash
pip install openpyxl
```

## When to Use

- "create an Excel report"
- "build a financial model"
- "export data to Excel"
- "automate a spreadsheet"

## Cell Style Convention

| Color | Meaning | Example |
|-------|---------|---------|
| **Blue** (#1F4E79) | Input / assumption (hard-coded) | revenue growth rate |
| **Black** (#000000) | Formula (calculated) | total revenue |
| **Green** (#375623) | Link to other sheet | cross-sheet reference |

## Basic Workbook Template

```python
from openpyxl import Workbook
from openpyxl.styles import Font, PatternFill, Border, Side, Alignment, numbers
from openpyxl.utils import get_column_letter

wb = Workbook()
ws = wb.active
ws.title = "Model"

# Styles
blue_font = Font(name="Calibri", size=11, color="1F4E79")
blue_fill = PatternFill(start_color="D6E4F0", end_color="D6E4F0", fill_type="solid")
black_font = Font(name="Calibri", size=11, color="000000")
green_font = Font(name="Calibri", size=11, color="375623")
bold_font = Font(name="Calibri", size=11, color="1F4E79", bold=True)
header_fill = PatternFill(start_color="4472C4", end_color="4472C4", fill_type="solid")
header_font = Font(name="Calibri", size=11, color="FFFFFF", bold=True)
thin_border = Border(
    left=Side(style='thin'), right=Side(style='thin'),
    top=Side(style='thin'), bottom=Side(style='thin')
)

# Number formats
pct_fmt = '0.0%'
num_fmt = '#,##0'
dec_fmt = '#,##0.00'
usd_fmt = '$#,##0'

# Helper functions
def set_cell(ws, row, col, value, font=black_font, fill=None, fmt=None):
    cell = ws.cell(row=row, column=col, value=value)
    cell.font = font
    if fill: cell.fill = fill
    cell.border = thin_border
    cell.alignment = Alignment(horizontal='center')
    if fmt: cell.number_format = fmt
    return cell

def input_cell(ws, row, col, value, fmt=None):
    return set_cell(ws, row, col, value, blue_font, blue_fill, fmt)

def formula_cell(ws, row, col, formula, fmt=None):
    return set_cell(ws, row, col, formula, black_font, None, fmt)

def link_cell(ws, row, col, formula, fmt=None):
    return set_cell(ws, row, col, formula, green_font, None, fmt)

# Headers
headers = ["Year", "Revenue", "Cost", "Profit"]
for i, h in enumerate(headers, 1):
    cell = ws.cell(row=1, column=i, value=h)
    cell.font = header_font
    cell.fill = header_fill
    cell.border = thin_border

# Data
for year in range(5):
    r = year + 2
    set_cell(ws, r, 1, 2024 + year)                      # Year (black)
    input_cell(ws, r, 2, 100000 * (1.1 ** year), usd_fmt) # Revenue (blue input)
    input_cell(ws, r, 3, 60000 * (1.05 ** year), usd_fmt) # Cost (blue input)
    formula_cell(ws, r, 4, f"=B{r}-C{r}", usd_fmt)        # Profit (black formula)

# Column widths
ws.column_dimensions['A'].width = 8
for col in range(2, 5):
    ws.column_dimensions[get_column_letter(col)].width = 14

# Save
wb.save("model.xlsx")
```

## Key Patterns

### Named Ranges
```python
from openpyxl.workbook.defined_name import DefinedName
ws.title = "Assumptions"
ws['B2'] = 0.15  # tax rate
ws['B2'].font = blue_font
ws['B2'].fill = blue_fill
ws['B2'].number_format = pct_fmt
ws['A2'] = "Tax Rate"

# Define name
ref = f"Assumptions!$B$2"
wb.defined_names.add(DefinedName("TaxRate", attr_text=ref))
```

### Charts
```python
from openpyxl.chart import BarChart, Reference
chart = BarChart()
chart.title = "Revenue by Year"
data = Reference(ws, min_col=2, min_row=1, max_row=6)
cats = Reference(ws, min_col=1, min_row=2, max_row=6)
chart.add_data(data, titles_from_data=True)
chart.set_categories(cats)
ws.add_chart(chart, "F2")
```

### Multiple Sheets
```python
ws_input = wb.create_sheet("Inputs")
ws_calc = wb.create_sheet("Calculations")
ws_output = wb.create_sheet("Output")

# Links between sheets
ws_calc['B2'] = "=Inputs!B2 * 1.1"  # green link
ws_calc['B2'].font = green_font
```

### Balance Check
```python
# Add a balance check row
check_row = data_end + 2
ws.cell(row=check_row, column=1, value="CHECK:").font = bold_font
ws.cell(row=check_row, column=2, value=f"={asset_start}-{liability_start}").font = Font(color="FF0000" if False else "008000")
```
