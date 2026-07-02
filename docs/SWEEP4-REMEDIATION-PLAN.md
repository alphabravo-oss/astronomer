# Sweep 4 Remediation Plan — 32 Verified Findings (2026-07-02)

> Fourth grounded ultracode sweep (11 finders → adversarial verify). 34 raw → 32 CONFIRMED/PLAUSIBLE. Deep-tail: concurrency/races, resource leaks, fleet-scale perf, and privilege holes in the recently-added RBAC surface (native + group-sync, incl. 2 regressions from sweep-3). File-disjoint workstreams; shared files (protocol, scheduler, server.go, generated sqlc) integrator-owned.

## Waves
- **Wave 1 (security + systemic leak + state):** A-native-rbac, B-groupsync, C-streamstop (systemic per-stream cancellation + io cap), F-state (races/outbox/decommission).
- **Wave 2 (concurrency + perf + frontend):** D-tunnel-race, E-rbac-routes, H-db, I-perf-worker, G-frontend.


---

## A-native-rbac (wave 1)
**Files:** `internal/handler/native_rbac.go`, `internal/rbac/native.go`

### S4-04 · [L/security] Native-RBAC rule authoring has no privilege-escalation guard: an rbac:create holder can self-grant permissions they lack (incl. secrets, pod create)
- **Loc:** `internal/handler/native_rbac.go:105` · CONFIRMED
- **Failure:** A non-superuser holding rbac:create/rbac:delete but NOT secrets/workload access (the exact delegated-admin case enforceNoEscalation exists to contain) POSTs /native-rbac-rules/ with {userId: <self>, apiGroup:
- **Fix:** Give NativeRBACHandler the RBAC engine + bindings querier and run an enforceNoEscalation-equivalent in Create(): for the target (apiGroup→ResourceCustomResources or the mapped built-in resource, each verb) at the rule's cluster/namespace scope, require the CALLER's own bindings to already satisfy en
- **Verify:** regression test reproducing the failure; standard gate.

### S4-23 · [M/security] Native evaluator's cluster-admin escalation refusal omits certificates.k8s.io, so a native rule granting CSR approval can mint a cluster-admin client cert
- **Loc:** `internal/rbac/native.go:31` · CONFIRMED
- **Failure:** A native rule (authored, or self-authored via finding 1) with {apiGroup:
- **Fix:** Add 
- **Verify:** regression test reproducing the failure; standard gate.


---

## B-groupsync (wave 1)
**Files:** `internal/auth/group_sync.go`, `internal/db/migrations/128_group_sync_binding_connector.up.sql`

### S4-16 · [M/correctness] Group-sync connector scoping (migration 128) never revokes wildcard-mapping grants except through the minting connector — regression from sweep-3 Finding #3
- **Loc:** `internal/auth/group_sync.go:244` · CONFIRMED
- **Failure:** Wildcard mapping grants global-admin to group 'g'; user in 'g' first logs in via connector A -> binding stamped A. Operator deletes the wildcard mapping (revoking admin fleet-wide). User now only logs in via connector B: `wanted` excludes the role, but the enumeration scoped to B (`IS NOT DISTINCT FROM B`) never returns the A-stamped binding, so it is never deleted — the user keeps global-admin indefinitely. Pre-128 code enumerated ALL group_sync bindings and would have revoked it, so this is a security-relevant over-retention regression introduced by the connector-scoping fix.
- **Fix:** Stamp the binding with the mapping's provenance (`m.ConnectorID`, NULL for wildcard) instead of the login `connectorID`, and enumerate wildcard-owned (NULL) bindings on every sync (e.g. `group_sync_connector_id IS NOT DISTINCT FROM $2 OR group_sync_connector_id IS NULL`) so wildcard grants are recon
- **Verify:** regression test reproducing the failure; standard gate.

### S4-17 · [M/security] Legacy NULL-connector group_sync bindings are never revoked after migration 128 (stale privilege retention)
- **Loc:** `internal/db/migrations/128_group_sync_binding_connector.up.sql:24` · CONFIRMED
- **Failure:** Operator was using group_sync before upgrading to migration 128, so pre-existing bindings carry group_sync_connector_id=NULL. After upgrade, a user's SSO login through a named Dex connector passes a Valid connectorID; `NULL IS NOT DISTINCT FROM <uuid>` is false, so the legacy NULL rows are never enumerated. If the operator then deletes the group mapping, subsequent logins never revoke those legacy bindings — the user retains the (possibly admin) role indefinitely. The create path also can't reclaim them: CreateGroupSyncGlobalBindingForConnector hits `ON CONFLICT (user_id, role_id) DO NOTHING`,
- **Fix:** Backfill group_sync_connector_id in migration 128 (or a follow-up) from user_idp_groups.connector_id, or have the reconciler additionally enumerate+revoke NULL-connector rows when the mapping set no longer covers them.
- **Verify:** regression test reproducing the failure; standard gate.


---

## C-streamstop (wave 1)
**Files:** `internal/agent/k8sproxy.go`, `internal/handler/pods_watch_tunnel.go`

### S4-05 · [L/leak] Tunnel K8S_STREAM watches (pod-watch SSE + /k8s/* watch passthrough) have no stop signal to the agent → agent goroutine + apiserver-watch + tunnel-bandwidth leak on client disconnect
- **Loc:** `internal/handler/pods_watch_tunnel.go:65` · CONFIRMED
- **Failure:** A user opens the pods page (SSE watch) or issues a `?watch=true` /k8s passthrough, then navigates away. The server drops the stream (CloseStream) but sends nothing to the agent; the agent keeps its kube-apiserver watch + goroutine alive and keeps base64-encoding pod events into K8S_STREAM_FRAME messages that the server discards (
- **Fix:** Mirror the logs/exec fix: add a `MsgK8sStreamStop` protocol message; have the server-side watch goroutine send it (with streamID) in the same defer that calls CloseStream; and have the agent register a handler that cancels a per-stream context so `HandleStreamRequest`'s Read unblocks and `resp.Body`
- **Verify:** regression test reproducing the failure; standard gate.

### S4-13 · [M/leak] Proxied k8s watch stream has no per-stream cancellation: abandoned watch leaks an agent goroutine + apiserver watch and can force a full-tunnel reset
- **Loc:** `internal/agent/k8sproxy.go:217` · CONFIRMED
- **Failure:** A dashboard user opens a pod watch then navigates away. The server closes its stream bookkeeping but never signals the agent. The agent's HandleStreamRequest goroutine keeps the kube-apiserver watch connection open and keeps calling tunnel.Send for every watch event. On a stable long-lived tunnel these accumulate (goroutine + apiserver watch per abandoned watch). On a busy cluster the orphaned event stream fills the 256-slot sendCh, at which point Send() triggers failClose (tunnel.go:473) and tears down the ENTIRE tunnel — killing every other in-flight stream/op for that cluster and forcing a 
- **Fix:** Add a stream-scoped cancel: give each K8sStreamRequest its own context stored in a sessions map keyed by StreamID, register a stop message handler (or reuse an END-from-server frame) that cancels it, and have the server send it when its consumer ctx is Done (mirroring MsgLogStop).
- **Verify:** regression test reproducing the failure; standard gate.

### S4-14 · [M/leak] Unbounded io.ReadAll of proxied k8s response body — no size cap on the agent's memory (service_proxy has one, k8s proxy does not)
- **Loc:** `internal/agent/k8sproxy.go:393` · CONFIRMED
- **Failure:** A single proxied non-paginated LIST (e.g. the dashboard requesting all pods/events/secrets on a large cluster, or a GET of a multi-hundred-MiB object) makes the agent allocate the full body plus its base64 copy in RAM at once; with the goroutine-per-message dispatch, several concurrent large reads multiply this, OOM-killing the agent pod.
- **Fix:** Wrap resp.Body in io.LimitReader with a configurable cap (and return 413 past it) as service_proxy does, or convert the large-body path to true streaming (read+chunk directly from resp.Body like HandleStreamRequest) instead of io.ReadAll-then-chunk.
- **Verify:** regression test reproducing the failure; standard gate.


---

## D-tunnel-race (wave 2)
**Files:** `internal/tunnel/handler.go`, `internal/tunnel/server.go`

### S4-07 · [L/race] stateLimiter() takes the hub-wide EXCLUSIVE write lock on every STATE_UPDATE frame
- **Loc:** `internal/tunnel/handler.go:653` · CONFIRMED
- **Failure:** Under the exact fleet-scale STATE_UPDATE flood the agent sharding (sharded_agents.go doc) was built to de-serialize, every state frame across ALL clusters now grabs the single hub-wide `h.mu` in exclusive mode just to fetch an already-initialized limiter pointer, blocking all the RLock readers (publishHeartbeat, handleMetrics, publish) and serializing state-update processing on one mutex — re-introducing the hub-wide bottleneck sharding removed.
- **Fix:** Double-checked locking: read h.stateLim under RLock first and return it if non-nil; only take the write lock to lazily construct it on the first call.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-08 · [L/inefficiency] stateUpdateLimiter eviction runs a full O(N) map scan on EVERY call once the map exceeds 256 entries (comment claims per-256-call amortization)
- **Loc:** `internal/tunnel/handler.go:705` · CONFIRMED
- **Failure:** With churny keys (ephemeral CI/preview namespaces, short-lived CRs) keeping >256 entries fresher than the 60s evict window, every single allow() call performs a full O(N) map iteration under the shared stateUpdateLimiter.mu — the map never drops below the threshold, so the intended amortization never happens and each state frame pays an O(N) scan serialized behind the one limiter lock.
- **Fix:** Add an actual call counter (e.g. increment a field and only scan when count%256==0) or switch to a time-bucketed/last-swept timestamp so the O(N) sweep runs at most once per interval, matching the comment's stated intent.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-26 · [M/race] Superseded connection's teardown clobbers the live locator entry on same-pod reconnect
- **Loc:** `internal/tunnel/server.go:691` · CONFIRMED
- **Failure:** Agent re-dials and lands on the SAME server pod before the old WS finishes tearing down. New HandleWebSocket runs h.agents.Set (replaces old, cancels old handler ctx) then loc.Set(newCtx) installs a fresh refresh loop + writes redis. The old goroutine then unblocks and calls loc.Delete(clusterID): it cancels the NEW refresh loop (now in l.cancels[clusterID]) and CAS-deletes the redis key (value == this pod's own address, so the compare matches). Result: the cluster is live in the in-memory map on this pod but has NO redis directory entry and NO refresh loop to re-add one. Sibling replicas 503 
- **Fix:** Make removeAgent return DeleteIfSame's bool and skip the locator Delete when it returns false (we were superseded), OR give Locator.Set an identity token stored alongside cancel so Delete only cancels/CAS-deletes the entry it actually created (a DeleteIfSame analog for the locator).
- **Verify:** regression test reproducing the failure; standard gate.


---

## E-rbac-routes (wave 2)
**Files:** `internal/server/middleware/rbac_cache.go`, `internal/server/middleware/rbac_queries.go`, `internal/server/routes.go`, `internal/server/routes_resources_workloads.go`

### S4-25 · [M/correctness] Namespaces & Events list pages 403 for the exact project persona namespace-scoped RBAC is meant to serve
- **Loc:** `internal/server/routes_resources_workloads.go:112` · CONFIRMED
- **Failure:** With namespace_scoped_rbac_enabled=true, a project-member user opens the cluster Namespaces or Events page: CheckPermission(clusters,read) fails, HasAnyNamespaceAccess(clusters,read) returns an empty set -> RequireListPermission returns a hard 403. The analogous Pods (ResourcePods) and Workloads (ResourceWorkloads) pages work for the same user. The feature's own comment (workloads.go:117) claims it filters 'pods/namespaces/events/workloads', but namespaces/events silently no-op to a lockout — including the namespace picker the scoped persona depends on.
- **Fix:** Authorize namespaces/events on a resource the target roles actually hold. Either (a) compute the union of namespaces the user can read across any granted resource for these two routes, or (b) key the namespace list/events allow-set off ResourceWorkloads/ResourcePods (what project roles grant) instea
- **Verify:** regression test reproducing the failure; standard gate.

### S4-29 · [S/inefficiency] RBAC cache takes a write lock on every cache HIT, serializing all authenticated requests through one mutex
- **Loc:** `internal/server/middleware/rbac_cache.go:152` · CONFIRMED
- **Failure:** GetUserBindings is on the hot path of every authenticated HTTP request and every /k8s/* proxy request (requireK8sProxyPermission, RequirePermission, RequireListPermission). At high request concurrency across many clusters, every request contends on the single c.mu write lock for the LRU MoveToFront even though the payload is already in hand, defeating the RWMutex and turning RBAC lookup into a global serialization point.
- **Fix:** Skip the MoveToFront when the entry is already near the front, or record the touch timestamp under RLock and only reorder opportunistically (e.g. amortized/segmented LRU, or a CLOCK approximation), so steady-state hits never take the exclusive lock.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-30 · [S/parity] Namespace-scoped list filter ignores native rules: a user whose only grant is a native namespaced rule gets 403 on a cluster-wide LIST instead of a filtered result
- **Loc:** `internal/server/routes.go:1269` · CONFIRMED
- **Failure:** With native_rbac_enabled and namespace_scoped_rbac_enabled, a user whose sole grant for a CRD is a native rule {namespace:
- **Fix:** In the namespace-scoped branch, also fold native per-namespace list grants into the allow-set (e.g. collect namespaces from the user's native rules matching resource+list+cluster) before deciding 403, so native namespaced grants participate in the list-filter like coarse project bindings do.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-32 · [S/inefficiency] Project->namespace binding expansion violates the RBAC cache's memory bound and forces O(namespaces) scans per list request
- **Loc:** `internal/server/middleware/rbac_queries.go:241` · PLAUSIBLE
- **Failure:** A member of a large multi-cluster project (e.g. 2000+ namespaces across clusters) gets 2000+ synthetic bindings cached; the documented 'few MB' bound is blown by a handful of such users, and every cluster-wide list gate (routes.go:1269 and workloads.go authorizedNamespaces) performs a full O(binding-count) scan per request instead of O(user-role-count).
- **Fix:** Don't materialize one binding per namespace: represent a project grant as a single binding carrying the namespace allow-set (a map/slice) and teach AuthorizedNamespaces/bindingApplies to consult it, or cap/stream the expansion. At minimum correct the cache-capacity sizing assumption once expansion i
- **Verify:** regression test reproducing the failure; standard gate.


---

## F-state (wave 1)
**Files:** `internal/db/sqlc/task_outbox_ext.sql.go`, `internal/handler/projects.go`, `internal/registration/service.go`, `internal/worker/tasks/cluster_condition_reconcile.go`, `internal/worker/tasks/cluster_decommission.go`

### S4-09 · [L/bug] Stuck-applying template rows are never auto-remediated (condition emitted as status=True, reconciler only reads status=False)
- **Loc:** `internal/worker/tasks/cluster_condition_reconcile.go:114` · CONFIRMED
- **Failure:** A cluster_template_applications row wedges in 'applying' (e.g. agent tunnel dies mid tool-install, or a helm post-install hook hangs). After 10m the hourly drift sweep upserts TemplateApplyStuck=True. The 30s reconciler lists only False conditions, so remediateTemplateApplyStuck (which would reset the row to 'failed' so the recovery sweep re-enqueues it) is never invoked. The 'applying' row is not covered by the failed/pending recovery sweeps either, so the cluster's wizard sits in 'provisioning'/'applying' forever with a red badge and never auto-recovers; only a manual reapply unblocks it.
- **Fix:** Either write the stuck condition with Status=
- **Verify:** regression test reproducing the failure; standard gate.

### S4-19 · [M/bug] task_outbox re-open (UpsertTaskOutbox ON CONFLICT) does not reset attempt_count, so a re-triggered task inherits its old failure count and can dead-letter after ~1 attempt
- **Loc:** `internal/db/sqlc/task_outbox_ext.sql.go:58` · CONFIRMED
- **Failure:** asynq/Redis is briefly unreachable; a materialization outbox row's delivery fails repeatedly and attempt_count climbs to 20 (default max_delivery_attempts), row→'dead'. Operator rotates the cloud credential, which calls UpsertCloudCredentialMaterializationWithTaskOutbox with the same dedupe_key. The row re-opens to 'pending' but attempt_count stays 20. Next dispatch claims it (→21); if the very first post-reopen enqueue hits any transient error, 21>=20 → immediately 'dead' again. The re-triggered task effectively gets zero retry budget instead of 20, so a recoverable delivery is silently dropp
- **Fix:** In the ON CONFLICT DO UPDATE branches (base upsert + all *_with_task_outbox CTEs), reset `attempt_count = CASE WHEN task_outbox.status='delivered' THEN task_outbox.attempt_count ELSE 0 END` alongside the existing status reset, so a re-triggered (non-delivered) row starts a fresh delivery budget.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-21 · [M/race] Concurrent AddNamespace assigns the same namespace to two projects (cross-project uniqueness TOCTOU not in the tx)
- **Loc:** `internal/handler/projects.go:1204` · CONFIRMED
- **Failure:** Two admins concurrently POST add-namespace 'shared-ns' to project A and project B on the same cluster. Both pass the pre-tx ListProjectsByCluster check (neither sees the other's uncommitted row), each tx locks a DIFFERENT project row (no contention) and UpsertProjectNamespace succeeds for both distinct PKs. Result: 'shared-ns' is assigned to BOTH projects; the flag-gated namespace-scoped RBAC then resolves it to two projects, granting each project's members access to the other's intended namespace (tenant-isolation break).
- **Fix:** Add a partial UNIQUE index enforcing one project per (cluster_id, namespace) (e.g. `CREATE UNIQUE INDEX ... ON project_namespaces (cluster_id, namespace)`), OR move the cross-project conflict check INSIDE the runTx after acquiring an advisory/row lock keyed on (cluster_id, namespace) so the second c
- **Verify:** regression test reproducing the failure; standard gate.

### S4-24 · [M/race] Registration phase state machine has a read-modify-write lost-update race (no compare-and-swap on registration_phase)
- **Loc:** `internal/registration/service.go:283` · CONFIRMED
- **Failure:** An operator clicks Cancel (handler → Advance(EventCancel), provisioning→failed) at the same moment the apply worker finishes (OnTemplateApplySuccess → Advance(EventTemplateApplied), provisioning→ready). Both read registration_phase='provisioning' and both issue an unconditional UPDATE by id. Whichever commits last wins: a cancelled registration can end up 'ready', or a successfully-provisioned cluster can end up 'failed' — the phase-machine's transition guarantees are violated and IsTerminal side effects (SSE, metrics duration) are emitted for the wrong outcome. Duplicate no_provisioning/agent
- **Fix:** Make the write a conditional CAS: add `AND registration_phase = $expectedCurrent` to UpdateClusterRegistrationPhase and treat a 0-row result as a lost race (reload + re-evaluate or return ErrIllegalTransition), or wrap read+transition+write in a single tx with `SELECT ... FOR UPDATE` on the cluster 
- **Verify:** regression test reproducing the failure; standard gate.

### S4-31 · [S/leak] control_plane_snapshots rows are orphaned on decommission — tombstone never fires the ON DELETE CASCADE and phaseDeleteDependents omits the table
- **Loc:** `internal/worker/tasks/cluster_decommission.go:772` · CONFIRMED
- **Failure:** Because the normal decommission path TOMBSTONES the cluster row (soft-delete) rather than hard-deleting it, the ON DELETE CASCADE never fires; and phaseDeleteDependents does not delete them explicitly. A decommissioned self-managed cluster leaks all its control_plane_snapshots rows forever — the identical integrity gap migration 127 was written to close for the sibling table, missed for the table added just two migrations earlier (125).
- **Fix:** Add a `DeleteControlPlaneSnapshotsByCluster :execrows` query (`DELETE FROM control_plane_snapshots WHERE cluster_id = $1`) and include it in phaseDeleteDependents' ops list, matching the apiserver_audit_events treatment.
- **Verify:** regression test reproducing the failure; standard gate.


---

## G-frontend (wave 2)
**Files:** `frontend/src/components/dashboards/widget-grid.tsx`, `frontend/src/components/layout/sidebar.tsx`, `frontend/src/components/ui/yaml-view-dialog.tsx`, `frontend/src/components/workloads/pod-terminal.tsx`

### S4-01 · [L/inefficiency] WidgetGrid re-fetches every widget and resets all timers on each parent render (request storm)
- **Loc:** `frontend/src/components/dashboards/widget-grid.tsx:75` · CONFIRMED
- **Failure:** On the cluster detail page, which re-renders frequently (useLiveClusterMetricsMerger patches the cache on every SSE metrics tick, plus multiple refetchInterval queries), each render produces a new `fetcher` identity, so WidgetGrid's effect tears down (clears all per-widget setTimeout timers) and immediately re-runs load(), hitting the /render endpoint for the full widget set again. That endpoint fans out to Prometheus/Grafana per widget, so a chatty cluster turns into a steady storm of upstream metric queries far exceeding each widget's configured refreshSeconds.
- **Fix:** Wrap the fetcher in useCallback at the call sites (`useCallback(() => renderForCluster(cluster.id), [cluster.id])`), or accept the fetch identity out of the dep array and store it in a ref so parent re-renders don't reschedule the grid.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-10 · [M/inefficiency] Sidebar fetches ~25 proxied cluster list endpoints (incl. all secrets/configmaps/pods) on 15-30s intervals just for count badges
- **Loc:** `frontend/src/components/layout/sidebar.tsx:271` · CONFIRMED
- **Failure:** Every operator viewing ANY cluster sub-page (Overview, a single Pod, YAML tab, etc.) causes the sidebar alone to issue ~25 concurrent list requests through the single per-cluster agent tunnel + apiserver proxy — including listing every Secret and ConfigMap in the cluster — and to repeat them every 15-30s and on each window focus, regardless of which resource page is actually open. On a large cluster this is sustained, largely wasted load on the tunnel and member apiserver, only to render count numbers next to nav items.
- **Fix:** Fetch counts from a single lightweight aggregated endpoint, or lazily fetch a group's counts only when that nav group is expanded, and drop the refetchInterval on count queries (counts change slowly and can piggyback on SSE cluster events).
- **Verify:** regression test reproducing the failure; standard gate.

### S4-11 · [M/correctness] YAML editor silently discards in-progress edits on any background refetch
- **Loc:** `frontend/src/components/ui/yaml-view-dialog.tsx:89` · CONFIRMED
- **Failure:** An operator opens the YAML tab of a Deployment (in ResourceDetail or the modal), switches to Edit, and types changes. They alt-tab to another window/app and return; refetchOnWindowFocus refetches the live object (its resourceVersion/status/managedFields timestamps have changed, so the YAML string differs), the [yaml] effect fires, and setEditedYaml(yaml) overwrites the entire editor with the server copy — all in-progress edits are lost with no warning. A k8s.all cache invalidation (from any other mutation via useK8sApplyYaml/useK8sDelete onSuccess) triggers the same overwrite.
- **Fix:** Only seed the editor from the fetched YAML when NOT in edit mode (guard the effect with `if (!editMode)`), or track a dirty flag and skip the sync once the user has typed. Optionally set refetchOnWindowFocus:false on useK8sGetYaml.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-12 · [M/leak] pod-terminal opens the exec WebSocket inside an un-cancellable async ticket promise; unmount (or effect re-run) before the ticket resolves leaks the socket and its server-side exec stream
- **Loc:** `frontend/src/components/workloads/pod-terminal.tsx:83` · CONFIRMED
- **Failure:** User opens a pod terminal and navigates away (or React re-runs the effect on container change / StrictMode) before the stream-ticket XHR resolves. Cleanup runs while wsRef.current is still null, so nothing is closed. The pending promise then resolves and calls `new WebSocket(...)`, assigning wsRef.current on an unmounted component whose cleanup already ran — the socket is never closed. Server-side this holds an open exec WS -> a live tunnel exec Stream + agent SPDY exec session that only tears down when the browser eventually GCs the orphaned socket, consuming one of the shared 256 per-agent s
- **Fix:** Capture a `let cancelled = false` in the effect, set it in the returned cleanup, and inside the .then do `if (cancelled) { ws.close(); return; }` before wiring handlers (and always assign to a local that the cleanup can close), matching the guard pattern already used in live-events.ts.
- **Verify:** regression test reproducing the failure; standard gate.


---

## H-db (wave 2)
**Files:** `internal/db/queries/apiserver_audit_events.sql`

### S4-03 · [L/inefficiency] apiserver_audit_events retention prune is a full sequential scan (no index on event_time)
- **Loc:** `internal/db/queries/apiserver_audit_events.sql:62` · CONFIRMED
- **Failure:** This table stores one row per kube-apiserver request across the ENTIRE fleet (append-only, self-described as 'unbounded, one row per apiserver request, fleet wide'). At fleet scale it reaches millions of rows in days. HandleApiserverAuditRetention (internal/worker/tasks/apiserver_audit_retention.go:52) runs the DELETE daily; each run seq-scans the full table to delete ~1 day's worth, leaving a large dead-tuple/bloat burden for autovacuum. The sibling audit_log table (migration 001/025) was made a PARTITIONED table precisely so retention drops whole partitions cheaply — this table got neither p
- **Fix:** Add `CREATE INDEX idx_apiserver_audit_events_event_time ON apiserver_audit_events (event_time)` in a new migration so the prune is an index range scan, or (better, matching audit_log) convert the table to monthly RANGE partitions on event_time and reap by DROP PARTITION.
- **Verify:** regression test reproducing the failure; standard gate.


---

## I-perf-worker (wave 2)
**Files:** `internal/agent/mirror_subscriber.go`, `internal/dashboards/prom.go`, `internal/handler/argocd.go`, `internal/maintenance/window.go`, `internal/metrics/publisher.go`, `internal/worker/tasks/alert_evaluation.go`

### S4-02 · [L/race] mirror subscriber dynamic-GVR retry leaks a goroutine per attempt and double-closes innerStop → panic on shutdown
- **Loc:** `internal/agent/mirror_subscriber.go:341` · CONFIRMED
- **Failure:** On a cluster where trivy-operator/gateway-api CRDs aren't installed yet, each retry (~every 30s) leaves the line-306 goroutine blocked on its select (neither ctx nor parentStop fired) — so one goroutine leaks per attempt (~2880/day for one absent GVR). Worse, line 341 already closed that iteration's innerStop, so when the agent finally shuts down and `Run`'s `defer close(stopCh)` fires parentStop, every leaked goroutine wakes and runs `close(innerStop)` on an already-closed channel → `panic: close of closed channel`, crashing the agent mid-shutdown (aborting decommission acks / audit-checkpoin
- **Fix:** Don't double-own innerStop. Either drop the explicit `close(innerStop)` at line 341 and instead use a per-iteration cancel that the watcher goroutine selects on, or scope the line-306 goroutine to the iteration (create it with its own done channel closed via a deferred func at the end of each loop i
- **Verify:** regression test reproducing the failure; standard gate.

### S4-06 · [L/inefficiency] maintenance IsActive walks 366 days of cron fires on every gated mutation; sweep-3's permitted-OR refactor now runs it for ALL matching windows
- **Loc:** `internal/maintenance/window.go:337` · CONFIRMED
- **Failure:** An operator configures a frequent-cron maintenance window (e.g. `*/5 * * * *` or `* * * * *`) scoped to an op type. Every gated mutation then spends the ~100k-500k-iteration backward cron walk (times the number of matching permitted windows) purely to decide gating, adding tens of ms of CPU to each destructive API call and to NextClose (which repeats the same walk at 405-412).
- **Fix:** Compute the most-recent fire cheaply: step back by duration+one cron period (or use a small fixed lookback like 2×period + duration) instead of 366 days, and/or memoize per-(window,minute) activeness in the 30s cache rather than recomputing on every IsBlocked.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-20 · [M/inefficiency] pollRunningOperations makes up to 50 serial upstream ArgoCD calls in the single reconciler goroutine, stalling all operation processing when an instance is unreachable
- **Loc:** `internal/handler/argocd.go:1537` · CONFIRMED
- **Failure:** Several running sync ops target ArgoCD instances that are slow/unreachable. Each GetApp blocks for the full client timeout (~10s). 50 such ops = ~500s of blocking in the ONE reconciler goroutine, during which no new pending operations are claimed (opTicker starves) and no health checks run — user-triggered syncs sit unprocessed for minutes.
- **Fix:** Dispatch the per-op poll work concurrently with a bounded worker pool (mirror dispatchClaimed/helmConcurrency used by processPendingOperations), or move poll onto its own goroutine so a stuck instance can't starve pending-op claiming.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-22 · [M/inefficiency] Metrics publisher fans out cluster snapshots serially with a 4s per-cluster blocking timeout
- **Loc:** `internal/metrics/publisher.go:214` · CONFIRMED
- **Failure:** Every 30s each active cluster's snapshot expires, so the publisher pays a synchronous tunnel round-trip (up to the 4s timeout for a stalled/half-disconnected agent) one cluster at a time. A cluster that is still 'active' but whose agent stalls blocks the whole loop for 4s. At a few hundred+ active clusters (or a partition that stalls many agents at once), one serial pass can take minutes — far longer than the 10s/30s cadence — so the loop can never keep the cache warm and most clusters permanently publish stale/zero CPU/mem/pod metrics.
- **Fix:** Fan out per-cluster metrics.Get across a bounded worker pool (e.g. errgroup with SetLimit) instead of a serial loop, and/or skip clusters whose last_heartbeat is already stale so a dead agent never costs 4s of the pass.
- **Verify:** regression test reproducing the failure; standard gate.

### S4-27 · [M/inefficiency] alert_evaluation re-lists the whole fleet and re-fetches per-cluster health once per global rule each tick
- **Loc:** `internal/worker/tasks/alert_evaluation.go:414` · CONFIRMED
- **Failure:** With G global threshold/anomaly rules and C clusters, a single evaluation tick does G full-fleet ListClusters paginations plus G×C GetClusterHealthStatus point queries — all serial. At 1000 clusters and 20 global rules that is ~20,000 redundant health reads plus 20 full-fleet scans every tick, even though the cluster rows and health snapshots are identical across all rules within the tick. Tick duration grows linearly with rule_count×cluster_count and the worker falls behind.
- **Fix:** Fetch the fleet cluster list + a cluster_id→health map ONCE per tick (like the silence hoist) and pass them into evaluateRule, or add a single batched ListClusterHealthStatusByIDs. This collapses G×C+G queries to O(C).
- **Verify:** regression test reproducing the failure; standard gate.

### S4-28 · [S/leak] Dashboard Prometheus query cache never evicts expired entries (monotonically growing map)
- **Loc:** `internal/dashboards/prom.go:214` · CONFIRMED
- **Failure:** Every distinct (datasource, widget query, duration|step) tuple ever rendered stays resident forever, even after the dashboard/widget is deleted or the operator changes the time-range picker (1h/6h/24h/7d each mint a new key). On a long-lived control plane the map grows monotonically and never shrinks, holding whole PromMatrix payloads (per-series sample slices) past their 30s TTL — a slow but permanent heap creep that a process restart is the only reclaim for.
- **Fix:** Delete the entry on an expired-read (`delete(c.entries, k)` in getMatrix/getStat when `time.Now().After(e.expires)`), or add a lightweight sweeper: on putMatrix/putStat, if len(entries) crosses a threshold, walk and drop expired keys. A TTL cache without eviction is just a leak with a staleness chec
- **Verify:** regression test reproducing the failure; standard gate.
