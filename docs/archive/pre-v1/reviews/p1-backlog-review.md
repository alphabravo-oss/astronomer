# P1 Backlog — Code Review Findings

**Verdict:** 23 confirmed findings across 11 units — 1 high-severity unauthenticated-destructive bug (SCIM hard-delete IDOR), 4 additional high-severity fail-open / data-loss defects, the rest medium/low hardening and correctness gaps. **Block on the highs (SCIM delete + MFA fail-open + watch error-swallow + the two drift/readiness data-loss bugs) before merge.**

**Counts by severity:** High **5** · Medium **12** · Low **6** · (Critical 0)

---

## Findings table

| Severity | Unit | Finding | File | Fix |
|---|---|---|---|---|
| High | scim | SCIM DeleteUser is an unscoped destructive IDOR — static SCIM token can hard-delete any user incl. superusers | `internal/handler/scim.go:290` | Refuse delete of `IsSuperuser`/`IsStaff`; ideally restrict to SCIM-owned rows |
| High | mfa | Runtime MFA-enforcement policy read fails OPEN on transient DB error | `internal/server/server.go:613` | Fail closed on real errors; only not-found/empty defaults to false |
| High | watch-stream | Header status code & end-of-stream error silently discarded — RBAC/upstream failures look like empty success | `internal/handler/pods_watch_tunnel.go:106` | Handle `K8sStreamFrameHeader` (≥400 → ERROR event); surface `frame.Error` on End |
| High | tools | Drift sweep erases real drift state on transient probe failure | `internal/worker/tasks/tool_drift_sweep.go:135` | Add `probeOK` return; `continue` without writing on transient failure |
| High | tools | Readiness probe flips a committed install/upgrade to 'failed' on transient Helm Status RPC error | `internal/handler/tools.go:1310` | Treat Status RPC transport error as non-fatal; return nil, surface readiness separately |
| Medium | scope-mw | apiserver-audit write route omits `clusters:write` scope pin — any `*:write` token bypasses scope gate | `internal/server/routes_security.go:47` | Wrap POST with `requireScope(iauth.ScopeWriteClusters)` |
| Medium | signed-urls | Signed-manifest endpoint replays within TTL and mints fresh 1h token per request, no rate limit | `internal/handler/clusters.go:1583` | IP-keyed `LoginRateLimit(5, time.Minute)`; cap minted TTL to remaining window; nonce single-use |
| Medium | scim | SCIM deactivation unsupported: re-provision ignores `active:false`, no PATCH/PUT path | `internal/handler/scim.go:200` | Apply `req.Active` on idempotent branch via `UpdateUser`; add PUT/PATCH handler |
| Medium | apiserver-audit | Ingest body unbounded — no `MaxBytesReader` / event-count cap | `internal/handler/apiserver_audit.go:81` | `MaxBytesReader(4<<20)` + `maxIngestEvents` cap |
| Medium | apiserver-audit | Negative `offset` query param reaches Postgres OFFSET → 500 | `internal/handler/apiserver_audit.go:135` | `if offset < 0 { offset = 0 }` |
| Medium | watch-stream | Watch reassembly buffer has no size cap — unbounded memory / OOM | `internal/handler/pods_watch_tunnel.go:67` | Cap `buf.Len()` against `maxAssembledResponseBytes` before `emitLines()` |
| Medium | tools | Readiness check reports 'Ready' for non-Ready workloads (false positive) | `internal/handler/tools.go:1400` | Set Helm `Wait=true` or rename to 'deployed'; don't equate deployed==ready |
| Medium | tools | Upgrade increments recorded revision by +1 instead of persisting Helm's actual revision | `internal/db/queries/catalog.sql:154` | `revision = $4`; pass `result.Revision` at both upgrade call sites |
| Medium | gitops-cond | `asynq.Unique` dedup defeated by per-request correlation ID in payload → task churn | `internal/handler/clusters.go:441` | Use `asynq.TaskID("argocd-auto-register:"+clusterID)` instead of `Unique` |
| Medium | compliance-outbox | Compliance deletion guard override is a no-op — superuser short-circuit makes 409 branch dead code | `internal/handler/webhooks.go:796` | Evaluate `settings:manage` without IsSuperuser short-circuit, or require explicit break-glass opt-in |
| Medium | crd-v1beta1 | v1beta1 promotion applied only to Cluster CRD; other 5 kinds still serve only v1alpha1 | `deploy/chart/templates/crd-cluster.yaml:40` | Promote all 5 manifests (or rescope doc/var); loop test over all `crd-*.yaml` |
| Medium | chart-frontend | Backup CronJob render gate omits `s3.bucket` — default-on backups silently fail when bucket unset | `deploy/chart/templates/management-plane-backup-cronjob.yaml:1` | Add `.s3.bucket` to gate in backup + restore-drill templates |
| Low | scim | ListUsers userName-filter swallows non-ErrNoRows DB errors → empty 200 | `internal/handler/scim.go:244` | 500 on real error; only set total=1 when `err == nil` |
| Low | signed-urls | `verifyManifestSignature` enforces no upper bound on attacker-presented expiry | `internal/handler/clusters.go:241` | Clamp window in verifier via `maxSignedManifestTTL` const |
| Low | signed-urls | Manifest HMAC key silently falls back to JWT signing secret (`cfg.SecretKey`) | `internal/server/server.go:661` | Domain-separate the fallback key via HMAC-derived subkey |
| Low | apiserver-audit | `event_time` silently defaults to `now()` when stageTimestamp unparseable | `internal/handler/apiserver_audit.go:93` | Skip/flag malformed-but-present timestamp instead of substituting now() |
| Low | alerting | Resolve/trigger notification reports rule's cluster (empty for global rules), not the firing cluster | `internal/worker/tasks/alert_evaluation.go:166` | Prefer `event.ClusterID`, fall back to `rule.ClusterID` |

