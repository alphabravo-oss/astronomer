# Cluster Explorer Drill‑Down — Implementation Plan

> **Goal:** close the biggest Rancher‑parity gap in the astronomer UI — make **every** Kubernetes
> resource a **clickable row → rich detail view** (Overview / YAML / Events / Related), add a real
> **pod detail**, and a **dynamic CRD / custom‑resource explorer** — so the product feels like
> Rancher's Cluster Explorer instead of "flat tables + YAML dialogs."
>
> **Scope:** `astronomer/frontend` (mostly composition) + a couple of small backend filters.
> **Sequencing:** implement **after** the agent‑authz security fixes
> (`docs/archive/pre-v1/plans/agent-authz-security-review.md`) land. WS-E depends on that historical plan's **F2** (CRD RBAC policy).
>
> **Status:** DRAFT v1.

---

## 0. Why (validated from the running UI)

Measured in `frontend/src` against the live build:

- The generic resource browser `app/dashboard/clusters/[id]/[resource]/page.tsx` renders **16 resource
  tables but has only ONE `onRowClick`** (nodes). The other 15 kinds (services, ingress, network
  policies, PV/PVC, configmaps, secrets, namespaces, HPA, PDB, RBAC objects, storage classes,
  endpoints, events, Gateway API) are **flat, non‑clickable tables** — interaction is per‑row
  **View YAML / Edit / Delete** buttons.
- **No generic detail view exists** — `components/resources/` has only configmap/secret/create
  dialogs. The only "detail" for most kinds is the raw **`YamlViewDialog`**.
- **Workloads** drill down only via a subtle name `<Link>` (`workloads/page.tsx:336`) → a workload
  detail page with **Pods / Logs / Metrics** tabs. The *row* isn't clickable (no `onRowClick`), so it
  doesn't read as clickable.
- **Pods have no standalone detail** — they appear only inside a workload's Pods tab.
- **Nodes** are the one good example: clickable row → `nodes/[nodeName]` detail page.

Net: astronomer's model is **"action menu + YAML dialog"**; Rancher's is **"click any row → detail
view with consistent tabs."** This plan converts the former into the latter.

**What we can build on (already exists):**
- Single‑resource fetch: `k8sGet(clusterId, path)` / `useK8sGetYaml` (`lib/api.ts:1923,1980`,
  `lib/hooks.ts:1958`); path builders `k8sResourcePath(type,name,ns)`, `k8sListPath`, `isNamespaced`,
  `getResourceDef` (`lib/k8s-paths.ts:74‑107`).
- `YamlViewDialog` (view/edit + dry‑run) → becomes the **YAML tab** (`components/ui/yaml-view-dialog.tsx`).
- Detail‑page patterns to generalize: `nodes/[nodeName]/page.tsx`,
  `workloads/[kind]/[namespace]/[name]/page.tsx` (tabbed), `PodLogsViewer`, `PodTerminal`,
  `MetricsChart`.
- The new **virtualized `DataTable`** (handles large CR lists), faceted filters, server‑side mode.
- Backend generic `/k8s/*` proxy already supports fetching any resource and `/apis` discovery.

---

## 1. Goals & non‑goals

### Goals
1. A reusable **`ResourceDetail`** view with tabs **Overview · YAML · Events · Related**, usable for
   any resource kind, URL‑addressable.
2. **Every** resource table row is clickable → its detail view (whole‑row click + a prominent name
   link), without losing the existing per‑row actions.
3. A real **pod detail** (containers, status, env, volumes, logs, exec, events).
4. A **dynamic CRD / custom‑resource explorer**: list CRDs, browse any CR's instances, drill into any
   CR with the generic detail view.

### Non‑goals
- Replacing the bespoke **workload** and **node** detail pages (they're good; optionally refactor them
  onto `ResourceDetail` later).
- Cluster provisioning / lifecycle (out of scope — astronomer is agent‑import).
- Kind‑specific *editing* forms beyond what exists (configmap/secret form editors stay; everything
  else edits via the YAML tab).

---

## 2. Priority gating

