# Production security, disaster recovery, and supply-chain plan

**Status:** IN PROGRESS — priority 3 of 5

**Planned at:** `32100314e081e635e89f43fcf14673a25449ef63` on 2026-09-16, against the current dirty working tree

**Branch:** `advisor/013-production-security-dr`

**Depends on:** Plan 011 green baseline; coordinate envelope choices with Plan 012

**Scope boundary:** the management plane adopts existing clusters and uses Flux. No provisioning or Fleet components belong in this work.

## Implementation progress (2026-09-16)

- Backup and restore-drill pods no longer mount the platform ServiceAccount or
  any Kubernetes API token.
- The management log forwarder now reads host logs read-only, stores tail
  offsets on a dedicated writable `emptyDir`, and renders restricted container
  and seccomp settings with contract tests.
- Network-policy completeness, authenticated immutable backups, safe air-gap
  verification, and the remaining image/supply-chain gates are still required.

## Objective

Make the shipped production/air-gap/DR paths enforce the security properties they advertise. Completion means default-deny networking still permits narrowly authorized platform work, DR jobs carry no unnecessary Kubernetes authority, backups are confidential and tamper-evident with immutable recovery points, air-gap content is authenticated before extraction, and every production image is reproducibly identified and security-scanned.

## Findings covered (global rank 21–30)

| Rank | ID | Finding | Primary evidence |
|---:|---|---|---|
| 21 | NET-01 | Default-deny selects backup, restore-drill, and logging pods, but no allow policies exist for them | `deploy/chart/templates/networkpolicy.yaml:8-29,32-471`; `deploy/chart/templates/management-plane-backup-cronjob.yaml:30`; `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml:47`; `deploy/chart/templates/management-logging-daemonset.yaml:25` |
| 22 | RBAC-01 | Backup and restore jobs mount the main platform ServiceAccount with secret/token/RBAC powers | `deploy/chart/templates/management-plane-backup-cronjob.yaml:34-36`; `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml:51-53`; `deploy/chart/templates/serviceaccount.yaml:42,135` |
| 23 | DR-01 | Database dumps can be uploaded plaintext and the same principal can delete all recovery points | `deploy/chart/templates/management-plane-backup-cronjob.yaml:89-149`; `deploy/chart/values-production.yaml:342` |
| 24 | DR-02 | Key backups use unauthenticated AES-CBC and are extracted after decryption | `deploy/chart/templates/management-plane-backup-cronjob.yaml:151-186`; `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml:271` |
| 25 | AIRGAP-01 | Air-gap load extracts before mandatory signature/index validation; compatibility extraction is unsafe | `scripts/airgap-kit.py:203,316-363`; `docs/airgapped-install.md:88` |
| 26 | DEPLOY-01 | Production chart renders may fall back to mutable tags for first-party images | `deploy/chart/values.yaml:48,104`; `deploy/chart/templates/_helpers.tpl:94`; `deploy/chart/values.schema.json:464` |
| 27 | HOST-01 | Log forwarder mounts host logs read-write and lacks the chart's restricted security context | `deploy/chart/templates/management-logging-daemonset.yaml:56,104`; `deploy/chart/templates/management-logging-configmap.yaml:56`; `deploy/chart/values.yaml:1188` |
| 28 | SUPPLY-01 | Downloaded kubectl is not checksum-verified; mutable `apk upgrade` and Alpine runtimes weaken reproducibility/minimality | `deploy/docker/Dockerfile.shell:48-55`; `deploy/docker/Dockerfile.server:37`; `deploy/docker/Dockerfile.worker:38`; `deploy/docker/Dockerfile.agent:38` |
| 29 | CI-SEC-01 | No dedicated gosec/SARIF or full-history secret gate; reusable development credentials are committed | `scripts/verify-enterprise.sh:352`; `.golangci.yml:9`; `.github/workflows/pr-validation.yaml:17`; `Makefile:49`; `deploy/docker-compose.yml:63`; `deploy/k8s/03-secret.yaml:10` |
| 30 | TOOL-01 | Go/npm/container updates are manual and no separately tested FIPS artifact path exists | `.github/dependabot.yml:1-8`; `Makefile:83,138`; `deploy/docker/Dockerfile.server:17`; `deploy/docker/Dockerfile.worker:18`; `deploy/docker/Dockerfile.agent:18` |

