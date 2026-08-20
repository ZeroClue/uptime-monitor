# SPEC: Uptime & System Status Monitor

## Problem Statement

Operators need a simple, self-hosted monitoring solution that can poll Linux hosts over SSH or Tailscale, collect system metrics (CPU, memory, disk, network, uptime), store them efficiently, and present them in a clean dashboard with per-host drill-down, multi-host comparison, and project-level health overviews. The solution must run as a single Docker container with minimal configuration and no external dependencies.

## Solution

A Go-based monitor that:
- Pulls metrics from configured hosts via SSH/Tailscale using a fallback collector chain (psutil → `/proc`+`df` → Tailscale)
- Stores samples in embedded SQLite with automatic downsampling (1-min, 1-hour aggregates)
- Serves a Chart.js dashboard with single-password auth
- Groups hosts by tags and explicit projects for operational views
- Exposes `/healthz` for orchestration and a self-monitoring dashboard page

## User Stories

1. As an **operator**, I want to **add a host by editing a YAML file**, so that **I can start monitoring a new server in under a minute**.
2. As an **operator**, I want the **monitor to try multiple collection methods automatically**, so that **it works on any Linux host regardless of installed tools**.
3. As an **operator**, I want to **see a host list with sparklines and status**, so that **I can spot problems at a glance**.
4. As an **operator**, I want to **drill into a host and see CPU, memory, disk, and network graphs over time**, so that **I can diagnose performance issues**.
5. As an **operator**, I want to **select time ranges and toggle between raw and aggregated data**, so that **I can see both recent detail and long-term trends**.
6. As an **operator**, I want to **compare the same metric across multiple hosts**, so that **I can spot fleet-wide patterns**.
7. As an **operator**, I want to **group hosts into projects by tags or explicit lists**, so that **I can see rolled-up health for each service/environment**.
8. As an **operator**, I want to **receive alerts when a host stops reporting or metrics breach thresholds**, so that **I learn about problems before users do**.
9. As an **operator**, I want to **acknowledge/silence alerts from the dashboard**, so that **noise is reduced during known maintenance**.
10. As an **operator**, I want to **deploy the monitor as a single Docker container**, so that **operations are simple and portable**.
11. As an **operator**, I want to **authenticate to the dashboard with a single shared password**, so that **access control is trivial to manage**.
12. As an **operator**, I want to **use per-host SSH keys stored outside the config**, so that **credentials are not in version control**.
13. As an **operator**, I want the **monitor to reach hosts via Tailscale when SSH is not directly accessible**, so that **I can monitor hosts in tailnets without VPN**.
14. As an **operator**, I want to **see the monitor's own health (poll success rate, latency, errors)**, so that **I know the monitoring system itself is working**.
15. As an **operator**, I want **configuration changes to take effect on container restart**, so that **deployment is predictable**.
16. As an **operator**, I want **raw metrics retained for 7 days, 1-min aggregates for 90 days, 1-hour aggregates forever**, so that **I have debugging detail and long-term capacity data without unbounded growth**.
17. As an **operator**, I want to **configure collection interval, timeouts, and sudo per host**, so that **I can tune for slow or restricted hosts**.
18. As an **operator**, I want to **optionally use a proxy jump host for SSH**, so that **I can reach hosts behind bastions**.
19. As an **operator**, I want to **see which collector succeeded for each host**, so that **I can debug collection issues**.
20. As an **operator**, I want to **receive notifications via webhook (Slack/Discord/PagerDuty)**, so that **alerts reach my existing on-call workflow**.

## Implementation Decisions

### Architecture
- **Single Go binary** — compiles to ~10MB static binary, runs in scratch/distroless container
- **Pull model** — monitor initiates SSH/Tailscale connections to hosts; no agent required on targets
- **Collector interface** — `Collector` interface with `Collect(host Host) ([]Sample, error)`; implementations tried in priority order until one succeeds
- **Scheduler** — fixed-interval ticker (default 30s) per host; jittered start to avoid thundering herd
- **Storage layer** — SQLite with WAL mode; tables: `hosts`, `samples_raw`, `samples_1m`, `samples_1h`, `projects`, `alerts`
- **Downsampling** — background goroutine runs every minute: computes 1-min aggregates from raw, 1-hour from 1-min; deletes expired raw/1-min per retention policy

### Collector Chain (ADR-0001)
1. **PsutilCollector** — SSH `python3 -c 'import psutil; print(json.dumps(...))'`; returns structured JSON; requires Python + psutil on target
2. **ProcfsCollector** — SSH `cat /proc/loadavg /proc/meminfo /proc/stat /proc/uptime` + `df -B1`; parses text; always available on Linux
3. **TailscaleCollector** — same as above but connects via Tailscale IP (resolved from host config)
4. **SNMPCollector / NodeExporterCollector** — deferred to v2

Each collector returns normalized `Sample{Metric, Value, Timestamp, HostID}`.

### Host Configuration (ADR-0003, ADR-0004)
```go
type Host struct {
    Name               string
    Connection         string   // "ssh" | "tailscale"
    Endpoint           string   // IP or hostname
    Port               int      // default 22
    User               string
    KeyPath            string   // relative to config dir
    Sudo               bool
    Timeout            duration // default 10s
    ProxyJump          string   // optional
    Tags               []string
    CollectorPreference string  // optional: force specific collector
}
```
Config loaded from `/config/hosts.yaml` at startup; reload on container restart.

