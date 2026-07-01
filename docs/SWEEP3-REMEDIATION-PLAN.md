# Sweep 3 Remediation Plan — 37 Verified Findings (2026-07-01)

> Third grounded ultracode sweep (11 finders → adversarial per-finding verification). 38 raw → **37 CONFIRMED/PLAUSIBLE**. Deeper than sweeps 1–2: a concentrated tenant-isolation/IDOR cluster, HA-correctness gaps, and 3 regressions in code shipped earlier this session. File-disjoint workstreams for parallel-safe remediation.

## Waves
- **Wave 1 (security + regressions):** WS-A (catalog/vuln/anomaly/extension IDOR), WS-B (secrets/gitops), WS-C (identity: SSO/OIDC/SCIM), WS-D (tunnel HA — incl. the ticket-double-consume regression), WS-H (db — incl. the etcd-prune regression).
- **Wave 2 (correctness/integrity/perf):** WS-E (worker), WS-F (compliance/maintenance), WS-G (frontend), WS-I (project/workload handlers).

## Cross-cutting rules
1. No shared-file edits across workstreams; scheduler.go / server.go / worker.go / routes*.go / config.go / generated sqlc are integrator-owned (described in wiring_needed).
2. Security fixes ship enforcing, each with a regression test proving the leak/bypass is closed and a legit caller still works.
3. New DB work = new migration (after 126) or append-only query; sqlc regen + querier/fakes by the integrator.
4. Every finding has a Verify step = its acceptance test.


---

## A-idor-catalog
**Files:** `internal/handler/anomaly.go`, `internal/handler/catalog.go`, `internal/handler/extensions.go`, `internal/handler/image_vulns_history.go`

### T05 · [L/security] Installed-chart values endpoint is an unauthenticated-of-scope IDOR leaking Helm values (secrets) fleet-wide
- **Loc:** `internal/handler/catalog.go:1395` · CONFIRMED
- **Failure:** A project-scoped or read-only user (any valid JWT/session, even with zero cluster grants) calls GET /api/v1/catalog/installed/{id}/values/ with an installed_chart UUID (IDs are freely returned by /catalog/installed/ and /clusters/{id}/apps/). The server returns values_override verbatim — the raw Helm values a chart was installed with, which routinely contain DB passwords, API keys, and connection strings — for any release on any managed cluster, with no 403.
- **Fix:** Mirror the sibling handlers: after GetInstalledChartByID, call `if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbRead) { return }` before responding.
- **Verify:** regression test reproducing the failure; standard gate.

### T06 · [L/security] GET /catalog/installed/ returns every cluster's installed-chart inventory with no tenant scoping or RBAC
- **Loc:** `internal/handler/catalog.go:1067` · CONFIRMED
- **Failure:** Any authenticated user (e.g. a read-only viewer scoped to a single project) GETs /api/v1/catalog/installed/ and receives the full fleet's helm release list — release names, namespaces, cluster IDs, and values_override across every managed cluster — despite holding catalog:read on none of them.
- **Fix:** Require a cluster_id and gate via authorizeClusterAction(ResourceCatalog, VerbRead), or for the unscoped fleet listing filter rows to clusters the caller's bindings permit (superuser-only otherwise); never return values_override in a list projection.
- **Verify:** regression test reproducing the failure; standard gate.

### T09 · [L/security] Cross-cluster IDOR: vuln scan-history query is not scoped to the URL cluster
- **Loc:** `internal/handler/image_vulns_history.go:271` · CONFIRMED
- **Failure:** A user whose RBAC grants cluster:read on cluster A only (a cluster-scoped operator) requests .../clusters/{A}/vulnerabilities/reports/{report_id-belonging-to-cluster-B}/history/. The route guard passes (they hold cluster:read on A), and the query returns cluster B's per-image CVE snapshot counts, breaking cluster isolation. Exploitation needs a report_id from cluster B, but the isolation control itself is fail-open.
- **Fix:** Add `AND cluster_id = $2` to listImageVulnerabilityHistoryForReport and pass the parsed clusterID (already parsed at line 252) as the second bind, mirroring the row.ClusterID != clusterID check used by the snapshot/netpol handlers.
- **Verify:** regression test reproducing the failure; standard gate.

### T20 · [M/security] Anomaly-baselines read endpoints expose per-cluster metric baselines fleet-wide with no authorization
- **Loc:** `internal/handler/anomaly.go:48` · CONFIRMED
- **Failure:** A user with access to only cluster A calls GET /api/v1/anomaly-baselines/ (or /{id}/ by iterating UUIDs) and reads statistical baselines (mean/stddev/p50/p95/p99, last values, sample counts) for every metric on every managed cluster, disclosing cross-tenant operational telemetry.
- **Fix:** Gate the group with requirePermission on a monitoring/clusters read resource, and in the handlers resolve each row's cluster_id through authorizeClusterAction / filter the unscoped list to the caller's authorized clusters.
- **Verify:** regression test reproducing the failure; standard gate.

