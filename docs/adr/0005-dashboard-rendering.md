# ADR 0005: Dashboard Chart Rendering

## Status
Accepted

## Context
The host, compare, and monitor pages originally rendered charts via HTMX partial fragments served from `/api/host/:id/metric/:metric`. During the chart-fix work (issue #19) we found this architecture was fragile:

- Templates fetched metric keys that no longer existed, so charts silently rendered nothing
- HTMX target-swap destroyed/recreated canvases, causing re-init races and lost chart state
- The compare and monitor pages mixed server-rendered panels with client-side logic inconsistently

## Decision
**Chart.js renders entirely client-side from JSON fetched once on `DOMContentLoaded`.**

- `/api/host/:id/metrics` and `/api/host/:id/metric/:metric` return JSON series (supporting `timeRange` and `resolution` query params)
- `/api/compare` returns JSON for `metric`, `hosts`, `timeRange`, `resolution`
- Templates draw charts from the fetched data; interface pickers and time-range changes re-fetch JSON and rebuild charts
- htmx is no longer used for metric panels (dead `<script>` tags remain in templates pending Phase 2 cleanup)

## Consequences
- **Positive**: single source of truth for data (the JSON API); canvases owned by Chart.js with no DOM swap races; time-range switching is one fetch + rebuild; compare page can plot multiple hosts with one request
- **Negative**: slightly more client-side JS; initial render depends on one JSON round-trip
- **Mitigation**: data is small (max ~500 points/host/series); uPlot remains an easy swap if Chart.js becomes a bottleneck

## Related
- Amends ADR 0002 (the Go + Chart.js choice stands; the htmx fragment mechanism does not)
- Replaces the "HTMX metric panel fragments" behavior described in README, SPEC.md, and CONTEXT.md