### Dashboard (ADR-002, ADR-005)
- **Go `net/http` + `html/template`** — server-rendered base layout
- **Chart.js** — canvas charts for time-series; rendered client-side from JSON fetched on load
- **Routes**:
  - `GET /` — host list with sparklines
  - `GET /host/:id` — host detail with metric panels
  - `GET /compare` — multi-host comparison (query: `metric=...&hosts=...`)
  - `GET /projects` — project health overview
  - `GET /alerts` — alert panel with ack/silence actions
  - `GET /monitor` — self-monitoring page (collector status, latency, errors)
  - `GET /healthz` — liveness/readiness for orchestration
  - `POST /login`, `POST /logout` — session auth

### Authentication (ADR-003)
- **Dashboard**: `DASHBOARD_PASSWORD` env var; login sets HttpOnly Secure cookie (24h); middleware validates on all routes except `/healthz`, `/login`
- **Host connections**: SSH key per host at `KeyPath`; keys mounted via `/config/keys/` volume; optional `Sudo` for `df`/`/proc` access

### Projects (ADR-003)
- **Tag-based**: `Project{Name, TagQuery}` — e.g., `web AND prod` matches hosts with both tags
- **Explicit**: `Project{Name, HostIDs[]}` — manual list
- Health rollup: project status = worst host status (down > critical > warning > ok)

### Alerting
- **Collection failure**: 3 consecutive failed polls → host status = down; alert fired
- **Metric thresholds**: configurable per metric (e.g., `cpu.user_pct > 90` warning, `> 95` critical; `disk.*.used_pct > 85` warning, `> 95` critical)
- **Notifications**: stdout log + optional webhook URLs (Slack/Discord/PagerDuty) via env/config
- **Acknowledgment**: dashboard button sets `Alert.AcknowledgedAt`; silenced alerts don't re-notify

### Tailscale (ADR-004)
- **Default**: monitor uses host network mode (`--network host`) to access host's Tailscale interface; hosts configured with `connection: tailscale` use tailnet IPs
- **v2**: in-container `tailscaled` with `NET_ADMIN` + `/dev/net/tun`

### Config Reload (ADR-004)
- **MVP**: Docker restart required; `hosts.yaml` read at startup only
- **Enhancement**: `fsnotify` watcher + SIGHUP handler for hot-reload

### Observability (ADR-004)
- **Self-metrics** (exposed on `/monitor` page, not Prometheus yet):
  - `poll_total{host, collector, result}` counter
  - `poll_latency_seconds{host, collector}` histogram
  - `collector_last_success{host}` timestamp
  - `db_size_bytes` gauge
  - `host_status{host}` gauge (0=ok, 1=warning, 2=critical, 3=down)

## Testing Decisions

- **Unit tests** for:
  - Collector implementations (mock SSH exec, test parsing against real `/proc` fixtures)
  - Downsampling logic (deterministic aggregates from known samples)
  - Config parsing (valid/invalid YAML, defaults applied)
  - Alert evaluation (threshold crossing, ack suppression)
  - Project health rollup (tag query matching, explicit list, worst-status)
- **Integration tests** (require Docker):
  - Full collection cycle against test containers (SSH + psutil, SSH + procfs)
  - Dashboard HTTP endpoints (auth, HTML fragments, Chart.js data endpoints)
  - SQLite persistence across restarts
- **Test seams**:
  - `Collector` interface — swap real SSH for in-memory fake
  - `Storage` interface — swap SQLite for in-memory map
  - `Notifier` interface — capture webhook calls
- **Prior art**: similar patterns in `go-metrics` / `prometheus` client tests

## Out of Scope

- Push-based agents / node_exporter scrape (v2)
- GPU metrics, per-process top-N, container/cgroup stats (v2)
- Prometheus `/metrics` endpoint (v2)
- Per-user dashboard auth / OAuth / OIDC (v2)
- In-container Tailscale (v2)
- Config hot-reload without restart (enhancement)
- Distributed / HA monitor deployment
- Log aggregation / log-based alerting
- Anomaly detection / ML-based baselines
- Mobile app / native clients

## Further Notes

- **Schema evolution**: SQLite migrations via `golang-migrate` embedded in binary
- **Time-series queries**: use SQLite's `strftime` for bucketing; consider `timescaledb` extension if scale exceeds ~200 hosts
- **Chart.js data API**: `GET /api/host/:id/metrics` and `GET /api/host/:id/metric/:metric` return JSON series (supports `timeRange` and `resolution`); resolution = `raw` | `1m` | `1h`
- **Client-side rendering**: Chart.js draws charts from JSON fetched once on load (see ADR-0005); htmx no longer serves metric panels
- **Default thresholds**: codified in config with env overrides; shipped as `thresholds.yaml` in `/config/`
- **SSH key management**: document `ssh-keygen -t ed25519` workflow; keys mounted read-only
- **Docker Compose example**: included in repo for one-command deploy