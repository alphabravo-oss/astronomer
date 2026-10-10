# Authenticated streamed offline database export

Owner: W03/distribution operator tooling. This source slice adds the local export
side of `offline-database-transfer.md`; CLI and published-graph capture wiring
remain separate steps. Source preparation is not payload, supplier or runtime
qualification.

The exporter independently authenticates the bounded original signed metadata
before creating any output. Its exact layer file set comes from the opaque
canonical verified graph, not caller path names or a mutable registry tag.
Caller-owned files must be regular, not links, with the exact claimed size.
Opening and final checks preserve their inode, size and modification time. Each
whole layer streams through the canonical size/SHA256 copier using fixed memory.

The existing two-GiB per-layer and eight-GiB kit limits remain finite. Aggregate
accounting includes original inputs, the simultaneously staged output and a
512-MiB reserve. A finite eight-MiB encoding allowance covers only bounded output
staging; an actual compressed output beyond that allowance fails. Two maximum
size layers can therefore exceed the aggregate staging limit and must fail;
eight GiB is not a payload allowance. Filesystem free capacity must also cover
planned output and reserve, excluding already existing inputs.

A completed private owned temporary file is synced and independently read back
through the canonical full transfer reader, reauthenticating original vendor
subjects and every byte. A same-directory hard link commits only a new output;
an existing output is never overwritten. The committed inode, size and
modification time must match the verified owned file. Failure cleanup checks
ownership before removal and preserves a replaced staging path. Local operator
filesystem ownership is this commit boundary: a privileged host can mutate a
path after any check. Every later importer must run the canonical verifier
again and must never treat path existence as an artifact acceptance proof.

The actual selected-Cosign QA helper adds a local export/import round trip,
changed/partial/missing/extra/linked source rejection, wrong-key and unsigned
metadata rejection, cancellation after bytes reach the actual private staging
file, changed input mtime after planning, replaced staging-path preservation and
existing-output preservation. These fixtures use tiny original QA bytes, not
actual supplier databases. Tests for this source slice have not yet executed;
the earlier reader evidence does not qualify this exporter or future CLI wiring.
No production publisher OIDC, upstream DB freshness, protected publication,
registry import, fresh-cache/no-egress deployment or release approval is claimed.
