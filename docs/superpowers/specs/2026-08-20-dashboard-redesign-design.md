# Design: Professional Dashboard Redesign (Issue #20)

Date: 2026-08-20
Status: Approved for implementation

## Goal

Make the uptime-monitor dashboard look professional and deliberate — Ops/Grafana-inspired. Dark default with dark/light toggle, dense data-first layout, monospace numerals, tight grids, high-contrast semantic status colors, minimal chrome. Self-contained assets (vendored Chart.js, no CDN, no htmx). Applied consistently across all templates. This follows the chart/data correctness work in #19 (merged).

## Decisions Summary

| Decision | Choice |
|----------|--------|
| Theme | Dark default; light twin; manual toggle persisted; `prefers-color-scheme` honored pre-choice |
| Visual language | Grafana-inspired restrained minimalism; Nord-derived curated palette; single accent |
| Typography | System UI stack + monospace for all data; uppercase micro-labels |
| htmx | Dropped entirely (dead code since #19); NOT vendored |
| Chart.js | Vendored single file, served via `go:embed` |
| Templates | Embedded via `go:embed`; all pages share `base.html` |
| Navigation | Top nav bar (A), theme toggle in bar |
| Host list | Dense table default (A) + compact tiles (B) via segmented toggle, deep-linked via URL |
| Auto-refresh | Host detail 30s, host list 30s, monitor 30s, alerts 60s; compare manual |
| Chart updates | In-place `chart.update()`, no rebuild; pause when tab hidden |
| Deep-linking | timeRange, resolution, metric, view in query params |

## Architecture & File Layout

```
internal/dashboard/
  static/
    chart.min.js          # vendored Chart.js v4.4.1 (single file)
    app.css               # design system: all theme vars, layout, components
    app.js                # theme toggle, nav active, chart defaults, refresh helpers
  templates/
    base.html             # shared layout: head, CSS, nav bar, theme toggle, blocks
    login.html            # standalone (no nav, pre-auth), same theme vars
    index.html            # extends base; host list (table default + tiles toggle)
    host.html             # extends base; chart panels + auto-refresh
    compare.html          # extends base; analysis view, manual refresh
    projects.html         # extends base
    alerts.html           # extends base
    monitor.html          # extends base
```

- `base.html` defines `{{define "base"}}` with a `{{template "content" .}}` block. Pages define `content`.
- Login is standalone (no nav pre-auth) but uses the same `app.css` theme tokens and theme-init JS so the toggle state carries across the auth boundary.
- `dashboard.go` uses `//go:embed static templates` into an `embed.FS`, parsed once in `NewServer`.
- Fixes today's `ParseGlob("internal/dashboard/templates/*.html")` relative-path fragility (binary breaks when run outside repo root).
- Static served at `/static/...` via `http.FileServer` wrapped in the auth middleware.

## Resolved Deviations (during implementation)

These were decided during implementation and override the lines above where they conflict:

- **`/static/` is served WITHOUT auth middleware** (contradicts line 49). The login page is pre-auth and must load `app.css`/`app.js` to render; the static files contain only CSS/JS/Chart.js (no data). All data-bearing routes remain behind auth. Rationale: an auth-wrapped static route would force the login page to inline all styles/scripts.
- **Default theme is strictly dark; `prefers-color-scheme` is NOT honored for the initial value** (contradicts line 87's "honors prefers-color-scheme before first manual choice"). The requirement "default dark" plus the chosen visual direction (Grafana-style dark) take precedence; OS preference only becomes relevant if a user explicitly toggles. `color-scheme`/`theme-color` still adapt per active theme.

## Design System & Theming

### Palette (Nord-inspired, curated)

| Token | Dark | Light | Use |
|---|---|---|---|
| `--bg` | `#11151c` | `#f8f9fb` | page background |
| `--bg-panel` | `#161b24` | `#ffffff` | panels, cards, table rows |
| `--bg-hover` | `#1d232d` | `#f0f2f6` | hover states |
| `--border` | `#2a313c` | `#d8dee9` | panel borders, table rules |
| `--text` | `#d8dee9` | `#2e3440` | body text |
| `--text-dim` | `#5e81ac` | `#4c566a` | secondary labels |
| `--text-faint` | `#4c566a` | `#8a94a6` | tertiary |
| `--accent` | `#8fbcbb` | `#5e81ac` | active nav, links, focus |
| `--ok` | `#a3be8c` | `#6a9955` | status OK |
| `--warn` | `#ebcb8b` | `#c99a2e` | status WARNING |
| `--crit` | `#bf616a` | `#c0392b` | status CRITICAL |
| `--down` | `#4c566a` | `#8a94a6` | status DOWN/UNKNOWN |

One accent (teal/blue), one restrained semantic status set. No gradients, no purple, no rounded-glass cards, no emoji icons.

### Typography

- UI: `system-ui, -apple-system, sans-serif`
- Data (values, numerals, chart ticks, timestamps, table cells): `ui-monospace, 'SF Mono', 'Cascadia Mono', Menlo, Consolas, monospace`
- `font-variant-numeric: tabular-nums` on data columns/ticks
- Panel/label headers: uppercase, `letter-spacing: .05em`, 11px
- `…` single ellipsis char in loading states ("Loading…")

### Components (all theme-aware via CSS custom properties)

- **Nav bar**: 44px, `--bg-panel` bg, bottom `--border`, left wordmark + right nav links + theme toggle. Active page: `--accent` underline + `--accent` text.
- **Panels**: `--bg-panel`, 1px `--border`, 3px left accent edge (per-metric), 8px padding, panel-head with uppercase title + right-aligned live value.
- **Status pill**: color dot `●` + uppercase label (never color-only — WCAG).
- **Segmented control** (Table|Tiles, severity filters): pill group, active = `--accent` fill.
- **Buttons/inputs/selects**: `--bg-panel`, `--border`, `:focus-visible` ring in `--accent`. Explicit background/color on native `<select>` (Windows dark mode).
- **Theme toggle**: `☾`/`☀` icon button with `aria-label="Toggle theme"`, keyboard accessible. Persists `localStorage('theme')`; defaults dark; honors `prefers-color-scheme` before first manual choice; sets `data-theme` on `<html>`; `[data-theme="light"]` overrides vars. Sets `color-scheme: dark|light` on `<html>` and `<meta name="theme-color">` to match `--bg`.

### Chart theming

- Chart.js global defaults read from the CSS vars (grid color, tick color, font stack) at init; re-read on theme change so charts restyle live.
- Series palette (6): `[--accent, --ok, --warn, --crit, #b48ead, #81a1c1]`. The last two are fixed Nord accent variants (not theme tokens) so multi-series lines stay distinguishable in both themes.

## Per-Page Layout & Behavior

### base.html (shared)
Nav bar (wordmark + links Hosts/Compare/Projects/Alerts/Monitor + theme toggle), skip link to main content, `main` block, script includes (app.js, chart.js). Login is standalone (no nav) but shares CSS tokens + theme init.

### index.html — Host list
- Default **Table**: columns Host / Status / CPU / MEM / Uptime; tags inline under host name; host name links to `/host/:id`. Rows separated by `--border`, hover `--bg-hover`, status dot+label. Sortable columns (click header), in-memory.
- **Tiles** toggle (segmented control in toolbar row with page title): compact grid cards, same fields.
- Auto-refresh 30s; URL param `view=table|tiles` deep-links the choice (set via `history.replaceState`, no reload); `localStorage` stores the chosen view as the default on future visits.

### host.html — Host detail
- Header: host name (mono), status dot, connection/uptime meta, tags.
- Toolbar: time range (1h/6h/24h/7d/30d) + resolution (raw/1m/1h) selects (exist; restyled).
- Panel grid: CPU, Load, Memory, Disk, Network. Each panel: uppercase title, 3px accent edge, live current-value readout top-right, chart below. Net panel keeps interface picker (filtered, defaults eth0).
- Auto-refresh 30s, in-place `chart.update()`; pauses when tab hidden.

### compare.html — Compare
- Toolbar: metric select, multi-host select, range, resolution (exist) + manual Refresh button. No auto-refresh.
- One large chart panel + compact table below: per-host last value for the selected metric (numeric, mono, tabular-nums).

### projects.html — Projects
- Dense card grid (exists), restyled to panel style; status per project; host list inside.

### alerts.html — Alerts
- Filter row: All / Warning / Critical / Down segmented control + count badge per severity.
- Alert rows: severity-colored left edge, dot+severity label, host:type title, metric/message/fired-time meta, Acknowledge button (keeps existing wired action).
- Auto-refresh 60s.

### monitor.html — Self-monitoring
- Stats row (DB size, host count, interval) as numeric readouts + collector status table (exists), restyled. Auto-refresh 30s.

### login.html
Centered card, wordmark, password field with label + `autocomplete="current-password"`, submit button. Theme applied via saved preference (no toggle pre-auth; vars active).

## Auto-Refresh

| Page | Interval |
|------|----------|
| Host detail | 30s |
| Host list | 30s |
| Monitor | 30s |
| Alerts | 60s |
| Compare | Manual (Refresh button) |

All updates in-place via `chart.update()` where charts exist; pause when `document.visibilityState` is hidden.

## Accessibility / Guidelines Compliance

From the Web Interface Guidelines review (loaded during design):

- Theme toggle: `aria-label`, keyboard accessible, `<button>`.
- `color-scheme` set on `<html>` for both themes (dark-mode scrollbars/inputs).
- `<meta name="theme-color">` matches `--bg` per theme.
- Native `<select>` explicit background/color.
- `font-variant-numeric: tabular-nums` on data columns.
- `:focus-visible` rings in `--accent`; never `outline: none`.
- Honor `prefers-reduced-motion`; animate `opacity`/`transform` only; no `transition: all`.
- `<button>` for actions (ack, toggle, view switch); `<a>` for navigation; labels on all selects.
- Status = color dot + text label, never color-only.
- Skip link to main content.
- Loading states end with `…`.
- URL reflects state (timeRange, resolution, metric, hosts, view in query params); view choice additionally persisted in `localStorage` as default.
- Semantically correct `<table>` for host list, alert rows, monitor table.

## Non-Goals (YAGNI)

- No sidebar nav (chose top bar; revisit later if wanted).
- No htmx vendoring or revival.
- No webfonts/font downloads (system stack + mono).
- No chart type additions (line charts remain the primitives).
- No new pages or routes.
- No backend behavior changes beyond embedding + static serving; all data flows unchanged.

## Testing

- `go build`, `go vet`, `go test ./...` stay green.
- Templates must still parse with `html/template` after the base/extend refactor.
- Playwright verification (existing `/tmp/opencode` scripts, rerun against redeployed container):
  - All 7 pages render; login flow works.
  - Dark default; toggle to light persists across pages and reload.
  - Host charts render with themed grid/ticks; net picker works.
  - Table view default; tiles toggle switches and persists.
  - Auto-refresh updates values (check a live value changes within ~35s).
  - Compare manual refresh works.
- `web-design-guidelines` review run against finished templates.