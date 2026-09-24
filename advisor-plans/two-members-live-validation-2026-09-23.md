# Two retained development members — 2026-09-23

The five previous K3d clusters were removed at the user's request. The user then
requested two fresh members with monitoring, automatic Trivy, admin-only agents,
and working metrics/logs, left running for inspection.

## Correction to the initial Grafana check

The original browser check rejected only "Bad Gateway" and HTTP 5xx responses;
it did not verify Grafana application assets or user identity. It missed the
asset-loading error reported by the user. Grafana authentication, actual page
rendering, dashboard queries, and Loki Explore were subsequently fixed and
verified in [the Grafana validation](grafana-observability-validation-2026-09-23.md).
That later report also supersedes the logging navigation, pipeline scope, and
image versions below. The original native metric/log checks remain valid.

## Running environment

- Original management K3s remains `astronomer-dev-1-mj`, serving
  https://astronomer.dev.alphabravo.io. Systemd K3s PID remains **938597**.
- Exactly two K3d members remain: `astro-demo-a` and `astro-demo-b`, both
  K3s v1.35.7+k3s1. API ports are loopback-only **16651** and **16652**.
- All three Astronomer registrations are active. Final `/readyz` at 21:06 UTC
  passed database, Redis, schema, critical runtime, and **3 connected clusters**.
- Management server, worker, and frontend are Ready. Helm revision **124**.
  Preview comparisons preserved management database/Redis StatefulSets, Secrets,
  Services, ingress, and PVCs. No management K3s restart or schema change.

## Real acceptance evidence

At 21:05 UTC, all checks below passed through authenticated Astronomer APIs.

| Cluster | Trivy reports | Native cluster metrics | Native demo workload charts | Native Loki query | Monitoring |
| --- | ---: | --- | --- | --- | --- |
| [astro-demo-a](https://astronomer.dev.alphabravo.io/dashboard/clusters/bbdf0ee4-e913-432d-88a5-d6f26f89a10b) | 21 | 1 node, 21 pods; nonzero CPU/memory | 3 CPU and 3 memory samples | 73 records, latest under 5 seconds old | healthy |
| [astro-demo-b](https://astronomer.dev.alphabravo.io/dashboard/clusters/95fe09a9-003c-41fb-807f-fb1fd56898aa) | 18 | 1 node, 21 pods; nonzero CPU/memory | 3 CPU and 3 memory samples | 72 records, latest under 5 seconds old | healthy |

Both members also passed authenticated Grafana health (`database: ok`, 11.1.0),
real Image Scans rendering, cluster overview metric values, and native logging
query-dialog rendering in Chromium. Browser checks recorded no page exceptions
or HTTP 5xx responses. Namespace-restricted Loki queries returned no demo logs
outside `observability-demo`.

Trivy was installed automatically through the normal Flux baseline provisioner;
there was **no manual Trivy installation**. Both registrations used
`install_baseline: false`, proving scanning is independent of the optional
metrics baseline. Both have current-generation Ready Trivy HelmReleases and
real vulnerability reports for the demo workload.

Enrollment produced an admin ConfigMap and wildcard management ClusterRole on
both members. Member B deliberately submitted the deprecated `viewer` annotation;
the API normalized it to admin and returned an admin manifest. Kubernetes
`auth can-i '*' '*'` passed for both agent service accounts. The registration UI
has no agent-profile selector, cluster pages have no read-only badge, and the
scanning opt-out remains enabled when Quick Start is unchecked. User access
continues through Astronomer's existing RBAC gates.

## Installed applications and inspection

Both members retain:

- kube-prometheus-stack **61.3.2**, including Prometheus, Grafana, Alertmanager,
  kube-state-metrics and node exporter; 6-hour retention, 2 GiB local-path PVC.
- Trivy Operator chart **0.36.0**, operator **0.34.0**, scanner **0.74.0**.
- Loki chart **7.3.0**, Loki **3.6.11**, SingleBinary/filesystem, 2 GiB PVC.
- Fluent Bit chart **0.58.1**, with containerd log mounts and managed configuration.

Open **Logging** and query `astro-demo-a-loki` or `astro-demo-b-loki`.
The retained `observability-demo` Deployment emits a timestamped line every ten
seconds with its cluster's name. The pipelines select `observability-demo`;
the logs were collected from container files, not inserted directly into Loki.
All catalog applications and their Flux releases report installed/Ready. Both
monitoring install operations completed successfully.

The bootstrap was applied in two phases: namespaces/CRDs, establishment wait,
then dependent resources. This avoids the previously documented fresh-cluster
CRD discovery race; it does not claim the single-apply bootstrap race is fixed.

## Fixes made during validation

- Standalone cluster Prometheus queries now use the owning agent's Kubernetes
  service proxy. Shared backend credentials are not sent to member services.
  Local queries do not require external labels that are absent from local TSDB
  samples. Shared/federated backend selection remains available.
- Cluster-local Loki service FQDNs use the owning cluster's tunnel after existing
  query authorization and cluster/namespace scoping. External destinations
  retain their guarded HTTP transport and SSRF policy.
- Monitoring reconciliation and alert evaluation are registered/scheduled on
  the server tunnel queue, where the agent requester is actually available.
  This fixes false degraded status for healthy remote standalone Prometheus.
- Workload pod selectors now escape regular expressions and PromQL string
  literals separately, fixing charts for pod names with hyphens or dots.

Tests cover cluster-bound routing, no shared-backend fallback on disconnection,
scoped Loki queries, task ownership/scheduling, and exact workload pod matching.
Full monitoring, handler, worker-task, worker, server, and worker-command unit
packages passed for the relevant changes; the full handler package passed again
after the workload-selector fix. Go vet and pinned lint passed (0 issues).
Dependency boundaries, complexity ratchets and generated task inventory checks
passed. This is not a claim of a fresh full enterprise/Local CI qualification.

These are single-node development stacks with local storage. K3d nodes share
host resources; disk summary fields remain zero because the existing query
excludes their overlay filesystem. CPU, memory, pod/node, network and workload
series were checked. Registry-based scanning does not establish coverage for
sideloaded unpublished development images. HA, external alert delivery, and
long-term retention/recovery were not tested.

## Private evidence and deployment images

Evidence, kubeconfigs, manifests, Helm previews, raw query results and screenshots
are in `/var/tmp/astronomer-two-members-20260923` (private directory). Credentials
are not committed. `acceptance.json` and `browser-result.json` both report pass.
No commit or push was performed.

- server: `localhost/astronomer-go-server@sha256:66c86406d2190814267afb91df4a45d63a08a9e25b344bc8b1dcf0eb5261793c`
- worker: `localhost/astronomer-go-worker@sha256:0e00836497b22b7dc1f7a3276d85374073b0a0842642b0d022bab9526a6ad196`
- agent: `localhost/astronomer-go-agent@sha256:f31b1bacf73abcf18a8b89e7bcf60aa9157e696346bd0358f464fa63c9a9cf41`
- frontend: `localhost/astronomer-frontend@sha256:ec39ca0eaaa2c3c8b178811226f35eef7d416666acdb72372a828df8706377e0`
