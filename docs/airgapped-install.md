# Air-gapped installation and upgrade

This runbook installs one immutable Astronomer release without allowing the
management or member clusters to reach a public registry. A release is a single
compatibility unit: management chart and images, agent, the downstream Flux
distribution and controllers, built-in bundles and their images, and the exact
qualified Charlie artifact.

Do not build from a source checkout for a production install. Use the chart,
signed `release-manifest.json`, signature bundle, SBOMs, provenance, and
checksums attached to the same `vX.Y.Z` GitHub release. Do not substitute a tag
where this procedure uses `repository@sha256:...`.

This is the disconnected variant of the
[production installation runbook](runbooks/production-installation.md). Apply
the same external PostgreSQL/Redis, key custody, backup/restore proof, canary,
support-bundle, and acceptance requirements after replacing every public
subject with its verified private-registry mapping.

## Trust and network boundaries

Use three roles:

1. A release-verification host can reach GitHub and the public OCI registries.
2. A controlled mirror host can read the verified sources and write the private
   registry. Registry credentials stay in the normal Skopeo, ORAS, and Cosign
   credential stores; the Astronomer utility neither accepts nor records them.
3. The disconnected environment can reach only its private registry and
   approved internal services such as DNS, PostgreSQL, Redis, identity, object
   storage, and the Kubernetes API.

For a physically separated environment, perform step 2 in an approved transfer
enclave that can address the private registry. Retain the signed release and
mapping manifests on the transferred media. Never place registry credentials,
TLS private keys, database DSNs, bootstrap passwords, or Helm secret values on
that media.

Required tools are Helm 3.16+, Skopeo, ORAS 1.2.2+, Cosign 2.4.1+, `jq`, and
`sha256sum`. The exact versions used by release CI are visible in
`.github/workflows/release.yaml`.

For the new verified distribution contract, use the exact qualified toolchain
and vendor trust assets supplied with that distribution. Historical v1 kits
retain their tagged verification procedures; their availability does not
qualify the new signed parent and chart-child OCI contract. The source contains
local QA evidence, and publication/supplier qualification remains separate.

## Operator-provided registry and prerequisite boundaries

Use an existing operator-provided OCI registry when available. Astronomer does
not take over its deployment or a database operator. The registry must retain
the original digest bytes, all supported image platforms, parent distribution,
every chart child, and signing/attestation objects. Recursive referrer copy
alone is insufficient for legacy Cosign signatures: retain each digest-derived
`.sig` subject as well. An operator endorsement changes approved locations; it
cannot replace the original distribution publisher's signature or change a
content digest.

Record the registry's storage backend and recovery ownership. A PVC backend
needs durable storage and a tested restore; an S3 backend needs an explicit
internal endpoint, CA, bucket/prefix and credentials supplied outside public
values. Measure all unique blobs for the selected profiles and supported
architectures, retained current/rollback releases, concurrent uploads and
temporary transfer copies before allocating capacity. Set repository/storage
quotas and an alert threshold; keep garbage collection separate from imports,
and retain signed release references, rollback subjects and signature objects.
Recover storage plus registry configuration/CA/authentication policy, then
verify the same immutable subjects before enabling delivery. These are
acceptance requirements, not an unqualified bundled registry deployment.

The selected platform profile must identify the exact operator-owned CNPG
operator/CRDs, PostgreSQL server, Barman plugin/tools, object store and DR shell
assets. Valkey, Dex, Fluent Bit, Charlie and supported integrations must likewise
close under their enabled capabilities. A generic operator category or an
upstream image digest alone does not qualify this profile. Trivy DB and Java DB
payloads are separate OCI artifacts; retain manifest, config and layer bytes.
W04 verifies freshness and real scanning, and W12 verifies database/object-store
write, read, list, delete and retention behavior. An HTTP health result is not
that qualification.

Network authority is supplied independently of the signed content. Configure
`delivery.distribution.destinationPolicy` with `restricted: true`, exact HTTPS
origin/port entries and narrow destination CIDRs. For example an origin can be
`https://registry.internal.example.com:443` with only the operator-approved
registry subnet in `cidrs`. DNS must resolve entirely inside that policy; each
connection pins checked addresses. Loopback, link-local and cloud metadata are
never authorized. Mount the registry CA through `delivery.sourceResolution.ca`
and vendor trusted roots through `delivery.sourceResolution.trustKeys` in both
server and worker. Registry credentials remain in separately managed Secrets;
do not print them during diagnostics. Source policy exceptions grant transport
access only, and do not make an unsigned artifact trusted.

