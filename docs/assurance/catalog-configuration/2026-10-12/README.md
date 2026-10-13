# Catalog configuration verification — 2026-10-12

This change adds catalog-owned exact-version choices, forms and presets. The
producer repository retains the chart render receipts in `docs/evidence/034/`.
Enterprise verification manifests and their original logs are retained alongside
this document after the final source-frozen runs; inspect their individual
subgate results and source-tree hashes for the precise validation boundary.

The desktop (1280×800) and mobile (390×844) images use the actual application
configuration page with an explicitly synthetic API adapter. No cluster mutation is
performed. The fixture is `frontend/tests/browser-components/configuration.html`.
It imports the reviewed Loki 7.3.0 profile, copied into the isolated fixture.

The T3 walkthrough verified:

- The recommended version and explicit other-upstream checkbox.
- Persistent preset selection shows changed paths before Apply.
- Applying the persistent preset enables persistence and sets a 50Gi volume.
- Entering an invalid size produces a field-path validation error.
- Changing the volume to 80Gi and selecting a StorageClass works visually.
- Unknown values, existing Secret references, storage settings and a custom array
  survive Form → YAML → a different version → original version → Form.
- Both viewports avoid horizontal overflow; the footer remains visible while
  the document scrolls.

Unit tests independently exercise denied Secret-list access, namespaced queries,
off-page references, pagination continuation, invalid YAML and cyclic aliases,
invalid field types, preset review, archive identity drift, unavailable versions,
Kubernetes compatibility and upgrade constraints. The existing signed-OCI
producer/consumer interoperability test consumes the actual generated catalog.

These are source, rendering and browser checks. They do not constitute live
install/upgrade/rollback qualification, architecture qualification or an Assured
support promise. Longhorn 1.12.1 remains unavailable because its downloaded
archive differs from the retained catalog lock.


The final UI uses dedicated configuration routes for application installs,
application upgrades and Platform Tools. Focused tests cover direct chart-ID
resolution, unavailable project scope, disallowed cluster targets, navigation
without mutation, saved upgrade settings and explicit confirmation. T3 also
checks the multi-release Tool page and its short confirmation dialog using the
synthetic `configuration.html?tool=1` fixture. Earlier modal screenshots are
historical; the configuration screenshots show the final full-page layout.

The released v1.2.1 SDK compile fixture passes without additional compatibility
exceptions. Configuration metadata travels on named chart-version records and
in the tool preview's separate `catalog_versions` map, preserving the legacy
anonymous chart-values and tool chart-row types. The source-bound SDK review
hash is refreshed for the generated client; its exception lists are unchanged.

## Final verification

- Backend enterprise static verification passed, including the full Go test and
  race suites, build/vet/lint, vulnerability scan, generated SQL/API checks,
  released SDK compatibility and release contracts. The optional live agent
  identity check was skipped because no test context was supplied.
- Frontend enterprise static verification passed: **356 test files / 2,465
  tests**, lint, type checking, staged formatting, generated-code health,
  production build, route-tree checks and dependency audit (zero vulnerabilities).
- All bundle budgets passed: bootstrap 382,701 gzip bytes, login 410,878 and
  application 469,949. The configuration component lives outside the route
  definition so the router can load the editor on demand.
- Catalog validation passed all **49 tests**, Ruff and Mypy. All **22 presets
  across 20 offerings** passed archive-digest/schema checks and Helm rendering
  for Kubernetes 1.31 and 1.35. The signed producer/consumer check passed.

The [backend manifest](backend-verification.json) and
[frontend manifest](frontend-verification.json) both record a stable source tree
through their respective runs. Original logs are in
[backend-verification.tar.gz](backend-verification.tar.gz) and
[frontend-verification.tar.gz](frontend-verification.tar.gz). Every retained
artifact SHA-256 was verified against its manifest before packaging.

The backend run preceded the final frontend-only route component extraction,
operation timeline inventory test correction and generated code-health report.
No backend, API or catalog source changed after that run. The final frontend run
includes those changes. Only this verification documentation, evidence packaging
and the plan's final results were added after the frontend run.

Changes remain local and staged. Publication, merging, deployment source-pin
updates and live lifecycle qualification have not been performed. The deployment
chart still references the older published catalog; release the generated
immutable catalog payload and update that pin together with this consumer.
