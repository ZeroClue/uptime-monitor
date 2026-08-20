# ADR 0002: Dashboard Framework

## Status
Accepted (amended by ADR-0005, ADR-0006)

## Context
Need a web dashboard served from the monitor container. Requirements: lightweight, fast, graphing/plotting, single binary preferred, minimal build complexity.

## Decision
**Go + Chart.js** (client-side rendering)

- Go: single static binary, excellent stdlib for HTTP, SQLite, SSH
- Chart.js: canvas-based charts, sufficient for time-series, themed by the design system (ADR-0006)
- htmx was originally chosen for server-rendered fragments, but was removed in the Phase 2 redesign — charts now render entirely client-side from the JSON API (ADR-0005)

## Consequences
- **Positive**: ~5MB Docker image; no npm/node in build; fast cold start; easy to embed templates/assets (go:embed); canvases owned by Chart.js with no DOM swap races
- **Negative**: Less component reuse than React/Vue; Chart.js less performant than uPlot for high-frequency updates
- **Mitigation**: uPlot swap is straightforward if Chart.js becomes bottleneck

## Alternatives Considered
- **Python + Jinja2 + Chart.js** — larger image, slower startup, more deps
- **Node + React + uPlot** — build step, larger image, more complex
- **Rust + askama + uPlot** — steeper learning, slower iteration