Until a signed local application catalog and its immutable child subjects have
been qualified, set `catalog.enabled: false`, `catalog.sourceURL: ""` and
`catalog.digest: ""` in the disconnected profile. This gives an explicit
unavailable catalog, preserves existing installed workloads and makes no first
boot public request. Do not enable a rewritten mutable HTTP index as a fallback.

The S3 `managementBackup.s3.endpoint` is the internal data endpoint; approve that exact
origin/CIDR through `managementBackup.s3.destinationPolicy` and mount its CA through
`managementBackup.s3.ca`. Arbitrary stored URLs keep the public SSRF guard. Compliance
async presigning is currently disabled and audit/support downloads use the
authenticated API, so there is no implemented external browser presigning
endpoint to configure. Real backup/restore/retention qualification remains
required for the selected object store.

After provisioning, inspect only structured distribution status:

```bash
kubectl -n astronomer logs deployment/astronomer-server --since=10m \\
  | jq -c 'select(.msg == "distribution status") | {mode,release,source_digest,location,verification,inventory_complete,last_successful_sync,blocked_category}'
```

The actual provisioner emits these fields after loader attempts. Locations
contain only the registry host/path; status excludes credentials, tokens,
query parameters and trust material. `inventory_complete` describes the
selected reviewed profile; it does not assert whole-site qualification. A
failed local subject or signature yields its blocked category before mutation,
with no public fallback. Preserve accepted workload history while repairing
the owned artifact/policy and retry the durable delivery operation.

## Signed v2 distribution graph transfer

Future v2 distributions carry immutable parent and chart OCI subjects. Their
offline graph transfer includes every original manifest, configuration, layer
and legacy Cosign signature manifest. A recursive registry copy alone may omit
legacy signature tags. The qualified precompiled distribution verifier captures
them explicitly with `--export-published-layout` and `--transfer-output`, using
the exact prepared `subjects.json` and independent release/root/catalog pins.
It verifies the captured graph before emitting an archive. Registry CA and
credentials use the approved CA file and normal protected Docker credential
store; no credential is an archive field or command argument.

After the release manifest has passed the canonical authenticated validator,
an offline import can derive its pins from that authenticated manifest:

```bash
bundle_subject=$(jq -er '.built_in_bundles.artifact.reference' release-manifest.json)
bundle_subject=${bundle_subject#oci://}
catalog_digest=$(jq -er '.built_in_bundles.catalog_digest' release-manifest.json)
"${DISTRIBUTION_VERIFIER:?qualified precompiled verifier required}" \
  --verify-offline-transfer distribution-transfer.tar.gz \
  --offline-import-directory verified-distribution-store \
  --repository "${bundle_subject%@*}" \
  --expected-distribution-digest "${bundle_subject##*@}" \
  --expected-catalog-digest "$catalog_digest" \
  --expected-release "${RELEASE_TAG:?authenticated release identity required}" \
  --trust-directory /etc/astronomer/delivery-trust \
  --signature-provider cosign_keyless \
  --certificate-identity "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${RELEASE_TAG}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Offline verification has no HTTP or DNS transport and refuses network options.
It authenticates the parent and every consumed child with the same mounted
Sigstore trust before creating the new private OCI store. The index grants no
trust by itself. Missing, changed, orphan, extra or unsafe members fail before
filesystem mutation; original vendor signature bytes remain unchanged. This
source interface is qualified with an owned QA registry and a distinct local
QA key. Production OIDC publisher execution, signed verifier asset availability,
the complete current supplier profile and live disconnected operation remain
separate prerequisites. Stop if the selected release lacks these artifacts;
historical v1 tags do not become v2 qualified through this interface.

## Air-gap kit (USB / sneakernet)

Each GitHub Release includes `astronomer-airgap-vX.Y.Z.tar.gz` and
`astronomer-images.txt`. The kit is the signed release unit plus save/load
scripts. It does **not** include container image blobs (GitHub's 2 GiB asset
limit). Default save is `linux/amd64` only.

```bash
export ASTRONOMER_RELEASE=v1.2.1
gh release download "$ASTRONOMER_RELEASE" --repo alphabravo-oss/astronomer \
  --pattern "astronomer-airgap-${ASTRONOMER_RELEASE}.tar.gz" \
  --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf "astronomer-airgap-${ASTRONOMER_RELEASE}.tar.gz"
