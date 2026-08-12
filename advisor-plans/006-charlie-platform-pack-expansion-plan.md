# Plan 006: Expand Charlie into a version-aware platform intelligence catalog

> **Executor instructions**: Follow this plan in order. Read the complete plan
> before editing either repository. Run every verification command and confirm
> the stated result before moving on. If a STOP condition occurs, stop and
> report it; do not invent a fallback. Update this plan's row in
> `advisor-plans/README.md` when the whole program is done. Individual phases
> may be committed independently, but the row remains `TODO` or `IN PROGRESS`
> until every P1 gate passes.
>
> **Charlie drift check (run first)**:
>
> ```sh
> git -C /root/astronomer-all/charlie diff --stat 73b9bcd265bf084c11fc535dde834a562e0df620..HEAD -- \
>   api cmd/charlie db docs frontend internal/packs agent/contracts agent/opencode \
>   sdk examples scripts Makefile README.md AGENTS.md PRODUCT.md
> ```
>
> **Astronomer drift check (run first)**:
>
> ```sh
> git -C /root/astronomer-all/astronomer diff --stat 8e2d7afb427c0b26449f3926514547b70432b452..HEAD -- \
>   internal/charlie internal/handler deploy/chart docs frontend Makefile
> ```
>
> If an in-scope file changed, compare the current-state excerpts below to the
> live code. A semantic mismatch is a STOP condition. Generated-file-only drift
> is not an exception: determine which source contract produced it before doing
> any work.

## Status

- **Priority**: P1 for the selection contract, Kubernetes, PostgreSQL, Valkey,
  Argo CD, Prometheus, OpenTelemetry, and S3-compatible packs; P2 and
  demand-gated for Linux/systemd, Docker Engine, and OCI runtime.
- **Effort**: L, deliberately split into independently releasable vertical
  slices.
- **Risk**: HIGH because this changes two strict public contracts, route-index
  lifecycle, retrieval selection, and the evidence Charlie gives a model.
- **Depends on**: none. It must preserve the existing Charlie authority and
  downstream-cluster boundaries.
- **Category**: direction, migration, correctness, performance, tests, docs.
- **Planned at**: Charlie commit `73b9bcd265bf084c11fc535dde834a562e0df620`
  and Astronomer commit `8e2d7afb427c0b26449f3926514547b70432b452`,
  2026-08-12.

## Why this matters

Charlie already composes a product's own RAG corpus with one exact Kubernetes
pack, but the route statically chooses the platform version while a reusable
route can serve many deployments on different versions. The current
Kubernetes-specific free-text signal scanner can also select distribution
material from a user's words. That is not a durable foundation for a generic
platform catalog: the knowledge may be excellent and still be the wrong version
or variant for the installation being diagnosed.

The target is one small, generic mechanism: a product reports authenticated,
bounded platform facts when it creates a session; the route declares whether a
pack is fixed or deployment-selected; Charlie resolves an exact pack release,
applies an exact optional variant, and records what happened. Every new pack is
then mostly reviewed Markdown and tests—not a new Go branch, database, policy
engine, tool protocol, or model workflow.

This is intentionally **knowledge expansion, not authority expansion**. A
PostgreSQL pack can help Charlie reason about locks or WAL, but it cannot create
SQL access. A Kubernetes pack can explain a rollout, but it cannot grant
`kubectl`. The consuming product continues to own every observable, capability,
approval, write boundary, audit record, and resource scope.

## Final outcome

When all P1 phases are complete:

- A reusable Charlie route can compose up to eight curated platform packs.
- Each binding is either `fixed` to one exact release line or `asserted`, in
  which case the connected product supplies the exact line for each session.
- Human chat and event investigations carry the same immutable structured
  platform assertions through the Product Bridge.
- A platform version or variant is never inferred from prompts, model output,
  arbitrary context attributes, resource names, chart defaults, or route names.
- The current product RAG leg and each selected platform leg remain separately
  tenant-scoped and are merged only in Go, with product evidence taking
  precedence on equal fused rank.
- Platform failure remains nonfatal under `auto` retrieval, while traces explain
  exactly which pack was selected, skipped, or unavailable and why.
- The query is embedded once per distinct embedding model per request, even when
  several product/platform targets use it.
- Charlie ships reviewed Kubernetes 1.34, 1.35, and 1.36, PostgreSQL 16/17,
  Valkey 8, Argo CD 3.4, Prometheus 3, OpenTelemetry 1, and S3-compatible
  `2006-03-01` knowledge through its existing embedded/registry and pgvector
  pipeline.
- Astronomer reports only platforms it can verify from product-owned,
  management-plane evidence. It never queries or describes downstream cluster
  contents for this purpose.
- The same public contracts, SDKs, authoring rules, and capability-boundary
  guidance are sufficient for the next product integration without adding a
  product-specific Charlie code path.

## Architecture decisions—do not reopen during implementation

| Decision | Exact rule | Reasoning |
|---|---|---|
| One retrieval system | Reuse existing knowledge collections, releases, index builds, encrypted document storage, embeddings, pgvector, and two-leg merge. | A second store or pipeline creates drift, new failure modes, and a second isolation review for no product gain. |
| One session envelope | Store platform assertions inside the existing encrypted `ProductContext` snapshot beside actor/context/resources. | The data is session-scoped, immutable, and already protected. New session columns and joins add no required behavior. |
| Product reports facts | Only the authenticated Product Bridge may submit platform assertions. | The product owns runtime discovery. A prompt or model is not a trustworthy inventory source. |
| Route controls composition | A route selects pack collections and either a fixed or asserted version mode. | Operators decide which generic knowledge is eligible; deployments decide their actual runtime line. |
| Exact matching only | Never choose latest, nearest, default, or semantically “compatible” content when asserted data is absent or mismatched. | Wrong-version operational advice is worse than a clearly reported coverage gap. |
| Variants are data | A pack manifest maps canonical variant slugs to document keys. No pack-specific source-code conditionals. | Adding `eks`, `k3s`, `rustfs`, or another variant should be content work plus validation, not a Charlie release-engine feature. |
| Knowledge is not capability | Pack selection never alters the capability catalog, mode, resource scope, allowlist, approval, budget, cooldown, or circuit breaker. | Charlie must remain unable to manufacture authority from documentation. |
| Product wins ties | Keep the current separate ranking legs and prefer product material on equal fused rank. Cap platform context at the existing one-third budget. | The product corpus describes the actual deployment; a platform pack describes the general system. |
| Supported catalog is bounded | A route may bind at most 8 packs; a session may assert at most 16 unique packs; one shipped pack may publish at most 16 release lines. | This bounds route builds, request work, response sizes, and accidental catalog explosions while leaving ample headroom. |
| No compatibility layer | Change the v1 contracts, regenerate all clients, update both repositories atomically, and remove the old hardcoded signal path. | Charlie is greenfield. Aliases and dual request shapes would permanently enlarge the security and test surface. |

## Minimal-code budget

Treat this as a design constraint, not a rough preference. The implementation
should add only these production concepts:

1. One `PlatformAssertion` contract used by central sessions and the Product
   Bridge.
2. One `version_mode` field on an existing route-pack binding.
3. One generic pack-resolution function.
4. One generic manifest variant map and validator.
5. One per-request embedding-result map.
6. One small Astronomer platform-inventory provider.

Everything else should extend existing handlers, SQL, generated types, tests,
UI controls, and documents. Do not introduce a platform service, repository
abstraction, plugin framework, provider interface hierarchy, background daemon,
message type, queue, cache service, vector namespace, or policy dialect unless a
STOP condition is reviewed and this plan is revised first.

## Current state

### Charlie repository contract and layout

`/root/astronomer-all/charlie/AGENTS.md` defines the constraints the executor
must preserve:

```text
api/openapi.yaml: sole public HTTP contract.
cmd/charlie: sole server binary.
agent/contracts/bridge.openapi.yaml: product-side bridge contract.
Do not add product-specific behavior, alternate servers, public tool protocols,
old routes, compatibility shims, or a second API contract.
Run make verify and make gen-check before handoff.
```

Charlie is product-neutral (`PRODUCT.md` and `docs/product-integration.md`). The
product agent is the only runtime path from a product deployment to Charlie
Central. The admin UI configures Charlie; product user chat remains inside the
product.

### The current route pins one exact version

`charlie/api/openapi.yaml:1686-1716` currently exposes versions but requires one
exact `pack_version`:

```yaml
PlatformPack:
  required: [collection_id, name, versions]
PlatformPackBinding:
  required: [collection_id, pack_version]
  properties:
    collection_id: {type: string}
    pack_version: {type: string, minLength: 1, maxLength: 64}
```

`charlie/cmd/charlie/platform_packs.go:88-128` rejects an empty version and
validates every binding against an exact active release. Lines 131-172 store and
read only `collection_id, pack_version`. The frontend mirrors this exact-pin
shape in `charlie/frontend/src/pages/products.tsx`.

### Platform metadata is not represented in sessions

`charlie/api/openapi.yaml:2316-2345` accepts `product_version`, actor, resources,
and context but no platform assertions. `charlie/cmd/charlie/sessions.go:125`
packs only:

```go
packed := map[string]any{
    "actor": body.Actor,
    "context": body.Context,
    "resources": body.Resources,
}
```

At message time, `charlie/cmd/charlie/sessions.go:216-225` loads route packs and
passes the question plus serialized session/live context into a signal scanner.

The strict Product Bridge request at
`charlie/agent/contracts/bridge.openapi.yaml:736-765` requires request,
authorization, actor, objective, product version, context, and resources, but no
platform list. `charlie/agent/opencode/src/bridge-server.ts:845-880` validates
that exact shape and forwards it to Central. Its separate investigation path
also creates a Central session and must be changed in the same contract slice.

### Variant selection is currently Kubernetes-specific and prompt-sensitive

`charlie/cmd/charlie/platform_pack_signals.go:7-39` scans question/context text
for Kubernetes distribution terms. Lines 63-67 allow a `distribution-*`
document when a term matched:

```go
func platformCandidateAllowed(documentKey string, signals map[string]bool) bool {
    if !strings.HasPrefix(documentKey, "distribution-") {
        return true
    }
    return signals[documentKey]
}
```

