# Plan: Make the Astronomer API & CLI Elite

Status: proposed · Owner: platform · Last updated: 2026-06-16

## Why

The engine is already elite — RBAC with caching, dual read/write audit,
per-surface rate limiting, API-token scopes, feature gates, security headers,
and ~90% handler test coverage (106 test files / 118 handlers). What lags is
**surface polish**: the public contract, error model, list semantics, and the
CLI. This plan closes that gap and adds CI gates so it stays closed.

We do **not** rewrite the working parts. Every task is additive or a
mechanical refactor behind unchanged behaviour, guarded by the existing tests.

## Definition of "elite" (measurable exit criteria)

- [x] **100% of non-internal routes are in the OpenAPI spec** (coverage tool
      reports 100.0%, 397/397; drift 0, missing 0). *Not yet enforced in CI —
      see A5.* Today: ~49 of 626 routes.
- [ ] **Every error response uses a code from a single typed catalog**, and
      every code is documented. Today: 260 distinct ad-hoc string literals.
- [x] **Every (bare-`{data}`) list endpoint returns pagination metadata**
      (`total`, `has_more`, `next_offset`) via `RespondList`. *Stable ordering
      (D4) and accurate COUNT-driven totals (D2) still partial — many endpoints
      use a running `len`-based total.* Today: limit/offset in, no metadata out.
- [x] **Request bodies are validated declaratively** with uniform 422s
      (`decodeAndValidate` rolled out broadly across handlers). *A documented
      set of risky cross-field/domain-coded validations was deliberately left on
      hand-rolled checks to preserve 400 codes — see C2 below.* Today:
      hand-rolled `if req.X == ""` per handler.
- [ ] **The CLI can drive every resource the API exposes**, with global
      `--output`, shell completion, and config management. Today: auth/cluster/
      k8s/docs only.
- [ ] **A generated, versioned SDK** (at least Go + TypeScript) ships from the
      spec. *PARTIAL: a generated Go SDK (`pkg/astroclient`, oapi-codegen v2.5.0)
      now ships and builds GREEN (H1); the TypeScript side is still only the
      generated **types** (`openapi.generated.ts`), not a full client (H2), and
      no CI drift gate regenerates either SDK (H3). Not yet versioned.*
- [ ] `routes.go` is split by domain; no single file > ~150 routes.

---

## Workstream A — API contract completeness (OpenAPI)

The spec is hand-maintained (`docs/openapi.yaml` → `scripts/generate-openapi-types.mjs`
→ `frontend/src/types/openapi.generated.ts`), so coverage drifts. Make the spec
authoritative and complete, then gate it.

- [ ] **A1.** Decide the source of truth: (a) annotate handlers and generate the
      spec (swaggo/`swag`), or (b) keep `openapi.yaml` hand-authored but add a
      **coverage checker** that walks the chi route tree and fails CI on any
      route missing from the spec. **Recommended: (b)** — keeps the curated
      spec, avoids annotation noise, and is a smaller change. (M)
- [x] **A2.** Build `scripts/openapi-coverage.mjs`: enumerate chi routes
      (expose `chi.Walk` via a `astro docs routes --json` debug command or a
      test that dumps the route table), diff against `openapi.yaml` paths,
      report missing/extra. (M)
- [x] **A3.** Backfill the ~577 undocumented paths in waves by domain
      (clusters, argocd, tools, auth, logging, monitoring, backup, rbac,
      settings…). Each wave: request/response schemas, error responses, auth +
      scope, examples. (L — the bulk of the work; parallelizable per domain)
      **DONE: coverage 89.9% (363/404) → 100.0% (397/397)** — drift 31 → 0,
      missing 41 → 0. The matcher was fixed to fold trailing catch-all
      wildcards (`/*` and `/{...path}`) to a single sentinel and to skip CONNECT
      ops; 2 genuinely new shell-session GET paths were added to the spec.
      *Caveat for full "elite": several `$ref`'d schemas remain permissive
      stubs rather than authoritative field shapes (full schema fidelity is the
      remaining quality gap).*
- [x] **A4.** Add shared components: the `{data: …}` envelope, the error
      envelope, the pagination envelope (see D), and common path params, as
      reusable `$ref`s so handlers don't redefine them. (S)
- [x] **A5.** CI gate: `openapi-coverage --check` + `generate-openapi-types --check`
      (already exists) both run on PR; drift fails. (S) **DONE** — new
      `.github/workflows/api-contract.yaml` gate runs both checks plus Go
      build/vet, API package tests, the F2 route-table golden, and the apierror
      catalog lint on every PR/push to main; each command fails the job. `make
      verify` mirrors it locally.
- [ ] **A6.** Publish the spec: keep the served Swagger UI, add a versioned
      `docs/openapi.yaml` artifact to releases, and a changelog of breaking
      changes. (S)

## Workstream B — Error model

260 distinct error-code string literals scattered across handlers: consistent in
spirit, but no single source of truth, untyped, undocumentable, typo-prone.

- [x] **B1.** Create `internal/handler/apierror` with a typed catalog:
      `const CodeNotFound = "not_found"`, etc., grouped and documented. Seed it
      from the existing top codes (`not_found` ×297, `invalid_id` ×264,
      `invalid_body` ×144, `validation_error` ×128, `list_error`, `db_error`,
      `create_error`, `update_error`, …). (M)