cd "astronomer-airgap-${ASTRONOMER_RELEASE}"
sha256sum --check SHA256SUMS
```

On a host that can pull public registries:

```bash
./airgap-kit.py approve \
  --manifest release-manifest.json \
  --signature release-manifest.sigstore.json \
  --signing-key /secure/path/airgap-approval.key \
  --signature-output release-manifest.offline.sigstore.json

./astronomer-save-images.sh \
  --manifest release-manifest.json \
  --output astronomer-images.tar.gz
```

`approve` verifies the upstream GitHub/Sigstore signature while the verifier
has its normal Sigstore trust services, then signs the exact manifest bytes
with an installation-owned key without uploading that local signature to a
transparency log. The manifest SHA-256 is embedded in the image archive index,
so the offline signature authenticates the release contract and thereby the
exact image set. Transfer `release-manifest.offline.sigstore.json` and the
trusted public key through your approved channel; never transfer the private
key. Prefer a dedicated handoff key, separate from the key used to sign Flux
distribution artifacts.

Move the kit directory **and** `astronomer-images.tar.gz` into the dark site.
Authenticate Skopeo to the private registry using its normal credential file
(not these scripts), then:

```bash
./astronomer-load-images.sh \
  --manifest release-manifest.json \
  --signature release-manifest.sigstore.json \
  --offline-signature release-manifest.offline.sigstore.json \
  --offline-public-key /secure/path/airgap-approval.pub \
  --images astronomer-images.tar.gz \
  --destination-registry registry.internal.example.com \
  --values-output airgap-values.json
```

The load command verifies the offline handoff signature against the explicitly
trusted public key, then validates the archive's closed index, exact release
image set, paths, member types, and every payload checksum before extracting
anything or writing to the registry. Keep the upstream keyless bundle with the
release for audit; no network access to Sigstore is needed at the dark site.
Links, traversal, duplicate or extra members, and archives from another release
are rejected.

Continue at [step 5](#5-prepare-the-cluster) with the kit chart,
`airgap-values.json`, and `--set-file release.manifest=release-manifest.json`.
`--all-platforms` saves the multi-arch index; `--first-party` saves only the
seven Astronomer images: server, worker, migrate, frontend, agent, shell and DR
(smaller smoke USB). Historical tags use their own image-role contract.

Registry-to-registry copy without a USB stick is the rest of this document
(`mirror-release.py`).

## 1. Download and verify the release unit

Set an exact release; there is no `latest` release channel.

```bash
export ASTRONOMER_RELEASE=v1.2.1
mkdir "astronomer-${ASTRONOMER_RELEASE}"
cd "astronomer-${ASTRONOMER_RELEASE}"
gh release download "$ASTRONOMER_RELEASE" --repo alphabravo-oss/astronomer
sha256sum --check SHA256SUMS
```

Authenticate and validate the signed manifest before trusting any digest in it.
Use the precompiled distribution verifier from the approved signed tool assets;
this source implementation does not imply that a historical release ships that
new tool. If the matching qualified verifier/trust assets are unavailable, stop
the affected install qualification. Do not build or fetch trust during offline
verification. Mount the approved Sigstore trusted root and set the exact vendor
publisher policy:

```bash
export RELEASE_SIGNATURE_PROVIDER=cosign_keyless
export RELEASE_CERTIFICATE_IDENTITY="https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${ASTRONOMER_RELEASE}"
export RELEASE_CERTIFICATE_OIDC_ISSUER=https://token.actions.githubusercontent.com
export SIGSTORE_TRUST_DIRECTORY=/secure/mounted-sigstore-trust
export DISTRIBUTION_VERIFIER=/secure/qualified-tools/distribution-verifier
export RELEASE_MANIFEST=release-manifest.json
export RELEASE_SIGNATURE=release-manifest.sigstore.json
```

<!-- verified-release-contract:start -->
```bash
"$DISTRIBUTION_VERIFIER" \
  --validate-release-manifest "$RELEASE_MANIFEST" \
  --signature "$RELEASE_SIGNATURE" \
  --trust-directory "$SIGSTORE_TRUST_DIRECTORY" \
  --signature-provider "$RELEASE_SIGNATURE_PROVIDER" \
  --key-ref "${RELEASE_KEY_REF:-}" \
  --certificate-identity "${RELEASE_CERTIFICATE_IDENTITY:-}" \
  --certificate-oidc-issuer "${RELEASE_CERTIFICATE_OIDC_ISSUER:-}" \
  --expected-release "$ASTRONOMER_RELEASE"
