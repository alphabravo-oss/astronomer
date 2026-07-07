# 009 — Namespace-Scoped RBAC: Promote-or-Keep-Opt-In Decision + kubectl-Shell Scope Spike

Date: 2026-07-07
Status: Proposed (decision required before any code)
Finding: DIR-04
Owner: (assign)
Scope: The `namespace_scoped_rbac_enabled` authorization filter (config flag, default OFF) and the in-browser kubectl-shell scope, which is derived from the shell's own cluster-wide ClusterRole rather than the requesting user's effective namespace grants.

---

## 1. Summary of the ask

Two related multi-tenancy gaps sit behind default-OFF flags and have soaked dark:

1. **Namespace-scoped cluster reads** are fully implemented but gated by `namespace_scoped_rbac_enabled` (config), default `false`. When ON, a project/namespace-restricted user can reach cluster LIST routes and see only their authorized namespaces; when OFF those users get nothing on cluster routes (byte-identical to pre-feature). It has never been promoted.
2. **The kubectl shell** provisions a **cluster-wide** ClusterRole for the session ServiceAccount (`internal/kubectl/manifests.go:172` — `apiGroups:["*"], resources:["*"]` across all namespaces). Its scope is the SA's blanket grant, **not** the requesting user's effective namespace grants. A namespace-limited user who passes the `clusters:update` route gate gets a cluster-wide reach. A partial opt-in mitigation exists (`feature.shell_scope_to_caller`, also default OFF) but only caps verbs and *audits* out-of-scope targets — it does not truly enforce per-namespace confinement.

This plan (a) documents exactly what each flag changes and what is still not wired, (b) states the decision the maintainer must make on promoting the authorization filter, with a recommendation and a safe rollout, and (c) specifies the concrete change to intersect kubectl-shell scope with the user's effective namespace grants.

**This plan writes no code.** It is a decision + spike document.

---

## 2. What the `namespace_scoped_rbac_enabled` flag changes TODAY

The flag is defined at `internal/config/config.go:114` (`NamespaceScopedRBACEnabled`, mapstructure key `namespace_scoped_rbac_enabled`) and registered as a bindable env/config key at `internal/config/config.go:237`. Default is `false` (`config_test.go:51` asserts the zero-value default is off). It fans out to exactly three seams, all wired from the one flag in `internal/server/server.go`:

### 2a. Binding expansion — `internal/server/middleware/rbac_queries.go`
- Constructed at `server.go:375`: `NewSQLCRBACQuerierWithNamespaceScoping(queries, cfg.NamespaceScopedRBACEnabled)`.
- When ON, `GetUserBindings` calls `expandProjectBindings` (`rbac_queries.go:199`, `:217`): every `project` binding is expanded into **synthetic namespace-scoped cluster bindings**, one per `(cluster_id, namespace)` row the project owns (`ListProjectNamespaces`, `rbac_queries.go:235`). The original project binding is retained. Fails closed: a DB error resolving a project's namespaces propagates as an error, denying the request (`rbac_queries.go:236`).
- When OFF, the block at `:199` is skipped entirely — project bindings are emitted as before and grant nothing on cluster resource routes. Byte-identical.

### 2b. LIST-route admission gate — `internal/server/middleware/rbac.go` + `routes_resources_workloads.go`
- `RequireListPermission` (`rbac.go:118`): with `namespaceScoped==true` and a **bare** list (no `?namespace=`), a caller who fails the plain `CheckPermission` falls back to `engine.HasAnyNamespaceAccess` (`rbac.go:176-177`), which admits a user holding the permission in ≥1 namespace on the cluster. A request that pins a specific `?namespace=` gets no fallback — an unauthorized namespace still 403s (`rbac.go:172`).
- Wired at `routes_resources_workloads.go:176` (workloads), `:186` (pods). `requireNamespacePickerListPermission` (`:182`, `:185`) does the analogous thing for the namespace picker + events, keeping `clusters:read` as the primary check so the flag-OFF path is byte-identical (`routes_resources_workloads.go:25-40`).

