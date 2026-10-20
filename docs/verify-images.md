# Verifying Astronomer container images

Every Astronomer container image published from the
`.github/workflows/release.yaml` pipeline is:

- **Signed** with [cosign](https://github.com/sigstore/cosign) keyless
  via Sigstore (GitHub OIDC → Fulcio short-lived cert → Rekor transparency log).
- **Attested** with an [SPDX](https://spdx.dev/) SBOM emitted by
  [syft](https://github.com/anchore/syft).
- **Published with Buildx provenance** for the pushed image digest.

This document is the verifier-side procedure. Procurement / supply-chain
teams can run these commands before pulling our images into an internal
registry mirror.

---

## Prerequisites

```bash
# cosign — keyless signature verification
brew install cosign        # or download from https://github.com/sigstore/cosign/releases

# syft — only needed if you want to inspect or compare SBOMs locally
brew install syft
```

---

## Verify a signed image

The signing identity is the GitHub Actions workflow that produced the image.
That identity is `https://github.com/<owner>/<repo>/.github/workflows/release.yaml@refs/tags/<tag>`
and the OIDC issuer is `https://token.actions.githubusercontent.com`.

```bash
# Pick the image you want to verify
IMAGE=ghcr.io/alphabravo-oss/astronomer-go-server:v0.2.0

# Resolve to an immutable digest (don't verify a mutable tag in production)
DIGEST=$(cosign triangulate "$IMAGE" --type digest)

# Verify the signature
cosign verify "$DIGEST" \
  --certificate-identity-regexp 'https://github.com/alphabravo-oss/astronomer/\.github/workflows/release\.yaml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

A successful verify prints the verified signature payload and exits 0.

### Allowing workflow_dispatch hotfixes

If you accept hotfix builds (manually-triggered via `workflow_dispatch`),
broaden the regex to allow `refs/heads/main` as well:

```bash
--certificate-identity-regexp 'https://github.com/alphabravo-oss/astronomer/\.github/workflows/release\.yaml@refs/(tags/v[0-9]+\.[0-9]+\.[0-9]+|heads/main)'
```

---

## Pull and inspect the SBOM attestation

```bash
# Download the attestations attached to the image
cosign download attestation "$DIGEST" > attestations.jsonl

# Each line is one attestation; extract the SPDX SBOM (predicateType = spdx-json)
python3 -c "
import json, sys, base64
for line in open('attestations.jsonl'):
    env = json.loads(line)
    payload = json.loads(base64.b64decode(env['payload']))
    if payload.get('predicateType', '').endswith('spdx-json'):
        print(json.dumps(payload['predicate'], indent=2))
" > sbom.spdx.json

# Inspect:
syft attest "$DIGEST" -o spdx-json     # also works, requires syft+cosign auth
```

The SBOM lists every Go module and OS package in the image with version
+ license. Useful for CVE matching, license audit, and SBOM-driven
procurement gates.

---

## Verify both signature AND SBOM in one shot

```bash
cosign verify-attestation "$DIGEST" \
  --certificate-identity-regexp 'https://github.com/alphabravo-oss/astronomer/\.github/workflows/release\.yaml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --type spdxjson
```

Exit 0 means the SBOM attestation was signed by the expected workflow
identity. Exit non-zero means the image is missing the attestation OR
the signature doesn't match the expected identity.

---

## Inspect build provenance

The release workflow asks Buildx to attach provenance for the pushed digest.
Use the same identity checks as the signature/SBOM commands, then inspect
the attestation payload for the build inputs and builder metadata:

```bash
cosign download attestation "$DIGEST" > attestations.jsonl

python3 -c "
import base64, json
for line in open('attestations.jsonl'):
    env = json.loads(line)
    payload = json.loads(base64.b64decode(env['payload']))
    ptype = payload.get('predicateType', '')
    if 'slsa' in ptype.lower() or 'provenance' in ptype.lower():
        print(json.dumps(payload, indent=2))
"
```

---

## Mirror to an internal registry

After verification, copy the image (and its signature + attestation
metadata) to your internal registry with `cosign copy`:

```bash
cosign copy "$DIGEST" "your-registry.internal/astronomer-go-server@$DIGEST"
```

This preserves the signatures and attestations alongside the image — a
later `cosign verify` against the internal registry will use the same
OIDC identity and pass.

---

## Admission enforcement (Sigstore policy-controller)

Verification at pull time is not enough: a namespace should only run images
whose signatures the cluster itself checks. `deploy/policy-controller/` ships
the install for [sigstore policy-controller](https://docs.sigstore.dev/policy-controller/overview/):

| File | Content |
|------|---------|
| `values.yaml` | Helm values for chart `policy-controller` 0.10.8 (app 0.13.1): `failurePolicy: Fail`, `webhook.configData.no-match-policy: deny` (chart-managed, not a hand edit of `config-policy-controller`), bounded resources. |
| `cip-astronomer.yaml` | `ClusterImagePolicy` objects, all `mode: enforce`. |

```bash
helm upgrade --install policy-controller policy-controller \
  --repo https://sigstore.github.io/helm-charts --version 0.10.8 \
  -n cosign-system --create-namespace -f deploy/policy-controller/values.yaml
kubectl apply -f deploy/policy-controller/cip-astronomer.yaml
# Opt the Astronomer namespace in only after the dry-runs below pass.
kubectl label ns astronomer policy.sigstore.dev/include=true
```

With `no-match-policy: deny` every image in an opted-in namespace must match a
policy; an image that matches a policy must satisfy one of its authorities.
The authorities, each proven with cosign 3.1.3 against the digests the chart
pins (2026-10-20):

| Images (glob) | Authority |
|---------------|-----------|
| `ghcr.io/alphabravo-oss/astronomer-**` | keyless, issuer `https://token.actions.githubusercontent.com`, subject `^https://github\.com/alphabravo-oss/astronomer/\.github/workflows/release\.yaml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$` |
| `ghcr.io/cloudnative-pg/cloudnative-pg**` (operator; bootstrap init container of every instance pod) | keyless, `cloudnative-pg/cloudnative-pg` `release-publish.yml@refs/tags/v*` |
| `ghcr.io/cloudnative-pg/postgresql**` | keyless, `cloudnative-pg/postgres-containers` workflows |
| `ghcr.io/dexidp/dex**` | keyless, `dexidp/dex` workflows at release tags. The chart now defaults Dex to `ghcr.io/dexidp/dex` (same digest as Docker Hub, which carries no signature). |
| `index.docker.io/fluent/fluent-bit**` | keyless, `fluent/fluent-bit` workflows on `master` (`Release from staging`) |
| `quay.io/jetstack/cert-manager-**` (HTTP-01 solver pods) | cert-manager's long-lived key `cert-manager-pubkey-2021-09-20.pem`, `hashAlgorithm: sha512`, no Rekor entry (cert-manager does **not** sign keyless) |
| `index.docker.io/library/{busybox,postgres}**`, `index.docker.io/valkey/valkey**`, `ghcr.io/cloudnative-pg/plugin-barman-cloud-sidecar**` | `static: pass` — these upstreams publish no cosign signature at all, so no authority can verify them. The chart pins them by digest (`image.requireDigest`); to close the gap mirror them into a registry you sign and replace the pass with a key authority. |
| `localhost/**`, `127.0.0.1/**` | `static: fail` (unsigned local builds) |

`deploy/policy_controller_test.go` renders the chart (live astronomer-dev
values, every optional component, bundled defaults) and fails when any image
the chart can produce matches no policy, so a new third-party image must be
added here in the same change.

Dry-run before opting a namespace in (no object is created):

```bash
kubectl -n astronomer run sig-ok --image=ghcr.io/alphabravo-oss/astronomer-go-server@sha256:<release digest> --dry-run=server   # allowed
kubectl -n astronomer run sig-deny --image=docker.io/library/nginx:1.27 --dry-run=server                                     # denied: no matching policies
```

### CloudNativePG operator and Barman Cloud plugin images must be tag@digest

policy-controller mutates every admitted image reference from tag to digest.
The CNPG operator compares the running instance pods' bootstrap image with
`OPERATOR_IMAGE_NAME` (and the plugin sidecar with `SIDECAR_IMAGE`); when those
are bare tags the comparison never matches the mutated pod spec and every
reconcile restarts the instances ("old bootstrap container image"). Install
both with digest-pinned strings, e.g.

```bash
kubectl -n cnpg-system set env deploy/cnpg-controller-manager \
  OPERATOR_IMAGE_NAME=ghcr.io/cloudnative-pg/cloudnative-pg:1.30.1@sha256:923c267ec29636db3bee20f993d0ec4973fa22998e1adad37da79e4d32b5bc07
kubectl -n cnpg-system set env deploy/barman-cloud \
  SIDECAR_IMAGE=ghcr.io/cloudnative-pg/plugin-barman-cloud-sidecar:v0.15.1@sha256:2287a793a804db751edd0d07d44cd549e357677e98a800eb12e2cf7c0d53f9b0
```

(or set the same values in the operator/plugin Helm values or manifests). The
astronomer chart's own CNPG string, `postgres.cloudNativePG.imageName`, is
already `tag@digest` and a render test keeps it that way.

---

## Delivery trust root (in-process verification)

The server and resolver workers verify keyless-signed release artifacts
in-process (`internal/delivery/resolver/verifier.go`); they never contact TUF,
so they need the Sigstore trusted root on disk at
`/etc/astronomer/delivery-trust/trusted_root.json`. The chart now renders
Secret `<release>-delivery-trust` with that file by default
(`delivery.sourceResolution.trustKeys.chartManagedTrustedRoot: true`), taken
from `deploy/chart/files/sigstore/trusted_root.json`, which a test keeps
byte-identical to the root compiled into the `astro` CLI
(`internal/ops/upgradecli/toolchain/trust/trusted_root.json`). Setting
`delivery.sourceResolution.trustKeys.existingSecret` replaces it with your own
Secret (which must then also contain `trusted_root.json` for keyless policies,
plus any `<keyRef>.pub` keys). Rotate the pinned root by updating the toolchain
copy, copying it into the chart and releasing.

## Sigstore policy-controller and the cluster-local registry (zot)

Clusters that enforce Sigstore `policy-controller` with
`no-match-policy: deny` (the live `astronomer` namespace does) must name every
image the chart can run. Enabling `registry.zot` adds one new third-party image
and moves every other reference onto the zot host, so two changes are needed.

1. **The zot image itself.** project-zot does not publish cosign signatures
   for `ghcr.io/project-zot/zot` (verified: `cosign verify` reports "no
   signatures found" for v2.1.22), so admit exactly the reviewed digest with a
   static authority — never a tag or a repository-wide glob:

   ```yaml
   apiVersion: policy.sigstore.dev/v1beta1
   kind: ClusterImagePolicy
   metadata:
     name: astronomer-zot-registry
   spec:
     mode: enforce
     images:
       - glob: ghcr.io/project-zot/zot@sha256:96cda11459ce6f8c60da3b03f03e9f67fd11b3ae2d82c4c04c92b3c608996080
       # Only when image.registry pre-loads it from an operator mirror:
       # - glob: <image.registry>/project-zot/zot@sha256:96cda11459ce6f8c60da3b03f03e9f67fd11b3ae2d82c4c04c92b3c608996080
     authorities:
       - name: pinned-zot-v2-1-22
         static:
           action: pass
   ```

   Bump the digest in this policy in the same change that bumps
   `registry.zot.image.digest`.

2. **Images served from zot.** With `registry.zot.enabled`, images render as
   `<release>-zot.<namespace>.svc.cluster.local:5000/<path>`. policy-controller
   matches the rendered reference, so the existing policies must cover the
   zot host too:

   - first-party images keep their keyless authority — `astro airgap mirror`
     copies the cosign `.sig`/`.att` companions and OCI referrers next to each
     digest, so verification against the zot copy uses the same identity:

     ```yaml
     # astronomer-release-keyless: add to spec.images
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/alphabravo-oss/astronomer-**
     ```

   - the pinned third-party passthrough list gets the zot spelling of each
     entry, e.g.:

     ```yaml
     # astronomer-third-party-passthrough: add to spec.images
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/busybox**
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/valkey/valkey**
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/postgres**
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/dexidp/dex**
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/fluent/fluent-bit**
     - glob: astronomer-zot.astronomer.svc.cluster.local:5000/cloudnative-pg/**
     ```

   Replace `astronomer-zot.astronomer` with `<release>-zot.<namespace>` (or
   the `registry.zot.host` override).

---

## What's covered, what's not

**Covered:**
- All first-party chart images: `astronomer-go-server`,
  `astronomer-go-worker`, `astronomer-go-agent`,
  `astronomer-shell`, and `astronomer-frontend`
- Both the `:v<version>` and `:latest` tags
- SBOMs (SPDX 2.3 JSON format) attached as cosign attestations
- Build provenance attached by Buildx for each pushed digest
- PR validation scans every first-party image with Trivy before merge

**Not covered (yet):**
- Helm chart signing. The chart ships as a directory; once we publish an
  OCI artifact (`oras push` style), we'll add `cosign sign` for it too.
- A release-time vulnerability gate. The pull-request workflow blocks on
  Trivy for high/critical fixed vulnerabilities; operators should still scan
  mirrored release images against their own policy before promotion.
