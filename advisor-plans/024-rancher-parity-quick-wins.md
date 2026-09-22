# Plan 024: Rancher parity quick wins over APIs that already exist — Helm upgrade, rendered branding and banners, Clone/Download YAML, inline labels & annotations, project members, probe and typed-secret fields, rows-per-page, and a Rancher-shaped Home

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/routes/dashboard/catalog frontend/src/lib/api/public-settings.ts frontend/src/routes/dashboard/settings/platform/index.tsx frontend/src/components/layout/sidebar.tsx frontend/src/routes/auth/login/index.tsx frontend/src/routes/dashboard/route.tsx frontend/src/components/resources frontend/src/routes/dashboard/projects frontend/src/lib/api/user-preferences.ts frontend/src/components/clusters/estate-clusters-table.tsx frontend/src/routes/dashboard/index.tsx docs/openapi.yaml internal/userpreferences`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1 (steps 1–2 are P0-adjacent: finished features that do nothing)
- **Effort**: M–L (2–3 days total; each step is independently shippable)
- **Risk**: LOW (all additive; one schema field)
- **Depends on**: 021 for `ResourceMasthead`/`Card` in step 5 (can use existing primitives if 021 is not merged); 019 already adds `pinned_clusters` — do not duplicate it
- **Category**: direction (Rancher parity), bug (unused finished code)
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

Each item below is something a Rancher operator does many times a day and Astronomer either already built and never wired (a complete Helm upgrade modal is never imported; branding/banner settings save with a success toast and change nothing — for regulated customers the classification banner is a compliance control), or can do in a few hours over an existing API (clone/download YAML, inline label edits, project members, secret types, probe fields, rows-per-page). Together they remove most of the "why can't I…" moments a Rancher migrant hits in the first week.

## Current state

**Helm upgrade** — `frontend/src/routes/dashboard/catalog/-upgrade-chart-modal.tsx:13-19`:

```tsx
export function UpgradeChartModal({ installation, onClose }: { installation: InstalledChart; onClose: () => void }) {
  const versionsQuery = useInstalledChartUpgradeVersions(installation.id);
  const upgrade = useUpgradeInstalledChart();
```

`grep -rl UpgradeChartModal frontend/src` → only its own file. `routes/dashboard/catalog/-installed-tab.tsx:96-125` actions column: comment `UX-06: hide Upgrade until an upgrade modal / version picker is wired`, then Rollback (`onRollback(row.id, row.revision - 1)`) and Uninstall (`setUninstallTarget(row)`) raw buttons. `routes/dashboard/catalog/index.tsx:113` `const rollback = useRollbackChart();` and `:312` `onRollback={(id, revision) => rollback.mutate({ id, revision })}` is the wiring to mirror. API: `docs/openapi.yaml` `GET /api/v1/catalog/installed/{id}/upgrade-versions/` and `POST /api/v1/catalog/installed/{id}/upgrade/`.

**Branding/banners** — `frontend/src/lib/api/public-settings.ts:31-37` exports `getPublicBranding()` and `getPublicBanner()` (0 consumers; only `getRegistrationTLS` is used). Admin editor `routes/dashboard/settings/platform/index.tsx:202-250` edits `banners.loginBannerText`, `banners.globalBannerText`, `banners.globalBannerColor` (`info|warning|critical`) with a `BannerPreview` component in the same file. Product name hard-coded: `components/layout/sidebar.tsx:171` ("Astronomer") and `:174` ("by AlphaBravo"); `routes/auth/login/index.tsx:163,223` ("Astronomer"), `:233` ("Sign in to Astronomer"). Dashboard shell `routes/dashboard/route.tsx` renders `<Topbar />`, an offline banner (`{!online && (...)}`), then `<main>`. APIs: `GET /api/v1/settings/branding`, `GET /api/v1/settings/banner` (public).

**Row actions** — `components/resources/resource-list-page.tsx:128-203` builds `items: ActionMenuItem[]` (Execute Shell, View Logs, View YAML, Edit YAML, Scale, Restart, Delete). YAML is fetched by `useK8sGetYaml(clusterId, path)` (`lib/hooks/kubernetes-proxy.ts:137-145`) / `k8sGetYaml`. `CreateResourceDialog` (`components/resources/create-resource-dialog.tsx`) seeds from `k8sTemplates[templateKey]`; 019 makes `templateKey` optional — add an `initialYaml?: string` prop here (compatible with 019).

**Labels/annotations** — `components/resources/resource-overview.tsx:216-221` renders them via a read-only `KeyValueTable`; editing only via the YAML tab (`yaml-view-dialog.tsx`). The guided editor already patches through a dry-run path; a metadata-only merge patch can use the same `k8s` mutation hooks (find `useK8sPatch` or the apply hook in `lib/hooks/kubernetes-proxy.ts`).

**Project members** — `routes/dashboard/projects/$id/index.tsx:72-73` a "Members" stat = `project.members?.length ?? 0`; `components/projects/` has `namespaces-card.tsx`; binding creation lives in `routes/dashboard/rbac/-binding-modal.tsx:56-81` (project scope) with `useApplyProjectRoleTemplate` at `:32,69` and the principal picker `components/rbac/principal-picker.tsx`.

**Probes** — `components/resources/guided-resource-advanced-section.tsx:141-163` writes only `readinessProbe.httpGet.path` / `livenessProbe.httpGet.path` (no port → API server rejects). Read side `components/resources/pod-resource-overview.tsx:262-263` summarizes all three probes.

**Secrets** — `components/resources/guided-resource-access-sections.tsx:24-49`: one `stringData` key=value textarea; `data` cleared at `:42`; no `type` selector. Registry secrets have their own better surface (`routes/dashboard/clusters/$id/registries/index.tsx`).

**Preferences** — `docs/openapi.yaml:4155-4200` `UserPreferences` (theme, table_density, landing_route, time_format, favorites; 019 adds `pinned_clusters`). Table page size: find the constant in `components/ui/use-data-table-state.ts` / `data-table-pagination.tsx`. Preferences page `routes/dashboard/account/preferences/index.tsx:64-109` has four selects.

**Home** — `routes/dashboard/index.tsx:37-39` `useClusters({ pageSize: 10 })`; `components/clusters/estate-clusters-table.tsx:14-93` columns Name, Status, Version, Nodes, Pods, CPU, Memory — no Provider/Distribution though `cluster.provider` and `cluster.distribution` exist (used at `routes/dashboard/clusters/index.tsx`). Rancher `pages/home.vue:133-157` columns: State, Name, Provider (+distro), Version, CPU, Memory, Pods; `:496-498,644-646` "showing some of N / show all"; `:453-459` dismissible `BannerGraphic` + `DynamicContentBanner`.

Conventions: `ActionMenu`/`ActionButton`, `ModalShell`, `toastSuccess`/`toastApiError`, `useAppForm` forms, `Card`, query keys from `lib/query-keys.ts`.

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Frontend gate | `cd frontend && npm run type-check && npm run lint && npm test` | exit 0 |
| Smoke | `cd frontend && npm run test:e2e:smoke` | pass |
| OpenAPI (step 8 only) | `make openapi-generate && cd frontend && npm run openapi:check` | exit 0 |
| Backend (step 8 only) | `go test ./internal/userpreferences/... && go vet ./internal/...` | pass |

## Scope

**In scope**: files named above; new `components/layout/global-banner.tsx`, `lib/hooks/public-settings.ts`, `components/projects/members-card.tsx`, `components/resources/probe-fields.tsx`, `components/resources/labels-annotations-editor.tsx`; `docs/openapi.yaml` `UserPreferences.rows_per_page` + `date_format`; `internal/userpreferences/preferences.go` (+test); generated outputs.

**Out of scope**: array-valued guided fields (025), group-by-namespace/bulk actions (025), `questions.yaml` (025), cert expiry (deferred), header kubeconfig/import (019), pinned clusters (019).

## Git workflow

- Branch per step group: `advisor/024-helm-upgrade`, `advisor/024-branding`, `advisor/024-explorer-actions`, `advisor/024-forms`, `advisor/024-prefs-home`.
- Conventional commits (`feat(ui): …`).

## Steps

### Step 1: Wire the Helm upgrade modal

- `routes/dashboard/catalog/index.tsx`: add `const [upgradeTarget, setUpgradeTarget] = useState<InstalledChart | null>(null)`; render `{upgradeTarget && <UpgradeChartModal installation={upgradeTarget} onClose={() => setUpgradeTarget(null)} />}` next to the rollback/uninstall wiring (~line 312); pass `onUpgrade={setUpgradeTarget}` into `InstalledTab`.
- `-installed-tab.tsx`: replace the raw buttons with an `ActionMenu` (`@/components/ui/action-menu`) containing Upgrade (gated by the same permission as Rollback; disabled with reason when `versionsQuery` has no candidates is handled inside the modal), Rollback (disabled `row.revision <= 1`), Uninstall (destructive). Delete the `UX-06` comment.
- Ensure `UpgradeChartModal` invalidates the installed list on success (check `useUpgradeInstalledChart` in `lib/hooks/catalog.ts`; add `queryClient.invalidateQueries(queryKeys.catalog.installed…)` if missing).
- Also expose Upgrade on the per-cluster Apps installed view (`routes/dashboard/clusters/$id/apps/index.tsx` `InstalledView`, ~628–700) if it renders the same `InstalledChart` rows; otherwise leave a follow-up note.

**Verify**: extend `routes/dashboard/catalog/repositories-table.test.tsx` or add `installed-tab.test.tsx`: an installed row's menu contains "Upgrade"; clicking opens a modal titled `Upgrade <releaseName>`. `grep -rl UpgradeChartModal frontend/src | wc -l` ≥ 2.

### Step 2: Render branding and banners

- `lib/hooks/public-settings.ts`: `useBranding()` and `useBanner()` (`useQuery` over `getPublicBranding`/`getPublicBanner`, `staleTime: 5 min`, `retry: 1`, keys from the factory). Both must degrade: on error return `undefined` and let callers fall back to today's defaults.
- `components/layout/global-banner.tsx`: renders `banner.globalBannerText` with the `globalBannerColor` tone using `StatePanel`-like styling (info = `status-info`, warning = `status-warning`, critical = `status-error`), `role="status"`, non-dismissible (it is an admin control). Mount in `routes/dashboard/route.tsx` directly under `<Topbar />` (above the offline banner).
- Login: render `loginBannerText` above the form in `routes/auth/login/index.tsx` (both the desktop hero at ~158 and the mobile header at ~220 if they are separate layouts); replace the three "Astronomer" strings with `branding?.productName ?? "Astronomer"`; if branding exposes a logo URL, render it in place of the `Orbit` icon (`<img alt={productName}>`), else keep the icon. Same in `sidebar.tsx:166-176`.
- Apply `branding.primaryColor` (if present) by setting `--primary` on `document.documentElement` in a small effect inside the dashboard layout (convert hex → HSL triplet to match `globals.css` token format; write a 10-line helper with a unit test).
- Remove or keep `BannerPreview` on the platform settings page — keep it; it is now a true preview.

**Verify**: `components/layout/global-banner.test.tsx`: renders text with the right tone class; renders nothing when text is empty; renders nothing when the query errors. Smoke crawl stub for `/settings/banner` (add to `tests/e2e-smoke/stub-overrides.ts`) → gallery `dashboard.png` shows the banner; `auth_login.png` shows the login banner.

### Step 3: Clone and Download YAML row actions

In `resource-list-page.tsx` (and the `resource-detail.tsx` action bar if it has an action menu):
- "Download YAML": fetch via `k8sGetYaml(clusterId, path)` and save as `${namespace}-${name}.yaml` (Blob + `<a download>`); disabled by `permissions.read`.
- "Clone": fetch YAML, `yaml.load`, strip `metadata.uid`, `resourceVersion`, `creationTimestamp`, `generation`, `managedFields`, `ownerReferences`, `selfLink`, and the whole `status`; set `metadata.name = \`${name}-copy\``; open `CreateResourceDialog` with a new `initialYaml` prop (YAML mode). Disabled by `permissions.create` (add to the decisions object if absent; check `resource-action-policy.ts`).

**Verify**: unit test for the strip function (`lib/k8s-clone.ts` + test) covering all stripped keys; `resource-list-page.test.tsx` asserts both menu items exist.

### Step 4: Inline labels & annotations editor

`components/resources/labels-annotations-editor.tsx`: an "Edit" toggle on the overview's `KeyValueTable` for `metadata.labels` and `metadata.annotations` that turns rows into key/value inputs with add/remove, validates label keys/values against the Kubernetes regex (`[a-z0-9A-Z]([-a-z0-9A-Z_.]*[a-z0-9A-Z])?` with optional `prefix/`, ≤ 63 chars for the name part; values ≤ 63), and submits a JSON merge patch limited to `{ metadata: { labels, annotations } }` via the existing patch hook (`null` to delete a key). Gate on the same update permission the YAML tab uses. Toast on success; inline error on failure.

**Verify**: unit test: removing a key produces `null` in the patch body; invalid key blocks submit with an error message.

### Step 5: Project members card

> **Reconciled 2026-09-22 (executor STOP):** `project.members` is hardcoded `[]` in `lib/api/project-detail.ts` and the project GET carries no roster. Membership is the list of project-scoped RBAC bindings: `lib/api/rbac.ts` (~310–330) already lists them with a `project_id` query filter and `-bindings-tab.tsx` renders the same rows. The card is built from that list (principal + role; Remove = delete project binding; Add = binding modal with `fixedScope`). No new endpoint. `lib/api/rbac.ts` / `lib/hooks/rbac*.ts` are in scope read-only (plus a thin typed wrapper if needed).


`components/projects/members-card.tsx`: lists `project.members` (name, principal type, role) with a Remove action (confirm via `ConfirmDialog`) and an "Add member" button that opens the existing binding modal from `routes/dashboard/rbac/-binding-modal.tsx` pre-scoped to `project` with `projectId` fixed and the scope selector hidden (add props `fixedScope?: { kind: "project"; projectId: string }`). Mount it under the Namespaces card on `projects/$id/index.tsx`; replace the numeric "Members" stat's value with a link to the card.

**Verify**: `components/projects/__tests__/members-card.test.tsx`: renders members; Add opens the modal with the project preselected and the scope select absent.

### Step 6: Probe fields and typed secrets

- `components/resources/probe-fields.tsx`: `ProbeFields({ kind: "readiness"|"liveness"|"startup" })` — type select (`httpGet` | `tcpSocket` | `exec`), then path+port(+scheme) / port / command, and `initialDelaySeconds`, `periodSeconds`, `timeoutSeconds`, `failureThreshold`. Writes the full probe object at `containerPath(kind) + ["<kind>Probe"]` in the guided model; a missing port blocks submit. Replace the two path-only fields in `guided-resource-advanced-section.tsx:141-163` and add startup.
- Secrets: add a `type` select to `SecretSection` in `guided-resource-access-sections.tsx` (Opaque / `kubernetes.io/tls` / `kubernetes.io/basic-auth` / `kubernetes.io/ssh-auth`); for the typed variants render fixed fields (`tls.crt`/`tls.key`; `username`/`password`; `ssh-privatekey`) and set `type`; keep the freeform textarea for Opaque.

**Verify**: extend the guided-form tests (`components/resources/__tests__/`) — a readiness probe without port is invalid; a TLS secret manifest contains `type: kubernetes.io/tls` and both keys.

### Step 7: Home: provider column, "showing N of M", dismissible banner slot

- `estate-clusters-table.tsx`: add a `provider` column after Status rendering `cluster.provider` with `cluster.distribution` as a muted sublabel (match how `routes/dashboard/clusters/index.tsx` renders them).
- `routes/dashboard/index.tsx`: read the total from the clusters query (`clustersQuery.data?.total` or the pagination shape it returns) and render "Showing 10 of N clusters · View all" when `N > 10`.
- Add a dismissible `WelcomeBanner` (a `Card` with the product name, the docs link, and "Register your first cluster" when the estate is empty), dismissal stored in `localStorage` key `astronomer.home.welcomeDismissed` (try/catch).

**Verify**: gallery `dashboard.png` shows a Provider column; unit test for the "Showing 10 of N" string at N = 25.

### Step 8: `rows_per_page` and `date_format` preferences

- `docs/openapi.yaml` `UserPreferences`: add `rows_per_page: { type: integer, enum: [10, 25, 50, 100] }` and `date_format: { type: string, enum: [locale, iso, relative] }` (not required). Backend `Validate()` + tests. `make openapi-generate`.
- `defaultUserPreferences`: `rows_per_page: 25`, `date_format: "locale"`.
- `use-data-table-state.ts`: initial page size from `useUserPreferences().preferences.rows_per_page` (fallback to the current constant).
- `lib/utils.ts` `formatRelativeTime`/date formatters: honour `date_format` where a `time_format` switch already exists (mirror its wiring).
- Preferences page: two more selects.

**Verify**: backend tests pass; `npm run openapi:check` exit 0; `data-table.behavior.test.tsx` case: preference 50 → first page renders up to 50 rows.

### Step 9: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
go test ./internal/userpreferences/... && go vet ./internal/...
```

## Test plan

Per step above. Add smoke stubs for `/settings/banner` and `/settings/branding` so the crawl exercises the banner path.

## Done criteria

- [ ] Gates exit 0
- [ ] `grep -rl UpgradeChartModal frontend/src | wc -l` ≥ 2; `grep -n "UX-06" frontend/src/routes/dashboard/catalog/-installed-tab.tsx` → 0
- [ ] `grep -rn "getPublicBranding\|getPublicBanner" frontend/src | grep -v lib/api/public-settings` → ≥ 2 hits
- [ ] `grep -n '"Astronomer"' frontend/src/components/layout/sidebar.tsx` → 0 (uses branding with fallback)
- [ ] Row menu contains "Clone" and "Download YAML" (test)
- [ ] `grep -n "rows_per_page" docs/openapi.yaml internal/userpreferences/preferences.go` → hits
- [ ] Provider column present in `estate-clusters-table.tsx`
- [ ] README row updated

## STOP conditions

- Excerpts don't match live code.
- The branding endpoint has no product-name/logo fields (only colours) — render what exists and report the gap; do not invent fields.
- The k8s patch hook only supports strategic-merge or full apply, not JSON merge patch — use whatever it supports for `metadata.labels`/`annotations` but STOP if deleting a key is impossible without a full-object write.
- (Resolved: see Step 5 note — use the project-scoped bindings list.)
- Adding enum fields to `UserPreferences` breaks `additionalProperties: false` clients in `tests/e2e-live` — update fixtures; if a live fixture cannot be changed in scope, STOP.

## Maintenance notes

- Branding is public and unauthenticated by design (login page needs it); never add secrets to that payload.
- Clone strips a fixed key list; if the API server later rejects a cloned object because of a new server-managed field, extend `lib/k8s-clone.ts` and its test.
- Deferred to 025: multi-container/array editing that the probe fields sit inside; group-by-namespace; bulk restart; `questions.yaml`.
