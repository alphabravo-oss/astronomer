# Astronomer Go — Scale Baseline (Honest Status)

## Status: no validated load-test baseline yet

This document intentionally does **not** claim a certified fleet size or RPS
envelope. The load-test harness exists under `scripts/loadtest/`, but as of the
last review **no pass row has been recorded** against a production-like
management plane. Treat any prior marketing numbers as aspirational until a
harness run lands a `pass` verdict here.

## What exists today

| Artifact | Role |
|---|---|
| `scripts/loadtest/` | HTTP + agent-style workload harness |
| `scripts/loadtest/profiles/estate-{100,500,1000}.yaml` | Production certification rungs |
| `scripts/loadtest/profiles/estate-1000-soak.yaml` | Four-hour production-envelope leak certification |
| `scripts/loadtest/profiles/estate-2000-lab.yaml` | Lab-only four-hour ceiling/soak rung |
| `.github/workflows/scale-certification.yaml` | Approval-gated, self-hosted certification workflow |
| `.github/workflows/scale-certification-aggregate.yaml` | Same-release four-rung verification and aggregation workflow |
| `scripts/reduce-component-sizing.py` | Fail-closed per-component reducer and documentation-table input generator |
| `make load-test` | Entry point (see Makefile) |
| This file | Place to record **measured** results only |

## How to produce a baseline

Run against a real management-plane deployment (not a unit-test fake):

```bash
# from astronomer/ root
make load-test \
  LOADTEST_SERVER=https://your-server \
  LOADTEST_TOKEN=/path/to/admin.jwt \
  LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE=/path/to/read-only-postgres.dsn \
  LOADTEST_CLUSTERS=100 \
  LOADTEST_RPS=200 \
  LOADTEST_DURATION=10m
```

Then:

1. Run with `LOADTEST_CERTIFICATION=true` and provide the reproducibility
   metadata plus passing JSON evidence for every profile drill. Preflight the
   same evidence locally with
   `go run ./scripts/loadtest -validate-drill-evidence`; it uses the selected
   `LOADTEST_PROFILE` and the certification environment variables described
   below, without contacting the management plane. Certification sustains its
   mandatory-audit rate for the full measured window and requires a separate
   read-only PostgreSQL observer to prove each exact `audit_outbox` intent;
   HTTP acceptance alone is never durability evidence.
2. Confirm the Markdown and JSON verdicts are `pass` and verify the generated
   SHA-256 file.
3. Retain the signed evidence manifest and deterministic baseline row, then
   aggregate all four production rungs for the same release.
4. Do **not** invent numbers. If the harness was not run, leave the table empty
   and record a blocked row with the failure reason.

The report records commit, image set, chart-values digest, Kubernetes,
PostgreSQL and Redis versions, hardware, duration, resource cardinality,
HTTP p50/p95/p99, agent reconnects, DB pool pressure, queue depth, and
goroutine/heap/open-FD evidence. The workflow retains the Markdown, machine JSON,
integrity digest, and separate fault/browser drill artifacts for 90 days.

The signed aggregate also contains `component-sizing.json` and
`component-sizing-table.md`. These are evidence-derived update inputs, not
published production guidance: the reducer requires at least three distinct
source runs and two declared-load points for every server, worker, tunnel, and
audit signal. It rejects duplicate runs, mixed releases, heterogeneous replica
topologies, decreasing capacity, and scaling efficiency outside 50–125%. Its capacity basis is a one-sided
95% Student-t lower confidence bound capped at the minimum observed per-replica
rate, not the optimistic maximum.

## Drill evidence provenance contract

Certification accepts one `<drill>.json` file for every `day2FailureDrills`
entry in the selected profile. Each file must use schema
`astronomer-day2-drill-evidence-v2` and retain the exact drill-execution
workflow, run, event, commit, success conclusion, release-manifest digest,
environment, timestamps, digest-bound execution log, and closed
precondition/injection/effect/recovery/cleanup measurements. The protected
execution workflow requires a digest-pinned environment driver for every
declared fault and fails unsupported drivers; generic unit tests cannot be
stamped as production drill evidence. Drivers bind the exact release and estate;
arbitrary successful-workflow artifacts and rewritten run IDs are rejected.
Unknown fields, extra artifact files, symlinks, and malformed timestamps are
rejected.
The files must also be digest-bound by `drill-evidence-manifest.json` using
schema `astronomer-day2-drill-manifest-v1`; that manifest preserves both raw
execution and qualification provenance and must be keyless-signed by the exact
trusted `day2-drill-qualification.yaml` workflow identity.

The qualification run must match `LOADTEST_DRILL_EVIDENCE_RUN_ID`; each drill
retains its distinct raw execution run. Values must match `LOADTEST_COMMIT`, the
selected profile, `LOADTEST_IMAGES` release-manifest digest, and
`LOADTEST_ENVIRONMENT`. `status` must be `pass`; the completion timestamp must
not precede the start, be more than five minutes in the future, or be older
than 30 days. The GitHub workflow requires the selected source run to have the
exact trusted workflow path, exact commit, completed/success conclusion, and a
unique unexpired artifact. It verifies the certificate identity, signed manifest
digest, and every declared file digest before exposing the API token or starting
the scale workload. The Go validator rechecks this provenance fail-closed.

The generated machine report uses schema `astronomer-scale-report-v2` and
records its generation time, current Actions run ID, source drill-evidence run
ID, commit, profile, image set, and environment. The workflow digests the
Markdown, machine report, rendered values, drill evidence, raw qualification
data, and baseline row, then keyless-signs and verifies the manifest against
the exact workflow identity. The aggregate workflow rejects missing,
duplicate, failed, or cross-release production rungs.

## Latest validated baseline

| Date | Build | Clusters | HTTP RPS | p99 (cluster-list) | p99 (resources) | DLQ at end | Verdict |
|---|---|---|---|---|---|---|---|
| _none recorded_ | — | — | — | — | — | — | — |

## Blocked / failed harness attempts

| Date | Build | Environment | Reason | Evidence |
|---|---|---|---|---|
| 2026-07-09 | `2991f9d` + residual tree | host k3s | Management-plane namespace `astronomer` empty (no server pods); leftover agents in `astronomer-system` unhealthy (`ImagePullBackOff`/`Error`). Cannot dial a live LOADTEST_SERVER. | Goal session residual assessment; harness not started against a reachable control plane. |

## Engineering guidance (unvalidated)

These are **starting points for capacity planning**, not SLOs:

| Cluster count | Suggested chart posture |
|---|---|
| up to 50 | base `values.yaml` |
| 50–500 | `values-production.yaml`, raise worker replicas / pool |
| 500–2000 | external HA Postgres + Redis; tune rate limits |
| 2000+ | custom sizing after a harness pass at that scale |

Until a **pass** row is recorded above, operators should assume the product has
not been load-certified for their target fleet size.