```
<!-- verified-release-contract:end -->

The command authenticates the same owned bytes that it validates, using the
canonical application release validator. It accepts the current
`fresh_and_upgrade` contract and exact seven image roles for 1.2.x, and rejects
missing DR, duplicate roles, stale install modes, unknown fields and wrong
release identities. It does not replace this with a partial `jq` assertion.
For an approved offline handoff, an installation-owned key policy can validate
the exact already vendor-verified manifest; keep the original vendor evidence
and the enclave approval record. A local QA key used by runbook fixtures is
never Astronomer's release identity.

Review `release-manifest.json` as the authoritative bill of materials. The
human-readable `RELEASE_SUBJECTS`, SPDX documents, and SLSA provenance files are
supporting evidence; they do not override it.

## 2. Check capacity before copying

From the matching Astronomer source tag, run the read-only capacity guard:

```bash
./scripts/check-build-capacity.sh --path . --min-free-gib 20 --min-free-percent 20
```

If it blocks, first produce an exact cleanup plan. The repository cleanup tool
only considers untagged Docker images that no running or stopped container
references; it never removes containers, volumes, tagged/current/rollback
images, backups, TLS material, or workspace files.

```bash
./scripts/safe-disk-cleanup.sh plan --output ./disk-cleanup.plan
# Review every docker-image ID before authorizing removal.
./scripts/safe-disk-cleanup.sh apply --plan ./disk-cleanup.plan
./scripts/check-build-capacity.sh --path . --min-free-gib 20 --min-free-percent 20
```

If that cannot recover safe headroom, add storage. Do not prune rollback or
release-manifest-referenced content.

## 3. Generate the deterministic mirror and install contracts

Authenticate Skopeo and ORAS to the destination registry using their standard
credential files. First complete the keyless release-manifest verification in
step 1 on this connected verification host; then generate a plan without
writing the registry:

```bash
export PRIVATE_REGISTRY=registry.internal.example.com

./scripts/mirror-release.py plan \
  --manifest release-manifest.json \
  --destination-registry "$PRIVATE_REGISTRY" \
  --output mirror-mapping.json \
  --values-output airgap-values.json
```

The plan contains only immutable source and target references. Repository paths
are preserved while every public registry host is replaced by the private host.
The generated Helm JSON sets every management-plane image to the mapped
repository and digest and configures the exact Flux and built-in-bundle
artifacts. It contains no credentials and is safe to review in source control;
do not add operational secret values to it.

Review the complete copy set before applying it:

```bash
jq -r '.entries[] | [.kind, .source, .target] | @tsv' mirror-mapping.json
jq -e '
  all(.entries[];
    (.source | contains("@sha256:")) and
    (.target | contains("@sha256:")))
' mirror-mapping.json >/dev/null
```

## 4. Mirror, verify, and sign the mapping

Use an enterprise-held Cosign key for a mapping that must be verified wholly
offline. Keep the private key outside the release directory and provide its
password through Cosign's documented environment mechanism.

```bash
./scripts/mirror-release.py apply \
  --plan mirror-mapping.json \
  --signature-output mirror-mapping.sigstore.json \
  --cosign-key /secure/path/airgap-mirror.key

./scripts/mirror-release.py verify --plan mirror-mapping.json

cosign verify-blob \
  --key /secure/path/airgap-mirror.pub \
  --bundle mirror-mapping.sigstore.json \
  --insecure-ignore-tlog \
  mirror-mapping.json