### T22 · [M/security] Extension re-install does not reset bundle_verified — signed-bundle gate bypass lets an unsigned bundle be served/executed as verified
- **Loc:** `internal/handler/extensions.go:646` · CONFIRMED
- **Failure:** Admin (who does NOT hold the Ed25519 signing key) installs extension 'foo' with a Tier-2 bundle descriptor sha256=X, gets the trusted-key holder to sign X, and calls verify-bundle → bundle_verified=true, enabled=true. The admin then re-POSTs /extensions/ for 'foo' with a manifest whose bundle descriptor points at a malicious url + sha256=Y (never signed/verified). The upsert leaves bundle_verified=true and (since a trusted key is configured, the enabled fail-closed guard at line 642 does not fire) enabled=true. /extensions/mounts/ now hands the host loader the malicious Y descriptor as a verified Tier-2 mount, which is fetched and executed in the console — the entire Ed25519 signature contro
- **Fix:** In the upsertUIExtension ON CONFLICT branch reset bundle_verified=false whenever checksum/manifest changes (e.g. SET bundle_verified = (ui_extensions.checksum = EXCLUDED.checksum AND ui_extensions.manifest = EXCLUDED.manifest)), or unconditionally clear it on re-install so any new bundle must be re-verified before it can mount.
- **Verify:** regression test reproducing the failure; standard gate.


---

## B-secrets-gitops
**Files:** `internal/gitops/parser.go`, `internal/handler/cloud_credentials.go`, `internal/handler/cloud_credentials_test_endpoint.go`, `internal/handler/gitops.go`

### T07 · [L/security] Cloud-credential target_refs are not authorized against the target cluster/namespace — project-Update RBAC lets a caller write or delete arbitrary Secrets in any imported cluster
- **Loc:** `internal/handler/cloud_credentials.go:801` · CONFIRMED
- **Failure:** A user with Project-Update on project A (no rights on cluster X owned by project B) POSTs a cloud credential with target_ref {cluster_id: X, namespace: kube-system, secret_name: <victim>}. The worker force-applies a Secret into X/kube-system, overwriting the data of any existing Secret of that name (cross-tenant tampering/DoS). Deleting the credential enqueues an unconditional Secret DELETE for that (cluster,namespace,name), letting the same low-privileged user delete arbitrary Secrets in any imported cluster.
- **Fix:** In canonicaliseTargetRefs, verify each target cluster/namespace is owned by the request's project (e.g. the (cluster,namespace) pair exists in project_namespaces for projectID) and reject otherwise; additionally have the worker refuse to overwrite/delete Secrets that lack the astronomer managed-by label.
- **Verify:** regression test reproducing the failure; standard gate.

