# ADR 0003: Authentication Model

## Status
Accepted

## Context
The dashboard and host connections both need authentication. Requirements: simple, viable for small teams, no external identity providers.

## Decision
**Dashboard**: Single shared password via `DASHBOARD_PASSWORD` env var. Session cookie (HttpOnly, Secure) set on login, 24h expiry.

**Host connections**: Per-host SSH key path in `hosts.yaml`. Keys stored in config directory mounted into container. Optional `sudo: true` per host for metrics requiring root.

## Consequences
- **Positive**: Zero external deps; easy to rotate; works with reverse proxies; keys never in config file
- **Negative**: Shared password = no individual accountability; SSH key management manual
- **Mitigation**: Document key rotation; consider per-user tokens in v2

## Alternatives Considered
- **HTTP Basic Auth** — credentials sent every request; less UX-friendly
- **OAuth/OIDC** — overkill for MVP
- **No auth (VPN only)** — defense in depth preferred