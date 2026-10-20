---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Image signature admission with per-vendor authorities"
tags: [security, supply-chain, deployment]
---

# Image signature admission with per-vendor authorities

## Context

astronomer-dev runs Sigstore policy-controller with `no-match-policy: deny` in the `astronomer` namespace. The hand-made policies verified first-party images keylessly but passed every third-party image (`ghcr.io/cloudnative-pg/**`, Dex, Valkey, BusyBox, PostgreSQL, Fluent Bit, cert-manager) with `static: pass`, and the `no-match-policy` was a hand edit of a Helm-managed ConfigMap. The delivery verifier also needed a hand-made Secret holding the Sigstore trusted root.

## Decision Drivers

- Admission must check signatures wherever the vendor publishes one; a pass must be the exception, named and narrow.
- Everything the chart can render must be admitted, or installs fail closed.
- Trust material must be reproducible from the repository.

## Considered Options

- Keep the vendor-wide `static: pass`.
- Per-vendor authorities, with a narrowed pass only for upstreams that publish no signature (this ADR).
- Mirror and re-sign every third-party image under the release identity.

## Decision Outcome

Chosen option: per-vendor authorities. Checked with cosign 3.1.3 against the pinned digests: CNPG operator and PostgreSQL, Dex (`ghcr.io`) and Fluent Bit are keyless-signed by their GitHub workflows; cert-manager signs with a long-lived key (sha512, no Rekor entry), not keyless; BusyBox, PostgreSQL (Docker official), Valkey and the Barman Cloud plugin sidecar publish no signature and stay on a `static: pass` limited to those exact repositories, with digests required by the chart. Dex defaults to `ghcr.io/dexidp/dex` (same digest, signed). The policy-controller values pin `no-match-policy: deny` through `webhook.configData`. The chart renders the delivery trust root Secret from a copy of the toolchain's pinned `trusted_root.json`, kept byte-identical by a test.

### Positive Consequences

- Five of the eight third-party repositories are now cryptographically verified at admission.
- A render test fails when any image the chart can produce matches no policy.

### Negative Consequences

- Four upstream repositories remain unverifiable until they are mirrored and signed by us.
- The CNPG operator and plugin must be installed with `tag@digest` image strings, or policy-controller's tag-to-digest mutation makes CNPG restart instances on every reconcile.

## Consequences

`deploy/policy-controller/` and `docs/verify-images.md` are the source of truth for the cluster policy. Adding a third-party image to the chart requires a policy in the same change.
