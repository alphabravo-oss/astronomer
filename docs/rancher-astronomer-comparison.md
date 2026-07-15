# Astronomer vs Rancher: Day-2 multi-cluster comparison (current)

_Last updated: 2026-07-09_

This document compares **Astronomer** to **Rancher** for **day-2 operations on
already-provisioned clusters**. Astronomer deliberately **excludes cluster
provisioning / machine drivers / node pools**. GitOps is **Argo CD /
ApplicationSets**, not Fleet. Prefer this file over any older draft that claimed
missing SCIM, MFA, namespace bindings, or SSA.

---

## Status legend

| Status | Meaning |
|---|---|
| **Ahead** | Materially more day-2 capability than Rancher |
| **At Parity** | Comparable for adopted-cluster ops |
| **Behind** | Trails Rancher density or defaults |
| **Intentionally Excluded** | Out of product scope |

---

## Scoreboard (code-current 2026-07-09)

| Area | Status | Notes |
|---|---|---|
| Cluster provisioning | **Intentionally Excluded** | Import / adopt only |
| Cluster import / agent / health | **At Parity** | Registration wizard, tunnel, heartbeat, decommission |
| GitOps (Argo vs Fleet) | **At Parity** | Argo CD multi-instance + ApplicationSets + fleet ops |
| Cluster explorer | **Behind** | Watch + SSA YAML exist; Steve-grade forms still thin |
| Monitoring lifecycle | **Ahead** | Per-cluster Prometheus/Thanos reconciler |
| Alerting | **At Parity** | Rules, channels, silences, inhibition, resolve paths |
| Logging / SIEM | **Ahead** | Fluent Bit pipelines + multi-transport SIEM; Loki query present |
| Security / policy | **Ahead** | CIS, trivy, Gatekeeper authoring, PSA/netpol |
| Compliance baselines | **Ahead** | PCI/HIPAA/FedRAMP/SOC2 + posture |
| RBAC / tenancy | **At Parity** | Templates, inheritance, projects, ns bindings; **ns-RBAC default OFF** until product flip after F7-b soak |
| Auth / SSO / SCIM / MFA | **At Parity** | Local + TOTP + Dex SSO; **SCIM Users + Groups** (incl. DELETE); password policy + session absolute JWT TTL |
| Audit | **Ahead** | Partitioned Postgres + outbox + SIEM |
| Backup / DR | **Ahead** | Velero + mgmt pg_dump + restore drill + key wrap (prod requires wrap secret) |
| Catalog / tools | **At Parity** | Helm/OCI; git chart type accepted (worker maturity evolving) |
| Tools / shell / apply | **At Parity / Ahead** | kubectl shell, exec/logs, **server-side apply** from UI |
| API / OpenAPI / CLI | **Ahead** | OpenAPI + `astro` CLI + contract CI |
| HA / airgap | **At Parity** | Multi-replica chart, dual tunnel documented (install default = legacy `connect`) |

---

## Landed capabilities (do not claim as missing)

| Capability | Evidence (paths) |
|---|---|
| SCIM 2.0 Users + Groups write/delete | `internal/handler/scim.go`, `routes.go` SCIM routes |
| MFA / TOTP | `internal/handler/totp.go` |
| Password policy | `internal/auth/password_policy.go` + platform settings keys |
| Session absolute TTL | `session.timeout_minutes` → JWT mint (not idle logout) |
| Namespace-scoped bindings + authoring | RBAC handlers + UI; flag `namespace_scoped_rbac_enabled` **default false** |
| F7-b ns-filtered raw-proxy watch | Middleware admits list+watch with allow-set; tunnel filters list + watch frames |
| Server-side apply from UI | `frontend/src/lib/api.ts` `k8sApplyYaml` |
| Live resource watches (typed pods + SSE) | pods watch + events stream |
| Dual-tunnel matrix | `docs/dual-tunnel-matrix.md` — install stays on `connect` |

---

## Residual gaps (honest)

1. **Explorer form density** vs Rancher Steve (YAML-first; partial structured forms).
2. **`namespace_scoped_rbac_enabled` default remains OFF** until product sign-off after F7-b soak.
3. **Dual tunnel cutover** incomplete — remotedialer is experimental and existing-durable-identity-only; see dual-tunnel matrix.
4. **Measured scale baseline** empty — see `docs/scale-baseline.md` (blocked row when no live plane).
5. Non-Loki log query backends may still return 501.

---

## Intentionally different

| Choice | Implication |
|---|---|
| No provisioning | No machine drivers / CAPI node pools in product |
| Argo CD not Fleet | Operators use ApplicationSets + cluster labels |
| Postgres + asynq hybrid | Drift converges on task cadence, not pure etcd controllers |
| Dex-brokered SSO | LDAP/SAML/OIDC via connectors, not 17 first-party plugins |

---

## Marketing rules

- Do **not** claim cluster provisioning.
- Do **not** claim certified N-cluster scale without a pass row in `docs/scale-baseline.md`.
- Do **not** claim Fleet product compatibility.
- Do claim SCIM, MFA, SSA, ns bindings, and Argo CD GitOps when describing current code.
