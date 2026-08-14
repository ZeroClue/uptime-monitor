# ADR 0002: Dashboard Framework

## Status
Accepted

## Context
Need a web dashboard served from the monitor container. Requirements: lightweight, fast, graphing/plotting, single binary preferred, minimal build complexity.

## Decision
**Go + htmx + Chart.js**

- Go: single static binary, excellent stdlib for HTTP, SQLite, SSH
- htmx: server-rendered HTML fragments, no JS build step, progressive enhancement
- Chart.js: canvas-based charts, works with htmx, sufficient for time-series

## Consequences
- **Positive**: ~5MB Docker image; no npm/node in build; fast cold start; easy to embed templates/assets
- **Negative**: Less component reuse than React/Vue; Chart.js less performant than uPlot for high-frequency updates
- **Mitigation**: uPlot swap is straightforward if Chart.js becomes bottleneck

## Alternatives Considered
- **Python + Jinja2 + Chart.js** — larger image, slower startup, more deps
- **Node + React + uPlot** — build step, larger image, more complex
- **Rust + askama + uPlot** — steeper learning, slower iteration