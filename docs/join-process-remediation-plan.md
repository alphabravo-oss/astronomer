# Cluster JOIN Process — Remediation Plan (Rancher-Parity Hardening)

**Source of findings:** `docs/join-process-review.md` (43 verified findings: 9 HIGH · 14 MEDIUM · 20 LOW).
**Goal:** close the gaps that keep the join process at ~5/10 Rancher parity and get to ~9/10 — without regressing `v0.1.0` (which is prod-shipped) or the two soak-validated lifecycles (admin→Argo-push, viewer→pull).

## How to use this plan
- Tasks are grouped into **workstreams (WS-A … WS-G)**. Within a workstream, tasks are roughly dependency-ordered.
- Each task is **self-contained**: it lists the finding(s) it closes, the root cause, the exact files, the change, the tests to add, and how to verify. Pick any unblocked task and execute it top-to-bottom.
- **Execution order** (cross-workstream) is in §Sequencing. The **tracking table** at the end is the single status board — flip `[ ]`→`[x]` as tasks land.

## Guiding principles (apply to every task)
1. **Fail closed on trust.** CA/token/auth changes must reject on mismatch, never silently downgrade. Provide an explicit, loud escape hatch (`ASTRONOMER_INSECURE=true`) only where unavoidable.
2. **Don't regress v0.1.0.** Anything behavior-changing on the default path is feature-flagged or defaulted to current behavior, then flipped on deliberately.
3. **Every fix is tested + soaked.** Unit test the logic; add/extend a soak assertion (see WS-G) that would fail if the fix regresses. `make verify` must stay green.
4. **Honesty over marketing.** Where a doc-comment/control promises a protection the code lacks, either implement it or correct the comment in the same PR — never ship the false contract.

## Global Definition of Done (per task)
- [ ] Code change complete, least-privilege, `gofmt`/`go vet` clean.
- [ ] Unit test(s) added that fail without the fix.
- [ ] `make verify` green; `go test ./...` green.
- [ ] The relevant soak assertion (WS-G) added/updated and passing.
- [ ] The doc-comment / `docs/*.md` updated if the finding cited a false/misleading comment.
- [ ] The corresponding finding marked closed in `docs/join-process-review.md` (append `RESOLVED: <task-id>`).

---

# WS-A — Join-Trust Integrity (highest impact; the core Rancher gap)

### A1 — Server-CA pinning end-to-end + reject plaintext  `(H1, M1, L5, L13)`
**Root cause:** `CACert` is rendered (`clusters.go:1940`, `agent_manifest.go:85` both hardcode `""`), the Secret is mounted at `/etc/astronomer/tls` (`install.yaml.template:609-619`) but **no agent code reads it**; both dial paths use a bare websocket dialer with the OS trust store only.
**Files:** `internal/agent/config.go`, `internal/agent/tunnel.go` (`:119-121`), `internal/agent2/client.go` (`:107-114`), `internal/handler/clusters.go` (`renderAgentInstallManifest` ~1940, `GetCABundle` 1673), `internal/worker/tasks/agent_manifest.go:85`, `deploy/agent/install.yaml.template` (483, 609-619), `deploy/agent/template.go` (placeholder).
**Change:**
1. Add `CACert string` + `CAChecksum string` to `AgentConfig`; load from env `ASTRONOMER_CA_CERT` / `ASTRONOMER_CA_CHECKSUM` and/or read the mounted `/etc/astronomer/tls/ca.crt`.
2. Populate `CACert` in both manifest renderers from `platform_settings[registration.ca_bundle]` (reuse `GetCABundle`), wiring the `{{CA_CERT}}` placeholder.
3. Build a `*tls.Config{RootCAs:…}` from the CA bundle; if `CAChecksum` is set, verify the leaf/chain SHA-256 in `VerifyConnection` (Rancher `CATTLE_CA_CHECKSUM` semantics). Pass via `DialOptions.HTTPClient` in **both** `tunnel.go` and `agent2/client.go`. **Fail the dial closed on pin mismatch.**
4. In `LoadAgentConfig`, reject `ws://`/`http://` ServerURL unless `ASTRONOMER_INSECURE=true` (loud warn) — this is M1.
5. Remove or correct the dead-config comments (L5: empty CA Secret, `minAvailable:0` PDB comment).
**Tests:** unit — dial succeeds with matching CA/checksum, **fails closed** on mismatch; `LoadAgentConfig` rejects `ws://` without the escape hatch. Render test — manifest carries a non-empty CA when `registration.ca_bundle` is set.
**Verify:** WS-G adds a private-CA soak variant (management endpoint behind a self-signed CA → agent connects only with the pin; tamper the checksum → dial refused).
**Risk:** could break existing agents if a CA is set wrong → default `CACert=""` keeps current OS-trust behavior; pinning only engages when a bundle/checksum is provided. **Effort:** L. **Depends on:** none. **(Headline fix — do first.)**

