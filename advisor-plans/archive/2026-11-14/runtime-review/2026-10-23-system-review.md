> **Archived 2026-11-14. Historical record only.** All execution authority and remaining work moved to [the current working plan](../../../CURRENT.md). Historical statuses, commands and checkboxes below are not current instructions.

# Astronomer v2.1.1 system review — 2026-10-23

## Verdict and scope

**Implementation continuation:** the user subsequently authorized remediation. Current tasks and source-fix status are in [existing Plan 032 §20](../032-technology-guide-alignment-and-latest-stable-master-plan.md#20-v211-system-review-remediation--2026-10-23). This report preserves the original review findings; local fixes do not change deployed v2.1.1 until a new candidate is qualified.

**Core operations are functioning; the system does not receive a clean security/release sign-off.** Three reproduced authentication defects require correction. Other confirmed reliability, backup, interface, and assurance gaps follow below.

Reviewed source: `/root/work/astronomer-fixes`, commit `e222313603`, whose tree is identical to `origin/main` commit `040870752fc1635ecbb08d64f9e4b71538a061f4`. The deployed management plane and new demo agent are v2.1.1. Source was reviewed without implementation changes. This report and its evidence are the only repository additions.

Review covered critical Go authentication/authorization, tenant/project context, streaming/tunnels, delivery and idempotency, React catalog/configuration and dashboard state, CI, Helm, backup/restore implementation, signature policy, catalog generation, and current local Kubernetes operations. Reviewers consulted the architecture decisions before classifying findings. This is a broad, risk-weighted review, not an exhaustive proof of every package or integration.

## Operational work completed

- Restored `k3d-ui-demo-agent-0` from NotReady. Its containerd CRI plugin could not start an inotify watcher. The host limit was 128 instances. Set `fs.inotify.max_user_instances=1024`, persisted in `/etc/sysctl.d/90-astronomer-local-clusters.conf`, and restarted that node. Both original nodes became Ready.
- Registered the legacy `ui-demo` with the deployed HTTPS management plane instead of its retired `host.k3d.internal:8000` development server. Preserved its old identity and empty delivery checkpoint locally, then removed those obsolete bindings so the new registration could establish its own identity. Existing workloads were retained.
- Found that Kubernetes 1.30.4 is outside the signed v2.1.1 delivery range of 1.33–1.35. Created a separate two-node `ui-demo-v211` on K3s 1.35.7, keeping the original cluster intact. No in-place multi-minor upgrade or data deletion was attempted.
- Registered the supported replacement through the UI and applied its issued agent/delivery manifest. Both nodes and its v2.1.1 agent are Ready; signed delivery state is accepted. Cluster ID: `10a06091-4d36-4e49-a9ff-78480522f9d0`. Its kubeconfig is saved privately at `/root/.kube/astronomer-ui-demo-v211.yaml`; the default context was not changed.
- Installed the temporary `review-smoke` Alertmanager application through Catalog → full-page configuration → confirmation. Form/YAML switching preserved edited settings. The operation navigated to Applications and displayed request and delivery progress separately. The resulting HelmRelease reached Ready=True, the application pod reached 2/2 Ready, and its `/-/ready` endpoint returned `OK`. Uninstalled the temporary release through the UI afterward and verified its HelmRelease and application pod were removed. The 390px mobile view had no horizontal document overflow; desktop/mobile screenshots are retained in the evidence directory.

The supported replacement is a fresh cluster; existing demo workloads/data were not migrated from the preserved legacy cluster. Its baseline add-ons and image scanning were deliberately left off during registration to keep this verification bounded.

**Replacement qualification is incomplete:** application delivery succeeded, but at 05:52 UTC the system Kustomization reported HealthCheckFailed against the intentional source-controller standby (STATUS-01). The Apps list also retained stale installing/uninstalling labels despite the underlying resource transitions (UI-03). Keep the original cluster until these issues are resolved and qualification is repeated.

## Current management-plane health

- K3s management node Ready; Astronomer server, worker, frontend, Redis, and CNPG ready. CNPG reports a healthy cluster.
- Current TLS certificates Ready; all listed persistent volume claims Bound.
- Today's scheduled management backup and retention jobs succeeded. This deployment does not mount the optional catalog trust directories implicated in BACKUP-01.
- Core cluster registration, live inventory, catalog configuration, and application delivery responded successfully through the production UI/API.
- Optional Charlie integration is disabled. Its two running agents are unready because enrollment/refresh credentials expired; bridge health is reachable, enrollment readiness is 503, and the bridge Service has no ready endpoints. This is not a core management-plane outage. Restarting with the same expired enrollment will not repair it. Re-enroll via the supported workflow if wanted, or suspend/uninstall via the documented integration lifecycle while preserving retained identity. No credentials were exposed or changed.

## Prioritized confirmed findings

All findings below have HIGH confidence from source inspection; authentication, idempotency, backup, widget behavior, dotted keys, and bundle size additionally have isolated reproductions. Effort includes tests; S means hours, M approximately a day, L multiple days.

| Priority | ID | Finding | Category | Effort | Fix risk |
|---|---|---|---|---|---|
| Blocker | AUTH-01 | Streaming accepts purpose-bound pre-MFA JWTs | Security | S | Low |
| Blocker | AUTH-02 | Streaming ignores API-token IP restrictions | Security | S–M | Medium |
| Blocker | AUTH-03 | Ordinary protected API accepts refresh JWTs | Security | S | Low |
| High | DELIVERY-01 | Idempotency conflates different resources/project contexts | Correctness | S | Low |
| High | BACKUP-01 | Optional private catalog trust breaks backup creation | Reliability | M | Medium |
| Medium | UI-01 | Widget fetch lifecycle stalls or duplicates requests | Correctness/performance | M | Medium |
| Medium | UI-02 | Literal dotted configuration keys lose edits | Correctness | S | Low |
| Medium | UI-03 | Applications list retains stale delivery status | Correctness | S–M | Low |
| Medium | BUILD-01 | Current bundles exceed budgets absent from CI | Performance/tooling | M | Low–medium |
| Medium | DOC-01 | Recovery instructions use obsolete schema and tolerate restore errors | Operations/docs | S | Low |
| Medium | POLICY-01 | Documentation overstates image admission verification | Security/docs | S | Low for docs |
| High | STATUS-01 | Flux standby causes failed system rollout health and degraded inventory | Correctness/reliability | M | Medium |

### AUTH-01 — Restrict streaming JWTs to access tokens

`internal/auth/streamauth.go:105` validates the JWT and returns its user ID without checking token type. `internal/handler/totp_login.go:26` creates purpose-bound challenges before MFA completion. Exec/log routes are outside ordinary auth middleware (`internal/server/routes_long_lived.go:82`), and the exec consumer uses the stream validator (`internal/tunnel/exec_consumer.go:158`). A synthetic pre-MFA challenge was accepted by that boundary. RBAC permission is still required, but the second factor is bypassed for those privileged operations.

Require AccessToken at the shared stream JWT boundary. Cover both TOTP purposes, refresh JWTs, and rejection before WebSocket upgrade. Keep purpose-specific tokens usable only at their intended endpoints. Treat as a release blocker.

### AUTH-02 — Apply API-token CIDRs consistently to streams

`internal/auth/streamauth.go:81–102` checks token expiration, active owner, and write scope but omits AllowedCidrs. Ordinary REST auth enforces it at `internal/server/middleware/auth.go:136`. A synthetic restricted token was accepted from a disallowed documentation IP range by the stream boundary.

Share restrictions between REST and streaming, including fail-closed parsing and trusted client IP resolution. Test direct and sibling-forwarded connections. Do not introduce trust in arbitrary forwarding headers to fix this.

### AUTH-03 — Reject refresh JWTs as API access credentials

`internal/server/middleware/auth.go:181` excludes purpose tokens but not refresh tokens. `internal/auth/jwt.go:525` deliberately validates access and refresh tokens. The dedicated refresh endpoint has separate cookie/CSRF and rotation handling (`internal/handler/auth_sessions.go:24,80`). A synthetic refresh JWT reached a protected handler and returned 204 instead of 401.

Require AccessToken for ordinary API authentication, including bearer and session-cookie inputs. Retain dedicated refresh validation and rotation. Coordinate with AUTH-01 so both boundaries have the same token-purpose contract.

### DELIVERY-01 — Bind idempotency to concrete targets and resolved project

`internal/server/middleware/idempotency.go:348` selects a chi route pattern, discarding actual IDs. Both its request digest (`:341`) and cache key (`:422`) use it. Actual delivery mutations attach this middleware at `internal/server/routes_delivery.go:141`. Header-selected project context is absent from the digest too.

Two distinct target URLs with the same key/method/body executed only one synthetic mutation: the second request replayed the first result. Use concrete canonical request paths and authoritative project scope in identity/digest computation. Tests must distinguish different IDs/projects from genuine retries.

### BACKUP-01 — Preserve catalog trust using a compatible archive format

The chart creates `catalog-ca/` and `catalog-signature-trust/` under the key source directory when optional trust Secrets are configured (`deploy/chart/templates/management-plane-backup-cronjob.yaml:138–148`). The real bundle creator rejects directories (`internal/drcrypto/keybundle.go:53–56`); extraction also rejects nested paths (`:118`). A synthetic flat bundle passed; adding a catalog trust directory failed.

These supported configurations cannot publish a completed backup manifest. Define a bounded, compatible representation of optional trust material and restore it symmetrically. Preserve archive path protections. Test the rendered script against the real CLI for both optional trust modes and roundtrip extraction.

### UI-01 — Give widgets a scope-aware query lifecycle

`frontend/src/components/dashboards/widget-grid.tsx:52–99` updates the fetcher without resetting state/fetching, schedules one whole-dashboard request per widget, and stops refreshing on an initial failure. Cluster and project mounts lack a scope key (`frontend/src/routes/dashboard/clusters/$id/-page.tsx:264`, `frontend/src/routes/dashboard/projects/$id/index.tsx:71`). Isolated execution of the real component confirmed no initial-error retry, no new fetch after changing an empty scope, and duplicate same-cadence requests.

Use a scope-keyed query with cancellation, retry, and one bounded refresh schedule. Preserve intended cadence without coupling new-scope behavior to every parent rerender.

### UI-02 — Preserve literal dots in configuration paths

`frontend/src/lib/catalog-configuration.ts:60` joins path segments with dots. `frontend/src/components/clusters/tool-catalog-fields.tsx:61–62` splits them again for reads/writes. An actual helper reproduction showed an edit to an annotation key containing dots retained the old value.

Use segment arrays for machine paths and separate display formatting. Test literal dots, slashes, annotations, and ordinary nested objects.

### UI-03 — Project authoritative delivery status into Applications

`internal/handler/catalog_installed_enriched.go:70–73,148` returns persisted installation status directly. It does not invoke `refreshInstalledChartStatus` in `internal/handler/catalog_installations.go:416`, which maps Delivery ready to installed and removed to uninstalled, deleting the obsolete compatibility row. Legacy installation lists invoke that helper, but the enriched Apps list does not.

Live verification reproduced an Apps row stuck at installing while Delivery reported ready and the application was healthy. After UI uninstall, the HelmRelease and workload were gone while the row remained uninstalling. The missing projection is confirmed; final uninstall-operation completion was not established. Repeated polling of this list cannot itself refresh its compatibility record.

Use a shared or ingestion-maintained projection from authoritative Delivery state. Test installation becoming ready and completed removal disappearing, including consistent pagination counts.

### BUILD-01 — Restore and enforce bundle ceilings

A fresh normal Vite build in an isolated source copy produced login closure **440,744 gzip bytes vs 428,032 limit** and application closure **497,520 vs 489,472**. Bootstrap remained within budget. `frontend/package.json:13–14` separates build from budget checking, and `.github/workflows/pr-validation.yaml:107–110` never runs the latter.

Add bundle:check after the CI production build and reduce eager imports to meet existing ceilings. Do not silently increase the budgets. Fresh build and CSP checks passed; exceeding budgets is a separate result.

### DOC-01 — Make recovery instructions fail closed and use Goose

`docs/management-plane-dr-runbook.md:366` tells operators to tolerate pg_restore errors; `:380` checks legacy schema_migrations. `internal/db/migrations/migrations.go:39` uses public.goose_db_version; `docs/runbooks/upgrade-2.1.md:44` says the old table is inert.

Require successful restoration and current Goose schema verification before re-enabling writers. Rehearse the corrected instructions in a disposable database. A current successful backup alone is not proof of a successful restore.

### POLICY-01 — Correct admission assurance claims

`docs/decisions/0015-image-signature-admission.md:33` and `docs/verify-images.md:168–171` describe cryptographic upstream admission checks. `deploy/policy-controller/cip-astronomer.yaml:51–141` instead documents compatibility exceptions and uses static pass for four upstream image repository groups.

Distinguish offline digest verification from admission verification and record the exceptions truthfully. Separately qualify digest-scoped exceptions or a compatible verifier. Blindly switching to unsupported keyless authorities could block deployments.

### STATUS-01 — Make rollout and inventory health account for Flux standby

`internal/agent/delivery/probe.go:163–169` intentionally accepts one elected source-controller with a warm standby; `probe_test.go:40` covers it. Generic inventory classification feeds 2 desired/1 ready into replicaHealth (`internal/agent/delivery/system_inventory.go:151,184–189,221,422–427`), which reports degraded.

Share the controller-specific health interpretation while retaining actual replica counts. Test warm standby, no available leader, and stale deployment generation. Do not restart or relax probes on a healthy standby merely to make all replicas show Ready.

The live replacement also exposed a stronger consequence: `internal/agent/delivery/system.go:352` creates the system Kustomization with `wait: true` and no controller-specific health expression. At 05:52:36 UTC it reported Ready=False / HealthCheckFailed after ten minutes, explicitly naming source-controller. That Deployment had one available of two replicas, Available=False / MinimumReplicasUnavailable, and Progressing=False / ProgressDeadlineExceeded. Helm and kustomize controllers were 2/2; application install and removal had succeeded. The special handling in the agent probe therefore does not extend to Flux's own rollout gate.

Qualify a controller-specific rollout health strategy that accepts the intended standby without hiding absent leadership, stale generations, or real failed upgrades. Add a fresh-cluster integration check that waits beyond the Deployment progress deadline and requires the system Kustomization to become Ready. Treat the replacement as partially qualified until this is corrected.

## Verification results and limits

- **Go packages:** initial `go test -p 4 ./...` passed 127 packages, with 12 reporting no test files. Only upgradecli failed because `/tmp` is a 4 GiB tmpfs and the production capacity guard requires at least 20 GiB remaining. The complete failing package passed unchanged when TMPDIR used a private directory on the root filesystem. No guard was bypassed. This exposes a test-portability assumption, not a demonstrated upgrade failure.
- **Additional security regressions:** four synthetic regression checks failed as expected for AUTH-01/02/03 and DELIVERY-01. Existing auth, middleware, RBAC, and tunnel suites passed. The parent independently reran the failing checks. No live MFA-bypass exercise was performed.
- **Security tools:** make security passed. Gosec returned no findings at configured thresholds; govulncheck reported no reachable vulnerabilities (one module-only advisory); redacted gitleaks scanned 2,304 commits with no leaks. These scanners do not detect the confirmed business-logic auth defects.
- **Frontend:** typecheck, lint, fresh build, CSP check passed. Full Vitest: 2,430/2,432 tests passed, 350/351 files. The two pagination failures passed in an isolated four-test rerun; record the suite timing sensitivity rather than claiming an unqualified green run.
- **Dependency audit:** npm audit reports critical DOMPurify/Monaco package entries and fails the configured high-severity gate. The inspected sanitizer does not enable the advisory's affected modes; reachable exploitation was not established. Triage/upgrade is still required to restore that gate.
- **Browser:** 19/20 initial deployed live-project checks passed, including ten accessibility pages and UI/navigation checks. The sole failure pointed metrics at development localhost:9090. Rerunning against the deployed listener through a loopback port-forward passed, covering the 20th check. No browser stubs were used.
- **Platform/catalog:** Helm lint/template and deploy/releasecontract/catalog/distribution/builtin/drcrypto packages passed. Catalog schema, archive-lock coverage, and all 35 generated outputs were current. The catalog's 49-test run had five errors because installed Python 3.14 lacks a Python 3.15 API; the repository requires 3.15. This is an environment limitation.
- **Not established:** fresh disaster-recovery restore, production HA failover, scale/load, every RBAC/tenant permutation, every catalog offering, air-gap operation, external cloud environments, and a complete Charlie service code audit. Existing release evidence is not relabeled as newly tested here.

Verification logs are under [evidence/2026-10-23](evidence/2026-10-23). Credential-bearing manifests, cookies, onboarding material, and browser storage state are excluded.

## Considered and rejected / lower-priority observations

- Flux source-controller standby readiness is intentional; inconsistent inventory classification and failed system rollout health are the findings, not proof that a healthy standby should serve artifacts.
- Missing optional Trivy/Gateway CRDs during a minimal demo registration cause retry logs, not proof of failed core registration.
- Python mismatch and upgradecli temporary-filesystem constraints were not classified as production defects.
- Frontend dependency advisory reachability was not proven; package audit output alone was not labeled a confirmed XSS exploit.
- Planned flat-handler/RPC and other documented architecture transitions were not treated as defects solely because migration is incomplete.
- No feature roadmap is recommended ahead of authentication and recovery fixes.

## Recommended next work

Fix AUTH-01/02/03 together first, with negative tests for token purpose, refresh lifetime, CIDRs, and trusted forwarding. Then address DELIVERY-01, BACKUP-01, and STATUS-01 with real boundary/roundtrip and fresh-cluster tests. Follow with UI correctness and CI budgets; correct recovery and admission documentation immediately alongside those changes. Keep source remediation and a new release/deployment distinct from the operational recovery performed in this review.