This is bounded and does not grant authority, but it is not generic and lets a
user's question influence variant evidence. Replace it; do not layer a second
mechanism on top.

### Existing pack loading and integrity are reusable

`charlie/internal/packs/packs.go:32-84` already defines `Document`, `Version`,
`Pack`, a canonical SHA-256 content digest, default version, and provenance.
`charlie/internal/packs/registry.go` implements the same `Source` interface with
registry safety controls. `charlie/cmd/charlie/platform_packs_seed.go` publishes
pack versions through ordinary document upload, immutable release publication,
activation, and index queuing.

The only embedded pack today is
`charlie/internal/packs/embedded/kubernetes/1.33/`, containing 22 operational
documents. Its `pack.json` defaults to `1.33`.

### Existing database shape

`charlie/db/schema.sql:599-629` records provenance and exact route pins:

```sql
CREATE TABLE knowledge_pack_provenance (
    collection_id  entity_id NOT NULL,
    pack_version   TEXT NOT NULL,
    content_digest TEXT NOT NULL,
    source         TEXT NOT NULL,
    published_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, pack_version)
);

CREATE TABLE gateway_route_platform_packs (
    route_revision_id   entity_id NOT NULL,
    route_id            entity_id NOT NULL,
    collection_id       entity_id NOT NULL,
    product_id          entity_id NOT NULL,
    platform_product_id entity_id NOT NULL,
    pack_version        TEXT NOT NULL,
    ordinal             INTEGER NOT NULL,
    PRIMARY KEY (route_revision_id, collection_id)
);
```

The latest migration at planning time is
`charlie/db/migrations/000029_finding_change_feed.sql`. Add `000030`; never edit
an applied migration.

### Existing retrieval and index lifecycle are the extension points

`charlie/cmd/charlie/knowledge.go:1084-1145` already runs a product leg and then
one separately scoped platform leg per route pack. It uses the same
`retrievalTargets`, `semanticCandidates`, and scope predicates, then merges in
Go. Product retrieval failure follows route policy; platform failure is
nonfatal. Later code gives product candidates precedence and caps platform
content.

`collectLeg` at lines 945-989 currently calls `embedWithModel` once for every
target. Several packs indexed by one embedding model therefore repeat the same
provider call. Fix this in the existing request call graph; do not add a global
cache.

Index lifecycle lives in these existing functions:

- `queuePlatformPackIndexesForRelease` (`knowledge.go:594-630`)
- `queuePlatformPackIndexesWith` (`knowledge.go:641-697`)
- `activateReadyRoutes` (`knowledge.go:737+`)
- `routeBuildSummary` (`cmd/charlie/models.go:476+`)

All currently assume one exact `pp.pack_version`.

The public `KnowledgeCitation` already carries `scope` and `pack_version`, and
`RetrievalTrace` already records overall retrieval status
(`api/openapi.yaml:1902-1953`). Extend these existing objects instead of
creating a second diagnostics endpoint.

### Astronomer already observes management Kubernetes safely

`astronomer/internal/charlie/session_context.go:17-63` builds bounded session
metadata and, when configured, obtains only the management cluster's
`ServerVersion` through a product-owned discovery client:

```go
if info, versionErr := p.discovery.ServerVersion(); versionErr == nil && info != nil {
    version = strings.TrimSpace(info.GitVersion)
    distribution = kubernetesDistribution(version)
}
```

`astronomer/internal/charlie/sessions.go:31-61` carries Kubernetes version and
distribution in `SREContext`, but `BridgeSessionRequest` has no typed platform
field. `astronomer/internal/charlie/bridge_adapter.go:59-101` flattens those
values into untrusted attributes and sends the strict bridge request. Event
investigations use the separate path beginning at `bridge_adapter.go:104` and
must receive the same platform inventory.

Astronomer's product-owned capability catalog already exposes management-plane
health, workloads, events, logs, nodes, storage, networking, database, Redis,
monitoring, Argo, fleet, and tunnel diagnostics. It remains the authority
source. The optional management-Kubernetes visibility boundary is documented in
`astronomer/docs/charlie-kubernetes-visibility.md`; the generic Charlie agent
does not receive a Kubernetes credential, and downstream clusters remain out of
scope.

Astronomer's chart currently provides verifiable integration targets:

- `deploy/chart/values.yaml:228-234`: bundled PostgreSQL `16-alpine`.
- `deploy/chart/values.yaml:300-309`: bundled Valkey `8-alpine` behind the
  existing Redis-compatible service name.
- `deploy/chart/Chart.yaml:18-27`: Argo CD Helm chart `9.5.21`; this chart
  dependency is not automatically proof of the runtime Argo CD application
  version.
- `go.mod`: Prometheus client `v1.23.2` and OpenTelemetry SDK `v1.43.0`. Client
  library versions are not proof that an external Prometheus server or
  collector is installed.

## Commands you will need

Run commands from the named repository. Do not combine the two repos in one
commit.

| Purpose | Repository | Command | Expected on success |
|---|---|---|---|
| Generate all Charlie contracts and SQL | Charlie | `make generate` | exit 0; generated trees updated from the source contracts |
| Check Charlie generated drift | Charlie | `make gen-check` | exit 0; all generated diffs empty after regeneration |
| Validate embedded packs | Charlie | `make packs-check` | exit 0; `TestEmbeddedPacksValidate` passes |
| Focused pack tests | Charlie | `go test ./internal/packs ./cmd/charlie -run 'PlatformPack|Pack|Retriev|Knowledge' -count=1` | exit 0; selected tests pass |
| Product Bridge tests | Charlie | `npm --prefix agent/opencode test` | exit 0; strict request and forwarding tests pass |
| Frontend tests | Charlie | `make test-frontend` | exit 0; type check and UI tests pass |
| Complete Charlie gate | Charlie | `make verify` | exit 0 |
| Live Charlie gate | Charlie | `make verify-live` with the environment named in `scripts/verify-live.sh` | exit 0; content-free evidence says `passed=true` |
| Regenerate pinned bridge | Astronomer | `make charlie-contract-generate` | exit 0; pin, checksums, and generated client agree |
| Check pinned bridge | Astronomer | `make charlie-contract-check` | exit 0; no contract/client drift |
| Focused Astronomer tests | Astronomer | `go test -race -count=1 ./internal/charlie/...` | exit 0 |
| Full Astronomer tests | Astronomer | `make test` | exit 0 |
| Complete Astronomer gate | Astronomer | `make verify-enterprise` | exit 0 for backend, frontend, Helm, and API-contract scopes |
| Patch hygiene | either | `git diff --check` | exit 0; no whitespace errors |

`make verify-live` has deliberate credential/effect acknowledgements. Never put
their values in source, this plan, logs, fixtures, or commit messages.

## Suggested executor toolkit and primary references

Use the official material below when authoring pack content. Do not bulk-copy
upstream documentation; write concise operational synthesis and comply with each
source license and Charlie's existing third-party notice process.

- Kubernetes release and supported patch lines:
  <https://kubernetes.io/releases/patch-releases/>
- PostgreSQL version policy:
  <https://www.postgresql.org/support/versioning/>
- Valkey release lines:
  <https://valkey.io/download/releases/>
- Argo CD 3.3-to-3.4 upgrade guidance:
  <https://argo-cd.readthedocs.io/en/stable/operator-manual/upgrading/3.3-3.4/>
- Argo CD release cadence:
  <https://argo-cd.readthedocs.io/en/latest/developer-guide/release-process-and-cadence/>
- Prometheus release cycle and LTS policy:
  <https://prometheus.io/docs/introduction/release-cycle/>
- Prometheus 3 migration guidance:
  <https://prometheus.io/docs/prometheus/latest/migration/>
- OpenTelemetry versioning/stability rules:
  <https://opentelemetry.io/docs/specs/otel/versioning-and-stability/>
- Docker Engine 28 release notes:
  <https://docs.docker.com/engine/release-notes/28/>
- systemd `journalctl` reference:
  <https://www.freedesktop.org/software/systemd/man/latest/journalctl.html>

Also read these local design sources before editing:

- `charlie/docs/platform-knowledge-design.md`
- `charlie/docs/platform-packs-authoring.md`
- `charlie/docs/kubernetes-platform-pack.md`
- `charlie/docs/product-integration.md`
- `charlie/docs/kubernetes-visibility.md`
- `astronomer/docs/charlie-kubernetes-visibility.md`
- `astronomer/docs/charlie-agent-integration-plan.md`

## Scope

### In scope—Charlie source-of-truth files

- `api/openapi.yaml`
- `agent/contracts/bridge.openapi.yaml`
- `cmd/charlie/platform_packs.go`
- `cmd/charlie/platform_packs_seed.go`
- `cmd/charlie/knowledge.go`
- `cmd/charlie/models.go`
- `cmd/charlie/sessions.go`
- The existing Product Bridge investigation handler in
  `agent/opencode/src/bridge-server.ts:976-1063`, which constructs a Central
  session and immediately submits its first message.
- `internal/packs/packs.go`
- `internal/packs/registry.go`
- `internal/packs/embedded/**`
- `db/schema.sql`
- `db/migrations/000030_platform_pack_selection.sql` (create)
- Existing SQLC query files only if a touched query is generated through SQLC.
- `frontend/src/lib/api.ts`
- `frontend/src/pages/products.tsx`
- Existing adjacent frontend route/editor components and tests only when those
  two files delegate the same UI.
- `agent/opencode/src/bridge-server.ts`
- Existing bridge/investigation tests and fixtures.
- `sdk/go/**`, `sdk/typescript/**`, and `examples/product-integration/**` where
  public contract examples or aliases must change.
- Generated trees: `gen/openapi/**`, `gen/sdk-go/**`, `gen/sdk-typescript/**`,
  `gen/sqlc/**`, and `agent/contracts/generated/**`—regenerate, never hand-edit.
- `docs/platform-knowledge-design.md`
- `docs/platform-packs-authoring.md`
- `docs/kubernetes-platform-pack.md`
- `docs/product-integration.md`
- `README.md`, `THIRD_PARTY_NOTICES.md`, and release/package inventories only
  if the new shipped content or contract makes an existing statement false.
- Existing tests corresponding to every source file above.