### T08 · [L/security] GitOps source Update stores new git auth token/SSH key in plaintext at rest (Create encrypts, Update does not)
- **Loc:** `internal/handler/gitops.go:336` · CONFIRMED
- **Failure:** A superuser edits an existing GitOps source via PUT /api/v1/admin/gitops-sources/{id}/ and supplies a fresh (non-sentinel) HTTPS PAT or SSH private key. The cleartext credential is written verbatim into the auth_encrypted column. The sync worker's decryptGitAuth() silently falls back to the raw value on Fernet-decrypt failure (gitops_sync.go:789-797), so sync still works — masking the fact that the credential now sits in plaintext in Postgres/backups/replicas, exactly the exposure Fernet-at-rest exists to prevent.
- **Fix:** In Update, mirror Create: when `req.Auth != GitOpsAuthSentinel && req.Auth != 
- **Verify:** regression test reproducing the failure; standard gate.

### T19 · [M/bug] GitOps parser silently drops all but the first document in a multi-document YAML file
- **Loc:** `internal/gitops/parser.go:81` · CONFIRMED
- **Failure:** An operator commits a .yaml file containing several ClusterRegistration docs separated by `---` (a normal GitOps/kubectl pattern), or a file whose first doc is an unrelated resource (e.g. a Namespace) followed by a ClusterRegistration. Only the first document is parsed: the trailing ClusterRegistration clusters are never registered (silent data loss), and if the first doc is non-ClusterRegistration its apiVersion mismatch is treated as IsSkippable so the whole file is skipped — the intended cluster registration silently never happens, with no error stamped on the source.
- **Fix:** Replace yaml.Unmarshal with a yaml.NewDecoder loop that decodes every document in the file, returning one ClusterRegistration per matching doc (skipping non-matching ones), so walkSource emits a ParsedDoc for each registration.
- **Verify:** regression test reproducing the failure; standard gate.

### T21 · [M/stub] GCP credential /test/ is a stub that reports OK=true whenever the service-account JSON merely parses
- **Loc:** `internal/handler/cloud_credentials_test_endpoint.go:148` · CONFIRMED
- **Failure:** An operator pastes a GCP service_account_json whose private key has been revoked or rotated. The Test button returns OK/green because the blob is syntactically valid; the operator saves a dead credential, and later cloud-credential materialization (or downstream workloads consuming the Secret) fails with an auth error that the successful test told them couldn't happen.
- **Fix:** Actually sign the JWT with the SA private key and exchange it at token_uri (like TestAzure/TestAWS do a real network call), returning OK based on the token response; until then return OK=false with a clear 
- **Verify:** regression test reproducing the failure; standard gate.


---

## C-identity
**Files:** `internal/auth/group_sync.go`, `internal/auth/oauth.go`, `internal/handler/scim.go`, `internal/handler/sso.go`

### T10 · [L/bug] SSO callback requires CSRF state to live in the local process's in-memory map, breaking SSO in any multi-replica (HA) deployment
- **Loc:** `internal/handler/sso.go:211` · CONFIRMED
- **Failure:** In an HA install (this platform ships leader-election + Redis-backed stream tickets for multi-replica), a user hits /auth/login on replica A (state stored in A's memory) and the IdP redirect lands /auth/callback on replica B behind the round-robin LB. B has no `states[state]` entry, so consumeState returns false and every such login fails with 403 'OAuth state did not match', even though the HMAC-signed cookie validates fine. SSO effectively works only when both requests happen to hit the same replica.
- **Fix:** Drop the mandatory in-memory consumeState gate (or make it best-effort/advisory) and rely solely on the already-implemented HMAC-signed `astro_sso_state` cookie, which is stateless and HA-safe. If replay protection is desired, back the one-time-use marker with Redis like the stream tickets.
- **Verify:** regression test reproducing the failure; standard gate.

### T11 · [L/correctness] Single sign-out silently stops working after the first token refresh because sso_sessions is keyed to the initial access-token JTI and never re-persisted
- **Loc:** `internal/handler/sso.go:361` · CONFIRMED
- **Failure:** A user logs in via OIDC, keeps the tab open past one access-token lifetime (default minutes), so the SPA silently refreshes and now holds a new access JTI. When they later click Logout, buildSSOLogoutRedirect queries GetSSOSession by the new JTI, gets ErrNoRows, records metric 'no_session', and returns no redirect_url. RP-initiated logout at the IdP never fires; the upstream session survives and a stolen IdP cookie can re-mint access. The NIST AC-12 / single-sign-out control is effectively dead for any session older than one access lifetime.
- **Fix:** Key sso_sessions by user_id (or refresh-token JTI) instead of the ephemeral access JTI, and/or re-persist the SSO session in the Refresh handler so the current JTI always resolves. Set ExpiresAt to the refresh-token expiry, not the access-token expiry.
- **Verify:** regression test reproducing the failure; standard gate.

### T16 · [M/bug] Group sync revokes group_sync role bindings granted via a different connector because the diff enumerates all of a user's bindings but computes 'wanted' from only the current connector's mappings
- **Loc:** `internal/auth/group_sync.go:204` · CONFIRMED
- **Failure:** Operator configures connector-A-scoped mapping (group X -> role R) and connector-B-scoped mapping (group Y -> role S). A user entitled under both logs in via connector A: they gain R and, because the connector-B binding for S isn't in this run's `wanted`, S is DELETED (and the RBAC cache invalidated). Their next login via connector B re-adds S but deletes R. The user's group-synced privileges flap on every login, silently dropping access they legitimately hold.
- **Fix:** Store the granting connector_id on group_sync role_bindings and scope both the existing-binding enumeration and the delete diff to the connector being synced (or union all of the user's connector snapshots before computing the revocation set).
- **Verify:** regression test reproducing the failure; standard gate.

### T17 · [M/security] OIDC id_token email_verified claim is parsed but never enforced, so an IdP that emits unverified emails can auto-provision or hijack accounts by email
- **Loc:** `internal/auth/oauth.go:478` · CONFIRMED
- **Failure:** On an IdP that permits users to set an arbitrary, unverified email (self-service Keycloak/Authentik realm, or any misconfigured connector), an attacker sets their profile email to a platform admin's address. The id_token validates (signature/issuer/audience all fine — email_verified=false is ignored), findOrCreateUser matches the existing admin row by email, and the attacker is issued that admin's session with superuser privileges. Even without a pre-existing account, unverified emails let attackers provision accounts under others' identities.
- **Fix:** Reject the callback when the provider advertises email support but returns email_verified=false, and prefer a stable `sub`+issuer identity for account linking rather than a raw, possibly-unverified email address.
- **Verify:** regression test reproducing the failure; standard gate.

### T24 · [M/security] SCIM can deactivate and mutate superuser/staff accounts, bypassing the DeleteUser privilege guard and enabling full admin lockout from the IdP
- **Loc:** `internal/handler/scim.go:341` · CONFIRMED
- **Failure:** A SCIM client (Okta/Azure AD bearer token, or a compromised/misconfigured IdP) issues PUT/PATCH /scim/v2/Users/{id} with active:false against every superuser. Each call sets is_active=false AND revokes the user's live sessions, locking every platform admin out simultaneously — precisely the outcome the DeleteUser guard was written to prevent. The IdP can also silently rewrite a superuser's email/username, which (given SSO email-based matching) enables rebinding the account.
- **Fix:** Apply the same `IsSuperuser || IsStaff` guard used in DeleteUser to the deactivate/attribute-mutation paths in PutUser, PatchUser, and the CreateUser update branch (reject with 403, or at minimum refuse active:false on privileged accounts).
- **Verify:** regression test reproducing the failure; standard gate.

### T25 · [M/security] SCIM PUT/PATCH/POST can deactivate a superuser and kill its sessions, bypassing the DeleteUser privileged-user guard
- **Loc:** `internal/handler/scim.go:328` · CONFIRMED
- **Failure:** A holder of the SCIM bearer token (or a misconfigured IdP that syncs `active:false` for a username that collides with a local superuser) issues PUT /scim/v2/Users/{superuser-id} with active:false. is_active flips to false AND revokeUserSessions terminates the admin's live JWTs — a silent, complete lockout of all platform admins that the DELETE guard was written to prevent.
- **Fix:** Apply the same `IsSuperuser || IsStaff` refusal in PutUser, PatchUser, and the CreateUser re-provision branch before calling UpdateUser (or at minimum before flipping is_active to false / revoking sessions on such a user).
- **Verify:** regression test reproducing the failure; standard gate.


---

## D-tunnel-ha
**Files:** `internal/handler/service_proxy.go`, `internal/tunnel/exec_consumer.go`, `internal/tunnel/logs_consumer.go`, `internal/tunnel/proxy.go`

### T12 · [L/bug] Cross-pod WS exec/logs/shell double-consumes the one-use stream ticket → HA browser sessions hard-401
- **Loc:** `internal/tunnel/exec_consumer.go:151` · CONFIRMED
- **Failure:** Two server replicas (the .247 stack) behind nginx. A browser opens pod exec/logs/shell with a one-use `?ticket=`. nginx pins the WS handshake to the non-owner pod. That pod consumes the ticket globally (Redis `Take`), passes RBAC, then reverse-proxies to the owner pod. The owner pod calls Validate again → `Take` returns not-found → returns 401. The client gets 401 and a retry can't help (ticket already burned). Browser exec/logs/shell fails ~50% of the time in HA — exactly the deployment ForwardWSToOwnerPod was built to support. JWT/api-token header callers are unaffected (idempotent), but browsers can only use tickets.
- **Fix:** Do the owner-pod detection/forward BEFORE consuming the ticket: call ForwardWSToOwnerPod first (routing needs no identity), and only run authenticateStreamRequest + authorizeCluster on the pod that actually terminates the stream. The 'forged request can't reach the sibling' concern is moot because the sibling does its own auth. Alternatively have the sibling skip re-validation when X-Astronomer-Fo
- **Verify:** regression test reproducing the failure; standard gate.

### T13 · [L/bug] Log stream leak: server never sends LOG_STOP when the logs WS closes, so the agent's follow=true kubelet stream + goroutine leak forever
- **Loc:** `internal/tunnel/logs_consumer.go:261` · CONFIRMED
- **Failure:** A user opens a live (follow) pod-logs view and closes the tab. The server closes its local Stream; subsequent LOG_DATA frames from the agent hit routeToStream, find no stream, and are dropped — but the agent's sendFn keeps succeeding (tunnel WS still up), so its goroutine never sees an error and keeps tailing the pod. Every opened-then-closed follow-logs view permanently leaks one goroutine + one open kubelet log connection on the agent until the tunnel reconnects or the pod dies. Over a busy operator day this exhausts agent FDs/goroutines.
- **Fix:** In HandleLogs, on loop exit send `MsgLogStop{StreamID:streamID}` to the agent (defer it right after CloseStream) so agent/logs.go:HandleLogStop cancels sessionCtx and the goroutine drains — mirroring how exec_consumer.go:295 sends MsgExecEnd on read-loop exit.
- **Verify:** regression test reproducing the failure; standard gate.

### T26 · [M/inefficiency] Service proxy reads the entire request body into memory with no size cap
- **Loc:** `internal/handler/service_proxy.go:85` · CONFIRMED
- **Failure:** An authenticated user with an allowlisted proxy target POSTs a multi-gigabyte body to .../clusters/{id}/proxy/service/{ns}/{svc:port}/; io.ReadAll buffers all of it in the control-plane process before the tunnel call, exhausting memory.
- **Fix:** Wrap r.Body in http.MaxBytesReader(w, r.Body, <cap>) before io.ReadAll (as service_mesh.go does), returning 413 on overflow; ideally add a server-level body-size limit middleware for all mutating routes.
- **Verify:** regression test reproducing the failure; standard gate.

### T31 · [M/bug] Cross-pod HTTP watch forwarding buffers events (io.Copy with no per-chunk Flush), stalling real-time watch/SSE
- **Loc:** `internal/tunnel/proxy.go:548` · CONFIRMED
- **Failure:** Two replicas; nginx pins a `?watch=true` (or Accept: stream=watch) request to the non-owner pod. That pod forwards to the owner and pipes the response with io.Copy. Watch events smaller than the ~2KB bufio threshold (most ADDED/MODIFIED/bookmark events) are held in the front pod's buffer and not delivered to the browser/ArgoCD until ~2KB accumulates or the watch ends — which for a quiet resource can be minutes. Cross-pod watches appear frozen/stale even though data is flowing.
- **Fix:** Replace the trailing single Flush with a copy-and-flush loop for streaming responses (read into a buffer, Write, then `flusher.Flush()` each iteration), or detect isWatchRequest and use a flushing writer wrapper as consumeStreamingResponse does.
- **Verify:** regression test reproducing the failure; standard gate.


---

## E-worker
**Files:** `internal/db/sqlc/argocd_operations.sql.go`, `internal/worker/tasks/alert_evaluation.go`, `internal/worker/tasks/argocd_auto_register_cluster.go`, `internal/worker/tasks/cluster_decommission.go`, `internal/worker/tasks/email_dispatch.go`, `internal/worker/tasks/siem_dispatch.go`

### T18 · [M/bug] ArgoCD operation reconciler double-executes syncs across HA replicas (claim is not atomic, not leader-gated)
- **Loc:** `internal/db/sqlc/argocd_operations.sql.go:530` · CONFIRMED
- **Failure:** With server.replicaCount>1 (a supported HA deployment — see the Redis-backed stream-ticket work), replica A and replica B tick concurrently, both ListPendingArgoCDOperations return the same pending sync op, both call MarkArgoCDOperationRunning (both succeed because the UPDATE has no status precondition), and both run executeSync → two `POST /api/v1/applications/{name}/sync` calls fire upstream for one requested op, racing on the same argocd_operations row's progress/completion writes (one may 409 and be recorded as Failed while the other succeeds).
- **Fix:** Make the claim atomic: `... WHERE id=$1 AND status IN ('pending','failed')` (returning zero rows means another worker won → skip), or select pending ops `FOR UPDATE SKIP LOCKED`, and/or gate StartReconciler's dispatch behind the existing leader.New() election used for the periodic tasks.
- **Verify:** regression test reproducing the failure; standard gate.

### T32 · [M/correctness] Alert-event read-back capped at 200 rows per rule while global-rule fan-out now creates up to one event per cluster, so large fleets get stuck-firing events and duplicate alert storms
- **Loc:** `internal/worker/tasks/alert_evaluation.go:84` · CONFIRMED
- **Failure:** For a global rule on a fleet where more than 200 clusters have fired (e.g. a network partition disconnects 250 clusters, each firing a `deadman`/`disconnected` event), only the 200 most-recently-fired events are read back. A currently-firing cluster whose event is outside that 200-row window is invisible to filterActiveEventsForCluster -> (a) when it recovers, its firing event is never transitioned to resolved (stuck-firing forever), and (b) while it keeps triggering, len(activeEvents)==0 so a fresh CreateAlertEvent fires every tick and dispatchAlertNotifications re-pages every minute (alert storm). Before the fan-out shipped, a global rule collapsed to a single evaluation/event, so the 200-
- **Fix:** Page ListAlertEventsByRule until a short batch (like listAllAlertRules/listActiveSilences already do), or fetch only the ACTIVE (firing/acknowledged/silenced) events for the rule via a status-filtered query, so every cluster's active event is considered regardless of fleet size.
- **Verify:** regression test reproducing the failure; standard gate.

### T33 · [M/bug] ArgoCD auto-adoption periodic sweep is the one periodic reconciler NOT leader-gated; it runs on every worker replica and races on per-cluster proxy-token minting
- **Loc:** `internal/worker/tasks/argocd_auto_register_cluster.go:98` · CONFIRMED
- **Failure:** Two astronomer-worker replicas fire the 5m sweep concurrently for a newly-connected cluster with no proxy token yet. Both hit ErrNoRows, generate distinct tokens A and B, both call RegisterCluster (upsert) so ArgoCD ends with whichever ran last (say bearer A), while UpsertArgoCDClusterProxyToken last-writer-wins stores hash(B). ArgoCD then presents bearer A to the cluster proxy; GetArgoCDClusterProxyTokenByHash(hash(A)) misses → 401, so ArgoCD cannot reach that managed cluster until the next sweep re-converges (up to 5 min). Every replica also duplicates the full ListSecrets index-repair each tick.
- **Fix:** Wrap the sweep body (the nil-cluster_id branch) in runPeriodicTaskWithLeader like the other reconcilers so only the lease holder runs the fleet sweep; keep the per-cluster enqueued path (asynq.Unique) unguarded.
- **Verify:** regression test reproducing the failure; standard gate.

### T34 · [M/bug] Email dispatcher re-renders every message with an EMPTY data bag, discarding the stored rendered body — all dynamic content (reset links, alert details, usernames) ships blank
- **Loc:** `internal/worker/tasks/email_dispatch.go:191` · CONFIRMED
- **Failure:** An operator requests a password reset. The enqueuer renders password_reset with `{{.Data.ResetURL}}` populated and stores it in email_messages.body_text. The dispatcher then re-renders the same template against `map[string]any{}`, so `{{.Data.ResetURL}}` becomes `<no value>` and the recipient gets a reset email with no working link; the row is still marked 'sent'. Same silent breakage hits account_locked ('Hello ,'), TOTP notices, api_token_created, and alert_fired (blank AlertName/Severity/Message/DashboardURL). Account recovery is effectively unusable whenever SMTP is enabled.
- **Fix:** Add a Body/BodyText/BodyHTML field to email.Message (or a Sender.SendPreRendered path) and have sendOne pass row.BodyText/row.BodyHtml straight through — this is the 'alternate send path' the code comment already promises. Do not re-render from Template with an empty Data map.
- **Verify:** regression test reproducing the failure; standard gate.

### T35 · [M/inefficiency] SIEM HTTPS/HEC forwarders build a brand-new http.Client+Transport on every 2s drain and Close() is a no-op — the configured shared client is dead code and every batch pays a fresh TLS handshake
- **Loc:** `internal/worker/tasks/siem_dispatch.go:466` · CONFIRMED
- **Failure:** An audit-heavy stack keeps an HTTPS SIEM forwarder's queue non-empty; every 2s a new *http.Transport is allocated, performs a full TLS handshake to the sink, sends one batch, and is discarded with no connection reuse. Under sustained load this is thousands of redundant TLS handshakes/hour and steady per-tick allocation churn instead of a pooled keep-alive connection.
- **Fix:** Build the per-forwarder *http.Client once (cache it keyed by forwarder id + relevant TLS fields, or reuse siemDeps.HTTPClient) so keep-alive connections are reused across drains; alternatively have the HEC/NDJSON Close() call client.CloseIdleConnections() and set a sane IdleConnTimeout.
- **Verify:** regression test reproducing the failure; standard gate.

### T37 · [S/correctness] Cluster-decommission audit archive → delete is non-atomic: audit_log rows written in the window are DELETEd but never archived (silent audit loss)
- **Loc:** `internal/worker/tasks/cluster_decommission.go:748` · PLAUSIBLE
- **Failure:** Between the archive SELECT snapshot and the DELETE, any audit_log row matching the WHERE that is committed by a concurrent request (a final in-flight API call against the cluster, or another pod's audit middleware writing detail.cluster_id=this cluster) is not in the archived set but IS matched by the DELETE — it is permanently removed from both audit_log and audit_archive. Result: silent, unrecoverable loss of audit records during decommission, exactly the security-sensitive history the archive phase claims to preserve 'indefinitely'. (The DELETE's detail->>'cluster_id' clause is also over-broad, pulling unrelated resource audit rows that merely reference this cluster out of the live log.)
- **Fix:** Run ArchiveAuditLogsForCluster and DeleteAuditLogsForCluster inside one pgx transaction so the DELETE only removes exactly the rows the INSERT...SELECT captured (or add a RETURNING/ids handshake so the DELETE targets only archived ids), keeping the phase idempotent on re-run.
- **Verify:** regression test reproducing the failure; standard gate.


---

## F-compliance-maint
**Files:** `internal/compliance/apply.go`, `internal/maintenance/window.go`, `internal/server/baseline_appsets.go`, `internal/server/server.go`

### T02 · [L/correctness] Compliance baseline Revert cannot turn OFF flags it turned ON — writeSpec only ever sets truthy/non-empty fields
- **Loc:** `internal/compliance/apply.go:273` · CONFIRMED
- **Failure:** Pre-apply totp.required=false. Operator applies a hardening baseline that sets totp.required=true and smtp.required=true; buildSnapshot correctly captures previous_state{RequiredTOTP:false, RequiredSMTP:false}. Operator then Reverts: writeSpec(snapshot) skips both flags because they are false, so totp.required and smtp.required stay TRUE. The application row is marked 'reverted' and the operator believes prior state was restored, but the security-relevant platform settings remain pinned on — a silent, misleading partial revert (also affects pss_profile reverting from '' and audit retention reverting to 0).
- **Fix:** Make writeSpec write every field the baseline owns unconditionally (including the false/empty value), or have Revert use a distinct restore path that always sets each captured key rather than reusing the set-only-when-truthy Apply writer.
- **Verify:** regression test reproducing the failure; standard gate.

### T28 · [M/bug] Multiple 'permitted' maintenance windows are AND-ed instead of OR-ed — an operation is wrongly blocked when one permitted window is active but another is inactive
- **Loc:** `internal/maintenance/window.go:269` · CONFIRMED
- **Failure:** Operator defines two permitted windows for op cluster_template.apply on the same cluster: window A (business hours, active now) and window B (weekend, inactive now). During business hours the apply should be allowed (A is active), but IsBlocked iterates, reaches B first, sees !active, and returns blocked=true → the operation is refused/deferred even though a permitted window is currently open.
- **Fix:** For permitted mode, only block when there is at least one matching permitted window AND none of the matching permitted windows are active: scan all matches, track whether any is active, and block only if matched>0 && activeCount==0.
- **Verify:** regression test reproducing the failure; standard gate.

### T29 · [M/correctness] ArgoCD baseline ownership decisions never expire — expires_at is stored/validated but no code path enforces it
- **Loc:** `internal/server/baseline_appsets.go:317` · CONFIRMED
- **Failure:** An operator records a temporary `leave_local` ownership decision for cluster X, component kube-state-metrics, with expires_at=+7d (intending ArgoCD to take over the baseline after a 7-day legacy-Helm cutover). The baseline ApplicationSet generator appends X's cluster-id to the NotIn matchExpression, excluding X from the platform baseline fan-out. After 7 days the decision is expired but ListArgoCDBaselineOwnershipDecisionsByDecision still returns it, so X is excluded forever — ArgoCD never adopts/reconciles the baseline components on X, and the ownership UI keeps reporting state=local_manual. The same permanence applies to `replace`/`adopt` decisions.
- **Fix:** Add `AND (expires_at IS NULL OR expires_at > now())` to both ListArgoCDBaselineOwnershipDecisions and ListArgoCDBaselineOwnershipDecisionsByDecision (and have argoOwnershipState treat an expired row as hasDecision=false), plus optionally a periodic DELETE of expired rows so the exclusion lapses exactly when the operator intended.
- **Verify:** regression test reproducing the failure; standard gate.

### T30 · [M/stub] Deferred maintenance operations return 202 'deferred, will run at next window' but are never replayed (no replayers registered) and never captured the request body
- **Loc:** `internal/server/server.go:1392` · CONFIRMED
- **Failure:** Operator configures a maintenance window with on_block='defer'. A gated destructive mutation (e.g. tool.uninstall / helm.install) during the window is answered 202 with 'Operation deferred until the next maintenance window open' and a deferred_operations row is created. The dispatcher runs every 60s, finds no replayer for the op_type, and marks the row 'failed' — the operation NEVER executes. The client/operator believes the action is safely queued; it is silently dropped.
- **Fix:** Register real DeferredReplayer functions for each gated op_type (or fall the gate back to on_block='refuse' until they exist), and have buildDeferredSpec capture the request body + chi URL params so a replay reconstructs the original mutation.
- **Verify:** regression test reproducing the failure; standard gate.


---

## G-frontend
**Files:** `frontend/src/components/auth/connector-form.tsx`, `frontend/src/components/backups/restore-modal.tsx`, `frontend/src/lib/live-events.ts`

### T01 · [L/correctness] Restore modal inverts intent: deselecting ALL namespaces silently restores EVERYTHING
- **Loc:** `frontend/src/components/backups/restore-modal.tsx:60` · CONFIRMED
- **Failure:** An operator toggles off all namespaces (intending to restore none / narrow the set), types the backup name to confirm, and clicks Start Restore. Because an empty selection collapses to `undefined`, Velero receives no namespace filter and restores ALL namespaces captured in the backup — the opposite of the operator's selection, potentially clobbering/recreating production workloads.
- **Fix:** Distinguish empty-selection from full-selection: if includedFilter.length === 0, block submit (disable button / show 'select at least one namespace') or send an explicit empty list; only fall back to undefined when the full set is selected (length === sourceNamespaces.length).
- **Verify:** regression test reproducing the failure; standard gate.

### T14 · [M/bug] Axios camelCase interceptor mangles Dex connector secret markers, forcing secret re-entry on every edit and persisting a garbage config key
- **Loc:** `frontend/src/components/auth/connector-form.tsx:99` · CONFIRMED
- **Failure:** An admin opens an existing OIDC/LDAP connector to edit any field (toggle enabled, change displayName). secretIsSet('clientSecret') returns false because the marker is now stored under `_ClientSecretSet`. So (a) the `••••••••` placeholder and 'Stored — leave blank' helper never render, and (b) handleSubmit (line 113) pushes `
- **Fix:** Do not blanket-camelize dynamic-key maps. Either exclude the dex connector config object (like the /k8s/ carve-out) from camelizeKeys, or have the backend emit the marker in a form the interceptor won't touch (e.g. a separate `secretsSet: [
- **Verify:** regression test reproducing the failure; standard gate.