## Out of scope

- Claiming a FIPS certification. The output must be accurately labeled as a capable/tested build path only to the extent evidence supports it.
- Embedding a registry into the air-gap bundle; the operator-owned registry model is retained.
- Opening broad egress to make tests pass.
- Deleting recovery points, rotating live credentials, or changing bucket retention without operator authorization.
- Replacing Flux or adding a cluster provisioner.

## Preflight and threat model

1. Preserve all current working-tree changes and re-open cited templates/scripts.
2. Render development and production values before changing anything. Save manifests as test artifacts, not tracked secrets.
3. Enumerate each selected pod's required network destinations: kube-dns, PostgreSQL, Kubernetes API, S3-compatible endpoint, OTLP/log sink, and nothing else.
4. Document the supported CNI semantics and the limits of namespace/pod selectors for external endpoints and API-server DNAT.
5. Inventory existing backup formats and determine the oldest format the restore drill must still read. New writes must use the new format; legacy reads need an explicit sunset.
6. Inventory credential literals without echoing their values. Treat every environment that reused them as needing rotation by its operator.

## Implementation steps

### 1. Add complete, narrow NetworkPolicies

- Add explicit egress policies for management backup, restore drill, and logging pods. Separate DNS, database, Kubernetes API, object storage/HTTPS, and configured log-sink rules.
- Scope DNS to configurable approved resolver CIDRs or kube-dns namespace/pod selectors; do not allow port 53 to every destination.
- Scope server/worker metrics ingress to configured Prometheus namespace/pod selectors or CIDRs rather than any pod.
- Production schema validation must reject enabled components whose required destination policy is absent.
- Add a render contract that inventories every chart-owned pod selected by default-deny and asserts an intentional ingress/egress policy. Add real CNI smoke tests for production values.

### 2. Remove Kubernetes authority from DR jobs

- Set `automountServiceAccountToken: false` on backup and restore pods and omit the main platform ServiceAccount.
- If Kubernetes API access becomes demonstrably necessary, create a dedicated ServiceAccount with only the exact resourceNames/verbs and add a test proving privilege escalation, secret listing, and token creation are denied.
- Keep database/object-store credentials in separately scoped Secrets and mount only the keys each job needs.

### 3. Enforce confidential, immutable backups

- Require either authenticated client-side encryption or explicitly configured and validated SSE-KMS/SSE-S3 for production. Prefer client-side AEAD when S3-compatible behavior is inconsistent.
- Store a signed/authenticated manifest covering archive checksum, schema/version, encryption key ID, creation time, source version, and restore prerequisites.
- Separate writer and retention/deleter identities. Validate bucket versioning/Object Lock or an equivalent immutable retention contract before production backup startup.
- Never prune with the backup writer's credential. Make retention a separately authorized workflow with dry-run/reporting.
- Extend the restore drill to verify authenticity, decrypt, restore, and check representative data without logging contents.

### 4. Replace CBC key wrapping with an authenticated envelope

- Use age or the same reviewed AEAD envelope selected in Plan 012. Authenticate before extraction.
- Validate archive member paths and expected file set before writing them; reject links, absolute paths, traversal, duplicates, and oversized entries.
- Add a versioned legacy-reader path for existing CBC backups only, with corruption warnings and a fixed migration/removal policy. Never write new CBC bundles.
- Test bit flips, truncation, wrong key, wrong manifest, traversal, excessive size, and successful legacy recovery.

### 5. Verify air-gap kits before extraction

- Make signature and trust-policy inputs mandatory for `pack`, `load`, and install paths in production mode.
- Verify the signed release BOM/manifest and artifact digest before opening/extracting the archive.
- Replace unrestricted `extractall` fallback with explicit safe-member validation on every supported Python version.
- Verify the internal index, every image digest, platform matrix, and expected file allowlist; reject unexpected/missing members.
- Keep the local-first registry model and amd64/arm64 support. Add offline negative tests for unsigned, wrong-signer, tampered, traversal, and digest-mismatch kits.

### 6. Require immutable production image references

- Extend the chart production conditional to require digests for every rendered first-party and third-party image, including backup/restore/logging helpers.
- Alternatively accept only a verified generated release mapping that resolves every image to a digest. Tags can remain for development.
- Add negative Helm tests for each missing digest and a positive release-mapping test. Ensure runtime reports both semantic version and digest/provenance.