```
P0  Generic detail view + clickable rows (the 80% UX win)     ── GATE A ──
P1  Pod detail + Events/Related tabs                          ── GATE B ──
P2  Dynamic CRD / custom-resource explorer                    ── GATE C ──
```

| Gate | Pass criteria |
|---|---|
| **GATE A** | From any of the 15+ resource tables, clicking a row opens a `ResourceDetail` with working **Overview + YAML** tabs (YAML view/edit/dry‑run preserved); rows are visibly clickable; back‑nav works; detail URLs are shareable. |
| **GATE B** | Pods have a dedicated detail (overview + logs + exec + events); the **Events** and **Related** tabs are populated for common kinds. |
| **GATE C** | A CRD list + CR‑instance browser exists; any custom resource is clickable into the generic detail (Overview falls back to metadata+status, YAML always works); respects the security plan's CRD RBAC (F2). |

---

## 3. Workstream A — Generic `ResourceDetail` + detail route (P0) ⭐

### A1 — Single‑resource hook + detail route
- **Priority:** P0 · **Depends‑on:** —
- **Reasoning:** We need a URL‑addressable detail page for any resource (namespaced or cluster‑scoped).
  Use a **catch‑all child route** under the existing `[resource]` segment so both shapes work:
  `…/[resource]/ns/name` (namespaced) and `…/[resource]/name` (cluster‑scoped).
- **Files:** new `app/dashboard/clusters/[id]/[resource]/[...path]/page.tsx`; new
  `useK8sResource` hook (JSON, not just YAML).
- **Example:**
  ```ts
  // lib/hooks.ts — single object as JSON (mirrors useK8sGetYaml)
  export function useK8sResource(clusterId: string, path: string, enabled = true) {
    return useQuery({
      queryKey: queryKeys.clusterPages.k8sObject(clusterId, path), // add to factory
      queryFn: () => apiClient.k8sGet(clusterId, path),
      enabled: enabled && !!path,
    });
  }
  ```
  ```tsx
  // [resource]/[...path]/page.tsx — resolve ns/name from the catch-all slug
  const { resource, path: slug } = useParams(); // slug: string[]
  const [namespace, name] = isNamespaced(resource) ? slug : [undefined, slug[0]];
  const k8sPath = k8sResourcePath(resource, name, namespace);
  return <ResourceDetail clusterId={clusterId} resourceType={resource}
            namespace={namespace} name={name} k8sPath={k8sPath} />;
  ```
- **DoD:** navigating to a detail URL fetches and renders the object; cluster‑scoped vs namespaced both
  resolve correctly.
- **Tests:** unit on slug→(ns,name) resolution for namespaced & cluster‑scoped; render test with a
  mocked object.

### A2 — `ResourceDetail` shell with tabs (Overview + YAML first)
- **Priority:** P0 · **Depends‑on:** A1
- **Reasoning:** One component, consistent chrome (title, kind/namespace, age, actions), tabs. Ship
  **Overview + YAML** in GATE A; Events/Related land in GATE B (tabs render "coming soon"/empty‑safe
  until then). Follow the existing tabbed pattern from the workload detail page.
- **Files:** new `components/resources/resource-detail.tsx`; new `resource-overview.tsx`.
- **Example (shell):**
  ```tsx
  type Tab = 'overview' | 'yaml' | 'events' | 'related';
  export function ResourceDetail({ clusterId, resourceType, namespace, name, k8sPath }: Props) {
    const { data: obj, isLoading } = useK8sResource(clusterId, k8sPath);
    const perms = useResourcePermissions(resourceType); // existing permission hooks
    const [tab, setTab] = useState<Tab>('overview');
    return (
      <DetailShell title={name} kind={resourceType} namespace={namespace}
        actions={<RowActions ... />/* reuse the existing View/Edit/Delete actions */}>
        <Tabs value={tab} onChange={setTab} tabs={['overview','yaml','events','related']} />
        {tab === 'overview' && <ResourceOverview obj={obj} resourceType={resourceType} />}
        {tab === 'yaml' && <YamlPanel clusterId={clusterId} k8sPath={k8sPath}
                              title={`${resourceType}: ${name}`} allowEdit={perms.update.allowed} />}
        {tab === 'events' && <ResourceEvents .../>   /* WS-D */}
        {tab === 'related' && <RelatedResources .../> /* WS-D */}
      </DetailShell>
    );
  }
  ```