- [x] **B2.** Mechanical migration: replace literal codes in
      `RespondRequestError(...)` calls with the constants (codemod / gofmt
      rewrite). Behaviour-preserving; covered by handler tests. (M)
- [x] **B3.** Add a lint check (`go vet`-style or a small AST test) that fails
      if `RespondRequestError` is called with a string literal instead of a
      catalog constant. (S)
- [x] **B4.** Document the catalog (auto-generate `docs/error-codes.md` from the
      constants + doc comments) and reference codes from the OpenAPI error
      responses. (S) **DONE for the doc generator** — new
      `scripts/error-code-docs.mjs` parses `internal/handler/apierror/codes.go`
      and emits `docs/error-codes.md` (217/217 constants, 10 category sections,
      6 legacy aliases surfaced); `make error-codes` writes, `make
      error-codes-check` is a `--check` freshness guard. *Caveat: the generated
      doc is not yet cross-referenced from the OpenAPI error-response schemas,
      and `error-codes-check` is not yet wired into a CI workflow.*
- [ ] **B5.** Audit for over-fragmentation: collapse near-duplicates
      (`list_error`/`db_error` semantics) where they leak storage details to
      clients; map internal failures to a small stable public set. (S)

## Workstream C — Request validation

Validation is hand-rolled per handler (`if req.Name == "" { … }`). Make it
declarative and uniform.

- [x] **C1.** Adopt `go-playground/validator` (or equivalent). Add `validate:`
      tags to request structs; one helper `decodeAndValidate[T](r)` that returns
      a uniform 422 with field-level details. (M)
- [x] **C2.** Migrate handlers to `decodeAndValidate`, deleting the bespoke
      `if`-ladders. Start with the highest-traffic mutating endpoints
      (clusters, auth/tokens, projects, settings). (L — broad but mechanical)
      **DONE for the safe majority** across 5 shards (~40+ creation handlers
      migrated). A documented set was deliberately LEFT on hand-rolled checks to
      preserve domain-specific 400 codes / cross-field & trim-then-check logic
      that struct tags can't express (auth login/refresh, chart-rating stars,
      cron/timezone parsing, group-mappings/quota/dex/vault triples, etc.).
      Note: migrated paths shift missing-required-field from 400 → uniform 422
      `validation_error` (the intended contract; codes preserved).
- [x] **C3.** Standardize the 422 body: `{error: {code: "validation_error",
      fields: [{field, rule, message}]}}`; document it once in OpenAPI. (S)
      `decodeAndValidate` emits exactly this shape; the `ErrorEnvelope`
      component is in the spec.
- [x] **C4.** Centralize shared rules (RFC-1123 names already in
      `validClusterName`, UUIDs, durations) as reusable validators. (S)
      Custom `rfc1123` rule registered; UUID/email/required handled via
      validator built-in tags.

## Workstream D — Pagination & list semantics

76 handlers take limit/offset but return no metadata, so clients can't tell when
they've reached the end.

- [x] **D1.** Define a list envelope: `{data: [...], pagination: {total,
      limit, offset, has_more, next_offset}}`; add `RespondList(w, items,
      page)`. (S)
- [ ] **D2.** Thread `total` from the queries (most sqlc list queries need a
      companion `COUNT(*)`; add where missing). (M) **PARTIAL** — endpoints with
      an existing COUNT (alerting channels/rules/silences, fleet ops, anomaly
      unscoped, smtp messages…) drive a real `Total`; the rest use a running
      `len`-based total carrying `// TODO(total): add Count<X>` markers. A fix
      pass added `NewPaginationFromPage` so truly DB-paginated endpoints
      (ListChartVersions, ListChartsByRepository, tools.ListOperations) compute
      `has_more`/`next_offset` correctly from page fullness rather than the old
      always-false `offset+len < len`. Full COUNT backfill remains.
- [x] **D3.** Migrate list handlers to `RespondList`. Keep `{data: [...]}` shape
      backward-compatible by nesting, or version the change (see E2). (M)
      **DONE** — all bare-`{data: [...]}` list endpoints across 5 shards
      migrated to `RespondList` (data array unchanged; `pagination` added).
      Non-bare envelopes (`{items, total}`, render contracts, log streams) were
      intentionally left to preserve their wire contract.
- [ ] **D4.** Add stable default ordering + `sort`/`order` params where listing
      is non-deterministic (prevents page tearing). (M)
- [ ] **D5.** Optional: cursor (keyset) pagination for the big tables
      (audit_logs, kubectl_session_commands) where offset is O(n). (M)

## Workstream E — API consistency & polish

- [x] **E1.** Standard rate-limit headers (`RateLimit-Limit`,
      `RateLimit-Remaining`, `RateLimit-Reset`) on the existing limiters. (S)
      Both the API limiter (token-bucket) and login limiter (fixed-window) now
      emit all three on success and 429 paths.
- [x] **E2.** Versioning & deprecation policy: document the `/api/v1` contract,
      add a `Deprecation`/`Sunset` header convention, and a written rule for
      what constitutes a breaking change. (S) New `docs/api-versioning.md`
      (RFC 8594 Deprecation/Sunset/Link, breaking-change rules, RateLimit-* and
      two-layer Idempotency semantics).