```

The signed mirror mapping authenticates the source-to-target digest map; it does
not create a signature for the Flux OCI artifact. For an installation that must
verify Flux without Sigstore's public transparency service, sign the exact
mirrored digest with the offline installation key. This does not change the OCI
artifact digest. Keep the private key on the controlled signing host and transfer
only the public key through the approved media process:

```bash
FLUX_SOURCE=$(jq -er '.flux.distribution.reference' release-manifest.json)
FLUX_TARGET=$(jq -er --arg source "$FLUX_SOURCE" \
  '.entries[] | select(.source == $source) | .target' mirror-mapping.json)
test -n "$FLUX_TARGET"

COSIGN_YES=true cosign sign \
  --key /secure/path/airgap-mirror.key \
  --tlog-upload=false \
  "$FLUX_TARGET"

cosign verify \
  --key /secure/path/airgap-mirror.pub \
  --insecure-ignore-tlog \
  "$FLUX_TARGET"
```

The verification still checks the signature against the pinned public key and
the exact artifact digest; `--insecure-ignore-tlog` skips only transparency-log
verification because this signature was deliberately created without a log
entry. The signed release manifest and mapping must still pass their separate
release-workflow signature checks. Repeat the artifact signing and verification
for every new Flux-distribution digest imported for an upgrade.

Container images are copied with all platforms and preserved digests. Helm and
OCI artifacts are copied recursively so OCI referrers such as signatures, SPDX
attestations, and SLSA provenance move with the subject. Registries that reject
a push directly to `repository@digest` receive a deterministic digest-derived
tag; the utility accepts the copy only after `repository@digest` resolves to the
original digest. Re-running `apply` is idempotent.

Transfer these files into the disconnected environment:

- `astronomer-X.Y.Z.tgz`
- `release-manifest.json` and `release-manifest.sigstore.json`
- `mirror-mapping.json` and `mirror-mapping.sigstore.json`
- `airgap-values.json`, `SHA256SUMS`, `RELEASE_SUBJECTS`, SBOMs, and provenance
- for a USB transfer, `release-manifest.offline.sigstore.json` and its trusted
  public key (never the private handoff key)
- the verified offline Flux-signing **public** key (never its private key)

Re-run the checksums and `mirror-release.py verify` from inside the environment.
For a registry mirror, verify the offline mirror-map signature with its trusted
public key and compare `release_manifest_digest` to the exact manifest file;
the signed map binds the manifest and every digest-preserving rewrite. For the
USB flow, verify the manifest handoff signature passed to `load-images`. These
local signatures avoid calling public transparency services from the dark site. Verification
must succeed against the private registry before Helm is allowed to run.

The registry-mirror checks are:

```bash
cosign verify-blob \
  --key /secure/path/airgap-mirror.pub \
  --bundle mirror-mapping.sigstore.json \
  --insecure-ignore-tlog \
  mirror-mapping.json

test "$(jq -er '.release_manifest_digest' mirror-mapping.json)" = \
  "sha256:$(sha256sum release-manifest.json | awk '{print $1}')"
./mirror-release.py verify --plan mirror-mapping.json
```

## 5. Prepare the cluster

Install the supported Gateway API CRDs and an approved GatewayClass from your
own mirrored artifacts before Astronomer. The Astronomer release does not
silently install a public gateway controller.

Pre-create all production secrets through your secret manager or external
secrets controller. If the registry requires authentication, create a
`kubernetes.io/dockerconfigjson` Secret from a protected Docker config file;
do not put the password in shell history:

```bash
kubectl create namespace astronomer --dry-run=client -o yaml | kubectl apply -f -
kubectl -n astronomer create secret generic internal-registry-creds \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson=/secure/path/private-registry-config.json
```

The production values require external HA PostgreSQL and Redis, the public
hostname/TLS source, bootstrap identity, and durable application encryption and
token-signing keys. See `deploy/chart/README.md` for the complete preflight
contract.

## 6. Render, inspect, and install

Render with the local packaged chart and the exact release contracts. The
example assumes application and bootstrap secrets already exist:

```bash
helm template astronomer ./astronomer-1.2.1.tgz \
  --namespace astronomer \
  -f values-production.yaml \
  -f airgap-values.json \
  -f environment-values.yaml \
  --set 'image.pullSecrets[0].name=internal-registry-creds' \
  --set secrets.existingSecret=astronomer-secrets \
  --set bootstrap.existingSecret=astronomer-bootstrap \
  --set-string delivery.artifacts.fluxDistribution.trustPolicy.certificateIdentity= \
  --set-string delivery.artifacts.fluxDistribution.trustPolicy.oidcIssuer= \
  --set-file 'delivery.artifacts.fluxDistribution.trustPolicy.publicKeys[0]=/secure/path/airgap-mirror.pub' \
  --set-file release.manifest=release-manifest.json \
  --set-file release.mirrorMapping=mirror-mapping.json \
  > rendered.yaml