---

## High-severity detail

### H1 — SCIM DeleteUser is an unscoped destructive IDOR
`internal/handler/scim.go:290`

**What's wrong.** `DeleteUser` parses `{id}`, does `GetUserByID`, then calls `queries.DeleteUser(id)`, which is `DELETE FROM users WHERE id = $1` (`internal/db/queries/users.sql:55-56`) — a hard delete with zero scoping. There is no `is_scim_managed`/`provisioned_by` marker on users, so SCIM can delete rows it never created. Authentication is a single static bearer token (`scim.go:134-150`) with no per-token scope, no expiry, and no `revoked_at` (rotation is delete-only). `GetUser`/`ListUsers` are equally unscoped, enabling id enumeration first.

**Why it matters.** Any holder of one `astro_scim_` token can irreversibly delete *any* user id — including the bootstrap superuser (`CreateBootstrapAdmin` sets `is_superuser=true`), permanently locking the operator out of the control plane. The happy-path test (`scim_test.go TestSCIMUserLifecycle`) only deletes a freshly SCIM-created user, so admin deletion is never exercised. The SCIM contract trusts the IdP, but that trust does not extend to deleting accounts SCIM never provisioned.

**Fix.** Minimal: in `DeleteUser`, after `GetUserByID`, return 403/404 when `u.IsSuperuser || u.IsStaff`. Stronger: add a `provisioned_by='scim'`/`is_scim_managed` column (set true in `CreateUser`, `scim.go:208`) and have `GetUser`/`DeleteUser`/`ListUsers` filter on it so the SCIM surface can only touch accounts it created.

### H2 — Runtime MFA-enforcement policy read fails OPEN on transient DB error
`internal/server/server.go:613`

**What's wrong.** The `SetTOTPPolicy` resolver collapses every failure into `if err != nil || len(row.Value) == 0 { return false }`. `GetPlatformSetting` is a `:one` query via `QueryRow().Scan()` (`db/queries/platform_settings.sql:1`, `sqlc/platform_settings.sql.go:32-40`): a missing row yields `pgx.ErrNoRows`, but a connection drop, pool exhaustion, statement timeout, ctx cancellation, or DB failover lands in the *same* `err != nil` branch → returns false → enforcement OFF. The unmarshal-error branch likewise returns false.

**Why it matters.** `totpEnforced` (`auth.go:350-358`) consults the policy as the *sole* enforcement source when the static `cfg.TOTPRequire` knob is off — and the shipped compliance activation path writes only the runtime setting (`compliance/apply.go:273-279` upserts `totp.required=true`; `baselines.go:110` documents this as the intended path). During a fault window, `Login` (`auth.go:622`) skips the enroll-only challenge and falls through to `GenerateTokenPair` (`auth.go:637`), issuing a full privileged session to an unenrolled local-password user — a real compliance-MFA bypass triggerable by any login timed during a routine DB restart/failover, no special capability required.

**Fix.** Fail closed; only genuine not-found/empty defaults to false:
```go
authHandler.SetTOTPPolicy(func(ctx context.Context) bool {
    row, err := queries.GetPlatformSetting(ctx, "totp.required")
    if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(row.Value) == 0) {
        return false // setting never written -> documented default
    }
    if err != nil {
        logger.Error("totp.required policy read failed; failing closed (enforcing MFA)", "error", err)
        return true
    }
    var v bool
    if json.Unmarshal(row.Value, &v) != nil {
        return true
    }
    return v
})
```
Add imports `errors` and `github.com/jackc/pgx/v5` (the diff only added `encoding/json`). Add a regression test injecting a non-ErrNoRows query error and asserting `Login` returns the enroll-only 423.