- [x] **E3.** Idempotency keys on mutating POSTs that automation retries
      (cluster create, token create, tool install): accept `Idempotency-Key`,
      dedupe within a TTL. Lets agents/CI retry safely. (M) New in-memory
      `idempotency.go` middleware (caches status/headers/body, replays with
      `Idempotent-Replayed: true`, collapses concurrent retries; -race clean),
      wired onto the resources + workloads/nodes mutation groups. *Currently
      scoped to those two groups — broader wiring noted as a followup.*
- [x] **E4.** Uniform `Location` headers + `201` bodies on resource creation.
      (S) ~38 creation handlers across 5 shards now set `Location` to the
      canonical resource path while keeping their 201 body. *Async 202 creators
      intentionally excluded.*
- [ ] **E5.** Consistent timestamp (RFC3339 UTC) and ID (UUID string) encoding
      audited across responses. (S)

## Workstream F — Routing organization

`routes.go` carries all 626 routes in one file — readable but unwieldy.

- [x] **F1.** Split into `routes_<domain>.go` (clusters, argocd, auth, tools,
      logging, monitoring, backup, rbac, settings, platform), each registering
      on a passed `chi.Router`. Pure move; behaviour identical. (M) — verified by
      the F2 golden (`TestRouteTableMatchesGolden`) and `TestRouteDumpCanBeGenerated`,
      both PASS; the route table is byte-identical after the split.
- [x] **F2.** Add a route-table test that snapshots the full method+path set so
      the split provably changes nothing. (S)

## Workstream G — CLI to parity + ergonomics

`cmd/astro` (Cobra) covers auth/cluster/k8s/docs with a per-command `--json` and
config at `~/.config/astronomer/config.yaml`. Make it cover the surface and feel
first-class.

- [x] **G1.** Global `--output table|json|yaml` (replace per-command `--json`);
      a shared renderer so every command supports all three. (M)
- [ ] **G2.** Generate command groups from the OpenAPI spec (or a thin
      generated client) so the CLI tracks the API: `astro tools`, `astro argocd`,
      `astro projects`, `astro rbac`, `astro settings`, `astro backup`,
      `astro logs`, `astro monitoring`, `astro audit`. (L)
- [x] **G3.** `astro completion bash|zsh|fish|powershell` (Cobra built-in, wire
      it up) + docs. (S)
- [x] **G4.** `astro config` subcommand (get/set/use-context) for multiple
      servers/profiles; keep the 0600 token file. (S)
- [x] **G5.** API-token auth in the CLI (`astro auth token` to mint via the new
      `/auth/tokens/` flow; `--token`/`ASTRO_API_TOKEN` env) so it matches the
      testbed fixture. (S)
- [ ] **G6.** Non-zero exit codes mapped from the error catalog; `--debug` for
      request/response tracing; respect `NO_COLOR`. (S)
- [ ] **G7.** Man pages / `astro docs` from Cobra; ship completions in the
      release tarball. (S)

## Workstream H — SDKs (from the spec)

Once A is complete, the spec generates clients for free.

- [x] **H1.** Generate a typed Go SDK (`oapi-codegen`) published as
      `pkg/astroclient` and consumed by `cmd/astro` (dogfood it). (M) **DONE for
      generation** — `pkg/astroclient` generated from `docs/openapi.yaml` via
      oapi-codegen v2.5.0 (pinned, models+client, std net/http; no server
      stubs). 414 operations, 901 types; `go build ./pkg/...` and `go build
      ./...` both GREEN, `go mod tidy` idempotent. Wired via a `make sdk` target
      + `//go:generate` in `pkg/astroclient/doc.go`. Collision/dup-field issues
      fixed via an OpenAPI Overlay (`oapi-codegen.overlay.yaml`) so
      `docs/openapi.yaml` is untouched. *Caveat: `cmd/astro` does NOT yet consume
      it — the dogfood step is deferred to G2 (needs a NewClient wrapper that
      injects the Bearer-auth RequestEditorFn).*
- [ ] **H2.** Keep the existing generated TS types; optionally emit a full TS
      client for external integrators. (S)
- [ ] **H3.** Version SDKs with the API; CI regenerates and fails on drift. (S)
      *Not done — the `api-contract.yaml` gate does not regenerate or diff
      `pkg/astroclient`; an `sdk-check` target mirroring
      `scripts/check-sqlc-generated.sh` is still needed.*

## Workstream I — CI gates (lock it in)

- [ ] **I1.** PR gates: OpenAPI coverage check (A5), error-code lint (B3),
      generated-types/SDK drift, route-table snapshot (F2). (S)
- [ ] **I2.** Coverage floor for `internal/handler` (keep ≥ current ~90%). (S)
- [ ] **I3.** A contract test that boots the server and asserts every spec path
      responds (auth/RBAC-aware) — catches spec/impl drift the static check
      can't. (M)
- [ ] **I4.** Spectral (or similar) lint on `openapi.yaml` for style
      consistency (naming, required fields, examples present). (S)

---

## Sequencing

1. **Foundations first (unblock everything):** B1–B2 (error catalog),
   D1 (list envelope), C1 (validation helper), A4 (shared components). These are
   the primitives the rest reference.