### T15 · [M/bug] Live-event transport never registers listeners for cluster.registration.step/phase, so the registration wizard's SSE live-update is dead (silently degrades to 5s polling)
- **Loc:** `frontend/src/lib/live-events.ts:201` · CONFIRMED
- **Failure:** During cluster adoption, the agent emits cluster.registration.step/phase frames. dispatch() is never invoked for them (no listener registered, not the default 'message' type), so state.target never fires and the timeline's subscribe handlers never run. The wizard progress / adoption timeline only advances via the 5s polling fallback (registration-timeline.tsx:82), making step transitions lag up to 5s and appear non-live despite the SSE stream being open.
- **Fix:** Add 'cluster.registration.step' and 'cluster.registration.phase' to KNOWN_EVENT_TYPES so openSource registers `addEventListener` for them (or drive the addEventListener loop off the LiveEventType union directly).
- **Verify:** regression test reproducing the failure; standard gate.


---

## H-db
**Files:** `internal/db/queries/apiserver_audit_events.sql`, `internal/db/sqlc/control_plane_snapshots.sql.go`

### T03 · [L/stub] apiserver_audit_events grows unbounded — retention prune query exists but no sweeper is scheduled
- **Loc:** `internal/db/queries/apiserver_audit_events.sql:58` · CONFIRMED
- **Failure:** An operator enables apiserver-audit collection; agents stream one row per kube-apiserver audit event (get/list/watch/create... for every request on every managed cluster). Rows are only ever INSERTed (idempotent on cluster_id,audit_id) and never deleted. Over days the table reaches tens/hundreds of millions of rows, bloating the primary Postgres instance, slowing the cluster_id/event_time index, and eventually exhausting disk — a control-plane-wide outage. It is never cleaned on decommission either (phaseDeleteDependents omits it and migration 112 gives it no FK/ON DELETE CASCADE), so tombstoned clusters leak their rows permanently.
- **Fix:** Add a leader-gated daily task (mirroring TypeEnforceAuditLogRetention) that calls PruneApiserverAuditEventsBefore(now - retentionWindow), register it in scheduler.go, and add apiserver_audit_events to the decommission delete_dependents op list (plus a clusters FK with ON DELETE CASCADE).
- **Verify:** regression test reproducing the failure; standard gate.

