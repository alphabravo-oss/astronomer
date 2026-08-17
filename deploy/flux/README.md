# Astronomer Flux distribution

This directory is the authenticated, reproducible Flux execution substrate for
Astronomer-managed clusters. Flux is deliberately not the product control
plane: Astronomer owns sources, placement, rollouts, approvals, assignments,
status, audit, API, and UI. These controllers only reconcile an assignment on
the downstream cluster where the agent materializes it.

Do not hand-edit `upstream-install.yaml`, `kustomization.yaml`, `install.yaml`,
`provenance.json`, `checksums.txt`, or `LICENSE.flux2`. Change an overlay file or
the generator, then regenerate the distribution.

## Qualified release

Astronomer pins [Flux v2.9.3](https://github.com/fluxcd/flux2/releases/tag/v2.9.3),
published 2026-07-23 from commit
`16602fa989daa99762f1c6d1186ae2ad1c735815`. The release was selected on
2026-08-17 after checking the upstream release, installation prerequisites,
controller options, API versions, multi-architecture image indexes, signed
checksums, release provenance, SPDX SBOM, and controller image signatures.

Only these components are installed:

| Component | Version from Flux v2.9.3 | Multi-architecture index digest |
| --- | --- | --- |
| source-controller | v1.9.3 | `sha256:ff8f3c92f1bcb433e858c948040c3a3393fe73f5dd72048a4502bfaf0a4c26cd` |
| kustomize-controller | v1.9.4 | `sha256:2b8bec54ffb6caf421bd2a6c005d27f567d5dd4db7feb55794fb51fcabd69b8f` |
| helm-controller | v1.6.3 | `sha256:16ada99456385100698a5d7adf90aba8a2089d987ab541c9566b6d7b0e897038` |

Every index contains `linux/amd64`, `linux/arm/v7`, and `linux/arm64`. The
generator verifies each image with Sigstore before resolving its immutable
digest. It does not install notification-controller, source-watcher, image
automation controllers, Flux Operator, or the community Helm chart.

The eight installed CRDs use these storage APIs:

- source.toolkit.fluxcd.io `Bucket`, `ExternalArtifact`, `GitRepository`,
  `HelmChart`, `HelmRepository`, and `OCIRepository`: `v1`;
- kustomize.toolkit.fluxcd.io `Kustomization`: `v1`;
- helm.toolkit.fluxcd.io `HelmRelease`: `v2`.

### Kubernetes qualification

The current [official installation prerequisites](https://fluxcd.io/flux/installation/)
support Kubernetes 1.33 from 1.33.0, 1.34 from 1.34.1, and 1.35 or later from
1.35.0. Flux may happen to run on Kubernetes 1.32, but upstream explicitly does
not support that EOL minor for production. Therefore this distribution's hard
minimum is Kubernetes 1.33.0. Astronomer's release compatibility contract and
CI/live matrix must not advertise or test 1.32 as a supported v1 target.

## Supply-chain contract

`scripts/update-flux-distribution.sh` performs the following fail-closed chain:

1. Fetch exact assets from the official GitHub release and compare every asset
   with the digest recorded by GitHub Releases.
2. Verify the release's checksum file using its Sigstore certificate and
   signature under the exact identity in `trust-policy.json`.
3. Verify the downloaded CLI, source archive, license source, and SPDX SBOM
   against that signed checksum file.
4. Confirm the upstream SLSA payload binds the exact tag, source commit, CLI
   filename, and signed CLI digest.
5. Generate only the three allowlisted components using the verified CLI.
6. Verify every controller image signature under the reviewed Flux controller
   release workflow identity, resolve the registry index, compare registry,
   content, and signature digests, and require all supported architectures.
7. Render the local Kustomize overlay and checksum every committed input and
   generated output.

`provenance.json` is machine-readable evidence for the exact release, source
commit, inputs, signature policy, controller images, platforms, and Kubernetes
support decision. `checksums.txt` protects the complete local distribution.
The upstream Apache-2.0 license is retained in `LICENSE.flux2`; the signed SBOM
URL and digest are retained in `provenance.json` instead of committing a second
megabyte-scale copy.

Upstream references:

- [Flux releases and signed artifacts](https://fluxcd.io/flux/releases/)
- [Flux security and SLSA provenance](https://fluxcd.io/flux/security/)
- [Flux security best practices](https://fluxcd.io/flux/security/best-practices/)
- [source-controller options](https://fluxcd.io/flux/components/source/options/)
- [kustomize-controller options](https://fluxcd.io/flux/components/kustomize/options/)
- [helm-controller options](https://fluxcd.io/flux/components/helm/options/)

## Hardening boundary

The overlay:

- enforces the restricted Pod Security Standard on
  `astronomer-delivery-system`, with enforcement/audit/warn behavior pinned to
  the oldest supported Kubernetes minor instead of floating on `latest`;
- preserves upstream non-root, read-only-root-filesystem, dropped-capability,
  RuntimeDefault seccomp, probes, and resource bounds;
- disables cross-namespace references, Kustomize remote bases, and privileged
  fallback reconciliation by configuring `astronomer-noop` defaults;
- watches only Flux objects labeled `app.kubernetes.io/managed-by=astronomer-agent`,
  removes built-in role aggregation that would let namespace editors create
  Flux resources, and removes RBAC subjects/rules for excluded components;
- adds topology spread, a dedicated priority class, and PodDisruptionBudgets;
- replaces permissive upstream policies with default-deny, DNS, Kubernetes API,
  source-fetch, source-artifact, and opt-in monitoring paths; and
- exposes metrics only through a ClusterIP Service. A monitoring namespace must
  have `monitoring.astronomer.io/scrape=true` before it can connect.

The official `cluster-reconciler` binding of helm-controller and
kustomize-controller to `cluster-admin` is retained specifically because Flux
uses Kubernetes impersonation to apply as each assignment's explicit service
account. This is a controller trust boundary, not workload authority: the
default service account flags fail closed, agent-generated objects always name
an allowlisted reconciler account, and those accounts carry the namespace or
approved platform permissions enforced by Kubernetes. Removing this binding
without replacing Flux's impersonation authorization breaks reconciliation;
any future reduction must first pass the live namespace and platform-scope RBAC
suite. The separate controller role is reduced to the three installed API
groups, and its binding contains only the three installed controllers.

Kubernetes NetworkPolicy cannot express DNS names. The portable base policy
therefore restricts source egress by ports 22, 80, and 443 and Kubernetes API
egress by ports 443 and 6443. A cluster profile may further restrict destination
CIDRs or apply a CNI-specific FQDN policy. It must never broaden ingress or
remove default deny. The agent creates `astronomer-noop` and explicitly scoped
reconciler accounts in every project delivery namespace; the system-namespace
account here has no RBAC binding and does not automount a token.

## Update and verification

Required tools are Bash, `curl`, `jq`, `cosign`, `kubectl` (for its embedded
Kustomize), `sha256sum`, `tar`, and standard POSIX utilities.

```bash
# Regenerate after reviewing trust-policy.json and upstream support/docs.
./scripts/update-flux-distribution.sh v2.9.3

# Online deterministic regeneration, signature, registry, and drift check.
./scripts/update-flux-distribution.sh --check

# Offline committed-artifact contract check.
FLUX_DISTRIBUTION_OFFLINE_ONLY=true ./scripts/tests/flux-distribution_test.sh
```

An update PR must review all RBAC, CRD, API, image, NetworkPolicy, and rendered
manifest diffs. A version bump also requires updating the global compatibility
matrix and testing fresh install, upgrade, rollback, reconciliation, drift
repair, suspend/resume, auth recovery, and namespace-scope denial in disposable
clusters. Production and air-gapped publishing must mirror the exact image
digests and preserve signature/provenance evidence; no runtime path follows a
mutable tag or `latest`.
