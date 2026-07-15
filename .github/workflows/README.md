# GitHub Actions

## Authoritative enterprise verification

`scripts/verify-enterprise.sh` is the single source of truth for the local and
CI release-integrity gates. The default is the complete gate; dependency
installation remains an explicit prerequisite so verification never changes a
lockfile or resolves a different dependency graph:

```bash
cd frontend && npm ci && cd ..
make verify-enterprise
```

The Make target accepts `VERIFY_SCOPE=backend|frontend|helm|all` and defaults to
`all`. CI uses the same scoped script directly so independent jobs can run in
parallel:

```bash
./scripts/verify-enterprise.sh backend
./scripts/verify-enterprise.sh frontend
./scripts/verify-enterprise.sh helm
./scripts/verify-enterprise.sh api-contract
```

The scopes are intentionally additive rather than quick approximations:

- `backend` runs migration safety, sqlc generated drift, `go build`, `go vet`,
  the complete normal and race suites, and API/OpenAPI/generated/embed/route/
  error-code contracts.
- `frontend` runs code-health, lint with zero warnings, type-check, unit tests,
  the production build, and `npm audit`. The default audit threshold is
  `moderate`, preserving the existing CI policy and therefore covering the
  enterprise high/critical minimum. Set `NPM_AUDIT_LEVEL=high` only when
  reproducing the minimum policy explicitly.
- `helm` rebuilds dependencies from `Chart.lock`, lints, produces development
  and fully wired production renders, and runs `go test ./deploy/ -count=1`.
- `api-contract` is the focused diagnostic gate used by `make verify` and the
  dedicated API workflow. It does not replace the complete backend scope.

Logs, test reports, Helm stderr, and rendered manifests are written to
`${VERIFY_ARTIFACT_DIR}`. The local default is
`${TMPDIR:-/tmp}/astronomer-verify-enterprise`; CI uploads this directory when a
gate fails. The script has no quick mode, and CI does not opt out of a failing
enterprise check.

Playwright and live-cluster validation are deliberately separate from
`verify-enterprise`: Playwright runs in the named `frontend-e2e` PR job, while
`smoke-fresh-cluster.yaml` owns k3d/live-agent validation. Keeping those suites
named makes their topology and failure artifacts explicit without serializing
the backend, frontend, and Helm matrix.

## Active workflows

### `pr-validation.yaml` — pull-request quality and security gates

Runs on every pull request to `main` and on manual dispatch. It covers:

1. `./scripts/verify-enterprise.sh backend`, plus the stateful migration
   roundtrip smoke that intentionally remains a distinct integration step.
2. `./scripts/verify-enterprise.sh frontend` after `npm ci`.
3. A named Playwright end-to-end job with its own browser setup and report.
4. `./scripts/verify-enterprise.sh helm`, including locked dependency build,
   lint, both renders, and chart contract tests.
5. The unchanged container supply-chain matrix: build the server, worker,
   agent, migrate, shell, and frontend images, scan high/critical fixed
   vulnerabilities, and upload SPDX SBOMs.

Backend, frontend, Helm, and API contract jobs upload their command logs or
partial renders on failure. Playwright uploads its HTML report on every
non-cancelled run.

### `api-contract.yaml` — API contract gate (A5)

Runs on every pull request to `main`, on push to `main`, and on manual
dispatch. Frontend dependencies are installed first because the OpenAPI `.mjs`
scripts resolve `js-yaml` from `frontend/package.json`, then the job runs:

```bash
./scripts/verify-enterprise.sh api-contract
```

That scope verifies:

1. `go build ./...` and `go vet ./...`.
2. `go test` for the API packages: `internal/handler`, `internal/server`,
   `internal/auth`, `internal/server/middleware` (`-count=1`).
3. `node scripts/openapi-coverage.mjs --check` — fails on spec/route drift.
4. `node scripts/generate-openapi-types.mjs --check` — fails when the
   committed `frontend/src/types/openapi.generated.ts` is stale.
5. `go test ./internal/server/ -run RouteTable -count=1` — golden route table.
6. `go test ./internal/handler/ -run TestApierrorCatalogCoverage -count=1` —
   apierror catalog lint.

`make verify` runs the same focused sequence locally. The broader
`make verify-enterprise` remains the release-integrity answer.

### `release.yaml` — signed release pipeline (T12)

Fires on `v*.*.*` tag push (also dispatchable manually for hotfix tags from
non-tag commits). For each first-party chart image (`server`, `worker`,
`agent`, `migrate`, `shell`, `frontend`) it:

1. Builds the image with `docker buildx` and pushes to
   `ghcr.io/<owner>/astronomer-go-<component>:<tag>` plus `:latest`.
2. Signs the image by **digest** (not tag — signing by mutable tag is a
   forgery risk) with cosign keyless via the GitHub OIDC token →
   Sigstore Fulcio short-lived cert → Rekor transparency log.
3. Attaches Buildx provenance for the pushed image digest.
4. Generates an [SPDX](https://spdx.dev/) SBOM with
   [syft](https://github.com/anchore/syft).
5. Attaches the SBOM with `cosign attest --type spdxjson` so consumers
   can `cosign verify-attestation` to retrieve it.
6. Uploads the SBOM as a workflow artifact (90-day retention) for
   browsing without a registry pull.

The verifier-side runbook at
[`../../docs/verify-images.md`](../../docs/verify-images.md) documents how
procurement / supply-chain teams reproduce the cosign + syft verification
before pulling our images into an internal registry mirror.

### `smoke-fresh-cluster.yaml` — fresh-cluster end-to-end smoke (T2.1)

Drives the full operator-onboarding flow against a real k3d cluster on
every PR to `main` and nightly: wizard registration → agent install →
baseline operators → kubectl shell open → trivy vuln reports flowing.
This catches the class of regression that the bitnami/kubectl:1.31
404, the SQLSTATE 42P08 migration bug, the cert-manager unmarshal
bug, and the phase-machine stuck-on-`failed` bug all share — nothing
else in CI exercised the fresh-cluster registration path end-to-end,
and each sat in `main` for at least a week before a human surfaced
it manually.

Hard cap: 25 minutes per run. The script's per-step timeouts add up
to ~13 minutes worst case; the slack covers k3d provisioning + image
pulls. On failure, the workflow uploads kubectl logs from the
management cluster + the smoke cluster's agent pod as a `smoke-debug-*`
artifact retained 7 days.

## Disabled workflows

`workflows-disabled/` holds workflows that are intentionally inert (GitHub
Actions only auto-loads `.yml`/`.yaml` from `.github/workflows/`).

```bash
mv .github/workflows-disabled/<name>.yaml .github/workflows/<name>.yaml
```