### T04 · [L/correctness] Control-plane snapshot retention prune counts in-flight (pending/running) rows in the keep-set, retaining fewer terminal snapshots than configured
- **Loc:** `internal/db/sqlc/control_plane_snapshots.sql.go:279` · CONFIRMED
- **Failure:** When one or more pending/running snapshot rows exist (they are always newest, since the sweep creates a row then immediately prunes at control_plane_snapshot.go:270-305), those in-flight rows occupy slots in the newest-$2 keep-set. With retention=7 and 1 running + 7 succeeded rows, the keep-set is {running, 6 newest succeeded}, so the oldest succeeded row is deleted even though only 6 terminal snapshots remain — the operator asked to retain 7 DR snapshots but gets 6 (or fewer with more in-flight rows).
- **Fix:** Scope the keep-set subquery to terminal rows: `... SELECT s.id FROM control_plane_snapshots s WHERE s.cluster_id=$1 AND s.status IN ('succeeded','failed') ORDER BY s.created_at DESC LIMIT $2`, so in-flight rows never consume retention slots.
- **Verify:** regression test reproducing the failure; standard gate.


---

## I-handlers
**Files:** `internal/handler/projects.go`, `internal/handler/workloads.go`

### T23 · [M/bug] AddNamespace/RemoveNamespace write projects.namespaces JSONB and the project_namespaces sidecar non-atomically, and swallow the sidecar failure while returning 200
- **Loc:** `internal/handler/projects.go:1266` · CONFIRMED
- **Failure:** If UpsertProjectNamespace fails (or the process dies between the two writes), the JSONB says the namespace belongs to the project (UI shows it, enforcement expectations set) but no project_namespaces row exists, so namespace_scoped_rbac_enabled never grants project members read on that namespace and the reconcile enforcement never runs — a silent authz/enforcement gap reported to the caller as success. Symmetrically, two concurrent AddNamespace calls to the same project read the same JSONB snapshot and last-writer-wins drops one namespace from the JSONB while both sidecar rows persist, permanently diverging the two sources of truth. RemoveNamespace is worse: enqueueCleanup (which deletes the
- **Fix:** Wrap the JSONB UpdateProject and the project_namespaces upsert/delete in a single pgx transaction (like compliance_baselines' runTx), fail the request if either half fails, and take a row lock (SELECT ... FOR UPDATE on the project) around the read-modify-write of the JSONB list to prevent concurrent lost updates.
- **Verify:** regression test reproducing the failure; standard gate.

### T27 · [M/inefficiency] List endpoints pass ?limit straight to SQL LIMIT with no clamp (unbounded result + int32-overflow 500)
- **Loc:** `internal/handler/workloads.go:537` · CONFIRMED
- **Failure:** `GET .../workloads/operations/?limit=2000000000` issues `LIMIT 2000000000`, materializing and serializing the entire operations table into memory (OOM on a busy fleet). `?limit=2147483648` overflows int32 to a negative LIMIT, which Postgres rejects (
- **Fix:** Route these handlers through queryLimitOffset (or a shared clamp) so limit is bounded to a sane max and floored at 1, matching the audit/smtp/webhooks/maintenance handlers that already use the clamped helper.
- **Verify:** regression test reproducing the failure; standard gate.

### T36 · [S/security] Removing a namespace from a project does not invalidate the RBAC cache, leaving a stale namespace-scoped authorization window
- **Loc:** `internal/handler/projects.go:1253` · CONFIRMED
- **Failure:** The namespace_scoped_rbac_enabled feature expands each project binding into synthetic namespace-scoped cluster bindings via ListProjectNamespaces (rbac_queries.go:199-259), and GetUserBindings caches the expanded result for RBACCacheDefaultTTL=15s (rbac_cache.go:18). project_namespaces membership is thus a NEW input to a cached authz decision, but AddNamespace/RemoveNamespace never invalidate that cache. Concretely: user U is a member of project P which owns namespace `payments`; an admin removes `payments` from P to revoke U's access. For up to 15s afterward U's cached synthetic binding still grants (pods/secrets):list/read/delete in `payments` — U can `GET /api/v1/clusters/{id}/namespaces/
- **Fix:** Wire the SQLCRBACQuerier/native invalidator into ProjectHandler and call InvalidateAll() (targeted per-user isn't possible without a project->users reverse index) in both AddNamespace and RemoveNamespace after the project_namespaces write, mirroring rbac.go's post-mutation invalidation.
- **Verify:** regression test reproducing the failure; standard gate.
