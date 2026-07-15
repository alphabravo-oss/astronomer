# Plan 004: Migrate the frontend from Next.js 16 to a Vite + React 19 + TanStack SPA with live-everything SSE

> **Executor instructions**: Read this entire plan before changing code. Execute
> phases in order on a single migration branch; phases are internally ordered so
> the full gate (Section 6) is runnable after every phase. This plan is
> self-contained: it embeds every decision, file path, and command needed, and
> it must not be executed by relitigating the locked decisions in Section 1.
> If a file cited under "Current state" has semantically drifted from its
> description, reconcile before proceeding.
>
> **Repository**: `/root/astronomer-all/astronomer`. Frontend root:
> `/root/astronomer-all/astronomer/frontend` (all relative frontend paths below
> are relative to it; Go paths are relative to the repo root).
>
> **Branch**: create one migration branch (suggested name
> `migrate/vite-tanstack-spa`) off current `main`. Big-bang merge: the branch
> merges to `main` only when the entire merge gate in Section 6 is green.
> Rebase onto `main` at least weekly to bound drift (Section 7, R4).

## Status

- **Status**: REVIEWED (2026-07-15)
- **Date**: 2026-07-15
- **Priority**: P1 platform migration (product-owner locked)
- **Effort**: XL — 8 phases, 42 tasks, frontend + targeted Go backend work
- **Risk**: HIGH aggregate (router + data layer + stores + forms + build swap in one branch), mitigated by phase-internal gates and the adapter-layer strategy
- **Category**: migration, frontend, real-time, build/deploy, tests

---

## 1. Locked decisions (product owner — design within, do not relitigate)

| # | Decision | Reasoning |
|---|---|---|
| L1 | Migrate `frontend/` from Next.js 16 (App Router, 107 pages, zero API routes, zero server actions, zero route handlers) to a Vite + React 19 SPA. | The app is already a pure client app: 116 of 119 route-tree files are `'use client'`, the only server pieces are a 19-line cookie-presence middleware and a redirect stub. Next's server runtime buys nothing and costs a node container, SSR-shaped CI, and framework coupling. |
| L2 | Stack: Vite, React 19, TypeScript, Tailwind, lucide-react, and the full TanStack suite — Router (file-based, auth via `beforeLoad`), Query v5 (kept), Table + Virtual (kept), Form (replaces hand-rolled useState forms), Store (replaces zustand), Pacer (debounce/throttle), DB (SSE-fed reactive collections). | One vendor family with consistent idioms; Query/Table/Virtual are already in place and proven here. |
| L3 | Serving: static `dist/` in its **own** tiny static container (**nginx** — see D-BD1) behind the existing ingress. Ingress routes `/api/*` (plus `/health`, `/readyz`, `/argocd`) to the Go server and everything else to the frontend service. Same-origin, no CORS. **Rejected: `go:embed` of the SPA in the Go binary** — a UI-only deploy would roll control-plane pods and drop every live SSE stream, WebSocket terminal, and agent tunnel; the fleet dashboard is exactly the workload that keeps those open for hours. The deployed topology (Ingress/HTTPRoute → server:8000 for `/api`, frontend:3000 catch-all, `/internal` blackhole) is already SPA-shaped and needs zero routing changes. |
| L4 | Big-bang on one branch, phased internally, merged only when the full gate (type-check, lint, unit tests, Playwright e2e, code-health, plus the new smoke crawl and live tier) is green. | 107 pages cannot ship half-on-Next; the adapter-layer strategy (Section 5, P2.1) keeps intra-branch phases independently verifiable. |
| L5 | SSE scope: "live-everything" — one multiplexed SSE channel feeds TanStack Query invalidation/patching and TanStack DB collections so every list and detail view updates in real time; eliminate polling. Includes the required Go backend work (Section 5, Phase 4). | ~85 `refetchInterval` sites + 3 manual `setInterval` polls exist today (Appendix B); the bus, ticket auth, Redis fan-out, and per-event RBAC already exist server-side. |
| L6 | Philosophy: lazy/minimal — simplest working approach, no speculative abstraction, shortest diff. NEVER simplify away: auth guards, input validation, error handling, accessibility basics, test coverage. | Applied throughout: no zod, no router devtools, no brotli, no Last-Event-ID replay, no pixel-diff gate — but a11y is added to form fields, an open-redirect guard is added to `returnTo`, and the test estate grows (route smoke + live tier). |

---

## 2. Current state (facts from the surveys, 2026-07-15)

Routing and shell:
- **107 `page.tsx` files**, 3 layouts (root, dashboard, `projects/[id]`), 3 error boundaries, 2 not-found boundaries, 0 `loading.tsx`/`template.tsx`/route handlers/server actions.
- The entire router API surface is already wrapped: `src/lib/link.tsx` (72 consumer files), `src/lib/navigation.ts` (~80 import sites), `src/lib/navigation-server.ts` (1 consumer). No page imports `next/*` navigation directly (eslint-enforced).
- 15 pages + 1 layout use Next-16 Promise-shaped `params` with `use()`; 30 pages use the `useParams()` hook; 13 pages use `lib/use-tab-param.ts` (`?tab=` deep links); 6 pages read `useSearchParams` raw.
- `src/middleware.ts` (19 lines) is a presence-only check of the `astronomer_session` cookie on `/dashboard/:path*` with a `returnTo` redirect. Verified in `internal/handler/auth.go:447-495`: `astronomer_session`/`astronomer_refresh` are HttpOnly; `astronomer_csrf` is JS-readable and set/cleared in lockstep with the session. Verified in `src/app/auth/login/page.tsx`: the login page **never consumes `returnTo` today** (success handlers do `router.push('/dashboard')` unconditionally).
- Other next-isms: `next/image` in 1 file (catalog, 2 sites), `next/font/google` in 1 file, `next/dynamic` in 2 files (both lazy monaco), 8 `process.env.NEXT_PUBLIC_*` sites + 1 `NODE_ENV` site.

Data layer and live surface:
- One centralized axios instance (`src/lib/api.ts`, 2,776 lines) with load-bearing interceptors: snake→camel camelization (skipped for `/k8s/` paths), trailing-slash rewrite, CSRF header from `astronomer_csrf`, 401 single-flight refresh. 465 `useQuery` / 233 `useMutation` calls; query keys single-sourced in `src/lib/query-keys.ts` and lint/gate enforced.
- **~85 `refetchInterval` polling queries + 3 manual `setInterval` polls** (Appendix B). SSE exists already: singleton `src/lib/live-events.ts` (602 lines) on `GET /api/v1/events/stream/?ticket=`, ticket-authed, Redis fan-out, SEC-R07 per-event cluster RBAC, 13 event types, named-event framing with the documented `KNOWN_EVENT_TYPES` silent-drop footgun. No Last-Event-ID/replay anywhere. Agent informers cover 10 kinds; CRDs (Velero, Argo, Trivy, Gatekeeper) and ~15 core kinds are uncovered.
- `npm run code-health` is **red on main today**: `src/hooks/use-resource-watch.ts:116` (direct fetch) and `src/app/dashboard/argocd/[instanceId]/page.tsx:311` (inline query key).

State and forms:
- Exactly 3 zustand stores in 2 files (`lib/store.ts`: auth + UI; `lib/window-manager-store.ts`), all `persist`-wrapped with the zustand envelope `{"state":{...},"version":N}` in keys `astronomer-auth` (v2 + migrate), `astronomer-ui`, `astronomer-window-manager`. 3 jest files drive stores imperatively; all 8 Playwright specs seed the `astronomer-auth` envelope.
- No form library anywhere; ~75–85 hand-rolled useState forms (~25 S / ~35 M / ~15 L). Two secret round-trip wire variants (marker `__<name>_set` and smtp `__redacted__` sentinel) must survive.

Build, deploy, tests:
- Dockerfile: node:20-alpine 3-stage Next standalone; guarded by `deploy/docker_base_image_test.go` (digest pins mandatory). Chart: frontend on port 3000, uid 1001, `readOnlyRootFilesystem: true`, tcpSocket probes; ingress/HTTPRoute already route `/api|/health|/readyz|/argocd`→server:8000, `/internal`→blackhole, `/`→frontend:3000. The `NEXT_PUBLIC_*` runtime env in chart/compose is verified dead (never reached the client bundle).
- Tests: 55 jest files / ~408 cases (ts-jest, jsdom, **not** next/jest); 8 fully-mocked Playwright specs (2 projects) with cookie+localStorage auth seeding duplicated per spec; `pr-validation.yaml` frontend job = code-health, lint, type-check, `npm test -- --runInBand`, e2e, `npm audit --audit-level=moderate`; Trivy HIGH/CRITICAL on the image; `make verify` needs `js-yaml` to stay a frontend dep (createRequire).

---

## 3. Target architecture

```
            ┌──────────────────────────────────────────────────────────────────┐
 Browser ───►  Ingress / Gateway (HTTPRoute preferred, legacy Ingress path)     │
            │                                                                  │
            │   /api/*  /health  /readyz  /argocd ──────►  astronomer-server:8000 (Go)
            │                                              ├── REST /api/v1/* (axios, camelize, CSRF)
            │                                              ├── SSE  /api/v1/events/stream/?ticket=…
            │                                              │       (single multiplexed live channel)
            │                                              ├── WS   /api/v1/ws/* (logs/exec/shell)
            │                                              └── /argocd/* (co-hosted ArgoCD SPA)
            │   /internal ──────────────────────────►  blackhole Service (security H4, unchanged)
            │                                                                  │
            │   /* (catch-all) ───────────────────────►  astronomer-frontend:3000
            │                                              nginx (unprivileged, uid 1001, RO rootfs)
            │                                              ├── dist/ static assets (immutable /assets)
            │                                              ├── try_files $uri /index.html  (SPA fallback)
            │                                              └── /healthz
            └──────────────────────────────────────────────────────────────────┘

 SSE and WS never touch the frontend pod. The frontend pod makes zero outbound calls.
 In dev: `vite dev` on :3000 proxies /api (ws:true) to the local Go server on :8000.
```

---

## 4. Designer open questions — resolved

Every open question raised during design is resolved here. Executors implement these decisions; two items are additionally flagged for post-merge review (D14, D15) but do not block.

