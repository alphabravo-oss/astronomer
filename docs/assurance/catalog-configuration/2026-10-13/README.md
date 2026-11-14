# Catalog publication and local K3s qualification

This follows the [October 12 implementation evidence](../2026-10-12/README.md).
The target is the user's local K3s management cluster: Kubernetes v1.35.7+k3s1,
one x86_64 Ubuntu 26.04 node. This is development qualification, not a claim of
multi-node availability, production support, or an Assured offering.

## Published catalog

[Catalog PR #1](https://github.com/alphabravo-oss/astronomer-catalog/pull/1) merged
at `856f3356e811f63dd3514cb3ac950cbe36189891` using the owner's authorized admin
merge after the hosted validation passed. The `v0.2.0` publication
[workflow](https://github.com/alphabravo-oss/astronomer-catalog/actions/runs/38327333369)
passed. Its immutable OCI reference is:

`ghcr.io/alphabravo-oss/astronomer-application-catalog@sha256:5099796624be9c477c59816f70f9baba560fa5ae12408a1dc1703ff7937ca432`

The pulled canonical document matches
`sha256:56f31adcf7ade0d77bf91fd227cb3f52faf724180d6bd4f35e9e7e92e67cd8b4`.
Cosign 3.1.3 independently verified the artifact signature and signed build
evidence with the exact release workflow identity and GitHub OIDC issuer.
The cluster consumes the same canonical bytes through the immutable HTTPS
commit URL and digest verification; this is not an OCI signature verification
claim for that runtime source.

An earlier tag attempt accidentally referenced the old main commit when a
non-admin merge was rejected. Workflow `38325530347` failed before publication;
that tag was removed before the verified merged commit was tagged. No catalog
artifact was published by that failed attempt.

## Longhorn archive identity

The catalog's [review receipt](https://github.com/alphabravo-oss/astronomer-catalog/blob/d02c4e145f82669284af2fe131c1ac2e24a8eb71/docs/evidence/035/README.md)
corroborates the current 1.12.1 archive against both upstream index and release
asset metadata, and compares all 42 packaged files with the release source.
The original locked bytes were unavailable; the receipt does not claim a
proven upstream replacement. Longhorn remains Community.

The local Helm version cache still held the old digest. Repository sync only
inserts new versions, so an explicit, transactional repair changed that exact
uninstalled chart/version from the old digest to the reviewed digest. The
retained receipt shows one affected row. No integrity check was disabled.

## Deployment and environment repairs

The application candidate uses server/worker commit
`fc5c093e51c1692ed06f7c29b349a6df701d9ed6` and frontend commit `a45f06b3`.
The follow-up accepts both bare Helm-index SHA-256 values and canonical
`sha256:` identities while rejecting malformed or different identities.
Server and worker binaries were built with Go 1.27.2, CGO disabled, and packaged
on the previously built pinned runtime images. These are local development
images, not a published Astronomer application release.

A private Helm-values/manifest backup and a custom-format PostgreSQL dump were
retained under `/root/astronomer-backups/catalog-035/`. The dump's archive list
was readable; a restore was not performed. No credentials or database contents
are included in this evidence directory.

The management server's signed 2.0.1 manifest selected its 2.0.1 trust identity,
but the local embedded agent's database assignment still selected 1.2.1.
Managed-cluster rollout APIs exclude the local cluster. After independently
verifying both distributions, the local assignment alone was advanced to the
already-staged 2.0.1 release, incrementing generation and fence. The global
release and downstream assignments were not promoted. The configured signer
was aligned with the signed manifest. Flux verified and applied the 2.0.1
artifact successfully. This local repair is not proof of an automated
management-plane upgrade migration.

The Longhorn uninstall path exposed missing local service-account permissions.
The chart now grants inventory reads, patch access to the named deletion guard,
and settings creation for failed-release cleanup. A render test checks the
exact Longhorn rules. Application deletion still requires explicit confirmation.

## Validation status

Live desktop and mobile screenshots show the full-page Longhorn form, curated
1.12.1 selection, visual fields and presets, destination, and Community support
scope. There are no configuration dialogs or horizontal overflow at 390px.

The live curated-version API returned recommended, available profiles for all
21 offerings. All 24 configuration presets were sent through live preview:
23 were allowed, and ExternalDNS was correctly blocked until the operator
supplies its required unique ownership ID. Preview success is not an install
or functional qualification claim.

Loki 7.3.0 used the persistent single-binary preset with a 1 GiB local-path PVC.
A log marker was ingested and queried, then survived a same-version values
upgrade, deletion and recreation of its pod, and rollback to the first Helm
revision. All operations completed through Astronomer and Flux.

The first Longhorn attempt used a baseline-policy project. Its Helm operation
completed, but its dynamically created privileged CSI workloads were denied;
the storage canary correctly did not pass. After switching the dedicated
namespace to the privileged system project, that failed attempt was removed.
Cleanup required removing the finalizer from one already-deleting, empty
backup-target object after verifying that no Longhorn volumes or backups
existed. The retained recovery receipt identifies the object and guards. This
first cleanup is not claimed as an automatic uninstall pass.

Final Longhorn lifecycle, cleanup and complete-matrix results follow when complete.
