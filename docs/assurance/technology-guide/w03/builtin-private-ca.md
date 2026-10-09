# Trusted builtin TLS materialization

Server startup reads the operator's mounted registry CA once, as a regular file
bounded to 1 MiB. The distribution HTTP client and delivery provider use the
same captured bytes. Delivery also respects the existing protocol limit of
256 KiB per credential value. The provider rejects malformed PEM, non-certificate
blocks, trailing data, non-CA certificates and bundles exceeding 64 certificates.
TLS verification remains enabled. This CA is public certificate material;
registry credentials and vendor signing trust remain separate.

The provider's authorizer consumes the same verified catalog loader injected
into the builtin provisioner. Authorization requires the deterministic builtin
cluster/project/source identity, platform scope, Helm OCI kind, exact catalog
URL/chart/version/digest and exact publisher OIDC identity. It then checks the
independent canonical destination policy, including all DNS answers and approved
CIDRs, with a five-second DNS deadline. Failed catalog verification or denied
transport fails the snapshot. User sources, development catalogs, altered
digests/identities and unrelated platform assignments receive no inherited CA.
Existing explicitly stored per-source authentication and CA material keeps its
existing path.

Only the verified anonymous-read builtin path receives this public CA. The
existing credential receipt includes its public certificate fingerprint and
advances the global credential epoch on rotation; an old acknowledged snapshot
cannot suppress new CA material. No secret bytes are hashed into that receipt.
The provider clones both captured and returned CA bytes. Rotation takes effect
when the server restarts with the new mounted file.

Actual assignment materialization emits an Opaque Secret with `ca.crt` and an
OCIRepository `certSecretRef`. Client TLS keys use `tls.crt`/`tls.key`; incomplete
pairs remain invalid. Registry authentication retains `secretRef` when actual
authentication keys exist, including mixed authentication/TLS material. A
CA-only anonymous source does not acquire an authentication reference. The
immutable subject digest and mandatory vendor verification remain unchanged.
This follows the current [Flux OCI contract](https://fluxcd.io/flux/components/source/ocirepositories/).

The retained tests exercise provider snapshots, CA rotation and the real agent
`BuildAssignment` object graph. Server composition tests reject changed source
ownership, URL, chart, version, digest, OIDC identity, source kind, scope and
unapproved private CIDRs; unavailable catalogs and invalid CA files fail. These
are source contract fixtures. The catalog metadata fixture is not a production
OIDC signature, and no live Flux registry fetch occurred in this slice. Actual
registry authentication for builtins remains an operator-owned prerequisite;
the anonymous-read profile must not be labeled a private authenticated profile.
External Flux DNS/network enforcement and private-registry operation remain
part of the disconnected live qualification.

No public API, database schema, root dependency graph or version pin changed.
The only JSON shape addition is `public_ca` in the existing internal credential
receipt projection; its existing canonical serializer remains the owner.