### 7. Harden the management log forwarder

- Mount host log paths read-only. Move the tail-offset database to a dedicated writable `emptyDir`.
- Apply run-as-non-root where compatible, read-only root filesystem, seccomp RuntimeDefault, dropped capabilities, no privilege escalation, resource limits, and a narrow ServiceAccount.
- Document/validate distributions where host log permissions need a supplemental group. Do not make the mount read-write as a compatibility shortcut.

### 8. Make image builds minimal and reproducible

- Verify the official per-architecture kubectl checksum or signature before installation.
- Remove mutable `apk upgrade` operations. Pin necessary packages/repository snapshots or move Go service runtimes to distroless/scratch where operationally viable.
- Derive the Go builder version from `go.mod` through one automated consistency check; do not maintain divergent hard-coded versions.
- Retain non-root users, CA certificates where needed, SBOM, signing, SLSA provenance, and multi-architecture output.
- Add container-structure tests proving no shell/package manager in minimal service images unless an explicit debug image is selected.

### 9. Add security-specific CI and remove reusable literals

- Add pinned gosec with SARIF upload and a pinned secret scanner over full reachable history. Use `fetch-depth: 0` only for that isolated job and maintain reviewed, minimal allowlists.
- Generate per-install local development credentials into gitignored files/Secrets. Keep only templates and explicit setup commands in source.
- Make live E2E credentials mandatory inputs; remove fallback credential values.
- Do not automatically rotate external environments. Publish a rotation checklist for operators who reused old development material.

### 10. Automate currency and add a qualified compliance build

- Configure grouped, review-required update streams for gomod, npm, GitHub Actions, and Docker bases. Do not auto-merge majors.
- Add a reproducible `make updates`/currency report that identifies stale direct dependencies without modifying the tree by default.
- Add a separately named FIPS-oriented build lane using a supported crypto toolchain/base, cryptographic self-test, SBOM/provenance, and integration suite.
- Document precisely what is and is not certified. Standard and FIPS-oriented artifacts must be distinguishable and independently signed.

## Verification

```bash
helm lint deploy/chart
go test ./deploy/... -count=1
go test ./internal/releasecontract/... -count=1
go test ./... -count=1
cd frontend && npm run type-check && npm run lint && npm test
```

Also run the repository enterprise/release verification, image build/scan, air-gap negative suite, and a disposable-cluster CNI test. Expected results:

- every production image renders by digest;
- no default-denied enabled workload lacks a tested allow policy;
- DR pods have no Kubernetes token;
- a backup is rejected after any byte/manifest modification;
- unsigned/tampered kits fail before extraction;
- image/SBOM/signature/provenance identities agree.

## Commit sequence

1. `fix(chart): complete default-deny workload policies`
2. `fix(chart): remove dr job kubernetes authority`
3. `feat(dr): enforce encrypted immutable backups`
4. `fix(dr): authenticate key backup envelopes`
5. `fix(airgap): verify kits before safe extraction`
6. `fix(chart): require production image digests`
7. `fix(logging): harden host log forwarding`
8. `build(images): make tool and runtime layers reproducible`
9. `ci(security): add sast and history secret gates`
10. `build(compliance): add qualified fips artifact lane`

## Done criteria

- Production backup, restore, and logging work under default-deny with no broad catch-all egress.
- DR jobs cannot list Secrets, mint tokens, or mutate RBAC.
- New backups are encrypted, authenticated, restorable, and protected by immutable retention.
- Air-gap tools reject untrusted input before extraction.
- Every production render uses immutable image digests.
- Host logs are read-only to the forwarder and its container is restricted.
- Downloaded binaries are cryptographically verified; service images are reproducible/minimal.
- Security CI emits actionable SARIF and scans full history without leaking findings' secret values.
- Dependency update coverage spans Go, npm, actions, and containers.
- Any FIPS-oriented claim is backed by a distinct tested artifact and accurate documentation.

## Stop conditions

- Stop before altering a production bucket, retention rule, external credential, or signing trust root; those require operator authorization.
- Stop if the chosen CNI cannot express the intended external/API-server policy safely; document the constraint and obtain an architecture decision.
- Stop if legacy backup compatibility would require accepting unauthenticated new writes.
