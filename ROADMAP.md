# Uptime Monitor - ROADMAP

## ✅ Phase 1: Core Collection & Display (COMPLETED)

### Infrastructure
- [x] Tailscale SSH integration with keyless auth
- [x] Docker-based deployment with persisted state
- [x] SQLite storage with WAL mode
- [x] Downsampling (1m, 1h) and retention policies

### Collectors
- [x] **procfs collector** - reads /proc/* for CPU, memory, disk, uptime
- [x] **Network metrics** from /proc/net/dev (per-interface rx/tx bytes, packets, errors)
- [x] **Memory used** calculation (total - available)
- [x] **Disk metrics** via df with SSH warning handling
- [x] **CPU percentages** from /proc/stat with guest/guest_nice support
- [x] **Fallback chain** - psutil → procfs → tailscale
- [x] **psutil collector** (with time import fix)

### Dashboard
- [x] Host list with status
- [x] Host detail view with charts
- [x] CPU chart (user, system, idle, iowait, load1/5/15)
- [x] Memory chart (used, free, available, cached, total)
- [x] Disk chart (used, free, total)
- [x] Network charts - **dynamic per-interface** (auto-discovered)
- [x] Time range selector (1h, 6h, 24h, 7d, 30d)
- [x] Resolution selector (raw, 1m, 1h)
- [x] Dynamic metric discovery API
- [x] Chart.js client-side rendering (htmx removed in Phase 2 redesign, see ADR-0005/0006)

### Authentication
- [x] Single-password login
- [x] Session cookies (24h, HttpOnly, Secure)

---

## 📋 Phase 2: Core Improvements (HIGH PRIORITY) — COMPLETE

### Metrics Collection
- [x] **Swap usage** - SwapTotal/SwapFree from /proc/meminfo
- [x] **Per-core CPU** - individual cpuN lines from /proc/stat
- [x] **Disk I/O stats** - reads/writes, latency from /proc/diskstats or iostat
- [x] **Process count** - from /proc/loadavg or /proc/stat
- [x] **TCP/UDP connection states** - /proc/net/tcp*, /proc/net/udp*
- [x] **Filesystem details** - multiple mount points, inodes

### Alerting System (DB schema exists, needs UI)
- [x] **Alert silencing/acknowledgment** — UI for existing DB schema (acknowledge + silence wired in v0.3.0)
- [x] **Alert configuration UI** - create/edit thresholds per host/project
- [x] **Threshold types** - CPU%, memory%, disk%, load, network
- [x] **Notification channels** - Email, webhook, Slack, PagerDuty
- [x] **Alert history page** - view past alerts, silence/unmute

### Dashboard Improvements
- [x] **Dark mode** - CSS variables toggle (v0.3.0, persisted + themed charts)
- [x] **Project-level views** - aggregate health across hosts (v0.3.0)
- [x] **Compare hosts** - side-by-side charts (v0.3.0) + per-host last-value table
- [x] **Real-time updates** - interval-based auto-refresh (30s/60s, paused when hidden)
- [x] **Alert configuration UI** - create/edit thresholds per host/project
- [x] **Host tags filtering** on main list

---

## ✅ Phase 3: Architecture & Usability (MEDIUM PRIORITY) — COMPLETE

> ✅ **Complete** — shipped across PRs #89–#102; epic #55 closed.

### Configuration Management
- [x] **Move hosts.yaml to DB** - CRUD UI for host management
- [x] **Move thresholds.yaml to DB** - CRUD UI for alert rules
- [x] **Projects as isolation boundaries** - multi-tenancy
- [x] **API tokens** - for external integrations

### Collector Enhancements
- [x] **Local collector mode** - monitor localhost without SSH
- [x] **SNMP collector** - for network devices
- [x] **Prometheus remote write** - export metrics
- [x] **Custom script collector** - user-defined commands

### Reliability
- [x] **Health check improvements** - per-host connectivity status
- [x] **Collector timeout config** - per-host
- [x] **Retry logic** - exponential backoff for failed polls
- [x] **SSH known_hosts management** - auto-accept or config

---

## 📋 Phase 4: Advanced Features (LOWER PRIORITY)

### Visualization
- [ ] **Dashboard templates** - save/share custom layouts
- [ ] **Annotations** - mark deployments, incidents on charts
- [ ] **Heatmaps** - host vs metric grids
- [ ] **Top-N tables** - e.g., top 10 CPU consumers

### Integrations
- [ ] **Grafana datasource plugin** - query uptime-monitor as source
- [x] **Prometheus exporter** - /metrics endpoint (shipped with remote write, #101)
- [ ] **Log aggregation** - collect system logs via SSH
- [ ] **Kubernetes integration** - service discovery

### Operations
- [ ] **Backup/restore** - SQLite dump/load
- [ ] **Migration tool** - config version upgrades
- [ ] **Multi-instance HA** - leader election for polling

---

## 🐛 Known Issues / Tech Debt

1. **Disk metrics for container** - df returns 0 in container (expected)
2. **Network interface churn** - veth interfaces appear/disappear (Docker)
3. **Timezone handling** - timestamps stored as Unix, displayed in local time
4. **API auth granularity** - dashboard cookie session + scoped API tokens exist; fine-grained RBAC remains future work

---

## 📝 Notes

- **Target**: Reliable monitoring of 4 VPS hosts over Tailscale SSH
- **Current poll interval**: 30s (configurable via POLL_INTERVAL)
- **Retention**: 7d raw, 90d 1m, ∞ 1h
- **Storage**: ~5MB per host per day at 30s interval