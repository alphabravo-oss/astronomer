# Authenticated offline database metadata

The operator transfer metadata index version 2 retains the existing typed
release, repository, subject, catalogue, profile and original signature fields.
Its [closed shape](../../../../deploy/bundles/schemas/offline-database-metadata-v2.schema.json)
has owner W03/distribution. `readOfflineDatabaseMetadata` uses the canonical
closed JSON reader and compares every identity field with the caller's pinned
inputs. The index grants no trust or network access. Version 1 readers and their
bounds remain unchanged.

`VerifyOfflineDatabaseMetadata` first builds the same closed no-network OCI
store and authenticates the parent and every child through the canonical
Loader. Every config, manifest, chart payload and original legacy signature
must be present, bounded and digest-correct. Only layers using the two fixed
Trivy database media types may be absent in this private constructor. The signed
BOM and signed child manifests must agree on exact digest, size and media. Each
missing digest has one exact media and size; contradictory claims fail. Missing
subjects, orphan bytes and unconsumed subjects also fail.

The constructor returns an opaque `VerifiedOfflineDatabaseGraph`. External
callers cannot populate its private fields, and a zero-value object grants no
permission. The returned layer map is a copy. Successful authentication grants
permission to stream only those exact bytes into quarantine; it reports
`descriptor-only` payload state and `not-assessed` freshness. Complete byte
verification never silently upgrades metadata or supplier freshness status.

`CopyDatabaseLayer` uses a fixed 64 KiB buffer, at most the authenticated size
plus one byte, the existing 2 GiB layer limit and a complete SHA256 check.
Canceled and expired operations retain the canonical context sentinel; ordinary
I/O failures return static sanitized messages. Quarantine bytes require cleanup
on failure. No database archive is extracted by this metadata boundary.

Actual-Cosign fixture coverage reuses the original signed registry graph and a
working external trap. It checks metadata authentication, versioned encoding
and decoding, original layer round trips, changed/truncated/extra bytes,
cancellation, missing config/signature, tampered parent, wrong key, orphan
content and caller mutation. Separate unit fixtures cover exact media/size
conflicts, zero-value objects, external/inline descriptors and copy-loop error
semantics. These source tests are not qualified yet while the shared heavy
window is occupied.

The outer metadata-first streamed kit reader, owned file import/export, disk
reserve, actual large supplier payload and freshness checks remain separate
work. This metadata slice does not qualify a live disconnected deployment,
production OIDC publisher or runtime candidate startup.