### A2 — Durable agent-token rotation + standalone revocation  `(H2, M7, L14)`
**Root cause:** `ensureClusterAgentToken` (`server.go:867-894`) is mint-once; no `expires_at`; the `rotate_agent_token` fleet op + `token_rotation_days` are dead code; only decommission deletes the token. The **agent-side** rotation channel already exists (`token_persistence.go` + `tunnel.go:177-184` honor `ack.AgentToken`).
**Files:** `internal/db/queries/clusters.sql` (+ new migration), `internal/tunnel/server.go` (`:374` ack path, `:867-894`), `internal/worker/tasks/fleet_orchestrate.go` (`:585-595`), `internal/handler/fleet_operations.go` (`:230,248`), `internal/worker/tasks/cluster_template_apply.go` (`:586-598` `token_rotation_days`), admin route.
**Change:**
1. Migration: add `last_rotated_at` (+ optional `expires_at`) to `cluster_agent_tokens`.
2. Implement rotation: mint a fresh durable token, deliver via `ackPayload.AgentToken` on the next CONNECT, revoke the old after the agent reconnects with the new (grace window). Add `RevokeClusterAgentToken (SET revoked_at = now())` query + admin endpoint (`agent.token.revoked` audit).
3. Implement `FleetOpTypeRotateAgentToken` (or remove it from the advertised op set + reject cleanly) and a leader-gated periodic re-issue keyed on `token_rotation_days`.
**Tests:** rotate → old token rejected after grace, new accepted; revoke → next CONNECT forced to re-mint; `token_rotation_days` triggers periodic rotation.
**Verify:** WS-G soak: adopt → rotate token → agent stays connected on the new token → revoke → agent reconnect fails.
**Risk:** mid-rotation reconnect race → grace window + keep-old-until-new-confirmed. **Effort:** L. **Depends on:** none (A1 recommended first for the trust story).

