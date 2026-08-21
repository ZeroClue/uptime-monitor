# Uptime & System Status Monitor

A self-hosted, single-binary monitoring solution that polls Linux hosts over SSH or Tailscale, collects system metrics, stores them in embedded SQLite, and serves a Chart.js dashboard.

## Quick Start

```bash
# 1. Clone and build
git clone https://github.com/yourorg/uptime-monitor
cd uptime-monitor
make docker-build

# 2. Prepare config
mkdir -p config/keys
cp config/hosts.yaml.example config/hosts.yaml
cp config/thresholds.yaml.example config/thresholds.yaml
# Edit config/hosts.yaml with your hosts
# Add SSH keys to config/keys/

# 3. Run
DASHBOARD_PASSWORD=changeme docker-compose up -d

# 4. Open dashboard
open http://localhost:8080
```

## Architecture

```
                    ┌─────────────────────────────────────────────────────────────┐
│                           Uptime Monitor (Single Binary)                         │
├─────────────────────┬─────────────────────┬─────────────────────┬──────────────┤
│  Collector Chain    │   Storage (Domain   │   Scheduler         │  Dashboard   │
│  (internal/collector)│   Stores)           │   (internal/       │  (internal/  │
│                     │   (internal/storage)│   scheduler)        │   dashboard) │
├─────────────────────┼─────────────────────┼─────────────────────┼──────────────┤
│ ┌─────────────────┐ │ ┌─────────────────┐ │ ┌─────────────────┐ │ ┌─────────┐ │
│ │ PsutilCollector │ │ │ HostStore       │ │ │ Poll Ticker     │ │ │ Host    │ │
│ │ (psutil JSON)   │ │ │ (hosts CRUD)    │ │ │ (30s + jitter)  │ │ │ List    │ │
│ └────────┬────────┘ │ └────────┬────────┘ │ └────────┬────────┘ │ └────┬────┘ │
│ ┌────────▼────────┐ │ ┌────────▼────────┐ │ ┌────────▼────────┐ │ ┌────▼────┐ │
│ │ ProcfsCollector │ │ │ SampleStore     │ │ │ Poller          │ │ │ Host    │ │
│ │ (/proc + df)    │ │ │ (samples +      │ │ │ (SSH + parsing) │ │ │ Detail  │ │
│ └────────┬────────┘ │ │  downsampling)  │ │ └────────┬────────┘ │ └────┬────┘ │
│ ┌────────▼────────┐ │ ┌────────▼────────┐ │ ┌────────▼────────┐ │ ┌────▼────┐ │
│ │ TailscaleColl.  │ │ │ AlertStore      │ │ │ Downsampler     │ │ │Compare  │ │
│ │ (via Procfs)    │ │ │ (alerts CRUD)   │ │ │ (1m tick)       │ │ │Projects │ │
│ └─────────────────┘ │ └────────┬────────┘ │ └────────┬────────┘ │ └────┬────┘ │
│       │             │ ┌────────▼────────┐ │ ┌────────▼────────┐ │ ┌────▼────┐ │
│       └─────────────►│ SSHClient       │ │ │ Cleanup         │ │ │ Alerts  │ │
│        (SSH transport)│ (internal/ssh)  │ │ │ (daily tick)    │ │ │Monitor  │ │
└───────────────────────┴─────────────────┴─┴─────────────────┴─┴────┴─────┘
                              │
                              ▼
                        ┌─────────────────┐
                        │  SQLite (WAL)   │
                        │  samples_raw    │
                        │  samples_1m     │
                        │  samples_1h     │
                        │  hosts/alerts/  │
                        │  projects       │
                        └─────────────────┘
```

