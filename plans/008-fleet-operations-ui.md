# 008 — Fleet Operations UI (DIR-01)

**Status:** Spike / design plan (no code written yet)
**Author:** senior-eng handoff
**Finding:** DIR-01 — the bulk fleet-operations backend is complete and has **zero** frontend consumers.

---

## Why this matters

Multi-cluster fleet operations are the product's core promise (README: drive an
upgrade/restart/template-fanout across the whole fleet from one dashboard). The
backend for this is fully built, RBAC-gated, audited, idempotent, and
orchestrated by a durable worker — but there is **no way for an operator to
reach it**. `grep -r "fleet-operations" frontend/src` returns nothing. An
operator who wants to upgrade a tool across 50 clusters today has to hit the API
by hand with a bearer token. This plan closes that gap: it exposes the existing
`/api/v1/fleet-operations/*` surface as a first-class dashboard feature.

This is pure frontend + wiring work. **No backend changes are required** for the
MVP — every endpoint, wire shape, and RBAC resource already exists (see Current
state). Two *optional* backend follow-ups (a selector dry-run/preview endpoint
and fleet SSE events) are called out as open questions, not blockers.

---

## Current state (grounded in files read)

### Routes — the full operator surface already exists
`internal/server/routes_cluster_addons.go:142-152` mounts, behind
`if deps.FleetOperations != nil`:

| Method | Path | Handler | RBAC |
|---|---|---|---|
| GET  | `/api/v1/fleet-operations/` | `List` | `fleet_operations:list` |
| POST | `/api/v1/fleet-operations/` | `Create` | `clusters:write` scope + `fleet_operations:create` |
| GET  | `/api/v1/fleet-operations/{id}/` | `Get` | `fleet_operations:read` |
| GET  | `/api/v1/fleet-operations/{id}/targets/` | `ListTargets` | `fleet_operations:read` |
| POST | `/api/v1/fleet-operations/{id}/pause/` | `Pause` | `clusters:write` + `fleet_operations:update` |
| POST | `/api/v1/fleet-operations/{id}/resume/` | `Resume` | `clusters:write` + `fleet_operations:update` |
| POST | `/api/v1/fleet-operations/{id}/abort/` | `Abort` | `clusters:write` + `fleet_operations:update` |
| POST | `/api/v1/fleet-operations/{id}/retry-failed/` | `RetryFailed` | `clusters:write` + `fleet_operations:update` |

> Note: the DIR-01 finding mentions list/create/get/targets/abort; the handler
> doc-comment at `internal/handler/fleet_operations.go:8-15` and the routes show
> **three more state-transition hooks** — `pause`, `resume`, `retry-failed`. The
> UI should surface all of them.

The RBAC resource is `fleet_operations` (underscore) —
`internal/rbac/types.go:42` (`ResourceFleetOperations Resource = "fleet_operations"`).
Frontend `permission` checks must use the string `'fleet_operations'`.

