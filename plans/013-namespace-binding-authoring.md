# 013 — Namespace-Scoped Binding Authoring (the DIR-04 unblocker)

Date: 2026-07-07
Status: Proposed
Finding: DIR-04 (companion to plan 009)
Owner: (assign)
Scope: Give an operator a first-class, RBAC-gated way to **author, list, and revoke** a role binding scoped to a specific `(cluster, namespace)`, so that `namespace_scoped_rbac_enabled` can be promoted without confined users failing closed with no UI to grant them access. Backend storage/engine/spec already support this; the gap is validation + a wired UI + flipping the "pending" warning.

---

## 1. Why

Plan 009 documents that namespace-scoped cluster reads are fully implemented but gated behind `namespace_scoped_rbac_enabled` (default OFF), and explicitly lists **"Namespace-scoped binding *authoring*"** as the one thing still not wired, citing `internal/handler/rbac_effective.go:242`. Today the *only* production path that produces a namespace-scoped cluster binding is the flag-gated project→namespace expansion in `internal/server/middleware/rbac_queries.go:192-205`. There is no way for an operator to bind a user to a role scoped to one namespace directly, so a confined user who needs access to a single namespace cannot be granted it — the feature cannot be safely promoted.

The surprising finding from investigation: **the backend, storage, engine, and OpenAPI spec already support a namespace-scoped cluster role binding end-to-end.** The namespace column exists, the create/list/delete queries carry it, the handler accepts and audits it, the privilege-escalation guard evaluates it, and the engine honors it. What is actually missing is small: (a) input validation of the namespace string on create, (b) a frontend that is wired to the *real* binding endpoints at all (the current UI points at a route that does not exist), and (c) flipping the stale "pending" warning.

---

## 2. Current state (with file:line)

### 2a. Storage — namespace column ALREADY EXISTS. No migration needed.
- `internal/db/migrations/113_cluster_role_binding_namespace.up.sql` adds `ALTER TABLE cluster_role_bindings ADD COLUMN namespace VARCHAR(253) NOT NULL DEFAULT ''`. Empty string == full cluster scope, matching `rbac.RoleBinding.Namespace` semantics.
- The sqlc model already carries it: `internal/db/sqlc/models.go:775` — `Namespace string \`json:"namespace"\`` on `type ClusterRoleBinding struct`.
- `global_role_bindings` and `project_role_bindings` (`internal/db/migrations/001_initial.up.sql:179`, `:231`) have **no** namespace column — namespace scoping lives only on the cluster binding, which is correct (a namespace belongs to a cluster).

### 2b. Queries — already namespace-aware.
- `internal/db/queries/rbac.sql`: `CreateClusterRoleBinding` inserts `(user_id, "group", role_id, cluster_id, namespace)` with `namespace = $5`.
- `ListClusterRoleBindings` / `ListClusterRoleBindingsByCluster` are `SELECT *`, so they return the namespace column.
- `ListUserBindingsWithRoles` (the middleware fan-in) already selects `cb.namespace AS namespace` for the cluster arm and emits `''` for global/project arms.

### 2c. Engine — already honors namespace.
- `internal/rbac/engine.go:272` (`bindingApplies`): `if b.Namespace != "" && b.Namespace != namespace { return false }`, plus a guard at `:275` that a namespace-scoped binding with no cluster/project scope never applies.
- `internal/server/middleware/rbac_queries.go:183` materializes `binding.Namespace = row.Namespace` for cluster bindings, so a persisted namespace-scoped binding is honored today at authorization time.

### 2d. Handler — already accepts, guards, and audits namespace.
- `internal/handler/rbac.go:172-181` — `roleBindingRequest` has a `Namespace string \`json:"namespace"\`` field.
- `internal/handler/rbac.go:581-626` — `CreateClusterRoleBinding` reads `req.Namespace`, passes it to `guardClusterBinding(w, r, roleID, clusterID, req.Namespace)` (`:600`), persists it via `CreateClusterRoleBindingParams{... Namespace: req.Namespace}` (`:608`), and audits it under key `"namespace"` (`:621`).
- `internal/handler/rbac.go:889-899` — `guardClusterBinding` → `enforceNoEscalation(..., namespace)` (`:930`) calls `engine.CheckPermission(callerBindings, resource, verb, clusterID, uuid.UUID{}, namespace)` (`:951`), so the "you cannot grant permissions you do not hold" check is already evaluated **at the namespace scope**.
- `internal/handler/rbac.go:1035` — `bindingResponse` returns the raw `sqlc.ClusterRoleBinding`, so the `namespace` field is already in the create + list JSON.
- **GAP:** `CreateClusterRoleBinding` does **not** validate `req.Namespace` as a DNS-1123 label. Any string is accepted and persisted. The preview handler already has the validation pattern to copy — `internal/handler/rbac_effective.go:239` uses `k8svalidation.IsDNS1123Label` (imported at `rbac_effective.go:12` as `k8svalidation "k8s.io/apimachinery/pkg/util/validation"`).

