# Plan 025: Explorer depth parity — array-valued guided fields (containers, rules, volumes, env, ports, paths, tolerations, affinity), group-by-namespace tables, bulk restart, generic related resources, and a `questions.yaml` spike

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/components/resources frontend/src/components/ui/data-table.tsx frontend/src/components/ui/data-table-toolbar.tsx frontend/src/components/ui/data-table-virtualized-body.tsx frontend/src/components/ui/data-table-features.ts frontend/src/routes/dashboard/catalog/-install-chart-modal.tsx`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: L (5–7 days across four independent PRs; the `questions.yaml` item is a spike, not a build)
- **Risk**: MED (the guided editor writes into live manifests; array indexing bugs can drop sibling entries)
- **Depends on**: 024 step 6 (probe fields live inside the per-container editor); 021 (`Field`, `Card`); none for the table items
- **Category**: direction (Rancher parity)
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

Astronomer's guided create/edit forms are structurally limited to the first element of every Kubernetes array: one container, one volume, one env source, one port, one Ingress path, one Role rule, and `nodeSelector` only. Any real workload (sidecar + app, two mounts, a Role with three rules, a workload on a tainted GPU pool) forces the operator into YAML at exactly the moment the manifest is most fiddly, and round-tripping an existing multi-container Deployment through the guided editor shows only container 0. On the table side, "All namespaces" lists are a flat alphabetical blur (Rancher groups by namespace by default), and bulk actions stop at delete though restart/scale are routine incident actions with the same authorization shape. These are the last big functional deltas in the explorer.

## Current state

**Guided model** — `frontend/src/components/resources/guided-resource-model.ts:108-112`:

```ts
export function containerPath(kind: string): ManifestPath | null {
  const pod = podSpecPath(kind);
  return pod ? [...pod, "containers", 0] : null;
}
```

Consumers: `guided-resource-model.ts` and `guided-resource-form.tsx` (3 call sites). Sections: `guided-resource-basic-sections.tsx:113` (one container port), `:221` (one Ingress path); `guided-resource-advanced-section.tsx:173,190` (`envFrom[0]` ConfigMap, `envFrom[1]` Secret, hard-indexed), `:216-252` (`volumes[0]`, `volumeMounts[0]`), `:259-272` (`nodeSelector` textarea only); `guided-resource-access-sections.tsx:62,79,92` (Role `rules[0].apiGroups/resources/verbs`). Field primitives: `guided-resource-fields.tsx` (`Field` at 25, plus text/number/select helpers) and `manifestValue(value, path)` / `updateManifest` helpers in the model. Guided ↔ YAML round-trip and dry-run live in `components/ui/yaml-view-dialog.tsx:149,179-231` and `create-resource-dialog.tsx`.

**Tables** — `components/ui/data-table.tsx` (TanStack Table v9 via `data-table-features.ts:20-35`: sorting, filtering, faceting, pagination, selection, column visibility, resizing), toolbar `data-table-toolbar.tsx:41-135` (search, facets, column menu, selection bar at 121–133), virtualized body `data-table-virtualized-body.tsx`, persistence via `persistKey` (`explorer-data-table.tsx:251` `explorer:${resourceType}`). `grep -rn "getGroupedRowModel\|groupBy" frontend/src/components/ui` → 0. Namespace scope (`components/layout/cluster-scope-controls.tsx:219,401`) exposes whether more than one namespace is selected.

**Bulk actions** — `components/resources/explorer-data-table.tsx:232-265`:

```tsx
  const canDelete = (row: T) => { if (!bulkDelete) return false; try { bulkDelete.path(row); return scope.allows(permissionResource, "delete", …); } catch { return false; } };
  return (<DataTable {...tableProps} … selectable={bulkDelete ? canDelete : false}
      bulkActions={bulkDelete ? (selected) => (<BulkDeleteAction clusterId={clusterId} resourceType={resourceType} rows={selected} config={bulkDelete} keyExtractor={keyExtractor} />) : undefined} />);
