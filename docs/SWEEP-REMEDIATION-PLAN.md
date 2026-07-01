# Astronomer Remediation Plan — Verified Sweep Findings (2026-07-01)

> Source: grounded 10-analyst ultracode sweep + adversarial per-finding verification. **41 CONFIRMED findings** (false positives already dropped). Each entry cites a real `file:line` and a traced failure path. This plan is the execution contract for the follow-up ultracode remediation run.

## How to read this

- **Severity**: L (large/systemic), M (moderate), S (small/localized).
- **Batch**: execution wave — 1 = critical security + HA correctness, 2 = silent-failure stubs & data integrity, 3 = parity/UX, 4 = efficiency.
- **Workstream (WS)**: findings are grouped so each source file is owned by exactly ONE workstream — this lets the remediation fan out in parallel with no edit conflicts.
- Every fix MUST land behind the existing test gate: `go build ./... && go test ./...` + `gofmt` for Go, `tsc --noEmit` + `eslint` for frontend. Security-sensitive behavioral changes ship with a regression test.

## Severity / batch summary

| Batch | Theme | Count |
|---|---|---|
| 1 | Critical security + HA correctness | 4 |
| 2 | Silent-failure stubs & data integrity | 27 |
| 3 | Parity / UX | 4 |
| 4 | Efficiency | 6 |

| WS | Area | Files owned | Findings |
|---|---|---|---|
| A | Access control, sessions & authz scoping | 5 | 6 |
| B | Tunnel & streaming HA | 2 | 2 |
| C | Alert evaluation correctness & scale | 2 | 4 |
| D | Worker leader-gating & sweeps | 7 | 8 |
| E | Catalog / Helm / GitOps delivery | 3 | 6 |
| F | Backups, DR & decommission integrity | 2 | 3 |
| G | Frontend correctness & UX | 4 | 4 |
| H | Observability & cluster APIs | 7 | 8 |

## Cross-cutting execution rules

