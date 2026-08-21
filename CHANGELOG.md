# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Local collector mode** — `connection: local` hosts are polled by reading `/proc` directly (loadavg, meminfo, stat incl. per-core, diskstats, net/dev, TCP/UDP tables, uptime) plus `df` for mounts; no SSH keys needed. `HOST_PROC` env (or compose mount + `pid: host`) supports monitoring the host from inside a container. See README "Monitoring localhost".
- **Project switcher UI** — nav-bar dropdown populated from `/api/projects`; selecting a project scopes the hosts list and alerts pages via `?project_id=` (deep-linkable).
- **Project-scoped alerts** — `GET /api/alerts?project_id=N` filters alerts to hosts in a project; the alerts and alert-history pages honor the current project from URL/header/cookie; invalid ids return 400 instead of panicking on malformed input.
- **Default project bootstrap** — startup auto-creates a `Default` project when none exist and assigns unassigned hosts to it (`EnsureDefaultProject`).

### Changed
- Project middleware rewritten: typed context key, explicit 400 on invalid project scope, no silent first-project fallback ("all projects" remains the default view).

## [0.4.0] - 2026-08-20

### Added
- **Swap + process count metrics** — collectors now emit `mem.swap_total_bytes`, `mem.swap_free_bytes`, `mem.swap_used_bytes`, derived `mem.swap_used_pct` (guarded on no-swap hosts), and `system.process_count` (procfs counts `/proc` PIDs, psutil uses `psutil.pids()`). Swap metrics appear in the host detail Memory panel via auto-discovery.
- **Per-core CPU metrics** — collectors emit `cpu.core.N.user_pct`, `system_pct`, `idle_pct`, `iowait_pct` per core; host detail gains a "CPU Cores" panel with an all-cores overlay or per-core selector. Per-core series are excluded from the aggregate CPU panel.
- **Disk I/O metrics** — collectors emit `diskio.<dev>.read_bytes`, `write_bytes`, `read_ops`, `write_ops` (cumulative counters); host detail gains a "Disk I/O" panel with a device selector showing per-second rates via client-side conversion.
- **TCP/UDP connection states** — collectors parse `/proc/net/{tcp,tcp6,udp,udp6}` and `psutil.net_connections`, emitting `net.tcp.*` / `net.udp.*` state counts (ESTABLISHED, LISTEN, TIME_WAIT, etc.); host detail gains a "Connections" panel with a TCP/UDP selector and per-state bar chart.
- **Filesystem details (multi-mount + inodes)** — collectors parse `df` for all mounts and `df -i` for inodes, emitting `disk.<mount>.{total,used,free}_bytes` and `inodes_used_pct`; host detail Disk panel gains a mount selector with inode usage readout. Threshold wildcards (`disk.*.used_pct`) still match.
- **Host tags filtering** — host list page gets a row of tag chips derived from all hosts' tags; clicking a chip filters both table and tiles views client-side; state reflected in URL query param for deep-linking.
- **Alert history page** — dedicated page at `/alerts/history` showing full alert archive (active, acknowledged, silenced, resolved) with filters by severity, status, and host; silence/unmute and acknowledge actions; deep-linkable filters via URL query params.
- **Alert rules + notification channels to DB** — `alert_rules` and `notification_channels` tables with migration; engine reads from DB, seeds from `thresholds.yaml`; rules evaluated from DB (changes apply without restart); CRUD JSON API for rules and channels.
- **Alert rules & channels management UI** — `/alerts/config` page with two tabs: Alert Rules (list/create/edit/delete with metric type dropdown, scope, thresholds, below toggle) and Notification Channels (list/create/edit/delete with type selector, JSON config editor, enabled toggle).
- **Email notification channel** — SMTP email sending via `net/smtp` with plain auth; config includes smtp_host, smtp_port, username, password, from, to; UI template for config.

## [0.3.0] - 2026-08-20

