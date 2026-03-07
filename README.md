# wiki

Small self-hosted wiki for Markdown files.

## Features

- Markdown-based articles (`.md` files from a content directory)
- Client-side Mermaid rendering for `mermaid` code blocks
- Full-text search with Lunr
- Section-level search indexing with anchor links to matched parts
- Search integrated into the same article tree UI (filter + expand matches)
- "On this page" nested table of contents for article headings
- systemd deployment via `make install`

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