### 2e. Routes — authoring endpoints already exist and are RBAC-gated.
- `internal/server/routes_rbac_audit_agents.go:36-38` and the `-role-bindings` aliases at `:46-48`:
  - `GET /rbac/cluster-role-bindings/` → `requirePermission(ResourceRBAC, VerbRead)`
  - `POST /rbac/cluster-role-bindings/` → `writeRBAC` + `requirePermission(ResourceRBAC, VerbCreate)`
  - `DELETE /rbac/cluster-role-bindings/{id}/` → `writeRBAC` + `requirePermission(ResourceRBAC, VerbDelete)`
- So the authoring endpoint is already gated on `rbac:create` + the `rbac:write` api-token scope. No route change needed.

### 2f. OpenAPI spec — already documents namespace.
- `docs/openapi.yaml:2491` `RBACClusterRoleBinding` includes a `namespace` property; `docs/openapi.yaml:2551` `RBACClusterBindingRequest` includes an optional `namespace`. `make verify` (the api-contract gate) already passes with namespace present.

### 2g. Frontend — NO working binding create/edit UI, and the list is disconnected.
- `frontend/src/app/dashboard/rbac/page.tsx` "Bindings" tab (`bindingColumns` at `:350`, rendered at `:502`) is **read-only** — the "Create" button only renders for the users/roles tabs (`:410`, `:426`). There is no create-binding modal and no delete affordance.
- `frontend/src/lib/api.ts:897` `getRoleBindings` calls `GET /rbac/bindings`, and `:902` `createRoleBinding` calls `POST /rbac/bindings`. **No `/rbac/bindings` route exists in the backend** (only `/rbac/{global,cluster,project}-bindings/` and the `-role-bindings` aliases — see 2e). So the current bindings list is a 404 and the create hook is dead.
- `frontend/src/lib/hooks.ts:644` `useCreateRoleBinding` is defined but **used nowhere** (grep of `frontend/src/app` + `frontend/src/components` returns no callers). Its payload shape (`roleName`/`roleType`/`subjects`/`scope{clusterId,projectId}`) does not match the backend's `roleBindingRequest` (`user_id`/`role_id`/`cluster_id`/`namespace`) and has no namespace field.
- The `RoleBinding` type (`frontend/src/types/index.ts:729`) has a `scope` object with cluster/project but **no namespace**.
- Available building blocks for the UI: `useUsers` (`hooks.ts:676`), `useClusterRoles` (`hooks.ts:586`), `useClusters` (`hooks.ts:64`), `DataTable`, `OverlayShell`, `ConfirmDialog` (already imported in `page.tsx`).

### 2h. The "pending" warning that gates promotion.
- `internal/handler/rbac_effective.go:242` appends the warning: `"namespace context is enforced for namespace-scoped bindings when present; binding storage and assignment UI are still pending"`. Storage is not pending (2a) and, once this plan lands, neither is the UI — the second clause must be removed. `internal/handler/rbac_effective_test.go` asserts on this warning string and must be updated in lock-step.

---

## 3. Scope

### In scope (minimum to unblock DIR-04)
1. **Backend validation:** reject a non-DNS-1123 `namespace` on `POST /rbac/cluster-role-bindings/` with a 400 before persisting. Keep the existing escalation guard and audit.
2. **Frontend authoring for cluster bindings:** a create form (user + cluster role + cluster + **optional namespace** field) wired to the *real* `POST /api/v1/rbac/cluster-role-bindings/`; a bindings list wired to the real `GET /api/v1/rbac/cluster-role-bindings/` that shows the namespace column; and a delete affordance wired to `DELETE /api/v1/rbac/cluster-role-bindings/{id}/`.
3. **Flip the warning** at `rbac_effective.go:242` (drop the "assignment UI are still pending" clause) and update its test.

### Out of scope (do NOT expand)
- Per-request performance work; the raw-k8s-proxy namespace watch filter; watches/streaming filtering (all called out in plan 009).
- New tables, new migrations, or namespace on global/project bindings.
- Rebuilding global/project binding CRUD in the UI, or fixing the phantom `/rbac/bindings` shape — only the cluster-binding path (the namespace-relevant scope) is in scope. Leave the unused `createRoleBinding`/`useCreateRoleBinding`/`getRoleBindings` helpers alone or delete them, but do not re-plumb them.
- Promoting the `namespace_scoped_rbac_enabled` flag itself — that is plan 009's decision. This plan only removes the "no UI" blocker to that decision.
- **Optional, explicitly deferred:** validating that the namespace belongs to `project_namespaces` for the cluster. A cluster binding is not project-scoped, so there is no project to check against, and the handler's `RBACQuerier` does not currently expose `ListProjectNamespaces`/`ListAllProjectNamespaces`. Deferring keeps the change minimal; DNS-1123 is the mandatory validation. If a maintainer wants the membership guard later, it is a follow-up that extends the querier interface.

