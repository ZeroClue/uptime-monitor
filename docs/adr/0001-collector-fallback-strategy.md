# ADR 0001: Collector Fallback Strategy

## Status
Accepted

## Context
The monitor needs to collect metrics from heterogeneous Linux hosts. Some hosts have Python + psutil, some only have basic `/proc` and `df`, some are reachable via Tailscale, and some may expose SNMP or Prometheus node_exporter.

We need a strategy that works everywhere without requiring host preparation.

## Decision
Try collectors in this order until one succeeds:
1. **SSH + psutil** — structured JSON, rich metrics, requires Python + psutil on target
2. **SSH + `/proc` + `df`** — always available on Linux, fragile parsing, fewer metrics
3. **Tailscale + same as above** — for hosts only reachable via tailnet
4. **SNMP / node_exporter** — deferred to v2

Each collector implements the same interface returning normalized metric samples.

## Consequences
- **Positive**: Works on any Linux host with zero prep; graceful degradation
- **Negative**: More code to maintain; parsing `/proc` is brittle across kernel versions
- **Mitigation**: Unit tests against real `/proc` fixtures; collector health logging

## Alternatives Considered
- **Single collector (psutil only)** — simpler but requires host prep
- **Push agents** — more complex deployment model
- **Only SNMP** — not universally available