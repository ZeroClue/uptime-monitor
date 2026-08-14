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

## Architecture Summary

```
��─────────────────��     SSH/Tailscale      ��──────────────��
│   Monitor       │ ─────────────────────�� │   Hosts      │
│  (Go binary)    │ ��───────────────────── │  (Linux)     │
��────────��────────��   Metrics (JSON/text)  └──────────────��
         │
         ��
��─────────────────��
│   SQLite        │  ← Raw samples (7d), 1m aggregates (90d), 1h aggregates (forever)
│  (embedded)     │
��────────��────────��
         │
         ��
��─────────────────��
│   Dashboard     │  ← htmx + Chart.js, single-password auth
│  (Go templates) │
��─────────────────��
```

**Key Design Decisions:**
- **Pull model** — Monitor initiates connections; no agent required on targets
- **Collector fallback chain** — psutil → `/proc`+`df` → Tailscale (works on any Linux host)
- **Single binary** — ~10MB static Go binary, runs in distroless/scratch container
- **Embedded SQLite** — Zero external dependencies, WAL mode for concurrency
- **Downsampling** — Automatic background aggregation per retention policy

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DASHBOARD_PASSWORD` | *required* | Shared password for dashboard login |
| `POLL_INTERVAL` | `30s` | Collection interval per host |
| `LOG_LEVEL` | `info` | Log level: debug, info, warn, error |

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
```

## Deployment

### Docker Compose (Recommended)

```yaml
# docker-compose.yml
services:
  monitor:
    image: uptime-monitor:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config:/config:ro
      - ./data:/data
    environment:
      - DASHBOARD_PASSWORD=${DASHBOARD_PASSWORD}
      - POLL_INTERVAL=30s
      - LOG_LEVEL=info
    network_mode: host  # Required for Tailscale access
    restart: unless-stopped
```

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

## Development

```bash
# Build binary
make build

# Run tests
make test

# Lint
make lint

# Run locally (requires config)
make run

# Build Docker image
make docker-build
```

## Retention Policy

| Resolution | Retention | Use Case |
|------------|-----------|----------|
| Raw (30s) | 7 days | Debugging, incident investigation |
| 1-minute | 90 days | Operational dashboards |
| 1-hour | Forever | Capacity planning, trend analysis |

## Alerting

- **Collection failure**: 3 consecutive failed polls → host DOWN
- **Metric thresholds**: Configurable warning/critical per metric
- **Notifications**: Stdout + optional webhooks (Slack, Discord, PagerDuty)
- **Acknowledgment**: Dashboard button to silence alerts

## License

MIT License — see [LICENSE](LICENSE) for details.