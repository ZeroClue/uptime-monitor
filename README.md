# Uptime & System Status Monitor

A self-hosted, single-binary monitoring solution that polls Linux hosts over SSH or Tailscale, collects system metrics, stores them in embedded SQLite, and serves a clean htmx + Chart.js dashboard.

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
    timeout: 10s
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
export UPTIME_MONITOR_VERSION=0.2.0

# Deploy
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

The production compose (`docker-compose.prod.yml`) pulls from GHCR:
- `ghcr.io/zeroclue/uptime-monitor:0.2.0`
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
UPTIME_MONITOR_VERSION=0.2.0
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

### Manual Docker Run

```bash
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

### Manual Docker Run

```bash
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

## Dashboard

| Route | Description |
|-------|-------------|
| `/` | Host list with sparklines and status |
| `/host/:id` | Per-host detail with CPU, memory, disk, network graphs |
| `/compare` | Multi-host metric comparison |
| `/projects` | Project health overview (tag-based + explicit) |
| `/alerts` | Alert panel with acknowledge/silence |
| `/monitor` | Self-monitoring (collector status, latency, errors) |
| `/healthz` | Liveness/readiness endpoint |
| `/metrics` | Prometheus-format metrics (experimental) |

### API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /api/hosts` | List all hosts (JSON) |
| `GET /api/host/:id/metric/:metric` | HTMX fragment for single metric chart |
| `GET /api/host/:id/metrics` | All metrics for a host (JSON) |
| `GET /api/compare` | Multi-host comparison data |
| `GET /api/projects` | Project list with health status |

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

- HTMX partial fragments for metric panels at `/api/host/:id/metric/:metric`
- Chart.js rendered in server-rendered HTML fragments
- Session-based auth with configurable `Secure` cookie flag

## Retention Policy

| Resolution | Retention | Use Case |
|------------|-----------|----------|
| Raw (30s) | 7 days | Debugging, incident investigation |
| 1-minute | 90 days | Operational dashboards |
| 1-hour | Forever | Capacity planning, trend analysis |

## Alerting

- **Collection failure**: 3 consecutive failed polls → host DOWN
- **Metric thresholds**: Configurable warning/critical per metric (supports wildcards like `disk.*.used_pct`)
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