2. **Make the contract real:** A2 (coverage tool) → A3 (backfill by domain, in
   parallel) → A5/I1 (gate). Do D2–D3 and C2 alongside each domain wave so docs,
   validation, and pagination land together per domain.
3. **Polish:** E (headers, idempotency, versioning), F (route split).
4. **Surface:** G (CLI parity) and H (SDK) — both ride the now-complete spec, so
   they're cheap once A is done.
5. **Lock:** I gates throughout, not at the end.

The big rock is **A3 + C2 + D3** (per-domain backfill of docs, validation, and
pagination). It's parallelizable across ~10 domains and is the bulk of the
effort; everything else is small-to-medium.

## Non-goals / preserve

- Don't touch the auth/RBAC/audit/rate-limit/feature-gate middleware — it's the
  elite part already.
- Don't drop below current handler test coverage.
- Keep `/api/v1` backward-compatible; additive envelopes or a clean `/v2`, never
  a silent breaking change.

---

## Run log (undefined)

A single parallel run that landed the foundation primitives plus CLI ergonomics,
establishing the worked-example pattern for each bulk workstream. Build is GREEN
across the tree; tests GREEN after the fix pass.

### Shipped this run

**B1 (error catalog) — worked example, NOT mass-migrated.**
- New: `internal/handler/apierror/codes.go` — `type Code = string` alias plus
  seeded constants grouped by HTTP-status family (validation/400, not-found/404,
  conflict/409, auth 401/403, server/500), each documented.
- Migrated one worked example: `internal/handler/clusters.go` Create + Get
  handlers reference `apierror.*` constants.
- New: `internal/handler/apierror_catalog_test.go` —
  `TestApierrorCatalogCoverage` regex-scans for bare-literal 4th args; currently
  `t.Skip()`'d with a `TODO(apierror-codemod)` so CI stays green until B2 lands.

**C1 (declarative validation) — helper + 2 worked examples.**
- New: `internal/handler/validate.go` — generic `decodeAndValidate[T]` emitting
  400 `invalid_body` on decode error and a uniform 422
  `{error:{code:"validation_error", fields:[{field,rule,message}], request_id}}`
  on validation failure. Uses `go-playground/validator/v10` (v10.30.3, added to
  `go.mod`/`go.sum`), json-tag field names, and a custom `rfc1123` rule.
- Migrated `clusters.go` (CreateClusterRequest) and `cluster_groups.go`
  (MoveClustersRequest) off hand-rolled if-ladders.

**D1 (pagination envelope) — helper + 2 worked examples.**
- New: `internal/handler/response_list.go` — `Pagination` struct
  (`total/limit/offset/has_more/next_offset`), `NewPagination(...)` constructor,
  and `RespondList(w, items, page)` emitting `{"data":[...],"pagination":{...}}`.
  The `data` array is unchanged, so the existing shape stays backward-compatible.
- Migrated `group_mappings.go` List (real COUNT) and `cluster_groups.go` List
  (bare-slice). Note: see fix-pass below — `group_mappings.go` List was
  subsequently reverted to `RespondPaginated` to match the sibling DRF
  `count` envelope the tests assert.

**A2 (OpenAPI coverage tool) + A4 (shared components).**
- New: `internal/server/route_dump_test.go` —
  `TestRouteDumpCanBeGenerated` walks the wired router via `chi.Walk` and writes
  `docs/routes.json` (406 normalized entries) when `DUMP_ROUTES=1`; otherwise
  asserts >=100 entries.
- New: `scripts/openapi-coverage.mjs` — diffs `docs/routes.json` vs
  `docs/openapi.yaml` paths; `--check` exits non-zero on genuine drift, with a
  `KNOWN_NIL_GATED` allowlist for ~17 routes the test router can't expose
  (nil handler deps). **Measured coverage: 9.2% (37 of 404 router operations
  documented).** No paths were backfilled (A3 deferred).
- A4: added `DataEnvelope`, `ErrorEnvelope`, `PaginatedEnvelope` reusable
  components to `docs/openapi.yaml`; regenerated
  `frontend/src/types/openapi.generated.ts`.

**F2 (route snapshot test) — routes.go NOT split.**
- New: `internal/server/route_table_test.go` +
  `internal/server/testdata/route_table.golden` (404 sorted method+path lines).
  `TestRouteTableMatchesGolden` proves a future split is behaviour-preserving;
  `UPDATE_ROUTE_TABLE=1` regenerates the golden.

**G1/G3/G4/G5 (CLI ergonomics) — `cmd/astro` only, no new deps.**
- New: `cmd/astro/output.go` — persistent `-o/--output table|json|yaml`,
  `--json` kept as a deprecated alias, shared `render(...)` helper.
- New: `cmd/astro/completion.go` — `astro completion bash|zsh|fish|powershell`.
- New: `cmd/astro/config_cmd.go` — `astro config get|set|current` over
  `~/.config/astronomer/config.yaml` (0600 preserved); tokens read-only/redacted.
- Edited: `cmd/astro/main.go`, `cmd/astro/auth.go` (bearer override via
  `--token`/`$ASTRO_API_TOKEN`), `cmd/astro/cluster.go`.

### Fix pass (post-verification)

