# Release contract

`release-manifest.json` is generated once for a tagged release and is the
authoritative compatibility and artifact unit consumed by the management plane,
member-cluster registration, air-gap tooling, and qualification jobs. It is not
maintained by hand and is not committed with build-specific digests.

## Inputs

- `compatibility.yaml` declares the supported Kubernetes, agent protocol,
  PostgreSQL, browser, Flux, and install-mode ranges.
- `deploy/flux/provenance.json` and `deploy/flux/trust-policy.json` bind the
  exact upstream Flux release, controller images, APIs, signatures, and source.
- `deploy/bundles/catalog.json` binds every built-in chart, chart digest, image,
  target namespace, and required capability.
- Release CI supplies the source commit; packaged chart; six image identities;
  resolved runtime-image identities; Flux and bundle OCI subjects; and Charlie
  version, subject, capability disclosure digest, and signing identity.

`release-manifest.schema.json` is a closed schema. Unknown fields, mutable image
tags, missing evidence, non-exact versions, and incomplete inventories fail the
release before publication.

## Publication sequence

The tag workflow in `.github/workflows/release.yaml`:

1. verifies the public repository, exact `vX.Y.Z` tag, chart versions, source
   ancestry, Charlie qualification inputs, and disk headroom;
2. builds the six multi-platform images once, records their manifest-list
   digests, signs them, and attaches SPDX and SLSA attestations;
3. reproducibly builds and publishes the signed Flux distribution and built-in
   bundle OCI artifacts;
4. resolves every tagged chart/runtime image to an immutable digest;
5. packages, publishes, signs, and attests the dependency-free Helm chart;
6. verifies the exact externally built Charlie artifact and generates the
   canonical manifest with `scripts/generate-release-manifest.py`;
7. signs the manifest as a blob, publishes a signed OCI copy, and installs the
   exact unit into a new Kubernetes cluster;
8. creates the immutable GitHub Release only after qualification passes.

There is no mutable `latest` promotion. The resume workflow accepts one original
workflow run, re-verifies every downloaded digest/signature/commit, repeats clean
qualification, and promotes those same bytes; it never rebuilds artifacts.

## Local contract gate

Run:

```bash
make release-contract-check
```

The gate checks capacity, compatibility/schema/docs agreement, manifest
determinism, mirror behavior, Flux/bundle archive reproducibility, rendered
image inventory, release workflows, Helm release wiring, and the Go runtime
loader. It performs no registry write, tag, deployment, or release creation.

## Air-gap projection

`scripts/mirror-release.py` consumes the verified published manifest. `plan`
emits a deterministic mapping and exact Helm JSON; `apply` copies all platforms
and recursive OCI referrers, verifies every destination digest, and signs the
mapping only after success. The management plane accepts that mapping only when
its release version and SHA-256 bind the exact mounted release-manifest bytes.

See [`docs/airgapped-install.md`](../../docs/airgapped-install.md) for operator
commands, offline trust handling, capacity/cleanup policy, clean installation,
member enrollment, Charlie qualification, and rollback retention.
