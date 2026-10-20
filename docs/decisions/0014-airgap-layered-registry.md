# 0014 — Air-gap layered registry: cluster-local zot, opt-in fallbacks, fail closed

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide ch.09 (embedded/cluster-local OCI registry, layered resolver, egress line)

## Context

Runtime image and artifact references were rendered from per-image public
defaults (`ghcr.io/alphabravo-oss`, Docker Hub short names, `mirror.gcr.io`,
`registry.k8s.io`), several of them hard-coded in Go (agent image, management
backup image). Air-gap support was an operator-signed mirror plan produced by
Python scripts (`scripts/mirror-release.py`, `scripts/airgap-kit.py`) plus a
global `image.registry` prefix whose path rule differed per image. Nothing
logged which registry satisfied a reference and nothing declared the
out-of-cluster endpoints a process would contact.

## Decision

- **One re-host rule everywhere.** `internal/registry.Rehost(ref, base)`
  keeps the repository path, drops the registry host (and Docker Hub's
  `library/`), and preserves tag and digest:
  `ghcr.io/alphabravo-oss/astronomer-go-server@sha256:…` →
  `<local>/alphabravo-oss/astronomer-go-server@sha256:…`. The chart helper
  `astronomer.rehost`, the server/worker configuration policy, the built-in
  bundle catalog projection, and `astro airgap plan/mirror` all apply it, so
  what the mirror writes is exactly what manifests name.
- **Layer 1: cluster-local registry.** `REGISTRY_LOCAL` (chart:
  `image.registry`, else the in-cluster zot when `registry.zot.enabled`). When
  set, every configured runtime reference (agent image, kubectl shell image,
  management backup image, Flux distribution, built-in bundle distribution,
  OCI catalog source) is re-hosted at config load, and every image the chart
  renders uses the local host. Verified built-in catalogs re-host their child
  charts, workload images and values subjects; the source-tree development
  catalog (public `helm_http` charts) is refused under `REGISTRY_LOCAL`.
- **Layer 2: opt-in fallbacks.** `REGISTRY_FALLBACKS` is empty by default and
  requires `REGISTRY_LOCAL`. A fallback hit is accepted only for a
  digest-pinned reference (the digest comes from the signed release manifest
  or signed bundle catalog) and only when the fallback serves that exact
  digest (`registry.VerifyDigestPinned`); tag-only references cannot be
  authenticated and fail closed.
- **Layer 3: fail closed.** Nothing falls through to a public default. The
  resolver HEADs manifests with oras-go and logs
  `msg="registry resolution" layer=local|fallback|fail_closed` per reference;
  server and worker resolve every configured runtime reference at startup.
  With no local registry the explicit online profile logs `layer=online` and
  uses references as rendered (today's behaviour, documented as such).
- **Egress line.** Server, worker and agent log one INFO
  `msg="egress endpoints" service=<name> endpoints=[…] dynamic=[…]` line at
  startup: static out-of-cluster endpoints from configuration (fallback
  registries, OTLP, OIDC, catalog/bundle sources outside the cluster) and the
  categories configured at runtime (SMTP, SIEM, webhooks, Helm repositories,
  backup destinations, …).
- **Chart.** `registry.zot` deploys project-zot v2.1.22 (digest-pinned) with a
  PVC, Service, restricted security context, an ingress-only NetworkPolicy
  (no egress: zot never syncs upstream) and a server/worker egress policy to
  it. Its own image cannot come from itself and must be pre-loaded on
  air-gapped nodes. Node runtimes reach the Service name through a containerd
  / k3s `registries.yaml` mirror entry.
- **Tooling.** `astro airgap plan` (inventory: `deploy/chart/images.txt`, the
  zot image, embedded Flux controllers and built-in bundle images, optional
  release-manifest artifacts) and `astro airgap mirror --to` (oras-go copy of
  full graphs incl. multi-arch indexes, cosign tag-scheme companions and OCI
  referrers; verified digests; idempotent and resumable) replace the Python
  scripts. `dr-crypto` is a Go command (`cmd/dr-crypto`); the DR image no
  longer contains Python.

## Consequences

- Hard-coded public registries are gone from server/worker/agent Go code;
  the remaining defaults are bare local names (`astronomer-go-agent`,
  `astronomer-shell:dev`, `astronomer-dr:dev`) and the chart supplies
  explicit references.
- `image.registry` now re-hosts first-party images under their
  `alphabravo-oss/` path (previously `<registry>/astronomer-go-server`).
- The zot instance is plain HTTP inside the cluster (`REGISTRY_PLAIN_HTTP`).
  The verified built-in/Flux distribution loaders fetch over HTTPS through the
  destination policy, so serving release distributions from zot requires TLS
  on zot and a destination-policy entry for it (not yet automated).
- The DR envelope format moved to `ASTRODR2` (chunked AES-256-GCM STREAM,
  per-chunk authentication) because Go's GCM is not streaming; no backups in
  the old format exist (pre-production).
- Not done: Flux controller images inside the signed Flux distribution on
  adopted clusters are not re-hosted (needs Kustomization `images:` overrides
  in the agent system manager); the operator-signed mirror mapping
  (`release.mirrorMapping`) and the air-gap kit `approve` flow were not
  ported to `astro airgap`; `internal/ops/upgradecli` (an operator-workstation
  tool) still defaults to GitHub/GHCR.