| ID | Question | Decision + reasoning |
|---|---|---|
| D1 | Do `@wterm/*` packages prebundle cleanly under Vite dev, and does the WASM terminal work under `vite dev` and `vite preview`? | Scheduled as the **P1.7 canary**, run before any mass port. `wasmUrl` prop loading (no `.wasm` imports) means esbuild prebundling should work; the documented escape hatch is `optimizeDeps: { exclude: ['@wterm/core','@wterm/dom','@wterm/react'] }`. If neither works, STOP and report — do not port pages against a broken terminal. |
| D2 | Do any Link consumers pass Next `UrlObject` hrefs? | **Verified now: zero.** `grep -rn 'href={{' src/` returns nothing. String-only `hrefToLocation` adapter is safe; the P2.6 grep gate re-asserts it. |
| D3 | Does the current login page validate `returnTo`? | **Verified now: the login page never reads `returnTo` at all** (`router.push('/dashboard')` on success; `returnTo` is only ever *written* by middleware). Decision: P2.4 implements consumption with the open-redirect guard (`returnTo.startsWith('/') && !returnTo.startsWith('//')`), restoring the deep-link intent. This cannot break the SSO flow — SSO uses `window.location.href` to the provider and a backend callback redirect, and nothing consumes `returnTo` today. The tier-2 live spec `guard-returnto.live.spec.ts` locks the new behavior. |
| D4 | Is TanStack Router static > dynamic > splat ranking safe for `clusters/$id/$resource` vs 19 static siblings? | Asserted by docs but **must be proven before the mass port**: P2.2 writes the memory-history ranking test against the clusters/$id subtree first, then the rest of the tree is ported. If ranking fails, restructure with explicit route ordering before proceeding. |
| D5 | Commit `routeTree.gen.ts` or generate in CI? | **Commit it** (TanStack default guidance) so `tsc --noEmit` and Vitest run without Vite. Drift check: P2.6 adds a CI step `npm run build && git diff --exit-code src/routeTree.gen.ts` in the frontend job. Never hand-edit; regenerate on conflict. |
| D6 | Pre-1.0 pins for `@tanstack/react-db` / `@tanstack/react-pacer`? | Pin **exact versions** (no caret) at P0.4, run `npm audit --audit-level=moderate` on the resolved tree before Phase 1 lands, and again when P4.7/P4.8 add them if deferred. Any advisory blocks adoption of that package until resolved. |
| D7 | Server drop-rate threshold that triggers per-type coalescing after the 64→256 buffer bump? | **0.1% of published events dropped (RecordDroppedEvent / published) sustained over a 24h fleet soak.** Coalescing is a named post-merge follow-up, not in this branch (Section 9). The metric must be confirmed alertable in P4.5. |
| D8 | ArgoCD live-app freshness: informers + reconcile-pass events enough, or permanent poll for some tabs? | Application/ApplicationSet CRD informers (P4.6) + `argocd.changed` published at the end of each server reconcile pass cover instances/apps/appsets/repos/projects/operations. The three Argo-side-truth tabs with no event source at content granularity — **manifests, history, orphan report — keep their current plain `refetchInterval`** (30s/60s/60s, not converted to `liveFallback`). Everything else argocd converts. |
| D9 | Do restricted users need `admin_queue`/`siem_forwarder` liveness (their unscoped events are fail-closed dropped by SEC-R07)? | No change needed. Verified: `settings/operations` and `settings/siem` REST endpoints are admin-scoped server-side; a restricted user gets 403s on data fetch, so event liveness is moot, and anyone who can read the data still has `liveFallback` polling when the stream is down. The fail-closed drop stands (it is correct for superuser-only domains). |
| D10 | Is a 5-minute RBAC binding re-snapshot acceptable for hours-long streams? | **Yes — 5 minutes** (P4.5/T5). Today the staleness window is the entire stream lifetime; 5 minutes is a strict improvement and matches the platform's session-revocation posture. Flagged for security review post-merge (D15) but not blocking. |
| D11 | Toaster is hardcoded `theme="dark"` while the app supports light/system — fix or keep? | **Keep hardcoded `theme="dark"`** (pre-existing inconsistency, zero-churn parity). This follows from D12: next-themes is replaced by a native provider with no `resolvedTheme` (verified: zero `resolvedTheme` consumers exist). Note it in the migration PR description so nobody files it as a regression. |
| D12 | next-themes: keep (state-forms design) or replace with a native provider (routing-shell design)? | The two designs conflicted; **resolved in favor of replacement**: `src/lib/theme.tsx` (~35 lines, P1.6) keeping the load-bearing `astronomer-theme` localStorage key, the raw `"light"|"dark"|"system"` stored values (existing prefs survive), class strategy, system tracking, and an index.html no-flash script. Rationale: the app uses a sliver of next-themes (topbar cycle + one re-render trick), the used surface fits in 35 dependency-free lines, and the routing workstream verified there is no `resolvedTheme` consumer. Consequence: `pod-terminal.test.tsx`'s next-themes mock retargets to `@/lib/theme` (one line). |
| D13 | Does the SSE workstream keep `hooks/use-resource-watch.ts` (Pacer item 4)? | **Replaced.** The proxy-watch fetch moves to `src/lib/api/k8s-watch.ts` in P0.1 (fixing the code-health red); the hook itself is deleted in P4.7 when TanStack DB collections land. Pacer item 4 is therefore dropped; Pacer adoption is the 3 search-input sites + data-table filters (P4.8) plus the central paced invalidator (P4.4). |
| D14 | Does TanStack Form v1 `getFieldMeta().isDirty` stay true across `form.reset(initial)` after refetch? | **Verify empirically in P5.1** with a dedicated kit unit test (`form/secrets.test.tsx`: dirty a secret field, `reset(newInitial)`, assert omission behavior). Documented fallback if isDirty resets: `SecretField` keeps an explicit internal touched map (exactly today's `touchedSecrets` pattern) behind the same `stripUntouchedSecrets` API — callers unchanged either way. |
| D15 | F5 pages left unconverted at gate time: debt tickets or permanent useState? | Forms **below** the convert policy (S forms, ConfirmDialog, ExtForm, helm-values-form, list editors) stay useState permanently by design. M/L forms in F5 not converted by gate time merge as-is and are filed as follow-up tickets listed in Section 9. |
| D16 | Trivy-verify base digests before first CI push? | Yes — P6.1 pins the current `node:22-alpine` digest and reuses the repo's pinned `nginx:1.27-alpine@sha256:65645c…`; if 1.27 no longer scans clean at HIGH/CRITICAL, bump **both** `frontend/Dockerfile` and `deploy/nginx/Dockerfile.nginx` to the current `nginx:1.29-alpine` digest in one commit. Local `trivy image` run is a P6.1 step. |
| D17 | Does `networkpolicy_render_test.go` assert the frontend egress rule? | **Verified now: no.** It asserts each component policy exists with `Ingress`+`Egress` policyTypes and the component selector label, and asserts zero egress only for postgres/redis. The P6.2 netpol trim (frontend→server egress + server ingress-from-frontend) is safe against the existing assertions; still re-run `go test ./deploy/...` and keep the trim droppable if anything else bites. |
| D18 | jest vs vitest, and who owns the `npm test -- --runInBand` CI line? | **Vitest 4** (P1.5). `import.meta.env` in src makes keep-jest untenable under ts-jest CJS. The pr-validation frontend job's test line changes to `npm test` (script becomes `vitest run`) in the same commit. The build workstream's `__APP_VERSION__` define-global with `typeof` guard is runner-agnostic. |
| D19 | Who lands `src/main.tsx` + theme bootstrap in `index.html`? | Phase 1 owns both: P1.1 ships `index.html` plus **stub** `src/main.tsx`/`src/routes/__root.tsx`/`src/routes/index.tsx` (so `vite build` and the router plugin work from the first task), P1.7 ships `src/router.tsx` and fleshes out the stubs. |
| D20 | Routes directory + ignore pattern (code-health and manifest generator depend on it)? | **`src/routes/`**, `routeFileIgnorePattern: '\\.test\\.(ts|tsx)$'`, co-located non-route modules get the `-` prefix (5 files). P2.5 repoints code-health; P7.1's manifest generator scans `src/routes/**`. |
| D21 | Final localStorage persist key/shape after the store swap? | **Byte-compatible with today**: keys `astronomer-auth` (v2 envelope + migrate), `astronomer-ui`, `astronomer-window-manager`, zustand envelope `{"state":…,"version":N}` reproduced by `persistedStore` (P3.1). `tests/e2e/helpers/auth.ts` therefore never changes shape. |
| D22 | Live e2e job: `go run ./cmd/server` or built image? | **`go run ./cmd/server`** + GH service containers (postgres:16-alpine, redis:7-alpine) for speed. The built container is exercised by the P6.3 k3d smoke instead. |
| D23 | Module name/API of the SSE invalidation map (exhaustiveness test target)? | **`src/lib/live/routes.ts`** exporting `EVENT_ROUTES: Record<string,(d)=>QueryKey[]>` and `K8S_KIND_ROUTES: Record<string,(cid,d)=>QueryKey[]>`. The exhaustiveness test is `src/lib/live/routes.test.ts` (P4.4/P4.5). |
| D24 | Is `vite preview`'s history fallback close enough to production nginx (incl. `/argocd` exclusion)? | Yes for the mocked/smoke tiers: `/argocd/*` and `/api/*` never reach the frontend container in production (ingress routes them first), so the only fallback semantic that matters — unknown path → index.html — is identical in preview. The real container + `/argocd` co-hosting is verified in the P6.3 k3d smoke and the manual QA checklist. Route-smoke does not need to run against the container. |
| D25 | Scroll behavior parity: Next App Router scrolls to top on every push navigation; what replaces it? | `createRouter({ scrollRestoration: true })` (P1.7) — resets to top on new history entries and restores position on back/forward. `resetScroll: false` stays confined to the `useTabParam` replace path (P2.1) so `?tab=` switches don't jump. The P7.3 manual QA checklist verifies bottom-of-long-list → detail navigation lands at top. |

**Decisions still needed: none blocking.** D10 (RBAC re-snapshot interval) and D7 (drop-rate threshold) are flagged for post-merge security/scale review.

---

## 5. Phases

Phase dependency graph: P0 → P1 → P2 → {P3, P4, P5 in any order or parallel; P4's Go tasks can start any time after P0} → P6 → P7. Each phase ends with the currently-applicable slice of the merge gate green.

---

### Phase 0 — Baseline green and preflight facts

**Goal**: a green gate on the *pre-migration* layout plus the small extractions every later phase depends on, so migration diffs are never entangled with pre-existing red.

**Reasoning**: repo norm is "fix what you find"; the code-health gate is red on main today, and the e2e auth seed is duplicated across all 8 specs — both would otherwise be edited mid-migration under pressure.

#### P0.1 — Fix the two pre-existing code-health reds
Steps:
1. Extract `camelizeKeys` from `src/lib/api.ts` into new `src/lib/camelize.ts`; re-import it in `api.ts` (behavior identical). This is also the seed for the SSE envelope camelization in P4.3.
2. Create `src/lib/api/k8s-watch.ts` and move the raw streaming `fetch` (NDJSON proxy watch) from `src/hooks/use-resource-watch.ts:116` into it as `openProxyWatch(...)`; `use-resource-watch.ts` imports it. `lib/api/` is inside the fetch-containment allowlist, so the gate goes green.
3. Move the inline `['argocd','operations','recent']` key at `src/app/dashboard/argocd/[instanceId]/page.tsx:311` into `src/lib/query-keys.ts` (e.g. `queryKeys.argocd.recentOperations`).
Definition of done: `npm run code-health` green; no behavior change.
Tests: existing `use-resource-watch.test.tsx` and api tests pass unchanged.
Validation: `cd frontend && npm run code-health && npm test && npm run type-check`.

#### P0.2 — Consolidate the e2e auth seed
Steps: create `tests/e2e/helpers/auth.ts` exporting `seedAuth(context, page, user)` that performs the `addCookies(astronomer_session, astronomer_csrf)` + `addInitScript(localStorage['astronomer-auth'] = {state:{user,isAuthenticated:true},version:2})` block; replace the duplicated blocks in all 8 specs.
Definition of done: 8 specs green, one seed implementation.
Validation: `npm run test:e2e`.

#### P0.3 — Lock window-manager behavior with unit tests
Steps: add `src/lib/__tests__/window-manager-store.test.ts` covering `tabIdFor` dedupe, LRU eviction at 10 tabs, next-tab-on-close selection (prefer right, fall back left), and `clampHeight`. This is the only store with algorithms and it currently has zero direct tests — it must be locked **before** the P3 swap.
Definition of done: tests pass against the current zustand implementation.
Validation: `npm test -- window-manager-store`.

#### P0.4 — Version pinning and audit preflight
Steps:
1. Resolve and record exact versions for: `vite@^7`, `@vitejs/plugin-react@^5`, `@tanstack/react-router@^1`, `@tanstack/router-plugin@^1`, `@tanstack/react-form@^1`, `@tanstack/store`/`@tanstack/react-store@^0.7`, and **exact pins (no caret)** for `@tanstack/db`, `@tanstack/react-db`, `@tanstack/react-pacer`; plus `@fontsource-variable/inter@^5`, `@fontsource-variable/jetbrains-mono@^5`, `vitest@^4`, `jsdom@^25`, `vite-tsconfig-paths@^5`.
2. In a scratch dir, `npm install` the candidate set and run `npm audit --audit-level=moderate`; any advisory on a pre-1.0 TanStack package blocks its adopting phase (D6).
3. Record the current `node:22-alpine` digest and confirm the repo's `nginx:1.27-alpine@sha256:65645c…` digest still Trivy-scans clean at HIGH/CRITICAL (D16).
Definition of done: the pinned dependency list committed to the branch as `frontend/docs/migration-pins.md` (one line per package: name, exact resolved version, caret-or-exact policy, audit result) — this file is the canonical source P1.1/P4.7/P4.8 copy versions from, and P1.1's package.json+lockfile commit must match it; audit clean or exceptions recorded in the same file.
Validation: `npm audit --audit-level=moderate` in the scratch install; `test -f frontend/docs/migration-pins.md`.

---

### Phase 1 — Toolchain: Vite scaffold, env shim, Vitest, theme, canary

**Goal**: the app builds and runs under Vite with a minimal 2-route tree, unit tests run under Vitest, and the two bundler-sensitive dependencies (wterm WASM, monaco) are proven before any mass port.

**Reasoning**: everything after this phase assumes `import.meta`, the Vite transform, and the route plugin; the canary (P1.7) is deliberately before Phase 2 so a wterm failure stops the migration cheaply.

**Phase-1 gate rule (locked)**: `next` and `next-themes` STAY INSTALLED (unused by the new entry chain) until P2.6 — 11 files (`src/lib/link.tsx`, `src/lib/navigation.ts`, `src/lib/navigation-server.ts`, `src/middleware.ts`, `src/app/layout.tsx`, providers, topbar, pod-terminal, yaml-editor, catalog, template) import `next/*` until Phase 2 ports them, and their consumers span the whole tree. With the packages kept, `npm run type-check`, `npm run lint`, and `npm test` are expected FULL-TREE GREEN after every Phase-1 task; only `eslint-config-next` is removed in Phase 1 (P1.4 rebuilds the flat config without referencing it). The `no-restricted-imports` ban on `next/*` is NOT enabled in P1.4 — it lands in P2.6 together with the package removal, after the last `next/*` import is gone. If any Phase-1 validation is red, that is a real defect in that task, not an expected intermediate state.

#### P1.1 — Dependency swap, vite.config.ts, index.html, tsconfig
Steps:
1. package.json: add the P0.4 set; remove `eslint-config-next` only — **keep `next` and `next-themes` installed until P2.6** (Phase-1 gate rule above: 11 files still import them until Phase 2 ports land; removing them here reds out type-check/lint/tests tree-wide); keep `js-yaml` (make verify/api-contract createRequire — do not remove). Scripts: `"dev": "vite"`, `"build": "vite build"`, `"preview": "vite preview"`; keep `predev`/`prebuild` wterm.wasm copy verbatim; delete `"start"`.
2. New `vite.config.ts`:
   ```ts
   import { defineConfig } from 'vite';
   import react from '@vitejs/plugin-react';
   import { tanstackRouter } from '@tanstack/router-plugin/vite';
   import tsconfigPaths from 'vite-tsconfig-paths';
   export default defineConfig({
     plugins: [
       tanstackRouter({
         target: 'react',
         routesDirectory: './src/routes',
         generatedRouteTree: './src/routeTree.gen.ts',
         routeFileIgnorePattern: '\\.test\\.(ts|tsx)$',
         autoCodeSplitting: true,
       }),                                   // MUST precede react()
       react(),
       tsconfigPaths(),
     ],
     define: { __APP_VERSION__: JSON.stringify(process.env.VERSION ?? '0.2.0-dev') },
     server: {
       port: Number(process.env.PORT) || 3000,
       proxy: { '/api': { target: process.env.BACKEND_URL ?? 'http://localhost:8000', ws: true } },
     },
     preview: {
       proxy: { '/api': { target: process.env.BACKEND_URL ?? 'http://localhost:8000', ws: true } },
     },
     build: { outDir: 'dist', chunkSizeWarningLimit: 1500 },
   });
   ```
   (`ws: true` covers `/api/v1/ws/*` terminals; Vite's http-proxy streams SSE unbuffered. The `preview.proxy` block exists for the P7.2 live tier.)
3. New `index.html` at frontend root: viewport meta, `<title>Astronomer - Kubernetes Multi-Cluster Management</title>`, `<link rel="icon" href="/icon.svg">`, the P1.6 no-flash script, `<div id="root">`, `<script type="module" src="/src/main.tsx">`. Move `src/app/icon.svg` → `public/icon.svg`.
4. Delete `next.config.js`, `next-env.d.ts`. tsconfig.json: drop `plugins:[{name:'next'}]` and `next-env.d.ts`/`.next/types/**` includes; add `"types": ["vite/client", "vitest/globals"]`; bump `target` to ES2020; keep `paths: {"@/*": ["./src/*"]}`; include `src/routeTree.gen.ts`.
5. **Minimal build scaffold (so `vite build` and the router plugin work from this task onward — fleshed out in P1.7)**: stub `src/main.tsx` (render `<div/>` into `#root`), stub `src/routes/__root.tsx` (bare `<Outlet/>`), stub `src/routes/index.tsx` (empty component); commit the first generated `src/routeTree.gen.ts`. Without these, the index.html → `/src/main.tsx` entry chain is unresolved and P1.3's `npm run build` validation cannot pass, and P1.5's vitest config (which merges vite.config and its `routesDirectory: './src/routes'`) points at a nonexistent directory.
Definition of done: `npm ci` succeeds on the new tree; `npm audit --audit-level=moderate` clean; `npm run build` succeeds on the stub scaffold.
Validation: `npm ci && npm audit --audit-level=moderate && npm run build`.

#### P1.2 — Env shim (kill NEXT_PUBLIC_*)
Steps:
1. New `src/lib/env.ts`:
   ```ts
   declare const __APP_VERSION__: string | undefined; // vite define; absent under vitest config without define — typeof-guarded
   export const APP_VERSION = typeof __APP_VERSION__ === 'undefined' ? '0.2.0-dev' : __APP_VERSION__;
   export const API_BASE = '/api/v1';
   export function wsBase(): string {
     const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
     return `${proto}//${window.location.host}/api/v1/ws`;
   }
   export const IS_DEV = import.meta.env.DEV;
   ```
2. Replace the 9 sites: `src/lib/api.ts:9,704`, `src/lib/api/account-security.ts:17`, `src/lib/live-events.ts:149`, `src/hooks/use-resource-watch.ts:34`, `src/components/clusters/cluster-shell.tsx:136`, `src/components/workloads/pod-terminal.tsx:83`, `src/components/layout/sidebar.tsx:84` (×2 → `APP_VERSION`), providers `NODE_ENV` devtools gate → `IS_DEV`.
3. No `.env` files, no `VITE_*` vars: same-origin relative defaults are the design; there is nothing to configure at runtime.
Definition of done: `grep -rn 'NEXT_PUBLIC' src/` returns zero; `grep -rn "process.env" src/` returns zero (or only type-decl files).
Validation: the greps above + `npm run type-check`.

#### P1.3 — Tailwind 3.4 stays; content globs; fonts
Steps:
1. **Decision (locked here): ship on Tailwind 3.4 + PostCSS; v4 is a separate post-merge PR.** v4 changes visual defaults across 107 pages with only 8 e2e flows and no pixel gate as a net; nothing about v4 gets cheaper in-branch.
2. `tailwind.config.ts` content: `['./index.html', './src/**/*.{ts,tsx}']` (drop dead `./src/pages/**`, `./src/app/**`).
3. Fonts: import `@fontsource-variable/inter` and `@fontsource-variable/jetbrains-mono` once in `src/main.tsx`; add to `src/styles/globals.css`: `:root { --font-inter: 'Inter Variable', system-ui, sans-serif; --font-mono: 'JetBrains Mono Variable', ui-monospace, monospace; }`. `tailwind.config.ts` already consumes the vars — unchanged.
Definition of done: styles render identically on the canary routes; no Google-fetch (air-gap posture kept).
Validation: visual check on P1.7 canary; `npm run build` succeeds.

#### P1.4 — ESLint flat config rebuild
Steps: replace `eslint-config-next/core-web-vitals` in `eslint.config.mjs` with `@eslint/js` recommended + `typescript-eslint@^8` recommended + `eslint-plugin-react-hooks@^5` (flat recommended) + `eslint-plugin-jsx-a11y@^6` (flat recommended — keeps the a11y coverage eslint-config-next provided, per L6). Delete the React-Compiler-family `'off'` entries (unknown rules under vanilla react-hooks). PRESERVE verbatim: `@tanstack/eslint-plugin-query` flat/recommended, `exhaustive-deps` + `no-rest-destructuring` as errors, the `no-restricted-syntax` inline-queryKey ban (factory exempt). RETARGET `no-restricted-imports`: ban `@tanstack/react-router` imports outside `src/lib/navigation.ts`, `src/lib/link.tsx`, `src/routes/**`, `src/router.tsx` — this rule is what keeps the adapter layer (and every test mock) intact permanently. The `next/*` import ban is **deferred to P2.6** (Phase-1 gate rule: 11 files legitimately import `next/*` until Phase 2 ports them; enabling the ban here would red out lint).
Definition of done: `npm run lint` green (script name unchanged).
Validation: `npm run lint`.

#### P1.5 — Jest → Vitest 4 port
Steps:
1. Remove `jest`, `jest-environment-jsdom`, `ts-jest`, `@types/jest`; add `vitest@^4`, `jsdom@^25`. Skip coverage tooling (no coverage gate exists today).
2. New `vitest.config.ts` merging the app vite config (inherits alias, react plugin, router plugin):
   ```ts
   import { defineConfig, mergeConfig } from 'vitest/config';
   import viteConfig from './vite.config';
   export default mergeConfig(viteConfig, defineConfig({
     test: { environment: 'jsdom', globals: true, setupFiles: ['./vitest.setup.ts'],
       include: ['src/**/*.test.{ts,tsx}'], exclude: ['tests/e2e/**', 'node_modules/**'] },
   }));
   ```
   `vitest.setup.ts`: `import '@testing-library/jest-dom/vitest';`. Delete `jest.config.cjs`, `jest.setup.ts`.
3. Codemod across 55 test files: `jest.fn→vi.fn`, `jest.mock→vi.mock`, `jest.clearAllMocks→vi.clearAllMocks`, `jest.spyOn→vi.spyOn` (~168 sites); type imports `jest.Mock/Mocked/MockedFunction` → `Mock/Mocked/MockedFunction` from `vitest` (~25 sites); add `import { vi } from 'vitest'` where needed.
4. Hand fixes: `vi.hoisted` in `src/components/backups/restore-modal.test.tsx` and the gatekeeper page test (budget +0.5 day for a few more found at first run); delete the `jest.mock('@wterm/react/css', …, {virtual:true})` line in `pod-terminal.test.tsx` (Vite resolves the subpath; fallback is a `test.alias` to an empty module).
5. package.json: `"test": "vitest run"`, `"test:watch": "vitest"`. Edit `.github/workflows/pr-validation.yaml`: `npm test -- --runInBand` → `npm test` (vitest parallel; if CI memory pressure appears use `vitest run --maxWorkers=2`).
Definition of done: `npx vitest run` green with the same ~408-case count; compare collected file count to 55 (± renames) to prove nothing is silently skipped.
Validation: `npm test && npm run type-check`.

#### P1.6 — Theme provider replacement (D12)
Steps:
1. New `src/lib/theme.tsx` (~35 lines): `ThemeProvider` + `useTheme` with localStorage key **`astronomer-theme`** (NEVER `theme` — the co-hosted ArgoCD SPA JSON-parses the bare `theme` key and crashes), values `'light'|'dark'|'system'` stored raw (existing prefs survive), `class` strategy on `<html>` + `colorScheme` style, `matchMedia('(prefers-color-scheme: dark)')` tracking, default `dark`. No `resolvedTheme` (verified: zero consumers).
2. index.html no-flash inline script (before the module script): read `astronomer-theme`, toggle `dark` class, default dark, try/catch to dark.
3. Swap the provider in providers (P1.7 moves the file); retarget the `next-themes` mock in `src/components/workloads/pod-terminal.test.tsx` to `@/lib/theme`. Topbar cycle keeps `{theme, setTheme}` API. Toaster stays `theme="dark"` (D11). Drop `disableTransitionOnChange` (add the 6-line class-freeze later only if QA shows flicker).
Definition of done: theme cycles on canary; hard reload shows no flash; localStorage key unchanged.
Tests: pod-terminal test green with retargeted mock; add a small `theme.test.tsx` (init from stored value, system tracking, key name literal).
Validation: `npm test`; manual reload check in P1.7.

#### P1.7 — Router scaffold + wterm/monaco canary (D1)
Steps:
1. New `src/router.tsx` (`createRouter({ routeTree, defaultPreload: false, scrollRestoration: true })` + `Register` module augmentation) and flesh out the P1.1 stub `src/main.tsx` (fontsource imports, ~15-line plain ErrorBoundary wrapping `<RouterProvider>`, `vite:preloadError` → `window.location.reload()` one-liner for post-deploy stale-chunk insurance). `scrollRestoration: true` is deliberate (D25): it resets scroll to top on new history entries — matching Next App Router's push-navigation behavior — and restores position on back/forward; `false` would preserve the current offset across pushes, landing detail pages mid-scroll after long-list navigation. The `useTabParam` replace path keeps `resetScroll: false` (P2.1) so tab switches still don't jump.
2. Move `src/app/(components)/providers.tsx` → `src/components/providers.tsx` (content unchanged except ThemeProvider swap and `IS_DEV` devtools gate). Flesh out the P1.1 stub `src/routes/__root.tsx` to mount `<Providers><Outlet/></Providers>`; `notFoundComponent` = port of `app/not-found.tsx`.
3. `src/routes/index.tsx`: `beforeLoad: () => { throw redirect({ to: '/dashboard' }) }` (replaces `app/page.tsx` + `navigation-server.ts` consumer).
4. Port `src/routes/auth/login/index.tsx` only (login page body moved as-is for now; TanStack Form conversion comes in P5.4).
5. **Canary**: under `vite dev` AND `vite build && vite preview`: login renders; a scratch route mounting `cluster-shell.tsx`/`pod-terminal.tsx` opens the wterm terminal (WASM instantiates via `/wterm.wasm`); the monaco `yaml-editor` lazy-loads (convert its `next/dynamic` → `React.lazy` + existing placeholder as Suspense fallback now; same for `clusters/[id]/template` when it ports in P2.3). If wterm prebundle fails, apply `optimizeDeps.exclude` and re-verify; if still broken, STOP.
Definition of done: canary passes in dev and preview; committed `src/routeTree.gen.ts`.
Validation: manual canary + `npm run build && npm run preview`.

---

### Phase 2 — Routing: wrappers, guard, mass port, boundaries, cleanup

**Goal**: all 107 pages served by TanStack Router with the auth guard, error/not-found boundaries, typed search params where they are deep-link contracts, and zero `next/*` remaining. The ~152 consumer files of the wrapper modules never change.

**Reasoning**: rewriting the 3 wrapper files in place is the single biggest de-risking move — 72 Link consumers, ~80 navigation-hook sites, 37 jest mock sites, and 3 mock-heavy test files survive untouched.

#### P2.1 — Wrapper rewrite + auth guard + dashboard layout
Steps:
1. Rewrite `src/lib/link.tsx`: export `hrefToLocation(href)` (split `#` then `?`, build `{to, search, hash}` — TanStack `to` does not parse query strings, and audit/search pages build hrefs with them) and a `forwardRef` `Link` accepting `href: string` that renders `RouterLink` with the parsed location. String-only is safe (D2).
2. Rewrite `src/lib/navigation.ts` with identical export names/shapes: `useRouter()` returning `{push, replace(href,{scroll}), back}` (replace uses `replace: true, resetScroll: false` to preserve `useTabParam`'s no-jump contract), `usePathname()` via `useLocation({select})`, `useSearchParams()` returning a real `URLSearchParams` from `location.searchStr`, `useParams()` via `useRouterParams({strict:false})`.
3. Delete `src/lib/navigation-server.ts` and `src/middleware.ts`.
4. Auth guard: add `CSRF_COOKIE = 'astronomer_csrf'` and `hasSessionHint()` (3 lines: `document.cookie` contains `astronomer_csrf`) to `src/lib/auth/session.ts`. New `src/routes/dashboard/route.tsx`:
   ```ts
   beforeLoad: ({ location }) => {
     if (!hasSessionHint()) throw redirect({ to: '/auth/login', search: { returnTo: location.href } });
   }
   ```
   Rationale (locked): the CSRF cookie is set/cleared in lockstep with the HttpOnly session by `setBrowserSessionCookies`/`clearBrowserSessionCookies` (`internal/handler/auth.go:447-495`), giving exact presence-check fidelity to the old middleware, synchronously, with zero network. A `/auth/me` probe in `beforeLoad` is rejected: it adds latency to every first navigation and duplicates what `useCurrentUser` + the axios 401 refresh already enforce. e2e specs already set both cookies, so they pass unchanged.
5. Port `app/dashboard/layout.tsx` into the same `route.tsx` component: Sidebar, Topbar, CommandPalette, **WindowManager mounted above `<Outlet/>`** (logs/exec WebSockets must survive navigation), ExtensionProvider, offline banner, `useLiveEvents()` + `useLiveClusterMetricsMerger()`, and — **explicitly kept in the component, NOT the guard** — the `must_change_password` kick-out and feature-flag path gating (both depend on async query data; in `beforeLoad` they would block every navigation).
6. Retain the eslint ban from P1.4 so nothing else imports the router directly.
Definition of done: dashboard shell renders behind the guard; unauthenticated `goto /dashboard/...` bounces to `/auth/login?returnTo=…`.
Tests: keep the 3 `@/lib/navigation` mock-based test files green unchanged; audit component tests rendering `Link` outside a router — mock `@/lib/link` per file if needed; add a `renderWithRouter` util in `src/test-utils.tsx` only if more than ~10 files need it (rule of ten, not speculative).
Validation: `npm test`; manual guard check.

#### P2.2 — Route-ranking proof, then the clusters/$id subtree (D4)
Steps:
1. **First** write `src/routes/__tests__/route-ranking.test.ts`: build the router with `createMemoryHistory`, table-driven (~30 rows) asserting resolved route ids for: `/dashboard/clusters/c1/adoption` (and each of the ~19 static siblings) → static; `/dashboard/clusters/c1/deployments` → `$resource`; `/dashboard/clusters/c1/deployments/ns/foo` → `$resource/$`; `/dashboard/clusters/c1/custom-resources` and `.../custom-resources/g/v1/things` → the paired optional-catch-all files; `/dashboard/clusters/c1/control-plane-snapshots` → its own route; the two register routes; plus one row asserting `hrefToLocation` handles a query-string href.
2. Port the `clusters/$id` subtree: `route.tsx` (NEW thin layout: `<Outlet/>` + `errorComponent` port of `clusters/[id]/error.tsx`; keep the `error.digest` read behind an `in` check), all static siblings, `$resource/index.tsx` + `$resource/$.tsx` (keep the in-component `$resource` allowlist → not-found behavior; do not move it to `beforeLoad`), the `[[...slug]]` split (`custom-resources/index.tsx` + `custom-resources/$.tsx` sharing `src/components/clusters/custom-resources-page.tsx` taking `slug: string[]`), and the snapshots alias (`snapshots/index.tsx` + `control-plane-snapshots/index.tsx` both importing `src/components/clusters/snapshots-page.tsx` — route files must not import each other under autoCodeSplitting).
Definition of done: ranking test green against the real subtree BEFORE step 3 of P2.3 begins.
Validation: `npm test -- route-ranking`.

#### P2.3 — Mass route port (remaining ~85 pages, mechanical)
Steps, per page (codemod-able; section-parallelizable):
1. Move `src/app/<path>/page.tsx` → `src/routes/<path>/index.tsx`; wrap the body: `export const Route = createFileRoute('<url>')({ component: PageComponent })`; the body stays in the route file (autoCodeSplitting extracts the lazy chunk — no parallel components tree, shortest diff).
2. Mapping rules: `[id]`→`$id`, `[...path]`→`$.tsx`, `layout.tsx`→`route.tsx` with `<Outlet/>`; the 15 Promise-params pages + `projects/[id]/layout.tsx` delete the `params: Promise<…>` prop + `use()` and call the shim's `useParams()`.
3. `-`-prefix the 5 co-located non-route modules (`alerting/inhibition-hooks.ts`, `alerting/inhibition-panel.tsx`, `clusters/[id]/gatekeeper/hooks.ts`, `settings/auth/scim-tokens/hooks.ts`, `settings/siem/hooks.ts`) and fix their imports (+6 co-located tests move with their pages; `routeFileIgnorePattern` already excludes tests).
4. Auth pages: `change-password`, `forgot-password`, `reset-password` under `src/routes/auth/…`.
5. `catalog` page: `next/image` → plain `<img src width={40} height={40} alt loading="lazy">` (2 sites; remote icons — there is no optimizer in a static container and 40px icons gain nothing from srcset; keep width/height and alt per L6).
6. `clusters/[id]/template`: `next/dynamic` → `React.lazy` (second of the 2 monaco sites; first done in P1.7).
Definition of done: all 107 URLs resolve (spot-check per section; the full proof is P7.1); `src/app/` directory deleted.
Tests: co-located tests moved and green; `rbac-routing.test.ts` / `search-routing.test.ts` pass byte-identical (URL scheme unchanged).
Validation: `npm test && npm run type-check && npm run build`.

#### P2.4 — Boundaries, validateSearch, returnTo (D3)
Steps:
1. Boundary placement: `app/not-found.tsx` → `__root.tsx` `notFoundComponent`; `app/dashboard/not-found.tsx` + `app/dashboard/error.tsx` → `routes/dashboard/route.tsx` `notFoundComponent`/`errorComponent` (chrome stays mounted, `reset` maps 1:1); `clusters/[id]/error.tsx` already placed in P2.2; `app/global-error.tsx` → router `defaultErrorComponent` (chrome-less panel, no html/body) + the `main.tsx` ErrorBoundary from P1.7. Add stable testids now (P7.1 depends on them): `data-testid="route-error-boundary"`, `"route-not-found"`, and `data-testid="app-shell"` on the dashboard layout wrapper.
2. `validateSearch` policy (minimal — only where the URL is a deep-link contract): the 6 raw `useSearchParams` routes get passthrough-plus-typed validators (login `returnTo?`, reset-password `token?`, search `q?`, clusters / clusters/$id/apps / catalog / argocd/$instanceId filter keys) — passthrough so unrelated params survive; the 13 `useTabParam` routes get `(s) => s as { tab?: string } & Record<string, unknown>` (the hook's per-page allowlist stays the real validator); all other routes: none.
3. `returnTo` consumption: login success handlers navigate to `returnTo` iff `returnTo.startsWith('/') && !returnTo.startsWith('//')`, else `/dashboard` (new behavior — today it is never consumed; see D3).
4. Coordinated api.ts fix: the 401 terminal handler's `window.location.href = '/auth/login'` becomes `'/auth/login?returnTo=' + encodeURIComponent(location.pathname + location.search)` (full-page nav acceptable there; no router import in api.ts).
5. `useTabParam` keeps its implementation and signature (it already goes through the shim; zero page edits).
Definition of done: garbage URL renders `route-not-found`; a thrown render error renders `route-error-boundary` inside chrome; login round-trips a deep link.
Tests: `src/routes/__tests__/auth-guard.test.ts` (unauthenticated → redirect with exact `returnTo` incl. search; authenticated passes; note: guard is the cookie check, `must_change_password` is asserted at the layout-component level), a `use-tab-param` unit test (allowlist + other-param preservation), a returnTo-validation unit test (rejects `//evil`, `https://evil`).
Validation: `npm test`.

#### P2.5 — code-health repoint + anti-vacuous guard
Steps (in `astronomer/scripts/code-health-inventory.mjs`, same phase as the route move or the gates go silently vacuous):
1. Repoint the `frontend/src/app/` prefix filters in `localResponseShapes()` and `pageQueryKeys()` to `frontend/src/routes/`.
2. Update path literals: sonner-Toaster allowlist `frontend/src/app/(components)/providers.tsx` → `frontend/src/components/providers.tsx`; keep `frontend/src/lib/__tests__/auth-session.test.ts` where it is (do not move that test).
3. **Anti-vacuous guard (~6 lines, permanent)**: each path-filtered gate records how many files it scanned; `--check` fails with `gate <name> matched 0 files — path filter is stale` if a route-scoped gate scans nothing.
4. Regenerate the committed doc: `node scripts/code-health-inventory.mjs --write` (it will be regenerated once more as the branch's final commit, P7.4).
Definition of done: `npm run code-health` green on the new layout; deliberately breaking a filter path makes it fail loudly.
Validation: `npm run code-health`.

#### P2.6 — Next cleanup + routeTree drift check (D5)
Steps:
1. Grep gates (all must return zero): `grep -rn "from 'next" src/`, `grep -rn 'NEXT_PUBLIC' src/ deploy/ | grep -v .md`, `grep -rn 'href={{' src/`.
2. **Now** remove `next` and `next-themes` from package.json (kept installed through Phase 1 per the Phase-1 gate rule) and enable the `no-restricted-imports` ban on `next/*` in `eslint.config.mjs` (deferred from P1.4). Full-tree `type-check`, `lint`, and `npm test` must be green in this same commit.
3. Add the CI drift step to the pr-validation frontend job after build availability: `npm run build && git diff --exit-code src/routeTree.gen.ts`.
Definition of done: greps clean; `next`/`next-themes` gone from package.json; next/* ban active; drift step present and green.
Validation: the greps + `npm ci && npm run lint && npm run type-check && npm test` + CI run.

#### P2.7 — Playwright webServer → build + preview
Steps: in `playwright.config.ts`: `webServer: { command: 'npm run build && npx vite preview --host 127.0.0.1 --port <port>', url, reuseExistingServer: !process.env.CI, timeout: 180_000 }`. Preview (not dev) deliberately: it serves the built `dist/` with SPA-fallback semantics, so every existing deep-link `page.goto` implicitly tests fallback + the real bundle. Keep `PLAYWRIGHT_PORT`/`PLAYWRIGHT_CHROMIUM_EXECUTABLE` conventions and the `predev` wasm copy. If virtualized-table specs flake on SPA first paint, raise `expect.timeout` to 15s in config — never sprinkle waits.
Definition of done: all 8 mocked specs green on both projects against preview.
Validation: `npm run test:e2e`.

---

### Phase 3 — Stores: zustand → TanStack Store (zero consumer churn)

**Goal**: the three stores backed by TanStack Store behind zustand-shaped compat hooks at the existing module paths; byte-compatible persistence; zustand removed.

**Reasoning**: ~18 consumer files, 3 imperative-`setState` test files, and all 8 e2e localStorage seeds depend on today's shapes; a facade costs ~50 lines and eliminates the branch's flagged silent-breakage risk (D21).

#### P3.1 — `persisted-store.ts` + `store-hook.ts` + unit tests
Steps:
1. New `src/lib/persisted-store.ts` (~35 lines): wraps `new Store<T>(seed)` with zustand-persist envelope semantics — hydrate from `{"state":…,"version":N}`, run `migrate` on version mismatch, spread-over-initial, write `partialize`d state on subscribe, try/catch both directions (corrupted JSON → initial; quota errors → memory-only).
2. New `src/lib/store-hook.ts` (~15 lines): `createStoreHook(store)` returning a zustand-shaped hook (callable bare or with selector via `useStore(store, sel)`) plus `hook.getState()` and **shallow-merging** `hook.setState(partial|fn)` (zustand semantics — the 3 imperative test files rely on partial setState).
Definition of done: unit tests green.
Tests: `src/lib/__tests__/persisted-store.test.ts` — envelope byte-compat (serialize → exact zustand shape), migrate on version bump, corrupted-JSON fallback, partialize excludes functions; `store-hook.test.ts` — selector re-render, getState, shallow-merge setState.
Validation: `npm test -- persisted-store store-hook`.

#### P3.2 — Port the three stores, delete zustand
Steps:
1. `useAuthStore` (in `src/lib/store.ts`, same path): port 1:1 onto the helpers — key `astronomer-auth`, `version: 2`, `migrate` strips legacy `token`, `partialize` `{user, isAuthenticated}`, `logout()` keeps `clearLegacyTokenStorage()`. **Do not dissolve into Query** (rejected: `lib/permission-hooks.ts` and `useIsSuperuser` need the user synchronously at first render; login/TOTP writes before any query exists; e2e seeds the snapshot). The `ReturnType<typeof useAuthStore.getState>` type seam at `sidebar.tsx:807` works via the compat hook.
2. `useUIStore`: port only the live surface `{sidebarCollapsed, commandPaletteOpen}`; **delete dead** `sidebarOpen`/`setSidebarOpen`/`toggleSidebar` and `theme`/`setTheme` (zero consumers; theme owned by `lib/theme.tsx`). Key `astronomer-ui`, `partialize` `{sidebarCollapsed}` (stale persisted `theme` value is ignored harmlessly).
3. `useWindowManagerStore`: port verbatim (dedupe, LRU, close-selection, clampHeight); key `astronomer-window-manager`, `partialize` `{height, minimized}`; `addTab` keeps returning the id; imperative `useWindowManagerStore.getState().addTab(...)` call sites in the `$resource` page keep working.
4. Remove `zustand` from package.json.
Definition of done: `grep -rn zustand src/ package.json` empty; the 3 imperative-setState test files and all 8 e2e specs pass **unmodified**; a browser session seeded with current-prod-shaped localStorage hydrates unchanged (manual check of the three keys).
Tests: P0.3 window-manager tests now run against the new implementation unchanged.
Validation: `npm test && npm run test:e2e`.

---

### Phase 4 — Live-everything: SSE channel, Go events, polling elimination, TanStack DB

**Goal**: one multiplexed SSE channel drives every list/detail view; all ~85 `refetchInterval` sites become `liveFallback(base)` (zero polling while the stream is open); TanStack DB collections own pods + workload kinds; the Go backend publishes `<domain>.changed` events at write sites and expands informer coverage.

**Reasoning**: the endpoint, ticket auth, Redis fan-out, and per-event RBAC already exist; the work is framing (killing the KNOWN_EVENT_TYPES footgun), a real heartbeat, ~13 mechanical domain publishers, informer expansion, and a disciplined client dispatcher. Conversion rule (hard): **no `refetchInterval` is converted for a domain until its event coverage is committed on the migration branch with its Go tests green and a `routes.ts` entry + test exists** — each per-domain conversion commit is tiny and individually revertable. ("Committed on the branch", not "merged to main" — this is a big-bang branch per L4; nothing merges until the end.)

#### P4.1 — Go: default-message framing + `sys.ping` heartbeat (T1/T1b)
Steps:
1. `internal/handler/events_stream.go:154`: write `id: %d\ndata: %s\n\n` (drop the `event:` line). The JSON envelope already carries `type`; the client dispatches on `parsed.type` in `onmessage`. This kills the named-event `addEventListener` registration footgun permanently. No compat shim — this branch is the only client; big-bang.
2. Replace the invisible `: ping` comment keepalive (`events_stream.go:137`) with a real data frame `{"type":"sys.ping","time":…}` every 25s (same ticker).
3. Exempt `sys.*` from the SEC-R07 RBAC drop (before `eventAllowedForUser`, `events_stream.go:165`) — `sys.ping` has no `cluster_id`; without the exemption restricted users' client watchdogs fire spuriously.
Definition of done: stream emits default-framed events + pings; restricted-user stream still receives pings.
Tests: extend `events_stream` handler tests: frame format (no `event:` line), ping cadence frame shape, restricted user receives `sys.ping` but not unscoped domain events.
Validation: `go test ./internal/handler/... && make verify`.

#### P4.2 — Go: `PublishChanged` helper + envelope contract test (T2)
Steps: new `internal/events/publish.go` with `PublishChanged(bus, resource, clusterID, entityID string, extra map[string]any)` emitting `{type: "<resource>.changed", data: {cluster_id, id, …extra}}`; add the new type constants to `internal/events/bus.go`. Table-driven test asserting every cluster-scoped type's payload carries `cluster_id` (SEC-R07 drops events without it fail-closed for restricted users — a publisher forgetting `cluster_id` silently breaks liveness for exactly the users least able to debug it). Envelope rules: `type` = `<resource>.<verb>` (verb `changed` for all DB domains; reserved special verbs: `metrics`, `status_changed`, `heartbeat`, `step`, `phase`, `ping`); `data` snake_case on the wire; **no object bodies** (metadata-only avoids secret leakage and server-transform drift; payloads stay limited to the existing metrics/status shapes).
Definition of done: helper + constants + test in.
Validation: `go test ./internal/events/...`.

#### P4.3 — Client live module: transport port, watchdog, status store, liveFallback
Steps:
1. Port `src/lib/live-events.ts` → `src/lib/live/stream.ts` keeping the proven singleton (module `ConnectionState`, EventTarget fan-out, refcount, 1s→30s backoff, mint-then-connect with single-use tickets — never EventSource auto-reconnect, `visibilitychange` reopen). Changes: `onmessage`-only dispatch, delete `KNOWN_EVENT_TYPES` (type union becomes advisory), env via `API_BASE`, drop `'use client'`.
2. Watchdog: reset a 75s timer on ANY frame; on expiry force-close and enter the normal backoff/re-mint loop (3 missed `sys.ping`s = half-open connection).
3. `src/lib/live/status-store.ts`: TanStack Store atom `liveStatus: 'idle'|'connecting'|'open'|'closed'` + `liveFallback(baseMs)` returning `() => liveStatus.state === 'open' ? false : baseMs`; keep `liveEventsStatus()` as a non-reactive read.
4. Transition invalidations: `closed→open` (not first open) ⇒ `queryClient.invalidateQueries({ refetchType: 'active' })` — this IS the resume story (no Last-Event-ID replay: the bus has no history, IDs are per-pod counters; a ring buffer + global ordering + RBAC replay filtering is speculative infrastructure that bulk invalidation of ~5–15 mounted queries replaces). `open→closed` ⇒ the same bulk active invalidate — required so every mounted query's `refetchInterval` function re-evaluates and fallback polling actually restarts (React Query only re-evaluates intervals after a fetch).
5. `src/lib/live/envelope.ts`: frame parse + central camelization via `src/lib/camelize.ts` (P0.1) — all downstream live code sees camelCase only, resolving the snake/camel split once.
6. `src/lib/live/cluster-merger.ts`: port `useLiveClusterMetricsMerger` + merge helpers verbatim (including the deliberate no-invalidate-on-status-change and the optimistic `disconnected→active` flip); the manual `cpu_percentage→cpuPercentage` mapping collapses (dispatcher delivers camelCase). `setQueryData` patching stays **only** for `cluster.metrics`/`cluster.status_changed` ticks — do not extend patching to new domains.
7. `src/lib/live/hooks.ts`: `useLiveEvents`, `useLiveSubscribe`, and `useLiveQueryInvalidation` with its **exact current signature** (~20 call sites port untouched). In this task the hooks wrap the raw stream EventTarget directly (the dispatcher `src/lib/live/dispatch.ts` and its routing/pacing do not exist until P4.4); P4.4 step 3 re-points them at the dispatcher with no signature change. The P4.3 DoD's catch-up-invalidation check uses the P4.3 step-4 transition invalidations only.
8. Mount point unchanged: dashboard layout route above `<Outlet/>` (P2.1 already placed it).
Definition of done: stream connects, heartbeat observed, kill-connection test shows watchdog reconnect + catch-up invalidation.
Tests: port `live-events.test.ts` and extend: default-frame dispatch, watchdog expiry, first-open does NOT invalidate, reconnect DOES, open→closed kick, ticket re-mint per (re)connect, refcount close, envelope camelization (snake wire → camel subscriber).
Validation: `npm test -- live`.

#### P4.4 — Targeted k8s invalidation + Pacer throttle (zero backend work)
Steps:
1. `src/lib/live/paced-invalidate.ts`: keyed trailing-throttle map — one `Throttler` per stringified query key, `wait: 400, leading: true, trailing: true` (leading keeps single events instant; trailing coalesces informer storms); clear the map on stream close. ALL live invalidations flow through it.
2. `src/lib/live/routes.ts` (D23): `K8S_KIND_ROUTES` mapping `cluster.k8s_changed` per `data.kind` → precise `queryKeys` entries (Deployment → workloads byKind, Pod → podsAll + workload pods, Backup/Application CRDs when P4.6 lands, default → generic list for the kind); `EVENT_ROUTES` starts with the existing 13 types + `audit.` prefix → `qk.activity` (restricted users never receive audit events — no `cluster_id` — and heal via liveFallback + reconnect; documented in the table).
3. `src/lib/live/dispatch.ts`: `onmessage` → envelope → route lookup → `pacedInvalidate` / merger.
4. Convert the k8s-domain `refetchInterval` sites (clusters/agents/workloads/generic/explorer families in Appendix B) to `liveFallback(base)`; delete `liveAwareRefetchInterval` (the ×4-stretch compromise existed only because coverage was partial).
Definition of done: with a live agent, k8s edits reflect in lists without polling; devtools shows zero interval refetches while the stream is open.
Tests: `src/lib/live/routes.test.ts` — **exhaustiveness**: every event type named anywhere in `routes.ts` has a mapping and every mapped key exists in the `query-keys.ts` factory (no ad-hoc keys); `paced-invalidate.test.ts` — burst of N events in the window ⇒ exactly 1 trailing invalidate + 1 leading (uses `vi.useFakeTimers`, vitest-native).
Validation: `npm test -- routes paced`.

#### P4.5 — Go domain publishers (T3) + per-domain poll conversion + T5/T6/T7
Steps:
1. Publishers, pattern = `internal/handler/clusters.go:484` (fire-and-forget after successful DB write; lossy by design, no write-path error coupling). Domains → handler files (all verified to exist): `backup.changed` (`backups.go`, `backups_reconciler.go`, `backups_velero.go`, `backups_retention.go`; kind field backup|restore|schedule), `fleet_operation.changed` per **target** with the target's `cluster_id` (`fleet_operations.go`, `fleet_dispatcher.go`), `logging_operation.changed` (`logging.go`), `tool_operation.changed` (`tools.go`, `operation_runner.go`, `operation_status.go`), `cis_scan.changed`, `image_scan.changed` (`image_vulns*.go`), `argocd.changed` with scope field (`argocd.go`, `argocd/` subpackage, `argocd_metrics.go`, `argocd_ownership.go`, `argocd_orphans.go` — publish on own writes AND at the end of each server-side reconcile pass so Argo-side drift surfaces at reconcile cadence), `admin_queue.changed` + `siem_forwarder.changed` (unscoped, superuser-only by fail-closed drop — correct per D9), `agent_fleet.changed`, `template_binding.changed`, `registry.changed`, `snapshot.changed`.
2. T5: re-snapshot RBAC bindings every 5 minutes inside the stream loop (`events_stream.go:100` — move binding load into a ticker-checked refresher). Bounds mid-stream revocation staleness to 5 min (D10).
3. T6: bus subscriber buffer 64→256 in `internal/events/bus.go`; confirm `RecordDroppedEvent` is exported to an alertable metric; no coalescing in this branch (D7 threshold documented).
4. T7: delete the unused registration SSE endpoint (`internal/handler/cluster_registration.go:383` `StreamEvents`) + its route in `internal/server/routes.go` (frontend uses the global bus for registration events already). **In the same commit** (or the gate goes red with no in-plan remediation): remove the `/api/v1/clusters/{id}/registration/events/` path (docs/openapi.yaml:5731) and any now-orphaned schemas from `docs/openapi.yaml` — `scripts/openapi-coverage.mjs --check` hard-fails on any documented-but-unrouted operation and runs inside `make verify` — then run `make openapi-embed` to sync `internal/handler/assets/openapi.yaml` (make verify diff-checks the embedded copy) and regenerate the RouteTable snapshot if it captures this route. T7 validation: `make verify` (openapi-coverage --check + generated-types check + embed diff + RouteTable test) green.
5. Frontend, per domain **after its publisher is committed on the migration branch with its Go tests green** (not "merged" — see the Phase 4 conversion rule): add `EVENT_ROUTES` entries (list + detail keys via `data.id`), convert that domain's `refetchInterval` sites to `liveFallback(base)`; poll-until-terminal trackers wrap: `refetchInterval: (q) => isTerminal(q) ? false : liveFallback(base)(q)` (belt-and-braces for in-flight ops during a stream drop). Exception (D8): argocd manifests/history/orphan-report keep plain polls. Manual intervals: registration-timeline poll becomes stream-status-conditional; cluster-shell 5s session poll stays (WS-scoped, out of SSE scope; its 1s countdown tick is UI); `useDeleteCluster`'s 6×5s invalidation loop is deleted (`cluster.deleted` + `fleet_operation.changed` cover the tombstone window).
Definition of done: every P4.5-listed domain converted; Go tests per publisher. (The Appendix B domains NOT covered by the P4.5 publisher list — alerting, installed charts, security policies/scans, quota usage, network access, service mesh, conditions/remediation — are closed out in **P4.9**; the full "every Appendix B row converted or explicitly KEEP-annotated" check is the P4.9 DoD.)
Tests: per-publisher Go tests (event emitted on write with `cluster_id`); `routes.test.ts` exhaustiveness grows with each domain; extend the P4.2 table test with every new type.
Validation: `go test ./... && make verify && npm test && npm run code-health` (`make verify` is mandatory here because of T7's openapi.yaml + embed edits).

#### P4.6 — Go: informer expansion + CRDs (T4); delete per-page proxy watches
Steps:
1. `internal/agent/state_subscriber.go`: add a `metadatainformer` factory (metadata-only — same `MsgStateUpdate` shape, no bodies, no secret surface) for Namespace, Job, CronJob, Ingress, NetworkPolicy, PV, PVC, StorageClass, HPA, ServiceAccount, Role, RoleBinding, ClusterRole, ClusterRoleBinding, ResourceQuota, **plus Secret filtered agent-side to Helm release storage only (`type=helm.sh/release.v1` / name prefix `sh.helm.release.v1.` — drop all other Secrets before forwarding; needed for the P4.9 installed-charts conversion, and never forwards non-Helm secret metadata)**; CRDs discover-if-present (tolerate absence): Velero Backup/Restore/Schedule, Argo Application/ApplicationSet, Gatekeeper constraints, Trivy VulnerabilityReport. Reuse the per-key 1/s limiter and reconnect replay. Server side needs zero change (`internal/tunnel/handler.go:629` already folds into `cluster.k8s_changed`); publish at the bus regardless of tunnel transport (tunnel2 migration is transport-agnostic at the publish point).
2. Frontend: add the CRD kinds to `K8S_KIND_ROUTES` (Backup → backups keys, Application → argocd keys, VulnerabilityReport → image-scan keys, …); convert the CRD-domain polls (backups list, argocd liveApps, scans) to `liveFallback`.
3. Delete the dedicated per-page proxy watch inside `useResourceWatchInvalidation` consumers (explorer tables): the bus signal replaces it and stops fighting the `ClassK8sProxy` ~20-request rate limiter. The explorer invalidation path becomes `cluster.k8s_changed` routed per kind + Pacer.
4. **Webhook/SIEM bus-tap exposure (R9)**: `internal/webhook/tap.go` and `internal/siem/bus_tap.go` subscribe to the same bus and create a delivery row per matched event — the informer expansion multiplies `cluster.k8s_changed` volume that existing `cluster.*`/`cluster.k8s_changed` filters already match, and P4.5/P4.9 add new types a `*` filter matches. Decide exposure here: (a) always exclude `sys.*` from tap matching (new types, no existing subscriber can depend on them); (b) do NOT silently change `cluster.k8s_changed` tap semantics (existing subscriptions may rely on it) — instead measure the delivery-row write rate on a broad-filter subscription during the D7 24h soak and record the delta; if amplification is unbounded, add per-type tap exclusions as a scoped follow-up (Section 9).
Definition of done: informer kinds emit `k8s_changed`; explorer tables live-update via the bus with no per-page watch; `sys.*` tap exclusion in with a test; soak measurement task recorded.
Tests: agent informer registration test (kinds list), tolerate-absent-CRD test; frontend routes test rows for the CRD kinds.
Validation: `go test ./internal/agent/... ./internal/tunnel/... && npm test`.

#### P4.7 — TanStack DB collections (pods + workload kinds); delete `use-resource-watch.ts`
Steps:
1. Add exact-pinned `@tanstack/db` + `@tanstack/react-db` (audit-gated per D6). Do NOT use `@tanstack/query-db-collection` — these two collections are stream-fed, so plain `createCollection` with a custom `sync` is smaller.
2. `src/lib/db/collections.ts`: `k8sCollection({clusterId, source})` — `getKey` = uid fallback ns/name (reuse `identityOf`); `sync` = REST list seed via lib/api (camel-exempt `/k8s/` path) then `openProxyWatch`/pods-SSE frames → `write({type:'insert'|'update'|'delete'})` + `commit`; on drop, mark meta `fallback` (caller's `liveFallback` poll re-seeds) and retry with backoff. Scope (justified): **IN** — pods per (cluster, namespace) via the pods SSE watch, and the 5 workload kinds via proxy NDJSON (the only streams carrying full objects with ADDED/MODIFIED/DELETED verbs; do not open more per-page watches — proxy rate limiter). **OUT (plain Query)** — clusters (server-paginated at fleet scale; the proven merger stays), operations (poll-until-terminal + metadata-only events = Query + invalidation by definition), audit (append-only, silently empty for restricted principals — a trap as a collection).
3. Workloads page + pods consumers read via `useLiveQuery`; hand-rolled search/filter moves into the collection query.
4. Delete `src/hooks/use-resource-watch.ts` and `foldWatchFrame`; port the fold tests to collection-sync tests (ADDED/MODIFIED/DELETED ordering, resourceVersion regression ignore).
5. Optional (skip unless trivial): pods-watch ticket kind `logs` → `events` (T8).
Definition of done: workloads/pods views live-fold objects; `use-resource-watch.ts` gone; code-health still green (watch fetch already lives in `lib/api/`).
Tests: collection sync tests (frame folding, seed+watch merge, drop→fallback status); existing workloads e2e spec green.
Validation: `npm test && npm run test:e2e`.

#### P4.8 — Pacer for search inputs (bounded: 3 sites + data-table)
Steps: add exact-pinned `@tanstack/react-pacer` (shared version with P4.4). (1) `routes/dashboard/search/index.tsx`: delete hand-rolled `useDebouncedValue`, use Pacer's (250ms, URL sync unchanged); (2) `clusters/$id/apps` page: add the 200ms debounce the code comment asks for; (3) `components/ui/data-table.tsx`: 200ms `useDebouncedValue` between filter input state and filter application (one hook at the shared component covers 7 inputs). `global-search.tsx` stays un-debounced (navigates on submit — correct). Former item 4 (use-resource-watch) is moot per D13.
Definition of done: 3 sites converted; no hand-rolled debounce left in src (grep `useDebouncedValue` custom impl).
Tests: existing search/data-table tests green; behavior parity (250ms) asserted where a test exists.
Validation: `npm test`.

#### P4.9 — Coverage completion: remaining Appendix B domains (convert or KEEP) [ADDED IN REVIEW]
The P4.5 publisher list + P4.4 k8s routing + P4.6 CRDs leave ~14 Appendix B sites with no event source and no KEEP annotation, which would make the "eliminate polling" DoD unsatisfiable. This task closes every remaining row. Same publisher pattern as P4.5 (`PublishChanged`, fire-and-forget after successful DB write, `cluster_id` where cluster-scoped, table-test extended per type).

Steps — CONVERT (new publishers / routes):
1. `alerting.changed` (`internal/handler/alerting.go` — verified to exist and publish nothing today): published on rule/silence/baseline CRUD writes AND on the alert-event ingestion/resolution path, `data.kind` = `rule|event|silence|baseline`. Converts `hooks.ts` :848 useAlertRules, :899 useAlertEvents, :972 useAlertSilences, :1001 useAnomalyBaselines.
2. `security_policy.changed` + `security_scan.changed` (`internal/handler/security.go`): converts :1850 useClusterSecurityPolicies and :1901 useSecurityScans (`cis_scan.changed`/`image_scan.changed` cover only their own pages, not the generic scans list — `security_scan.changed` is published by the shared scan-write path).
3. `network_access.changed` (apiserver-allowlist write handlers — grep `apiserver` under `internal/handler/`): published on allowlist create/update/enforce writes and on the agent status-ingest write if one exists. Converts network-access :116; enforcement progress additionally heals via the conditions route below.
4. Installed charts: route k8s kind `Secret` (Helm-release-filtered agent-side per P4.6 step 1) in `K8S_KIND_ROUTES` → `useInstalledCharts` (:1540) + `clusterPages.appsInstalled` (:176) keys; also publish `catalog_release.changed` at the server-side install/uninstall/upgrade write sites in `internal/handler/catalog.go` so server-initiated changes surface even when the agent stream lags. Converts both sites.
5. Cluster conditions/remediation (`hooks.ts` :107/:119): **no new publisher** — add `EVENT_ROUTES` entries routing the existing `cluster.status_changed` + heartbeat events to the conditions/remediation query keys. Converts both.

Steps — KEEP (explicit annotations, same standing as the D8 trio):
6. Project quota usage (`components/projects/hooks.ts:86`, 30s) and `settings/quotas/usage`: KEEP — usage is a computed aggregate over cluster state with no discrete write event; a publisher would have nothing truthful to fire on.
7. Service mesh (`service-mesh` :375/:382 60s; cluster detail :103 `serviceMeshHeader` 5min): KEEP — mesh state is cluster-side truth read through the agent at request time (`internal/handler/service_mesh.go` performs no writes to publish on), exactly the D8 "other-side-truth" shape.

Definition of done: every Appendix B row is now either converted or carries an explicit KEEP annotation (D8 trio, cluster-shell session poll, quota usage, service mesh) — this is the check P4.5's DoD delegates here; `routes.test.ts` exhaustiveness covers the new types; per-publisher Go tests green.
Validation: `go test ./internal/... && npm test && npm run code-health`.

---

### Phase 5 — Forms: TanStack Form kit + M/L conversions

**Goal**: a small `createFormHook` kit with a11y-correct field components; ~45–55 M/L forms converted; the two secret round-trip variants preserved and unit-tested; S forms and special components explicitly left alone.

**Reasoning**: validation surface is thin (required checks, password match, occasional format) — plain function validators, **no schema library** (adding zod to express `required` is speculative abstraction; TanStack Form v1 supports Standard Schema later if ever needed). Guard-rail per L6: every existing imperative check is ported into a validator 1:1 — conversion commits list old check → new validator.

Conversion policy (hard): convert iff ≥4 fields, or secret round-trip, or multi-step wizard, or shared form component. Never convert (reviewers reject drive-bys): `confirm-dialog.tsx`, ScaleDialog, create-namespace inline, the small dialogs inside the `$resource` page, forgot-password, register-sso, read-audit filters, compliance, quota S-pages, `ExtForm.tsx` (declarative FormSpec interpreter), `helm-values-form.tsx` (a controlled *field*, not a form), and list editors (`key-value-editor`, `role-editor`, `selector-builder`, `target-refs-editor` — used as fields via `form.Field` array values).

#### P5.1 — F0: kit + secrets helper + isDirty verification (D14)
Steps:
1. `src/lib/form.ts` (~20 lines): `createFormHookContexts` + `createFormHook` with `fieldComponents: { TextField, NumberField, PasswordField, SecretField, TextareaField, SelectField, SwitchField, CheckboxField }`, `formComponents: { SubmitButton }`.
2. `src/components/form/fields.tsx`: dumb fields — label + control + error line — styled with **today's exact class strings** (extract the shared input class once as `inputClassName`; there is no shared Input component today). `SelectField` wraps the native `<select>` styling in use; Switch/Checkbox wrap the existing Radix primitives. A11y baked in (L6): generated `id` + `htmlFor`, `aria-invalid` on error, error `<p>` via `aria-describedby` — a strict improvement over today's label-without-htmlFor markup.
3. `src/components/form/secrets.ts`: `stripUntouchedSecrets(value, form, secretKeys)` using per-field `meta.isDirty`; `secretMarkerKey(name)` (the snake→camel marker recompute from `connector-form.tsx:38-43`, moved verbatim); `isStoredSecret(config, name)`. `SecretField` takes `stored: boolean` and renders the `••••••••` placeholder + "type to rotate" hint when stored and pristine.
4. **D14 verification test**: dirty a secret field, `form.reset(refreshedInitial)`, assert `stripUntouchedSecrets` still omits/includes correctly; if `isDirty` resets across reset, switch `SecretField` to an internal touched map behind the same API and keep the test.
Definition of done: kit + tests green; both secret variants (marker + sentinel) covered by unit tests including the axios camelize interaction.
Tests: `components/form/__tests__/fields.test.tsx` (a11y wiring, error display), `form/secrets` tests.
Validation: `npm test -- form`.

#### P5.2 — F1 pilot: smtp + credential-form
Steps: convert `routes/dashboard/settings/smtp/index.tsx` (sentinel secret `__redacted__`, 8 fields, test-send mutation — strip-in-mutation behavior unchanged) and `components/projects/cloud-credentials/credential-form.tsx` (+ its new/edit wrapper pages; provider-dependent fields, marker secrets). The pilot validates the kit against both secret variants on bounded surfaces — adjust the kit here, not later.
Definition of done (the per-form DoD used for ALL of Phase 5): identical submit body (spot-check via existing mocks), every prior validation check present as a validator, secret omission verified, no visual diff, mutations/toasts/invalidation untouched (onSubmit calls the existing hooks).
Validation: `npm test`; e2e-mock body assertion on credential edit.

#### P5.3 — F2: shared form components
Steps: `components/auth/connector-form.tsx` (506L — marker secrets, dynamic registry-driven fields, nested groups; delete the hand-rolled `touchedSecrets` in favor of the kit) and `components/projects/cluster-templates/template-form.tsx` (577L — 5 sections, KV labels, per-tool overrides). These carry many pages each.
Validation: connector/template page tests + `npm run test:e2e`.

#### P5.4 — F3: auth flows (critical path; run e2e after each)
Steps: login (+423 TOTP branch as a sub-form), change-password, reset-password, `account/security` MFA wizard (3 forms — one per step, not a mega-form). Every password-match/strength/required check ported 1:1.
Validation: `npm run test:e2e` after each page; auth-guard unit tests still green.

#### P5.5 — F4: settings hub (stampable, parallelizable)
Steps: `general`, `siem`, `platform`, `auth/settings`, `auth/install`, `vault`, `cluster-groups`, `gitops/new`+`$id`, `group-mappings`, `native-rbac`, `network-policies`, `templates/$key`, `webhooks/new`+`$id`, `widgets`, `scim-tokens` dialog.
Validation: co-located tests (`siem`, `scim-tokens`) pass with at most selector-level edits; `npm test`.

#### P5.6 — F5: cluster/dashboard L pages and dialog suites (droppable per page)
Steps: `clusters/$id/{snapshots,registries,network-access,image-scans,service-mesh,template}`, nodes drain dialog, `alerting` (+inhibition-panel), `rbac` modals, `catalog` modals (+app-install-modal), `logging`, `security`, `backups/{storage,schedules}/new` + restore-modal, projects create/policy/catalogs, argocd dialog suite (7 dialogs in `components/argocd/`), `applicationsets/new`, fleet create-operation dialog, `admin/users/$id`, `clusters/register`. Any page not finished by gate time ships unconverted (works today) and is filed per D15.
Validation: `npm test && npm run test:e2e` at the end of the phase.

---

### Phase 6 — Container, chart, deploy

**Goal**: the SPA ships in its own nginx static container on the unchanged name/port; chart trimmed of dead knobs; k3d end-to-end proof.

**Reasoning (D-BD1, nginx over caddy)**: the repo already digest-pins, Trivy-scans, and operates exactly this nginx base (`deploy/nginx/Dockerfile.nginx` uses `nginx:1.27-alpine@sha256:65645c…`) with established idioms (`pid /tmp/nginx.pid`, JSON logs). Caddy would add a second server technology to the pin/scan/SBOM/cosign/air-gap matrix for zero capability gain (auto-TLS is useless — TLS terminates at the gateway). Keeping image name `astronomer-frontend` and port 3000 means Makefile, k3d scripts, Service, HTTPRoute, Ingress, PDB, images.txt, and the release matrix stay byte-identical or nearly so; the **workflow files and Makefile themselves need zero edits** — but note this does NOT mean the api-contract gate is untouched by the branch: T7 (P4.5) must edit `docs/openapi.yaml` and regenerate the embedded `internal/handler/assets/openapi.yaml` (`make openapi-embed`) or `make verify` fails; a red api-contract gate after T7 is that omission, not an unrelated failure.

#### P6.1 — `frontend/nginx.conf` + `frontend/Dockerfile`
Steps:
1. `frontend/nginx.conf` (<20 lines): `worker_processes auto; pid /tmp/nginx.pid;` all five temp paths under `/tmp` (readOnlyRootFilesystem + uid 1001 — non-root nginx skips the privilege split, no `user` directive), `listen 3000`, `include mime.types` (application/wasm present in stock 1.27 — required for `instantiateStreaming`), gzip on (no brotli — stock alpine lacks the module and TLS/h2 is at the gateway), `location = /healthz { return 200; }`, `location /assets/ { Cache-Control "public, max-age=31536000, immutable"; try_files $uri =404; }`, `location / { Cache-Control "no-cache"; try_files $uri /index.html; }`. No `/api`/`/argocd` carve-outs needed (ingress routes them before this pod) and no redirects that could shadow them in compose setups. `index.html` and unhashed `/wterm.wasm` fall in the no-cache bucket (ETag revalidation — correct for a file that changes with `@wterm/core` upgrades).
2. `frontend/Dockerfile`: stage 1 `node:22-alpine@sha256:<P0.4 pin>` (matches CI Node 22; current node:20 is behind), plain `npm ci` (drop the stale `--legacy-peer-deps` and `libc6-compat`), `ARG VERSION=0.2.0-dev` → `ENV VERSION=$VERSION` → `npm run build` (prebuild copies wterm.wasm; the Vite `define` bakes `__APP_VERSION__`); stage 2 `nginx:1.27-alpine@sha256:65645c…` (or the D16 1.29 bump in lockstep with `Dockerfile.nginx`), declare `ARG VERSION GIT_COMMIT BUILD_DATE` + OCI labels (release.yaml passes all three), `rm -rf /etc/nginx/conf.d /usr/share/nginx/html`, COPY nginx.conf + dist, `EXPOSE 3000`, wget HEALTHCHECK on `/healthz`, `ENTRYPOINT ["nginx","-g","daemon off;"]` (deliberately bypasses `/docker-entrypoint.d` scripts that write under a read-only rootfs).
Definition of done: image builds; digest-pin test green; Trivy clean.
Validation: `go test ./deploy/ -run TestDockerfileBaseImagesAreDigestPinned`; `docker build -t astronomer-frontend:dev frontend/ && trivy image --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 astronomer-frontend:dev`.

#### P6.2 — Chart, values, schema, compose, ingress annotations, netpol
Steps (one coordinated commit for values+schema or `helm lint` breaks):
1. `deploy/chart/templates/frontend-deployment.yaml`: delete the env block (`NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_WS_URL`, `NEXT_TELEMETRY_DISABLED` — verified dead); add `volumeMounts: [{name: tmp, mountPath: /tmp}]` + `volumes: [{name: tmp, emptyDir: {sizeLimit: 64Mi}}]`. Keep tcpSocket probes (port-agnostic, zero churn).
2. `values.yaml`/`values-k3d.yaml`/`values-production.yaml`: remove `frontend.apiBaseUrl`/`wsBaseUrl`; remove the k3d `hostAliases:` block (existed solely for Next SSR resolution; keep the generic template hook); update the comment header to "(SPA dashboard)". `values.schema.json`: drop the removed keys.
3. Legacy-Ingress SSE/WS defaults in `values.yaml` `ingress.annotations`: `proxy-read-timeout: "3600"`, `proxy-send-timeout: "3600"`, `proxy-buffering: "off"` (idle terminals + hours-long SSE outlive ingress-nginx's 60s default). **Gateway path: no changes** — NGINX Gateway Fabric honors the backend's `X-Accel-Buffering: no` and sets no HTTPRoute timeouts today; do not add `rules[].timeouts`.
4. `deploy/docker-compose.yml:113-127`: drop the frontend `NEXT_PUBLIC_*` env block.
5. NetworkPolicy trim (droppable per D17): remove the frontend→server:8000 egress rule and the server ingress-from-frontend rule (`templates/networkpolicy.yaml:33-70,107-115`) — a static pod makes no outbound calls; keep DNS egress.
Definition of done: renders + schema green for both dev (`frontend.enabled=true`) and production values.
Validation: `helm lint deploy/chart && go test ./deploy/...`.

#### P6.3 — k3d end-to-end smoke (real container, real gateway)
Steps: `make docker-build-frontend k3d-import-all helm-install`, then verify: deep-link hard refresh on `/dashboard/clusters/<id>` (SPA fallback), `/argocd/` UI loads and its theme is not clobbered (D24), an exec terminal (WS through the gateway), the live events stream (SSE, watch a cluster row update), version badge shows the baked VERSION, and the frontend pod runs read-only as uid 1001 (`kubectl exec` write attempt outside /tmp fails).
Definition of done: all checks pass; failures here are release blockers, not follow-ups.
Validation: the manual checklist above, recorded in the PR.

---

### Phase 7 — Test hardening and gate closure

**Goal**: the new test floors (107-route smoke crawl, live e2e tier), the visual review artifact, and the final green run of the entire merge gate.

#### P7.1 — Route-smoke crawl (all 107 routes)
Steps:
1. `frontend/scripts/generate-route-manifest.mjs` (~60 lines): scan `src/routes/**` (same convention as the router plugin), emit `tests/e2e-smoke/route-manifest.generated.json` — one URL per route with params substituted from a single `PARAM_FIXTURES` map (`id: 'c-smoke-1'`, `nodeName`, `resource: 'deployments'`, `path`, `slug: []` and a populated variant, …). The script **fails on any param without a fixture** and asserts manifest length equals a checked-in expected count (starts at 107) so dropped routes fail loudly. Wire as `pretest:e2e:smoke`.
2. `frontend/scripts/generate-e2e-stubs.mjs`: read `docs/openapi.yaml` (js-yaml via the same createRequire trick), emit minimal 200 bodies per GET path from response schemas (arrays→`[]`, required props with type-zero values, enums→first member); runtime matcher converts `{id}` templates to regexes, longest-literal wins; hand-written `stub-overrides.ts` for the ~10 endpoints needing real-ish data (`/auth/me`, `/features/`, `/extensions/mounts/`, the smoke cluster detail). Emit **snake_case** bodies (the camelize interceptor expects wire format).
3. New Playwright project `route-smoke` (chromium only, `testDir: './tests/e2e-smoke'`): loop over the manifest; per URL: collect `pageerror` + console errors, `seedAuth`, install stubs, `goto`, assert `app-shell` visible, `route-error-boundary` count 0, `route-not-found` count 0, filtered console errors empty (ALLOWLIST starts empty; additions need an issue-link comment). Auth pages run without seed and assert their forms render. Two negative tests keep the detector honest: garbage URL must show `route-not-found`; a forced-500 stub must show the error boundary.
4. Timebox stub-override writing to 1 day; any page that crashes on skeleton data is a real error-handling bug — fix in src (L6), not in stubs.
5. `package.json`: `"test:e2e:smoke": "playwright test --project=route-smoke"`; tier-1 script pinned to `--project=chromium --project=mobile-chromium` so it never picks up smoke/live.
6. **Edit `.github/workflows/pr-validation.yaml`** (frontend job): add a `npm run test:e2e:smoke` step immediately after the existing `npm run test:e2e` step — Section 6 lists the smoke crawl inside the frontend job, and without this step the merge gate's smoke tier runs only locally.
Definition of done: 107/107 green (~2–3 min at 4 workers); negative tests fail when sabotaged.
Validation: `npm run test:e2e:smoke`.

#### P7.2 — Live e2e tier (real Go backend)
Steps:
1. New Playwright project `live` (`testDir: './tests/e2e-live'`, `retries: 1` for this project only), reachable via the P1.1 `preview.proxy` (`BACKEND_URL`).
2. Exactly 4 specs: `login.live.spec.ts` (bootstrap admin → HttpOnly cookie → dashboard → logout → guard bounces); `guard-returnto.live.spec.ts` (unauthenticated deep link with `?tab=` → login → returns to the deep link, tab intact — locks D3); `sse-live.spec.ts` (create a cluster via `request.post` with cookies + `X-CSRF-Token` from the readable csrf cookie; row appears without navigation ≤10s — proves ticket mint → stream → dispatch → cache); `sse-reconnect.live.spec.ts` (`setOffline(true)` 3s → `setOffline(false)` → create second cluster via API → appears ≤15s — end-to-ends re-mint + invalidate-on-reconnect; this is the test that fails if resume is broken).
3. CI job `frontend-e2e-live` in `pr-validation.yaml` (parallel with the frontend job): services postgres:16-alpine + redis:7-alpine; setup-go; `make migrate-up`; background `go run ./cmd/server` with compose dev env values + `ASTRONOMER_BOOTSTRAP_PASSWORD=e2e-live-password` (verified: `EnsureBootstrapAdmin` creates an immediately usable admin, no forced reset); curl-wait `/health`; setup-node 22; `BACKEND_URL=http://localhost:8000 npx playwright test --project=live`; upload report. Runtime ≤6 min.
Definition of done: 4 specs green in CI. A spec flaking >2×/week post-merge moves to nightly — never deleted.
Validation: CI run of the new job.

#### P7.3 — Screenshot gallery + manual QA checklist
Steps:
1. Gallery (non-blocking): behind `SMOKE_GALLERY=1`, the route-smoke crawl also takes full-page screenshots to `gallery/`; **edit `.github/workflows/pr-validation.yaml`** to run the smoke step with `SMOKE_GALLERY=1` and add an `actions/upload-artifact@v4` step (frontend job, `if: always()`, `name: smoke-gallery`, `path: frontend/gallery/`) so the artifact actually appears on the migration PR. Reviewers eyeball 107 thumbnails once. No pixel-diff gate (107 pages × themes would be a flake factory for a one-time migration).
2. Manual QA checklist, executed once on the P6.3 k3d deployment by whoever merges: auth (login local + one SSO redirect, MFA 423, forgot/reset, must_change_password kick-out, returnTo, logout); terminals (cluster shell, pod exec, logs follow; window-manager keeps sessions across navigation, minimize/restore, LRU eviction); monaco lazy-loads on template + YAML editor; theme cycle + `astronomer-theme` key + ArgoCD co-hosting not clobbered; 3 of the 13 `?tab=` pages via hard refresh + back/forward; scroll behavior (D25): navigate from the bottom of a long clusters/audit list to a detail page → lands at top, back restores position, tab switch does not jump; live data with a real agent + devtools-offline recovery; one virtualized and one server-paginated table; Pixel-7-ish viewport spot-check; version badge + favicon.
3. Anything found that is automatable gets a regression test in the appropriate tier before its fix merges.
Definition of done: checklist executed and recorded in the PR; gallery reviewed.

#### P7.4 — Final regen + full-gate run
Steps: `node scripts/code-health-inventory.mjs --write` (final doc regen — any file move after this re-runs it), then execute the complete merge gate (Section 6) top to bottom locally and in CI.
Definition of done: every command in Section 6 green in one run; branch is merge-ready.

---

## 6. Merge gate — exact commands (ALL must be green)

```bash
# frontend job (Node 22, working-directory: frontend) — pr-validation.yaml order preserved
npm ci
npm run code-health                    # openapi types check + inventory --check --verify-doc (repointed, anti-vacuous)
npm run lint                           # rebuilt flat config (P1.4)
npm run type-check                     # tsc --noEmit incl. committed routeTree.gen.ts
npm test                               # vitest run (was jest --runInBand); ~408 cases, same count
npm run build && git diff --exit-code src/routeTree.gen.ts    # routeTree drift check (D5)
npx playwright install --with-deps chromium
npm run test:e2e                       # 8 mocked specs × (chromium + mobile-chromium) against build+preview
npm run test:e2e:smoke                 # 107-route crawl, manifest count enforced, error/not-found/console clean
npm audit --audit-level=moderate       # entire new dep tree incl. exact-pinned pre-1.0 packages

# frontend-e2e-live job (parallel; postgres+redis services, Go toolchain)
make migrate-up
# go run ./cmd/server (background, bootstrap admin env) ; wait for /health
BACKEND_URL=http://localhost:8000 npx playwright test --project=live   # 4 live specs

# repo-level jobs that must also pass
make verify                            # Go gate incl. new events/handler/agent tests
helm lint deploy/chart                 # + CI helm renders: dev values (frontend.enabled=true) AND production values
go test ./deploy/...                   # digest pins, chart contract, values schema, netpol renders
docker build frontend + Trivy HIGH,CRITICAL --ignore-unfixed --exit-code 1   # new nginx image
```

Plus, before merge (non-command): P6.3 k3d smoke checklist and P7.3 manual QA checklist executed and recorded.

---

## 7. Risk register

| # | Risk | Mitigation |
|---|---|---|
| R1 | **SSE backend scope creep** — "live-everything" pulls in ever more Go work (coalescing, replay, per-connection filters). | Scope is fenced to T1–T7 + informer expansion, all mechanical patterns with named files. Explicit non-goals: Last-Event-ID replay (invalidate-on-reconnect is the resume story), server coalescing (post-merge follow-up gated on the D7 0.1% soak threshold), payload-carrying events beyond the existing metrics/status shapes. The per-domain conversion rule keeps each slice small and revertable; if a domain's publisher stalls, its polls simply stay — the app remains correct. |
| R2 | **@wterm WASM under Vite** breaks terminals (prebundle/ESM/WASM serving). | P1.7 canary runs before any mass port under both dev and preview; escape hatch `optimizeDeps.exclude`; wasm stays a `public/` copy via the unchanged pre-scripts (no `?url` imports); nginx stock mime.types serves `application/wasm`. STOP condition if the canary fails both ways. |
| R3 | **TanStack DB maturity** (pre-1.0 API churn, subtle sync bugs). | Blast radius fenced to two collection types (pods, workload kinds) behind one factory file; exact version pins; plain Query everywhere else with written justifications; collection-sync unit tests port the old fold tests; if DB misbehaves late, the fallback is reverting P4.7 alone — `liveFallback` polling + invalidation keeps those pages fully functional. |
| R4 | **Big-bang drift vs main** — weeks-long branch conflicts with ongoing main work (backlog memory shows active P1/remediation streams). | Weekly rebase minimum; `routeTree.gen.ts` is regenerate-never-hand-merge; the adapter layer means most main-side page edits port mechanically (URL scheme and consumer imports unchanged); Phase 0 fixes land early and can be cherry-picked to main to shrink the diff; freeze main-side `frontend/src/app/**` feature work during Phases 2–3 by team agreement. |
| R5 | **E2E flake** — SPA first paint (no SSR), preview build time, live tier against a real server. | Preview keeps `reuseExistingServer` locally; config-level `expect.timeout` bump to 15s is the only sanctioned response (no sleeps); live tier is capped at 4 specs with `retries: 1` and a move-to-nightly (never delete) policy; route-smoke uses deterministic stubs and an empty, issue-linked-only console allowlist. |
| R6 | **Silent secret-form regression** — submit "works" but backend overwrites ciphertext with empty strings. | `form/secrets` unit tests cover both wire variants incl. camelize interaction; D14 isDirty semantics are verified with a dedicated test before any conversion; one e2e-mock body assertion on connector edit; per-form DoD requires identical submit bodies. |
| R7 | **Gate vacuousness** — code-health path filters silently stop matching after the layout move. | P2.5 anti-vacuous guard makes a 0-file gate a hard failure permanently; manifest count check does the same for the route crawl. |
| R8 | **RBAC/liveness correctness under the new stream** (missed events, revoked users, buffer drops). | `cluster_id` mandatory on cluster-scoped events (table-tested); SEC-R07 fail-closed drop preserved; 5-min binding re-snapshot (T5); buffer 64→256 + alertable drop metric; `refetchOnWindowFocus` + `staleTime: 30s` + reconnect-invalidate heal drops; `sys.ping` RBAC exemption prevents restricted-user watchdog storms. |
| R9 | **Webhook/SIEM bus-tap write amplification** — `internal/webhook/tap.go` and `internal/siem/bus_tap.go` create a delivery row per event matching an enabled subscription's glob filters; P4.6's informer expansion multiplies `cluster.k8s_changed` (already matched by existing `cluster.*` filters) and P4.5/P4.9 add ~16 new types a `*` filter matches. | P4.6 step 4: `sys.*` excluded from tap matching (tested); `cluster.k8s_changed` tap semantics unchanged (existing subscribers may depend on it) but delivery-row write rate on a broad-filter subscription is measured during the D7 24h soak with the expected delta recorded; unbounded amplification triggers the per-type tap-exclusion follow-up (Section 9). |

---

## 8. Rollback story

**Pre-merge (any time before the branch merges)**: rollback = do not merge. `main` still builds and ships the Next.js frontend; nothing in this plan touches `main` except the optional cherry-picks of P0.1–P0.3 (which are pure improvements, safe to keep). Abandoning mid-branch loses only branch work.

**Post-merge, image-level (first response)**: the frontend is an independently deployed static container with an unchanged name and port. Roll back by redeploying the last Next-based `astronomer-frontend` image tag (`helm upgrade --set frontend.image.tag=<previous>`). Caveats that make this safe but time-boxed: (a) the old image expects the chart env block and hostAliases that P6.2 removed — the old Next server ignores their absence (they were dead at runtime), so the old image runs fine on the new chart; (b) Go-side SSE changes are backward-compatible for the old client except framing (T1): the old client registers named-event listeners per `KNOWN_EVENT_TYPES` plus an `onmessage` handler that dispatches under the `'message'` key (`live-events.ts:194`) — which no subscriber listens to — so with T1 deployed the old client receives **zero** live events. Worse than "falls back to polling": the stream still connects and `es.onopen` sets status `'open'` (`live-events.ts` UX-04), so `liveAwareRefetchInterval` **stretches** the clusters-list/cluster-detail/nodes polls to `max(4×base, 60s)` (clusters list 30s → 120s staleness) and `useLiveClusterMetricsMerger` gets no metrics ticks at all — the open-but-mute stream actively suppresses the polling the rollback relies on. Non-liveAware `refetchInterval` pages poll normally. Therefore: **reverting T1 on the server is the default companion action for any rollback expected to outlast a few minutes**, not an optional extra; image-only rollback without the T1 revert is acceptable only as a very short bridge.

**Post-merge, commit-level**: revert the merge commit. The branch deliberately avoids destructive data migrations: localStorage keys/shapes are byte-compatible (P3), cookies unchanged, no API contract changes besides additive event types and the deleted (unused) registration SSE endpoint — reverting restores the old world cleanly. The `useDeleteCluster` interval and `use-resource-watch.ts` deletions come back with the revert.

---

## 9. Out of scope / deferred (explicit)

1. **Tailwind v4 + `@tailwindcss/vite`** — separate post-merge PR (`npx @tailwindcss/upgrade`, delete postcss/autoprefixer, class-rename sweep).
2. **Server-side event coalescing / per-connection filtering** — only if the drop metric exceeds the D7 threshold (0.1% over a 24h fleet soak).
3. **Per-route `document.title`** — static title is full parity (no page ever overrode the template); nicety later.
4. **`defaultPreload: 'intent'`**, `manualChunks` vendor tuning, router devtools — perf/DX experiments after measurement, not part of parity.
5. **httpGet probes** replacing tcpSocket for the frontend pod.
6. **F5 forms not converted at gate time** (D15) — filed as follow-up tickets at merge; S forms/ExtForm/helm-values-form/list editors are permanently out by policy.
7. **T8** pods-watch ticket kind `logs`→`events` unification — optional, only if trivial during P4.7.
8. **Toaster theme-awareness** (D11) and `disableTransitionOnChange` flicker freeze — only if QA surfaces them.
9. **Coalescing-independent tightening of the 5-min RBAC re-snapshot** — pending post-merge security review (D10).
10. **Visual pixel-diff infrastructure** — deliberately not built.

---

## Appendix A — Full route inventory checklist (107 pages)

Target file convention: `src/routes/<url>/index.tsx` (layouts: `route.tsx`; splats: `$.tsx`). Check off when ported AND green in the route-smoke crawl.

Auth + root (5):
- [ ] `/` → `routes/index.tsx` (redirect to /dashboard)
- [ ] `/auth/login` → `routes/auth/login/index.tsx` (P1.7 scaffold; returnTo consumption P2.4; TOTP form P5.4)
- [ ] `/auth/login/forgot-password`
- [ ] `/auth/login/reset-password` (validateSearch: token)
- [ ] `/auth/change-password`

Dashboard core (10):
- [ ] `/dashboard` (layout `routes/dashboard/route.tsx`: guard + chrome + SSE mount)
- [ ] `/dashboard/account/security` (MFA wizard, P5.4)
- [ ] `/dashboard/admin/users/[id]` (Promise-params rewrite)
- [ ] `/dashboard/agents`
- [ ] `/dashboard/audit`
- [ ] `/dashboard/audit/shell-sessions`
- [ ] `/dashboard/extensions`
- [ ] `/dashboard/monitoring`
- [ ] `/dashboard/search` (validateSearch: q; co-located `search-routing.test.ts` byte-identical)
- [ ] `/dashboard/tools`

Alerting (2):
- [ ] `/dashboard/alerting` (tab param; `-inhibition-hooks.ts`/`-inhibition-panel.tsx` prefix)
- [ ] `/dashboard/alerting/baselines`

ArgoCD (4):
- [ ] `/dashboard/argocd`
- [ ] `/dashboard/argocd/[instanceId]` (raw search + tab; D8 poll exceptions)
- [ ] `/dashboard/argocd/[instanceId]/applications/[appId]` (tab)
- [ ] `/dashboard/argocd/[instanceId]/applicationsets/new`

Backups (5):
- [ ] `/dashboard/backups` (tab)
- [ ] `/dashboard/backups/restores/[restoreId]`
- [ ] `/dashboard/backups/runs/[runId]`
- [ ] `/dashboard/backups/schedules/new`
- [ ] `/dashboard/backups/storage/new`

Catalog (1):
- [ ] `/dashboard/catalog` (next/image → `<img>`; raw search + tab)

Clusters (26):
- [ ] `/dashboard/clusters` (raw search)
- [ ] `/dashboard/clusters/register`
- [ ] `/dashboard/clusters/register/[id]/connect` (tab)
- [ ] `/dashboard/clusters/register/[id]/progress`
- [ ] `/dashboard/clusters/[id]` (+ NEW thin `route.tsx` carrying errorComponent)
- [ ] `/dashboard/clusters/[id]/adoption`
- [ ] `/dashboard/clusters/[id]/apps` (raw search)
- [ ] `/dashboard/clusters/[id]/control-plane-snapshots` (imports shared `snapshots-page.tsx`)
- [ ] `/dashboard/clusters/[id]/custom-resources/[[...slug]]` → paired `index.tsx` + `$.tsx` sharing `custom-resources-page.tsx`
- [ ] `/dashboard/clusters/[id]/gatekeeper` (`-hooks.ts` prefix; test moves)
- [ ] `/dashboard/clusters/[id]/image-scans`
- [ ] `/dashboard/clusters/[id]/network-access`
- [ ] `/dashboard/clusters/[id]/network-policies`
- [ ] `/dashboard/clusters/[id]/nodes/[nodeName]` (tab)
- [ ] `/dashboard/clusters/[id]/registries`
- [ ] `/dashboard/clusters/[id]/[resource]` (ranking test P2.2; allowlist stays in-component)
- [ ] `/dashboard/clusters/[id]/[resource]/[...path]` → `$resource/$.tsx`
- [ ] `/dashboard/clusters/[id]/resources`
- [ ] `/dashboard/clusters/[id]/service-mesh`
- [ ] `/dashboard/clusters/[id]/service-mesh/mtls`
- [ ] `/dashboard/clusters/[id]/shell` (wterm)
- [ ] `/dashboard/clusters/[id]/snapshots` (exports shared page component)
- [ ] `/dashboard/clusters/[id]/template` (monaco React.lazy)
- [ ] `/dashboard/clusters/[id]/tools`
- [ ] `/dashboard/clusters/[id]/workloads` (TanStack DB collections P4.7)
- [ ] `/dashboard/clusters/[id]/workloads/[kind]/[namespace]/[name]` (tab)

Cluster templates (4):
- [ ] `/dashboard/cluster-templates`
- [ ] `/dashboard/cluster-templates/new`
- [ ] `/dashboard/cluster-templates/[id]` (Promise-params)
- [ ] `/dashboard/cluster-templates/[id]/edit` (Promise-params)

Fleet (2):
- [ ] `/dashboard/fleet`
- [ ] `/dashboard/fleet/[id]` (Promise-params; terminal-aware poll wrap)

Logging (1):
- [ ] `/dashboard/logging` (tab)

Projects (8; layout `projects/$id/route.tsx`, Promise-params rewrite):
- [ ] `/dashboard/projects`
- [ ] `/dashboard/projects/[id]`
- [ ] `/dashboard/projects/[id]/catalogs`
- [ ] `/dashboard/projects/[id]/cloud-credentials`
- [ ] `/dashboard/projects/[id]/cloud-credentials/new`
- [ ] `/dashboard/projects/[id]/cloud-credentials/[credId]/edit`
- [ ] `/dashboard/projects/[id]/policy`
- [ ] `/dashboard/projects/[id]/quota`

RBAC / security (4):
- [ ] `/dashboard/rbac` (tab; co-located `rbac-routing.test.ts` byte-identical)
- [ ] `/dashboard/security` (tab)
- [ ] `/dashboard/security/scans/new`
- [ ] `/dashboard/security/scans/[scanId]`
- [ ] (audit pages counted above)

Settings (35):
- [ ] `/dashboard/settings`
- [ ] `/dashboard/settings/auth`
- [ ] `/dashboard/settings/auth/connectors/new`
- [ ] `/dashboard/settings/auth/connectors/[id]` (Promise-params)
- [ ] `/dashboard/settings/auth/install`
- [ ] `/dashboard/settings/auth/register-sso`
- [ ] `/dashboard/settings/auth/scim-tokens` (`-hooks.ts` prefix; test moves)
- [ ] `/dashboard/settings/auth/settings`
- [ ] `/dashboard/settings/backup-drill`
- [ ] `/dashboard/settings/cluster-groups`
- [ ] `/dashboard/settings/compliance`
- [ ] `/dashboard/settings/compliance/baselines`
- [ ] `/dashboard/settings/general` (tab)
- [ ] `/dashboard/settings/gitops`
- [ ] `/dashboard/settings/gitops/new`
- [ ] `/dashboard/settings/gitops/[id]` (Promise-params)
- [ ] `/dashboard/settings/group-mappings`
- [ ] `/dashboard/settings/native-rbac`
- [ ] `/dashboard/settings/network-policies`
- [ ] `/dashboard/settings/operations`
- [ ] `/dashboard/settings/platform`
- [ ] `/dashboard/settings/quotas`
- [ ] `/dashboard/settings/quotas/new`
- [ ] `/dashboard/settings/quotas/usage`
- [ ] `/dashboard/settings/quotas/[name]`
- [ ] `/dashboard/settings/read-audit`
- [ ] `/dashboard/settings/siem` (`-hooks.ts` prefix; test moves)
- [ ] `/dashboard/settings/smtp` (P5.2 pilot)
- [ ] `/dashboard/settings/templates`
- [ ] `/dashboard/settings/templates/[key]`
- [ ] `/dashboard/settings/vault`
- [ ] `/dashboard/settings/webhooks`
- [ ] `/dashboard/settings/webhooks/new`
- [ ] `/dashboard/settings/webhooks/[id]` (tab)
- [ ] `/dashboard/settings/widgets`

Count check: 5 + 10 + 2 + 4 + 5 + 1 + 26 + 4 + 2 + 1 + 8 + 4 + 35 = **107** total pages (`/dashboard` is counted once, inside Dashboard core); the route-smoke manifest expected count starts at 107.

---

## Appendix B — Polling sites to eliminate (→ `liveFallback` unless noted)

Shared hooks — `src/lib/hooks.ts` (31 sites): useClusters :77 liveAware(30s); useCluster :86 liveAware(15s); useClusterNodes :95 liveAware(30s); useClusterConditions :107 60s (**P4.9 — routed from existing `cluster.status_changed`/heartbeat**); useClusterConditionRemediation :119 30s (**P4.9 — same route**); useNodeDetail :128 30s; useClusterEvents :145 15s; useClusterPods :154 15s; useWorkloads :260 15s; useWorkload :269 10s; useWorkloadPods :278 10s; useClusterMetrics :515 60s; useClusterMetricsSummary :524 30s; useWorkloadMetrics :539 60s; useArgoApplications :558 30s; useActivityFeed :836 30s; useAlertRules :848 30s (**P4.9 `alerting.changed`**); useAlertEvents :899 15s (**P4.9**); useAlertSilences :972 30s (**P4.9**); useAnomalyBaselines :1001 30s (**P4.9**); useLoggingOperations :1088 5s; useLoggingOperation :1097 5s; useInstalledCharts :1540 30s (**P4.9 — Secret-kind route + `catalog_release.changed`**); useBackups :1678 15s; useClusterSecurityPolicies :1850 30s (**P4.9 `security_policy.changed`**); useSecurityScans :1901 15s (**P4.9 `security_scan.changed`**); useClusterToolsStatus :1937 30s; useToolOperation :1950 2s-until-terminal (terminal-wrap pattern); useGenericResources :2011 30s; useCISScans :2185 30s; useCISScan :2198 10s-until-terminal (terminal-wrap).

Pages/components (~51 sites): projects hooks quotaUsage :86 30s (**KEEP (P4.9) — computed aggregate, no write event**); image-scans page :94/:102/:132/:164/:171 30s + :183 progress 1.5–3s conditional (terminal-wrap); snapshots page :158/:168 30s, :175 60s, :1172 15s; fleet-target-table :24 5s-until-terminal; network-access :116 30s (**P4.9 `network_access.changed`**); cluster apps :176 30s (**P4.9 — Secret-kind route + `catalog_release.changed`**); service-mesh :375/:382 60s (**KEEP (P4.9) — cluster-side mesh truth, D8-shaped**); agents :21 30s, :43 15s; argocd list :39 30s; registries :70 30s; template binding :125 5s-while-pending; backups hooks :154 15s, :163/:174 10s; argocd app detail :79 30s, :88 15s, :100 5/20s conditional, **:254 manifests 30s KEEP (D8)**, **:295 history 60s KEEP (D8)**; argocd instance :161/:281/:286 30s, :291/:296/:301 60s, **:306 orphanReport 60s KEEP (D8)**, :313 15s (key moved P0.1), :318 30s, tabs :603 15s, :612/:795/:954/:1068 30s, :1262 60s, :1389 10s; siem hooks :31 10s; cluster detail :90 5min, :103 serviceMeshHeader 5min (**KEEP (P4.9)**), :633 60s, :1016 registration badge 5s; settings/operations :53 5s, :73/:79 10s; workloads page :187-211 ×5 watch-aware (→ DB collections P4.7); fleet list :47 15s; fleet detail :43 5s-until-terminal.

Manual `setInterval` (3): registration-timeline :82 5s (SSE-primary → stream-status-conditional); cluster-shell :69 5s session status (**KEEP** — WS-session scoped; :52 1s countdown is UI); `useDeleteCluster` hooks.ts:222 6×5s invalidation follow-up (**DELETE** — covered by `cluster.deleted` + `fleet_operation.changed`).

---

## Appendix C — Store and form inventories with migration targets

Stores (zustand → TanStack Store via `persistedStore` + `createStoreHook`, P3):

| Store | File (path unchanged) | Persist key / version | Partialize | Disposition |
|---|---|---|---|---|
| `useAuthStore` | `src/lib/store.ts` | `astronomer-auth` / v2 + migrate (strip legacy `token`) | `{user, isAuthenticated}` | Port 1:1 (Query-only rejected: sync RBAC gating, login-before-query, e2e seeds) |
| `useUIStore` | `src/lib/store.ts` | `astronomer-ui` | `{sidebarCollapsed}` | Port live surface only; DELETE dead `sidebarOpen`/`toggleSidebar`/`theme` |
| `useWindowManagerStore` | `src/lib/window-manager-store.ts` | `astronomer-window-manager` | `{height, minimized}` | Port verbatim (dedupe/LRU/close-selection/clampHeight); tests locked in P0.3 |

Forms — convert (M/L, ≈45–55 forms; phases P5.2–P5.6): smtp; credential-form (+wrappers); connector-form; template-form (+wrappers); login (+TOTP); change-password; reset-password; MFA wizard (3 step-forms); settings general, siem, platform, auth/settings, auth/install, vault, cluster-groups, gitops new/edit, group-mappings, native-rbac, network-policies, templates/[key], webhooks new/edit, widgets, scim-tokens dialog; clusters/[id] snapshots dialogs, registries, network-access, image-scans, service-mesh, template; nodes drain dialog; alerting (+inhibition-panel); rbac modals; catalog modals + app-install-modal; logging; security; backups storage/schedules/restore-modal; projects create/policy/catalogs; argocd 7-dialog suite + applicationsets/new; fleet create-operation dialog; admin/users/[id]; clusters/register.

Forms — never convert (stay useState/controlled by policy): confirm-dialog (type-to-confirm), ScaleDialog, create-namespace inline + the small `$resource`-page dialogs, forgot-password, register-sso, read-audit filters, compliance, quota S-pages and the remaining ~25 S forms; `ExtForm.tsx` (FormSpec interpreter); `helm-values-form.tsx` (controlled field, embeddable inside a TanStack Form); list editors `key-value-editor`, `role-editor`, `selector-builder`, `target-refs-editor`, `operation-spec-fields` (used as fields).

Pacer targets (P4.8 + P4.4): search page 250ms; cluster apps 200ms (new, comment-requested); data-table filters 200ms (7 inputs, one hook); central `pacedInvalidate` 400ms leading+trailing (replaces the hand-rolled watch debounce). Not debounced by design: `global-search.tsx`.

---

## Review log (2026-07-15)

All 15 critique findings were verified against the repo and survey files before application. Several findings were duplicates across critics; they are grouped below.

**Applied (12 distinct issues, 15 findings):**
1. **[blocker, executability] Phase 1 gate ordering** — P1.1 no longer removes `next`/`next-themes` (11 files import `next/*` until Phase 2; verified `src/lib/link.tsx` re-exports `next/link`). Removal + the `no-restricted-imports` `next/*` ban moved to P2.6. New "Phase-1 gate rule" preamble states full-tree green is expected after every Phase-1 task. (Chose option (a) of the suggested fixes.)
2. **[major, executability] P1.3/P1.5 before scaffold exists** — P1.1 gains step 5: stub `src/main.tsx` / `src/routes/__root.tsx` / `src/routes/index.tsx` + first committed `routeTree.gen.ts`, so `vite build` and the router plugin work from the first task. P1.7 and D19 reworded to "flesh out the stubs".
3. **[major ×2, completeness+correctness] T7 vs openapi gate** (two duplicate findings) — T7 now includes the `docs/openapi.yaml:5731` path removal, `make openapi-embed`, and `make verify` as its validation (verified: `openapi-coverage.mjs --check` hard-fails on extra operations; Makefile:94 diff-checks the embedded copy). Phase 6's "zero edits" claim amended to point at T7's mandatory spec/embed edits. P4.5 validation line now includes `make verify`.
4. **[major, completeness] Uncovered Appendix B domains** — new task **P4.9** (appended, no renumbering): converts alerting (`alerting.changed`), security policies/scans (`security_policy.changed`/`security_scan.changed`), network access (`network_access.changed`), installed charts (Helm-filtered Secret metadata informer added to P4.6 + `catalog_release.changed`), conditions/remediation (routed from existing status/heartbeat events); explicit KEEP for quota usage (computed aggregate) and service-mesh (+`serviceMeshHeader`) (cluster-side truth, D8-shaped). Appendix B rows annotated; P4.5 DoD delegates the full convert-or-KEEP check to P4.9. Verified: `internal/handler/{alerting,quotas,service_mesh,security}.go` exist and publish no bus events today.
5. **[major, executability] Missing pr-validation.yaml edits** — P7.1 gains step 6 (add `npm run test:e2e:smoke` step to the frontend job); P7.3 now specifies the `SMOKE_GALLERY=1` env + `actions/upload-artifact@v4` step.
6. **[minor, completeness] scrollRestoration** — P1.7 changed to `scrollRestoration: true` with recorded rationale; new **D25** documents the decision and adds a manual-QA line (P7.3 checklist covers it via D25).
7. **[minor, completeness] Webhook/SIEM tap amplification** — new P4.6 step 4 (`sys.*` excluded from tap matching; `cluster.k8s_changed` semantics unchanged but soak-measured) + new risk-register row **R9**. Verified `internal/webhook/tap.go` and `internal/siem/bus_tap.go` exist.
8. **[minor ×3, all critics] Appendix A arithmetic** (three duplicate findings) — headers corrected to Settings (35) and RBAC/security (4); count-check rewritten as a straight sum to 107 with the `/dashboard` fudge clause removed. Verified: 35 settings bullets, 4 RBAC bullets, sum = 107.
9. **[minor, correctness] Rollback story understates T1 degradation** — Section 8 amended: open-but-mute stream stretches liveAware polls to max(4×base, 60s) and starves the metrics merger; reverting T1 is now the default companion action. One factual correction to the finding: the old client DOES set an `onmessage` handler (`live-events.ts:194`), but it dispatches under the `'message'` key that no subscriber uses — the finding's conclusion stands unchanged.
10. **[minor, executability] P4.3 step 7 depends on P4.4 dispatcher** — P4.3 step 7 now states hooks temporarily wrap the raw EventTarget and are re-pointed in P4.4 with no signature change.
11. **[minor, executability] "merged" wording in conversion rule** — Phase 4 preamble and P4.5 step 5 now say "committed on the migration branch with its Go tests green".
12. **[minor, executability] P0.4 DoD unverifiable** — pinned list is now a committed file `frontend/docs/migration-pins.md`, checkable and the canonical source for P1.1/P4.7/P4.8 versions.

**Rejected: none.** Every finding verified as substantively correct (one contained a minor factual error, noted in item 9, that did not change the required fix).

Task count updated 41 → 42 (P4.9 added). Status set to REVIEWED (2026-07-15).