Verification found build GREEN / vet GREEN but 3 failing handler tests; all were
fixed and the tree is now GREEN:
- `internal/astrocli/config.go` `SaveConfig` now `os.Chmod(path, 0o600)` on every
  write (was only on create — pre-existing config files kept loose perms,
  world-readable JWT).
- `internal/handler/group_mappings.go` — client-facing `err.Error()` replaced
  with static messages (convention match, no DB internals leak); List switched
  back to `RespondPaginated` to restore the `count` envelope.
- `logging_render_test.go` — stale "unsupported output" example changed from
  `splunk` (now supported) to `kafka`.
- `logging_operation_test.go` — relaxed to locate the per-target ConfigMap POST
  rather than asserting every POST, tolerating the aggregate
  `astronomer-fluent-bit-config` refresh.

### Build / test status

- `go build ./...` and `go vet ./...` — GREEN (exit 0).
- `go test ./internal/handler/ ./internal/server/ ./internal/auth/ ./cmd/astro/`
  — GREEN after the fix pass (cmd/astro has no test files).
- `node scripts/openapi-coverage.mjs --check` — exit 0; coverage 9.2% (37/404).
- OpenAPI type-gen `--check` — GREEN.

### DEFERRED bulk work (pattern now established per workstream)

The expensive, mechanical bulk of each workstream was intentionally NOT done
this run; a single worked example/helper establishes the pattern for each:
- **B2** — mass codemod of ~2,000 `RespondRequestError` call sites to
  `apierror.*` constants, then un-skip `TestApierrorCatalogCoverage`. (Enforcement
  regex is single-line only; the codemod needs a slurp-mode parser for multi-line
  calls.)
- **A3** — backfill the ~367 undocumented paths into `docs/openapi.yaml` by
  domain (coverage 9.2% → 100%) using the new envelope `$ref`s.
- **C2** — migrate remaining handlers to `decodeAndValidate` (many have coupled
  cross-field/UUID-parse logic needing struct-level validators).
- **D3** — migrate the remaining ~26 `RespondPaginated` and ad-hoc list handlers
  to `RespondList`; add companion `COUNT(*)` queries where missing (D2).
- **E** — idempotency keys, rate-limit headers, Location/201 bodies (E1–E5).
- **F1** — split `routes.go` into `routes_<domain>.go` (guarded by F2 golden).
- **G2** — generate CLI command groups from the spec
  (tools/argocd/projects/rbac/settings/backup/logs/monitoring/audit).
- **H** — generated Go + TS SDKs from the spec (H1–H3).

---

## Run log (undefined) — bulk pass

The bulk/mechanical follow-through on the foundations the previous run seeded:
the error-code codemod (B2/B3), the OpenAPI domain backfill (A3, partial), and
the `routes.go` split (F1). Build GREEN, tests GREEN after the fix pass, route
snapshot held. Nothing was reverted.

### B2/B3 — error-code codemod (DONE, verified)

- `internal/handler/apierror/codes.go` expanded **17 → 217 constants** (216 from
  the codemod + `ScopeDenied` restored in the fix pass), grouped by HTTP-status
  family with doc comments and "Collapses legacy literal(s)" notes for the 6
  near-duplicate merges (AggregateError, ComponentInvalid, InvalidSince,
  LookupError, RetryError, TransitionError).
- Deterministic `gofmt -r` codemod across all non-test handler files:
  **2020 of 2024 `RespondRequestError` call sites** now use `apierror.*`
  selectors; **0 bare-literal code args remain**. The 4 untouched sites are
  genuinely dynamic (a `code` variable in `response.go`/`authorization.go`,
  `handlerErr.code` in `agent_fleet.go`) — outside the lint by design.
- `goimports -w` added the `apierror` import to the 73 referencing files.
- **B3:** `TestApierrorCatalogCoverage` un-skipped and rewritten to parse each
  file with `go/ast` and fail on any `RespondRequestError` whose 4th arg is a
  bare string literal (multi-line-safe; replaces the old single-line regex).
  PASSES; verified to fail on an injected literal.
- Test assertions updated for intentional canonicalization wire-value changes
  (auth_test.go, cluster_groups_test.go, kubectl_shell_test.go).
- Fix pass corrected 3 semantic remaps **within the catalog** (no legacy-literal
  revert): `stream_tickets.go` restored `scope_denied` (added `ScopeDenied`
  constant — a missing-token-scope denial is distinct from an RBAC forbidden);
  `auth.go:473` 400 "Email is required" remapped to `ValidationError` (was wrongly
  collapsed to `AuthenticationRequired`); `argocd.go:844` 500 remapped to
  `InternalError` (was wrongly carrying `Forbidden`).

### A3 — OpenAPI domain backfill (PARTIAL: 9.2% → 89.9%)

- Merged **all 12 domain fragments** into `docs/openapi.yaml` (path-level deep
  merge, method-level last-wins, schemas de-duped by name). One dup operation
  collision resolved last-wins: `GET /api/v1/clusters/{id}/v2/pods`.
- **Coverage before: 9.2% (37/404) → after: 89.9% (363/404)** — +220 paths,
  +359 operations, +82 component schemas (0 duplicate schemas).
- Fix pass added 30 missing `$ref`'d schemas as permissive stubs (dangling refs
  30 → 0) and hardened `scripts/generate-openapi-types.mjs` with
  `validateSchemaRefs` that walks the **entire** spec (not just
  `components.schemas`) and exits 1 on any dangling `#/components/schemas/<name>`
  ref — closing the false-validity gap where dangling path refs were invisible
  to `--check`.
