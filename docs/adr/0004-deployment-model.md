# ADR 0004: Deployment & Operations Model

## Status
Accepted

## Context
The monitor runs as a single Docker container. Needs to be simple to deploy, operate, and observe.

## Decision
**Container spec**:
- Port 8080 (HTTP dashboard)
- Volumes: `/config` (hosts.yaml + SSH keys), `/data` (SQLite)
- Env: `DASHBOARD_PASSWORD`, `POLL_INTERVAL=30s`, `LOG_LEVEL=info`
- Network: bridge by default; host network for external Tailscale access

**Tailscale**: External by default (monitor uses host's Tailscale interface via host network mode). In-container `tailscaled` deferred to v2.

**Config reload**: Docker restart (simplest). File watcher as future enhancement.

**Alerting**: Collection failure threshold (3 consecutive = down). Metric thresholds (CPU/disk/mem) with warning/critical. Notification via stdout + webhook endpoints (Slack/Discord/PagerDuty) configurable. Acknowledgment in dashboard.

**Monitor observability**: `/healthz` for orchestration. Dashboard "monitor" page shows collector status, poll latency, error rates, DB size. Prometheus `/metrics` deferred.

## Consequences
- **Positive**: Single container, minimal moving parts, standard Docker ops
- **Negative**: Config change = brief downtime; no hot-reload; Tailscale requires host network
- **Mitigation**: Document restart workflow; add SIGHUP reload later if pain point

## Alternatives Considered
- **Multiple containers (monitor + Tailscale sidecar)** — more complex, better isolation
- **Kubernetes operator** — overkill for single-binary monitor
- **Systemd service outside Docker** — loses container portability