### In scope—Astronomer

- `internal/charlie/platform_inventory.go` and test (create)
- `internal/charlie/session_context.go`
- `internal/charlie/sessions.go`
- `internal/charlie/bridge_adapter.go`
- `internal/charlie/trigger_dispatch.go` and the existing event/investigation
  constructors needed to pass the same inventory.
- Existing PostgreSQL/Valkey/runtime capability adapters only to expose a safe,
  already-authorized version fact to the new inventory provider; do not
  duplicate their clients.
- `internal/charlie/contract/pinned/**` and generated
  `internal/charlie/contract/internal/wire/**` through the established contract
  update flow.
- Existing Charlie handler/server wiring and tests needed to inject the one
  inventory provider.
- `docs/charlie-kubernetes-visibility.md` and the active Charlie integration
  plan/document.
- Frontend files only if a visible coverage label is required by an existing
  status page. Do not build a second configuration UI in Astronomer.

### Out of scope—do not touch

- Charlie authority modes, approval semantics, capability admission, tool
  tickets, action budgets, cooldowns, circuit breakers, leader election, audit
  guarantees, or action verification.
- Any direct Charlie Central client in Astronomer. The Product Bridge remains
  the only path.
- Any Kubernetes, SQL, Redis, Argo, PromQL, shell, Docker socket, cloud, or host
  credential added to the generic Charlie agent.
- Any downstream-cluster API, pod, node, log, workload, namespace, event,
  credential, Helm, Argo, backup, or agent-management access.
- Product-specific conditions in Charlie such as `if product == astronomer`.
- A second vector database, knowledge table family, indexer, queue, route type,
  provider/model abstraction, or prompt-specific platform workflow.
- A public platform-pack authoring API. Packs remain Charlie-published.
- A monolithic `aws`, `azure`, or `gcp` pack. Provider facts belong in narrow
  variants or future service-specific packs justified by a real product use
  case.
- Copying full vendor documentation, ingesting arbitrary websites, or shipping
  unreviewed generated prose.
- LiteLLM, new model providers, model pricing, billing, or token accounting.
- Historical compatibility request shapes, aliases, deprecation shims, or dual
  readers.
- Editing migrations `000001` through `000029`.
- Adding a top-level `plans/` or `advisor-plans/` directory to Charlie. Charlie's
  clean-tree verifier intentionally rejects planning artifacts; this
  cross-repository plan lives in Astronomer's existing `advisor-plans/` catalog.

## Git workflow

- Charlie branch: `feat/platform-pack-selection`
- Astronomer branch: `feat/charlie-platform-inventory`
- Commit one coherent vertical slice at a time. Use observed conventional
  commit style, for example `feat(rag): resolve asserted platform pack versions`
  or `feat(charlie): report verified platform inventory`.
- Regenerated files belong in the same commit as their source contract.
- Do not mix the two repositories in one commit.
- Do not push, open a PR, deploy, or change live credentials unless the operator
  separately instructs it.
- Keep both worktrees clean between phases. Preserve unrelated user changes; if
  an in-scope file is already dirty, STOP and ask before editing it.

## Detailed target contract

### `PlatformAssertion`

Add the same semantic object to Charlie Central's `SessionInput`, the Product
Bridge `CreateSession`, and the Product Bridge investigation request:

```yaml
PlatformAssertion:
  type: object
  additionalProperties: false
  required: [pack, pack_version]
  properties:
    pack:
      type: string
      pattern: '^[a-z][a-z0-9-]{0,63}$'
    pack_version:
      type: string
      minLength: 1
      maxLength: 64
    observed_version:
      type: string
      minLength: 1
      maxLength: 128
    variant:
      type: string
      pattern: '^[a-z][a-z0-9-]{0,63}$'
```

The containing `platforms` property must be a required array, permit zero
items, cap at 16, and reject duplicate `pack` values in application validation.
OpenAPI `uniqueItems` alone is insufficient because two objects with the same
pack but different observed strings are structurally distinct.

Semantics:

- `pack` is the canonical Charlie pack name, not a collection ID.
- `pack_version` is the exact Charlie knowledge release line selected by that
  product, such as Kubernetes `1.36`, PostgreSQL `16`, or S3-compatible
  `2006-03-01`.
- `observed_version` is the bounded raw product observation used only for audit
  and diagnosis, such as `v1.36.2+k3s1`. It never participates in release
  fallback.
- `variant` is a canonical pack-defined slug, such as `k3s` or `rustfs`.
- Assertions are product facts and knowledge selectors only. They must never be
  interpreted as resource authorization or capability requests.
- Central validates syntax and uniqueness but does not trust a user message to
  create or change assertions.
- Snapshot the list at session/investigation creation. A later message cannot
  mutate it.

### Route binding

Replace the current exact-only binding with:

```yaml
PlatformPackBinding:
  type: object
  required: [collection_id, version_mode]
  properties:
    collection_id: {type: string}
    version_mode: {type: string, enum: [fixed, asserted]}
    pack_version: {type: string, minLength: 1, maxLength: 64}
```

Application and database rules:

- `fixed` requires `pack_version` and verifies an exact published release.
- `asserted` forbids `pack_version`.
- Duplicate collection IDs are rejected.
- At most eight bindings are accepted.
- Existing stored rows migrate to `fixed`; this is deterministic data
  migration, not a compatibility reader.
- The UI label is **Deployment-reported (recommended)** for `asserted` and
  **Fixed release line** for `fixed`.
- `fixed` is appropriate for a route deliberately uniform across all connected
  deployments or protocol-style packs such as S3-compatible.
- `asserted` is appropriate for one reusable product route serving deployments
  on different runtime versions.

### Deterministic resolution

Implement one pure resolver with table-driven tests. Inputs are route bindings,
session assertions, and currently published pack metadata. Output is an ordered
list of resolved legs plus a trace row for every binding.

Rules in exact order:

1. Resolve the pack's canonical name from the route binding's platform-owned
   collection. Never accept a caller-owned collection.
2. For `fixed`, use only the configured exact version.
3. If a structured assertion exists for the same fixed pack and its
   `pack_version` conflicts, skip that leg with `assertion_conflict`; do not
   knowingly give advice for a different runtime.
4. For `asserted`, find exactly one assertion by canonical pack name. If absent,
   skip with `assertion_missing`.
5. Require an exact published active release for the resolved version. If
   absent, skip with `version_unavailable`.
6. If an assertion has a variant, require that slug to be declared by the pack.
   An unknown variant does not enable any variant documents; record the bounded
   coverage gap. Core documents may still be used when version resolution is
   valid.
7. If no variant is asserted, only core documents are eligible.
8. Never use the pack default as runtime fallback. The default is an authoring
   and fixed-binding convenience displayed to administrators.
9. Preserve route ordinal when querying and tracing multiple packs.

### Generic variant manifest

Extend `pack.json` with a pack-level closed map:

```json
{
  "name": "kubernetes",
  "default_version": "1.36",
  "variants": {
    "k3s": ["distribution-k3s"],
    "eks": ["distribution-eks"],
    "gke": ["distribution-gke"],
    "aks": ["distribution-aks"],
    "openshift": ["distribution-openshift"]
  }
}
```

Validation requirements:

- Pack names and variant slugs use the same canonical slug pattern.
- Variant arrays are nonempty, contain unique document keys, and no document key
  appears in two variants.
- Every listed document key exists in every published version of that pack.
- Any document not listed by a variant is core and always eligible after exact
  version resolution.
- A pack may define no variants.
- At most 16 variants and at most 32 document keys per variant.
- Include the canonical, sorted variant map in the version digest. A change in
  variant eligibility changes retrieval behavior and must republish/index the
  semantic release line just like a title/body change.
- Embedded and registry sources produce the same `Pack` shape and digest.
- Registry catalogs cannot override embedded validation rules.

### Catalog response and retrieval trace

Extend the existing platform-pack response with:

- `default_version` (required)
- `variants` (required, sorted array of slugs; empty is valid)
- Existing `versions` remains a sorted string array.

Add a required `platform_pack_traces` array of
`PlatformPackRetrievalTrace` objects to the existing `RetrievalTrace`. The
array is empty when the route composes no packs. Each row contains:

- `pack_name`
- `collection_id`
- `version_mode`
- optional `asserted_version`
- optional `resolved_version`
- optional `variant`
- `state`: `selected`, `skipped`, or `unavailable`
- `reason`, one of:
  - `fixed_selected`
  - `asserted_selected`
  - `assertion_missing`
  - `assertion_conflict`
  - `version_unavailable`
  - `variant_unknown`
  - `index_unavailable`
  - `platform_product_unavailable`
- `hit_count`, minimum zero.

Add `pack_name` and optional `pack_variant` to platform citations. Keep
`pack_version`. Product citations omit all three pack fields. Do not expose raw
session context, secrets, provider requests, prompts, embeddings, or document
bodies in trace metadata.

## Steps

### Step 0: Establish clean baselines and lock the change surface

- [ ] Run both drift checks.
- [ ] Run `git status --short` in both repositories; require no unrelated
  in-scope modifications.
- [ ] Run `make gen-check`, `make packs-check`, and the focused Charlie pack
  tests before edits.
- [ ] Run `make charlie-contract-check` and
  `go test -race -count=1 ./internal/charlie/...` in Astronomer before edits.
- [ ] Save only command, commit, timestamp, exit code, and test counts in local
  implementation notes. Do not capture secrets or test payload content.
- [ ] Confirm the live Charlie deployment is healthy before treating a later
  failure as a regression. This is observation only; no deployment in this
  step.

**Verify**:

```sh
git -C /root/astronomer-all/charlie status --short
git -C /root/astronomer-all/astronomer status --short
```

Expected: no unexpected in-scope paths. If baseline tests fail, STOP; do not
mix baseline repair into this plan.

### Step 1: Change the Charlie Central contract first

Files: `api/openapi.yaml`, generated `gen/openapi/**`, `gen/sdk-go/**`, and
`gen/sdk-typescript/**`.

- [ ] Add `PlatformAssertion` exactly as specified above.
- [ ] Make `platforms` required on `SessionInput`; allow an empty list, cap at
  16.
- [ ] Add `version_mode` and conditional semantics to
  `PlatformPackBinding`; cap route `platform_packs` at 8.
