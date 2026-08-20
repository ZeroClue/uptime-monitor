# Domain Model: Uptime & System Status Monitor

## Core Concepts

**Monitor** — The central Docker container that polls hosts, stores metrics, and serves the dashboard.

**Host** — A target machine (server, VM, device) identified by name, connection endpoint, and auth credentials. Has tags for grouping.

**Metric** — A named, typed measurement (e.g., `cpu.user_pct`, `mem.used_bytes`, `disk.root.used_pct`) with timestamp and host_id.

**Sample** — A single metric reading at a point in time. Raw samples retained for 7 days.

**Aggregate** — Downsampled metric (1-min, 1-hour) for long-term retention and fast dashboard queries.

**Collector** — A strategy for fetching metrics from a host. Multiple collectors tried in order until one succeeds.

**Connection** — How the Monitor reaches a Host: SSH (with key), Tailscale IP, or VPN endpoint.

**Dashboard** — Web UI served by the Monitor showing host list, per-host drill-down, and metric graphs.

**Project** — A named group of hosts (explicit list or tag query) with rolled-up health status for the overview dashboard.

## Host Config Schema (`hosts.yaml`)

```yaml
hosts:
  - name: web-01
    connection: ssh          # ssh | tailscale
    endpoint: 10.0.0.5       # IP or hostname
    port: 22                 # default 22
    user: monitor            # SSH user
    key_path: /keys/web-01   # relative to config dir, required
    sudo: false              # whether to sudo for df /proc
    timeout: 10s             # connection + command timeout
    proxy_jump: ""           # optional SSH proxy jump host
    tags: [web, prod]        # for grouping / project queries
    collector_preference: "" # optional: force specific collector
```

## Settled Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Collection architecture | Pull (Monitor → Host) | No agent install; matches SSH/Tailscale requirement |
| Storage backend | SQLite (embedded) | Zero-dep, fits in Docker, sufficient for ~100 hosts |
| Host discovery | YAML config file | Simple, version-controllable, no external deps |
| Poll interval | 30 seconds | Standard infra monitoring cadence |
| Raw retention | 7 days | Debugging window |
| 1-min aggregate retention | 90 days | Operational dashboards |
| 1-hour aggregate retention | Forever | Capacity planning |
| Collector fallback order | 1) SSH+psutil 2) SSH+/proc+df 3) Tailscale+same 4) SNMP/node_exporter (later) | Progressive enhancement; works on any Linux host |
| Core metric schema | cpu, mem, disk, net, uptime namespaces (see CONTEXT.md) | Covers 90% of infra monitoring needs |
| GPU / per-process / containers | Deferred to v2 | Out of scope for MVP |
| Real-time updates | Poll on load + manual refresh | No WebSocket complexity |
| Dashboard views | Host list, host detail, multi-host compare, alert panel, project health overview | Covers operational workflows |
| Dashboard framework | Go + htmx + Chart.js | Single binary, no build step, lightweight |
| Dashboard auth | Single shared password (env var) | Simplest viable auth |
| Host auth | SSH key per host (configurable per host) | Standard, flexible |
| Project model | Tag-based + explicit projects | Ad-hoc queries + structured dashboards |
| Tailscale mode | External (host network) by default | In-container option for v2 |
| Config reload | Docker restart (simplest) | File watcher as enhancement |
| Monitor observability | /healthz + dashboard monitor page | Self-metrics (Prometheus) deferred |
| Chart rendering | Fetch JSON from `/api/host/:id/metrics`, render client-side with Chart.js | htmx target-swap destroyed canvases; single fetch on DOMContentLoaded |
| Net metrics display | Cumulative counters converted to per-second rates client-side (host) and server-side (compare) | Raw counters produce meaningless rising lines |
| Interface picker | Defaults to eth0; veth/br/docker filtered out | Avoids chart spam on virtual interfaces |
| Alert thresholds | Upper-bound by default; `below: true` opts into lower-bound (uptime.seconds) | uptime must alert when *under* a threshold |
| SSH command output | `LogLevel=ERROR` suppresses host-key warning; parsers scan for first numeric token | "Permanently added" stderr line was parsed as field[0], zeroing uptime/load |