---

## 4. Commands

- Backend build/vet: `make build` / `make vet`
- Go tests (race): `make test` (or targeted: `go test ./internal/handler/... ./internal/rbac/...`)
- API-contract gate (mirrors CI): `make verify`
- sqlc staleness (should be a no-op — no query change): `make sqlc-check`
- Frontend lint/test/build: `cd frontend && npm run lint && npm test && npm run build`

---

## 5. Steps (each with a verify command)

### Step 1 — Validate the namespace on create (backend)
In `internal/handler/rbac.go`, inside `CreateClusterRoleBinding` (before the escalation guard at `:600`), if `req.Namespace != ""` reject a non-DNS-1123 value with a 400 using the existing error helper, e.g.:
```go
if req.Namespace != "" {
    if errs := k8svalidation.IsDNS1123Label(req.Namespace); len(errs) > 0 {
        RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
            "namespace must be a valid Kubernetes namespace")
        return
    }
}
```
Add the import `k8svalidation "k8s.io/apimachinery/pkg/util/validation"` to `internal/handler/rbac.go` (already used in `rbac_effective.go`, so it is a project dependency). Empty namespace stays allowed (== cluster-wide).

Verify:
```
go build ./internal/handler/... && go vet ./internal/handler/...
```

### Step 2 — Backend test for validation + happy path
Add a table test (e.g. in `internal/handler/rbac_group_binding_test.go` or a new `rbac_namespace_binding_test.go`) covering: (a) `namespace:"kube-system"` → 201 and the persisted binding echoes `namespace`; (b) `namespace:"Bad_NS"` → 400 `validation_error`; (c) `namespace:""` → 201 (cluster-wide). Reuse the existing handler test harness/mocks in that package (the escalation tests already construct an `RBACHandler` with a fake querier).

Verify:
```
go test ./internal/handler/ -run 'ClusterRoleBinding|NamespaceBinding' -race
```

### Step 3 — Flip the "pending" warning (backend)
In `internal/handler/rbac_effective.go:242`, change the appended warning to drop the UI-pending clause, e.g. to: `"namespace context is enforced for namespace-scoped bindings when present"`. Update the assertion in `internal/handler/rbac_effective_test.go` that checks this string.

Verify:
```
go test ./internal/handler/ -run 'Effective' -race
```

### Step 4 — Frontend types + API client for real cluster bindings
- In `frontend/src/types/index.ts`, add a `ClusterRoleBinding` type mirroring the backend response: `{ id; user_id: string|null; group: string; role_id: string; cluster_id: string; namespace: string; created_at: string }`.
- In `frontend/src/lib/api.ts`, add:
  - `listClusterRoleBindings(params?: { cluster_id?: string })` → `GET /rbac/cluster-role-bindings/`.
  - `createClusterRoleBinding(data: { user_id: string; role_id: string; cluster_id: string; namespace?: string })` → `POST /rbac/cluster-role-bindings/`.
  - `deleteClusterRoleBinding(id: string)` → `DELETE /rbac/cluster-role-bindings/{id}/`.
- Do NOT reuse the dead `createRoleBinding`/`getRoleBindings` (they point at the non-existent `/rbac/bindings`).

Verify:
```
cd frontend && npm run lint
```

### Step 5 — Frontend hooks
In `frontend/src/lib/hooks.ts`, add `useClusterRoleBindings(params?)`, `useCreateClusterRoleBinding()`, `useDeleteClusterRoleBinding()` following the existing `useCreateRoleBinding` pattern (`hooks.ts:644`) — invalidate `queryKeys.rbac.all` on success, `toastSuccess`/`toastApiError` on settle.

Verify:
```
cd frontend && npm run lint
```

