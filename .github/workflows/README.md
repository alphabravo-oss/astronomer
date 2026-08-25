# GitHub Actions

## Authoritative enterprise verification

`scripts/verify-enterprise.sh` is the authoritative cluster-safe static gate
used locally and in CI. Its default `all` scope is complete only for static,
generated, unit/race, API, frontend, and Helm verification; dependency
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
  `scripts/check-go-lint.sh` (golangci-lint, pinned, against `.golangci.yml`),
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
static check. Its manifest enumerates every parallel and protected
qualification still required. Release integrity is decided only by the
protected promotion approval aggregator after it verifies their exact signed
artifacts.

Browser and live-cluster validation are deliberately separate from
`verify-enterprise`. Required lanes cover Playwright desktop/tablet/mobile,
visual baselines, and a disposable live browser stack with Trivy enabled;
`smoke-fresh-cluster.yaml` owns fresh k3d/live-agent adoption. Their named jobs
and retained artifacts keep topology explicit without serializing the static
backend, frontend, and Helm matrix.

PostgreSQL failover is likewise an explicit, parallel PR certification job so
the Docker-based physical-replication drill does not make the default local
`make verify-enterprise` loop impractically slow. Run the identical lane with
`make test-postgres-failover-certification`; CI retains its schema-versioned
RPO/RTO evidence and database logs for 90 days.

## Active workflows

### `pr-validation.yaml` — pull-request quality and security gates

Runs on every pull request to `main` and on manual dispatch. It covers:

1. `./scripts/verify-enterprise.sh backend`, plus the stateful migration
   roundtrip smoke that intentionally remains a distinct integration step.
2. The parallel PostgreSQL physical-replication failover certification, with
   zero-row RPO and 30-second RTO thresholds and retained machine evidence.
3. `./scripts/verify-enterprise.sh frontend` after `npm ci`.
4. Required Playwright desktop/tablet/mobile, visual, and disposable live-stack
   browser lanes, including Trivy, with retained reports.
5. `./scripts/verify-enterprise.sh helm`, including locked dependency build,
   lint, both renders, and chart contract tests.
6. The unchanged container supply-chain matrix: build the server, worker,
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
4. `node scripts/openapi-request-fields.mjs --check` — field-level request
   contract: compares each documented request schema against the Go struct that
   decodes it (associated by an `// openapi:request <Schema>` marker on the
   struct) and fails on drift in either direction. `openapi-coverage` only
   proves the path still exists; this is what catches a field that the handler
   reads but the spec never mentions, or vice versa.
5. `node scripts/generate-openapi-types.mjs --check` — fails when the
   committed `frontend/src/types/openapi.generated.ts` is stale.
6. `go test ./internal/server/ -run RouteTable -count=1` — golden route table.
7. `go test ./internal/handler/ -run TestApierrorCatalogCoverage -count=1` —
   apierror catalog lint.

`make verify` runs the same focused sequence locally. `make verify-enterprise`
is the authoritative cluster-safe static verification gate; it emits a closed,
commit-bound manifest that explicitly lists the protected qualifications it
cannot execute locally. Release integrity is decided by the protected
`release.yaml`/`resume-release.yaml` promotion jobs, which download and verify
the exact signed RC, cloud, scale/audit/sizing, Rancher automated+human, and
assistive-technology evidence named by the digest-bound release approval.

### `release.yaml` — qualified immutable release pipeline (T12)

Fires only on a stable `vX.Y.Z` tag in the public
`alphabravo-oss/astronomer` repository. The tag must point at public `main`,
and the tag, chart, binary, frontend, Dockerfile, and default image versions
must agree. For each first-party chart image (`server`, `worker`, `agent`,
`migrate`, `shell`, `frontend`) it:

1. Builds the image with `docker buildx` and pushes only the immutable exact
   tag under `ghcr.io/alphabravo-oss`.
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
7. Pushes the exact OCI chart, then installs that published chart and its
   remote images on a clean k3d cluster with the pinned Gateway API/NGF pair.
8. Only after qualification, promotes the six images to the mutable `latest`
   convenience channel and creates the GitHub Release with chart, SBOMs, and
   checksums. Production upgrades always use the exact tag/version, not
   `latest`.

If the tag workflow is interrupted after publishing its immutable images or
chart, do not rerun it and do not move the tag. `resume-release.yaml` accepts
the tag and original run ID, then revalidates the public-main commit, original
run identity, unexpired artifact set, signed image digests, multi-platform
indexes, and byte-identical OCI chart. It repeats the clean-cluster install and
only then performs the withheld `latest` promotion and GitHub Release creation.

The verifier-side runbook at
[`../../docs/verify-images.md`](../../docs/verify-images.md) documents how
procurement / supply-chain teams reproduce the cosign + syft verification
before pulling our images into an internal registry mirror.

### `smoke-fresh-cluster.yaml` — fresh-cluster end-to-end smoke (T2.1)

Drives the full operator-onboarding flow against a real k3d cluster on
every PR to `main` and nightly: wizard registration → agent install →
catalog-defined Flux baseline → kubectl shell open → API health checks.
This catches the class of regression that the bitnami/kubectl:1.31
404, the SQLSTATE 42P08 migration bug, the cert-manager unmarshal
bug, and the phase-machine stuck-on-`failed` bug all share — nothing
else in CI exercised the fresh-cluster registration path end-to-end,
and each sat in `main` for at least a week before a human surfaced
it manually.

Hard cap: 40 minutes per run. The script's per-step timeouts add up
to ~13 minutes worst case; the slack covers k3d provisioning + image
pulls. On failure, the workflow uploads kubectl logs from the
management cluster + the smoke cluster's agent pod as a `smoke-debug-*`
artifact retained 7 days. Independently, every completed smoke invocation
atomically emits a sanitized `astronomer-fresh-cluster-smoke/v1` JSON manifest
bound to the commit, Actions run identity, tested image identities, Kubernetes
and Flux versions, completed checks, and timestamps. The workflow uploads that
manifest with `if: always()` and retains it for 90 days; it contains no login,
registration, agent, or API credentials.

## Disabled workflows

`workflows-disabled/` holds workflows that are intentionally inert (GitHub
Actions only auto-loads `.yml`/`.yaml` from `.github/workflows/`).

```bash
mv .github/workflows-disabled/<name>.yaml .github/workflows/<name>.yaml
```