```

`BulkDeleteAction` (find in `components/resources/`) performs per-row authorization, typed-name confirmation, sequential execution, and a partial-failure result table. Row actions Restart/Scale exist per object in `resource-list-page.tsx:171-194` via per-object endpoints; `resource-action-policy.ts` holds the permission predicates.

**Related resources** — `components/resources/resource-detail-tabs.tsx:425` (Owned By), `:470` (Pods), `:513` (Selected by Services, computed client-side by label match at 410–421). Nothing generic over `ownerReferences`.

**Chart install** — `routes/dashboard/catalog/-install-chart-modal.tsx:51-57` builds the form from `version.valuesSchema` only; `:109` falls back to raw YAML; `grep -rn questions frontend/src internal` → 0. Ingest stores `values_schema`/`default_values` (find in `internal/handler/catalog*.go` / `internal/catalog`).

Rancher reference: `rancher-dashboard/shell/edit/workload/index.vue:224-330` (tabbed multi-container editor), `shell/edit/workload/storage/index.vue` (N volumes), `shell/edit/rbac.authorization.k8s.io.role.vue` (N rules), `shell/components/ResourceTable.vue:128,156,512-566` (group-by-namespace default when > 1 namespace), `shell/components/SortableTable/index.vue` (bulk bar promotes supported row actions), `shell/components/Questions/*` (questions-driven install), `shell/components/ResourceDetail/*` Related Resources (generic owner traversal).

## Commands you will need

| Purpose | Command (in `frontend/`) | Expected |
|---|---|---|
| Gate | `npm run type-check && npm run lint && npm test` | exit 0 |
| Smoke | `npm run test:e2e:smoke` | pass |
| Table e2e | `npx playwright test tests/e2e/data-table.spec.ts --project=chromium` | pass |
| Explorer keyboard e2e | `npx playwright test tests/e2e/resource-actions-keyboard.spec.ts --project=chromium` | pass |

## Scope

**In scope**: `components/resources/guided-resource-*.ts(x)` and their tests; new `components/resources/array-field.tsx`; `components/ui/data-table*.ts(x)` (grouping); `components/resources/explorer-data-table.tsx`, `bulk-*.tsx`, `resource-list-page.tsx` (bulk restart); `components/resources/resource-detail-tabs.tsx` + `resource-detail-model.ts` (generic related); `docs/architecture/decisions/` (spike ADR for questions.yaml); catalog files only if the spike is approved.

**Out of scope**: YAML editor internals, dry-run/apply logic, backend (except the spike's proposal), nav/shell/design system.

## Git workflow

- Branches: `advisor/025-array-fields`, `advisor/025-table-grouping`, `advisor/025-bulk-restart`, `advisor/025-related-resources`, `advisor/025-questions-spike`.
- Conventional commits.

## Steps

### Step 1: Characterization tests for the guided model (do first)

Before changing indexing, write tests in `components/resources/__tests__/guided-resource-model.test.ts` that load a two-container Deployment manifest with two volumes, two env sources, two ports, and assert that `updateManifest` for a container-0 field leaves container 1, volume 1, envFrom[1] and ports[1] byte-identical. These must pass **before** and **after** the refactor.

**Verify**: `npm test -- guided-resource-model` pass.

### Step 2: `ArrayField` primitive and indexed paths

- `components/resources/array-field.tsx`: `ArrayField<T>({ path, value, onChange, label, addLabel, emptyItem, renderItem: (item, index, helpers) => ReactNode, min = 0, max })` — renders one bordered block per element with a remove button (`aria-label="Remove <label> N"`), an add button, and move up/down (optional). Keys by index (stable enough; no reorder animation).
- Model: change `containerPath(kind)` → `containerPath(kind, index: number)`; add `podSpecArrayPath(kind, key: "volumes" | "initContainers")`, `containerArrayPath(kind, index, key: "ports" | "env" | "envFrom" | "volumeMounts")`. Update the 3 call sites.
- Sections:
  - **Containers**: a tabbed or stacked `ArrayField` over `containers` (name, image, imagePullPolicy, command/args, resources, ports `ArrayField`, env `ArrayField` (name/value or valueFrom secret/configmap key), envFrom `ArrayField`, volumeMounts `ArrayField`, probes via 024's `ProbeFields`). Add an "Init containers" toggle exposing the same editor over `initContainers`.
  - **Volumes**: `ArrayField` over `volumes` with type select (emptyDir, configMap, secret, persistentVolumeClaim, hostPath) and the type's fields.
  - **Role/ClusterRole rules**: `ArrayField` over `rules`.
  - **Ingress**: `ArrayField` over `rules[].http.paths[]` (nest one `ArrayField` per rule host).
  - **Scheduling**: keep `nodeSelector`; add `tolerations` `ArrayField` (key/operator/value/effect/tolerationSeconds) and a minimal `affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions` `ArrayField` (key/operator/values).
- Round-trip: `guided-resource-form.tsx` must hydrate every array from an existing manifest (edit mode) and never truncate on write (assert with step 1 tests extended to write through container 1).

**Verify**: step-1 tests still pass; new tests: adding a second container writes `containers[1]`; removing container 0 shifts container 1 to index 0; a Role with three rules round-trips; tolerations serialize correctly. Smoke crawl passes.

### Step 3: Group-by-namespace

- `data-table.tsx`: optional `groupBy?: keyof T | ((row) => string)`; enable `getGroupedRowModel` + `getExpandedRowModel` from `@tanstack/react-table`; default all groups expanded.
- `data-table-virtualized-body.tsx`: render group header rows (sticky within the scroll container, `role="row"` with a single `role="rowheader"` cell spanning columns, showing the namespace and a count) — measure group rows with the same virtualizer (they are just rows with a different renderer).
- Toolbar: a "Group: None | Namespace" toggle, shown only when a `groupBy` is configured; persist the choice under `persistKey`.
- `explorer-data-table.tsx`: pass `groupBy="namespace"` and default to grouped when the cluster scope has > 1 namespace selected (read from `useClusterScopeStore`), ungrouped otherwise.

**Verify**: `data-table.behavior.test.tsx` cases: grouped render shows N group headers; toggling to None flattens; sorting sorts within groups. `tests/e2e/data-table.spec.ts` passes (add one grouped assertion).

### Step 4: Bulk restart (and scale) over the delete infrastructure

- Extract from `BulkDeleteAction` a `useBulkOperation({ rows, authorize, execute, label, confirm })` hook: per-row authorization, typed confirmation (`ConfirmDialog` `confirmValue`), sequential execution with a partial-failure result table, and `toastSuccess`/`toastApiError`.
- `BulkRestartAction`: authorize with `resource-action-policy.ts`'s restart predicate per row; execute the existing per-object restart call; only for kinds in `WORKLOAD_SCALABLE_KINDS` (+ DaemonSet).
- `BulkScaleAction`: one replica input applied to all selected (Deployment/StatefulSet/ReplicaSet only).
- `explorer-data-table.tsx`: `selectable` becomes true when **any** bulk action is authorized for the row; `bulkActions` renders the bar with Delete / Restart / Scale as applicable.
- Audit: the per-object endpoints already write audit rows; confirm the bulk path does not bypass them (it calls the same endpoints).

**Verify**: `bulk-restart-action.test.tsx`: two rows, one unauthorized → confirmation lists exactly one object; a failing second call yields a result table with one failure. `resource-actions-keyboard.spec.ts` passes.

### Step 5: Generic related resources

- `resource-detail-model.ts`: `relatedResources(resource, index)` walking `metadata.ownerReferences` upward (fetch each owner by uid/name via the discovery-aware get) and downward (children whose `ownerReferences` include this uid — use the existing list query for the common child kinds: ReplicaSet→Pod, Deployment→ReplicaSet, StatefulSet/DaemonSet/Job→Pod, CronJob→Job, Service→Endpoints/EndpointSlice, Ingress→Service, PVC→PV, HPA→target). Keep the three curated sections as typed overlays on top.
- Empty state: "No related resources found" (explicit), never blank.

**Verify**: unit tests with fixture objects for Deployment→ReplicaSet→Pod and Ingress→Service; smoke crawl.

### Step 6: `questions.yaml` spike (design only)

Write `docs/architecture/decisions/helm-questions-forms.md` (ADR format used by the sibling files in that directory): what Rancher's `questions.yaml` supports (`group`, `type`, `show_if`, `show_subquestion_if`, `options`, `required`, `default`), which fields Astronomer would honour in v1, where ingest would store it (a `questions` JSONB column on chart versions, populated from the chart archive), the renderer shape (`HelmQuestionsForm` alongside `HelmValuesForm`, preferred over `valuesSchema` when both exist, YAML toggle preserved via the existing `dumpHelmValuesYAML` round-trip at `-install-chart-modal.tsx:125`), estimated effort, and the fallback when neither exists (raw YAML, as today). Do **not** implement; end with a recommendation and open questions.

**Verify**: file exists; `node scripts/check-docs.mjs` (repo root) passes.

### Step 7: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
npx playwright test tests/e2e/data-table.spec.ts tests/e2e/resource-actions-keyboard.spec.ts --project=chromium
```

## Test plan

- Characterization first (step 1), extended through step 2.
- `array-field.test.tsx`: add/remove/min/max; remove announces via `aria-label`.
- Grouping and bulk tests per step.
- Related-resources fixtures.

## Done criteria

- [ ] Gates exit 0
- [ ] `grep -n '"containers", 0' frontend/src/components/resources/guided-resource-model.ts` → 0
- [ ] `grep -rn "envFrom\", 1\|envFrom, 1" frontend/src/components/resources` → 0
- [ ] A two-container Deployment round-trips through the guided editor unchanged (test)
- [ ] `grep -c getGroupedRowModel frontend/src/components/ui/data-table.tsx` ≥ 1
- [ ] Bulk bar offers Restart for selected Deployments (test)
- [ ] Related tab shows an explicit empty state (test)
- [ ] ADR for questions.yaml exists
- [ ] README row updated

## STOP conditions

- Step 1 characterization tests fail on the current code (the model already drops siblings) — STOP and report; that is a data-loss bug to fix before this plan.
- The virtualizer cannot measure heterogeneous rows without a measurement API change — STOP; report the constraint before rewriting the body.
- The per-object restart endpoint is not idempotent or has rate limits that a bulk loop would trip — STOP and report; a server-side batch may be needed.
- `ownerReferences` traversal needs a "get by uid" the proxy does not expose — fall back to name+namespace and note the limitation; if even that is unavailable, STOP.

## Maintenance notes

- Any new guided section must use `ArrayField` for Kubernetes list fields; reviewers should reject fixed-index paths.
- Group headers interact with row selection: "select all" selects visible rows across groups; document this in `frontend/docs/design-system.md`.
- After the spike, if `questions.yaml` is approved, ingest changes belong to a backend plan; this plan's renderer sketch is the frontend half.
