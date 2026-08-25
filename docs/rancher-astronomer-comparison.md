# Astronomer and Rancher: adopted-cluster operations scorecard

Status: current release guidance
Last reviewed: 2026-08-25
Scope: day-2 management of clusters that already exist

<!-- api-contract-evidence: operations=793 mounted_routes=754/754 request_bindings=151 provisional_request_shapes=0 actionable_202=104 async_202_exceptions=5 -->

Astronomer is intended to meet or exceed Rancher's operator experience for
adopted clusters. It intentionally does not provision clusters, machines, node
pools, or cloud infrastructure. Astronomer uses its own API, durable PostgreSQL
intent, an outbound cluster agent, and release-pinned local Flux controllers;
it does not install or emulate Rancher Fleet.

This is an engineering scorecard, not a marketing assertion. A capability is
only marked complete when an executable surface and evidence exist in this
repository. Scale and availability remain qualified only when a retained
certification report records a pass.

## Status definitions

| Status | Meaning |
| --- | --- |
| Complete | Implemented through public API and operator UI, with automated evidence. |
| Partial | Useful implementation exists, but the stated limitation matters to enterprise operation. |
| Qualified externally | Harness and acceptance criteria exist; a production-like environment must produce the release-specific pass artifact. |
| Excluded | Deliberately outside the product boundary. |

## Capability scorecard

