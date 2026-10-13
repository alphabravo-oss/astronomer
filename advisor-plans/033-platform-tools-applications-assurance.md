# Platform Tools, Applications, and offering assurance

Status: assurance, terminology and catalog-driven version/forms implementation complete. Finite validation evidence is linked below. Created 2026-10-12.

## Outcome and scope

Provide two understandable entry points: **Platform Tools** for guided cluster
capabilities and **Applications** for general catalog browsing and installed
releases. Support classification is independent: **Assured**, **Community**, or
**Experimental**. Every installation identifies its cluster, namespace(s),
release(s), management owner, and project context where applicable. Catalog
integrity must never imply functional certification.

Implement in `astronomer` and `astronomer-catalog`; preserve the catalog branch's
existing unpublished work. No application is promoted without actual evidence.
No customer workload, shared cluster, cloud resource, or release is changed by
this implementation. Live qualification requires an explicitly owned test estate
and release identities; local mocked tests are not substitutes.

## Decisions

- Keep existing `/tools`, `/apps`, catalog URLs, permission names, and v1 support
  enum values compatible. Translate user-facing terminology centrally.
- Platform Tools retain guided presets and ordered multi-chart operations.
  Applications retains general Helm browsing and the shared installed inventory.
- A Tools-owned release is visible in Applications and directs management back
  to Platform Tools. Existing-release guidance is advisory when inventory is
  paginated; server ownership/duplicate protections remain authoritative.
- Automatic metrics components remain separately owned platform components.
- Community means Astronomer does not assure/support the application's behavior;
  it does not imply an upstream commercial support contract.
- Experimental means evaluation without a production assurance commitment.
- Assured describes a named, tested configuration of an exact chart version and
  digest, with pinned workload images, tested environments, evidence, owner, and
  support expiry. Changing configuration/version is outside that profile until
  requalified. Repositories, signatures, product ownership and UI presets do not
  establish assurance.
- Selected upstream versions stay pinned. Features, security fixes, upstream EOL,
  or platform incompatibility trigger reviewed updates; do not auto-upgrade
  customer installations or fork upstream products by default.

## Repository responsibilities

| Repository | Responsibility |
| --- | --- |
| astronomer | UI, installation ownership, API/lifecycle qualification runners, diagnostics |
| astronomer-catalog | Exact artifacts, assurance metadata/evidence gates, update review, signed publication |
| astronomer-testbed | Repeatable supported Kubernetes environments |
| astronomer-full-test-iac | Existing local qualification estate; future consolidation into testbed |
| astronomer-examples | Small delivery and storage regression fixtures |
| astronomer-docs | Published support scope, versions, upgrade/recovery instructions |

Consolidating infrastructure and updating the separate docs publication are
follow-on repository operations; this change documents the canonical policy in
current source and does not erase either existing test harness.

## Implementation tasks and acceptance

### 1. Terminology and navigation

- [x] Rename cluster navigation, page headings, installed-owner links and related
  operator guidance to Platform Tools / Applications.
- [x] Keep public routes and API permission identifiers unchanged.
- [x] Explain the guided-versus-general distinction and automatic components.

### 2. Honest support classification

- [x] Add a shared status resolver and badge with scope explanations.
- [x] Map upstream to Community, experimental/unproven first-party to Experimental.
- [x] Require a valid, nonexpired, trusted, version-bound assurance profile for
  Assured; display the exact profile and limitations rather than certifying
  arbitrary values or future versions.
- [x] Show support independently on application presentation and Platform Tools.
  Missing/failed metadata must never grant assurance.
- [x] Replace the blanket green verified banner with the actual integrity status
  and state explicitly that integrity is distinct from application qualification.
- [x] Remove unsupported support claims from the development catalog.

### 3. Installation destination and ownership

- [x] Shared destination summary in global chart, cluster app, and tool configuration pages:
  cluster name plus stable identity; project context; namespaces; release names;
  versions; owning management page.
- [x] Explain that operators/CRDs/RBAC may affect the entire cluster, and that a
  namespace is not an authorization boundary for all installed resources.
- [x] Keep the summary visible while editing values and reviewing installation.
- [x] Link already-installed applications to their owning management page;
  display namespace and ownership without offering a misleading duplicate action.
- [x] Preserve intentional multiple releases and all server authorization checks.

### 4. Catalog assurance and upstream policy

- [x] Extend the compatible artifact metadata with an optional assurance profile.
- [x] Producer validation rejects unsupported assured claims, mismatched versions
  or chart digests, expired windows, incomplete checks and absent evidence.
