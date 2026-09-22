# Plan 023: Never lie during an outage — error-truthful pages, unsaved-changes guard, form validation/error summaries, router-native URL state, and the seven functional bugs

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/routes/dashboard/delivery/index.tsx "frontend/src/routes/dashboard/clusters/\$id/apps/index.tsx" "frontend/src/routes/dashboard/clusters/\$id/index.tsx" frontend/src/routes/dashboard/clusters/register/index.tsx frontend/src/routes/dashboard/settings/widgets/index.tsx frontend/src/components/resources/resource-detail.tsx frontend/src/routes/dashboard/search/-page.tsx frontend/src/components/extensions frontend/src/components/ui/query-states.tsx frontend/src/components/ui/form-shell.tsx frontend/src/lib/form.ts`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P0 for steps 1–3 (truth bugs), P1 for the rest
- **Effort**: L overall (steps 1–3 are S and can ship first as their own PR)
- **Risk**: LOW–MED (`validateSearch` additions change typed search contracts)
- **Depends on**: none for steps 1–6; step 7 (tabs URL state on Apps) coordinates with 022 step 2
- **Category**: bug, UX, tests
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

The delivery overview is the page an operator checks to decide whether a fleet rollout is healthy. When its API calls fail it renders "0 drifted, 0 active rollouts" and hides the degraded-sources warning — an outage looks like a green fleet. Cluster Apps and the anomaly-baselines panel render errors as "nothing installed / no baselines computed yet", which sends operators to reinstall charts that are running. The register-cluster wizard loses its draft id on refresh, so Back exits the wizard and the operator's own draft name reports as "taken". Beyond those, nothing in the app guards unsaved work, 26 of 47 forms have no client validation and 32 no error summary, five widget mutations fail silently, deleting a resource pops the browser history (possibly out of the app), list filters are lost on refresh, three pages bypass the router with `history.replaceState`, and extension sidebar links 404. None of this requires new backend work.

## Current state

**UX-01 delivery overview**, `frontend/src/routes/dashboard/delivery/index.tsx`:
- queries at 611 (`sources`), 618 (`unhealthySources`), 628 (`bundles`), 635 (`targets`), 642 (`rollouts`), 651 (`deployments`), plus `system`.
- `:733-745` `(rollouts.data?.data ?? []).filter(...).length` → "Active rollouts"; `:670-682` `deployments.data?.data ?? []` → `drifted`; `:764` warning banner gated on `unhealthySources.data &&`; `:778` `{system.isError && <ErrorMessage error={system.error} />}` is the **only** error branch.

**UX-02 empty-state-on-error**:
- `routes/dashboard/clusters/$id/apps/index.tsx:714-728` — `InstalledView`: `if (q.isLoading) {…}` then `if (items.length === 0) { … "No apps installed yet" }`; same shape in `RecommendedView` at `:1153-1162`.
- `routes/dashboard/clusters/$id/index.tsx:934-959` — `AnomalyBaselinesPanel` guards `isLoading` then `(data ?? []).slice(0,5)` → "No baselines computed yet. The nightly … task fills these in".
- `components/ui/query-states.tsx:33-40` documents that this is exactly what `QueryStates` prevents; 44 routes use it.
- Also `routes/dashboard/projects/$id/index.tsx:54` calls `renderForProject(project.id)` before `project` is guaranteed; guard with `enabled: !!project?.id` (visible in the stubbed gallery as "Widgets: Missing path parameter id").

**UX-06 register wizard**, `routes/dashboard/clusters/register/index.tsx`:

```ts
28  const { clusterId } = Route.useSearch();
29  const [draftClusterId, setDraftClusterId] = useState<string | null>(null);
...
77  const cluster = draftClusterId ? await updateCluster(draftClusterId, {...}) : await createCluster({...});
...
135 const nameTaken = name.length > 0 && (nameMatches.data?.pages ?? []).some((page) =>
140   page.data.some((cluster) => cluster.id !== draftClusterId && cluster.name.toLowerCase() === name.trim().toLowerCase()));
...
150 onBack={() => { if (draftClusterId === clusterId) { navigate({ to: "/dashboard/clusters/register", search: registrationSearch(), replace: true }); } else { navigate({ to: "/dashboard/clusters" }); } }}
```

**UX-07 Apps section**, `routes/dashboard/clusters/$id/apps/index.tsx:229-231` `const [section, setSection] = useState<Section>(requestedSection ?? (requestedInstall ? "browse" : "installed"))`; `:484` `onClick={() => setSection(s)}`; `:745-756` a `RouterLink search={{ section: "browse" }}` whose `onClick` calls `e.preventDefault()` then `document.querySelector<HTMLButtonElement>("nav button:nth-of-type(2)")?.click()`. `setProjectId` at `:213-221` already navigates with search params — the URL-write path exists in the same component.

**UX-10 widgets**, `routes/dashboard/settings/widgets/index.tsx:145-170` — five `useMutation` calls with `onSuccess` only. The settings convention is `components/settings/hooks.ts:156-430` (`toastSuccess` + `toastApiError` on every mutation). Toast helpers: `@/lib/toast` (`toastSuccess`, `toastError`, `toastApiError`).

**UX-11 history.back**, `components/resources/resource-detail.tsx:189` (Back arrow) and `:228` (`onDeleted={() => window.history.back()}`); `routes/dashboard/admin/users/$id/index.tsx:153`. The node page does it right: `routes/dashboard/clusters/$id/nodes/$nodeName/index.tsx:704` navigates to the parent list.

**UX-03 unsaved guard**: `grep -rn "useBlocker\|beforeunload" frontend/src` → 0. `components/ui/yaml-view-dialog.tsx:174-179` `changeEditMode(false)` sets `setEditedYaml(undefined)` (silent discard). `FormShell` is `components/ui/form-shell.tsx:8` (the only `<form>` element in route code); the form kit is `lib/form.ts:20-37` (`useAppForm` with `TextField`/`SelectField`/`TextareaField` and `FormErrorSummary` at line 37).

**UX-04/05 forms**: `grep -rl useAppForm routes components | xargs grep -L validators` → 26 files (e.g. `settings/gitops/new/index.tsx:62-96` — bare `<Input required>` inside `form.Field`, no error slot; `settings/vault/index.tsx`, `settings/smtp/index.tsx`, `settings/auth/settings/index.tsx`, `settings/backup/index.tsx`, `clusters/register/index.tsx`). `… | xargs grep -L FormErrorSummary` → 32 files. The correct pattern: `clusters/register/index.tsx:199` `<form.FormErrorSummary serverError={submissionError} />`.

**UX-12/13 URL state**: `routes/dashboard/clusters/index.tsx:41` `const [search, setSearch] = useState("")`; `clusters/$id/workloads/index.tsx:29`; `projects/index.tsx:34`; `alerting/baselines/index.tsx:26`; `logging/-operations-tab.tsx:16-17`; `clusters/$id/registries/index.tsx:628`. Router bypass: `routes/dashboard/search/-page.tsx:133-151`:

```ts
  useEffect(() => {
    const params = new URLSearchParams();
    params.set("type", resourceType);
    if (debouncedNamespace) params.set("namespace", debouncedNamespace);
    ...
    window.history.replaceState(window.history.state, "", `${pathname}${qs ? `?${qs}` : ""}`);
  }, [...]);