### 2c. Handler-side result filtering — `internal/handler/workloads.go` + `authorization.go`
- Enabled *together* with the gate at `server.go:390` (`workloadHandler.SetNamespaceScopedRBAC(...)`). The comment at `server.go:387` is the load-bearing invariant: **gate and filter must be enabled together or results leak** — the gate admits scoped users, the filter narrows their results.
- `authorizedNamespaces` (`authorization.go:161`) returns `all==true` (fast path, no DB) when the flag is off, and otherwise the caller's exact namespace allow-set via `engine.AuthorizedNamespaces` (`engine.go:222`).
- Applied in the list handlers: workloads (`workloads.go:426` → `filterItemsByNamespaceKey :432`), namespaces (`:745`/`:751`), events (`:811`/`filterEventsByNamespace :817`), pods (`:834`/`:840`). All filters are **strict allow-lists** — an item with a missing/empty namespace key is dropped, so a scoped caller never sees cluster-scoped/unlabeled objects (`workloads.go:143-155`).

### 2d. Raw k8s-proxy LIST filtering — `internal/server/routes.go`
- Wired at `routes.go:1057` into `requireK8sProxyPermission`. When ON, for a plain cluster-wide **GET + VerbList + namespace=="" + not-a-watch** that both coarse and native checks denied, it computes `engine.AuthorizedNamespaces` (`routes.go:1341`), folds in any native per-namespace list grants (`:1358`), and stashes the allow-set via `tunnel.WithNamespaceFilter` (`routes.go:1378`) so the tunnel proxy filters the buffered list body. Watches, mutations, named GETs, and users with no namespace access keep failing closed (`routes.go:1335-1339`).

### What is STILL NOT wired (with the flag ON)
- **Namespace-scoped binding *authoring*.** The effective-permissions endpoint already understands a namespace context but explicitly warns that storage + assignment UI are pending: `internal/handler/rbac_effective.go:242` ("namespace context is enforced for namespace-scoped bindings when present; binding storage and assignment UI are still pending"). Project→namespace expansion (2a) is the only production path that produces namespace-scoped cluster bindings today; there is no first-class UI to bind a role to `(cluster, namespace)` directly.
- **Watches / streaming** are never namespace-filtered on the k8s-proxy path — they fail closed (`routes.go:1339`). A scoped user gets no live updates on cluster resources.
- **Mutations** on cluster-proxy routes are not namespace-filtered — only LIST reads are. A scoped user can read (filtered) but write paths depend entirely on `CheckPermission`/native rules.
- **The kubectl shell** does not consult this flag at all — see §4. Its scope is a *separate* opt-in.

---

## 3. DECISION: promote `namespace_scoped_rbac_enabled` by default, or keep opt-in?

This is a **security-semantics change**, not a config default tweak. Flipping an authorization *filter* on changes what existing users see. The maintainer must decide explicitly.

### The risk of flipping the default: who could LOSE visibility
Turning the flag ON changes behavior for **project-scoped users** in a way that can subtract visibility:

- Today (flag OFF), a project binding **grants nothing on cluster routes** — those users get a hard 403 on cluster LIST endpoints. Turning it ON *grants them a filtered view*: that direction is purely additive and safe.
- The subtractive risk is the **strict allow-list drop** (`workloads.go:143`, `routes.go:1377`). If any deployment today relies on a *cluster-scoped* binding whose namespace field is empty but who also holds a namespace-scoped binding, the `AuthorizedNamespaces` short-circuit at `engine.go:256` returns `all==true` and they keep full visibility — so a genuinely cluster-wide user is unaffected. The users who could lose visibility are those whose effective grant is **only** namespace-scoped and whose expected resources carry **no namespace label** (cluster-scoped objects, or objects the agent returns without a `metadata.namespace`): those items are dropped by the fail-closed filter. This is correct behavior but it is a *visible change* for anyone who had been seeing those objects through a coarse grant.
- **Correctness dependency:** the gate (2b) and filter (2c) must flip together (`server.go:387`). They already do because both read the one flag — but any future partial rollout (e.g. a per-cluster override) must preserve this invariant or leak unfiltered results.