### H3 — Header status code and end-of-stream error silently discarded
`internal/handler/pods_watch_tunnel.go:106`

**What's wrong.** The agent always sends a `K8sStreamFrameHeader` first carrying the real upstream `StatusCode` (`k8sproxy.go:208-214`), then body data frames, then an End frame. The consumer switch (`pods_watch_tunnel.go:106-121`) handles only `K8sStreamFrameData` and `K8sStreamFrameEnd`; the header frame falls through and is ignored, so a 403/404/500 watch is never detected. For a 403, k8s returns a JSON `Status` object — valid JSON — so `json.Unmarshal` into `PodWatchEvent` (line 81) *succeeds* with `Type==""`, and the empty-Type event is dropped by the SSE handler (`pods_watch.go:101-102`). Since `pods_watch.go:80-82` already wrote HTTP 200 + `: connected`, the client sees a clean connect, zero events, normal close — indistinguishable from a healthy empty namespace. Separately, `frame.Error` on `K8sStreamFrameEnd` (set by `sendStreamEnd`, `k8sproxy.go:252-257`) is ignored at lines 119-120, whereas the unary path surfaces it (`k8s_requester.go:245-246`).

**Why it matters.** A routine RBAC permission error (403 on a scoped token) silently presents as success with no data — a silent data-correctness failure on a common production scenario.

**Fix.** Add `case protocol.K8sStreamFrameHeader:` — when `frame.StatusCode >= 400`, emit a terminal `out <- PodWatchEvent{Type: "ERROR", Object: <status json>}` and return. In the `K8sStreamFrameEnd` case, when `frame.Error != ""`, emit an ERROR event before returning. The SSE handler already forwards non-empty Type values, so the browser can distinguish failure from a normal close.

### H4 — Drift sweep erases real drift state on transient probe failure
`internal/worker/tasks/tool_drift_sweep.go:135`

**What's wrong.** `chartDrift` (lines 123-147) returns `(false, "")` for any non-"release not found" error (agent disconnected/timeout) at line 135. `runToolDriftSweep` (lines 100-114) then calls `MarkInstalledChartDrift` *unconditionally* for every row — `UPDATE installed_charts SET drift_detected=$2, drift_detail=$3, drift_checked_at=now()` (`catalog.sql:133-138`). So a row with genuine `drift_detected=true` is overwritten to false/'' on a single transient blip, and `drift_checked_at` is bumped to now(). `ListInstalledChartsForDriftSweep` orders by `drift_checked_at ASC NULLS FIRST` (`catalog.sql:124-131`), so the just-stamped row sinks to the back of the rotation — the genuine drift stays hidden until the batch wraps.

**Why it matters.** This is the worst failure direction for a safety/observability signal: it reports clean precisely when it could *not* observe. Loss is transient (later sweeps re-detect once the agent reconnects), but the self-perpetuating hiding plus a false-clean badge justifies high.

**Fix.** Change `chartDrift` to return `(detected, detail string, probeOK bool)`: `probeOK=false` in the transient branch, `true` elsewhere (release-not-found stays `true` so genuine deletions are recorded). In `runToolDriftSweep`, when `!probeOK`, `continue` without calling `MarkInstalledChartDrift`, preserving prior state and the old `drift_checked_at` so the row is re-probed promptly.

### H5 — Readiness probe flips a committed install/upgrade to 'failed' on transient Helm Status RPC error
`internal/handler/tools.go:1310`

**What's wrong.** For install, `CreateInstalledChart` commits the row as `'installed'` (`tools.go:1297-1306`) *before* `checkToolReleaseReady` runs (line 1310); same ordering for both upgrade branches. `checkToolReleaseReady` (1409-1444) calls `h.helm.Status` over the agent WebSocket; on any error it returns `fmt.Errorf("readiness check failed: %w", err)`. That error propagates out of the `Run` closure (1224-1226), triggering `OnFailure` (1231-1240) → `MarkToolOperationFailed`. Result: `tool_operations` = `'failed'` while `installed_charts` = `'installed'` — an inconsistent state produced by a transient WS drop right after a long install (a realistic reconnect window).

**Why it matters.** A successful, committed install/upgrade is reported as a hard failure, confusing operators and any automation gating on operation status. This is a regression — the original code returned as soon as helm + DB write completed.

**Fix.** In `checkToolReleaseReady`, when `h.helm.Status` errors, record the readiness event at warn level and return `nil` so the operation stays `'completed'`; surface readiness separately (e.g. via the drift/readiness column the sweep already writes). Combined with H-readiness (medium), don't treat a non-'deployed' status as a hard error either — record and let the drift sweep reconcile.

