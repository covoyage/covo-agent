---
name: claude-design
description: "Design one-off HTML artifacts: landing pages, pitch decks, prototypes."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  tags: [design, html, css, landing-page, prototype, mockup, deck]
  related_skills: [sketch, architecture-diagram, canvas]
---

# Claude Design — One-Off HTML Artifacts

Create polished, standalone HTML/CSS artifacts for quick visual communication.
Use when the user wants a landing page mockup, a slide deck, a product
prototype, or any self-contained visual piece.

## When to Use

- "design a landing page for..."
- "make a slide deck about..."
- "create a prototype UI"
- "build a quick dashboard"
- "show me a visual mockup of..."

## Design Principles

1. **Single-file delivery:** Everything in one HTML file (inline CSS, no build step)
2. **Dark mode by default:** Dark background (#1a1a2e, #0f0f23) with light text
3. **System fonts:** `font-family: system-ui, -apple-system, sans-serif`
4. **Responsive:** max-width container, flexbox/grid layouts
5. **No frameworks:** Pure CSS, no Tailwind/Bootstrap
6. **Accessible:** Semantic HTML, proper contrast ratios

## Template Structure

```html
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Project Name</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: system-ui, -apple-system, sans-serif;
  background: #1a1a2e;
  color: #e0e0e0;
  line-height: 1.6;
}
.container {
  max-width: 1200px;
  margin: 0 auto;
  padding: 40px 20px;
}
.hero { text-align: center; padding: 80px 0; }
.hero h1 { font-size: 3em; margin-bottom: 16px; }
.hero p { font-size: 1.2em; color: #999; max-width: 600px; margin: 0 auto; }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 24px; }
.card {
  background: #16213e;
  border-radius: 12px;
  padding: 24px;
  border: 1px solid #0f3460;
}
.card h3 { margin-bottom: 12px; color: #e94560; }
.btn {
  display: inline-block;
  padding: 12px 32px;
  border-radius: 8px;
  text-decoration: none;
  font-weight: 600;
  transition: transform 0.2s;
}
.btn-primary { background: #e94560; color: white; }
.btn:hover { transform: translateY(-2px); }
</style>
</head>
<body>
<div class="container">
  <section class="hero">
    <h1>Product Name</h1>
    <p>Compelling one-line description that captures the value proposition.</p>
    <a href="#" class="btn btn-primary">Get Started</a>
  </section>
  <section class="grid">
    <div class="card">
      <h3>Feature One</h3>
      <p>Description of what this feature does and why it matters.</p>
    </div>
    <!-- More cards -->
  </section>
</div>
</body>
</html>
```

## Slide Deck Pattern

For presentations, use horizontal sections:
```css
.slide {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px;
  scroll-snap-align: start;
}
.slides { scroll-snap-type: y mandatory; overflow-y: scroll; height: 100vh; }
```

## Dashboard Pattern

```css
.stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
.stat { background: #16213e; padding: 20px; border-radius: 8px; text-align: center; }
.stat-value { font-size: 2em; font-weight: bold; color: #e94560; }
.stat-label { font-size: 0.85em; color: #999; }
```

## Delivery

Use `canvas` tool with mode=html to render and open in browser.
