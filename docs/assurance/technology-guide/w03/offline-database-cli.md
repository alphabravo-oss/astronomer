# Explicit streamed database transfer CLI

Owner: W03/distribution operator tooling. `-offline-transfer-format` defaults to
`v1`, preserving the bounded original transfer/export/import paths. The explicit
`database-v2` format routes operator verification/import through the streamed
owned-file reader and export through the authenticated streamed writer. Unknown
formats, database-v2 without a transfer action, and combined mirror/transfer
selections fail before any action. The format selects a closed container shape;
it cannot select trust, alter destination authority or enable unsigned content.

Export with `-export-published-layout` requires caller-pinned original parent,
release, catalog, built-in profile and vendor trust. It captures unchanged
published parent/child/config/signature bytes through the canonical verifier and
operator-approved client. Only fixed main/Java DB roles may omit their layer
bytes from this bounded metadata capture. The canonical signed parent/child
verifier closes those exact missing claims before any output is staged. Digest
paths under the existing prepared layout identify original regular layer files;
the writer hashes every byte and independently reauthenticates the completed
kit. This is published-metadata plus verified prepared-original-byte export,
not a claim that supplier layers were re-downloaded during export.

Verification/import with `-verify-offline-transfer` refuses destination-policy,
registry-CA and transfer-output inputs before reading the archive. It has no
HTTP or DNS transport. Import creates only a new private directory after the
original signed metadata authenticates. The default v1 and explicit database-v2
readers refuse each other's container shapes; there is no format detection or
silent fallback. Existing mirror commands remain bounded v1 consumers and must
not be claimed qualified for database-v2 until a separate streamed mirror/import
implementation is reviewed and tested.

The actual selected-Cosign registry helper exercises metadata capture, streamed
export and operator flag verification/import. Mutex-protected exact per-blob
counters verify that metadata capture does not refetch DB payloads. Negatives
cover a missing original file, changed/partial files, a different release/key,
and deletion of one known chart child's exact signature tag. Failures must leave
neither an accepted output nor owned staging files. Format/action cases include
an explicit database-v2 selection without an action or combined with a mirror;
both formats reject offline network/CA/output inputs before input access.

This source freeze has not yet executed Go/race/vet gates. Reader-only and old
format evidence do not qualify this CLI, exporter, selected supplier payloads,
DB schema/freshness, production vendor OIDC, live registry import, startup or a
fresh-cache/no-egress cluster. Protected production publication and full W04
runtime qualification remain separate prerequisites.