Net: promoting is **mostly additive** (project users gain a scoped view they didn't have). The real hazards are (i) fail-closed drops of unlabeled/cluster-scoped objects for namespace-only users, (ii) the missing binding-authoring UI (§2 gap) meaning the only namespace grants in the wild come from project membership, and (iii) per-request DB cost of project→namespace expansion.

### Recommendation: **KEEP OPT-IN for one release; ship promotion tooling; promote by default only after a measured bake.**

Rationale / trade-offs:
- **Keep opt-in (recommended now).** Pro: zero risk of surprise visibility changes; lets design partners turn it on deliberately. Con: the multi-tenancy story stays dark and Rancher-parity remains unmet; features soaking dark tend to bit-rot.
- **Promote by default now.** Pro: closes the parity gap immediately, and the change is mostly additive. Con: no in-product way to author namespace bindings yet (`rbac_effective.go:242`), so the promoted behavior only benefits project members; fail-closed drops of unlabeled objects are a support-ticket risk; per-request expansion cost is unmeasured at scale.

The blocker to *default-on* is not correctness (the seams are complete and fail closed) — it is the **missing binding-authoring UI** and an **unmeasured per-request cost**. Promote by default only once those two are addressed. Until then, ship it as a documented, per-deployment opt-in with a migration note, which is a strictly better posture than leaving it undocumented-dark.

---

## 4. kubectl-shell scope: intersect with the user's effective namespace grants

### Current behavior (the DIR-04 escalation)
- Opening a shell only proves the `clusters:update` route gate (`internal/handler/kubectl_shell.go:264`, verbs via `effectiveVerbsFor`).
- `kubectl.Open` (`internal/kubectl/session.go:140`) provisions SA → **ClusterRole** → ClusterRoleBinding → Pod. The ClusterRole is cluster-wide `["*"]/["*"]` (`internal/kubectl/manifests.go:176-180`); superuser gets `cluster-admin` directly (`manifests.go:173`). **There is no namespaced Role** — the SA can reach every namespace, i.e. the shell's scope is the SA's blanket grant, not the caller's namespace grants.
- A partial mitigation already exists behind `feature.shell_scope_to_caller` (platform setting, default OFF — `kubectl_shell_scope.go:59`, evaluated at `kubectl_shell_scope.go:280-285`). When ON:
  - `deriveCallerScope` (`kubectl_shell_scope.go:107`) derives an envelope from the caller's own bindings: superuser → full; a cluster/global binding → cross-namespace; a namespace-scoped binding → that namespace **capped at read-only** (`kubectl_shell_scope.go:148-156`), because a ClusterRole can't express a per-namespace write.
  - Open-time it narrows `verbs` and **fails closed** on an undetermined scope (`kubectl_shell.go:273-287`); HandleWS re-derives + fails closed before the WS upgrade (`kubectl_shell.go:544-553`).
  - Per-command it is only a **detective** control: out-of-scope namespace targets are audited/logged but **not blocked** (`kubectl_shell.go:633-665`), because `onInput` has no back-channel to suppress the forwarded frame.

So even with the mitigation ON, a namespace-restricted caller's *reads* are still cluster-wide at the apiserver — the SA ClusterRole is unchanged; only verbs are capped and violations are logged. True per-namespace confinement is not achieved.

### The change: make shell scope the intersection of caller grants AND enforce it
Two seams already carry the caller's effective grants; wire them into actual enforcement.

1. **Reuse the effective-grant computation.** `deriveScopeForCaller` (`kubectl_shell_scope.go:290`) already calls `h.Bindings.GetUserBindings`, and because the shell handler is constructed with the *same* `rbacQuerier` (`server.go:1282`, `1947`), when `namespace_scoped_rbac_enabled` is ON those bindings **already include the project→namespace synthetic cluster bindings** (§2a). So the caller's effective namespace set is available. Replace the conservative `bindingAppliesToCluster` project handling (`kubectl_shell_scope.go:169-188`, which admits a project binding as "applicable" but contributes no concrete namespace) with `engine.AuthorizedNamespaces(bindings, ResourcePods, VerbList, clusterID)` (`engine.go:222`) so the scope's `Namespaces` set is the caller's real effective grant, not a coarse "determined but empty."

2. **Enforce, don't just audit.** Pick one enforcement mechanism (spike both, choose one):
   - **(A) Namespaced Role manifests.** In `internal/kubectl` (`manifests.go:172`, `session.go:185-199`), when the derived `CallerScope.AllNamespaces==false`, emit one namespaced `Role` + `RoleBinding` per authorized namespace instead of the cluster-wide ClusterRole. This lets namespace-scoped callers get **write** verbs safely (removing the read-only cap at `kubectl_shell_scope.go:148`) because the grant is genuinely confined. Cost: N Role/RoleBinding objects per session, teardown must delete them all (`session.go:429`).
   - **(B) Impersonation on the exec proxy.** `CallerScope.ImpersonationHeaders()` already exists (`kubectl_shell_scope.go:229`) and emits `Impersonate-User: astronomer:user:<id>`. Have the exec proxy attach these on every apiserver call so the apiserver evaluates the *user's* RBAC, not the SA's blanket grant. Cost: requires per-user RBAC to actually exist in the target cluster (may not for adopted clusters) and touches the exec relay path (`kubectl_shell.go:685`, `Exec.ProxyToAgentWithInputRecorder`).
   - Recommendation: **(A)** for confinement that works on any adopted cluster without pre-provisioned per-user RBAC; keep (B)'s header plumbing as defense-in-depth.

3. **Collapse the two flags.** Today `feature.shell_scope_to_caller` and `namespace_scoped_rbac_enabled` are independent. Once (1)+(2) land, gate shell scoping on the same `namespace_scoped_rbac_enabled` decision (or make `feature.shell_scope_to_caller` default to it) so an operator makes **one** multi-tenancy choice, not two. Keep the fail-closed contract at `kubectl_shell.go:276` and `:549`.

File:line seams to touch: `internal/handler/kubectl_shell_scope.go:107` (`deriveCallerScope`), `:148-156` (verb cap — relax under mechanism A), `:169-188` (`bindingAppliesToCluster` → `AuthorizedNamespaces`); `internal/kubectl/manifests.go:172` + `internal/kubectl/session.go:185-199`/`:429` (namespaced Role emit + teardown); `internal/handler/kubectl_shell.go:633-665` (upgrade detective→preventive once enforcement exists); wiring at `server.go:1282`/`1420`.

---

## 5. Safe rollout

### 5a. Measure per-request filter overhead (gating data for a default flip)
- **project→namespace expansion cost.** `expandProjectBindings` (`rbac_queries.go:217`) issues one `ListProjectNamespaces` per distinct project the user holds, on every cache miss. The 15s TTL LRU cache (`rbac_queries.go:33`, `NewRBACCache`) absorbs repeats, but the cold path scales with (# projects × # namespaces). Instrument: added latency on `GetUserBindings` (cold vs warm), extra DB queries/sec at the target scale profile, and cache hit ratio. Use `scripts/loadtest` scale profiles (small/medium/large/extreme) referenced in the day-2 parity plan.
- **filter cost.** `filterItemsByNamespaceKey`/`filterEventsByNamespace` (`workloads.go:143`,`:159`) are O(items) per list; the k8s-proxy path buffers the whole list body to filter (`routes.go:1378`). Measure added latency + memory on large (10k+ object) list responses.
- **Acceptance bar (proposed):** p95 `GetUserBindings` cold path < X ms and warm < 1 ms; no measurable regression on cluster LIST p95 with the flag ON at the "large" profile.

### 5b. Opt-out / staged enablement
- Keep the single boolean `namespace_scoped_rbac_enabled` as the master switch. Provide an env override so a deployment can flip it back OFF instantly if a scoped user reports lost visibility (the flag flip invalidates the RBAC cache via `SetNamespaceScoping` → `InvalidateAll`, `rbac_queries.go:68-75`, so toggling is safe at runtime).
- Do **not** promote the default without first shipping the namespace-binding authoring UI (close the `rbac_effective.go:242` gap) — otherwise default-on benefits only project members.

### 5c. Migration note (ship with the feature)
> Enabling `namespace_scoped_rbac_enabled` changes cluster-resource visibility for project-scoped users. **Additive:** project members gain a namespace-filtered view of workloads/pods/namespaces/events that they were previously 403'd from. **Subtractive:** a user whose *only* effective grant is namespace-scoped will no longer see cluster-scoped or unlabeled objects on cluster LIST routes (strict fail-closed allow-list). Watches and mutations on the raw k8s-proxy remain governed by exact `CheckPermission`/native rules — scoped users get no live/streamed cluster-resource updates. Verify your project→namespace mappings (`project_namespaces`) are populated before enabling; an empty mapping means project members see nothing. The flag is runtime-togglable and invalidates the RBAC cache on flip.

---

## 6. Bake / validation plan

1. **Unit/contract (already present — extend):** `workloads_nsfilter_test.go`, `routes_k8sproxy_nsrbac_test.go`, `routes_k8sproxy_native_nsrbac_test.go`, `rbac/namespace_scope_test.go`, `kubectl_shell_scope_test.go`. Add cases for: unlabeled-object drop, project→namespace expansion with multi-cluster projects, and shell scope derived from a project-only user once §4(1) lands.
2. **Gate = filter invariant test.** Add a regression asserting that enabling the gate without the filter (or vice versa) fails — encode the `server.go:387` invariant so it can't silently break.
3. **Full gate:** `go test ./...`, `npm run type-check`, `npm test -- --runInBand`, `git diff --check` (per repo memory: run the full gate and fix what it surfaces).
4. **Live bake in a lab cluster:** with the flag ON, drive as (a) superuser, (b) cluster-wide-bound user, (c) project-only user, (d) namespace-only user. Confirm each sees exactly their authorized namespaces on workloads/pods/namespaces/events and the raw k8s-proxy LIST, and that watches/mutations behave per §5c. Verify the fail-closed 500 on a binding-lookup error (don't fall back to showing everything — `authorization.go:161` contract).
5. **Shell bake:** with §4 landed, open a shell as a namespace-only user and confirm `kubectl get pods -n <other-ns>` is *denied by the apiserver* (mechanism A/B), not merely audited; confirm the out-of-scope audit row still emits (`kubectl_shell.go:659`).
6. **Perf bake:** run §5a instrumentation at the "large"/"extreme" load profiles; compare flag-ON vs flag-OFF p95 and DB QPS against the §5a acceptance bar.

---

## 7. Explicit decision checklist for the maintainer

- [ ] **DECISION 1 — promote `namespace_scoped_rbac_enabled` default?** Recommendation: **keep opt-in now**, promote-by-default only after the binding-authoring UI (`rbac_effective.go:242`) ships and §5a perf bar is met. Ship the migration note (§5c) with the current opt-in.
- [ ] **DECISION 2 — shell enforcement mechanism:** namespaced Role manifests (A, recommended) vs impersonation headers (B) vs both. Then collapse `feature.shell_scope_to_caller` into the same master switch (§4.3).
- [ ] **DECISION 3 — scope of "not wired" work to fund now:** namespace-binding authoring UI/storage, and whether watches/mutations on cluster-proxy routes get namespace filtering or stay `CheckPermission`-only.