- **DoD:** the shell renders for any kind; tab switching works; actions (edit/delete) work from detail.
- **Tests:** render with a mock service/configmap object; tab switch; action wiring.

### A3 — `YamlPanel` (extract the dialog body into an embeddable tab)
- **Priority:** P0 · **Depends‑on:** —
- **Reasoning:** `YamlViewDialog` already does view/edit/dry‑run/diff; refactor its **body** into a
  `YamlPanel` that the dialog and the detail YAML tab both render (no behavior change to the dialog).
- **Files:** `components/ui/yaml-view-dialog.tsx` → extract `YamlPanel`; dialog wraps it.
- **DoD:** the existing dialog is visually/behaviorally unchanged; the same panel renders inside
  `ResourceDetail`'s YAML tab with full edit/dry‑run.
- **Tests:** existing YamlViewDialog tests still pass; new test mounting `YamlPanel` standalone.

### A4 — `ResourceOverview` (generic, with progressive kind‑specifics)
- **Priority:** P0 · **Depends‑on:** A2
- **Reasoning:** The human‑readable view. Start **generic** so it works for *every* kind immediately,
  then add kind‑specific sections for the common ones.
- **Generic sections (any object):** metadata (name, namespace, UID, created/age, labels,
  annotations), **owner references**, and — when present — `status.conditions` (as a table) and a
  compact spec/status summary. This alone beats "raw YAML only."
- **Kind‑specific (progressive, reuse existing column renderers):** Service (type, clusterIP, ports,
  selector, endpoints), Ingress (class, rules/hosts, TLS), ConfigMap/Secret (keys; secret values
  masked), PVC (status, capacity, SC, bound PV), HPA (targets/min/max/current), etc.
- **Files:** `components/resources/resource-overview.tsx` + small per‑kind renderers (can live in one
  file keyed by resourceType, falling back to generic).
- **DoD:** every kind shows a useful Overview (at minimum metadata + conditions); ≥5 common kinds have
  tailored sections.
- **Tests:** generic render for an arbitrary object; tailored render for Service + ConfigMap; secret
  values masked.

---

## 4. Workstream B — Make every resource table a clickable drill‑down (P0) ⭐

### B1 — Whole‑row click + name link across the generic browser
- **Priority:** P0 · **Depends‑on:** A1
- **Reasoning:** This is the change that makes the UI *feel* like Rancher. Each of the 16 tables in
  `[resource]/page.tsx` gets an `onRowClick` → the detail route, plus the name cell rendered as a
  visible link (so it reads as clickable and supports middle‑click/open‑in‑new‑tab). Keep the existing
  per‑row actions (they should `stopPropagation`).