---

## Medium / Low — fix opportunistically

- **scope-mw** (medium, `routes_security.go:47`) — Pin `requireScope(iauth.ScopeWriteClusters)` on the apiserver-audit POST so a wrong-domain `*:write` token gets 403 instead of slipping past the `required=""` backstop. RBAC still gates it (needs `cluster:update`), so this is scope-isolation defense-in-depth.
- **signed-urls** (medium, `clusters.go:1583`) — Add `LoginRateLimit(5, time.Minute)` to the signed-manifest route; cap minted token TTL to the remaining signed-URL window; consider nonce single-use. No production caller emits these URLs yet, but the mint path is live.
- **scim** (medium, `scim.go:200`) — Honor `active:false` on the idempotent re-provision branch and add PUT/PATCH `/Users/{id}`; `UpdateUser` already exists. DELETE works, so this is the soft-deactivate gap only.
- **apiserver-audit** (medium, `apiserver_audit.go:81`) — `MaxBytesReader(4<<20)` + `maxIngestEvents` cap; gated behind `cluster:update` so it's a privileged-writer exhaustion gap, not anonymous DoS. Matches sibling convention (`service_mesh.go:634`).
- **apiserver-audit** (medium, `apiserver_audit.go:135`) — One-line `if offset < 0 { offset = 0 }`; `?offset=-5` currently 500s via Postgres.
- **watch-stream** (medium, `pods_watch_tunnel.go:67`) — Cap reassembly `buf` against the existing 64 MiB const before `emitLines()`; OOM only on anomalous newline-free agent/upstream output.
- **tools** (medium, `tools.go:1400`) — Don't equate Helm `'deployed'` with workload-Ready; set `Wait=true` on install/upgrade or rename to `'deployed'`.
- **tools** (medium, `catalog.sql:154`) — Persist `result.Revision` (`revision = $4`) on upgrade instead of `revision + 1`; failed upgrades/rollbacks bump Helm's revision without a DB +1, causing spurious drift.
- **gitops-cond** (medium, `clusters.go:441`) — Replace `asynq.Unique(10*time.Minute)` with `asynq.TaskID("argocd-auto-register:"+clusterID)`; the per-request correlation ID merged into the payload changes the md5 unique key every request, fully defeating dedup. Audit sibling sites `clusters.go:422/866/1250/1275`.
- **compliance-outbox** (medium, `webhooks.go:796`) — The override delegates to `Engine.CheckPermission`, which short-circuits true on the synthetic `IsSuperuser` binding — and the handler already requires superuser, so the 409 `BaselineRequired` branch is dead code. Evaluate `settings:manage` without the superuser short-circuit, or require an explicit audited break-glass opt-in.
- **crd-v1beta1** (medium, `crd-cluster.yaml:40`) — Promote v1beta1 in the other 5 manifests (`crd-project/clusterbaseline/componentbundle/agentprofile/gitopstarget.yaml`) or rescope the group-level doc on `types.go:38-44`; extend `TestManagementCRDsServeBothVersions` to loop all `crd-*.yaml`.
- **chart-frontend** (medium, `management-plane-backup-cronjob.yaml:1`) — Add `.s3.bucket` to the render gate in both backup and restore-drill templates; `enabled` flipped to true + a gate missing bucket = silently failing nightly backups when bucket is unset.
- **scim** (low, `scim.go:244`) — Mirror `CreateUser`: 500 on non-`ErrNoRows`, set total=1 only when `err == nil`; currently fail-opens transient DB errors to an empty 200.
- **signed-urls** (low, `clusters.go:241`) — Clamp the expiry upper bound in `verifyManifestSignature` (`maxSignedManifestTTL` const + skew slack); secret-gated, latent.
- **signed-urls** (low, `server.go:661`) — Domain-separate the JWT-secret fallback for the manifest HMAC via an HMAC-derived subkey; key-reuse hardening.
- **apiserver-audit** (low, `apiserver_audit.go:93`) — Skip/flag a malformed-but-present `stageTimestamp` instead of stamping `now()` (which sorts the row to the top). Note: RFC3339Nano *is* accepted, so real apiserver payloads are unaffected.
- **alerting** (low, `alert_evaluation.go:166`) — Prefer `event.ClusterID`, fall back to `rule.ClusterID`, so global-rule notifications show the firing cluster instead of empty; cosmetic/diagnostic only.

---

**Clean units (zero confirmed findings):** none — every unit reviewed (`signed-urls`, `scope-mw`, `scim`, `mfa`, `watch-stream`, `apiserver-audit`, `alerting`, `tools`, `gitops-cond`, `compliance-outbox`, `crd-v1beta1`, `chart-frontend`) had at least one confirmed finding.