- Regenerated `frontend/src/types/openapi.generated.ts`; `--check` exit 0.
- **Left unchecked:** 41 router operations still undocumented; 31 wildcard
  `{path}` passthrough drift entries (tunnel/proxy/argocd) to reconcile to reach
  100%.

### F1 — routes.go split (DONE, verified)

- Routes split out of `internal/server/routes.go` into per-domain
  `internal/server/routes_*.go` (e.g. `routes_clusters.go`). The registration
  wizard routes now live in `routes_clusters.go` with correct
  writeClusters + VerbUpdate / VerbRead gating.
- `TestRegistrationWizard_RequiresClustersUpdateOnWrites` updated to glob+concat
  all `routes*.go` (was hardcoded to `routes.go`); its gating assertions hold.
- **F2 golden held:** `TestRouteTableMatchesGolden` and
  `TestRouteDumpCanBeGenerated` both PASS — route table byte-identical after the
  split.

### Build / test / snapshot status (verified)

- `go build ./...` — GREEN (exit 0). `go vet ./...` — GREEN.
- `go test ./internal/...` — GREEN after the fix pass (the 6 verify-stage
  failures — registration-wizard routes ×4, exec/shell `scope_denied` ×2 — are
  all resolved).
- `TestApierrorCatalogCoverage` — PASS. `TestLogin` (incl. updated validation
  cases) — PASS.
- `node scripts/generate-openapi-types.mjs --check` — exit 0 (types in sync).
- `node scripts/openapi-coverage.mjs` — exit 0; coverage 89.9% (363/404).
- Route snapshot — HELD.
- Coverage of statements: `internal/handler` 42.0%, `internal/server` 44.2%
  (`internal/handler/apierror` has no test files).

### Still deferred

- **A3 remainder** — 41 undocumented router ops + 31 wildcard `{path}` drift
  entries to reach 100%; replace the permissive schema stubs with authoritative
  field shapes.
- **A5/A6** — wire `openapi-coverage --check` into the PR CI gate; publish the
  versioned spec artifact.
- **C2** — mass migration of remaining handlers to `decodeAndValidate`.
- **D3** — remaining `RespondPaginated`/ad-hoc list handlers to `RespondList`
  (+ D2 companion `COUNT(*)` queries).
- **E** — idempotency keys, rate-limit headers, Location/201 bodies (E1–E5).
- **G2** — generate CLI command groups from the spec.
- **H** — generated Go + TS SDKs from the spec (H1–H3).

---

## Run log (undefined) — final pass

The closing parallel run that took the contract to 100% and landed the
remaining API polish (validation, pagination, rate-limit headers, idempotency,
Location/201). Six concurrent tracks — OpenAPI coverage, five handler shards,
and middleware — integrated, verified, and review-fixed. Build GREEN, full
suite GREEN, route-table golden HELD, coverage 100%. Nothing was reverted.

### A3 / A4 — OpenAPI contract to 100% (DONE, verified)

- **Coverage 89.9% (363/404) → 100.0% (397/397).** Drift 31 → 0, missing
  41 → 0, nil-gated 17 → 16.
- The gap was almost entirely a *matcher* problem, not missing docs:
  `scripts/openapi-coverage.mjs` `normalizePath` now folds a trailing catch-all
  to a single `/*` sentinel — name-gated to router literal `/*` or a spec
  template whose param ends in `path` (`/{path}`, `/{...path}`), so ordinary
  trailing ids (`/{session_id}`, `/shell/sessions/{id}`) stay real slots and
  remain genuinely missing if undocumented. CONNECT rows are dropped from both
  numerator and denominator (no OpenAPI connect operation). The redundant
  `KNOWN_NIL_GATED` k8s wildcard entry was retired (17 → 16).
- Only **2 genuinely new paths** were added to `docs/openapi.yaml` (shell-session
  `GET .../shell/sessions/{id}` and `.../commands`); 5 of 6 domain fragments'
  path keys were already present verbatim and were skipped to avoid duplicate
  keys. The fix pass also deleted one redundant duplicate
  `/api/v1/clusters/{id}/k8s/{path}` GET-only item that the normalizer had been
  silently folding onto the full `{cluster_id}` item.
- `generate-openapi-types.mjs --check` exits 0 (generated TS types in sync; the
  two new paths reuse existing schemas, so no structural change).

### C2 / C3 / C4 — declarative validation rollout (DONE for the safe set)

- ~40+ creation handlers across 5 shards migrated to `decodeAndValidate` with
  `validate:"required"` (and `email`) tags, deleting hand-rolled if-ladders:
  alerting, logging, control-plane, backups, projects, resources, catalog,
  rbac (creates only), security, totp, tools, cluster-templates, clusters,
  smtp test, etc.
- Migrated paths uniformly return **422 `validation_error`** with the
  `{error:{code,fields:[{field,rule,message}]}}` body (C3). Update handlers were
  left on raw decode to preserve partial-update (empty-field-allowed) semantics.
- **Deliberately left hand-rolled** (documented, build-safe) where migrating
  would flip domain 400 codes or break trim/cross-field logic: auth
  login/refresh/change-password, chart-rating stars, maintenance cron+IANA tz,
  group-mappings/quota/dex/vault scope triples, trim-then-check creators.