```

Before installation, confirm that every rendered image is an immutable target
in the signed mapping. For example, with `yq` v4:

```bash
yq -r '.. | .image? // empty' rendered.yaml | sort -u > rendered-images
jq -r '.entries[] | select(.kind == "container_image") | .target' \
  mirror-mapping.json | sort -u > mirrored-images
comm -23 rendered-images mirrored-images
test ! -s rendered-images || ! grep -Ev '@sha256:[a-f0-9]{64}$' rendered-images
```

`comm -23` and `grep -Ev` must print nothing. Then install atomically:

```bash
helm upgrade --install astronomer ./astronomer-1.2.1.tgz \
  --namespace astronomer --create-namespace \
  -f values-production.yaml \
  -f airgap-values.json \
  -f environment-values.yaml \
  --set 'image.pullSecrets[0].name=internal-registry-creds' \
  --set secrets.existingSecret=astronomer-secrets \
  --set bootstrap.existingSecret=astronomer-bootstrap \
  --set-string delivery.artifacts.fluxDistribution.trustPolicy.certificateIdentity= \
  --set-string delivery.artifacts.fluxDistribution.trustPolicy.oidcIssuer= \
  --set-file 'delivery.artifacts.fluxDistribution.trustPolicy.publicKeys[0]=/secure/path/airgap-mirror.pub' \
  --set-file release.manifest=release-manifest.json \
  --set-file release.mirrorMapping=mirror-mapping.json \
  --atomic --cleanup-on-fail --timeout 15m
```

The server and worker reject an unknown, malformed, wrong-version, or mutable
release contract at startup. The mirror mapping is accepted only when it binds
the exact bytes of that release manifest and preserves every digest.

For each update, repeat the release verification, mirror plan/copy, and
Flux-artifact signing steps with the new exact release version, then run the
same atomic Helm upgrade with its new chart and image mapping. An air-gapped
installation does not discover GitHub releases on its own: an operator imports
and approves each release, while the selected artifact trust policy verifies
what was imported. Re-use the protected offline signer for routine updates.

The Flux trust policy supports up to eight overlapping public keys. To rotate
one, first upgrade the management-plane keyring with both old and next public
keys, then re-apply the refreshed agent manifest on every enrolled cluster and
verify the new fingerprint is pinned before signing or promoting a release
that uses the next key. Keep the old key through the rollback window. Only
after rollback no longer requires it should you remove it from the keyring and
re-apply manifests to retire the old agent pin. Do not send private keys through
the mirror, chart, air-gap kit, or Kubernetes Secret.

## 7. Enroll member clusters and qualify Charlie

Use only the registration command generated by the installed Astronomer API/UI.
Do not hand-edit its agent or Flux objects. Ensure the member cluster can resolve
and authenticate to the same private registry before running the command. The
release projection causes registration and system reconciliation to use the
mapped agent, Flux distribution, controller, and built-in-bundle identities.

For Charlie, deploy exactly `.charlie.qualified_version` and
`.charlie.artifact.reference` from the release manifest (or its mapped target),
then compare its disclosed capability digest with
`.charlie.capability_disclosure_digest`. A different version or digest is not a
qualified pair, even if it appears API-compatible.

Verify management and member workloads:

```bash
kubectl -n astronomer get pods,jobs
kubectl -n astronomer get pods -o jsonpath='{.items[*].spec.containers[*].image}' \
  | tr ' ' '\n' | sort -u
kubectl -n astronomer logs deployment/astronomer-server --since=10m \
  | jq -c 'select(.msg == "distribution status") | {release,source_digest,verification,inventory_complete,blocked_category}'