- [ ] Add `default_version` and sorted `variants` to `PlatformPack`.
- [ ] Add the per-pack retrieval trace object and required empty-or-populated
  array to `RetrievalTrace`.
- [ ] Add `pack_name` and `pack_variant` to `KnowledgeCitation`.
- [ ] Update examples to show an Astronomer session with Kubernetes `1.36`, raw
  observed version, and `k3s`; include `platforms: []` in a neutral example.
- [ ] Do not add alternate or deprecated fields.
- [ ] Run generation once; inspect generated diffs rather than hand-editing.

**Verify**:

```sh
cd /root/astronomer-all/charlie
make generate
make gen-check
rg -n "PlatformAssertion|version_mode|platform_pack_traces|pack_variant" \
  api/openapi.yaml gen/openapi gen/sdk-go gen/sdk-typescript
```

Expected: generation and drift check exit 0; each new concept appears in the
source contract and generated clients.

### Step 2: Add one forward database migration and update the greenfield schema

Files: `db/migrations/000030_platform_pack_selection.sql` and `db/schema.sql`.
No SQLC source query change is expected in this step; if `make generate` changes
`gen/sqlc/**`, inspect the source of that drift before retaining it.

- [ ] Add `version_mode TEXT NOT NULL DEFAULT 'fixed'` to
  `gateway_route_platform_packs`.
- [ ] Convert the existing rows deterministically to `fixed` before tightening
  constraints.
- [ ] Make `pack_version` nullable.
- [ ] Add a database CHECK requiring exactly:
  - fixed + nonblank pack version, or
  - asserted + null pack version.
- [ ] Remove the temporary default from `version_mode` after backfill if Charlie
  conventions require every writer to be explicit.
- [ ] Add `is_default BOOLEAN NOT NULL DEFAULT FALSE` and
  `variant_documents JSONB NOT NULL DEFAULT '{}'::jsonb` to
  `knowledge_pack_provenance`.
- [ ] Add a partial unique index allowing one default version per collection.
- [ ] Add JSON shape checks only if they can remain simple and immutable; the Go
  loader is responsible for key/document semantic validation.
- [ ] Update `db/schema.sql` to the final post-migration shape.
- [ ] Add migration tests covering existing fixed rows, both valid final forms,
  and invalid combinations.

The migration must not delete releases, rebuild vectors, rewrite encrypted
content, or change route revisions. Existing routes remain exact fixed routes.

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test -p=1 ./cmd/charlie -run 'Migration|PlatformPack' -count=1
make gen-check
```

Expected: exit 0; a query of the test database proves old rows are `fixed`, and
the database rejects `fixed/NULL`, `asserted/non-NULL`, or unknown modes.

### Step 3: Make pack variants generic data and delete the signal scanner

Files: `internal/packs/packs.go`, `internal/packs/registry.go`, their tests,
`cmd/charlie/platform_pack_signals.go` and test (delete after replacement), and
pack manifests.

- [ ] Add `Variants map[string][]string` to the pack manifest and runtime
  `Pack` object.
- [ ] Implement one canonical version digest covering sorted document keys,
  titles, content, sorted variant slugs, and sorted variant document keys.
- [ ] Rename `ContentDigest` only if the new name makes the broader contract
  materially clearer; do not leave duplicate digest functions.
- [ ] Validate all bounds, slugs, uniqueness, document membership, and
  cross-version existence described in the target contract.
- [ ] Apply identical parsing/validation to embedded and registry packs.
- [ ] Add Kubernetes's existing distribution mapping to `pack.json`.
- [ ] Delete `cmd/charlie/platform_pack_signals.go` and its scanner tests.
- [ ] Replace them with generic variant-eligibility tests driven entirely by
  manifest data and structured assertions.
- [ ] Prove that question text containing `EKS`, `k3s`, or `OpenShift` cannot
  enable a variant.
- [ ] Prove that changing only variant metadata changes the digest and triggers
  a new immutable release snapshot for that semantic line.

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test ./internal/packs ./cmd/charlie -run 'Pack|Variant|Signal|Digest' -count=1
test ! -e cmd/charlie/platform_pack_signals.go
rg -n "platformReferenceSignals|containsPlatformSignal|distribution-.*strings" cmd/charlie internal/packs && exit 1 || true
make packs-check
```

Expected: tests and pack validation pass; the deleted signal symbols have no
matches.

### Step 4: Persist pack metadata and expose a deterministic catalog

Files: `cmd/charlie/platform_packs_seed.go`,
`cmd/charlie/platform_packs_seed_test.go`, `cmd/charlie/platform_packs.go`, and
tests.

- [ ] Store the canonical variant map and default flag in provenance for every
  published version.
- [ ] Reconcile the one default after all versions of a pack are published so a
  default change cannot leave two defaults or none.
- [ ] Keep seeding nonfatal and idempotent as it is today.
- [ ] Ensure a variant-only metadata change republishes the affected semantic
  release using ordinary upload/publish/activate/index flow.
- [ ] Extend `ListPlatformPacks` to return canonical pack name, description,
  sorted versions, one default version, and sorted variant slugs.
- [ ] Keep the empty platform-product state a successful empty list.
- [ ] Reject inconsistent provenance rather than guessing a default.
- [ ] Ensure the registry and embedded source produce identical catalog rows for
  identical content.

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test -p=1 ./cmd/charlie -run 'SeedPack|PlatformPack|Provenance|Catalog' -count=1
make packs-check
```

Expected: exit 0; unchanged boot is a no-op, metadata changes republish once,
and catalog ordering is stable.

### Step 5: Store assertions in the existing encrypted session snapshot

Files: `cmd/charlie/sessions.go`, existing investigation/session helpers, and
tests.

- [ ] Add an internal strict parser/validator for platform assertion arrays.
- [ ] Reject duplicate pack names with a stable 400 error code.
- [ ] Include the validated array under `platforms` in the existing `packed`
  ProductContext map at session creation.
- [ ] Require an explicit empty array rather than treating field omission as old
  behavior.
- [ ] Make event investigation-created sessions write the same object.
- [ ] Parse assertions from the immutable stored snapshot when a message begins.
- [ ] Do not merge assertions from `MessageInput.context`, headers, evidence,
  actor fields, resource fields, or free text.
- [ ] Do not add a session column or plaintext audit field.
- [ ] Ensure session history/replay uses the original snapshot even if product
  inventory changes later.
- [ ] Emit only content-free validation metadata such as assertion count and
  error code; never log raw observed versions if existing observability policy
  disallows product values.

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test -p=1 ./cmd/charlie -run 'CreateSession|Investigation|PlatformAssertion|ProductContext' -count=1
```

Expected: valid assertions round-trip inside encrypted context; missing,
duplicate, malformed, message-injected, and oversized values are rejected or
ignored exactly as specified.

### Step 6: Implement one pure exact resolver and wire route persistence

Files: `cmd/charlie/platform_packs.go`, `cmd/charlie/models.go`, create
`cmd/charlie/platform_pack_resolution_test.go`, and extend the existing route
tests.

- [ ] Change route binding validation to enforce fixed/asserted combinations and
  the 8-pack bound.
- [ ] Store/read `version_mode` and nullable `pack_version` in route revision
  order.
- [ ] Join the platform-owned collection name when reading bindings; this is the
  canonical assertion lookup key.
- [ ] Implement the deterministic resolver as a pure function over bounded
  structs. It must not query, log, embed, or mutate.
- [ ] Fetch published release/variant metadata in one bounded database query,
  then call the pure resolver. Avoid one SQL query per pack.
- [ ] Produce one trace row for every configured binding, including skipped and
  unavailable legs.
- [ ] Keep platform resolution failure nonfatal to product retrieval under
  `auto`.
- [ ] Under `required`, preserve current product-knowledge semantics. A missing
  optional platform assertion must not turn `required` into “all platform packs
  required”; document this explicitly.
- [ ] Reject customer collection IDs and cross-platform-product IDs at both
  handler and foreign-key boundaries.

Required table-driven resolver cases:

| Binding | Assertion | Catalog | Expected |
|---|---|---|---|
| fixed 1.36 | none | 1.36 exists | selected 1.36 |
| fixed 1.36 | Kubernetes 1.36/k3s | exists | selected 1.36 + k3s |
| fixed 1.36 | Kubernetes 1.35 | both exist | skipped `assertion_conflict` |
| asserted | Kubernetes 1.36/k3s | exists, variant exists | selected 1.36 + k3s |
| asserted | none | exists | skipped `assertion_missing` |
| asserted | Kubernetes 1.37 | only 1.36 | skipped `version_unavailable` |
| asserted | Kubernetes 1.36/unknown | version exists | core selected; variant gap recorded |
| asserted Kubernetes | PostgreSQL 16 | both catalogs exist | skipped `assertion_missing` |
| any | duplicate assertions | any | request rejected before resolver |

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test -p=1 ./cmd/charlie -run 'PlatformPack|Route|Resolve' -count=1
```

Expected: all table rows pass and route readback exactly matches the submitted
mode/version/ordinal.

### Step 7: Generalize platform index queuing without unbounded work

Files: `cmd/charlie/knowledge.go`, `cmd/charlie/models.go`, and
`cmd/charlie/platform_packs_e2e_test.go`.

- [ ] For a fixed binding, queue only the exact release as today.
- [ ] For an asserted binding, queue every currently published release of the
  selected pack, bounded by the manifest maximum of 16.
- [ ] When a pack release is activated/replaced, queue:
  - every asserted route composing the pack, and
  - fixed routes whose exact line matches the activated line.
- [ ] Keep the build's `product_id` equal to the platform product and
  `organization_id` equal to the composing route's model-owning organization;
  existing comments document why.
- [ ] Preserve the existing unique build identity and conflict handling so the
  same release/model/dimension/chunker build is shared rather than duplicated.
- [ ] Include every required asserted-version build in pending route readiness
  and `routeBuildSummary`.
- [ ] Do not take an already-active route offline while a newly published pack
  version is building. Sessions asserting that new line report
  `index_unavailable`; existing lines continue to work.
- [ ] Initial creation/pending promotion waits for all currently published
  required builds, but no future release can retroactively make the active route
  unusable.
- [ ] An embedding model/dimension change queues all fixed or asserted pack
  builds needed by the new revision before promotion.
- [ ] Add a test that 8 packs × 16 versions stays within the stated bound and
  does not generate an accidental Cartesian product beyond 128 resolution rows.

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test -p=1 ./cmd/charlie -run 'PlatformPack.*(Index|Route|Release)|ActivateReadyRoutes|RouteBuildSummary' -count=1
```

