# Streamed database preparation and publication

This W03 followup implements database preparation, owned layout snapshots and
mandatory publication and mirror payload readback for asset contract version 2.
The large offline kit and real supplier payload/freshness qualification remain
in progress. The earlier [metadata contract](database-descriptor-contract.md)
describes the runtime consumer, which continues to report descriptor-only
verification and freshness not assessed.

Asset input schema version 3 opts into this producer mode. Schema version 2
retains the embedded asset contract, prepared index version 1 and existing
limits. No release tag, public API version, upstream runtime source or source
version authority changes here.

| Input field | Descriptor database meaning |
| --- | --- |
| `id` | Exactly `trivy-database` or `trivy-java-database`. |
| `category` | Exactly `vulnerability_database`. |
| `path` | `assets/<id>.oci-manifest.json`, the bounded parent member. |
| `file`, `digest` | Captured regular payload file and exact original layer SHA256. |
| `artifact_subject` | Original immutable supplier repository and manifest digest. |
| `artifact_manifest_file`, `artifact_config_file` | Exact original bounded manifest and configuration bytes. |
| `images` | Explicit empty list for database assets. |

The producer derives the descriptor from the captured original manifest. It
streams the payload into the exact OCI blob with a 64 KiB buffer, compares the
whole byte count and SHA256, and rejects links, partial inputs, changed bytes,
oversize descriptors or file replacement. The original manifest digest is
preserved. The parent contains only manifest/configuration members and remains
under the existing 128 MiB compressed and 256 MiB expanded limits. Its asset
contract is version 2 and its emitted prepared index is version 2; mismatched
versions fail the canonical layout validator.

Only exact database claims use the streamed snapshot path. The operation checks
available filesystem bytes before staging, conservatively budgets 768 MiB for
bounded parent, chart and metadata copies, and leaves a further 512 MiB reserve.
Its aggregate ceiling remains 8 GiB. This local staging check supplements the
repository's independent build capacity gate; it does not override that gate.
Other blobs retain their existing allocation and size bounds. A failed producer
or snapshot removes its owned output. A successful private snapshot cannot
follow later changes to operator input files.

Publication signs unchanged database subjects before the parent, then runs the
canonical runtime loader. Metadata-only success is insufficient: every
descriptor-mode database payload must also pass complete streamed readback.
Mirrors use the same payload verifier after authenticating the complete operator
rewrite plan and original signatures. Verification uses the original manifest
digest, exact layer descriptor, existing guarded credential client and its
existing response limits.

Each payload request covers at most 64 MiB. At most 32 requests cover the 2 GiB
layer ceiling, under a total 15-minute deadline and caller cancellation. Every
response must be HTTP 206 with one exact `Content-Range` start/end/total, exact
body length and identity encoding. HTTP 200, multipart bodies, duplicate or
misaligned ranges, wrong totals, truncated or oversized responses, changed
bytes, untrusted signatures and substituted claims fail closed. The full
original layer SHA256 must match before bytes in an owned quarantine can be
accepted. No database freshness claim follows from a matching payload hash.

Actual QA tests stream a 134,217,729-byte sparse payload, one byte beyond the
historical embedded limit, while the parent remains below 1 MiB. The old mode
rejects the same input. Separate genuine QA-key signed range tests exercise a
67,108,881-byte two-chunk payload and the negative controls. Selected Cosign
3.1.3 signs the descriptor-mode local graph, and the actual publication, offline
small graph, complete 15-role mirror and keyed consumer path verify it. An owned
registry returning HTTP 200 after metadata verification causes publication to
fail. These controlled payloads are not valid supplier databases or production
GitHub OIDC evidence.

The [actual supplier range probe](trivy-range-prerequisite.json) returned HTTP
307 from GHCR to `pkg-containers.githubusercontent.com`; the existing strict
client refused the cross-origin redirect. The
[retained probe source](trivy-range-probe.go.txt) uses the canonical guarded
client and prints only allowlisted status, origin and immutable digest fields.
No signed redirect query or registry token is retained. GitHub independently
identifies both origins as package/container endpoints in its
[runner communication documentation](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
This establishes attribution, not network permission or successful payload
verification. Any redirect support must separately retain the exact origin,
credential stripping, DNS and operator policy boundaries.

The real 935 MiB Java payload has not passed download, hash, safe unpacking,
database metadata or freshness checks. A large authenticated offline kit,
supplier qualification, actual protected OIDC candidate publication and W04
fresh-cache/no-egress qualification remain required. This source slice does not
mark W03 or a release complete.