### Wire shapes (authoritative — read from the Go structs, not openapi)
The openapi entries at `docs/openapi.yaml:8039-8280` are real path definitions,
but the schemas `CreateFleetOperationRequest` / `FleetOperationResponse` /
`FleetOperationTargetResponse` at `docs/openapi.yaml:3860-3889` are **permissive
`additionalProperties: true` placeholders** ("Schema not yet fully
enumerated"). The real shapes are the handler structs:

**`CreateFleetOperationRequest`** (`internal/handler/fleet_operations.go:193-203`):
```jsonc
{
  "name": "string (required)",
  "description": "string",
  "operation_type": "string (required, enum below)",
  "operation_spec": { /* per-type object, see below */ },
  "selector": { /* k8s-style selector, see below */ },
  "strategy": "parallel | sequential",     // default "parallel"
  "max_concurrent": 3,                       // default 3, clamp [1,100]
  "on_error": "abort | continue",            // default "abort"
  "respect_maintenance_windows": true        // optional *bool, default true
}
```

**`FleetOperationResponse`** (`internal/handler/fleet_operations.go:96-118`):
`id, name, description, operation_type, operation_spec, selector, strategy,
max_concurrent, on_error, respect_maintenance_windows, status, total_clusters,
completed_clusters, failed_clusters, skipped_clusters, started_at, completed_at,
last_error, created_by, created_at, updated_at`.

**`FleetOperationTargetResponse`** (`internal/handler/fleet_operations.go:155-167`):
`id, operation_id, cluster_id, status, sub_operation_id, sub_operation_type,
started_at, completed_at, last_error, created_at, updated_at`.

### Enums / validation (from `validateFleetOperation`, `fleet_orchestrate.go:95-129`)
- **Implemented operation types** (accepted): `tool_upgrade`, `tool_install`,
  `tool_uninstall`, `apply_template`, `rotate_agent_token`
  (`fleet_operations.go:213-219`).
- **Reserved → 400 today**: `drain_namespaces`, `custom_helm`
  (`fleet_operations.go:231-234`). The UI should hide/disable these or label
  them "coming soon".
- **`operation_spec` shape by type**
  (`fleet_operations.go:307-337`): tool ops require `{ "slug": "<tool-slug>" }`;
  `apply_template` requires `{ "template_id": "<uuid>" }`; `rotate_agent_token`
  needs no spec.
- **Statuses** (`fleet_orchestrate.go:95-100`): `pending, running, paused,
  completed, failed, aborted`.
- **State machine** (from handler): `pause` only from `running`; `resume` only
  from `paused`; `abort` from `pending|running|paused`; `retry-failed` requeues
  failed targets and, if parent is `failed|aborted`, bumps it back to `running`
  (`fleet_operations.go:481-546`). Invalid transitions return **409**
  (`transitionStatus`, `:566-571`).

### Selector shape (`internal/worker/tasks/fleet_selector.go:34-61`)
Mirrors a Kubernetes label selector:
```jsonc
{
  "matchLabels":  { "tier": "prod", "region": "us-east" },   // AND of equals
  "matchExpressions": [
    { "key": "env", "operator": "In", "values": ["staging","canary"] }
  ],                                                          // In|NotIn|Exists|DoesNotExist
  "matchGroupIDs": ["<cluster-group-uuid>", "..."]           // cluster_groups membership
}
```
**Load-bearing safety property** (`fleet_selector.go:19-23`): an empty selector
`{}` matches **no** clusters, and the handler rejects an empty selector at create
time. The UI must never submit an empty selector — enforce non-empty client-side
too.

### The orchestrator does **not** emit SSE events
`FleetOrchestrateDeps` (`fleet_orchestrate.go:260-264`) has only
`Queries / Dispatcher / MaintenanceWindow` — **no event bus**. The live-events
`KNOWN_EVENT_TYPES` list (`frontend/src/lib/live-events.ts:201-219`) has no
`fleet.*` types. **Consequence:** the "live" per-cluster target table must use
React Query `refetchInterval` polling (the pattern the Agents page already uses),
*not* SSE — unless the backend is extended (see Open questions).

### Existing frontend patterns to reuse
- **Query key factory** — `frontend/src/lib/query-keys.ts` (298 lines). Add a
  `fleetOperations` group mirroring `tools` (`:276-283`) and `agents`
  (`:202-204`). Suggested keys below.
- **Operations-list + polling detail** — the Agents page
  (`frontend/src/app/dashboard/agents/page.tsx:39-43`) is the closest analog:
  `useQuery({ queryKey: queryKeys.agents.operations(id), refetchInterval:
  15000 })` drives a live operations list purely by polling. Copy this for the
  target-status table.
- **API client** — plain async functions in `frontend/src/lib/api.ts` using
  `api.get/post` returning `APIResponse<T>`; paginated lists return
  `{ data, total, ... }`. Agent-operation helpers at `api.ts:284-301` are the
  template.
- **Sidebar nav** — `frontend/src/components/layout/sidebar.tsx:108-159`. Nav
  items are `{ label, href, icon, permission: { resource, verb }, featureFlag? }`
  grouped into sections. A "Fleet Operations" item belongs in the **Platform**
  section (`:108-116`), gated `permission: { resource: 'fleet_operations', verb:
  'list' }`.
- **Permission gating** — `can(resource, verb)` from `frontend/src/lib/permissions.ts`
  (imported in sidebar at `:58`).
- **Cluster labels + groups for the selector builder** — clusters already carry
  `labels: Record<string,string>` (`frontend/src/types/index.ts:75`), fetched via
  `getClusters` (`api.ts:234`). Cluster groups have their own client
  (`frontend/src/lib/api/cluster-groups.ts`, re-exported at `api.ts:2723`) for
  `matchGroupIDs`. There is **no** distinct-labels endpoint — the builder must
  aggregate label keys/values from the cluster list client-side.

---

## Scope

**In scope (MVP):**
1. New dashboard route `/dashboard/fleet` (list + create) and
   `/dashboard/fleet/[id]` (detail + targets + controls).
2. API client functions + query keys + TS types for all 8 endpoints.
3. Create form with a Kubernetes-style selector builder (matchLabels rows +
   matchExpressions rows + optional matchGroupIDs multi-select), operation-type
   picker (implemented types only), per-type `operation_spec` sub-form, and
   strategy/max_concurrent/on_error/respect_maintenance_windows controls.
4. Operation detail: header with status + rollup counters
   (total/completed/failed/skipped), a **polling** per-cluster target-status
   table, and Pause / Resume / Abort / Retry-failed controls with correct
   enable/disable per state-machine + RBAC.
5. Sidebar nav entry, permission gating, and (optional) feature flag.

**Out of scope:**
- Any backend change (dry-run endpoint, fleet SSE events, new operation types) —
  tracked as Open questions.
- `drain_namespaces` / `custom_helm` authoring (reserved → 400 today).
- Scheduling / recurring fleet operations.

---

## Proposed route + component breakdown

```
frontend/src/app/dashboard/fleet/
  page.tsx                         # list + "New operation" launcher
  [id]/page.tsx                    # detail: header, counters, targets, controls
frontend/src/components/fleet/
  fleet-operation-list.tsx         # paginated table (name, type, status, counters, created)
  create-fleet-operation-dialog.tsx# the create form (multi-step or single form)
  selector-builder.tsx             # matchLabels + matchExpressions + matchGroupIDs editor
  operation-spec-fields.tsx        # per-type spec sub-form (tool slug / template picker)
  fleet-target-table.tsx           # polling per-cluster status table
  fleet-operation-controls.tsx     # Pause/Resume/Abort/Retry buttons + confirm dialogs
  fleet-status-badge.tsx           # shared status pill (op + target statuses)
frontend/src/lib/api/fleet-operations.ts   # api client functions (or add to api.ts)
```

### Suggested query keys (`query-keys.ts`)
```ts
fleetOperations: {
  all: ['fleet-operations'] as const,
  list: (params?: Record<string, unknown>) => ['fleet-operations', 'list', params] as const,
  detail: (id: string) => ['fleet-operations', 'detail', id] as const,
  targets: (id: string, params?: Record<string, unknown>) =>
    ['fleet-operations', 'detail', id, 'targets', params] as const,
},
```

### Selector builder UX
- **matchLabels**: repeatable key/value rows. Autocomplete keys and values from
  the aggregated `getClusters()` label set (client-side dedupe).
- **matchExpressions**: repeatable rows `{ key, operator, values[] }`; `values`
  hidden when operator is `Exists`/`DoesNotExist`.
- **matchGroupIDs**: multi-select backed by the cluster-groups client.
- **Live match count**: without a backend preview endpoint, evaluate the selector
  **client-side** against the fetched cluster list to show "N clusters match"
  before submit (the same predicate logic as `matchesSelector`,
  `fleet_selector.go:114-160`). This is a stopgap; a real dry-run endpoint is an
  Open question.
- Block submit when the selector is empty (mirrors the backend safety property).

### Target table (live via polling)
- `useQuery({ queryKey: fleetOperations.targets(id), queryFn: getFleetTargets,
  refetchInterval: op.status is terminal ? false : 5000 })` — stop polling once
  the operation reaches `completed|failed|aborted`.
- Columns: cluster (resolve `cluster_id` → name via cached clusters list),
  target status pill, sub_operation_type, started/completed, last_error.
- Also poll the parent `detail(id)` query so header counters update live.

---

## Steps (phased build order)

**Phase 0 — API layer + types (no UI).**
- Add TS interfaces for the three wire shapes and the create request.
- Add `frontend/src/lib/api/fleet-operations.ts`: `getFleetOperations`,
  `getFleetOperation`, `createFleetOperation`, `getFleetTargets`,
  `pauseFleetOperation`, `resumeFleetOperation`, `abortFleetOperation`,
  `retryFailedFleetOperation`.
- Add the `fleetOperations` query-key group.
- Unit-test the client-side selector predicate helper against the Go behavior.

**Phase 1 — read-only list + detail.**
- `/dashboard/fleet` list page (paginated, status filter — the List endpoint
  takes `status/limit/offset`, `fleet_operations.go:344-353`).
- `/dashboard/fleet/[id]` detail with counters + polling target table.
- Sidebar nav entry (Platform section), permission gating on `fleet_operations`.
- No mutations yet — proves the read path end to end.

**Phase 2 — lifecycle controls.**
- Pause / Resume / Abort / Retry-failed mutations with confirm dialogs.
- Enable/disable each control by (a) current `status` per the state machine and
  (b) `can('fleet_operations','update')`. Handle the **409** invalid-transition
  response gracefully (toast + refetch).

**Phase 3 — create flow.**
- Operation-type picker (implemented types only; reserved types hidden or
  disabled with a "coming soon" note).
- Selector builder + per-type `operation_spec` sub-form (tool `slug` from the
  tools catalog / `template_id` from a template picker).
- strategy/max_concurrent/on_error/respect_maintenance_windows controls; when
  `sequential`, force + disable `max_concurrent` (server clamps to 1 anyway,
  `fleet_operations.go:278-282`).
- Client-side validation mirroring `validateFleetOperation`; on success,
  redirect to the new detail page (the Create response returns 201 + `Location`).

**Phase 4 — polish.**
- Empty states, live match-count preview, audit-friendly confirm copy, loading
  skeletons, error boundaries consistent with sibling dashboard pages.

---

## Test plan

- **Unit (Jest/RTL):** selector-builder emits the exact JSON shape
  (`matchLabels`/`matchExpressions`/`matchGroupIDs`); empty-selector submit is
  blocked; sequential strategy forces `max_concurrent=1`; controls
  enable/disable correctly per each status; client-side selector predicate
  matches the Go `matchesSelector` truth table (In/NotIn/Exists/DoesNotExist).
- **API client:** mock `api.get/post`; assert correct URLs, trailing slashes
  (backend routes use trailing `/`), and pagination param passthrough.
- **Integration (MSW):** create → 201 redirect → detail renders counters;
  target table polls and updates; abort on a `running` op flips status; abort on
  a terminal op surfaces the 409 gracefully.
- **RBAC:** a user without `fleet_operations:list` never sees the nav item;
  without `:update` the lifecycle buttons are hidden/disabled.
- **Manual/e2e:** drive a real `tool_upgrade` across a 2-cluster dev fleet and
  watch the target table progress pending→running→completed.

---

## Done criteria

- Operators can list, create, inspect, and control (pause/resume/abort/retry)
  fleet operations entirely from the dashboard — no manual API calls.
- All 8 endpoints have a typed client function and are exercised by the UI.
- The create form cannot submit an empty selector, an unimplemented operation
  type, or a malformed per-type `operation_spec`.
- Lifecycle controls respect both the backend state machine (no 409-triggering
  actions offered) and RBAC.
- The target table reflects live progress (≤5s lag) while an operation runs and
  stops polling when terminal.
- Nav entry gated on `fleet_operations:list`; Jest suite green; `make verify`
  (frontend lint/typecheck) green.

---

## Open questions for the maintainer

1. **Dry-run / selector preview endpoint.** There is no way to ask the backend
   "which clusters does this selector match?" before create — the orchestrator
   evaluates the selector once at pending→running (`fleet_orchestrate.go:370-393`).
   The MVP fakes a match-count client-side by re-implementing `matchesSelector`.
   Do you want a real `POST /api/v1/fleet-operations/preview` (evaluate selector,
   return matched cluster IDs/count) so the UI and backend can't drift? Strongly
   recommended before this ships to production.
2. **Fleet SSE events.** The orchestrator has no event bus
   (`FleetOrchestrateDeps`, `fleet_orchestrate.go:260-264`), so the "live" table
   is polling. Worth emitting `fleet.operation.progress` / `fleet.target.status`
   on the existing bus (and adding them to `KNOWN_EVENT_TYPES`) so the table can
   reuse `useLiveQueryInvalidation` instead of a 5s poll? Nice-to-have, not a
   blocker.
3. **Selector builder scope.** Is `matchLabels` + `matchExpressions` +
   `matchGroupIDs` the full desired UX, or do you want a raw-JSON "advanced mode"
   escape hatch too? Where do cluster label *keys/values* come from for
   autocomplete — is aggregating from `getClusters()` acceptable, or should there
   be a dedicated distinct-labels endpoint?
4. **Reserved operation types.** Hide `drain_namespaces` / `custom_helm`
   entirely, or show them disabled as "coming soon"? (They 400 today,
   `fleet_operations.go:231-252`.)
5. **Feature flag.** Gate behind a new `feature.fleet` flag
   (`api.ts:218-225` enumerates existing flags) like catalog/backups, or ship
   ungated behind RBAC only?
6. **operation_spec pickers.** For `tool_*` the spec needs a tool `slug` — reuse
   the tools catalog (`queryKeys.tools`, `query-keys.ts:276-283`). For
   `apply_template` it needs a `template_id` — reuse the cluster-templates list.
   Confirm those are the right sources.

---

## Coarse effort estimate

| Phase | Work | Estimate |
|---|---|---|
| 0 | API client + types + query keys + predicate helper | ~0.5 day |
| 1 | List + detail (read-only) + nav + polling table | ~1.5 days |
| 2 | Lifecycle controls + 409 handling | ~1 day |
| 3 | Create flow + selector builder + spec sub-forms | ~2.5 days |
| 4 | Polish, empty/error states, match-count preview | ~1 day |
| — | Tests (unit + MSW integration) across phases | ~1.5 days |

**Total: ~7–8 engineer-days** for the MVP, frontend-only. Add ~1–2 days if the
maintainer wants the backend preview endpoint (Open question 1) and/or fleet SSE
events (Open question 2).