1. **No shared-file edits across workstreams** — the grouping guarantees this; keep it.
2. **Feature-flag anything behaviorally risky.** The RBAC escalation guard (F01) changes who can create bindings — ship it enforcing (it's a security fix) but with a clear 403 message and a regression test proving a non-privileged caller is blocked and a superuser/self-holding caller is allowed.
3. **Leader-gating fixes must not change single-replica behavior** — `runPeriodicTaskWithLeader` is a no-op-passthrough when not HA.
4. **Pagination/scale fixes must preserve existing response envelopes** (don't break the frontend's `data`/`pagination` shape).
5. **DB changes** go through a new migration (next number after 126); never mutate a prior migration. sqlc regen + querier/fakes updated.
6. Each finding below has an explicit **Verify** step — that is the acceptance test for the fix.


---

## Workstream A — Access control, sessions & authz scoping

**Files owned:** `internal/db/queries/rbac.sql`, `internal/handler/auth.go`, `internal/handler/password_reset.go`, `internal/handler/rbac.go`, `internal/server/routes.go`

### F02 · [L/security] Role-binding creation has no privilege-escalation guard — rbac:create self-escalates to full admin

- **Location:** `internal/handler/rbac.go:500` · **Batch 1** · verdict CONFIRMED
- **Failure:** A non-superuser holding a custom global role that grants ResourceRBAC+VerbCreate (the only gate on POST /rbac/global-role-bindings/, per routes_rbac_audit_agents.go:44) POSTs {user_id: <self>, role_id: <a role whose rules are [{resource:
- **Fix:** Before persisting any binding, load the caller's own bindings and reject (403) unless every Rule in the target role is already satisfied by the caller's effective permissions at the binding's scope (or the caller is a superuser) — i.e. port Kubernetes' 'you cannot grant permissions you do not hold' escalation check into parseBindingRefs/CreateXRoleBinding.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F09 · [M/security] Group-scoped role bindings are stored/indexed but never evaluated at authorization time (silent no-op grant)

- **Location:** `internal/db/queries/rbac.sql:159` · **Batch 2** · verdict CONFIRMED
- **Failure:** An admin POSTs /api/v1/rbac/global-role-bindings (or cluster/project variants) with {
- **Fix:** Either (a) reject binding creation with an empty user_id until group bindings are real (return 400 in parseBindingRefs), or (b) actually expand groups: resolve the caller's IdP groups (user_idp_groups snapshot / identity_group_mappings) and add UNION branches to ListUserBindingsWithRoles that match *_role_bindings.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F12 · [M/security] Logout revokes only the access-token JTI; the refresh token stays valid for 7 days and can re-mint sessions

- **Location:** `internal/handler/auth.go:856` · **Batch 2** · verdict CONFIRMED
- **Failure:** Logout parses the caller's access token from the Authorization header and denylists only that JTI (claims.ID); it never bumps the per-user tokens_invalidated_at cutoff and never revokes the refresh token (whose JTI it doesn't possess). An attacker who captured or retained the 7-day refresh token (e.g. from a shared machine, XSS-exfil, or network capture) POSTs it to /auth/refresh (auth.go:772) after the victim logs out — ValidateToken only checks the refresh JTI against the deny list (not present) and IsActive (still true), so it mints a fresh access+refresh pair. Logout therefore does not actually terminate the session.
- **Fix:** In Logout, when revocation is wired, also call InvalidateAllTokens (bump tokens_invalidated_at to now) for claims.UserID — the same mechanism force-logout/password-reset use — so every token issued before logout (including the refresh token) is rejected by checkRevocations.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F21 · [M/stub] Group-scoped role bindings created via the RBAC API are a silent no-op (engine never resolves them)

- **Location:** `internal/handler/rbac.go:511` · **Batch 2** · verdict CONFIRMED
- **Failure:** An operator POSTs a binding with {group: 
- **Fix:** Either reject group-only bindings at the API (400 'group bindings are managed via identity group mappings') until group→user expansion is implemented, or extend GetUserBindings/ListUserBindingsWithRoles to also resolve bindings whose group matches the user's current group memberships.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F24 · [M/parity] RBAC has no namespace/project scoping on cluster resource reads — access is cluster-all-or-nothing, missing Rancher's core project multi-tenancy

- **Location:** `internal/server/routes.go:1168` · **Batch 3** · verdict CONFIRMED
- **Failure:** A user given a project/namespace-scoped binding (Namespace set, or ProjectID set without a project_id URL param) hits GET /clusters/{id}/pods/: bindingApplies() returns false (project binding never matches a cluster-scoped route, namespace never enters scope) so they get 403. The only way to let them list pods is a cluster-wide read binding, which then returns EVERY namespace's pods/secrets. There is no 'list only my namespaces' tier. Rancher's Project Member sees only their project's namespaces.
- **Fix:** Extract namespace into permissionScopeIDs and pass it to CheckPermission so namespace-scoped bindings apply; additionally filter list-handler results (ListPods/ListWorkloads/ListEvents) to the caller's authorized namespaces via the project→namespace mapping, instead of returning the whole cluster.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F39 · [S/security] Password-reset completion bumps the DB token cutoff but never flushes the JWT validation cache — 30s stale-session window

- **Location:** `internal/handler/password_reset.go:225` · **Batch 2** · verdict CONFIRMED
- **Failure:** A user (often reacting to account compromise) completes a password reset. The handler sets tokens_invalidated_at but, unlike the force-logout (users.go:385) and SCIM deactivate (revokeUserSessions -> jwt.InvalidateCache) paths, it does NOT call h.jwt.InvalidateCache(). JWTManager.checkRevocations short-circuits on the positive-result cache (cacheHit, jwt.go:333) for up to JWTValidationCacheTTL (30s), so any attacker session whose JTI was validated within the last 30s continues to pass every authenticated request for up to 30s after the reset, silently bypassing the freshly-set cutoff.
- **Fix:** After InvalidateAllTokens in PasswordResetComplete, call h.jwt.InvalidateCache() (nil-safe), matching the force-logout and SCIM revocation paths, so the new cutoff takes effect on the very next request.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream B — Tunnel & streaming HA

**Files owned:** `internal/auth/stream_tickets.go`, `internal/tunnel/handler.go`

### F01 · [L/bug] Browser exec/logs/shell WebSocket auth breaks under the default 2-replica deployment: stream tickets live in a per-pod in-memory single-use map

- **Location:** `internal/auth/stream_tickets.go:41` · **Batch 1** · verdict CONFIRMED
- **Failure:** deploy/chart/values.yaml sets server.replicaCount:2 and the server-service.yaml/ingress.yaml have NO sessionAffinity, so the pod that serves POST /streams/tickets/ and the pod nginx pins the WebSocket to are independently load-balanced. StreamTicketStore is a single in-memory instance per pod (server.go:1003 `auth.NewStreamTicketStore`) whose Validate() deletes on first use. Case A: browser mints a ?ticket= on pod A, WS lands on pod B -> B's Validate misses the key -> HTTP 401, exec/logs/shell terminal fails (~50% of opens with 2 replicas). Case B is deterministic even with affinity: HandleExec runs authenticateStreamRequest FIRST (exec_consumer.go:129, validates+deletes the ticket on the front pod) and THEN ForwardWSToOwnerPod (exec_consumer.go:151); the owner pod re-runs HandleExec->authenticateStreamRequest against ITS store, which never had the ticket -> 401. So whenever the tunnel-o
- **Fix:** Back stream tickets with the shared store the locator already uses (Redis) so any pod can validate, OR validate the ticket only once on the front pod and pass a signed internal assertion (like InternalForwardedUserHeader) to the owner pod instead of re-validating the consumed ticket; at minimum move ForwardWSToOwnerPod before authenticateStreamRequest and have the owner pod trust an internal-PSK-signed user id rather than re-redeeming the ticket.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F25 · [M/correctness] routeToStream silently drops tunnel frames when the 64-slot stream channel is full, corrupting chunked responses and losing watch events

- **Location:** `internal/tunnel/handler.go:552` · **Batch 2** · verdict CONFIRMED
- **Failure:** Stream.DataCh is buffered at 64 (stream.go:51). A large unary K8s response is chunked at 256KB/frame (protocol.K8sChunkSizeBytes) — a 64MB list is ~256 data frames. The single agent read loop routes all frames via this non-blocking send; if the consumer (reassembleK8sResponse, which json.Unmarshals + base64-decodes each frame) falls behind a burst, frames past 64 are silently dropped. The reassembler has NO gap detection: it keeps accumulating and, on the End frame, returns out.Body of the surviving chunks with StatusCode 200 -> client receives HTTP 200 with a truncated/corrupt body and no error. On the watch path a dropped 16KB frame silently loses a MODIFIED/DELETED event for the browser and for ArgoCD's live-state controller, producing stale UI / incorrect sync state.
- **Fix:** Detect loss instead of hiding it: carry a per-frame sequence number and fail the stream (502) on a gap for unary responses; for watches, close the stream on overflow so the client reconnects and re-lists rather than silently missing events. Alternatively give unary reassembly a bounded blocking send with a deadline instead of drop-on-full.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream C — Alert evaluation correctness & scale

**Files owned:** `internal/worker/tasks/alert_evaluation.go`, `internal/worker/tasks/alert_evaluation_anomaly.go`

### F26 · [M/correctness] Global alert rules silently evaluate at most 500 clusters — clusters 501+ never fire or resolve alerts

- **Location:** `internal/worker/tasks/alert_evaluation.go:333` · **Batch 2** · verdict CONFIRMED
- **Failure:** For a global (cluster-less) alert rule, evaluateRule fetches clusters with a single Limit:500, Offset:0 query and no pagination loop (contrast argocd_auto_register_cluster.go which pages at 500). On a fleet larger than 500 clusters — the exact scale a multi-cluster fleet plane targets — clusters beyond the 500th are invisible to every global rule: a global 'cluster disconnected'/'zero nodes' rule never pages for those clusters, and, worse, any already-firing event on a cluster that scrolls out of the first 500 is never resolved. alertRulesForEvaluation (line 182) has the same 500-rule cap, so the 501st alert rule is never evaluated either.
- **Fix:** Page through ListClusters (loop incrementing Offset until a short page) for the global-rule fan-out, and page ListAlertRules the same way, mirroring the argoCDAutoRegisterSweepPageSize loop.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F27 · [M/inefficiency] alert_evaluation re-lists all silences once per (rule,cluster) — N+1 full-table scan every 60s

- **Location:** `internal/worker/tasks/alert_evaluation.go:400` · **Batch 4** · verdict CONFIRMED
- **Failure:** processRuleEvaluation calls activeSilenceForRule (line 207) for every evaluation, and the evaluation loop (lines 81-85) runs one evaluation per cluster for a global rule. activeSilenceForRule does a fresh ListAlertSilences(Limit 500) each call. So for R global rules over C clusters the evaluator issues R*C ListAlertSilences queries every 60s tick (e.g. 20 global rules x 400 clusters = 8000 silence-list queries/min), on top of a per-cluster GetClusterHealthStatus and GetClusterMonitoringContext. The silence set is identical for the whole tick, so this is pure repeated work on a hot periodic path.
- **Fix:** Fetch the active-silence list once at the top of the tick (or once per rule) and pass it into processRuleEvaluation, filtering in-memory by rule/cluster instead of re-querying per cluster.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F28 · [M/bug] Global anomaly alert rule collapses to first triggering cluster; recovered clusters never resolve

- **Location:** `internal/worker/tasks/alert_evaluation_anomaly.go:74` · **Batch 2** · verdict CONFIRMED
- **Failure:** A global (unscoped) rule_kind='anomaly' rule covering clusters A and B: A fired an anomaly event last tick, then A recovers while B goes anomalous. EvaluateAnomalyRuleWith returns early on the first triggering cluster (B) as ONE eval, so evaluateRule yields a single ruleClusterEval for B (alert_evaluation.go:301). processRuleEvaluation therefore only resolves/dedups B's events; A's still-'firing' event is never revisited and stays firing indefinitely as long as any other cluster is anomalous. Threshold global rules were specifically reworked to fan out one eval per cluster (see the contract comment at alert_evaluation.go:281-284 'every recovered cluster resolves within the same tick'); the anomaly path silently violates it.
- **Fix:** Have the anomaly evaluator return one ruleClusterEval per cluster (like the threshold fan-out in evaluateRule) instead of returning on the first triggered cluster; change evaluateAnomalyRule to yield a slice and append each cluster's result so processRuleEvaluation can resolve recovered clusters independently.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F40 · [S/correctness] PromQL alert rules silently rewrite a user threshold of 0 to a hardcoded default

- **Location:** `internal/worker/tasks/alert_evaluation.go:745` · **Batch 2** · verdict CONFIRMED
- **Failure:** An operator creates a PromQL-backed rule meaning 'fire when the query value > 0' (e.g. count of failing pods) and sets threshold=0, comparison=gt. The evaluator overwrites threshold to 1, so a value of 1 (one failing pod) no longer trips 'gt 1' and the alert never fires. The non-PromQL evaluateClusterRule treats threshold 0 differently (it just skips the threshold check, line 442 'if threshold > 0'), so the two evaluation paths disagree on what threshold=0 means.
- **Fix:** Distinguish 'unset' from 'explicit 0' — check whether the threshold key is present in config before substituting the default (e.g. _, ok := config[
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream D — Worker leader-gating & sweeps

**Files owned:** `internal/worker/tasks/cluster_decommission.go`, `internal/worker/tasks/cluster_snapshot_poll.go`, `internal/worker/tasks/control_plane_snapshot.go`, `internal/worker/tasks/gitops_sync.go`, `internal/worker/tasks/health_check.go`, `internal/worker/tasks/kubectl_session_reap.go`, `internal/worker/worker.go`

### F03 · [L/bug] Decommission delete_dependents cleans only ~10 of ~50 cluster_id tables; tombstone (not DELETE) means the ON DELETE CASCADE never fires, so orphaned schedules keep firing against dead clusters

- **Location:** `internal/worker/tasks/cluster_decommission.go:768` · **Batch 1** · verdict CONFIRMED
- **Failure:** ~55 tables carry a cluster_id FK to clusters, almost all ON DELETE CASCADE (agent_lifecycle_operations, cluster_snapshot_schedules, cluster_snapshots, control_plane_snapshots, gitops_registered_clusters, argocd_instances, native_rbac_rules, image_vulnerability_reports, mirrored_*, deferred_operations, projects, fleet_operation_targets, ...). phaseDeleteDependents only deletes ~10 of them (+3 token tables in phase 2 + argocd_managed_clusters). Because phaseTombstoneCluster sets decommissioned_at instead of DELETEing the row, the CASCADE never runs, so every other table's rows are orphaned permanently. Concretely: cluster_snapshot_schedules is never cleaned, and the snapshot scheduler (cluster_snapshot_poll.go:361 -> ListEnabledSnapshotSchedules, cluster_snapshots.sql:143 `WHERE enabled = true` with no decommissioned filter/JOIN) keeps evaluating the cron and creating snapshot jobs for the
- **Fix:** Either (a) hard-DELETE the cluster row in the final phase (letting CASCADE clean everything) after archiving audit, or (b) drive phaseDeleteDependents from a schema-derived list of cluster_id-referencing tables and add the missing DeleteBy...ByCluster queries (snapshot schedules, gitops_registered_clusters, agent_lifecycle_operations, native_rbac_rules, deferred_operations, monitoring/logging configs, etc.). Also add `AND c.decommissioned_at IS NULL` guards to worker due-queries like ListEnabled
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F04 · [L/bug] cluster_snapshot:dispatch_scheduled runs on every replica (no leader gate) → duplicate scheduled Velero backups

- **Location:** `internal/worker/tasks/cluster_snapshot_poll.go:356` · **Batch 1** · verdict CONFIRMED
- **Failure:** None of the three cluster_snapshot handlers (poll:193, dispatch_scheduled:356, cleanup_expired:572) wrap their body in runPeriodicTaskWithLeader, unlike ~40 other periodic handlers (gitops_sync, task_outbox_dispatch, alert_evaluation, etc.). Because every astronomer-worker pod starts its own asynq.Scheduler unconditionally (cmd/worker/main.go:260 `s.Start()`), an HA deployment with N worker replicas enqueues cluster_snapshot:dispatch_scheduled N times each minute, and N workers run it concurrently. scheduleIsDue reads last_run_at and MarkSnapshotScheduleRan writes it (read-before-write with no row lock), so all N replicas see the same schedule as 'due' and each calls fireScheduledSnapshot → N CreateClusterSnapshot rows + N PostBackup Velero CRs for one scheduled tick (or unique-name collisions marking N-1 rows FailedValidation when they land in the same second). Result: duplicate/errorin
- **Fix:** Wrap each of the three handler bodies in runPeriodicTaskWithLeader(ctx, <type>, func() error { ... }) exactly like HandleGitOpsSync/HandleTaskOutboxDispatch do, so only the advisory-lock holder dispatches per tick.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F29 · [M/stub] cluster_snapshots_in_flight gauge is documented as poller-maintained but nothing ever sets it (dead DR metric)

- **Location:** `internal/worker/tasks/cluster_snapshot_poll.go:199` · **Batch 2** · verdict CONFIRMED
- **Failure:** The metric doc (cluster_snapshots_metrics.go:11-13) states 
- **Fix:** In HandleClusterSnapshotPoll, aggregate the non-terminal rows per cluster_id (they are already listed via ListPendingClusterSnapshots) and call SetInFlightSnapshotGauge(clusterID, count) for each, resetting clusters that dropped to zero; or delete the gauge if it is not wanted.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F30 · [M/stub] GitOps ClusterRegistration spec.registries and spec.toolPresets are parsed then silently dropped (half-built feature)

- **Location:** `internal/worker/tasks/gitops_sync.go:314` · **Batch 2** · verdict CONFIRMED
- **Failure:** An operator commits a ClusterRegistration YAML with `spec.registries: [harbor]` and `spec.toolPresets: [cert-manager-prod]`. Parse accepts them, Apply copies them into Result.Registries/ToolPresets (apply.go:128-129), but SyncSource only ever reads applied.Created/Updated/ClusterID/ClusterName — it never enqueues cluster_registry:apply or tool installs. The registries and tool presets declared in git are reconciled into nothing; the operator sees a successful sync and assumes the presets/registries were applied.
- **Fix:** After a successful Apply in SyncSource, enqueue the registry-apply and tool-install tasks for applied.Registries/applied.ToolPresets (the doc comment already promises this), or remove the fields + comment if the feature is out of scope.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F31 · [M/bug] Cluster status sweepers hard-cap at 500 clusters with Offset:0 and no pagination — in fleets larger than 500 the oldest clusters' status is never updated

- **Location:** `internal/worker/tasks/health_check.go:108` · **Batch 2** · verdict CONFIRMED
- **Failure:** ListClusters is `ORDER BY created_at DESC LIMIT 500 OFFSET 0` (clusters.sql). Both the authoritative status writer (metrics/publisher.go, which owns the active<->disconnected transition) and the backstop health-check sweep fetch only the newest 500 non-decommissioned clusters, with no loop over offsets. In a deployment with e.g. 800 imported clusters (the product explicitly targets 50+ / 'a few thousand'), the 300 oldest clusters are never re-evaluated: when their agents disconnect, clusters.status stays frozen at 'active', the UI shows them healthy, and disconnection is never detected.
- **Fix:** Page through all clusters (loop incrementing Offset until a short page, or use keyset pagination on created_at/id) in both metrics/publisher.go and health_check.go, or raise/remove the cap for these full-fleet sweeps.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F32 · [M/bug] kubectl:session_reap doc claims leader-election gating that does not exist; reaper runs on every replica

- **Location:** `internal/worker/tasks/kubectl_session_reap.go:60` · **Batch 2** · verdict CONFIRMED
- **Failure:** The comment asserts 'The leader-election wrapper around it ensures only one replica fires per tick,' but the handler calls kubectl.Reap directly with no runPeriodicTaskWithLeader wrapper, and instrumentTask (job_metrics.go:76) adds only tracing/metrics — no leader gate. On an N-replica worker deployment the reaper's idle/hard-cap teardown and the orphan-pod sweep (which lists+DELETEs kube-system pods labelled astronomer.io/component=kubectl-shell over the tunnel for every cluster with active sessions) run N times concurrently every 60s, multiplying tunnel/K8s API load and racing teardown DELETEs against live sessions. The guarantee the code documents is simply not implemented.
- **Fix:** Wrap the kubectl.Reap call in runPeriodicTaskWithLeader(ctx, KubectlSessionReapType, func() error { return kubectl.Reap(ctx, kubectlReapDeps.Deps) }) to make the comment true, matching the other periodic reapers.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F33 · [M/stub] chart_recommendations:recompute handler is wired but never scheduled or enqueued — nightly recompute never runs

- **Location:** `internal/worker/worker.go:307` · **Batch 2** · verdict CONFIRMED
- **Failure:** The handler for chart_recommendations:recompute is registered on the mux and has a full RuntimeQuerier surface (ListChartRatingsByChart, UpsertChartRatingAggregate, TruncateChartCoInstallation, etc.), but grep across internal/ and cmd/ (excluding tests) shows NO scheduler entry in RegisterPeriodicTasks and NO Enqueue site — only the mux registration and the constructor used by tests. The 'nightly chart-rating aggregate + co-installation matrix recompute' (worker.go:134) therefore never fires in production: chart_rating_aggregate and chart_co_installations are never rebuilt, so the recommendations UI shows the seed/empty state forever no matter how many ratings users submit.
- **Fix:** Add a scheduler entry to RegisterPeriodicTasks, e.g. {
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F41 · [S/bug] Control-plane snapshot reconcile advances OFFSET while removing rows from the filtered set, skipping still-running snapshots

- **Location:** `internal/worker/tasks/control_plane_snapshot.go:206` · **Batch 2** · verdict CONFIRMED
- **Failure:** With >200 rows in status='running', page 1 (offset 0) returns rows[0:200]; the loop marks them terminal, so they leave the status='running' result set. The next query uses OFFSET 200 against the now-shrunken set, skipping the ~200 still-running rows that shifted down to positions 0..N. Those rows are not reconciled this sweep tick and only get picked up on a later tick when they land back in the first page — reconciliation of large in-flight batches is silently delayed/starved because the offset is computed against a mutating filter.
- **Fix:** Don't paginate with a growing OFFSET over a set you mutate. Either always query OFFSET 0 in a loop (processed rows drop out, so the set shrinks toward empty), or keyset-paginate by created_at/id of the last unresolved row.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream E — Catalog / Helm / GitOps delivery

**Files owned:** `internal/handler/argocd.go`, `internal/handler/catalog.go`, `internal/handler/tools.go`

### F14 · [M/bug] Private HTTP Helm repositories cannot be synced — index fetch ignores repo AuthType/AuthConfig

- **Location:** `internal/handler/catalog.go:528` · **Batch 2** · verdict CONFIRMED
- **Failure:** Admin adds a classic (non-OCI) Helm repo with auth_type=basic + credentials in auth_config (a private ChartMuseum/Artifactory). SyncRepo -> fetchAndIngestRepoIndex GETs `<url>/index.yaml` with no Authorization header, so the server returns 401/403 and the sync fails with 
- **Fix:** Parse repo.AuthType/AuthConfig in fetchAndIngestRepoIndex (and TestRepoConnection's index branch at line 1229) and set BasicAuth / bearer header the same way the OCI branch does before issuing the request.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F15 · [M/security] Catalog chart UPGRADE never resolves ${vault://} markers — literal placeholder deployed as the real value

- **Location:** `internal/handler/catalog.go:1112` · **Batch 2** · verdict CONFIRMED
- **Failure:** Operator installs a chart with `password: ${vault://db/pw}` (Install resolves it at catalog.go:943, works). Later they edit values via UpgradeInstalledChart; the handler puts the RAW req.ValuesOverride in the envelope, and the reconciler's sendHelm (catalog.go:1806) only yaml-unmarshals env.ValuesOverride and ships it to helm — no vault resolution anywhere on the upgrade path. Helm receives the literal string `${vault://db/pw}` as the password, breaking the app or persisting a bogus placeholder secret into the cluster. Install and tools.Upgrade (tools.go:515) both resolve; catalog Upgrade is the lone gap.
- **Fix:** In UpgradeInstalledChart call `vaultResolveBlob(r.Context(), h.vaultResolver, uuid.Nil, req.ValuesOverride)` and put the resolved blob in the envelope (mirroring CreateInstallation at 943), keeping the raw blob only in the persisted installed_charts row.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F22 · [M/correctness] Distribution install-values

- **Location:** `internal/handler/tools.go:923` · **Batch 2** · verdict CONFIRMED
- **Failure:** Installing prometheus-node-exporter (or fluent-bit) on an OpenShift cluster: distributionInstallValues prepends `securityContext:\   privileged: true\   runAsUser: 0`. If the chosen preset or the user's values_override sets ANY `securityContext:` (e.g. `fsGroup: 1000`), the concatenated YAML has two top-level `securityContext` keys; sigs.k8s.io/yaml keeps only the later one, so `privileged: true` / `runAsUser: 0` are dropped. The node-level collector then fails to schedule under OpenShift's default SCC — exactly the failure the distribution override existed to prevent. Same for fluent-bit `daemonSetVolumes` on k3s.
- **Fix:** Deep-merge the distribution values with the preset/user values (unmarshal each to map[string]any and recursively merge, distribution as the base), instead of string-concatenating documents; or pass them as separate ordered values files to helm.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F34 · [S/stub] ArgoCD sync_window_override is inert — validated, audited, and stored but never sent upstream

- **Location:** `internal/handler/argocd.go:1352` · **Batch 2** · verdict CONFIRMED
- **Failure:** An operator hits POST /argocd/apps/{id}/sync with sync_window_override=true and a required reason (normalizeSyncRequest enforces the reason at argocd.go:253). The flag is put in the envelope and written to the audit log as `sync_window_override: true`, implying the deny window was overridden. But executeSync builds SyncOptions without it and client.Sync has no override field, so the upstream call is identical to a normal sync; the audit trail records an override that never happened.
- **Fix:** Either implement the override (there is no ArgoCD sync-API field for it, so it would require pre-sync manipulation of the AppProject window or a documented no-op) or remove the flag/validation/audit so operators aren't misled into believing a deny window was bypassed.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F36 · [S/security] Vault-resolved plaintext secrets are persisted in *_operations.payload despite the

- **Location:** `internal/handler/catalog.go:959` · **Batch 2** · verdict CONFIRMED
- **Failure:** CreateInstallation (and tools.go:460 Install, tools.go:526 Upgrade) resolve `${vault://...}` to plaintext, then place the resolved blob in the operation envelope. enqueueOperation json-marshals that envelope into catalog_operations.payload / tool_operations.payload, which is retained after the op completes (rows are marked completed, not deleted). So the very secrets vault indirection was meant to keep out of the DB end up sitting in the operations tables in cleartext, readable by anyone with DB/operations-read access — defeating the point of vault references while the code comment asserts the opposite.
- **Fix:** Keep vault markers in the persisted payload and resolve them at execution time inside the reconciler (sendHelm/sendHelmRaw), or scrub/redact the ValuesOverride field from the payload after the operation reaches a terminal state.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F37 · [S/parity] Helm rollback can only step back exactly one revision and there is no release-history view

- **Location:** `internal/handler/catalog.go:1161` · **Batch 3** · verdict CONFIRMED
- **Failure:** An app upgraded 1→2→3→4 that regressed at revision 2 can only be rolled back to 3; there is no way to target revision 1, and no endpoint surfaces prior revisions/values (helm history). Rancher and plain Helm expose full release history plus rollback-to-any-revision.
- **Fix:** Accept an optional target revision in the rollback request body and pass it through instead of hardcoding current.Revision-1; add a 'helm history' passthrough endpoint so the UI can list revisions before rolling back.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream F — Backups, DR & decommission integrity

**Files owned:** `internal/handler/backups_retention.go`, `internal/handler/cluster_snapshots.go`

### F13 · [M/correctness] Schedule retention attributes backups by name prefix, so one schedule prunes another's backups (data loss)

- **Location:** `internal/handler/backups_retention.go:64` · **Batch 2** · verdict CONFIRMED
- **Failure:** Two schedules exist whose Velero names are prefix-related, e.g. 
- **Fix:** Stop attributing backups by name prefix. Persist the owning schedule_id on each backup row (or match the exact Velero label velero.io/schedule-name == s.VeleroScheduleName) and select prune candidates on that equality instead of HasPrefix.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F35 · [S/inefficiency] enforceScheduleRetention only ever sees the newest 1000 backups, so retention silently stops pruning at scale

- **Location:** `internal/handler/backups_retention.go:32` · **Batch 4** · verdict CONFIRMED
- **Failure:** Once a busy fleet accumulates more than 1000 backup rows total, ListBackups(Limit:1000) returns only the newest 1000 across ALL schedules and statuses. Backups older than that window are never candidates for count-based pruning, so per-schedule 'keep N' retention silently under-prunes and old backups (plus their object-store bytes, since the DeleteBackupRequest is never issued) leak indefinitely. The scan is also O(schedules x 1000) every 15s.
- **Fix:** Query only schedule-owned, completed backups (filtered + ordered in SQL) and paginate to the full set, or push the 'keep newest N per schedule' selection into SQL (window function / lateral) instead of loading a bounded slice into Go.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F38 · [S/correctness] Cross-cluster restore pre-flight only checks that ANY BSL exists, not that the backup's storage location is present on the target

- **Location:** `internal/handler/cluster_snapshots.go:608` · **Batch 2** · verdict CONFIRMED
- **Failure:** Operator restores snapshot from cluster A into cluster B. Cluster B has Velero installed with a BSL, but that BSL points at a different object store/bucket than A's backup. The pre-flight sees len(bsls) > 0 and passes, a cluster_restores row + Velero Restore CR are created, and the restore only fails later (Velero can't locate the backup in B's store) surfacing as a Failed phase in the poller — exactly the outcome the comment claims the 409 prevents. The promised same-store validation is not implemented.
- **Fix:** Compare the source snapshot's storageLocation/bucket against the target's BSLs (e.g. via summarizeBSL bucket/provider) and 409 when none matches, instead of only asserting a BSL exists.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream G — Frontend correctness & UX

**Files owned:** `frontend/src/app/dashboard/clusters/register/[id]/connect/page.tsx`, `frontend/src/app/dashboard/page.tsx`, `frontend/src/components/clusters/registration-timeline.tsx`, `frontend/src/components/workloads/pod-terminal.tsx`

### F05 · [M/bug] Register wizard auto-detect never advances to progress (same SSE payload bug)

- **Location:** `frontend/src/app/dashboard/clusters/register/[id]/connect/page.tsx:88` · **Batch 2** · verdict CONFIRMED
- **Failure:** cluster.connected is published with `{cluster_id}` under Event.data (tunnel/server.go:626->331), but the handler reads `payload.cluster_id` off the LiveEvent wrapper, which is undefined. With auto-detect enabled, when the agent actually connects the guard `undefined === clusterId` is false, so the wizard never navigates to step 3. The operator applies the manifest, the agent connects, and the page just sits on the 'connect' step forever unless they hit the manual Continue button — defeating the auto-detect UX.
- **Fix:** Read `(payload as { data?: { cluster_id?: string } }).data?.cluster_id` and compare against clusterId.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F06 · [M/correctness] Dashboard alert metrics are hard-wired to 0 (wrong response-shape access)

- **Location:** `frontend/src/app/dashboard/page.tsx:54` · **Batch 2** · verdict CONFIRMED
- **Failure:** useAlertEvents -> getAlertEvents returns res.data.data, i.e. an AlertEvent[] ARRAY (see lib/api.ts:1113). The dashboard casts that array to `{ data?: [] }` and reads `.data`, which is undefined on an array, so `alertEvents` is ALWAYS []. Result: with N critical alerts firing, the 'Open Alerts' metric tile shows 0, the 'Firing alerts' Platform-Health row shows 'All clear'/success, and criticalAlerts/warningAlerts are 0 — the overview page tells an operator everything is fine during an active incident, while the topbar bell (which reads the array correctly) shows the real firing count.
- **Fix:** Treat the query result as the array it is: `const alertEvents = alertEventsData ?? []` (typed as AlertEvent[]), matching how topbar.tsx:142 consumes the same hook. Remove the `.data` unwrap.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F07 · [M/bug] Registration-progress timeline never live-updates: SSE handler reads cluster_id off the wrong object

- **Location:** `frontend/src/components/clusters/registration-timeline.tsx:65` · **Batch 2** · verdict CONFIRMED
- **Failure:** live.subscribe's handler receives the full LiveEvent wrapper `{id,type,time,data}` (see live-events.ts dispatch/wrap; and useLiveClusterMetricsMerger correctly reads detail.data.cluster_id). The backend publishes cluster_id INSIDE `data` (tunnel/server.go:331, events/bus.go Event.Data). So `payload.cluster_id` is always undefined, `undefined === clusterId` is always false, and refresh() never fires for step/phase events. There is no polling fallback (the file comment claims one but no interval exists), so the adoption/registration timeline is frozen at its initial fetch and never advances as steps complete — the user watches a stalled progress view until they manually reload.
- **Fix:** Read the nested payload: `const data = (payload as { data?: { cluster_id?: string } }).data;` then compare `data?.cluster_id === clusterId`. Apply to both the .step and .phase subscriptions (lines 63-70). Optionally add a low-frequency polling fallback as the comment promises.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F08 · [M/ux] Switching containers in the exec terminal disconnects the session with no reconnect

- **Location:** `frontend/src/components/workloads/pod-terminal.tsx:169` · **Batch 3** · verdict CONFIRMED
- **Failure:** connectWebSocket() is only invoked from handleReady (once, on WASM-core ready) and the manual Reconnect button. The teardown effect is keyed on selectedContainer, so picking a different container from the dropdown (handleContainerChange -> setSelectedContainer) runs cleanup and closes the WebSocket, but nothing opens a new one for the newly selected container. The terminal drops to 'disconnected' showing 'Press the reconnect button to try again', so the container-switch dropdown appears broken — the user must manually click Reconnect after every container change.
- **Fix:** Add an effect that (re)connects when selectedContainer changes once the core is ready (e.g. call connectWebSocket() in the same effect after teardown, gated on a ready flag), or have handleContainerChange close-then-connect explicitly.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Workstream H — Observability & cluster APIs

**Files owned:** `internal/handler/alerting.go`, `internal/handler/apiserver_audit.go`, `internal/handler/cluster_groups.go`, `internal/handler/clusters.go`, `internal/handler/logging.go`, `internal/handler/monitoring.go`, `internal/handler/workloads.go`

### F10 · [M/inefficiency] N+1 in alerting ListEvents: two DB round-trips per event row

- **Location:** `internal/handler/alerting.go:598` · **Batch 4** · verdict CONFIRMED
- **Failure:** GET /api/v1/alerting/events/ with the default limit=20 issues 1 list query + 20 GetAlertRuleByID + up to 20 GetClusterByID = ~41 DB queries per page; with ?limit=200 it is ~401 queries. The sibling rule-list path was explicitly batched (alertRuleResponses comment at line 1304-1308: 'replacing the ~3-queries-per-rule the single-rule path used to run'), but the event-list path was left on the per-row lookup pattern.
- **Fix:** Mirror alertRuleResponses: collect the distinct RuleIDs and ClusterIDs from the page, bulk-load rule names/severities and cluster names with ListAlertRulesByIDs / ListClustersByIDs (the latter already exists, used at alerting.go:1340), and build responses from in-memory maps instead of calling GetAlertRuleByID/GetClusterByID inside the loop.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F11 · [M/inefficiency] apiserver-audit ingest writes up to 1000 single-row INSERTs per request, and the read path runs a full COUNT(*) over an unbounded, never-pruned table

- **Location:** `internal/handler/apiserver_audit.go:135` · **Batch 4** · verdict CONFIRMED
- **Failure:** The agent batches kube-apiserver audit events (capped at maxIngestEvents=1000) and POSTs them; PersistAuditEvents (apiserver_audit.go:118) loops and issues one INSERT ... ON CONFLICT DO NOTHING per event with no transaction and no multi-row/COPY batching — 1000 sequential DB round-trips per ingest call on a high-frequency path (every cluster streams continuously). Compounding it, apiserver_audit_events has no retention/prune query anywhere in the codebase, so it grows unbounded (one row per apiserver request, fleet-wide, forever), and the List endpoint runs CountApiserverAuditEventsByCluster = `SELECT count(*) ... WHERE cluster_id=$1` (apiserver_audit.go:184) on every page load, an ever-slower full-index scan as the table grows to millions of rows per cluster.
- **Fix:** Batch the ingest into a single multi-row INSERT (or pgx CopyFrom) with ON CONFLICT DO NOTHING; add a retention/prune query (DELETE WHERE event_time < now()-interval, run by a sweeper) plus a config-gated cap; and replace the exact COUNT(*) with an estimate or a cap (e.g. count up to N, or drop total for this high-volume view).
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F16 · [M/correctness] Cluster-group reparent (Update) validates only the moved node's own depth, never its descendants' — a subtree can be pushed past MaxClusterGroupDepth, breaking the tree-depth invariant

- **Location:** `internal/handler/cluster_groups.go:489` · **Batch 2** · verdict CONFIRMED
- **Failure:** MaxClusterGroupDepth=2 (depths 0,1,2 allowed). Build top-level group B(0) -> child C(1) -> grandchild D(2). PUT /cluster-groups/{B}/ with parent_id=A where A is another depth-0 group: newDepth for B = 1 <= 2, so it is accepted. But C is now depth 2 and D is now depth 3, violating the cap. The DB has no CHECK constraint (docstring lines 57-62), so the deeper nodes persist; later resolveParent/depthOf on D hits the `parent chain exceeds maximum depth` ceiling (line 269) and 400s on any create under it.
- **Fix:** In Update, after computing newDepth, walk the moved group's subtree (or compute its current max relative depth) and reject when newDepth + subtreeHeight > MaxClusterGroupDepth.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F17 · [M/inefficiency] Clusters List (main dashboard) issues an ArgoCD N+1: up to 2 DB round-trips per cluster per page

- **Location:** `internal/handler/clusters.go:629` · **Batch 4** · verdict CONFIRMED
- **Failure:** GET /api/v1/clusters/?limit=200 loads a page of clusters, then loops (clusters.go:831 `for _, c := range clusters { resp := h.enrichClusterFromCache(...) }`). enrichClusterFromCache -> enrichClusterArgoCD runs ListArgoCDManagedClustersByCluster(c.ID) for EVERY cluster, plus ListArgoCDApplicationsByManagedClusterTargets for every registered cluster. A 200-cluster page therefore fires ~200-400 sequential DB queries on the primary fleet-list endpoint (which the frontend polls). ListClusters/CountClusters were already deliberately hoisted and inFlightDecommissionSet was batched to avoid exactly this pattern, but the ArgoCD enrichment was left per-cluster.
- **Fix:** Add a batch query ListArgoCDManagedClustersByClusterIDs(cluster_ids uuid[]) (and a batched applications-by-targets) returning all rows for the page in one round-trip, group the rows by cluster_id in Go, and pass the pre-grouped map into enrichClusterArgoCD instead of querying inside the loop.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F18 · [M/ux] Downloaded kubeconfig embeds a literal REPLACE_WITH_API_TOKEN placeholder, so one-click kubeconfig download is non-functional

- **Location:** `internal/handler/clusters.go:2172` · **Batch 3** · verdict CONFIRMED
- **Failure:** User clicks 'Download KubeConfig' (frontend page.tsx downloadKubeconfigFile) and runs kubectl; every request 401s because the token field is the literal string REPLACE_WITH_API_TOKEN. The user must separately navigate to Settings > General, create an API token, copy it, and hand-edit the YAML. Rancher's downloaded kubeconfig authenticates immediately.
- **Fix:** At generation time mint a short-lived, caller-scoped API token (or emit an exec-credential plugin block that calls the token endpoint) and embed it in the users[].user.token field so the downloaded file works out of the box.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F19 · [M/correctness] Cluster-scoped logging list endpoints report a global (unscoped) pagination total

- **Location:** `internal/handler/logging.go:296` · **Batch 2** · verdict CONFIRMED
- **Failure:** Cluster A has 2 outputs, cluster B has 40. GET /api/v1/clusters/A/logging/outputs/ returns the 2 rows for A but a pagination total of 42, so the UI shows 'showing 2 of 42', renders extra empty pages, and next-page fetches (offset=20) return nothing. Same defect in ListPipelines (logging.go:484 uses CountLoggingPipelines = 'SELECT count(*) FROM logging_pipelines' while the list is ListPipelinesByCluster). This is exactly the class of pagination-total bug already fixed for alert events (ListAlertEventsFiltered + CountAlertEventsFiltered).
- **Fix:** Add cluster-scoped count queries (CountOutputsByCluster / CountPipelinesByCluster with WHERE cluster_id = $1) and call those in ListOutputs / ListPipelines instead of the global CountLoggingOutputs / CountLoggingPipelines.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F20 · [M/stub] Node CPU/memory utilization is never populated: the node-metrics endpoint returns 501 and node detail hardcodes usage to 0

- **Location:** `internal/handler/monitoring.go:3720` · **Batch 2** · verdict CONFIRMED
- **Failure:** Route /api/v1/monitoring/metrics/node/{cluster_id}/{node}/ is registered (routes_monitoring.go:29) but always 501s, and the node-detail payload hardcodes cpuUsage/memoryUsage to 0, so the node detail page shows 0% CPU/mem even when a Prometheus/Thanos backend exists. Rancher shows live per-node CPU/memory/disk gauges. The realClusterSummary path already proves the Prom queries exist; only the per-node variant was left unbuilt.
- **Fix:** Add node-scoped Prometheus scalar/series queries (node_cpu_seconds_total / node_memory_* filtered by instance or node label) mirroring realClusterSummary, wire LegacyNodeMetrics to them, and fill cpuUsage/memoryUsage in getNodeDetail.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.

### F23 · [M/inefficiency] Cluster resource list endpoints ignore limit/offset — they return the entire cluster list while emitting non-functional pagination links

- **Location:** `internal/handler/workloads.go:709` · **Batch 4** · verdict CONFIRMED
- **Failure:** GET /clusters/{id}/pods/?limit=20 on a 5,000-pod cluster pulls all 5,000 pod objects across the single agent websocket and serializes them; the pagination envelope advertises limit=20 with a Next link at offset=20, but following it re-runs the same unbounded fetch and returns the identical full set, so the UI's 'Next' shows duplicate data. Same pattern in ListNodes (660), ListEvents (706), ListNamespaces (647), ListWorkloadPods (747), and List via RespondPaginated (350).
- **Fix:** Pass Kubernetes limit + continue tokens through to the apiserver (real chunked pagination) and thread the returned continue token into NewPagination/HasMore, or slice server-side and stop advertising Next when the page was not truncated.
- **Verify:** add/extend a test that reproduces the failure above and passes only after the fix; run the standard gate.


---

## Execution plan (ultracode)

The remediation runs as a fan-out over the 8 workstreams (disjoint files), each agent implementing all its findings + tests, followed by an adversarial review of each workstream's diff, then central integration by the lead (sqlc regen, cross-file wiring, `go build`/`go test`/frontend gate, commit).

**Wave ordering (land + validate before the next):**

- **Wave 1 (Batch 1 — critical/HA):** F-items in batch 1 across WS-A (RBAC escalation guard), WS-B (stream tickets → Redis-backed), WS-D (leader-gate the snapshot/reap tasks + the etcd OFFSET bug), WS-F (decommission cascade). Ship + redeploy + validate before Wave 2.

- **Wave 2 (Batch 2 — integrity/stubs):** group-binding no-op, gitops presets, logout refresh revoke, vault-on-upgrade, alert-eval caps/collapse, misc stubs.

- **Wave 3 (Batch 3 — parity/UX):** kubeconfig token, node metrics, dashboard alert tiles, namespace-scoped RBAC reads, list pagination, registration-timeline SSE, ArgoCD ApplicationSet surfacing, helm rollback range.

- **Wave 4 (Batch 4 — efficiency):** N+1s (alerting, clusters/argocd, silences), apiserver-audit bulk insert, backup-retention windowing.


Each wave: implement → adversarially verify each finding's fix against its **Verify** step → `go build ./... && go test ./...` + frontend gate → commit → (waves that change runtime behavior) redeploy to dev + smoke.


## Additional UX item folded in (from the ArgoCD-overview investigation)

- **ArgoCD overview surfaces only Applications, not ApplicationSets.** In astronomer's baseline model GitOps is ApplicationSet-driven, so an instance with 0 Applications but N ApplicationSets reads as an empty/broken 'Synced 0'. Add an ApplicationSets section (count + per-set generated-app count + target selector) and an empty-state that distinguishes 'no targets matched' from 'broken'. (WS-G / frontend, Batch 3.)
