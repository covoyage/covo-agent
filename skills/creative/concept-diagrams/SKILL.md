---
name: concept-diagrams
description: "Educational SVG diagrams: physics, chemistry, biology, engineering, and layered views."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [diagram, svg, education, science, physics, chemistry, concept]
  related_skills: [architecture-diagram, excalidraw]
---

# Concept Diagrams — Educational & Scientific SVG

Generate clean educational SVG diagrams for non-software topics: physics
setups, chemistry mechanisms, engineering schematics, biology processes,
cross-sections, and narrative journey maps.

## When to Use

- "explain how a jet engine works (with a diagram)"
- "diagram the Krebs cycle"
- "show the layers of the Earth's atmosphere"
- "illustrate this chemical reaction mechanism"
- "create a cross-section of a smartphone"
- "visualize this biological process"

Use `architecture-diagram` for software/cloud topics instead.

## Design Language

### Color Palette
```
Physics:    #4A90D9 (blue) | #E8A838 (amber) | #50B86C (green)
Chemistry:  #E94560 (red) | #4A90D9 (blue) | #F0E68C (yellow)
Biology:    #50B86C (green) | #E8A838 (amber) | #E94560 (red)
Engineering: #6C7A89 (grey) | #E94560 (red accent) | #4A90D9 (blue)
General:    #5B6ABF (purple) | #3B82F6 (blue) | #10B981 (green) | #F59E0B (amber) | #EF4444 (red)
```

### Typography
- Sentence case for all labels ("Heat source" not "Heat Source")
- Sans-serif font: system-ui or Arial
- Labels outside or beside elements, not overlapping

### Lines & Arrows
- 2px stroke width for main flows
- 1px dashed for annotations
- Arrow heads for direction (use SVG `<marker>`)
- 45-degree angled connectors preferred over curves

### Layout
- Centered on 800×600 or 1000×700 viewBox
- 40px padding on all sides
- Group related elements with light background rects

## SVG Templates

### Physics Setup
```svg
<svg viewBox="0 0 800 500" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5"
      markerWidth="6" markerHeight="6" orient="auto">
      <path d="M0,0 L10,5 L0,10 Z" fill="#4A90D9"/>
    </marker>
  </defs>
  <rect x="0" y="0" width="800" height="500" fill="#0f0f23"/>
  <!-- Elements -->
  <rect x="100" y="200" width="120" height="80" rx="8" fill="#16213e" stroke="#4A90D9" stroke-width="2"/>
  <text x="160" y="245" text-anchor="middle" fill="#e0e0e0" font-family="system-ui" font-size="14">Light Source</text>
  <!-- Arrow -->
  <line x1="220" y1="240" x2="300" y2="240" stroke="#4A90D9" stroke-width="2" marker-end="url(#arrow)"/>
  <!-- Labels -->
  <text x="260" y="230" text-anchor="middle" fill="#888" font-size="11">incident beam</text>
</svg>
```

### Chemical Reaction
```svg
<svg viewBox="0 0 800 400" xmlns="http://www.w3.org/2000/svg">
  <rect width="800" height="400" fill="#0f0f23"/>
  <!-- Reactants -->
  <circle cx="150" cy="200" r="40" fill="none" stroke="#E94560" stroke-width="2"/>
  <text x="150" y="205" text-anchor="middle" fill="#E94560" font-size="14">A</text>
  <text x="150" y="260" text-anchor="middle" fill="#888" font-size="11">Reactant 1</text>
  <!-- Arrow -->
  <line x1="200" y1="200" x2="350" y2="200" stroke="#F0E68C" stroke-width="2" marker-end="url(#arrow)"/>
  <!-- Transition state -->
  <path d="M380,160 Q420,120 460,160 Q420,180 380,160" fill="none" stroke="#F0E68C" stroke-width="1.5" stroke-dasharray="4 2"/>
  <text x="420" y="140" text-anchor="middle" fill="#F0E68C" font-size="9">TS</text>
  <!-- Products -->
  <circle cx="600" cy="180" r="35" fill="none" stroke="#50B86C" stroke-width="2"/>
  <text x="600" y="185" text-anchor="middle" fill="#50B86C" font-size="14">B</text>
</svg>
```

### Cross-Section View
```svg
<svg viewBox="0 0 600 400" xmlns="http://www.w3.org/2000/svg">
  <rect width="600" height="400" fill="#0f0f23"/>
  <!-- Outer layer -->
  <rect x="50" y="80" width="500" height="300" rx="20" fill="#16213e" stroke="#6C7A89" stroke-width="2"/>
  <!-- Middle layer -->
  <rect x="80" y="110" width="440" height="240" rx="15" fill="#0f3460" stroke="#4A90D9" stroke-width="2"/>
  <!-- Inner core -->
  <rect x="120" y="150" width="360" height="160" rx="10" fill="#e94560" opacity="0.3" stroke="#e94560" stroke-width="2"/>
  <!-- Labels with leader lines -->
  <line x1="50" y1="230" x2="10" y2="230" stroke="#6C7A89" stroke-width="1"/>
  <text x="5" y="234" fill="#6C7A89" font-size="11" font-family="system-ui">Outer shell</text>
</svg>
```

## Delivery

Use `canvas` tool with mode=svg to render and open in browser.
