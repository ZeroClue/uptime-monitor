# Quickstart: Uptime Monitor in 5 Minutes

Get the uptime monitor running locally or on a server in under 5 minutes.

## Prerequisites

- Docker & Docker Compose installed
- Target Linux hosts accessible via SSH (or Tailscale)
- SSH key pair for the monitoring user (optional if using Tailscale SSH)

---

## 1. Generate SSH Keys (Optional with Tailscale SSH)

```bash
# On your local machine (or the monitor host)
mkdir -p config/keys
ssh-keygen -t ed25519 -f config/keys/id_ed25519 -N ""

# Copy public key to each target host (skip if using Tailscale SSH)
ssh-copy-id -i config/keys/id_ed25519.pub user@target-host
```

> **Tip:** Create a dedicated `monitor` user on target hosts with passwordless sudo for `df` and `/proc` access if needed.
>
> **Skip SSH keys entirely** — use [Tailscale SSH](#tailscale-ssh-keyless-authentication) for keyless authentication via Tailscale ACLs.

---

## 1.5. Tailscale SSH (Keyless Authentication) — Skip SSH Keys!

If your hosts are on Tailscale, you can skip SSH keys entirely by using Tailscale's ACL-based SSH authentication.

1. **Generate auth key** in Tailscale admin console (Ephemeral + Pre-authorized + Tag: `tag:monitor`)
2. **Create ACL policy** allowing `tag:monitor` → `tag:server` on port 22
3. **Add to `.env`**:
   ```bash
   TS_AUTHKEY=tskey-auth-xxxxxxxxxxxx
   TS_HOSTNAME=uptime-monitor
   ```
4. **Tag target hosts** with `tag:server` in Tailscale admin console
5. **Configure hosts** with `connection: tailscale` and **no `key_path`** — Tailscale SSH handles auth automatically!

See [README.md#tailscale-ssh-keyless-authentication](README.md#tailscale-ssh-keyless-authentication) for full setup.

---

## 2. Configure Hosts

```bash
cp config/hosts.yaml.example config/hosts.yaml
```

Edit `config/hosts.yaml`:

```yaml
hosts:
  - name: web-server-1
    connection: ssh
    endpoint: 192.168.1.10
    user: monitor
    key_path: /keys/id_ed25519
    tags: [web, production]
    sudo: true          # needed for 'df' on some systems
    timeout: 10s
    proxy_jump: ""      # optional: bastion host

  - name: db-server
    connection: tailscale
    endpoint: 100.64.1.5   # Tailscale IP
    user: monitor
    # key_path: NOT NEEDED with Tailscale SSH!
    tags: [db, production]
```

> **Note:** With Tailscale SSH, omit `key_path` entirely — authentication happens via Tailscale's ACL-based certificates.

---

## 3. Configure Alert Thresholds (Optional)

```bash
cp config/thresholds.yaml.example config/thresholds.yaml
```

Edit `config/thresholds.yaml`:

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
  - name: pagerduty
    type: pagerduty
    url: https://events.pagerduty.com/v2/enqueue
    secret: your-routing-key
```

---

## 4. Launch

```bash
# Set dashboard password
export DASHBOARD_PASSWORD=your-secure-password

# Start
docker-compose up -d
```

Open http://localhost:8080 and log in with the password.

---

## 5. Verify

- **Dashboard**: http://localhost:8080 — host list with sparklines
- **Host detail**: Click any host → CPU, memory, disk, network graphs
- **Compare**: `/compare` — overlay metrics across hosts
- **Projects**: `/projects` — tag-based or explicit groups with health rollup
- **Alerts**: `/alerts` — acknowledge/silence
- **Self-monitor**: `/monitor` — collector status, latency, DB size

---

## Pull the Image (Optional)

```bash
# The image is on GHCR (private by default)
docker pull ghcr.io/zeroclue/uptime-monitor:0.3.0
# or
docker pull ghcr.io/zeroclue/uptime-monitor:latest

# To make it public: GitHub → Settings → Packages → uptime-monitor → Package settings → Change visibility
```

---

## Architecture at a Glance

```
┌──────────────┐     SSH/Tailscale      ┌─────────────┐
│   Monitor    │ ◄─────────────────────► │   Targets   │
│  (Go binary) │    Metrics (JSON/text)  │  (Linux)    │
└──────┬───────┘                         └─────────────┘
       │
       ▼
┌─────────────────┐
│   SQLite (WAL)  │
│  samples_raw    │  ← 7 days raw
│  samples_1m     │  ← 90 days 1-min aggregates
│  samples_1h     │  ← forever 1-hour aggregates
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│    Dashboard    │  ← Chart.js
│  (Go templates) │    Single-password auth
└─────────────────┘
```

---

## Next Steps

- Add more hosts to `config/hosts.yaml` and restart: `docker-compose restart`
- Configure webhooks in `thresholds.yaml` for Slack/Discord/PagerDuty
- Create projects in `config/projects.yaml` (or via dashboard) for grouped health views
- Set up a reverse proxy (nginx/Traefik) with HTTPS and set `COOKIE_SECURE=true`

---

## Troubleshooting

| Issue | Fix |
|-------|-----|
| "Permission denied" pulling image | Run `docker login ghcr.io -u USERNAME -p TOKEN` or make package public |
| Host shows DOWN | Check SSH key, user exists, `sudo` for `df`, firewall allows port 22 |
| No metrics | Check `sudo` for `df`, target has `psutil` or `/proc` readable |
| Dashboard not loading | Check `DASHBOARD_PASSWORD` set, port 8080 not in use |
| Tailscale auth fails | Check `TS_AUTHKEY` is valid, host has `tailscaled` running, ACL policy allows `tag:monitor` → `tag:server` |

---

## Next Steps

- Read full docs: [README.md](README.md)
- See version history: [CHANGELOG.md](CHANGELOG.md)
- Architecture deep-dive: [README.md#architecture-deepening](README.md#architecture-deepening)