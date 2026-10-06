# Phase 10 evidence: live vs polling inventory

Branch feat/031-p10-live. Source of truth: `frontend/src/lib/live/{stream,hooks,routes,dispatch,status-store}.ts`,
`internal/events/bus.go`, `docs/openapi.yaml`.

## How live works here (so "convert to live" has a precise meaning)

There is exactly one stream: `GET /api/v1/events/stream/` (ticketed SSE, one EventSource for the dashboard,
1s-30s backoff, 75s watchdog). It carries typed envelopes; there are NO per-resource watch endpoints for the UI
(`/clusters/{id}/pods/watch/` exists but is a separate pod-watch API not used by the list pages).
Pages do not subscribe individually. The convention is:

1. `lib/live/routes.ts` maps event type -> React Query keys (invalidation, never list replacement; the only
   `setQueryData` patching is `cluster.metrics` / `cluster.status_changed` in `cluster-merger.ts`).
2. The query declares `refetchInterval: liveFallback(ms)`: polling is OFF while the stream is open and ON as
   fallback when it is closed. The open->closed transition bulk-invalidates active queries so polling restarts.

So a "polled" query is already live if (a) it uses `liveFallback` and (b) its key is routed. The 52-file count in
the plan counts every `refetchInterval`; almost all are this live-with-fallback pattern already (done in P4.x).

## Result

- Live with fallback (liveFallback + routed key), unchanged: the large majority (grouped below).
- Defect found and fixed (liveFallback but key NOT routed, so the data froze while the stream was open): 3 files.
- Polling kept (no stream event exists, or operation-settling polls with their own terminal condition): listed below.
- Pages moved from raw polling to the live hook: 0 (nothing raw-polled had an existing event). Pages corrected so
  they no longer depend on a stream event that does not exist: 3.

## Fixed in this phase (liveFallback gated a resource no event refreshes)

| File | Fetches | Stream event for this key? | Decision |
|---|---|---|---|
| components/catalog/catalog-operation-timeline.tsx | catalog operation detail (`catalog.operation(id)`) | No (`catalog_release.changed` routes `catalog.installedAll` only) | Poll 2.5s until settled, independent of stream state |
| routes/dashboard/settings/operations/index.tsx (outbox query only) | task outbox list | No (`admin_queue.changed` routes `admin.queues` + dlq only) | Poll 10s unconditionally; queues + DLQ stay on liveFallback |
| components/clusters/control-plane-snapshots-page.tsx | control-plane snapshots page | No (`snapshot.changed` covers Velero snapshots/schedules/status) | Poll 15s unconditionally |

## Live with fallback, routed (unchanged)