**Key Design Decisions:**
- **Pull model** — Monitor initiates connections; no agent required on targets
- **Collector fallback chain** — psutil → `/proc`+`df` → Tailscale (works on any Linux host)
- **Single binary** — ~10MB static Go binary, runs in distroless/scratch container
- **Embedded SQLite** — Zero external dependencies, WAL mode for concurrency
- **Domain stores** — 6 focused storage modules with explicit interfaces
- **SSH transport abstraction** — `SSHClient` adapter decouples collectors from SSH details
- **Downsampling** — Automatic background aggregation per retention policy

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DASHBOARD_PASSWORD` | *required* | Shared password for dashboard login |
| `POLL_INTERVAL` | `30s` | Collection interval per host |
| `LOG_LEVEL` | `info` | Log level: debug, info, warn, error |
| `COOKIE_SECURE` | `false` | Set `true` for HTTPS deployments |
| `DB_PATH` | `/data/monitor.db` | SQLite database path |

### Volumes

| Mount Point | Purpose |
|-------------|---------|
| `/config` | `hosts.yaml`, `thresholds.yaml`, SSH keys (read-only recommended) |
| `/data` | SQLite database file |

### Host Configuration (`/config/hosts.yaml`)

```yaml
hosts:
  - name: web-01
    connection: ssh           # ssh | tailscale
    endpoint: 10.0.0.5
    port: 22
    user: monitor
    key_path: /keys/web-01    # relative to /config
    sudo: false
    timeout: 30s              # per-command execution budget
    ssh_timeout: 10s          # optional: connection phase budget (default 10s)
    collector_timeout: 30s    # optional: whole-collect budget (default 30s)
    proxy_jump: ""
    tags: [web, prod]
    collector_preference: ""  # optional: force specific collector
```

### Threshold Configuration (`/config/thresholds.yaml`)

```yaml
thresholds:
  cpu.user_pct:
    warning: 80
    critical: 95
  mem.used_pct:
    warning: 85
    critical: 95
  disk.*.used_pct:
    warning: 85
    critical: 95
  uptime.seconds:
    warning: 300    # Alert if uptime < 5 min (recent reboot)
    critical: 60    # Alert if uptime < 1 min
    below: true

webhooks:
  - name: slack-alerts
    type: slack
    url: https://hooks.slack.com/services/XXX/YYY/ZZZ
  - name: pagerduty-alerts
    type: pagerduty
    url: https://events.pagerduty.com/v2/enqueue
    secret: your-routing-key
```

## Deployment

### Development (Local Build)

Use the included `docker-compose.yml` which builds locally:

```bash
# Build and run locally
DASHBOARD_PASSWORD=changeme docker-compose up --build -d

# Or just rebuild when code changes
docker-compose up --build -d
```

The default `docker-compose.yml` builds locally and uses `image: uptime-monitor:latest`.

### Production (GHCR Image)

Use the production override to pull from GHCR:

```bash
# Set version (optional, defaults to latest)
export UPTIME_MONITOR_VERSION=0.3.0

# Deploy
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

The production compose (`docker-compose.prod.yml`) pulls from GHCR:
- `ghcr.io/zeroclue/uptime-monitor:0.3.0`
- `ghcr.io/zeroclue/uptime-monitor:latest`

### Manual Docker Run

```bash
# Development (local image)
docker run -d \
  --name uptime-monitor \
  -p 8080:8080 \
  -v $(pwd)/config:/config:ro \
  -v $(pwd)/data:/data \
  -e DASHBOARD_PASSWORD=changeme \
  -e POLL_INTERVAL=30s \
  -e LOG_LEVEL=info \
  --network host \
  --restart unless-stopped \
  uptime-monitor:latest

# Production (GHCR image)
docker run -d \
  --name uptime-monitor \
  -p 8080:8080 \
  -v $(pwd)/config:/config:ro \
  -v $(pwd)/data:/data \
  -e DASHBOARD_PASSWORD=changeme \
  -e POLL_INTERVAL=30s \
  -e LOG_LEVEL=info \
  --network host \
  --restart unless-stopped \
  ghcr.io/zeroclue/uptime-monitor:latest
```

