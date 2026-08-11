---
name: excalidraw
description: "Hand-drawn Excalidraw diagrams: architecture, flow, sequence, and wireframe."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [diagram, excalidraw, hand-drawn, architecture, flow, wireframe]
  related_skills: [architecture-diagram, concept-diagrams]
---

# Excalidraw — Hand-Drawn Diagrams

Generate Excalidraw JSON for hand-drawn style diagrams. Use when the user
wants organic-looking architecture, flow, sequence, or UI wireframe diagrams.

## When to Use

- "draw an architecture diagram (hand-drawn style)"
- "sketch a flowchart with Excalidraw"
- "create a wireframe for..."
- "make a diagram that looks hand-drawn"

## Output Format

Excalidraw uses a JSON format. Generate valid Excalidraw JSON with these elements:

### Rectangle
```json
{
  "type": "rectangle",
  "x": 100, "y": 100,
  "width": 200, "height": 80,
  "strokeColor": "#1e1e1e",
  "backgroundColor": "#a5d8ff",
  "fillStyle": "solid",
  "strokeWidth": 2,
  "roughness": 1
}
```

### Text
```json
{
  "type": "text",
  "x": 120, "y": 120,
  "text": "Component Name",
  "fontSize": 16,
  "fontFamily": 1,
  "strokeColor": "#1e1e1e"
}
```

### Arrow/Line
```json
{
  "type": "arrow",
  "x": 300, "y": 140,
  "width": 150, "height": 0,
  "strokeColor": "#1e1e1e",
  "strokeWidth": 2,
  "roughness": 1
}
```

### Full Document Structure
```json
{
  "type": "excalidraw",
  "version": 2,
  "source": "covo-agent",
  "elements": [...],
  "appState": {
    "viewBackgroundColor": "#ffffff"
  }
}
```

## Design Guidelines

- **Spacing:** Leave 30-50px between connected elements
- **Colors:** Use muted, hand-drawn feel: `#a5d8ff` (blue), `#ffc9c9` (red), `#b2f2bb` (green), `#ffec99` (yellow), `#d0bfff` (purple)
- **Font:** Use `fontFamily: 1` (hand-drawn Virgil font)
- **Roughness:** Set to 1-2 for natural hand-drawn look
- **Layout:** Top-to-bottom for flow, left-to-right for sequence
- **Grouping:** Use container rectangles with dashed borders

## Tool

Use `canvas` tool with mode=html to embed and render the JSON:
```javascript
// Wrap in HTML that loads Excalidraw viewer
<script src="https://unpkg.com/@excalidraw/excalidraw/dist/excalidraw.production.min.js"></script>
```

Or write the JSON to a `.excalidraw.json` file for import into excalidraw.com.
