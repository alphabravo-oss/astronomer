# Verifying Astronomer container images

Every Astronomer container image published from the
`.github/workflows/release.yaml` pipeline is:

- **Signed** with [cosign](https://github.com/sigstore/cosign) keyless
  via Sigstore (GitHub OIDC → Fulcio short-lived cert → Rekor transparency log).
- **Attested** with an [SPDX](https://spdx.dev/) SBOM emitted by
  [syft](https://github.com/anchore/syft).

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

## What's covered, what's not

**Covered:**
- All four published images: `astronomer-go-server`, `astronomer-go-worker`,
  `astronomer-go-agent`, `astronomer-go-migrate`
- Both the `:v<version>` and `:latest` tags
- SBOMs (SPDX 2.3 JSON format) attached as cosign attestations

**Not covered (yet):**
- Helm chart signing. The chart ships as a directory; once we publish an
  OCI artifact (`oras push` style), we'll add `cosign sign` for it too.
- The `astronomer-frontend` image (built from a separate `frontend/`
  Dockerfile under the same repo; will fold into the release workflow
  once its build is unified with the Go components).
- Container scanning (Trivy / Grype) — the SBOM enables it but doesn't
  perform it. Operators are expected to run their own scanner against
  the SBOM or the image.