### Added
- **Dashboard redesign (issue #20)** — Grafana-style dark/light theme with persisted toggle, shared base layout, and themed charts across all pages
- **Host list** — dense sortable table (default) with live CPU/MEM/Uptime values + compact tiles toggle; deep-linkable `?view=table|tiles`, 30s auto-refresh
- **Host detail panels** — CPU / Load / Memory / Disk / Network each in a themed panel with live current-value readout; 30s in-place chart refresh (no rebuild), paused when tab hidden
- **Compare table** — per-host last-value table below the chart; selections deep-link via query params
- **Alerts filtering** — All / Warning / Critical / Down segmented filter with count badges; 60s auto-refresh
- **Monitor page** — stats readouts (DB size, host count, interval) + collector status table, 30s auto-refresh
- **Embedded assets** — templates and static files (CSS, JS, vendored Chart.js) shipped via `go:embed`; no CDN dependencies
- **New JSON endpoints** — `/api/hosts/status` (live per-host values), `/api/monitor` (self-monitoring stats), `/api/alerts` GET without `host_id` (all alerts)
- **Uptime label on host page** — shows "Uptime: Xd Yh Zm" from `uptime.seconds`
- **Interface picker on host pages** — defaults to `eth0`, hides virtual interfaces (veth/br/docker)
- **Per-second network rates** — `net.*.rx_bytes`/`tx_bytes` cumulative counters converted to rates (host charts client-side, compare + API server-side via `toRateSeries`)
- **Lower-bound thresholds** — new `below: true` option alerts when a metric drops *under* the threshold (e.g. `uptime.seconds`); thresholds are upper-bound by default
- **Alert acknowledge action** — `POST /api/alerts?action=acknowledge&id=` wired to the dashboard button
- **Single-metric JSON endpoint** — `/api/host/:id/metric/:metric?timeRange=&resolution=` returns one series (derives `used_pct`, converts net counters to rates)
- **Storage module split into domain stores** — `storage.go` split into 10 focused files:
  - `interfaces.go` — explicit interfaces for HostStore, SampleStore, AlertStore, ProjectStore, Downsampler, Cleanup, Migrator
  - `hoststore.go` — `GetHosts()`, `SeedHosts()`
  - `samplestore.go` — `SaveSamples()`, `GetSamples()`
  - `alertstore.go` — `InsertAlert()`, `GetActiveAlert()`, `UpdateAlert()`, `AcknowledgeAlert()`, `SilenceAlert()`, `GetAlerts()`
  - `projectstore.go` — `GetProjects()`, `GetProjectHosts()`, `GetProjectHealth()`
  - `downsampler.go` — `Downsample()`, `downsampleRawTo1m()`, `downsample1mTo1h()`
  - `cleanup.go` — `Cleanup()`
  - `migrator.go` — `Migrate()`
  - `util.go` — shared helpers (`parseTags`, `matchesTagQuery`, `resolutionMap`, `HostStatusInfo`)
  - `storage.go` — only `DB` struct, `New()`, types, `Close()`
- **SSHClient adapter extracted from collectors** — new `internal/ssh` package:
  - `SSHClient` interface with `Exec(ctx, target, cmd)` method
  - `sshClient` implementation with configurable `SSHTarget`, `SSHTargetDefaults`
  - `SSHTargetFromHost` free function for collector→SSH mapping
  - `SSHTargetDefaults` in constructor for defaults (`StrictHostKeyChecking=no`, etc.)
  - Collectors (`PsutilCollector`, `ProcfsCollector`, `TailscaleCollector`) use functional options for `SSHClient` injection
  - Shared `SSHClient` created in `main.go` and injected into all collectors
  - Backward-compatible `execSSH` wrapper in `collector/ssh.go`

### Fixed
- **Release changelog empty** — release workflow did a shallow checkout (`fetch-depth: 1`), so `git describe --tags --abbrev=0 HEAD^` failed and no release body was generated; `actions/checkout` now uses `fetch-depth: 0` (#30)
- **Charts not rendering** — host/compare/monitor pages fetched metric keys that no longer existed; canvases now render from the JSON API on DOMContentLoaded
- **Blank network chart** — `pickDefaultInterface` received the names array instead of the object, returning index `"0"` (no matching interface)
- **uptime.seconds and cpu.load_* zeroed** — SSH `Warning: Permanently added ... to the list of known hosts.` stderr line was parsed as field[0]; fixed with `-o LogLevel=ERROR` and parsers that scan for the first numeric token
- **CPU percentages understated** — `guest`/`guest_nice` were added to the total but are already counted within `user`/`nice` on Linux
- **Alerts page 500** — NULL `acknowledged_at`/`resolved_at`/`silenced_until` scanned into `int64`; now `sql.NullInt64` via shared `scanAlertRow` helper
- **Test coverage** — removed `-race -coverprofile` flags (Go 1.25 `covdata` format incompatible with runner)
- **golangci-lint removed** — binary built with Go 1.23 incompatible with GitHub Actions Go 1.25 runner
- **Docker build** — aligned Go version to 1.22 in Dockerfile

### Changed
- **Metrics API** — `/api/host/:id/metrics` and `/api/host/:id/metric/:metric` now accept `timeRange` and `resolution` query params; `/api/compare` accepts `metric`, `hosts`, `timeRange`, `resolution`
- **Dashboard rendering** — Chart.js draws from client-fetched JSON instead of HTMX partial fragments; htmx removed entirely, shared `base.html` layout with embedded assets
- **Themes** — default dark with light option, persisted across sessions and the login/auth boundary
- **Go version pinned to 1.22** in CI, Dockerfile, and `go.mod` (was 1.25.0)
- **CI simplified** — removed golangci-lint (incompatible with Go 1.25 runner), rely on `go vet` + `go fmt` + tests
- **Dockerfile updated** to use `golang:1.22-alpine` builder

### Architecture
- **Collector chain** — PsutilCollector → ProcfsCollector → TailscaleCollector fallback (ADR-0001)
- **Storage** — 6 focused domain stores with explicit interfaces (HostStore, SampleStore, AlertStore, ProjectStore, Downsampler, Cleanup)
- **Scheduler** — background downsampling (minute) and cleanup (daily) tickers
- **Dashboard** — Chart.js client-side charts fed by the `/api/host/:id/metrics` and `/api/host/:id/metric/:metric` JSON endpoints; templates + static assets embedded via `go:embed`
- **Design system** — Nord-inspired dark/light theme with persisted toggle, themed charts; documented in ADR-0006

## [0.1.0] - 2026-08-15

### Added
- Initial implementation of uptime monitor MVP
- Collector chain with Psutil, Procfs, Tailscale fallback
- SQLite storage with WAL mode, downsampling (1m/1h), retention (7d/90d/∞)
- htmx + Chart.js dashboard with single-password auth
- Alerting engine with collection failure detection, metric thresholds, webhooks (Slack/Discord/PagerDuty)
- Project health rollup (tag queries + explicit lists)
- Docker Compose deployment
- Self-monitoring page (`/monitor`)