### Environment Variables for Production

Create a `.env` file for production deployment:

```bash
# .env
DASHBOARD_PASSWORD=your-secure-password
TS_AUTHKEY=tskey-auth-xxxxxxxxxxxx
TS_HOSTNAME=uptime-monitor-prod
TS_ROUTES=10.0.0.0/24,192.168.1.0/24
UPTIME_MONITOR_VERSION=0.3.0
```

Then deploy:

```bash
docker-compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env up -d
```

### Tailscale Hosts

For hosts only reachable via Tailscale, use `network_mode: host` and configure hosts with `connection: tailscale`:

```yaml
hosts:
  - name: remote-db
    connection: tailscale
    endpoint: 100.x.y.z    # Tailscale IP
    user: monitor
    key_path: /keys/remote-db
```

### Tailscale SSH (Keyless Authentication)

For true keyless SSH via Tailscale's ACL-based authentication, the monitor can run Tailscale inside the container using its built-in SSH certificate authority.

#### Quick Setup

1. **Generate an ephemeral auth key** in the Tailscale admin console:
   - Settings → Keys → Generate auth key
   - Ephemeral: ✓
   - Pre-authorized: ✓
   - Tags: `tag:monitor`

2. **Create ACL policy** allowing monitor → targets:
   ```json
   {
     "ssh": [
       { "action": "accept", "src": ["tag:monitor"], "dst": ["tag:server"] }
     ]
   }
   ```

3. **Configure docker-compose** with your auth key:
   ```bash
   # .env file
   TS_AUTHKEY=tskey-auth-xxxxxxxxxxxx
   TS_HOSTNAME=uptime-monitor
   # Optional: advertise routes for subnet access
   TS_ROUTES=10.0.0.0/24,192.168.1.0/24
   ```

4. **Tag target hosts** with `tag:server` in Tailscale admin console.

5. **Configure hosts** to use Tailscale SSH (no SSH keys needed):
   ```yaml
   hosts:
     - name: remote-db
       connection: tailscale
       endpoint: 100.x.y.z   # Tailscale IP (or MagicDNS name)
       user: monitor
       # key_path: NOT NEEDED - Tailscale SSH handles auth
       tags: [db, production]
   ```

#### How It Works

- Monitor container runs `tailscaled` with `--tun=userspace-networking`
- On startup, authenticates via `TS_AUTHKEY` and joins your tailnet
- Tailscale's SSH certificate authority issues short-lived certificates per ACL policy
- SSH connections to `tag:server` hosts are authenticated via Tailscale's CA — no SSH keys needed
- Collector uses `connection: tailscale` to indicate Tailscale IP reachability

#### Alternative: Tailscale Sidecar (for separation)

If you prefer separate containers:

```yaml
services:
  tailscale:
    image: tailscale/tailscale:latest
    cap_add: [NET_ADMIN, SYS_MODULE]
    devices: ["/dev/net/tun:/dev/net/tun"]
    environment:
      - TS_AUTHKEY=${TS_AUTHKEY}
      - TS_HOSTNAME=uptime-monitor-sidecar
  monitor:
    environment:
      - TS_SOCKS5_PROXY=tailscale:1055
    depends_on: [tailscale]
```

Then configure hosts with `connection: tailscale` and they'll route through the sidecar's SOCKS5 proxy.

## Dashboard

| Route | Description |
|-------|-------------|
| `/` | Host list — sortable table (default) or tiles, live CPU/MEM/Uptime, 30s refresh; `?view=table|tiles` |
| `/host/:id` | Per-host detail — themed panels (CPU, Load, Memory, Disk, Network) with live readouts, 30s refresh; `?timeRange=&resolution=` |
| `/compare` | Multi-host metric comparison + per-host last-value table; manual refresh, deep-linkable selections |
| `/projects` | Project health overview (tag-based + explicit) |
| `/alerts` | Alert panel with severity filter, acknowledge/silence, 60s refresh |
| `/monitor` | Self-monitoring (collector status, stats), 30s refresh |
| `/healthz` | Liveness/readiness endpoint |
| `/metrics` | Prometheus-format metrics (experimental) |