### D2 / D3 — pagination envelope rollout (D3 DONE, D2 PARTIAL)

- All bare-`{data:[...]}` list endpoints migrated to `RespondList`; `data` is
  byte-for-byte unchanged, `pagination` added — backward compatible.
- `Total` is real where a COUNT exists (alerting, fleet ops, anomaly unscoped,
  smtp); elsewhere it's a running `len` with `// TODO(total): add Count<X>`.
- Review fix: added `NewPaginationFromPage` so the three genuinely
  LIMIT/OFFSET-paginated endpoints (catalog ListChartVersions,
  project_catalogs ListChartsByRepository, tools ListOperations) compute
  `has_more`/`next_offset` from page fullness — fixing the latent
  `offset+len < len` (always-false) bug that silently stopped pagination after
  page 1. Truly-unpaginated full-list endpoints were left at `has_more=false`.

### E1 / E2 / E3 / E4 — API polish (DONE)

- **E1:** `RateLimit-Limit/-Remaining/-Reset` on the API limiter
  (limit=burst, remaining=whole tokens, reset=refill seconds) and login limiter
  (fixed-window), on both success and 429 paths.
- **E2:** `docs/api-versioning.md` — `/api/v1` contract, breaking-change rules,
  RFC 8594 Deprecation/Sunset/Link convention, RateLimit-* + two-layer
  Idempotency semantics.
- **E3:** new in-memory `idempotency.go` middleware (TTL cache of
  status/headers/body, `Idempotent-Replayed: true` on retry, concurrent-retry
  collapse via done-channel, panic abandonment; -race clean), wired onto the
  resources + workloads/nodes mutation groups (NOT on the DB-backed
  operation-idempotency path, so no double dedup).
- **E4:** ~38 creation handlers set `Location` to the canonical resource path
  and keep their 201 body; async 202 creators excluded.

### Build / test / snapshot status (verified this pass)

- `go build ./...` — exit 0. `go vet ./...` — exit 0.
- `go test ./internal/handler/ ./internal/server/ ./internal/auth/ -count=1` —
  GREEN (handler 4.7s, server 1.4s, auth 0.9s).
- `internal/server/middleware/` — GREEN (passes under -race).
- `node scripts/openapi-coverage.mjs` — exit 0; **100.0% (397/397)**, 0 missing,
  0 extra, 16 nil-gated.
- `node scripts/generate-openapi-types.mjs --check` — exit 0.
- `TestRouteTableMatchesGolden` — PASS (golden HELD; middleware is `r.Use`, not
  new routes).
- Reverted: nothing. Two review findings (pagination `has_more` bug, duplicate
  OpenAPI path) were fixed forward, not reverted.

### What remains for true "elite"

- **A5** — wire `openapi-coverage --check` (+ existing `generate-openapi-types
  --check`) into `.github/workflows/pr-validation.yaml`. Coverage is at 100% and
  stable, so the gate can now be turned on to catch regressions. *(not done — no
  workflow currently references either check.)*
- **A6** — publish the versioned spec artifact + breaking-change changelog to
  releases.
- **Full schema fidelity** — replace the permissive `$ref` stub schemas with
  authoritative field shapes (the remaining OpenAPI quality gap behind the 100%
  path coverage).
- **D2 (COUNT backfill)** — add the `// TODO(total)` companion `COUNT(*)`
  queries so `Total`/`HasMore`/`NextOffset` are accurate on the `len`-based
  endpoints; **D4** — stable default ordering + `sort`/`order` params;
  **D5** — cursor/keyset pagination for the big tables (audit_logs,
  kubectl_session_commands).
- **C2 remainder** — the deliberately-skipped domain-coded/cross-field
  validators, once a 400→422 flip is signed off per endpoint.
- **E3 breadth** — extend the idempotency middleware beyond resources/workloads
  to other mutating groups (after confirming no overlap with DB-backed
  operation idempotency).
- **G2** — generated CLI command groups from the spec.
- **H** — generated, versioned Go + TS SDKs (H1–H3) and **I** CI gates.

---

## Run log (undefined) — tail pass

The tail pass closing the surfacing/lock-in gaps: the CI contract gate (A5),
the generated Go SDK (H1), and the auto-generated error-code reference (B4).
Three concurrent tracks integrated; the full authoritative verification suite
was re-run and is GREEN.

### A5 — API contract CI gate (DONE, verified)

- New `.github/workflows/api-contract.yaml` — single `api-contract` job,
  one named step per command, triggered on `pull_request`/`push` to main and
  `workflow_dispatch`. Follows repo conventions (checkout@v4, setup-go@v5 via
  `go-version-file: go.mod`, setup-node@v4 node 22 + npm cache, ubuntu-24.04,
  `permissions: contents: read`). `npm ci` (working-directory `frontend`) runs
  before the node scripts because they resolve `js-yaml` from frontend deps.
- Steps in order: `go build ./...`; `go vet ./...`;
  `go test ./internal/handler/ ./internal/server/ ./internal/auth/
  ./internal/server/middleware/`; `node scripts/openapi-coverage.mjs --check`;
  `node scripts/generate-openapi-types.mjs --check`; route-table golden
  (`TestRouteTableMatchesGolden`); apierror catalog lint
  (`TestApierrorCatalogCoverage`).