```

and `routes/dashboard/logging/-outputs-tab.tsx:66-70`, `routes/dashboard/logging/-logging-query-dialog.tsx:317`. `routes/dashboard/workloads/index.tsx:5-12` already declares `validateSearch` for these keys. The URL-param hook pattern is `lib/use-tab-param.ts` (validates against allowed keys, `replace: true`, preserves other params).

**UX-17 extensions**, `components/extensions/ExtensionNavItems.tsx:18-20` `extensionSidebarHref(name) → /dashboard/extensions/${encodeURIComponent(name)}`; `routes/dashboard/extensions/` contains only `index.tsx`. The full-page mount components are `SandboxedExtension` / `ExtensionSlot` under `components/extensions/`.

**UX-18 a11y**: `components/layout/sidebar-navigation-view.tsx:159-170` group toggle without `aria-expanded`; `routes/dashboard/clusters/$id/resources/index.tsx:118-124` collapsible sections; `routes/dashboard/settings/network-policies/index.tsx:122-128` icon-only `<Trash2>` delete button with no `aria-label`.

**Rollout detail gating**: `routes/dashboard/delivery/rollouts/$rolloutId/index.tsx` renders "No projects available / Select a project" before loading a rollout whose id is in the URL (gallery `dashboard_delivery_rollouts_rollout-smoke-1.png`). Read the file; the rollout's own `project_id` should seed the project scope.

**Tests**: 18 route-adjacent test files for 183 route components; zero for register, delivery index, targets detail, node detail, apps, agents, security, dashboard index. Smoke crawl (`tests/e2e-smoke/route-crawl.spec.ts`) is render-only over stubs (`tests/e2e-smoke/stubs.ts`, `stub-overrides.ts`).

## Commands you will need

| Purpose | Command (in `frontend/`) | Expected |
|---|---|---|
| Gate | `npm run type-check && npm run lint && npm test` | exit 0 |
| Smoke | `npm run test:e2e:smoke` | pass |
| One spec | `npx playwright test tests/e2e/<spec>.spec.ts --project=chromium` | pass |
| Gallery | `SMOKE_GALLERY=1 npx playwright test --project=route-smoke` | PNGs |

## Scope

**In scope**: the files named above; new `lib/use-unsaved-guard.ts`, `lib/use-search-param.ts`, `routes/dashboard/extensions/$name/index.tsx`, `tests/e2e-smoke/error-states.spec.ts`, `components/ui/__tests__/query-error-harness.tsx`; `validateSearch` additions on the eight list routes; `tests/e2e-smoke/route-manifest` expected count (+1 route).

**Out of scope**: visual/primitive changes (021/022), nav (018–020), backend, the YAML editor's dry-run/apply logic (only its discard path gets a guard).

## Git workflow

- Branch `advisor/023-truthful-states` for steps 1–3 (ship first, small PR); `advisor/023-forms-and-url-state` for the rest.
- Conventional commits (`fix(ui): …`).

## Steps

### Step 1: Delivery overview error roll-up (UX-01)

In `delivery/index.tsx`:
- Compute `const failed = [sources, unhealthySources, bundles, targets, rollouts, deployments, system].filter(q => q.isError)`.
- If `failed.length > 0`, render a page-level `StatePanel` (tone `danger`, title "Delivery status unavailable", description listing which datasets failed, `actionLabel="Retry"` calling `refetch()` on each) **above** the cards, and render every count that depends on a failed query as `—` with `aria-label="unavailable"` instead of a number. Never compute `drifted`/active rollouts from `?? []` when the source query `isError`.
- Keep the degraded-sources banner logic but show it when `unhealthySources.isSuccess && data.length > 0`; when `isError`, the page-level panel already covers it.

**Verify**: new unit test `routes/dashboard/delivery/index.test.tsx`: mock `rollouts` and `deployments` to reject → page shows the danger panel and no "0" in the Drifted/Active rollouts tiles. Model on `routes/dashboard/delivery/rollouts/$rolloutId/index.test.tsx` for router/query setup.

### Step 2: `QueryStates` on the three empty-on-error sites + project widgets guard (UX-02)

Wrap `InstalledView`, `RecommendedView`, and `AnomalyBaselinesPanel` bodies in `<QueryStates query={q} isEmpty={(d) => d.length === 0} empty={<EmptyState …/>}>…</QueryStates>` and delete the hand-rolled ladders. In `projects/$id/index.tsx` only mount `WidgetGrid` when `project?.id` is set.

**Verify**: tests for each: rejected query → error state text, not the empty-state text. `grep -n '"No apps installed yet"' apps/index.tsx` still 1 (inside `empty=`).

### Step 3: Register wizard draft identity (UX-06)

- Replace `draftClusterId` state with `const draftClusterId = clusterId ?? null` (from `Route.useSearch()`); on successful create, `navigate({ search: { clusterId: cluster.id }, replace: true })` (it likely already does for step 2 — keep).
- When re-entering step 1 with `?clusterId=`, prefill the form from `useCluster(clusterId)` (`display_name`, `description`, `environment`, `region`, `api_server_url`, …).
- `onBack` on the connect step always returns to step 1 with `search: { clusterId }` preserved.

**Verify**: `routes/dashboard/clusters/register/index.test.tsx` (new): render with `?clusterId=abc` → form prefilled from the mocked cluster; `nameTaken` false for the draft's own name; Back navigates to step 1 not `/dashboard/clusters`.

### Step 4: Unsaved-changes guard (UX-03)

- `lib/use-unsaved-guard.ts`: `useUnsavedGuard(isDirty: boolean, message?)` — registers TanStack Router `useBlocker({ shouldBlockFn: () => isDirty, withResolver: true })` and a `beforeunload` listener; renders (via a small portal component `UnsavedChangesDialog` using `ConfirmDialog`) "Discard unsaved changes?" with Stay / Discard.
- `FormShell`: accept `form` (the `useAppForm` instance) or `isDirty`; call the guard with `form.state.isDirty && !form.state.isSubmitting && !submittedRef.current`. Set `submittedRef` true in the kit's `onSubmit` wrapper so the post-submit redirect never blocks.
- `yaml-view-dialog.tsx`: guard `changeEditMode(false)` and the close path when `editedYaml !== undefined && editedYaml !== originalYaml` with the same dialog.

**Verify**: `lib/__tests__/use-unsaved-guard.test.tsx`: dirty → navigation blocked and dialog rendered; discard → navigation proceeds; after submit → not blocked. Playwright: extend `tests/e2e/credential-form.spec.ts` (or the closest form spec) with "type into a field, click sidebar link, dialog appears".

### Step 5: Form validation + error summaries (UX-04/05)

- For the 26 validator-less forms, start with the 8 highest-risk: `settings/gitops/new` (name required, `repo_url` must parse as URL with `http(s)`/`ssh`/`oci` scheme), `settings/vault` (address URL), `settings/smtp` (host, port 1–65535, from-address contains `@`), `settings/auth/settings`, `settings/backup`, `clusters/register` (name pattern the API enforces — copy the regex from `internal/handler/clusters_*` create validation or the OpenAPI `pattern`), `settings/webhooks/new` (URL), `settings/quotas/new` (non-negative numbers). Use `form.AppField` + `TextField` so errors render.
- Add `<form.AppForm><form.FormErrorSummary serverError={…} /></form.AppForm>` as the first child of every `FormShell` in the 32 files; make `ErrorSummary` focus itself on mount when it has content (check `components/form/error-summary.tsx`; add `tabIndex={-1}` + `ref.focus()` in an effect).
- Ratchet: `components/ui/__tests__/form-adoption.test.ts` — every file importing `useAppForm` under `src/routes` must contain `FormErrorSummary`.

**Verify**: `npm test -- form-adoption` pass; unit tests for the 8 forms' validators (model on any existing `*.test.tsx` that renders a `useAppForm` form, e.g. `routes/dashboard/settings/backup/index.test.tsx`).

### Step 6: Mutation feedback in widgets (UX-10) + a lint rule

Add `onError: (error) => toastApiError("<Action> failed", error)` and `toastSuccess` to the five mutations. Add an ESLint `no-restricted-syntax` selector in `eslint.config.mjs` for `CallExpression[callee.name='useMutation'] > ObjectExpression:not(:has(Property[key.name='onError']))` with the message "Every mutation must report failure (onError with toastApiError or an inline ErrorMessage)"; allowlist the files where the mutation's `error` is rendered inline (delivery pages) by disabling per line with a reason.

**Verify**: `npm run lint` exit 0; `grep -c onError routes/dashboard/settings/widgets/index.tsx` → 5.

### Step 7: Router-native navigation and URL state (UX-07/11/12/13)

- `apps/index.tsx`: make `section` a validated search param (`validateSearch` on the route with `section?: "installed"|"browse"|"repositories"|"recommended"`), switch via `navigate({ search: (prev) => ({ ...prev, section }), replace: true })`, and make the CTA a plain `RouterLink search={{ section: "browse" }}` with no `onClick`. (022 step 2 migrates the tab bar to `TabStrip` — coordinate in one PR.)
- `resource-detail.tsx:189,228` and `admin/users/$id:153`: navigate to the parent list (`/dashboard/clusters/$id/$resource` with current params; `/dashboard/admin/users`). Use `router.history.canGoBack()` only as an enhancement for the Back arrow when the previous entry is in-app.
- `rollouts/$rolloutId`: seed the delivery project scope from the loaded rollout's project id before rendering the "select a project" empty state; only show that state when the rollout query has resolved and carries no project.
- `lib/use-search-param.ts`: `useSearchParam(key, { debounceMs })` generalising `use-tab-param.ts` (string value, `replace: true`, preserves other params, validated by the route's `validateSearch`). Adopt on the eight lists (`clusters`, cluster `workloads`, `projects`, `alerting/baselines`, `logging` operations filters, `registries`, `agents`, `audit` if not already) with `validateSearch` schemas (`q?: string`, plus the specific filters).
- Replace the three `history.replaceState` sites with `navigate({ search: (prev) => ({ ...prev, ...next }), replace: true })`.

**Verify**: `lib/__tests__/use-search-param.test.tsx` (model on `use-tab-param.test.tsx`); `grep -rn "history.replaceState\|history.back()" frontend/src/routes frontend/src/components` → 0; Playwright: filter clusters, open one, Back → filter persists (add to `tests/e2e/dashboard-smoke.spec.ts`).

### Step 8: Extension full-page route (UX-17) and the small a11y fixes (UX-18)

- Create `routes/dashboard/extensions/$name/index.tsx` rendering the extension's `sidebar`/full-page mount via the existing sandbox component; 404 to the dashboard not-found boundary when the extension is not enabled. Extend `ExtensionNavItems.test.tsx` to assert the href appears in `routeTree.gen.ts`. Update the route-manifest expected count.
- Add `aria-expanded`/`aria-controls` to the sidebar group toggle, the cluster resources sections, and the audit/scan collapsibles; `aria-label={\`Delete ${policy.name}\`}` on the network-policies trash button.

**Verify**: smoke crawl (+1 route) passes with axe; `grep -c "aria-expanded" components/layout/sidebar-navigation-view.tsx` ≥ 1.

### Step 9: Error-state test harness for the busiest routes (UX-19 prerequisite)

- `components/ui/__tests__/query-error-harness.tsx`: a helper that renders a route component with a `QueryClient` whose listed query keys reject, and asserts an element with `role="alert"` or the `StatePanel` danger title is present and that no `EmptyState` title from a provided list is rendered.
- Apply to: dashboard index, clusters index, cluster overview, cluster apps, delivery index, agents, security, alerting, rbac, settings/backup.

**Verify**: `npm test -- error-harness` → 10 passing cases.

### Step 10: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
```

## Test plan

Listed per step. New files: `delivery/index.test.tsx`, `clusters/register/index.test.tsx`, `use-unsaved-guard.test.tsx`, `use-search-param.test.tsx`, `form-adoption.test.ts`, `query-error-harness.tsx` + 10 cases, extension route assertion, two Playwright additions.

## Done criteria

- [ ] Gates exit 0
- [ ] Delivery overview with two rejected queries shows a danger panel and no numeric zero for the affected tiles (test)
- [ ] `grep -rn "history.replaceState\|history.back()" frontend/src/routes frontend/src/components` → 0
- [ ] `grep -rn "querySelector" frontend/src/routes` → 0
- [ ] `grep -c "useState<string | null>(null)" frontend/src/routes/dashboard/clusters/register/index.tsx` → 0 (draft id derives from search)
- [ ] Every `useAppForm` route file contains `FormErrorSummary` (ratchet test)
- [ ] `useBlocker` present in `lib/use-unsaved-guard.ts` and used by `FormShell`
- [ ] `frontend/src/routes/dashboard/extensions/$name/index.tsx` exists
- [ ] README row updated

## STOP conditions

- Excerpts don't match live code.
- TanStack Router's `useBlocker` signature in the installed version (`@tanstack/react-router ^1.170`) differs from `{ shouldBlockFn, withResolver }` — check `node_modules/@tanstack/react-router/dist/esm/useBlocker.d.ts` and adapt; if blocking is unsupported, STOP.
- Adding `validateSearch` to a route makes more than ~10 `navigate`/`Link` call sites fail type-check — STOP and report the count; the schema may need optional keys.
- The rollout detail's data does not include a project id — STOP; report which field would be needed.
- The `useMutation` lint selector produces > 15 allowlist disables — loosen to a warning-level ratchet test instead and note it.

## Maintenance notes

- Rule for reviewers: a count derived from `query.data ?? []` must be inside a `QueryStates` or guarded by `!isError`. The harness in step 9 is the regression net; add every new overview page to it.
- Deferred (not in this plan): E2E for irreversible actions (register→connect, node drain, app uninstall, rollout launch). They need stub support for mutation results in `tests/e2e-smoke/stubs.ts`; scope them after Plan 022 splits the pages.