lib/hooks/clusters.ts (list, summary, detail, nodes, node detail, conditions, remediation, events, pods:
cluster.* + k8s_changed Node/Pod/Event), lib/hooks/workloads.ts (list/pods/metrics via `workloads.byCluster`),
lib/hooks/kubernetes-proxy.ts (generic resources/counts), components/resources/namespace-queries.tsx,
lib/hooks/alerting.ts (alerting.changed), lib/hooks/audit.ts (audit.*), lib/hooks/security.ts (security_policy.changed),
components/security/hooks.ts (cis_scan.changed; scan detail polls only while unfinished), components/backups/hooks.ts
(backup.changed), lib/hooks/logging.ts (logging_operation.changed), lib/hooks/tools.ts (tool_operation.changed),
lib/hooks/catalog.ts (catalog_release.changed), components/clusters/snapshot-page-hooks.ts (snapshot.changed),
routes/dashboard/clusters/$id/{-page (image_scan.changed, service_mesh.changed), apps, registries (registry.changed),
network-access (network_access.changed), service-mesh, image-scans (image_scan.changed), template (template_binding.changed),
delivery/*}, routes/dashboard/delivery/** (delivery_*.changed), routes/dashboard/settings/siem (siem_forwarder.changed),
routes/dashboard/agents (cluster_agents.changed), components/layout/topbar.tsx + components/charlie/settings/{agent,diagnostics}-tab
(charlie_finding.changed / sys.ping), components/projects/hooks.ts (ResourceQuota k8s_changed ->
`projects.detail` prefix), components/clusters/registration-timeline.tsx (own stream-aware interval).

## Polling kept (no backend stream event)

| File | What it fetches | Stream endpoint? | Decision |
|---|---|---|---|
| components/clusters/snapshot-restore-tracking.tsx (list 10s, detail 2.5s until terminal) | restore history/detail | `snapshot.changed` kind=restore fires on create only, not on phase transitions | Keep polling |
| components/monitoring/hooks.ts (adopt poll + op detail) | monitoring-stack operations | none | Keep |
| components/charlie/settings/mode-tab.tsx, components/charlie/use-charlie-conversation.ts | Charlie mode/agent settle, awaiting-reply | none (conversation reply has no event) | Keep |
| routes/dashboard/settings/general/-support-tab.tsx | support-bundle operation | none | Keep (stops at terminal) |
| components/settings/hooks.ts | management backup status while destinations reconcile | none | Keep (conditional) |
| routes/dashboard/clusters/$id/gatekeeper/-hooks.ts | constraint sync while `pending` | `Constraint` informer event is cluster-side only; `syncStatus` is central state | Keep (3s, conditional) |
| lib/hooks/workloads.ts workload operations (via useOperationMutation) | operation status | none | Keep |
| components/catalog/catalog-operation-timeline.tsx | see Fixed | none | Keep (now unconditional) |
| routes/dashboard/settings/operations/index.tsx outbox | see Fixed | none | Keep |
| components/clusters/control-plane-snapshots-page.tsx | see Fixed | none | Keep |
| routes/dashboard/clusters/$id/image-scans/index.tsx progress | scan progress while scanning/just rescanned | image_scan.changed is coarse | Keep fast poll while scanning; idle uses liveFallback |
| lib/live/status-store.ts, lib/live/stream.ts, snapshots-page.tsx | comments only | n/a | n/a |

## Tests

`lib/live/live-conversions.test.tsx` (stream event patches a cached row without a refetch; fallback to polling after
stream failure; catalog operation polls regardless of stream). e2e `tests/e2e/live-and-multicluster-search.spec.ts`
(stubbed `cluster.metrics` event changes a clusters-list row with no `/clusters` refetch; grouped multi-cluster search + axe).
Note: `cluster.status_changed` is NOT usable for a no-refetch demo on the clusters page because that page also
invalidates `clusters.all` on it (patch, then paced confirm refetch) - by design.

## 10.2 Cross-cluster workload/pod/image search

- Workloads and pods: ALREADY available. `GET /api/v1/resources/search` (`searchResourcesAcrossClusters`) is a
  bounded fan-out (worker pool, fleet deadline, top-K merge, per-cluster errors) and the Global Search page
  already uses it (types: pods, deployments, statefulsets, daemonsets, jobs, cronjobs, ...). Added: per-cluster
  grouping with status mix and not-searched (failed) clusters, plus DataTable `groupBy` (toggle "Group by cluster",
  default on). Pure grouping logic is in `routes/dashboard/search/-search-grouping.ts`.
- Images: STOPPED. The search endpoint filters only by `name` (substring on the object name), `label`, `field`
  selectors and namespace. `flattenSearchPods` (`internal/handler/resources_search.go`) does not emit container
  images, and Kubernetes field selectors cannot select by image. There is no `image` query parameter and no other
  fleet-wide inventory endpoint. `GET /clusters/{id}/vulnerabilities/images/` is per-cluster (one request per
  cluster) and only lists vulnerability-scanned images. Looping N clusters from the browser was not done because no
  existing bounded multi-cluster endpoint is used for this elsewhere in the UI.
  Needed backend change: add an `image` filter (and an `images` field on pod rows) to `/api/v1/resources/search`,
  or a dedicated `GET /api/v1/images/presence?image=` fan-out.

## 10.3 "Where is this image running?"

STOPPED for the same reason: without a server-side image filter on the cross-cluster search, a link filtered by
image would silently return unrelated rows. No UI was added on the image-scans page.