### A3 — Registration-token hardening: single-use + TTL convergence  `(M2, L1, L2)`
**Root cause:** `GetRegistrationTokenByToken` doesn't filter `is_used` (`clusters.sql:121-128`); `MarkRegistrationTokenUsed` is cosmetic; TTLs diverge (24h `clusters.go:1456` vs 1h `:1518` vs sig-capped `:1654`); the query is `:one` with a legacy plaintext OR-branch, no `ORDER BY`/`LIMIT`, non-unique hash index.
**Files:** `internal/db/queries/clusters.sql`, `internal/handler/clusters.go` (1456/1518/1654), config for TTL.
**Change:**
1. Once a durable agent token exists for the cluster, add `AND is_used = false` to the registration-token read (so a leaked token can't replay post-join). Keep reusability *only* during the initial join window.
2. Converge all registration-token TTLs to one documented, configurable value (default 1h); remove the incoherent 24h `POST /register/` path.
3. `ORDER BY created_at DESC LIMIT 1`; plan removal of the legacy `OR (token_hash='' AND token=$1)` branch post-backfill; make the hash index unique.
**Tests:** used token rejected after durable token exists; single TTL across all mint paths.
**Effort:** M. **Depends on:** A2 (the "durable token exists" gate).

### A4 — Tunnel connect: rate-limit + audit + replay defense  `(M5, M6, L13, L3)`
**Root cause:** tunnel route has no auth/limit at HTTP layer (`routes.go:927`), token validated only post-upgrade with no throttle; connect success/failure not in `audit_log`; CONNECT has no nonce/replay defense; `/register/{token}.yaml` unthrottled (`routes.go:790`).
**Files:** `internal/server/routes.go` (927, 790), `internal/tunnel/server.go` (296-300, 337-394, 451-457), `internal/agent/tunnel.go` (130-147).
**Change:**
1. IP-keyed limiter on the tunnel upgrade + `/register/*` (mirror `LoginRateLimit`); reject CONNECT after N failures/window.
2. `audit.Record` on `agent.connected` (success) and `agent.auth_failed` (failure) with cluster_id, source IP, agent_version, token-kind (Hub already holds `audit.Querier`, `server.go:897`).
3. (after A1) validate CONNECT `Timestamp` clock-skew / add a challenge nonce (L13).
**Tests:** N failed CONNECTs → throttled + audited; success audited with IP.
**Effort:** M. **Depends on:** A1 (replay defense ordering).

---

# WS-B — HA Correctness (multi-replica + scale)

### B1 — HA-correct + complete decommission  `(H8, H9, M12, M14, L15, L16)`
**Root cause:** `SendDecommission` (`server.go:698-702`) is node-local (no locator forward) → managed-side cleanup skipped ~50% under HA; the handler only removes the logging ns + Velero CRs + agent Deployment, orphaning 6/7 namespaces + cluster-scoped RBAC + agent SA + the **live token Secret**; skipped phases never re-run (`cluster_decommission.go:431-433`) and token-revoke happens before cleanup; the pause-guard is process-memory only (M14).
**Files:** `internal/tunnel/server.go` (`SendDecommission` 698-702; mirror `forwardToOwnerPod`/`ForwardWSToOwnerPod`), `internal/worker/tasks/cluster_decommission.go` (423-433, 525-529, 553-568, phase ordering), `internal/agent/decommission.go` (205-310), `pkg/protocol/types.go` (`DecommissionPayload` 201-215), `internal/tunnel/handler.go:294` (served desired state).
**Change:**
1. Make `SendDecommission` **locator-aware** (forward `MsgDecommission` + await ACK to the owning pod) **or** return a retryable agent-not-connected error so asynq re-queues onto the owning pod (the `cluster_template_apply.go:341-350` pattern) instead of marking the phase skipped.
2. Extend `DecommissionPayload`/handler to delete (scoped to the `astronomer.io/managed` / `managed-by=astronomer-server` labels — keep the "never delete what we didn't create" contract): baseline-namespace contents → cluster-scoped `astronomer-agent` + `astronomer-kube-state-metrics` ClusterRole/Binding → token Secret/ConfigMap/Service/NetworkPolicy/PDB/SA → finally `astronomer-system`. Report each as a `DecommissionStepResult`.
3. Defer `revoke_agent_token` until `cleanup_managed_side` succeeds (or max-attempts/timeout); make skipped cleanup re-runnable (fix the `shouldRunPhase` comment/code contradiction).
4. Serve an **empty/tombstone** desired set for `decommissioning` clusters (`handler.go:294`) so a restarted pull agent can't re-apply mid-teardown (M14); revoke at/just-after decommission start so a restarted agent fails reconnect.
5. Fix the Velero label mismatch (`managed-by=astronomer-go` vs selector `astronomer.io/managed=true`) and emit an orphan-audit event for residual BSLs/blobs (L16). Add `SELECT … FOR UPDATE SKIP LOCKED` / lease-CAS on the decommission claim (L15).
**Tests:** unit — full-cleanup deletes exactly the managed set, nothing unmanaged; skipped cleanup re-runs; payload round-trip.
**Verify:** WS-G — the **viewer-pull** and **admin-argo** soaks already assert agent removal; extend to assert **zero managed namespaces/RBAC/token Secret remain** after decommission, and add a **multi-replica** soak that decommissions a cluster whose tunnel is on a different replica.
**Risk:** deleting too much → strictly label-scoped; dry-run path already exists. **Effort:** XL. **Depends on:** none (highest-value HA fix).

### B2 — Locator clobber CAS + fail-fast on missing POD_IP  `(M10, L19)`
**Root cause:** `locator.go:127-148` does an unconditional `Del` (no owner check) → a moved agent's old read loop deletes its new entry (up to 25s of 503s); locator silently disables when `ASTRONOMER_POD_IP` unset (`server.go:343-351`).
**Files:** `internal/tunnel/locator.go` (127-148), `internal/tunnel/server.go` (519-522, 343-351, mirror `DeleteIfSame` 592-594).
**Change:** CAS/Lua `DEL IF value==addr` (or fencing token) — mirror the in-memory `DeleteIfSame`. Fail-fast / readiness-fail when `replicas>1 && RedisURL set && POD_IP missing`; inject POD_IP in raw `deploy/k8s` manifests.
**Tests:** A→B reconnect leaves B's locator entry intact; missing POD_IP under HA fails readiness.
**Effort:** M. **Depends on:** none.

### B3 — Configurable tunnel-work concurrency  `(M11)`
**Root cause:** `NewTunnelWorker` hardcoded `Concurrency:2`, single `tunnel` queue (`worker.go:195-224`); long helm `--wait` installs starve short RPCs.
**Change:** make concurrency an env knob; split a higher-concurrency queue for short tunnel RPCs vs a bounded queue for long installs; optionally shard by owning-pod via the locator.
**Tests:** config override respected; long install doesn't block a short RPC in a separate queue.
**Effort:** M. **Depends on:** none.

### B4 — Initial-connect non-fatal (enter reconnect loop)  `(L20)`
**Root cause:** `tunnel.go:99-106` only enters `reconnectLoop` after the *first* dial succeeds; initial failure → `os.Exit(1)` → CrashLoopBackOff during the join window. `connect2`/`localcluster` already loop.
**Change:** on initial dial failure fall through into `reconnectLoop(ctx)`; exit only on ctx cancel (adopt the `connect2` pattern).
**Tests:** agent with an unreachable server stays up and retries (no exit).
**Effort:** S. **Depends on:** none.

---

# WS-C — Liveness & Health Honesty

### C1 — Decouple liveness from inventory  `(H11, L11)`
**Root cause:** `collectHeartbeat` (`health.go:295-315`) hard-errors on any failed `List`; `sendHeartbeat` (`:256-261`) then sends nothing → `last_heartbeat` goes stale → `health_check.go:98` flips to disconnected + `cluster_condition_reconcile.go:281-320` mints a spurious token. WS Pong (`server.go:821-832`) never touches `clusters.last_heartbeat`.
**Change:** on partial collection failure, emit a **minimal liveness heartbeat** carrying `DegradedReasons` (the `HeartbeatPayload.DegradedReasons` field already exists, `handler.go:144`) **and/or** treat a successful Pong/`persistPing` as a liveness touch on `clusters.last_heartbeat`. Don't overwrite inventory columns with empty/zero on a degraded beat (L11 — keep-last-good).
**Tests:** simulate List failure → cluster stays `active` with a `degraded` condition, no token churn; inventory columns retain last-good.
**Verify:** WS-G — a soak variant that strips the agent's pod-list permission mid-run and asserts the cluster stays connected (degraded), no spurious token.
**Effort:** M. **Depends on:** none.

### C2 — One staleness writer + tunnel-backed `/readyz`  `(M3, M4)`
**Root cause:** two staleness writers with different thresholds (`metrics/publisher.go:49` 60s vs `health_check.go:98-107` 2m, unconditional) flap `clusters.status`; `/readyz` reads a latched `hr.connected` (`health.go:524-530`) never reset on disconnect.
**Change:** pick one authoritative staleness writer/threshold (make the worker write transition-only or drop it). Back `/readyz` with `tunnel.IsConnected()` (or toggle `SetConnected` on (re)connect/disconnect).
**Tests:** status doesn't flap in the 60–120s band; `/readyz` goes NotReady on tunnel drop.
**Effort:** M. **Depends on:** none.

### C3 — Metrics-stale distinct state + state replay  `(M13, L12)`
**Change:** track `last_metrics_at`; surface a distinct "metrics stale" condition vs "no metrics-server" (M13). Wire a connection watcher to re-emit `StateSubscriber` informer contents on reconnect (L12, defense-in-depth — MirrorSubscriber already does).
**Effort:** M. **Depends on:** C1.

---

# WS-D — Least-Privilege Honesty

### D1 — Make `operator` honest (or split it)  `(H4, L8)`
**Root cause:** operator grants cluster-wide secrets `*` + `pods/exec|attach|portforward` (`template.go:285,288-289`) bound cluster-wide, while comments/docs claim "no cluster-admin / no RBAC escalation"; the negative test only checks RBAC-write verbs (`template_test.go:135-176`).
**Change:** either drop cluster-wide secrets-write + exec/attach/portforward from `operator` (or scope exec to namespaces), **or** relabel it honestly (`privileged-operator`) and split a true non-privileged `operator`. Fix the comments (`template.go:283,308`) and `docs/agent-privilege-profiles.md`. Strengthen the negative test to assert no cluster-wide secrets-write + no exec for the non-privileged tier. Drop `secrets` read from the ksm ClusterRole or use a metric allowlist; soften the "no secret data" comment (L8).
**Tests:** profile-rules test asserts the honest boundary; render test.
**Effort:** M. **Depends on:** none. **(Decision needed: trim vs relabel — surface to product.)**

### D2 — RBACSyncer guardrails  `(H5)`
**Root cause:** `rbac.go:31-147` applies arbitrary Cluster/Role bindings verbatim, ungated for every profile (`main.go:143-145`), no `astronomer-*` bound — unlike `reconcile.go`'s explicit safety contract.
**Change:** profile-gate `RBACSyncer` registration (skip viewer/namespace-*/operator) **and/or** constrain `HandleSyncRequest` to refuse `ClusterRole`/`ClusterRoleBinding` + namespaces outside `AstronomerOwnedNamespaces`; validate roleRef targets; reject bindings granting verbs the agent itself lacks.
**Tests:** sync of a cluster-scoped binding is refused; namespaced sync within astronomer-* allowed.
**Effort:** M. **Depends on:** none.

### D3 — Read→credential escalation: gate `/manifest/` as write  `(H3)`
**Root cause:** `GET /{id}/manifest/` gated on `VerbRead` (`routes_clusters.go:37`) but mints a live 1h registration token (`clusters.go:1491-1544`), unlike `POST /{id}/register/` which requires write.
**Change:** require `writeClusters + VerbUpdate` on `/{id}/manifest/`. For read-only preview, use the placeholder path (`RenderAgentManifestForCluster`, `clusters.go:1973`) that renders `REPLACE_WITH_REGISTRATION_TOKEN` and persists nothing.
**Tests:** read-only role gets 403 on `/manifest/`; placeholder preview works for read.
**Effort:** S. **Depends on:** none.

### D4 — Agent-side profile guard (optional, defense-in-depth)  `(M8)`
**Change:** document the configmap `PRIVILEGE_PROFILE` as advisory; optionally add a startup `SelfSubjectRulesReview` that alerts/refuses if live permissions exceed the declared profile (also catches D2 widening).
**Effort:** M. **Depends on:** D1, D2.

---

# WS-E — Provisioning Integrity

### E1 — Gate push on pull + consult ownership decisions  `(H6, H7)`
**Root cause:** push provisioning isn't gated by `PullReconcileEnabled` (`server.go:1575`, `self_manage_argocd.go:138`) despite a false stand-down promise (`config.go:130`, `desired_state.go:58-61`); the ownership-decision table is recorded/audited but never read by any provisioning path (`argocd_ownership.go:188` reporting-only).
**Change:** gate `ensureBaselineApplicationSets` (and the baseline portion of auto-register) on `PullReconcileEnabled` **and/or** filter components per cluster by the ownership decision (skip `leave_local`; apply on adopt/replace) in `ensureBaselineApplicationSets`/`DesiredState`. Make the doc-comments honest. This resolves the dual-management flap and makes `leave_local`/`replace` behavioral.
**Tests:** pull-on + remote cluster → no push appset generated; `leave_local` decision → no baseline App; `replace` → App generated.
**Verify:** WS-G — viewer-pull soak asserts **no Argo baseline apps** are generated for the pull cluster (today they sit `Unknown`).
**Effort:** L. **Depends on:** none. **(Closes the "false safety contract.")**

### E2 — Appset profile pre-flight  `(M9)`
**Change:** filter the appset cluster generator on profile (`In [operator,admin]`) or warn at registration time that sub-operator profiles can't receive Argo-push baseline; surface the requirement in the ownership view (instead of opaque 403s). Pairs with the architectural finding that viewer+Argo-push is incompatible (use pull).
**Effort:** M. **Depends on:** E1.

### E3 — gitops mass-decommission guard  `(H10)`
**Root cause:** `WalkDir` swallows `os.IsNotExist` (`gitops_sync.go:465-471`) → empty doc set → every cluster treated as "missing" → mass `cluster:decommission` under `on_delete='decommission'`; only guard is a create-time WARN for empty prefix (`gitops.go:522-526`).
**Change:** before the missing-set loop, if `parsedDocs` is empty (or missing-count == total previous) **and** `on_delete='decommission'`: refuse, stamp a source error, emit a loud audit/alert, require manual override. Make `WalkDir` treat a non-existent walkRoot as a hard error (fail the sync, not "everything deleted").
**Tests:** bad path_prefix → sync errors, **no** decommission enqueued; legit single-cluster removal still works.
**Effort:** M. **Depends on:** none. **(Destructive-safety — do early.)**

### E4 — Appset disable cascade  `(L10)`
**Change:** add a `resources-finalizer` on the baseline appset / surface a pruning/orphaned state until downstream removal is confirmed (`baseline_appsets.go:265-271`).
**Effort:** S. **Depends on:** none.

---

# WS-F — Bootstrap Manifest Hygiene

### F1 — Per-component PSA enforce labels  `(L6)`
**Change:** stamp an explicit `pod-security.kubernetes.io/enforce` level on every Astronomer-owned component namespace (today only `astronomer-monitoring` has it). The real bite is opt-in fluent-bit (hostPath) in unlabeled `astronomer-logging`. Reuse the project-namespace pattern (`project_reconcile.go:83-85`).
**Effort:** S. **Depends on:** none.

### F2 — YAML-escape manifest scalars  `(L7)`
**Change:** reuse `escapeYAMLDoubleQuoted` for `SERVER_URL`/`AGENT_IMAGE` (and remaining scalars) in `template.go:51-67`; parse-validate the rendered manifest before serving.
**Effort:** S. **Depends on:** none.

### F3 — Bootstrap-token re-apply / annotation hygiene  `(L4, L17)`
**Change:** separate the immutable bootstrap token Secret from the agent's rotated Secret (or add an SSA owner guard) so `kubectl apply` re-apply doesn't clobber the rotated token (L4); require `--server-side` or split the Secret so the plaintext token isn't persisted in `last-applied` annotations (L17). Document the re-apply caveat.
**Effort:** M. **Depends on:** A2.

### F4 — tunnel2 fail-closed + dead-config cleanup  `(L18, L5, L9, L1)`
**Change:** make `tunnel2` validator fail closed on nil behind a test flag + accept durable tokens before promotion (L18); remove dead CA Secret / fix `minAvailable:0` comment (L5); fix/remove the false "drains stale responses" reconcile comment + correlate by RequestID (L9).
**Effort:** S–M. **Depends on:** A1 (CA), A2.

---

# WS-G — Validation & Soak Extension (cross-cutting; gates every WS)

Extend the existing fail-fast soak harnesses (`/tmp/soak.sh` admin-argo, `/tmp/soak-pull.sh` viewer-pull) and add new ones. **Each fix above must add or strengthen one of these assertions.**

### G1 — CA-pin soak  `(validates A1)`
Stand the management endpoint behind a self-signed CA; assert the agent connects **only** with the correct pin and **refuses** on a tampered checksum / wrong CA.

### G2 — Token-rotation/revocation soak  `(validates A2, A3, M7)`
Adopt → rotate durable token (agent stays connected on the new) → revoke (agent reconnect fails) → re-import recovers.

### G3 — HA decommission soak  `(validates B1, B2)`
Multi-replica server; pin the agent's tunnel to replica A, run decommission such that the task lands on replica B; assert managed-side cleanup still runs and **zero managed namespaces/RBAC/token Secret remain**.

### G4 — Degradation-liveness soak  `(validates C1, C2)`
Mid-run, strip the agent's pod-list RBAC (or apply a deny NetworkPolicy blip); assert the cluster stays `active` (degraded condition), `/readyz` tracks the tunnel, no spurious registration token minted.

### G5 — Dual-management / ownership soak  `(validates E1, E2)`
Pull-on remote cluster: assert **no Argo baseline appset** is generated; set `leave_local` ownership: assert no baseline App appears.

### G6 — Full-cleanup assertion (extend existing soaks)  `(validates B1, H9)`
After every decommission in both existing soaks, assert: no `astronomer-*` namespaces, no `astronomer-agent`/`astronomer-kube-state-metrics` ClusterRoles/Bindings, no `astronomer-agent-token` Secret remain on the (pre-delete) cluster.

### G7 — Destructive-safety unit/integration  `(validates E3, H10)`
gitops source with a bad `path_prefix` → assert the sync errors and enqueues **zero** decommissions.

---

# Sequencing (recommended execution order)

**Phase 1 — Trust & Safety (do first; highest impact, mostly independent):**
`A1` (CA pin + ws reject) → `A2` (token rotation/revoke) → `E3` (mass-decommission guard) → `D3` (read→credential) → `A4` (connect rate-limit/audit). Then `A3` (depends A2).

**Phase 2 — HA Correctness:**
`B1` (HA + complete decommission) → `B2` (locator CAS) → `B4` (non-fatal connect) → `B3` (concurrency).

**Phase 3 — Honesty & Integrity:**
`E1` (gate push / ownership) → `E2` (appset pre-flight) → `D1` (operator honesty) → `D2` (RBACSyncer) → `C1` (liveness) → `C2` (staleness/readyz).

**Phase 4 — Hardening & Hygiene:**
`C3`, `D4`, `E4`, `F1`–`F4`.

**WS-G runs alongside every phase** — no task in a phase is "done" until its WS-G assertion exists and passes.

**Rancher-parity exit criteria (target ~9/10):** A1, A2, A3, A4, B1, B2, C1, C2, D1, D2, D3, E1, E3 all closed + G1–G7 green. (The remaining LOW/medium are hygiene that don't move the parity needle much.)

---

# Tracking Table

| ID | Title | Closes | Phase | Effort | Status |
|---|---|---|---|---|---|
| A1 | Server-CA pinning + reject ws:// | H1,M1,L5 | 1 | L | [x] RESOLVED a0672b1 (G1 soak: A/B/C/D pass) |
| A2 | Durable-token rotation + revoke | H2,M7,L14 | 1 | L | [x] RESOLVED fbeac02+03acd34 (G2 soak: adopt→rotate→grace→revoke pass; revoke force-disconnects live session) |
| A3 | Registration-token single-use + TTL | M2,L1,L2 | 1 | M | [x] RESOLVED 1599ba4 (temporal adoption gate; G3 soak: TTL=1h, replay denied, re-import OK) |
| A4 | Connect rate-limit + audit + replay | M5,M6,L13,L3 | 1 | M | [x] RESOLVED f1a4d37 (failure-keyed limiter; G4 soak: healthy un-throttled, bad-token 429+audit, /register 429) |
| B1 | HA + complete decommission | H8,H9,M12,M14,L15,L16 | 2 | XL | [x] RESOLVED 42b718f+451226b (G-B1 soak: full footprint deleted, no over-deletion, token revoked) |
| B2 | Locator CAS + POD_IP fail-fast | M10,L19 | 2 | M | [x] RESOLVED 75dabe7 (CAS no-clobber via miniredis; /readyz fails on HA-misconfig) |
| B3 | Configurable tunnel concurrency | M11 | 2 | M | [x] RESOLVED 667cfd5 (tunnel_worker_concurrency, default 8) |
| B4 | Non-fatal initial connect | L20 | 2 | S | [x] RESOLVED 667cfd5 (G-B4 live: unreachable server → retries, no CrashLoop) |
| C1 | Decouple liveness from inventory | H11,L11 | 3 | M | [x] RESOLVED 9633c58 (G-C1: stayed active 150s under RBAC strip, no token churn, last-good kept) |
| C2 | One staleness writer + /readyz | M3,M4 | 3 | M | [x] RESOLVED a8a45a9 (unified 2m threshold no-flap; /readyz follows live tunnel) |
| C3 | Metrics-stale + state replay | M13,L12 | 4 | M | [ ] |
| D1 | Honest operator profile | H4,L8 | 3 | M | [x] RESOLVED e83a132 (honesty fix: comment+docs+test; trim/split surfaced as product decision) |
| D2 | RBACSyncer guardrails | H5 | 3 | M | [x] RESOLVED 33390fb (fail-closed bounds: refuse cluster-scoped + out-of-owned-ns; GC bounded) |
| D3 | Gate /manifest/ as write | H3 | 1 | S | [x] RESOLVED 322706f (read-only→403 on /manifest/ live; admin import intact) |
| D4 | Agent-side profile guard | M8 | 4 | M | [ ] |
| E1 | Gate push on pull + ownership | H6,H7 | 3 | L | [x] RESOLVED 4b3c5b5 (3 PASS verdicts incl no-regression; unit proves admin-push appset byte-identical when pull off) |
| E2 | Appset profile pre-flight | M9 | 3 | M | [x] RESOLVED 4f35c9d (generator filters In [operator,admin]; viewer/namespace-* excluded) |
| E3 | gitops mass-decommission guard | H10 | 1 | M | [x] RESOLVED 4778199 (WalkDir hard-error + threshold guard + one-shot override; real-git integration suite + deploy smoke) |
| E4 | Appset disable cascade | L10 | 4 | S | [ ] |
| F1 | Per-component PSA labels | L6 | 4 | S | [ ] |
| F2 | YAML-escape scalars | L7 | 4 | S | [x] RESOLVED 796a87a (escapeYAMLDoubleQuoted on operator scalars; injection test) |
| F3 | Bootstrap-token re-apply hygiene | L4,L17 | 4 | M | [ ] |
| F4 | tunnel2 fail-closed + dead config | L18,L5,L9,L1 | 4 | S | [ ] |
| G1–G7 | Soak/validation extensions | — | all | M | [ ] |