- Added a `make verify` target mirroring the gate and documented it in
  `.github/workflows/README.md`.

### H1 — generated Go SDK (DONE for generation, verified)

- `pkg/astroclient` generated from `docs/openapi.yaml` via oapi-codegen v2.5.0
  (pinned; models+client, std net/http; no server stubs — the server is the
  hand-written go-chi router). 414 operations, 901 types (31 are permissive
  `map[string]interface{}` stubs, mirroring the spec's `additionalProperties`
  schemas). `go build ./pkg/...` and `go build ./...` GREEN; `go vet
  ./pkg/astroclient/` clean; `go mod tidy` idempotent (pulled
  `github.com/oapi-codegen/runtime`).
- Name collisions and dup camelCase/snake_case fields fixed via an OpenAPI
  Overlay (`oapi-codegen.overlay.yaml`) — `docs/openapi.yaml` untouched, JSON
  wire tags preserved. Wired via `make sdk` + `//go:generate` in
  `pkg/astroclient/doc.go`.
- `cmd/astro` does NOT yet consume the SDK (dogfood deferred to G2).

### B4 — error-code reference generator (DONE for the doc, verified)

- New `scripts/error-code-docs.mjs` parses
  `internal/handler/apierror/codes.go` and emits `docs/error-codes.md`
  (GENERATED banner, intro, per-category Constant/Wire/HTTP/Description tables,
  legacy-alias table). 217/217 constants documented, 10 category sections,
  6 legacy aliases; literal wire values emitted verbatim (e.g.
  `InvalidClusterID -> invalid_cluster`, `PersistError -> persist_failed`).
- `make error-codes` writes; `make error-codes-check` (`--check`) is a freshness
  guard (exit 0 current / 1 stale). Not yet cross-referenced from the OpenAPI
  error schemas, and `error-codes-check` is not yet in a CI workflow.

### Build / test status (verified this pass)

- `go build ./...` — exit 0. `go vet ./...` — exit 0.
- `go test ./internal/handler/ ./internal/server/ ./internal/auth/
  ./internal/server/middleware/ -count=1` — all GREEN.
- `go build ./pkg/...` (new SDK) — exit 0.
- `node scripts/openapi-coverage.mjs --check` — exit 0; **100.0% (397/397)**,
  0 missing, 0 extra, 16 nil-gated.
- `node scripts/generate-openapi-types.mjs --check` — exit 0.
- `TestRouteTableMatchesGolden` — PASS; `TestApierrorCatalogCoverage` — PASS.
- Reverted: nothing.

### FINAL elite scorecard — 7 exit criteria

1. **100% routes in OpenAPI** — **MET** (100.0%, 397/397, drift 0) **and now
   CI-gated** (A5). *Quality caveat: stub-schema fidelity still open.*
2. **Every error uses a typed, documented catalog** — **MET in substance**:
   217-constant catalog, codemod done (0 bare literals), B3 lint gated in CI,
   and B4 now auto-generates `docs/error-codes.md`. *Open: OpenAPI error
   responses don't yet `$ref` the catalog codes.*
3. **Every bare-`{data}` list endpoint returns pagination metadata** — **MET**
   (D1/D3). *Open: D2 COUNT-driven totals partial, D4 stable ordering, D5
   keyset pagination.*
4. **Request bodies validated declaratively (uniform 422)** — **MET for the
   safe set** (C1–C4). *Open: deliberately-deferred cross-field/domain-coded
   validators (C2 remainder).*
5. **CLI drives every resource, with `--output`/completion/config** — **NOT
   MET**: ergonomics shipped (G1/G3/G4/G5), but the generated command groups
   (G2) that achieve full resource parity are still missing.
6. **Generated, versioned SDK (Go + TS)** — **PARTIAL**: Go SDK ships and builds
   (H1); TS is types-only not a full client (H2); no version stamping and no SDK
   drift gate (H3).
7. **`routes.go` split by domain (no file > ~150 routes)** — **MET** (F1/F2,
   golden held).

Net: **4 of 7 fully met (1, 3, 4, 7), 2 partial (2 lacks the OpenAPI
cross-ref; 6 has Go-only/ungated SDK), 1 not met (5 — CLI parity via G2).**

### What genuinely remains for full elite

- **G2** — generate CLI command groups from the spec / `pkg/astroclient` (with a
  Bearer-auth `NewClient` wrapper); the single biggest remaining gap (criterion 5).
- **H2** — full TS client (not just types); **H3** — version both SDKs and add an
  `sdk-check` CI drift gate mirroring `check-sqlc-generated.sh`.
- **A6** — publish the versioned spec artifact + breaking-change changelog.
- **Stub-schema fidelity** — replace the ~31 permissive `additionalProperties`
  stub schemas with authoritative field shapes (also removes the SDK's loose
  `map[string]interface{}` types).
- **D2/D4/D5** — COUNT backfill, stable ordering + `sort`/`order`, keyset
  pagination for the big tables.
- **C2 remainder** — the deferred domain-coded/cross-field validators.
- **B4 cross-ref** — reference the catalog codes from OpenAPI error responses,
  and wire `make error-codes-check` into CI.
