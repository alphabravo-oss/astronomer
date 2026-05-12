# Astronomer Operator Runbooks

This directory is the on-call reference for the Astronomer management plane.
Each runbook follows the same four-section skeleton so you can land in any
one and find the same shape:

1. **Symptoms** — what the alert / user / metric is actually showing.
2. **Triage** — what to check first, in order of cost.
3. **Recovery** — immediate stabilization and permanent fix.
4. **Verify** — the signals that prove the incident is resolved.

Anything reachable from a `runbook_url` annotation on a PrometheusRule
(see `deploy/chart/templates/prometheus-rules.yaml`) is listed below.

## Alert-linked runbooks

| Alert | Runbook |
|---|---|
| `AstronomerHighHTTPErrorRate` | [high-http-error-rate.md](high-http-error-rate.md) |
| `AstronomerWorkerQueueBacklog` | [worker-queue-backlog.md](worker-queue-backlog.md) |
| `AstronomerWorkerDLQGrowing` | [worker-dlq-growing.md](worker-dlq-growing.md) |
| `AstronomerDBPoolExhausted` | [db-pool-exhausted.md](db-pool-exhausted.md) |
| `AstronomerAgentDisconnected` | [agent-disconnected.md](agent-disconnected.md) |
| `AstronomerArgoSelfManageDrift` | [argocd-drift.md](argocd-drift.md) |

## Operational scenarios (no automated alert)

| Scenario | Runbook |
|---|---|
| Failed schema migration | [failed-migration.md](failed-migration.md) |
| cert-manager renewal stuck | [cert-manager-stuck.md](cert-manager-stuck.md) |
| Agent fleet de-pair / mass-disconnect | [agent-fleet-depair.md](agent-fleet-depair.md) |
| Redis data loss | [redis-data-loss.md](redis-data-loss.md) |
| Postgres failover (external DB) | [postgres-failover.md](postgres-failover.md) |
| OIDC / SSO outage | [oidc-outage.md](oidc-outage.md) |
| License expiry | [license-expiry.md](license-expiry.md) |
| Postgres PVC near full | [postgres-disk-full.md](postgres-disk-full.md) |

## Cross-references

- DR plan: [`../management-plane-dr-runbook.md`](../management-plane-dr-runbook.md)
- Upgrade: [`../upgrade-runbook.md`](../upgrade-runbook.md)
- Secret rotation: [`../secret-rotation-runbook.md`](../secret-rotation-runbook.md)
- On-call onboarding: [`../oncall-onboarding.md`](../oncall-onboarding.md)
- Image verification (signing): [`../verify-images.md`](../verify-images.md)

## Stub maturity

Runbooks linked from PrometheusRule alerts (top table) carry first-pass
operational detail. The bottom table is intentionally stub-level — each
incident class has the four-section skeleton and the *known correct
first move*. Flesh each one out when it actually fires in production;
add timestamps and learnings to the bottom of the file as a "Recent
incidents" section grows.
