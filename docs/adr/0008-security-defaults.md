# ADR 0008: Security Defaults

## Status
Accepted

## Context
Phase 3 introduced external authentication (API tokens) and tightened transport security. Three defaults were chosen deliberately, each a breaking or behavior-changing decision for existing deployments:

1. SSH execution originally hardcoded `StrictHostKeyChecking=no` with `UserKnownHostsFile=/dev/null` — every host key was silently trusted, so man-in-the-middle detection was impossible.
2. API tokens were stored under a trivial XOR scheme (colliding for tokens longer than 32 bytes), which could authenticate the wrong token.
3. Config pages interpolated admin-supplied strings into `innerHTML` unescaped.

## Decision
- **Host keys default to `strict`** (`StrictHostKeyChecking=yes`) against a managed file (`/config/known_hosts`, configurable via `ssh_known_hosts_file`). Operators seed it with `ssh-keyscan`; a per-host `auto` policy uses OpenSSH `accept-new` for zero-touch first connections while still failing on key changes. Existing rows inherit the strict global default (ticket #67 called for exactly this).
- **API token hashes are SHA-256**; pre-upgrade XOR-hashed tokens are rejected and must be recreated (a lazy-migration path was considered and rejected: XOR collides across distinct >32-byte tokens, so verifying against it could authenticate the wrong credential).
- **All user-derived strings are HTML-escaped before `innerHTML` interpolation** in config tables via a shared `escapeHtml`.

## Consequences
- Upgrading deployments with previously-working SSH hosts will see them fail until `/config/known_hosts` is seeded (README documents the keyscan step). This is intentional: silent trust was the bug.
- Pre-0.5 API tokens stop authenticating; recreate them in Alert Config.
- `known` policy is currently an alias of `strict`; it exists as a named slot for future divergence (e.g., read-only file handling).