- **Example:**
  ```tsx
  <DataTable
    data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace ?? ''}/${r.name}`}
    onRowClick={(row) => {
      if (!perms.read.allowed) return toastPermissionDenied(perms.read);
      router.push(detailHref(clusterId, resourceType, row.namespace, row.name));
    }}
  />
  // name column renders <Link href={detailHref(...)} onClick={(e)=>e.stopPropagation()}>{row.name}</Link>
  ```
  Add a `detailHref(clusterId, type, ns, name)` helper next to `k8sResourcePath`.
- **Files:** `[resource]/page.tsx` (the 15 non‑node tables), a `detailHref` helper, `lib/k8s-paths.ts`.
- **DoD:** every resource table row is clickable to its detail; per‑row actions still work without
  triggering navigation; events table (read‑only) may stay non‑clickable.
- **Tests:** a Playwright spec (reuse the cookie+mock harness) — click a Service row → detail URL +
  Overview/YAML render; clicking the row's Delete button does NOT navigate.

### B2 — Wire workloads & nodes consistently
- **Priority:** P0 · **Depends‑on:** B1
- **Reasoning:** Make the workload list row fully clickable (today only the name is a link) and keep
  nodes as‑is, so the whole app is consistent. Workload/node keep their bespoke detail pages.
- **Files:** `workloads/page.tsx` (add `onRowClick`).
- **DoD:** clicking anywhere on a workload row opens its detail; nodes unchanged.
- **Tests:** Playwright click‑row on workloads.

---

## 5. Workstream C — Pod detail (P1)

### C1 — Pod detail via `ResourceDetail` + pod‑specific overview, logs, exec, events
- **Priority:** P1 · **Depends‑on:** A2, A4
- **Reasoning:** Pods are the most‑inspected object and today have no standalone view. Render them
  through `ResourceDetail` with a pod Overview (phase, node, IP, QoS, restarts, **per‑container**
  status/image/resources/probes, env, volumes/mounts) and extra tabs **Logs** (`PodLogsViewer`) and
  **Exec** (`PodTerminal`) gated on `pods:logs`/`pods:exec`.
- **Files:** pod‑overview renderer in `resource-overview.tsx`; `ResourceDetail` gains optional
  Logs/Exec tabs when `resourceType === 'pods'`; ensure pods are reachable as a `[resource]` type and
  appear in workload‑detail Pods tab rows as links.
- **DoD:** clicking a pod (from a pod list or a workload's Pods tab) opens a pod detail with container
  status + logs + exec + events.
- **Tests:** Playwright — pod detail renders containers; Logs tab streams (mocked); Exec tab gated by
  permission.

---

## 6. Workstream D — Events & Related tabs (P1)

### D1 — Resource‑scoped Events tab
- **Priority:** P1 · **Depends‑on:** A2 · **Backend:** small filter
- **Reasoning:** Rancher's detail shows the object's events. Fetch events filtered by
  `involvedObject.name`/`kind`/`namespace`. Prefer a backend fieldSelector
  (`/api/v1/namespaces/{ns}/events?fieldSelector=involvedObject.name={name}`) via the existing proxy;
  fall back to client‑side filtering of the cluster events feed if needed.
- **Files:** `components/resources/resource-events.tsx`; optional small backend/event hook param.
- **DoD:** the Events tab lists only this object's events (type/reason/message/count/age).
- **Tests:** render with mocked events; assert filtering.

### D2 — Related Resources tab
- **Priority:** P1 · **Depends‑on:** A2
- **Reasoning:** Show the object graph: **owner refs** (up), **owned/selected** objects (down) —
  pods for workloads (label selector), endpoints for services, PVs for PVCs, etc. Start with owner
  refs (universal) + the 3–4 highest‑value relationships; each related item is itself a drill‑down
  link.
- **Files:** `components/resources/related-resources.tsx`; a small `relatedResolvers` map keyed by kind.
- **DoD:** owner refs always shown; ≥3 kinds show downstream relations; related items are clickable.
- **Tests:** resolver unit tests; render with a Deployment→ReplicaSet→Pods fixture.

---

## 7. Workstream E — Dynamic CRD / custom‑resource explorer (P2)

> **Depends on the security plan's F2** (decide & enforce CRD/non‑resource RBAC) — the explorer must
> honor whatever per‑resource policy F2 establishes, not silently rely on the generic `clusters` verb.

### E1 — API discovery + CRD list
- **Priority:** P2 · **Depends‑on:** A‑gate, security‑F2
- **Reasoning:** Rancher's superpower is browsing *every* type, including CRDs. Use apiserver
  discovery (`/apis` + each group's `APIResourceList`) — already proxyable — to enumerate resource
  types; list CRDs (`apiextensions.k8s.io/v1 customresourcedefinitions`) with group/version/scope.
- **Files:** `useApiResources` / `useCRDs` hooks; a "Custom Resources" nav entry + CRD list page.
- **DoD:** a page lists all CRDs (and optionally all served API resources) with scope and group.
- **Tests:** hook parses a mocked discovery doc; CRD list renders.

### E2 — Generic CR list + detail
- **Priority:** P2 · **Depends‑on:** E1, A2
- **Reasoning:** Clicking a CRD lists its **instances** via the dynamic path
  (`/apis/{group}/{version}[/namespaces/{ns}]/{plural}`) in the **virtualized** `DataTable` (CR lists
  can be large), each row clickable into `ResourceDetail`. The generic Overview (metadata + status +
  `status.conditions`) + the always‑available YAML tab make *any* CR viewable/editable without
  per‑kind code.
- **Files:** generic CR list page (reuse the virtualized table); `k8sResourcePath`/`detailHref`
  extended to arbitrary group/version/plural.
- **DoD:** browse any CRD's instances and drill into any CR (Overview falls back to generic; YAML
  edit/dry‑run works).
- **Tests:** Playwright — list a sample CRD's instances (mocked, 1000+ rows → virtualized/bounded DOM),
  open one, edit YAML.

---

## 8. Testing & validation strategy
1. **Type‑check + lint** on every change; **0 lint warnings** (matches current bar).
2. **Jest** unit tests for: slug→(ns,name) resolution, `detailHref`/path builders, `ResourceOverview`
   generic + tailored renders (secret masking), related resolvers, discovery parsing.
3. **Real‑browser Playwright** (system‑chromium harness already wired): click‑row‑→‑detail for a
   namespaced + a cluster‑scoped kind; action‑button does‑not‑navigate; pod logs/exec gating; CR list
   virtualized + drill‑down.
4. **No‑regression:** `YamlViewDialog` tests still pass after the `YamlPanel` extraction; existing
   workload/node detail pages unaffected.
5. **Permissions:** detail view + tabs respect existing `read/update/delete/logs/exec` permission gates
   (drill‑down must not become a permission bypass — read‑gated).

---

## 9. Risks
| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Generic Overview is too sparse for some kinds → "still just YAML" feeling | Med | Med | Generic metadata+conditions is the floor; ship tailored sections for the top ~8 kinds in A4; YAML tab always available. |
| Catch‑all route ambiguity (namespaced vs cluster‑scoped slug) | Med | Low | Drive ns/cluster decision from `isNamespaced(resourceType)`; unit‑test both; 404 on malformed slug. |
| CR explorer hits the CRD‑RBAC gap (security M3) | Med | Med | Gate WS‑E on security **F2**; until then, scope WS‑E behind a flag. |
| Resource‑scoped events need a backend filter not yet present | Low | Low | Client‑side filter fallback; add the fieldSelector param as a small backend follow‑up. |
| Whole‑row click conflicts with inline action buttons | Med | Low | `stopPropagation` on action controls; Playwright asserts buttons don't navigate. |

## 10. Execution checklist
```
P0 — generic detail + clickable rows                    ── GATE A
  [ ] A1  useK8sResource + catch-all detail route ([resource]/[...path])
  [ ] A3  Extract YamlPanel from YamlViewDialog (no dialog regression)
  [ ] A2  ResourceDetail shell (Overview + YAML tabs)
  [ ] A4  ResourceOverview (generic + ≥5 kind-specific)
  [ ] B1  onRowClick + name link across the 15 resource tables (actions stopPropagation)
  [ ] B2  Whole-row click on workloads; nodes consistent

P1 — pod detail + events/related                        ── GATE B
  [ ] C1  Pod detail (overview + logs + exec + events)
  [ ] D1  Resource-scoped Events tab
  [ ] D2  Related Resources tab (owner refs + top relationships)

P2 — dynamic CRD / custom-resource explorer             ── GATE C  (needs security-F2)
  [ ] E1  API discovery + CRD list
  [ ] E2  Generic CR list (virtualized) + CR detail
```

## Appendix — How this composes with work already done
- **Virtualized `DataTable`** (C0/C1) powers large CR lists in WS‑E.
- **Faceted filters + server‑side mode** (B3/B4) apply to the resource/CR lists.
- **Router‑isolation adapters** (D1–D3) keep the new routes Vite‑portable.
- **Backend generic `/k8s/*` proxy** already supports fetch/list/edit for any kind — so WS‑A/B/E are
  overwhelmingly **frontend composition**, with only a small optional events‑filter backend touch.
- **Security:** the agent‑authz hardening (esp. **F2** CRD RBAC and the read‑only‑token fixes) is the
  correct authorization substrate for the explorer; this plan is sequenced to follow it.
