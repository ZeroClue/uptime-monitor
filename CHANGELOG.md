# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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

### Changed
- **Go version pinned to 1.22** in CI, Dockerfile, and `go.mod` (was 1.25.0)
- **CI simplified** — removed golangci-lint (incompatible with Go 1.25 runner), rely on `go vet` + `go fmt` + tests
- **Dockerfile updated** to use `golang:1.22-alpine` builder

### Fixed
- **Test coverage** — removed `-race -coverprofile` flags (Go 1.25 `covdata` format incompatible with runner)
- **golangci-lint removed** — binary built with Go 1.23 incompatible with GitHub Actions Go 1.25 runner
- **Docker build** — aligned Go version to 1.22 in Dockerfile

### Architecture
- **Collector chain** — PsutilCollector → ProcfsCollector → TailscaleCollector fallback (ADR-0001)
- **Storage** — 6 focused domain stores with explicit interfaces (HostStore, SampleStore, AlertStore, ProjectStore, Downsampler, Cleanup)
- **Scheduler** — background downsampling (minute) and cleanup (daily) tickers
- **Dashboard** — HTMX metric panel fragments at `/api/host/:id/metric/:metric`

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