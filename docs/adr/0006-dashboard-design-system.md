# ADR 0006: Dashboard Design System

## Status
Accepted

## Context
The Phase 2 dashboard redesign (issue #20) needed a cohesive visual identity across all pages (host list, host detail, compare, projects, alerts, monitor, login) and a way to theme the client-side Chart.js charts. The prior UI used ad-hoc inline styles and a fixed light color scheme.

## Decision
**A single design system built on CSS custom properties, with a dark default and a persisted light toggle.**

- **Palette**: Nord-inspired — dark and light token sets defined as CSS custom properties (`--bg`, `--surface`, `--text`, `--accent`, `--series-1..6`, `--ok`/`--warn`/`--crit`) in `app.css`
- **Default theme**: **dark**; the light theme is opt-in via a toggle in the top-right corner
- **Persistence**: theme choice stored in `localStorage` (`uptime-monitor-theme`) and applied before paint to avoid flash-of-wrong-theme; it carries across the login/auth boundary (the login page is served unauthenticated and applies the saved theme too)
- **Chart theming**: Chart.js defaults and per-dataset colors are re-read from the CSS variables on theme change, so charts restyle live without a rebuild
- **Static assets**: `/static/` is served **without** the auth middleware. This is a deliberate deviation: the pre-auth login page must load CSS/JS, and static files contain no data. (The underlying auth model is unchanged — see ADR-0003.)

## Consequences
- **Positive**: consistent look across pages; one source of truth for colors (CSS variables); charts match the theme; no CDN dependencies (CSS, JS, and vendored Chart.js are embedded via `go:embed`)
- **Negative**: two visual themes must be maintained; CSS custom properties are unsupported in very old browsers (irrelevant for a self-hosted ops tool)
- **Deliberate deviation**: `prefers-color-scheme` is **not** honored for the initial theme — the dark default is strict by design; the OS preference only matters after a manual toggle

## Alternatives Considered
- **Honor `prefers-color-scheme` first** — rejected: the spec chose dark-by-default for a consistent Grafana-style ops look
- **Inline styles per element** — rejected: duplicated, unmaintainable, and cannot theme charts
- **External CSS framework (Bootstrap/Tailwind)** — rejected: adds a dependency for a small self-hosted tool

## Related
- Amends ADR-0002 (chart rendering mechanism unchanged; theming added)
- Complements ADR-0005 (client-side Chart.js rendering)