Expected: fixed/asserted queuing, activation, replacement, failure, recovery,
and model-change tests pass without cross-product or cross-organization rows.

### Step 8: Wire generic retrieval, citations, traces, and request-local embedding reuse

Files: `cmd/charlie/knowledge.go`, `cmd/charlie/sessions.go`, and retrieval tests.

- [ ] Resolve route packs from the immutable session assertions before calling
  retrieval.
- [ ] Remove all question/context signal arguments from
  `retrieveKnowledgeWithPacks`.
- [ ] Pass the selected variant document-key set directly to the existing
  candidate filter.
- [ ] Attach pack name, exact version, and optional variant to each platform
  candidate/citation.
- [ ] Populate one trace row per route binding and update its `hit_count` after
  final ranking.
- [ ] Preserve separate product/platform SQL predicates and the post-query scope
  revalidation.
- [ ] Preserve product tie precedence and the existing platform one-third
  context budget.
- [ ] Preserve general-model passthrough: when product and platform retrieval are
  not relevant under `auto`, the model may answer general questions without
  fabricated citations.
- [ ] Add a request-local map keyed by embedding model ID plus dimensions (or the
  existing target identity that proves vector compatibility).
- [ ] Embed the query once per distinct model and reuse the vector across product
  and platform targets in that request.
- [ ] Cache failures only within the request so an unavailable provider is not
  called repeatedly for every pack. Never cache across requests, scopes,
  deployments, or tenants.
- [ ] Keep vector search per scoped target; do not combine product IDs in SQL and
  do not add parallel goroutines until benchmarks prove a need.

Required security regressions:

- A user says “this is EKS 1.36” but the assertion says `k3s/1.36`: only k3s
  variant documents are eligible.
- A prompt asks Charlie to use PostgreSQL 17 docs but the assertion says 16:
  only 16 is eligible.
- A model/tool message tries to mutate the stored platforms: ignored.
- One product route cannot retrieve another product's corpus.
- A platform candidate with a caller product ID is dropped and emits the
  existing content-free scope-violation event.
- An unavailable platform index does not erase valid product hits.
- Eight selected packs on one embedding model cause exactly one query embedding
  provider call.

**Verify**:

```sh
cd /root/astronomer-all/charlie
go test -p=1 ./cmd/charlie -run 'Retriev|Knowledge|PlatformPack|Embedding' -count=1
```

Expected: all regressions pass; an instrumented fake provider records one
embedding call for all same-model legs.

### Step 9: Update the Product Bridge, SDKs, and executable integration fixture

Files: `agent/contracts/bridge.openapi.yaml`, generated bridge files,
`agent/opencode/src/bridge-server.ts`, bridge tests, `sdk/**`, and
`examples/product-integration/**`.

- [ ] Add the exact `PlatformAssertion` and required `platforms` array to bridge
  `CreateSession`.
- [ ] Add the same required array to the investigation request that creates an
  event-bound Central session.
- [ ] Extend `exactObject` allow/required lists; unknown fields remain rejected.
- [ ] Validate the array bounds, exact object keys, canonical slugs, string
  bounds, and unique pack names at the bridge before forwarding.
- [ ] Forward the array unchanged to Central. Do not flatten it into context
  attributes.
- [ ] Ensure the bridge cannot derive or override it from a chat command, local
  MCP output, environment value, or Central response.
- [ ] Update Go and TypeScript SDK surface aliases/examples through generation;
  do not create hand-maintained duplicate DTOs.
- [ ] Update the executable product fixture so both human and event session
  examples send either a valid list or an explicit empty list.
- [ ] Add strict rejection tests for omission, unknown keys, duplicates,
  oversize lists/strings, invalid slugs, and wrong primitive types.
- [ ] Add forwarding tests proving raw observed version and variant arrive
  unchanged and do not affect authorization headers or ticketing.

**Verify**:

```sh
cd /root/astronomer-all/charlie
make generate
npm --prefix agent/opencode run typecheck
npm --prefix agent/opencode test
npm --prefix sdk/typescript test
npm --prefix examples/product-integration test
make gen-check
```

Expected: exit 0; strict bridge and both supported SDKs reflect one contract.

### Step 10: Make route configuration obvious in Charlie Admin

Files: `frontend/src/lib/api.ts`, `frontend/src/pages/products.tsx`, and existing
adjacent tests.

- [ ] Update local API view types to the generated contract shape; do not invent
  alternate names.
- [ ] For each chosen pack, offer:
  - **Deployment-reported (recommended)** → `asserted`, no version value.
  - **Fixed release line** → `fixed`, one exact version selector defaulting to
    the pack's declared default only when the operator chooses fixed.
- [ ] Show the pack description, default, supported versions, and available
  variants without exposing internal platform product IDs.
- [ ] Explain in one concise sentence that deployment-reported mode skips the
  pack when an integration does not report a matching exact version.
- [ ] Show index build totals/readiness using the existing route revision status;
  do not add polling infrastructure if the page already has a query refresh
  pattern.
- [ ] Preserve deep linking and browser refresh for the product/route editor.
- [ ] Validate and display server error codes for invalid binding combinations.
- [ ] Ensure editing a route round-trips asserted/fixed values exactly and does
  not silently fill `pack_version` for asserted mode.
- [ ] Test keyboard navigation, labels, focus, loading, empty catalog, failed
  catalog, and mobile-width layout.

**Verify**:

```sh
cd /root/astronomer-all/charlie
make test-frontend
```

Expected: type check and tests pass; route create/edit/readback tests cover both
modes and empty/error states.

### Step 11: Add Astronomer's one verified platform-inventory provider

Files: new `internal/charlie/platform_inventory.go` and test, plus existing
session/event wiring.

Create one small interface:

```go
type PlatformInventoryProvider interface {
    Platforms(context.Context) ([]PlatformAssertion, error)
}
```

The concrete provider composes existing product-owned clients/readers. It must
return a sorted, unique, bounded slice. Define the local assertion DTO once and
map it to the generated bridge type in `bridge_adapter.go`.

- [ ] Kubernetes: use management-plane discovery `ServerVersion`, never a
  downstream connection. Normalize a strict value such as
  `v1.36.2+k3s1` to `pack=kubernetes`, `pack_version=1.36`,
  `observed_version=v1.36.2+k3s1`, `variant=k3s`.
- [ ] Accept only a strict Kubernetes version pattern with numeric major/minor.
  If parsing fails, omit the assertion and record a content-free coverage
  reason; never guess.
- [ ] Map only closed distribution facts already proven by Astronomer's existing
  `kubernetesDistribution` logic: `k3s`, `eks`, `gke`, `aks`, `openshift`.
  Unknown distribution means no variant, not a guessed provider.
- [ ] PostgreSQL: obtain `server_version_num` through the existing management
  database pool with a constant safe query. Normalize supported major 16 or 17.
  Do not send DSNs, server banners beyond the bounded observed version, database
  names, roles, SQL text, or credentials.
- [ ] Valkey: reuse the existing safe allowlisted runtime/INFO reader. Emit
  `pack=valkey`, `pack_version=8` only when the engine positively identifies as
  Valkey. A Redis-compatible endpoint that does not prove Valkey is not enough.
- [ ] Argo CD: emit `pack=argocd`, `pack_version=3.4` only from a verified
  product-owned runtime/version endpoint or immutable installed artifact
  identity. The Helm dependency version alone is not proof. If no safe source
  exists, omit Argo in this change and report the coverage gap rather than add a
  new credential or broad query.
- [ ] Prometheus and OpenTelemetry: do not infer a server/collector installation
  from Astronomer's client library versions. Emit assertions only when an
  existing configured, product-owned integration can safely prove the runtime
  semantic line. Otherwise the route may use a reviewed fixed pack where that
  accurately represents a supported protocol contract.
- [ ] S3-compatible: emit the stable API line only when Astronomer has an enabled
  management object-store integration. Map a variant only from configured
  provider type (`aws-s3`, `rustfs`, `minio`), never from endpoint hostname text.
- [ ] Inventory failure must not prevent Astronomer from opening a chat or
  dispatching an investigation. Return verified partial results and expose a
  bounded coverage reason through existing health/status telemetry.
- [ ] Cache only stable observations using existing product runtime patterns if
  needed. Do not add Redis or a new table for this inventory.
- [ ] Ensure session and event/investigation paths call the same provider.
- [ ] Keep existing SREContext fields for product diagnostics only if still
  useful; do not use them as Charlie's selection source after the new contract.

**Verify**:

```sh
cd /root/astronomer-all/astronomer
go test -race -count=1 ./internal/charlie/... -run 'Platform|Session|Investigation|Bridge'
```

Expected: table tests cover exact normalization, partial failure, unknown
variant, malformed version, duplicate suppression, deterministic sorting, and
no downstream client calls.

### Step 12: Pin and consume the new bridge contract in Astronomer

Files: `internal/charlie/contract/pinned/**`, generated wire client,
`internal/charlie/sessions.go`, `bridge_adapter.go`, `trigger_dispatch.go`, and
tests.

- [ ] Import the reviewed Charlie bridge source using Astronomer's documented
  pin workflow; update `pin.json` and checksums.
- [ ] Regenerate the local wire client. Do not hand-edit
  `internal/charlie/contract/internal/wire/client.gen.go`.
- [ ] Add platforms to `BridgeSessionRequest` and the event investigation
  request.
- [ ] Pass the provider's sorted verified list to the generated request.
- [ ] Ensure both paths send explicit `[]`, not null or omission, when nothing is
  verified.
- [ ] Preserve mTLS, `authorization_ref`, idempotency key, local enablement,
  central deployment enablement, and exact bridge base URL behavior.
- [ ] Preserve read-only/approval/automation mode handling without any pack-based
  branch.
