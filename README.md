# wiki

Small self-hosted wiki for Markdown files.

## Features

- Markdown-based articles (`.md` files from a content directory)
- Client-side Mermaid rendering for `mermaid` code blocks
- Full-text search with Lunr
- Section-level search indexing with anchor links to matched parts
- Search integrated into the same article tree UI (filter + expand matches)
- "On this page" nested table of contents for article headings
- **4 Switchable Themes** - Default Light, Default Dark, Sepia, Retro Terminal
- **Automatic theme detection** - uses your browser's light/dark preference by default
- Theme switcher only on index page (keeps articles clean)
- No flash of wrong theme - theme loads immediately with the page
- systemd deployment via `make install`

## Themes

The wiki automatically detects your browser's color scheme preference (light/dark) and applies the appropriate theme on first visit:
- **Default Light** (black text on white) - when system preference is light
- **Default Dark** (white text on black) - when system preference is dark

The theme switcher appears only on the index page with 4 options:
- **Default Light** - Clean black text on white background
- **Default Dark** - White text on black background
- **Sepia** - Warm, easy on the eyes, perfect for reading (uses serif fonts)
- **Retro Terminal** - Old terminal aesthetic with amber phosphor glow and scanlines

Your theme preference is saved to localStorage once you manually select a theme. The wiki will then respect your choice instead of following the system preference.

## Run

```bash
go run . -port=8080 -content=.
```

## Build

```bash
make build
```

## Install (systemd)

```bash
make install
```

## Theming System

The theming system uses semantic HTML tags and CSS custom properties:

- **Semantic HTML** - styles are applied to standard HTML tags (`article`, `nav`, `section`, `h1-h6`, `dl/dt/dd`, etc.) rather than classes
- **CSS Custom Properties** - all themeable values are CSS variables defined in each theme file
- **Base CSS** (`base.css`) - provides the structural styles and default variable values
- **Theme CSS** - each theme file only redefines the CSS variables
- **No flash** - theme is loaded inline in the HTML head before body renders

The theme switcher is only shown on the index page (`/`) to keep article pages clean and focused on content.