### API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /api/hosts` | List all hosts (JSON) |
| `GET /api/hosts/status` | Live per-host values (status, CPU, MEM, uptime) for the host list |
| `GET /api/host/:id/metrics?timeRange=&resolution=` | All metrics for a host (JSON) |
| `GET /api/host/:id/metric/:metric?timeRange=&resolution=` | Single metric series (JSON; `mem.used_pct`/`disk.used_pct` derived, net byte counters converted to rates) |
| `GET /api/compare?metric=&hosts=&timeRange=&resolution=` | Multi-host comparison data |
| `GET /api/alerts` | List all alerts; `?project_id=` scopes to a project, `?host_id=` to a host; `POST ?action=acknowledge&id=` or `?action=silence&id=&duration=` |
| `GET /api/monitor` | Self-monitoring stats + collector status |
| `GET /api/projects` | Project list with health status |

### Projects

Hosts and alerts can be scoped to projects. The nav bar's project switcher filters the hosts list and alerts pages via `?project_id=`; API endpoints accept the same param (or an `X-Project-ID` header). On startup the monitor auto-creates a `Default` project and assigns any unassigned hosts to it.

Alert rules and notification channels participate in scoping: rules with a project apply only to hosts in that project; rules without one are global. Notification delivery follows the same rule — a channel scoped to a project receives only that project's alerts; global channels receive everything. `/alerts/config` lists and creates within the active project (new rules inherit it; omit `project_id` in the API to create globals).

Manage projects at **Project Config** (`/projects/config`): create, edit (name/type/tag query/default), and delete. The host form has a project dropdown defaulting to the active project; the host list shows a Project column once more than one project exists.

### Poll retries

Failed polls retry with exponential backoff — `delay = min(base_delay * 2^attempt + jitter, max_delay)`. Defaults: 3 attempts, 2s base, 30s max, 0.2 jitter; configure globally via the `retry:` block in `hosts.yaml` or per host (`retry_max_retries`, `retry_base_delay`, `retry_max_delay`, or the host form). Authentication and host-key failures are never retried. The last poll's attempt count and total backoff time appear in `/api/hosts/status` (`poll_attempts`, `retry_time_ms`).

### SSH host keys

`ssh_host_key_policy` controls host-key verification (set globally in hosts.yaml or per host):

| Policy | Behavior |
|--------|----------|
| `strict` *(default)* | Fail when the key is missing **or** changed. Seed `/config/known_hosts` first: `ssh-keyscan -H host >> config/known_hosts` |
| `auto` | Accept and record new keys on first connect (`StrictHostKeyChecking=accept-new`) but still fail on key changes |
| `known` | Use the managed known_hosts file as-is (alias of strict) |

New and changed keys are logged for audit. The file location is configurable via `ssh_known_hosts_file`. Note: if `/config` is mounted read-only (the compose default), seed the file from the host side; `auto` mode needs a writable path.

### Host timeouts

Three per-host budgets (all optional, set in hosts.yaml or the host form):

| Field | Phase | Default |
|-------|-------|---------|
| `timeout` | each SSH command execution | 30s |
| `ssh_timeout` (`ssh_timeout_ms` in API/form) | connection establishment | 10s |
| `collector_timeout` (`collector_timeout_ms`) | whole collect across all collectors in the chain | 30s |

The collector budget is shared across a poll's retry attempts: once spent, remaining retries fail fast rather than getting a fresh window.

### API Tokens

External systems authenticate with `Authorization: Bearer <token>` instead of a session cookie. Create tokens in **Alert Config → API Tokens**; the plaintext is shown once at creation.