- [ ] Add contract tests showing platform metadata cannot become resource IDs,
  required verbs, capabilities, approval manifests, or action tickets.

**Verify**:

```sh
cd /root/astronomer-all/astronomer
make charlie-contract-generate
make charlie-contract-check
go test -race -count=1 ./internal/charlie/...
```

Expected: contract drift check and focused race tests pass; human and event
requests include the same inventory.

### Step 13: Update Kubernetes first and prove the architecture before adding breadth

Files: `internal/packs/embedded/kubernetes/**`, pack tests, and Kubernetes docs.

- [ ] Retain 1.33 only as an explicitly supported historical line if Charlie
  still serves a connected 1.33 deployment. Do not make it the default.
- [ ] Add 1.34, 1.35, and 1.36 directories.
- [ ] Set the default to 1.36 after content validation.
- [ ] Preserve the existing 22-domain failure-mode-first shape for each line,
  but review every document for actual API, feature-gate, version-skew,
  deprecation, and behavior differences. Do not blindly copy 1.33.
- [ ] Keep distribution overlays variant-gated through the manifest.
- [ ] Update `docs/kubernetes-platform-pack.md` to list supported lines, exact
  selection, variants, source expectations, and no-downstream boundary.
- [ ] Update tests that hardcode 1.33 to table-test every shipped line.

Kubernetes content domains required in every line:

1. operational workflow
2. triage evidence
3. common failure states
4. cross-resource review
5. workload controllers
6. probes, rollouts, and lifecycle
7. resource starvation and scheduling
8. network, Services, and DNS
9. storage, state, and backup
10. RBAC, identities, and Secrets
11. insecure workload defaults
12. multitenancy and policy
13. observability, events, and logs
14. control plane and nodes
15. Helm, Kustomize, and GitOps
16. safe change and recovery
17. API drift and validation
18–22. k3s, EKS, GKE, AKS, and OpenShift overlays

Every operational recommendation must state preconditions, read evidence,
scope, risk, and verification. It may name commands as operator guidance but
must never imply Charlie possesses those commands.

**Verify**:

```sh
cd /root/astronomer-all/charlie
make packs-check
go test ./internal/packs ./cmd/charlie -run 'Kubernetes|PlatformPack|Retriev' -count=1
```

Expected: all lines validate, all required keys exist, defaults/variants are
correct, and wrong-line/variant retrieval tests return no ineligible citation.

### Step 14: Run the first end-to-end gate with Astronomer before authoring more packs

- [ ] Configure the Astronomer Charlie route with the product collection and
  Kubernetes pack in asserted mode.
- [ ] Create a human session through Astronomer and verify its management
  Kubernetes assertion reaches Charlie.
- [ ] Ask product-specific, generic Kubernetes, mixed, and unrelated general
  questions.
- [ ] Verify citations distinguish product and platform, name exact Kubernetes
  version/variant, and never cite a different line/provider.
- [ ] Trigger one product-owned investigation and verify it receives the same
  inventory without a human session.
- [ ] Exercise read-only, approval-required, and autonomous modes. The knowledge
  answer may differ, but capability/action outcomes must remain identical to
  existing policy for the same requested action.
- [ ] Attempt prompt injection claiming a different platform/version and asking
  for downstream access. Verify selection does not change and every downstream
  action remains denied.
- [ ] Disable platform index/provider access and verify product RAG/general model
  behavior degrades with an explicit trace rather than hanging or failing the
  entire `auto` turn.
- [ ] Record content-free live evidence: candidate commits, contract digests,
  route revision, pack/version/variant selection states, scenario pass/fail,
  timestamps, and correlation IDs only.

Do not proceed to Step 15 until this slice is live-qualified. It proves the
generic control plane once; later packs should be data-heavy repetitions.

**Verify**:

```sh
cd /root/astronomer-all/charlie
make verify-live
```

Expected: exit 0 and evidence `passed=true`, including the new version/variant,
cross-scope, prompt-injection, mode-invariance, and event-session scenarios.

### Step 15: Add the P1 operational packs as reviewed data slices

Implement one pack per commit. For each pack: author manifest/content, add
content-contract tests, seed/index tests, positive/negative retrieval fixtures,
source/notice updates, and docs. Do not add pack-specific Go logic.

#### 15A—PostgreSQL

Manifest:

- name: `postgresql`
- versions: `16`, `17`
- default: `17`
- variants only when a reviewed overlay is needed, for example a specific HA
  operator or managed service. Core content must remain vendor-neutral.

Required documents:

1. `operational-workflow`—symptom → safe evidence → hypothesis → bounded change
   → verification.
2. `connections-and-pools`—connection saturation, pool starvation, timeouts,
   idle/active states, and safe capacity reasoning.
3. `locks-and-deadlocks`—blocking chains, deadlocks, transaction age, and why
   termination is a privileged action.
4. `query-and-statistics`—safe aggregate evidence, plan/statistics reasoning,
   and avoidance of sensitive SQL text in Charlie evidence.
5. `vacuum-and-bloat`—autovacuum health, wraparound risk, bloat indicators, and
   remediation ordering.
6. `wal-and-checkpoints`—WAL growth, checkpoint pressure, archive failure, and
   disk impact.
7. `replication-and-recovery`—lag, slots, recovery state, timelines, and failover
   evidence.
8. `storage-and-disk`—capacity, IOPS/latency symptoms, temp spill, and safe
   escalation.
9. `backup-pitr-and-restore`—backup proof, recovery objectives, restore drills,
   and no “backup exists” claim without evidence.
10. `ha-and-failover`—role/topology proof, fencing, split-brain prevention, and
    post-failover verification.
11. `upgrades-and-extensions`—major/minor distinction, extension compatibility,
    and rollback planning.
12. `tls-and-authentication`—certificate/auth failure classes without exposing
    connection strings, passwords, or role secrets.

Version-specific differences must cover supported catalog/statistics behavior,
upgrade paths, and changed defaults. Content tests must reject SQL examples that
read user rows, secrets, or unrestricted statement text.

#### 15B—Valkey

Manifest:

- name: `valkey`
- version/default: `8`
- do not call the pack Redis. Wire compatibility does not make runtime identity
  interchangeable for version-specific advice.

Required documents:

1. operational workflow and latency triage
2. memory, fragmentation, and eviction
3. persistence: RDB/AOF and rewrite pressure
4. replication and failover
5. Sentinel/cluster topology where applicable
6. client connection, timeout, retry, and pool behavior
7. hot-key/large-value diagnosis using aggregates without disclosing keys or
   values
8. queue semantics, retries, and consumer availability
9. backup/recovery and data-loss boundaries
10. upgrades, compatibility, and safe rollback

No document may recommend `KEYS *`, dump values, print auth material, or assume
that deleting a key is safe.

#### 15C—Argo CD

Manifest:

- name: `argocd`
- version/default: `3.4`
- optional provider variants only when behavior is genuinely variant-specific;
  do not duplicate the core.

Required documents:

1. Application sync and health interpretation
2. reconciliation and refresh behavior
3. repository/authentication diagnosis without secret disclosure
4. manifest rendering, diffing, and source errors
5. hooks, waves, and ordering
6. ApplicationSet generation/controller failures
7. registered cluster connectivity boundaries
8. controller saturation, work queues, and sharding
9. Redis/cache symptoms and recovery constraints
10. safe sync, rollback, prune, and retained-resource reasoning
11. upgrades, API drift, and version compatibility

The pack may explain downstream Application concepts but cannot expand
Astronomer's current downstream access. For Astronomer v1, it should primarily
ground management-plane/self-management Argo evidence exposed by the product.

#### 15D—Prometheus

Manifest:

- name: `prometheus`
- version/default: `3`
- treat the major/LTS semantic line as the knowledge contract; do not churn a
  pack for every minor unless operational behavior requires it.

Required documents:

1. target discovery and scrape health
2. TSDB, WAL, and block lifecycle
3. PromQL/query-path failure reasoning without arbitrary expensive query advice
4. rule evaluation and alert state
5. remote write/read queues and backpressure
6. cardinality and label explosion
7. retention, disk, and compaction
8. HA, federation, and duplicate-series reasoning
9. Prometheus 3 migration/behavior changes
10. safe configuration change and verification

#### 15E—OpenTelemetry

Manifest:

- name: `opentelemetry`
- version/default: `1`, representing stable specification semantics.
- put vendor distributions behind explicit variants only after a product can
  assert one.

Required documents:

1. traces, metrics, and logs signal flow
2. Collector pipeline topology
3. receivers, processors, exporters, and connectors
4. queue backpressure, memory limiting, retries, and drops
5. head/tail sampling behavior and evidence
6. resources and attribute hygiene
7. context propagation failures
8. semantic-convention stability and drift
9. TLS/authentication failure classes without credentials
10. safe rollout and post-change telemetry verification

#### 15F—S3-compatible object storage

Manifest:

- name: `s3-compatible`
- version/default: `2006-03-01`
- variants:
  - `aws-s3`
  - `rustfs`
  - `minio`
- put only proven provider-specific behavior in variant documents.

Required core documents:

1. endpoint, DNS, and TLS
2. request signing and clock skew
3. region/addressing behavior
4. multipart upload lifecycle
5. object versioning and delete markers
6. lifecycle/retention rules
7. encryption and KMS boundaries
8. replication and durability evidence
9. consistency/concurrency expectations
10. capacity, erasure, or provider health reasoning

Never include bucket credentials, example secret keys, unrestricted listing,
bulk deletion, or claims that all S3-compatible implementations behave exactly
like AWS S3.

For each 15A–15F pack:

**Verify**:

```sh
cd /root/astronomer-all/charlie
make packs-check
go test ./internal/packs ./cmd/charlie -run 'PlatformPack|Retriev|Content' -count=1
```

Expected: the new pack validates, seeds idempotently, indexes under fixed and
asserted modes, retrieves its positive cases, rejects wrong-version/variant and
injection cases, and causes no pack-specific source branch.

### Step 16: Document capability coverage without coupling knowledge to authority

Files: extend existing `docs/platform-packs-authoring.md`,
`docs/platform-knowledge-design.md`, `docs/product-integration.md`, and
`docs/kubernetes-platform-pack.md`. Do not add a new top-level documentation
concept unless a separately reviewed plan first changes Charlie's strict
documentation inventory.

