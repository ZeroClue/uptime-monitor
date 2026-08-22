# ADR 0007: Host Configuration Ownership Split

## Status
Accepted

## Context
Hosts can be created two ways: seeded from `hosts.yaml` on startup, or created/edited via the API and host-config UI. The original seeding behavior (`ON CONFLICT(name) DO UPDATE SET <every column>`) meant each restart rewrote the entire row from yaml — including timeouts, retry tuning, host-key policy, and project assignment that operators had set through the UI after deployment. Mixed management (yaml + UI) silently lost edits.

We needed a rule for which side wins per field, per startup.

## Decision
**hosts.yaml owns connectivity; the database owns operations.**

- yaml re-syncs every startup: `connection`, `endpoint`, `port`, `user`, `key_path`, `sudo`, `proxy_jump`, `tags`
- DB-owned after first seed: `timeout`, retry fields, `ssh_timeout_ms`/`collector_timeout_ms`, `ssh_host_key_policy`, `collector_preference`, project assignment

Rationale: yaml is the declared-infrastructure source of truth — changing a host's address in yaml must take effect. Operational tuning is operator behavior; it must survive restarts. New hosts still receive full yaml seeding so a yaml-only workflow works unchanged.

## Consequences
- Changing an operational value in yaml for an *existing* host has no effect (documented in README); delete the row or change it via API/UI.
- The upsert statement's `DO UPDATE SET` column list is now deliberately narrower than the insert list — keep them in sync consciously when adding columns.
- Future columns must be classified as connectivity or operations at design time.