### Step 6 — Frontend: namespace field in create form + namespace in list
In `frontend/src/app/dashboard/rbac/page.tsx`, Bindings tab:
- Point the list at `useClusterRoleBindings()` (real data) and add a **Namespace** column to `bindingColumns` (render the binding's `namespace` or a "cluster-wide" placeholder when empty).
- Add a "Create Binding" button on the Bindings tab (extend the header conditional at `:410`) opening an `OverlayShell` form with: user picker (`useUsers`), cluster role picker (`useClusterRoles`), cluster picker (`useClusters`), and a free-text **Namespace** input (optional; placeholder "leave blank for cluster-wide"). Client-side validate the namespace against DNS-1123 (`/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/`, ≤63 chars) before submit; submit via `useCreateClusterRoleBinding`.
- Add a row delete action wired to `useDeleteClusterRoleBinding` behind the existing `ConfirmDialog`.

Verify:
```
cd frontend && npm test && npm run build
```

### Step 7 — Full gate
Verify:
```
make verify && make test && (cd frontend && npm run lint && npm test && npm run build)
```

---

## 6. Test plan

- **Backend unit (Step 2):** create with valid namespace → 201 + echoed namespace; invalid namespace → 400; empty namespace → 201. Assert the audit record still carries `namespace` (the existing `recordAudit` at `rbac.go:621`).
- **Backend escalation (existing, keep green):** a caller lacking a rule at `(cluster, namespace)` is still 403 by `guardClusterBinding` — confirm the escalation tests in `internal/handler/rbac_escalation_test.go` still pass unchanged (validation runs before the guard but does not alter it).
- **Engine (existing, keep green):** `go test ./internal/rbac/...` — namespace matching in `bindingApplies` is unchanged; this plan adds no engine code.
- **Warning (Step 3):** `rbac_effective_test.go` asserts the new warning string and that a namespaced query still returns the (now shorter) warning.
- **Frontend:** `rbac-routing.test.ts` stays green; component test that the create form disables submit on an invalid namespace and posts `{user_id, role_id, cluster_id, namespace}` to `/rbac/cluster-role-bindings/`; list renders the namespace column.
- **Manual E2E (grounds the unblock):** with `namespace_scoped_rbac_enabled=true`, as an admin create a binding for a test user scoped to one namespace on a cluster; confirm the user can list that namespace's resources and 403s on others; delete the binding and confirm access is revoked (cache invalidation via `h.invalidateUser` at `rbac.go:618`).

---

## 7. Done criteria

- `POST /api/v1/rbac/cluster-role-bindings/` rejects a non-DNS-1123 namespace with 400 and persists a valid one; the create + list responses include `namespace`; the endpoint remains gated on `rbac:write` + `rbac:create`.
- An operator can, entirely from the RBAC → Bindings UI, create a `(user, cluster role, cluster, namespace)` binding, see the namespace in the list, and revoke it.
- `internal/handler/rbac_effective.go:242` no longer claims the assignment UI is pending, and its test reflects the new string.
- `make verify && make test` and the frontend `lint && test && build` all pass. `make sqlc-check` is a no-op (no query changed).

---

## 8. STOP conditions

- **A schema/migration seems necessary.** It should not — the namespace column already exists (`migration 113`). If you reach for a new migration, stop and re-read §2a; you are likely off-plan.
- **The escalation guard needs to change.** It already evaluates the namespace scope (`enforceNoEscalation` → `CheckPermission(..., namespace)`). If a change here is proposed, stop — it risks a privilege-escalation regression and is out of scope.
- **You are tempted to fix/rewire `/rbac/bindings`, global/project binding CRUD, or the dead `createRoleBinding` helper.** Out of scope — only the cluster-binding path unblocks DIR-04. Stop and confirm scope.
- **You are about to add namespace to global or project bindings.** Namespace scoping lives only on cluster bindings by design (a namespace belongs to a cluster). Stop.
- **You start touching the k8s-proxy watch filter, streaming, or per-request perf.** Explicitly out of scope (plan 009). Stop.
- **The `namespace_scoped_rbac_enabled` default flip comes up.** That is plan 009's decision, not this plan. This plan only removes the "no UI" blocker. Stop and defer.

---

## 9. Maintenance notes

- The namespace-scoped binding is a **cluster** role binding with a non-empty `namespace`; there is no separate table or endpoint. Anyone extending binding CRUD should preserve the `namespace=""` == cluster-wide invariant (see the comment in `migration 113` and `roleBindingRequest.Namespace` at `rbac.go:178`).
- Validation lives in two places by design: the preview handler (`rbac_effective.go:239`) and now the create handler (Step 1). Keep them consistent — both use `k8svalidation.IsDNS1123Label`.
- The optional "namespace must belong to `project_namespaces`" guard (deferred in §3) would require adding `ListProjectNamespaces`/`ListAllProjectNamespaces` to the handler's `RBACQuerier` interface (`internal/handler/rbac.go:19`). It is a policy tightening, not a correctness fix — a cluster binding legitimately may target a namespace not (yet) claimed by any project.
- If `namespace_scoped_rbac_enabled` is later promoted (plan 009), these directly-authored namespace bindings and the project→namespace synthetic bindings (`rbac_queries.go:192`) coexist — both flow through the same engine `bindingApplies` path, so no reconciliation is needed.
- Frontend: the bindings list previously pointed at a non-existent `/rbac/bindings` route (a latent 404). This plan repoints the cluster-binding view at the real endpoint; if global/project binding views are added later, wire them to `/rbac/global-role-bindings/` and `/rbac/project-role-bindings/` respectively, not `/rbac/bindings`.