| Operator outcome | Status | Astronomer implementation and evidence | Remaining limitation |
| --- | --- | --- | --- |
| Provision clusters, machines, and node pools | Excluded | The product begins at adoption; see [product boundary](../README.md#what-astronomer-is-not). | Use Terraform, Cluster API, a cloud service, or another provisioning system. |
| Adopt and inventory existing clusters | Complete | Signed, expiring registration, privilege profiles, progress/retry/cancel APIs, outbound tunnel, heartbeat, compatibility negotiation, and decommission lifecycle. See [registration API](cluster-registration-api.md), [agent profiles](agent-privilege-profiles.md), and `internal/handler/cluster_registration.go`. | None inside the adopted-cluster scope. |
| Multi-cluster grouping and tenancy | Complete | Projects, cluster groups, namespace assignments, global/project/cluster/namespace RBAC, effective-permission preview, and permission-aware navigation. See the [RBAC permission contract](rbac-permission-contract.md) and `internal/rbac`. | Namespace-scoped behavior depends on an agent profile capable of enforcing the requested operation. |
| GitOps source and delivery lifecycle | Complete | Immutable Git/OCI/Helm source identities, bundle versions, frozen placement snapshots, approvals, maintenance windows, rollout strategies, assignment generations, normalized status, rollback, and local Flux reconciliation. See [delivery ADR](architecture/decisions/flux-native-delivery.md), [state contract](control-plane-state-contract.md), and `internal/handler/delivery_*`. | No Rancher Fleet API or compatibility surface by design. |
| Operate while the management plane is unavailable | Complete | The last accepted assignment generation continues reconciling locally; reconnect reports bounded status and resumes newer generations. See [delivery control-plane runbook](runbooks/delivery-control-plane.md). | New intent, approvals, and centralized visibility require the management plane. |
| Browse built-in and custom Kubernetes resources | Partial | Live discovery, CRD served/storage versions, printer columns, structural schema, common-resource guided create/edit forms, list/get/create/update/delete, watch, strict dry-run, diff, server-side apply, impact previews, events/conditions/related resources, logs/exec, rollout history, and desktop/mobile keyboard workflows are implemented. The retry-free live suite also proves real resource dry-run/apply and recovery-adjacent journeys. See `internal/handler/resource_discovery.go`, `frontend/src/components/resources`, and the generated [OpenAPI](openapi.yaml). | Complex or non-structural CRDs may still be more effective in YAML mode. Relative clicks, clarity, recovery, and completion time remain unqualified until the retained [benchmark-v1](assurance/rancher-benchmark/README.md) paired run and human study pass. |
| Resolve server-side-apply conflicts safely | Complete | Normal writes never force ownership. Conflict takeover is an explicit, permission-gated, audited action after dry-run/diff. See `internal/handler/resources.go` and the resource explorer. | Force cannot bypass downstream Kubernetes authorization. |
| Workload and node operations | Complete | Scale, restart, pod deletion, logs, exec, cordon, uncordon, label/annotation/taint changes, and policy-aware drain. Requests are API-driven and audited. | Behavior is limited by the adopted agent's declared capability and privilege profile. |
| Controlled shell and service access | Complete | Expiring sessions, scoped command policy, audited proxy kubeconfigs, separately scoped 15-minute direct read-only kubeconfigs, service proxy allowlisting, cleanup, and audit. See [proxy inventory](kubernetes-proxy-inventory.md). | Astronomer does not mint direct long-lived cluster-admin kubeconfigs. |
| Monitoring and alerting | Complete | Monitoring stack lifecycle, Prometheus-compatible metrics, Grafana ticketing, alert rules/events, notifications, silences, inhibition, and management-plane health. | Backend retention and capacity depend on the operator-selected deployment topology. |
| Unified logs | Complete | Output capability discovery, tenant-scoped system Loki query, OpenSearch/Elasticsearch and credentialed Splunk adapters, safe deep links, shipping-only labeling, provider timeouts, response limits, secret redaction, and transactional same-cluster pipeline-to-destination routing. See `internal/handler/logging_loki_query.go` and `internal/handler/logging.go`. | Datadog and CloudWatch use secure deep links; S3 and syslog are deliberately shipping-only. |
| Security posture and policy | Complete | Durable CIS scan lifecycle, image vulnerability history, Gatekeeper constraints, Pod Security, network policy, API-server allowlist, registry and service-mesh posture. | Findings do not replace a human risk-acceptance process. |
| Identity and lifecycle management | Complete | Local identity, OIDC/OAuth connectors, TOTP and recovery, password policy, API tokens, group mapping, SCIM Users/Groups, session revocation, lockout, and quotas. | Connector-specific upstream availability remains external. |
| Audit and external evidence | Partial | Mutation-intent audit is fail-closed; durable outbox delivery, read-audit coverage, export, SIEM forwarding, redaction, and cross-tenant negative tests are present. See the [audit schema and delivery contract](audit-schema-v1.md). | Every domain mutation does not yet commit its audit row in the exact same database transaction; external SIEM receipt must also be monitored by the operator. |
| Backup, restore, and disaster recovery | Complete | Management-plane backup destinations, restore drills, Velero cluster workflows, key ownership guidance, PostgreSQL failover/restore runbooks, and operation history. | A release claim requires the operator's own restore rehearsal and storage durability evidence. |
| API automation | Complete | The current contract contains 793 quality-checked operations, covers 754/754 mounted routes, binds 151/151 Go request shapes with zero drift and zero provisional/propertyless request shapes, and generates TypeScript and Go clients plus the embedded specification. Exactly 104 actionable `202` mutations require bounded idempotency and durable typed receipts/status resources; the five locked exceptions are trusted ingest, privacy acknowledgement, or interactive message acceptance. See the generated [OpenAPI contract](openapi.yaml) and `scripts/openapi-quality.mjs`. | The raw Kubernetes proxy remains an intentionally opaque adapter rather than a domain schema; its policy, authorization, and audit boundaries are public and tested. |
| CLI and Kubernetes-native intent | Complete | `astro` exposes automation-oriented workflows; optional `management.astronomer.io/v1alpha1` CRDs support declarative Cluster, Project, and AgentProfile intent with ownership conflict protection. See [CRD API](crd-api.md). | CRDs intentionally do not mirror every relational product object. |
| Extension model | Complete | Signed/validated declarative extension manifests, governed mounts, capability checks, constrained proxy bridge, and uninstall/reinstall lifecycle. | Arbitrary in-process backend plugins are not supported. |
| Accessible, responsive operator UI | Partial | Vite/React console, shared semantic primitives, keyboard/focus policy, zero-warning accessibility lint, route-wide serious/critical axe gate, and dark/light desktop/mobile visual baselines. | Release-candidate keyboard-only and screen-reader acceptance for the critical workflows is still a human qualification step; browser support follows the generated [compatibility matrix](architecture/compatibility.md). |
| HA and scale qualification | Qualified externally | HA chart topology, leader election, tunnel ownership, durable outbox/worker recovery, exact 100/500/1000/2000-cluster profiles, reconnect storms, failure drills, machine-readable reports, SHA-256 evidence, and a protected certification workflow. See [scale baseline](scale-baseline.md). | No cluster-count or SLO claim is valid until the release commit has a retained passing report from production-like infrastructure. |
| Air-gapped operation and supply chain | Complete | Digest-pinned images, locked chart dependencies, release manifest, SBOM/signature workflow, mirror inventory, offline install guidance, and version preflight. See [air-gapped install](airgapped-install.md) and [image verification](verify-images.md). | Operators must mirror every item in the generated release manifest. |

## Architectural differences that are intentional

| Area | Rancher-oriented expectation | Astronomer contract |
| --- | --- | --- |
| Provisioning | Rancher can create and manage infrastructure-backed clusters. | Astronomer adopts clusters only. |
| Downstream delivery | Rancher Fleet supplies its own multi-cluster delivery model. | Astronomer owns product intent and placement; release-pinned Flux controllers reconcile a closed object set locally. |
| Control-plane persistence | Kubernetes APIs and Rancher services own different product objects. | PostgreSQL is authoritative for Astronomer intent/history; cluster Kubernetes APIs remain authoritative for live resources and Flux reconciliation state. |
| Connectivity | Rancher uses downstream agents. | Astronomer also uses an outbound agent, with protocol/capability negotiation and no inbound managed-cluster requirement. |
| Raw Kubernetes API | Rancher commonly exposes Kubernetes API access through its proxy. | Astronomer exposes explicit typed APIs plus a tightly constrained proxy surface with user/namespace authorization and audit. |

## Release-claim rules

- Never claim cluster provisioning, machine management, or Rancher Fleet
  compatibility.
- Never publish a cluster-count, latency, RPO, RTO, or availability claim from
  configuration defaults. Link the retained release-specific evidence.
- Describe unsupported logging providers as link-out or shipping-only; do not
  imply native query support.
- Describe proxy kubeconfigs as short-lived, revocable, read-only, and routed
  through Astronomer.
- A new UI mutation is incomplete until its public API contract, authorization,
  audit behavior, and generated client operation exist.

## Verification entry points

Run the same gates used by release CI:

```bash
make verify-enterprise
cd frontend && npm run test:e2e:smoke
node scripts/validate-rancher-benchmark.mjs
```

Live adopted-cluster, failure-injection, upgrade, restore, and scale
qualification are separate protected workflows because they require external
infrastructure. Their absence must produce an unqualified result, never an
assumed pass.

The Rancher-relative benchmark is separately qualified. Its mechanical half
requires paired, provenance-bound task evidence against the pinned Rancher
commit and Dashboard release; clarity and discoverability require a
counterbalanced human study. Neither independent Astronomer browser success nor
source-level feature comparison is a comparative pass.