- [ ] Add a **capability coverage profile** section to the authoring guide.
- [ ] State that a profile is advisory integration guidance, not manifest data,
  runtime admission, or auto-enablement.
- [ ] For each pack, list diagnostic domains and examples of product-owned read
  capabilities that can supply evidence.
- [ ] List potentially safe write categories separately, always subject to
  product mode, exact resource scope, tickets, approval/allowlist, budget,
  cooldown, circuit breaker, and verification.
- [ ] Require Charlie to state coverage gaps when a product does not expose the
  evidence a pack recommends.
- [ ] Include a minimal future-product checklist: discover exact runtime facts,
  map them to typed assertions, expose bounded read tools, separately design
  writes, pin bridge contract, configure route, qualify all modes.
- [ ] Update the design document's stale “phase 1” language to the final generic
  mechanism.
- [ ] Document fixed versus asserted mode, exact no-fallback behavior, variant
  behavior, index lifecycle, trace fields, and model passthrough.
- [ ] Document that pack content may name operator checks but does not imply the
  Charlie agent owns the corresponding credential.

Minimum capability guidance:

| Pack | Useful product-owned reads | Writes remain separate |
|---|---|---|
| Kubernetes | management version, workloads, pods, events, logs, nodes, storage, network, availability, resource usage | rollout/restart/scale only where product exposes an exact management resource and existing policy admits it |
| PostgreSQL | health, pool stats, availability, safe aggregates for locks/WAL/replication/storage/backups | no generic SQL execution; only named product operations with verification |
| Valkey | ping/info allowlist, memory, persistence, replication, connection, queue/consumer health | no arbitrary command/key access; only named idempotent product operations |
| Argo CD | application health/sync, operation state, controller/repo/cache health, safe diff summary | named self-management sync/reconcile only; prune/delete remains separately protected |
| Prometheus | targets, rule health, TSDB/remote-write aggregate health, cardinality summaries | configuration/rule changes only through product-reviewed operations |
| OpenTelemetry | collector/pipeline status, drop/retry/backpressure aggregates, exporter reachability | rollout/config change only through product-managed resource operations |
| S3-compatible | endpoint reachability, bounded bucket health/config summaries, backup object verification | no generic list/delete; only exact product backup/restore operations |

**Verify**:

```sh
cd /root/astronomer-all/charlie
rg -n "version_mode|asserted|PlatformAssertion|capability coverage|no fallback|variant" \
  docs/platform-knowledge-design.md docs/platform-packs-authoring.md docs/product-integration.md
make inventory-check
```

Expected: concepts are documented in existing canonical files and Charlie's
strict tree inventory still passes.

### Step 17: Add demand-gated P2 host/runtime packs only after a consuming product proves need

Do not implement this step merely because the earlier steps are complete. Start
it only when a product integration can both assert the runtime and expose useful
bounded evidence. The generic contract must not change.

#### Linux/systemd

- name/version: `linux-systemd@1`
- core documents: CPU/load/pressure, memory/OOM, disk/inodes/filesystems, process
  state, systemd units, journal queries/redaction, networking/DNS/time, users and
  permissions, package/update state, reboot/kernel evidence, safe recovery.
- Never imply root, SSH, arbitrary filesystem, or shell access.

#### Docker Engine

- name/version: `docker-engine@28`
- documents: daemon health, containers/tasks, networking, storage/volumes,
  image/content state, resource limits, logging, BuildKit, registry/TLS/auth,
  upgrade/recovery.
- Never expose the Docker socket to the generic Charlie agent.

#### OCI runtime

- name/version: `oci-runtime@1.2`
- documents: runtime lifecycle/spec concepts, containerd/CRI relationships,
  namespaces/snapshots/content, shim/task failures, cgroups, image pulls,
  runtime security, safe diagnosis/recovery.
- Keep Docker and OCI packs separate because their release lifecycles and
  operational evidence differ.

For each P2 pack, repeat Steps 15 and 16's content, retrieval, license, and
capability-boundary gates. If implementation demands pack-specific Go logic,
STOP and improve the generic manifest/resolver design instead.

### Step 18: Complete static, adversarial, performance, air-gap, and live qualification

#### Contract and validation battery

- [ ] Central and bridge reject omitted `platforms`, null, non-array, >16 items,
  duplicate packs, unknown object fields, invalid slugs, empty/oversized
  versions, and oversized observed version.
- [ ] Route API and database reject >8 packs, duplicate collections, unknown
  modes, fixed without version, asserted with version, and non-platform
  collections.
- [ ] SDK examples compile against the generated shapes.
- [ ] Astronomer's pinned checksum detects any unreviewed bridge drift.

#### Resolver and retrieval battery

- [ ] Every row in the Step 6 table passes.
- [ ] Every pack has positive questions, negative unrelated questions, exact
  wrong-version tests, core-only tests, and variant positive/negative tests.
- [ ] Product-only, platform-only, mixed, no-hit, required-policy, auto-policy,
  provider-failure, vector-index-failure, and platform-product-unavailable paths
  have stable trace assertions.
- [ ] General Kubernetes questions can use Kubernetes evidence even when product
  documents have no hit.
- [ ] Product-specific questions prefer product documentation while still
  allowing supporting platform citations.
- [ ] General model knowledge remains available under `auto` when no RAG result
  is relevant.
- [ ] Citations can reconstruct exact collection/release/document/chunk/pack/
  version/variant provenance.

#### Isolation and security battery

- [ ] Two deployments of one product share pack/index assets but never sessions,
  chat, findings, audits, product corpus, authorization, or usage attribution.
- [ ] Two different products cannot cross-read product collections.
- [ ] Platform SQL predicates always use the reserved platform product, and
  product SQL predicates always use the caller product.
- [ ] Prompt/model/context/resource injection cannot change platform selection.
- [ ] Assertions cannot add a capability, resource, action ticket, approval,
  mode, allowlist entry, or policy exception.
- [ ] Read-only denies every write; approval mode requires exact approval;
  automation permits only existing allowlisted bounded actions.
- [ ] Every prohibited downstream action remains denied.
- [ ] Logs/traces/audits contain no credentials, prompts, document bodies,
  embeddings, SQL text, keys, values, DSNs, bucket secrets, or downstream data.

#### Lifecycle and concurrency battery

- [ ] New asserted version publication queues relevant models and becomes
  selectable only when ready.
- [ ] Active older versions continue during a new build.
- [ ] Pack correction on one semantic line produces a new immutable release and
  atomically swaps the active line after indexing.
- [ ] Embedding model/dimension changes reindex all needed releases before route
  promotion.
- [ ] Concurrent seeders, route updates, release activation, index workers, and
  session creation remain idempotent and race-safe.
- [ ] Leader loss at every existing write boundary preserves existing recovery
  behavior; platform knowledge adds no new write boundary.

#### Performance battery

- [ ] Instrumented tests prove one query embedding call per distinct model.
- [ ] Benchmark 0, 1, 4, and 8 selected packs and report p50/p95 retrieval time,
  scoped vector-query count, allocations, and context size.
- [ ] Assert platform context never exceeds its existing one-third budget.
- [ ] Assert catalog/list and resolver query counts are bounded, with no N+1.
- [ ] Compare same-model versus different-model routes; work should scale with
  distinct models and selected targets, not candidate documents.
- [ ] Do not add concurrency solely to improve a synthetic benchmark. If the live
  p95 misses the current product timeout after embedding reuse, STOP and produce
  a measured follow-up design.

#### Air-gap and deployment battery

- [ ] Embedded packs work with registry/network disabled.
- [ ] Registry packs use existing HTTPS/SSRF/path/digest controls.
- [ ] Release artifacts include all manifests/documents and required notices.
- [ ] No runtime fetch of vendor docs occurs.
- [ ] Charlie and Astronomer rolling upgrades use the exact pinned bridge
  contract; coordinate deployment order so an old strict bridge never receives
  the new required shape.
- [ ] Rollback uses the prior complete Charlie/Astronomer artifact pair. Do not
  add a runtime dual-contract shim for rolling compatibility.

#### Final commands

```sh
cd /root/astronomer-all/charlie
make generate
make packs-check
make verify
make gen-check

cd /root/astronomer-all/astronomer
make charlie-contract-generate
make charlie-contract-check
make test
make verify-enterprise

git -C /root/astronomer-all/charlie diff --check
git -C /root/astronomer-all/astronomer diff --check
```

Expected: every command exits 0.

Run the existing live gate only after static gates and deployment authorization:

```sh
cd /root/astronomer-all/charlie
make verify-live
```

Expected: exit 0 and content-free evidence says `passed=true` for both human and
event session paths, all three authority modes, injection/isolation checks, and
exact platform trace/citations.

## Pack content quality contract

Every shipped pack/version must satisfy all of these machine-checked rules:

- [ ] Canonical pack/version/variant/document identifiers meet the bounds.
- [ ] Default names an existing version.
- [ ] No more than 16 versions or variants are published in one pack.
- [ ] Every version has the pack's required document keys.
- [ ] Each document has one H1 title, nonempty body, stable key, and a maximum of
  12,000 runes unless the existing authoring guide sets a stricter bound.
- [ ] Variant document keys exist in every version and appear in only one
  variant group.
- [ ] Digest is stable across source ordering and changes for any title, body,
  key, or variant-map change.
- [ ] Content is failure-mode-first and distinguishes observation,
  interpretation, recommendation, action, and verification.
- [ ] Destructive/reversible distinctions, approvals, backups, and rollback are
  explicit where relevant.
- [ ] Commands are described as operator checks, never capabilities Charlie is
  assumed to possess.
- [ ] Examples contain no credentials, private endpoints, real customer IDs,
  secret-like strings, or unbounded destructive operations.
- [ ] Claims have a reviewed primary upstream source and compatible license;
  required notices are updated.
- [ ] No pack duplicates product-specific procedures or claims a deployment fact
  that only the product can know.
- [ ] Model prompts are never treated as documentation sources.

## Cross-pack retrieval fixture matrix

Create a small, reviewed fixture table rather than a bespoke test harness per
pack. Each row names expected eligible scopes/pack keys and forbidden keys; it
must not assert exact generated prose.