- [x] Evidence identifies actual workload images, tested environments, values,
  qualification time, owner, and retained report hash; no fabricated receipts.
- [x] Add reviewed update-policy documentation and a scheduled read-only upstream
  version report; failures/unknown results stay explicit; no automatic promotion.
- [x] Regenerate catalog outputs using the builder and check deterministic output.
- [x] Document the distinction between build-tool SBOM and workload SBOM.

### 5. Qualification and release workflow

- [x] Document the promotion checklist and mapping to existing offering runner.
- [x] Validate implementation with focused negative/positive tests, frontend
  type/lint/full-unit gates, catalog validation/type/lint gates where the pinned
  toolchain is available, and applicable repository checks.
- [x] Walk through UI using T3 preview with an isolated fixture when a live
  authenticated server is unavailable; label fixtures as such.
- [x] Record exact commands, outcomes, limitations and outstanding external work.

## Added workstream: catalog-driven versions and visual configuration

Implemented in the follow-up change on 2026-10-12. The producer now owns
exact-version configuration records, and both application install entry points
use one catalog-driven editor. The former chart-name-only field/default overlays
have been removed. Validation evidence and its limits are recorded in
[the configuration verification report](../docs/assurance/catalog-configuration/2026-10-12/README.md).

### Contract and ownership

Use the catalog's existing JSON source and JSON Schema contracts. YAML may be
introduced as an authoring format later, but must compile into this single
canonical contract; do not maintain duplicate authoritative JSON and YAML.
Each `(repository, chart, exact chart version, chart digest)` record owns:

- Availability, recommended default, deprecation/replacement and revocation.
  "Recommended" means the curated choice; only evidence can grant "Assured".
- Application version separately from chart version; Kubernetes/architecture
  compatibility; qualification profile, support expiry and upgrade constraints.
- Exact upstream values schema plus reviewed Astronomer presentation metadata:
  grouped sections, field ordering, labels, help text, control types, enums,
  required fields, bounds, patterns, defaults and conditional visibility.
- Named presets (for example development and production), their version-bound
  effective values, resource/storage requirements and qualification scope.
- Secret references, storage classes, ingress classes and namespace choices
  resolved through authorized, target-scoped APIs. Catalog text must never embed
  credentials. Capability-specific resource pickers need explicit semantics.

### Implementation tasks

- [x] Extend `catalog-entry-version-v1.schema.json` and producer validation for
  per-version configuration/presets and recommendation metadata. Validate schema
  references, declared field paths, preset types, defaults, dependencies and
  every catalog artifact identity. Keep retained supported versions available.
- [x] Move the monitoring/logging curated definitions from chart-name-only
  frontend code into exact pinned catalog profiles first. Inspect the actual
  pinned chart schemas/templates and validate their presets with Helm before
  claiming the forms represent those versions. Migrate the remaining apps in
  reviewed batches; never silently reuse one version's overlay for another.
- [x] Extend the catalog consumer/API to expose those exact records. Version
  filtering and recommended-version lookup must happen server-side, so pagination
  cannot hide a supported pin or cause selection of an unrelated newest version.
  Preserve project/cluster authorization and installed-version upgrade context.
- [x] Default the picker to curated compatible versions and show support labels
  per version. Offer an explicit advanced view of other upstream versions with
  their actual support status. Preserve the current installed version on upgrade;
  disable revoked choices and explain why incompatible options are unavailable.
- [x] Feed the existing visual renderer from the selected version's schema and
  preset; make Form the primary path, with Review and Advanced YAML available.
  Add precise inline validation and grouped common/advanced fields rather than
  exposing every upstream value at once. Unknown YAML keys must survive edits.
- [x] Handle version/preset changes deliberately: show changed defaults and
  invalid/removed fields, preserve compatible user overrides and secret
  references, and never silently reset data-bearing/storage settings.
- [x] Have the server validate effective values against the selected version and
  enforce preflight and authorization regardless of which UI path submitted them.
  A browser-only schema or hidden control is not an enforcement boundary.
- [x] Use the same version/configuration definitions in Platform Tools where
  applicable, retaining tool composition/order and release ownership semantics.
- [x] Qualify each form/profile alongside its chart: default and custom presets,
  invalid inputs, conditional fields, secret/resource selectors, upgrade across
  schema changes, arrays, Form -> YAML -> Form round-trip, uncurated key retention,
  inaccessible/stale metadata, pagination, and mobile/accessibility behavior.