```

Use firewall, DNS, or flow logs during acceptance to prove there are no public
registry or GitHub requests. A successful cached pull alone is not evidence of
an air-gapped installation.

## 8. Export and import the application catalog

The optional application archive carries reviewed third-party chart bytes and
exact supported values profiles. Export never guesses images from only default
values or downloads mutable same-version charts. Each catalog application needs
reviewed profiles, exact TGZ hashes, recursively vendored dependencies and
captured OCI platform evidence for every runtime image. Unrenderable charts,
conditional images outside the reviewed inventory, mutable inputs or missing
architectures block export. Operator overrides need their own requalification.

On the authorized connected publisher lane, prepare the signed transfer unit:

```bash
./scripts/application-catalog-airgap.sh export \
  --catalog reviewed-application-catalog.json \
  --profile-index reviewed-application-profiles.json \
  --platform-evidence captured-platforms \
  --output application-catalog.tar.gz \
  --bom application-catalog.transfer.json \
  --signature application-catalog.transfer.sigstore.json \
  --verifier-bin "$DISTRIBUTION_VERIFIER" \
  --cosign-bin "$COSIGN_BIN" \
  --release "$ASTRONOMER_RELEASE" \
  --publisher-mode production
```

Production mode must execute under the approved publisher OIDC identity. Local
QA key mode is a separate fixture identity and does not qualify this artifact
as an Astronomer distribution. Transfer the archive, external document and
signature together; obtain the exact publisher policy and mounted trust through
an independently approved channel.

Import into a new owned directory with no archive extraction before canonical
publisher verification:

```bash
./scripts/application-catalog-airgap.sh import \
  --archive application-catalog.tar.gz \
  --bom application-catalog.transfer.json \
  --signature application-catalog.transfer.sigstore.json \
  --destination ./verified-application-catalog \
  --verifier-bin "$DISTRIBUTION_VERIFIER" \
  --trust-directory "$SIGSTORE_TRUST_DIRECTORY" \
  --signature-provider cosign_keyless \
  --certificate-identity "$APPLICATION_CATALOG_CERTIFICATE_IDENTITY" \
  --certificate-oidc-issuer "$APPLICATION_CATALOG_CERTIFICATE_OIDC_ISSUER"
```

Import checks the signed closed member set, hashes, sizes, path/types, values,
image inventory and actual offline chart rerenders before using the release-kit
safe extractor. Missing, extra, duplicate, link or traversal members fail.
Original catalog URLs and publisher bytes are preserved; no `sed` rewrite or
regenerated checksum can substitute for a publisher signature. The command
prints verification/inventory status and an explicit unavailable runtime
qualification category. Keep disconnected catalog synchronization disabled
until a signed local OCI catalog and immutable child subjects are qualified;
an imported chart archive alone does not authorize a mutable HTTP index.

## Clean upgrades and rollback

Treat each upgrade as a new immutable unit:

1. Download and verify the new tagged release independently.
2. Generate a new mapping and Helm values file; never edit the prior mapping.
3. Mirror and verify all new subjects while retaining the current and one tested
   rollback release.
4. Run `helm diff` or an equivalent rendered-manifest review, database backup,
   canary/member compatibility checks, and the capacity gate.
5. Run `helm upgrade --atomic` with both new `--set-file` contracts.
6. Keep the old chart, manifests, mappings, images, database backup, and TLS
   material until the acceptance/soak window closes.

Helm provides transactional Kubernetes object rollout; it does not make an
arbitrary database downgrade safe. Follow `docs/upgrade-runbook.md` for schema,
backup, compatibility, canary, and rollback decisions. The greenfield v1 line
does not support upgrading a pre-v1 database in place.

## Related material

- [`deploy/release/release-manifest.schema.json`](../deploy/release/release-manifest.schema.json)
- [`scripts/mirror-release.py`](../scripts/mirror-release.py)
- [`scripts/check-build-capacity.sh`](../scripts/check-build-capacity.sh)
- [`scripts/safe-disk-cleanup.sh`](../scripts/safe-disk-cleanup.sh)
- [`deploy/chart/README.md`](../deploy/chart/README.md)
- [`docs/upgrade-runbook.md`](upgrade-runbook.md)
- [`docs/runbooks/delivery-control-plane.md`](runbooks/delivery-control-plane.md)