| Scope | Allows |
|-------|--------|
| `read` | GET endpoints (hosts, alerts, metrics, monitor) |
| `write` | + POST/PUT/DELETE on hosts and projects |
| `admin` | + alert rules, notification channels, API token management |

Tokens may be scoped to a project: their requests see only that project's hosts and alerts, regardless of any `project_id` parameters they send. Requests are rate-limited to 60/min per token (`429` with `Retry-After`); `last_used_at` updates on use (throttled to once per minute to limit write load).

```bash
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/alerts
```

Tokens created before v0.5 (weak XOR hashing) no longer authenticate; recreate them in Alert Config.

### Monitoring localhost

Set `connection: local` on a host to collect metrics from the machine running the monitor itself — no SSH keys required. The collector reads `/proc` directly (`loadavg`, `meminfo`, `stat`, `diskstats`, `net/*`, `uptime`) and shells out to `df` for filesystem sizes.

Inside a container `/proc` belongs to the container. To monitor the host instead, mount the host proc read-only, share the PID namespace, and point `HOST_PROC` at it:

```yaml
services:
  monitor:
    pid: host
    volumes:
      - /proc:/host/proc:ro
    environment:
      - HOST_PROC=/host/proc
```

Note that `df` still reports the container's filesystem view; disk sizes may differ from the host unless relevant paths are also bind-mounted.

Leave `collector_preference` empty on `connection: local` hosts — forcing a remote collector (`psutil`, `procfs`, `tailscale`) would attempt SSH with no endpoint and fail every poll.

### Collected Metrics

Metrics are named `<namespace>.<metric>` and auto-discovered by the host detail API — new namespaces appear without dashboard changes.

| Namespace | Metrics |
|-----------|---------|
| `cpu.*` | `user_pct`, `system_pct`, `idle_pct`, `iowait_pct`, `load_1m/5m/15m`, `core.N.user_pct`, `core.N.system_pct`, `core.N.idle_pct`, `core.N.iowait_pct` |
| `mem.*` | `total_bytes`, `used_bytes`, `free_bytes`, `available_bytes`, `cached_bytes`, `swap_total_bytes`, `swap_free_bytes`, `swap_used_bytes`, derived `swap_used_pct` |
| `disk.<mount>.*` | `total_bytes`, `used_bytes`, `free_bytes`, `inodes_used_pct` (mount selector in panel) |
| `diskio.<dev>.*` | `read_bytes`, `write_bytes`, `read_ops`, `write_ops` (displayed as per-second rates) |
| `net.<iface>.*` | `rx_bytes`, `tx_bytes`, `rx_packets`, `tx_packets`, `errors` (displayed as per-second rates) |
| `net.tcp.*` | `ESTABLISHED`, `SYN_SENT`, `SYN_RECV`, `FIN_WAIT1`, `FIN_WAIT2`, `TIME_WAIT`, `CLOSE_WAIT`, `LAST_ACK`, `LISTEN`, `CLOSING`, `total` |
| `net.udp.*` | `CLOSE`, `total` |
| `system.*` | `process_count` |
| `uptime.seconds` | Seconds since boot (alerts support `below: true`) |

### Theming

The dashboard defaults to a dark Grafana-style theme with a light option. The toggle (top-right) is persisted in `localStorage` and carries across the login/auth boundary. Charts re-read their colors from the active theme on toggle.

## Development

```bash
# Build binary
make build

# Run tests
make test

# Format code
make fmt

# Vet
make vet

# Lint (requires golangci-lint)
make lint

# Run locally (requires config)
make run

# Build Docker image
make docker-build

# Run CI pipeline locally
make ci
```

## Architecture Deepening

### SSHClient Adapter (`internal/ssh`)

Extracted SSH transport logic from collectors into a dedicated adapter:

- **Interface**: `SSHClient.Exec(ctx, target, cmd) (string, error)`
- **Implementation**: `sshClient` with configurable `SSHTarget`, `SSHTargetDefaults`
- **Mapping**: `SSHTargetFromHost` free function converts `collector.Host` → `SSHTarget`
- **Defaults**: `SSHTargetDefaults` struct for `StrictHostKeyChecking`, `UserKnownHostsFile`, `ConnectTimeout`, `DefaultPort`, `DefaultTimeout`
- **Collectors**: `PsutilCollector`, `ProcfsCollector`, `TailscaleCollector` use functional options (`WithPsutilSSHClient`, `WithProcfsSSHClient`, `WithTailscaleSSHClient`)
- **Injection**: Single `SSHClient` created in `main.go`, injected into all collectors

### Storage Domain Stores (`internal/storage`)

Split monolithic `storage.go` (564 lines) into 10 focused files with explicit interfaces:

| Module | File | Interface | Responsibility |
|--------|------|-----------|----------------|
| HostStore | `hoststore.go` | `HostStore` | `GetHosts()`, `SeedHosts()` |
| SampleStore | `samplestore.go` | `SampleStore` | `SaveSamples()`, `GetSamples()` |
| AlertStore | `alertstore.go` | `AlertStore` | `InsertAlert()`, `GetActiveAlert()`, `UpdateAlert()`, `AcknowledgeAlert()`, `SilenceAlert()`, `GetAlerts()` |
| ProjectStore | `projectstore.go` | `ProjectStore` | `GetProjects()`, `GetProjectHosts()`, `GetProjectHealth()` |
| Downsampler | `downsampler.go` | `Downsampler` | `Downsample()`, `downsampleRawTo1m()`, `downsample1mTo1h()` |
| Cleanup | `cleanup.go` | `Cleanup` | `Cleanup()` |
| Migrator | `migrator.go` | `Migrator` | `Migrate()` (centralized schema) |

**Shared utilities** (`util.go`): `parseTags`, `matchesTagQuery`, `resolutionMap`, `HostStatusInfo`

**Interfaces** (`interfaces.go`): Explicit interfaces for each store enable testing with fakes and decouple callers.

### Scheduler (`internal/scheduler`)

- Separate tickers for polling (30s), downsampling (1m), cleanup (24h)
- `Poller` logic separated from scheduling concerns
- Host status tracking with consecutive failure counting

### Dashboard (`internal/dashboard`)

- Chart.js rendered client-side from JSON fetched from `/api/host/:id/metrics` and `/api/host/:id/metric/:metric`
- Design system: Nord-inspired dark/light themes, persisted toggle, themed charts (see ADR-0006)
- Interface picker on host pages defaults to `eth0` and hides virtual interfaces (veth/br/docker)
- Network byte counters shown as per-second rates (converted server-side)
- Session-based auth with configurable `Secure` cookie flag
- Templates and static assets (CSS, JS, vendored Chart.js) embedded via `go:embed`; no CDN dependencies

## Retention Policy

| Resolution | Retention | Use Case |
|------------|-----------|----------|
| Raw (30s) | 7 days | Debugging, incident investigation |
| 1-minute | 90 days | Operational dashboards |
| 1-hour | Forever | Capacity planning, trend analysis |

## Alerting

- **Collection failure**: 3 consecutive failed polls → host DOWN
- **Metric thresholds**: Configurable warning/critical per metric (supports wildcards like `disk.*.used_pct`). Thresholds are upper-bound by default; add `below: true` to alert when a metric drops *under* a value (e.g. `uptime.seconds`)
- **Notifications**: Stdout + optional webhooks (Slack, Discord, PagerDuty)
- **Acknowledgment**: Dashboard button to acknowledge/silence alerts

## Testing

```bash
# Run all tests
make test

# Run with race detector
make test-race

# Run specific package tests
go test -v ./internal/storage/...
go test -v ./internal/collector/...
go test -v ./internal/scheduler/...
```

## License

MIT License — see [LICENSE](LICENSE) for details.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for version history.