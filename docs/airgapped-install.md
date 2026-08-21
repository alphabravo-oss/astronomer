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

## Air-gap kit (USB / sneakernet)

Each GitHub Release includes `astronomer-airgap-vX.Y.Z.tar.gz` and
`astronomer-images.txt`. The kit is the signed release unit plus save/load
scripts. It does **not** include container image blobs (GitHub's 2 GiB asset
limit). Default save is `linux/amd64` only.

```bash
export ASTRONOMER_RELEASE=v1.0.0
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
./astronomer-save-images.sh \
  --manifest release-manifest.json \
  --output astronomer-images.tar.gz
```

Move the kit directory **and** `astronomer-images.tar.gz` into the dark site.
Authenticate Skopeo to the private registry using its normal credential file
(not these scripts), then:

```bash
./astronomer-load-images.sh \
  --manifest release-manifest.json \
  --images astronomer-images.tar.gz \
  --destination-registry registry.internal.example.com \
  --values-output airgap-values.json
```

Continue at [step 5](#5-prepare-the-cluster) with the kit chart,
`airgap-values.json`, and `--set-file release.manifest=release-manifest.json`.
`--all-platforms` saves the multi-arch index; `--first-party` saves only the
six Astronomer images (smaller smoke USB).

Registry-to-registry copy without a USB stick is the rest of this document
(`mirror-release.py`).

## 1. Download and verify the release unit

Set an exact release; there is no `latest` release channel.

```bash
export ASTRONOMER_RELEASE=v1.0.0
mkdir "astronomer-${ASTRONOMER_RELEASE}"
cd "astronomer-${ASTRONOMER_RELEASE}"
gh release download "$ASTRONOMER_RELEASE" --repo alphabravo-oss/astronomer
sha256sum --check SHA256SUMS
```

Verify the signed manifest before trusting any digest in it:

```bash
cosign verify-blob \
  --bundle release-manifest.sigstore.json \
  --certificate-identity \
    "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${ASTRONOMER_RELEASE}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  release-manifest.json

jq -e --arg version "$ASTRONOMER_RELEASE" '
  .schema_version == 1 and
  .release.version == $version and
  .release.install_mode == "fresh_only" and
  (.astronomer.images | length) == 6 and
  (.flux.controllers | length) == 3 and
  (.built_in_bundles.components | length) > 0 and
  (.charlie.qualified_version | test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))
' release-manifest.json >/dev/null
```

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
credential files, then generate a plan without writing the registry:

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
  mirror-mapping.json
```

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

Re-run the checksum, release-manifest signature, mirror-mapping signature, and
`mirror-release.py verify` checks from inside the environment. Verification
must succeed against the private registry before Helm is allowed to run.

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
helm template astronomer ./astronomer-1.0.0.tgz \
  --namespace astronomer \
  -f values-production.yaml \
  -f airgap-values.json \
  -f environment-values.yaml \
  --set 'image.pullSecrets[0].name=internal-registry-creds' \
  --set secrets.existingSecret=astronomer-secrets \
  --set bootstrap.existingSecret=astronomer-bootstrap \
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
helm upgrade --install astronomer ./astronomer-1.0.0.tgz \
  --namespace astronomer --create-namespace \
  -f values-production.yaml \
  -f airgap-values.json \
  -f environment-values.yaml \
  --set 'image.pullSecrets[0].name=internal-registry-creds' \
  --set secrets.existingSecret=astronomer-secrets \
  --set bootstrap.existingSecret=astronomer-bootstrap \
  --set-file release.manifest=release-manifest.json \
  --set-file release.mirrorMapping=mirror-mapping.json \
  --atomic --cleanup-on-fail --timeout 15m
```

The server and worker reject an unknown, malformed, wrong-version, or mutable
release contract at startup. The mirror mapping is accepted only when it binds
the exact bytes of that release manifest and preserves every digest.

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
helm get values astronomer -n astronomer -o yaml
```

Use firewall, DNS, or flow logs during acceptance to prove there are no public
registry or GitHub requests. A successful cached pull alone is not evidence of
an air-gapped installation.

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
