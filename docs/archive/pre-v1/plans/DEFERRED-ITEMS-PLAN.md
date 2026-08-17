# Deferred Items — Design & Execution Plan (2026-07-01)

Two items deferred from the sweep remediation, grounded by recon before implementation.

## Item 1 — Namespace/project-scoped RBAC reads (security-sensitive)

### The real gap (grounded)
- `internal/rbac/engine.go bindingApplies` (~L84): a **project-scoped binding** (`ProjectID set, ClusterID empty`) only matches when `b.ProjectID == projectID.String()`. On every cluster resource route (`/clusters/{cluster_id}/pods/` etc.) there is **no `{project_id}` URL param**, so `permissionScopeIDs` (`routes.go:1168`) passes `projectID = uuid.UUID{}` (zero). **Project bindings therefore grant nothing on cluster reads** — project members get 403 everywhere. This is the core bug.
- A **namespace-scoped cluster binding** already works via `?namespace=X` (the typed routes' `middleware.RequirePermission` passes `namespaceContext(r)` which reads `?namespace=`). The gap there is only the "list ALL my namespaces in one call" convenience + the fact that a cluster-wide read returns EVERY namespace (no per-namespace filtering tier).
- Two read paths exist: dedicated typed handlers (`internal/handler/workloads.go` — what the resource browser uses) and the generic `/k8s/*` passthrough. The browser filtering seam is `workloads.go`.

### Design (flag-gated: `namespace_scoped_rbac_enabled`, config, default false)
Strictly additive — with the flag OFF, behavior is byte-identical (project bindings still fail closed, cluster-wide readers still see everything, no filtering). Enable in dev, validate with a real scoped user, then promote.

1. **Project → namespace resolution** (authz correctness). In `GetUserBindings` (`internal/server/middleware/rbac_queries.go`, `case "project"`), when the flag is on, expand each project binding into synthetic **namespace-scoped cluster bindings** using `ListProjectNamespaces(project_id)` → one `RoleBinding{ClusterID: <cluster>, Namespace: <ns>, RoleRules: <project role rules>}` per `(cluster_id, namespace)` row. The pure engine then matches them unchanged (correctly per-cluster). Add `ListProjectNamespaces` to the querier interface + fakes. Flag carried on `SQLCRBACQuerier` (set from cfg at construction).

2. **"List all my namespaces" gate + filtering.**
   - New engine helpers (`internal/rbac/engine.go`): `HasAnyNamespaceAccess(bindings, resource, verb, clusterID) bool` (true if a cluster-wide/global binding grants it, OR any namespace-scoped binding on the cluster does) and `AuthorizedNamespaces(bindings, resource, verb, clusterID) (all bool, names map[string]struct{})` (`all=true` for cluster-wide/global; else the specific namespace set).
   - New middleware `RequireListPermission` (`internal/server/middleware/rbac.go`) used on the typed LIST routes (`routes_resources_workloads.go`): allows the request through if `HasAnyNamespaceAccess`, so a scoped user reaches the handler instead of a bare-list 403. Flag-gated: off → falls back to the existing `RequirePermission`.
   - Handler filtering (`internal/handler/authorization.go` new `authorizedNamespaces(ctx, clusterID)` on `authorizationSupport`; applied in `workloads.go` `ListPods`/`ListNamespaces`/`ListEvents` before `pageWindow`): if `all` → return everything (unchanged); else filter `items` to the authorized namespace set keyed on `item["namespace"]` (or `name` for ListNamespaces, `involvedObject.namespace` for events). Nodes are cluster-scoped → still require cluster-wide read.

### Adversarial verification (the reason this is ultracode)
Multiple skeptics attempt to prove: (i) a **data leak** (scoped user sees a namespace they're not authorized for), (ii) an **authz bypass** (namespace filter defeated by a crafted `?namespace=` or path), (iii) a **lockout/regression** (cluster-wide reader or superuser loses access, or flag-off changes any behavior).

## Item 2 — ArgoCD overview ApplicationSet surfacing (small, frontend)

Backend endpoint (`GET /argocd/instances/{id}/applicationsets/`), client (`listArgoApplicationSets`), query key (`queryKeys.argocd.appsets`), types, and a full `ApplicationSetsTab` ALL already exist. The only gap: `OverviewTab` (`frontend/src/app/dashboard/argocd/[instanceId]/page.tsx` ~L268-363) never queries or shows ApplicationSets, so an instance with 0 Applications but N ApplicationSets reads as a broken "Synced 0".

### Change
- Add a `useQuery` for `listArgoApplicationSets(instanceId)` (key `queryKeys.argocd.appsets(instanceId)`) to `OverviewTab`.
- Add an "ApplicationSets" stat card (deep-links to the appsets tab) to the stat grid.
- Add a small ApplicationSets `<section>` (name + generator kinds + a health chip from `status.conditions` where present) mirroring the "Recent operations" section, and an empty-state line that distinguishes "0 Applications · N ApplicationSets targeting 0 matched clusters" from "broken".
- Extend `ArgoApplicationSet` type with optional `status?: { conditions?: {...}[] }` (data is already on the wire via the raw-JSON `fetchInstanceJSON` path).
- Gate: `npx tsc --noEmit` + `npx eslint` clean.
