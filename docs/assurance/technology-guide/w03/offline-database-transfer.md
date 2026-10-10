# Authenticated streamed offline database transfer

Owner: W03/distribution operator tooling. This format is not a public API or an
application version. Transfer v1 and its 512 MiB bounds retain their original
reader and output behavior. Version 2 is a separate closed streamed container
for contract-2 database descriptors; no old supplier, offline, or OIDC receipt
qualifies it implicitly.

The outer gzip stream contains a USTAR archive whose first regular member is
`offline-metadata.tar.gz`. That bounded member contains the unchanged parent,
child manifests, configuration blobs, original legacy signature bytes and the
closed metadata-v2 index described in `offline-database-metadata.md`. The index
selects a format, never a trust authority. Caller-pinned source/release/catalog
identity and the canonical offline signature verifier must succeed before the
reader creates any output directory or writes database bytes. Verification has
no network/DNS transport and never fetches trust keys.

Every remaining member must be one exact `blobs/sha256/<digest>` database layer
endorsed by that authenticated graph, with its exact declared size. Missing,
extra, repeated, changed, partial, oversized, linked, extended/PAX, traversing,
or unexpected members fail. The reader consumes the gzip footer, rejects any
expanded remainder or second/trailing compressed stream, and checks that the
original input retains its regular-file inode, size and modification time across
opening and acceptance. Layer contents remain opaque;
the reader never expands or executes them.

The finite layer limit is 2 GiB, with at most two distinct authenticated layer
claims. Compressed and expanded outer bytes are bounded at 8 GiB. Overflow-safe
accounting includes the existing input, planned captured blobs/layers and a
512 MiB reserve within that aggregate limit. Available filesystem capacity must
also cover planned output plus the reserve; it already excludes existing files.
The reader uses a fixed 64 KiB buffer for layers and full-file readback. Bounded
metadata retains its previous 512 MiB maximum and all smaller per-blob limits.
The complete operation has a 15-minute deadline and preserves canonical
cancellation/deadline identity without reproducing ordinary IO error details.

A requested output must be a new private directory. The reader captures exact
original bytes, repeats complete regular-file size/SHA256 checks for every
config/signature/manifest/layer, then emits the same OCI-layout/index shapes as
the historical importer. Failure removes its captured newly owned directory;
an existing or subsequently replaced directory is never claimed for cleanup.
Verification without an output writes no database files. This implementation
has no operator CLI or large-kit exporter wiring yet; those are separate source
and qualification steps.

The actual-Cosign QA fixture tests the canonical signed metadata chain and tiny
opaque payloads before malformed outer-container cases. A private writer wrapper
cancels a real context immediately after bytes reach an actual owned layer file,
then asserts canonical cancellation identity and removal of staged outputs;
production always selects the fixed owned-file writer. A same-byte/inode/size
input with changed modification time also fails its identity check. Structural length
checks use declared sparse/oversized inputs without downloading or allocating
large payloads. The frozen reader passed 52 parents/210 cases with zero skips or failures
in the two-package race suite using actual selected Cosign3.1.3, plus vet,
focused readback/oversize tests and documentation/unchanged complexity gates.
The source-bound packet is `offline-database-transfer-evidence.json`. These
results establish the tested format boundary; they cannot qualify real upstream databases, full supplier hashes, database
schema/freshness, original Astronomer OIDC signatures, production startup,
registry import/readback, or a fresh-cache/no-egress live deployment. Those
conditions remain explicit prerequisites for W03/W04.
