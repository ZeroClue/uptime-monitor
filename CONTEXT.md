# Domain Model: Uptime & System Status Monitor

## Core Concepts

**Monitor** — The central Docker container that polls hosts, stores metrics, and serves the dashboard.

**Host** — A target machine (server, VM, device) identified by name, connection endpoint, and auth credentials. Has tags for grouping.

**Metric** — A named, typed measurement (e.g., `cpu.user_pct`, `mem.used_bytes`, `disk.root.used_pct`) with timestamp and host_id.

**Sample** — A single metric reading at a point in time. Raw samples retained for 7 days.

**Aggregate** — Downsampled metric (1-min, 1-hour) for long-term retention and fast dashboard queries.

**Collector** — A strategy for fetching metrics from a host. Multiple collectors tried in order until one succeeds. The **custom** collector runs a user-defined command over SSH and parses stdout (JSON array of `{metric, value, timestamp?}`, CSV `metric,value[,unix_ts]`, or plain number) into the `custom.<script_name>.*` namespace; selected via `collector_preference: custom`.

**Connection** — How the Monitor reaches a Host: SSH (with key), Tailscale IP, Local (procfs of the machine running the Monitor; `HOST_PROC` override for container deployments), SNMP v2c/v3 (network devices; credentials are yaml-owned like user/key), or VPN endpoint.

**Dashboard** — Web UI served by the Monitor showing host list, per-host drill-down, and metric graphs.

**Project** — A named group of hosts (explicit list or tag query) with rolled-up health status for the overview dashboard.

## Host Config Schema (`hosts.yaml`)

```yaml
retry:                     # global poll-retry policy
  max_retries: 3           # attempts per poll (1 = no retry)
  base_delay: 2s           # backoff doubles per attempt, capped by max_delay
  max_delay: 30s
  jitter: 0.2              # random fraction added to each delay

hosts:
  - name: web-01
    connection: local        # ssh | tailscale | local
    endpoint: 10.0.0.5       # IP or hostname
    port: 22                 # default 22
    user: monitor            # SSH user
    key_path: /keys/web-01   # relative to config dir, required
    sudo: false              # whether to sudo for df /proc
    timeout: 10s             # connection + command timeout
    proxy_jump: ""           # optional SSH proxy jump host
    tags: [web, prod]        # for grouping / project queries
    collector_preference: "" # optional: force specific collector (psutil|procfs|tailscale|custom)
    retry_max_retries: 5     # optional per-host retry overrides; unset = global
    retry_base_delay: 500ms
    retry_max_delay: 15s

# Custom script config is NOT in hosts.yaml: script_name, script_command and
# script_parse are DB-owned per-host fields edited via the dashboard/API
# (with a Test Run endpoint), like other operations data.
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
| Collector fallback order | 1) Local procfs (connection=local only) 2) SSH+psutil 3) SSH+/proc+df 4) SNMP v2c/v3 (connection=snmp only; gosnmp; IF-MIB + HOST-RESOURCES-MIB + UCD-SNMP-MIB → snmp.* namespaces) 5) Tailscale+same 6) Custom script (preference=custom only) 7) node_exporter (later) | Progressive enhancement; works on any Linux host or network device |
| SNMP credentials are connectivity data | snmp_version/community/v3 params live on hosts and are yaml-owned (re-synced like user/key_path per ADR-0007); extra OIDs poll as snmp.custom.<metric> gauges | Devices' SNMP creds deploy with the device config, not operator tuning |
| Failed polls retry with exponential backoff; auth/host-key errors never retry | Transient faults shouldn't flap hosts down; permanent failures shouldn't burn attempts | Retrying auth would lock accounts; backoff bounds thundering-herd on recovery |
| Alert rules/channels with a project apply only within it; project-less ones are global | Isolation without forcing everyone to assign projects | Global rules remain useful for single-project installs |
| hosts.yaml owns host **connectivity** (connection type, endpoint, port, user, key, sudo, proxy, tags); the DB owns host **operations** (timeouts, retries, key policy, collector preference, scripts) after first seed | yaml is the deployment source of truth; operators tune behavior via UI/API without losing edits on restart | Changing connectivity in code but not yaml would drift from declared infrastructure |
| Custom script collector | User-defined command over SSH; stdout parsed as JSON `{metric,value,timestamp}` / CSV / plain number into `custom.<script_name>.*`; 1 MiB output + 1000-sample caps; per-host Timeout bounds each run; scripts are DB-owned (never in yaml); allowlist/sandbox deferred to v2 | Monitors anything built-ins miss with zero code; caps bound runaway output |
| Core metric schema | cpu, mem, disk, net, uptime namespaces (see CONTEXT.md) | Covers 90% of infra monitoring needs |
| GPU / per-process / containers | Deferred to v2 | Out of scope for MVP |
| Real-time updates | Poll on load + manual refresh | No WebSocket complexity |
| Dashboard views | Host list (table/tiles), host detail, multi-host compare, alert panel, project health overview | Covers operational workflows |
| Dashboard framework | Go + Chart.js (embedded via go:embed) | Single binary, no build step, lightweight; htmx removed |
| Dashboard theme | Dark default + light toggle, persisted in localStorage | Grafana-style ops look; charts restyle on toggle (ADR-0006) |
| Dashboard auth | Single shared password (env var) | Simplest viable auth |
| Host auth | SSH key per host (configurable per host) | Standard, flexible |
| Project model | Tag-based + explicit projects | Ad-hoc queries + structured dashboards |
| Tailscale mode | External (host network) by default | In-container option for v2 |
| Config reload | Docker restart (simplest) | File watcher as enhancement |
| Monitor observability | /healthz + dashboard monitor page + /metrics (remote write health: sent/failed/dropped counters, queue depth) | Self-metrics beyond remote-write health deferred |
| Remote write export | Single global target (Alert Configuration → Remote Write); snappy-compressed protobuf v1; labels `__name__`, host, project, collector, job + extras; bounded queue drops oldest; failed batches drop after retries (never requeued) | One destination covers Prometheus/Mimir/Grafana Cloud; drop-oldest bounds memory; requeueing a poison batch would wedge the pipeline |
| Chart rendering | Fetch JSON from `/api/host/:id/metrics`, render client-side with Chart.js; live values via `/api/hosts/status`, `/api/monitor` | htmx removed; single fetch on DOMContentLoaded + interval refresh |
| Net metrics display | Cumulative counters converted to per-second rates client-side (host) and server-side (compare) | Raw counters produce meaningless rising lines |
| Interface picker | Defaults to eth0; veth/br/docker sorted last | Avoids chart spam on virtual interfaces |
| Alert thresholds | Upper-bound by default; `below: true` opts into lower-bound (uptime.seconds) | uptime must alert when *under* a threshold |
| SSH command output | `LogLevel=ERROR` suppresses host-key warning; parsers scan for first numeric token | "Permanently added" stderr line was parsed as field[0], zeroing uptime/load |