Acceptance: an operator can configure the supported monitoring/logging profiles
through visual controls without writing YAML; the picker clearly distinguishes
curated/Assured versions from unassured upstream alternatives; selecting a version
loads only its matching form and preserves explicit operator edits safely.

## Required live assurance matrix

Each supported profile must record exact Astronomer build, chart archive digest,
values digest, all workload image digests, Kubernetes distribution/version,
architecture, storage/network prerequisites, and immutable evidence identity.
Required cases: fresh install; functional canary (metrics/query/alert or log
write/read as applicable); access/tenant isolation; previous supported version
upgrade with existing data; recovery/rollback or an explicit unsupported-rollback
contract; uninstall/retained storage; vulnerability and workload SBOM review;
air-gap installation if advertised. Include failure/cancellation behavior and
cleanup. A healthy pod or rendered Helm chart alone is insufficient.

First candidates: one monitoring configuration and one logging configuration.
Keep both Experimental until all advertised dimensions pass. No claims for other
providers/architectures follow from a single local Kubernetes lane.

## Operating cadence and support

Daily upstream discovery produces a report only. Triage security/EOL notices
promptly and schedule regular version reviews; approve a support window per
profile rather than inventing a contractual response SLA. Every held update has
an owner and rationale. Candidate -> qualification -> reviewed signed catalog
release -> staged customer rollout. Expired or revoked profiles lose assurance.
Chart and application versions differ; inspect bundled dependencies/images.
Security remediation can require a new version even with no feature demand.

## Validation results and remaining work

Implemented on `implement/033-offering-assurance` in both repositories. The
catalog branch retains its pre-existing 12 unpublished commits; no history was
reset. This work is local and has not been pushed, merged, published or deployed.

### Executed checks (2026-10-12)

- Catalog: hash-verified Python **3.15.0** and uv **0.13.0**; synchronized and
  independently verified all **62** locked tool distributions without changing
  the dependency lock. System Python 3.14 is not a supported substitute.
- `PATH="$PWD/.venv/bin:$PATH" ./scripts/validate.sh`: **45 tests passed**;
  schemas, all artifact identities and all **35** generated outputs are current.
- `.venv/bin/ruff check scripts tests` and
  `.venv/bin/mypy --config-file pyproject.toml`: passed, including new scripts.
- Locked requirements export matched the retained hash-pinned reference;
  strict `pip_audit` found no known tool vulnerabilities. CycloneDX tooling
  SBOM emitted and validated. Its tool emits a root-component dependency warning;
  the full installed-version verifier separately checked every locked package.
  This tooling SBOM is not a workload SBOM or an application security attestation.
- Read-only upstream discovery completed against real upstream indexes: **15**
  newer stable chart candidates, **5** without a newer stable chart, **1**
  first-party manual review, **0** unknowns. Pins remain unchanged. This is a
  timestamped version observation, not a finding that those 15 pins are unsafe.
  Catalog evidence: `docs/evidence/033/` in `astronomer-catalog`.
- Existing Go producer/consumer interop test passed using the SHA256-bound
  regenerated catalog: valid signed OCI input plus rejection of wrong keys,
  missing/bad signatures, mismatched subjects/manifests/layers/documents,
  unsupported schema/reader versions and canceled work. Test command:
  `go test -tags catalogproducerinterop ./internal/catalog -run TestApplicationCatalogActualProducerSignedOCIConsumer -count=1 -v`,
  with `ASTRONOMER_W15_CATALOG_INPUT` and `ASTRONOMER_W15_CATALOG_DIGEST` bound
  to the current `generated/catalog.canonical.json` in the catalog repository.
- Documentation links and terminology gate passed. Complexity gate passed after
  extracting shared presentation components and **lowering**, not raising, the
  two pre-existing function ceilings (324 -> 321 and 283 -> 280).
- T3 collaborative browser: actual shared destination/support/modal components
  rendered in an explicitly synthetic fixture. Checked Platform Tools and
  Applications, multi-release namespaces, project/cluster IDs, changing the
  namespace, and desktop (1280x800) / mobile (390x844) layout. No horizontal
  overflow after correcting fixture button wrapping. This is component evidence,
  not an authenticated end-to-end installation or a live cluster qualification.
  Screenshots and consumer log: `docs/assurance/platform-offerings/2026-10-12/`.