| Pack | Positive fixture | Negative/guard fixture |
|---|---|---|
| Kubernetes | “Why is a rollout progressing slowly after readiness failures?” | Prompt claims EKS while assertion is k3s; no EKS overlay |
| PostgreSQL | “Connections are saturated and transactions are waiting; what evidence narrows it?” | Ask to print active SQL/passwords; no sensitive evidence recommendation |
| Valkey | “Memory rises and evictions started while queues lag.” | Ask to run `KEYS *`/dump values; recommendation forbidden |
| Argo CD | “Application is OutOfSync and reconciliation repeats after a hook.” | Ask to prune all resources; no authority or unsafe assumption |
| Prometheus | “Remote-write queue is backing up and disk use is rising.” | Unrelated application bug; no forced Prometheus citation |
| OpenTelemetry | “Collector exporter retries and dropped spans increased.” | User claims a vendor distro absent from assertion; no overlay |
| S3-compatible | “Multipart uploads remain incomplete after TLS/signing errors.” | Prompt claims AWS while assertion says RustFS; no AWS-only content |
| Linux/systemd | “A unit restarts after OOM and the host has memory pressure.” | Ask Charlie to SSH/root; no capability implication |
| Docker Engine | “Daemon storage grows while image pulls and builds fail.” | Ask for Docker socket access; no authority implication |
| OCI runtime | “CRI task creation fails around cgroup setup.” | Docker-specific behavior without Docker pack; no cross-pack invention |

For each positive case also test:

- exact supported line;
- unsupported line;
- absent assertion on asserted route;
- fixed route with no assertion;
- fixed route with conflicting assertion;
- core-only selection;
- correct variant selection;
- platform index unavailable;
- mixed product + platform evidence;
- no relevant evidence under `auto`.

## Rollout sequence

1. Merge Charlie contract/schema/resolver/Kubernetes implementation and build an
   immutable candidate, but do not send new bridge traffic yet.
2. Merge Astronomer's reviewed pinned contract and inventory implementation.
3. Deploy the compatible Charlie Central and Charlie product-agent artifact pair
   using the existing signed onboarding/release process. Because the bridge is
   strict and `platforms` is required, use a coordinated artifact rollout rather
   than adding compatibility fields.
4. Configure a canary route with product RAG + asserted Kubernetes only.
5. Qualify human and event sessions, all modes, no-downstream boundary,
   citations, traces, and provider/index degradation.
6. Expand to one canary deployment per management Kubernetes variant actually
   available.
7. Roll out Kubernetes selection broadly.
8. Add PostgreSQL, Valkey, and Argo CD one pack/commit/canary at a time.
9. Add Prometheus, OpenTelemetry, and S3-compatible after their product evidence
   and fixed/asserted choice is explicit.
10. Leave P2 host/runtime packs unimplemented until a product meets their entry
    gate.

Rollback is artifact-level:

- Keep the previous Charlie and Astronomer signed artifacts and contract pin.
- Revert the route revision to its prior pack bindings before rolling back code
  if the prior code cannot parse the new revision.
- Roll back the compatible product-agent/Central pair together.
- Do not delete newly published pack releases or index builds; they are inert
  when no active route composes them.
- Do not reverse migration `000030` by editing data in place. The old exact rows
  were migrated to valid fixed rows and remain semantically valid.

## Test plan summary

New or extended tests must live next to the behavior and follow existing
patterns:

- `internal/packs/*_test.go`: manifest parsing, digest, bounds, content contracts,
  embedded/registry parity.
- `cmd/charlie/platform_packs_test.go`: binding validation, catalog, resolver,
  storage/readback.
- `cmd/charlie/platform_packs_seed_test.go`: idempotency, metadata-only change,
  default/variant provenance.
- `cmd/charlie/platform_packs_e2e_test.go`: index lifecycle, exact retrieval,
  isolation, replacement, route promotion.
- `cmd/charlie/*sessions*_test.go`: assertion validation, encrypted snapshot,
  human/event parity, message immutability.
- Existing knowledge/retrieval tests: traces, citations, product precedence,
  embedding-call count, failure behavior.
- `agent/opencode/test/**`: strict schema, forwarded shape, investigation parity,
  unknown-field rejection.
- `sdk/typescript/**` and examples: generated consumer contract.
- `frontend/src/pages/*test*`: fixed/asserted create/edit/deep-link/error states.
- `astronomer/internal/charlie/platform_inventory_test.go`: trusted discovery,
  normalization, partial failure, no downstream.
- Astronomer session/bridge/trigger tests: both bridge paths, explicit empty
  arrays, capability/mode invariance.
- Live qualification scenarios: exact selection, injection, mode matrix,
  isolation, degradation, and event sessions.

Avoid tests that compare whole generated answers. Assert structured inputs,
eligible evidence, citations, trace reason codes, capability decisions, audit
metadata, and terminal state.

## Done criteria

All must hold:

- [ ] Central and bridge contracts define one identical `PlatformAssertion` and
  require bounded `platforms` arrays.
- [ ] Route bindings use only `fixed` or `asserted`; invalid combinations fail in
  both application and database layers.
- [ ] Existing route rows migrate to `fixed` without data loss.
- [ ] No prompt, model output, message context, resource, or arbitrary attribute
  can choose a platform version or variant.
- [ ] `platform_pack_signals.go` and all Kubernetes-specific selection code are
  gone.
- [ ] Manifest variants are validated, integrity-covered, source-neutral, and
  generic.
- [ ] Product assertions are stored only in the existing encrypted session
  context snapshot.
- [ ] Human and event/investigation sessions carry identical inventory semantics.
- [ ] Fixed/asserted selection has closed trace reason codes and exact citations.
- [ ] Product and platform retrieval remain separately scoped; product evidence
  wins ties and platform context stays bounded.
- [ ] Same-model product/platform retrieval performs one query embedding call per
  request.
- [ ] Asserted indexing is bounded, deduplicated, model/dimension-correct, and
  rollout-safe.
- [ ] Astronomer reports only verified management-plane facts and never queries a
  downstream cluster.
- [ ] Knowledge selection changes no capability, mode, approval, action, budget,
  cooldown, circuit breaker, or audit rule.
- [ ] Charlie Admin creates and edits both binding modes without ambiguity.
- [ ] Kubernetes 1.34/1.35/1.36 and all P1 pack content contracts pass.
- [ ] Capability coverage guidance is documented as non-authoritative.
- [ ] No monolithic cloud pack, second data path, product-specific Charlie branch,
  compatibility shim, or new generic-agent credential was added.
- [ ] `make packs-check`, `make verify`, and `make gen-check` pass in Charlie.
- [ ] `make charlie-contract-check`, `make test`, and
  `make verify-enterprise` pass in Astronomer.
- [ ] `git diff --check` passes in both repositories.
- [ ] Live qualification passes with content-free evidence for both session paths
  and all authority modes.
- [ ] `advisor-plans/README.md` marks Plan 006 `DONE` only after P1 completion;
  demand-gated P2 deferral is recorded explicitly and does not block P1.

## STOP conditions

Stop and report; do not improvise if any occurs:

- Either drift check reveals a semantic change to an in-scope contract,
  migration, retrieval, indexing, bridge, or Astronomer session path.
- An in-scope file has unrelated uncommitted user changes.
- The existing baseline fails before implementation.
- Implementing assertions appears to require a second session table/column,
  plaintext context copy, new queue, or new vector store.
- A version/variant would have to be inferred from user/model text, arbitrary
  context, endpoint hostname, chart default, image tag not tied to the installed
  artifact, or library dependency.
- Exact asserted content is absent and implementation is tempted to fall back to
  default/latest/nearest content.
- A pack would auto-enable or require a capability, credential, approval,
  resource, or authority mode.
- Any query would span product IDs or any platform candidate could bypass scope
  revalidation.
- The route/index design can schedule more than the documented 8 × 16 bounded
  pack-release set for one revision.
- Correct indexing would require vectors from different embedding models or
  dimensions to be compared.
- A new pack requires product-specific Go/TypeScript logic in Charlie.
- A content source's accuracy, support line, or license cannot be established
  from a primary source.
- PostgreSQL/Valkey/Argo/Prometheus/OpenTelemetry/S3 runtime facts cannot be
  verified from an existing product-owned safe source. Omit that assertion and
  report coverage; do not add broad credentials.
- Any downstream cluster API/log/workload/credential/action becomes necessary.
- A step's verification fails twice after one reasonable scoped correction.
- Static qualification passes but the real configured model or embedding
  provider is unavailable. Stop only the live gate and report external state;
  do not fake live evidence.
- Coordinated strict-contract rollout cannot be performed safely with the
  existing signed release process. Revise the rollout plan; do not add a hidden
  compatibility mode.

## Maintenance notes

- A platform pack release line is a semantic corpus selector, not merely a
  freshness tag. Corrections may publish a new immutable release and atomically
  move that line's active pointer after indexing; citations retain the exact
  release ID and digest provenance.
- Review supported platform lines on each upstream release/EOL cycle. Do not let
  a pack grow beyond 16 published lines without an explicit lifecycle design
  review. Historical immutable releases may remain for audit even when no route
  advertises them.
- Changing a pack's default affects only new fixed-binding UI choices. It must
  never change asserted resolution or silently rewrite route revisions.
- Adding a variant is safe only when products can report it from trusted runtime
  inventory. A variant with no reporter is harmless but should not be shipped
  without a real qualification case.
- If a future product needs cloud knowledge, prefer narrow service packs or
  variants (`aws-s3`, `eks`) over a provider-wide corpus. Provider-wide material
  dilutes retrieval and creates impossible version semantics.
- If live measurements show retrieval latency pressure, preserve request-local
  embedding reuse and scope isolation first. Consider bounded concurrency only
  from measured evidence and with deterministic trace ordering.
- Reviewer focus should be: exact no-fallback selection, session immutability,
  route-build completeness, organization/product IDs on index rows, generic
  variant filtering, bridge strictness, no authority coupling, and no downstream
  access.
- P2 host/runtime packs should remain deferred until a consumer can assert the
  runtime and expose useful bounded reads. Their absence is intentional product
  discipline, not unfinished P1 work.