- Full frontend subgates passed: **353 test files / 2,461 tests**, formatting
  contract test, lint, type check, production build/CSP, bundle budgets,
  unchanged generated routes and npm audit (**0 vulnerabilities**). The first
  complete run's final evidence was correctly rejected because source edits
  occurred during execution. The final frozen-tree run uses the repository's declared Node **26.11.1**
  and npm **12.2.0**, since the shell initially selected Node 22/npm 9.
  The aggregate gate **passed**, with `source_tree_stable: true` and all 10
  subgates passing. Evidence: [manifest](../docs/assurance/platform-offerings/2026-10-12/frontend-verification.json) and [unaltered logs](../docs/assurance/platform-offerings/2026-10-12/frontend-verification.tar.gz).
  Only this plan's completion record and packaging of retained evidence changed
  after the frozen run; implementation sources remained unchanged.

### External release tasks — still required

- [ ] Choose and own the monitoring/logging qualification estate and exact
  supported configuration/environment matrix; run all live lifecycle, functional,
  upgrade/data/recovery, isolation, workload security and air-gap checks above.
- [ ] Retain and review actual reports; create genuine hashed qualification
  receipts; only then promote those exact profiles. **No entries are Assured.**
- [ ] Review/merge the source changes, including the catalog branch's existing
  unpublished dependencies. Run protected hosted release gates, publish a signed
  catalog, and verify ingestion plus installation on staging.
- [ ] Activate daily upstream discovery through the merged default-branch
  workflow; assign a review owner. Local execution does not activate a schedule.
- [ ] Publish the corresponding operator/support documentation to
  `astronomer-docs` and consolidate test-estate ownership in `astronomer-testbed`
  as separate repository operations.

The implementation enforces honest support claims and provides the workflow; it
does not establish production assurance for all 21 applications. Local checks
must not be used to mark these external tasks complete.


## Configuration implementation outcome

All 21 offerings declare exact-version availability. Twenty have visual profiles
and 22 presets that pass immutable archive checks, configuration-schema validation
and Helm rendering against Kubernetes 1.31 and 1.35. Longhorn 1.12.1 is explicitly
unavailable: its downloaded archive SHA-256 differs from the existing lock. The
lock was not rewritten to conceal that drift. Re-enabling it requires upstream
artifact review and qualification.

The server retains curated pins, resolves recommended and requested versions by
exact identity before pagination, enforces per-version schemas and upgrade
constraints, and preserves authorization. The editor preserves user values across
versions and Form/YAML views, reviews preset changes, uses target-scoped resource
selectors, and explains Kubernetes incompatibility. Architecture declarations are
metadata; no architecture or workload lifecycle assurance is inferred from Helm
rendering. No offering has been promoted to Assured.

The earlier assurance-only evidence remains historical. The new configuration
report and enterprise manifests describe the follow-up code and verification.

## Full-page configuration correction

Install and upgrade configuration belong on full pages. Application actions now
open `/dashboard/catalog/configure` with chart/release, project and cluster IDs;
the page resolves authorized server records on direct navigation or refresh.
Platform Tool actions open `/dashboard/clusters/$id/tools/$slug/configure`.
Configuration uses the normal page scroll, a responsive action bar, existing
version-owned forms/presets, and short final confirmation dialogs. Uninstall and
rollback retain destructive confirmations. No form values or secrets are put in
URLs; unsaved edits are local to the page. Operation receipts return to the owning
inventory with their operation ID in the URL.

- [x] Replace application install and both upgrade entry points with the shared page.
- [x] Replace Platform Tool install/configuration dialogs with a dedicated route.
- [x] Resolve direct links by exact IDs and preserve project/cluster permission checks.
- [x] Keep ordered tool releases and persisted upgrade values intact.
- [x] Reserve install dialogs for confirmation; verify no mutation on opening the editor.
- [x] Verify application and tool layouts at desktop/mobile sizes and refresh route generation.

The configuration evidence supersedes the earlier modal screenshots. Live
qualification and immutable catalog publication remain separate release tasks.

Final local verification passed: backend enterprise static checks (including
full Go tests and race tests), frontend enterprise checks (356 files / 2,465
tests, production build, bundle budgets and zero audit vulnerabilities), and
49 catalog tests plus 22 preset renders on each of Kubernetes 1.31 and 1.35.
The [configuration verification report](../docs/assurance/catalog-configuration/2026-10-12/README.md)
retains source-bound manifests, original logs, browser evidence and the precise
validation/release boundary. The configuration editor loads on demand; the
initial application bundle remains within its existing budget.
