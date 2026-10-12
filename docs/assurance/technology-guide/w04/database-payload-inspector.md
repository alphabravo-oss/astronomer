# Whole database payload inspection

This is an unqualified source slice. No Go test has run against it, and no whole
current supplier layer has been downloaded or measured. It adds no extraction,
publication, runtime repository rewrite or actual scan-used provenance.

`internal/delivery/distribution/database_update_payload.go` requires the opaque
authenticated update from the [signed update reader](signed-update-core.md).
The role must select an authenticated main or Java child. The caller supplies an
absolute operator-owned regular file; signed data cannot select a filesystem path.
Symlinks, missing/nonregular files and size mismatches are rejected. The opened
file descriptor and path must retain the same inode, size and modification time.
The Unix opener uses no-follow/nonblocking flags and immediately checks the
descriptor type, so a regular path swapped to a FIFO cannot block before reads.
Other platforms refuse the procedure until an equivalent opener is qualified;
there is no blocking fallback. A deterministic fixture swaps FIFO/symlink paths
after Lstat and before open and requires prompt refusal.

The first pass hashes the complete compressed layer with fixed 64 KiB buffers,
the exact signed size and existing two GiB ceiling. No archive or metadata is
interpreted before this hash succeeds. After the same-file checks, the reader
rewinds the same descriptor. The second pass decodes with a fresh whole-layer
hash, a finite eight GiB expansion bound and a 15-minute context deadline. Final
descriptor/path identity checks and the second complete hash detect changes
during inspection. Privileged local filesystem mutation after this call remains
a host boundary; every later consumer must reverify the bytes it consumes.

No archive member is written. Exactly two regular USTAR/GNU members are allowed:
`trivy.db` and `metadata.json` for main, or `trivy-java.db` and `metadata.json` for
Java. Duplicate/extra members, paths, links, PAX/xattrs, oversized/truncated
members, nonzero content after the tar footer, concatenated gzip streams, trailing
compressed bytes and gzip checksum failures are rejected. The finite expansion
ceiling has not been measured against the selected supplier payload; a failure
must remain explicit rather than enlarging it without qualification.

Metadata is at most 64 KiB and must match the authenticated exact member digest,
size, schema and upstream clocks. Its JSON uses exact upstream capitalized keys,
recursive duplicate/depth/single-document enforcement and no unknown fields.
DownloadedAt is allowed as upstream operational data and never sets freshness.
The result is opaque and returns copied content claims. It proves the bytes and
metadata that were inspected, not that a scanner subsequently used those bytes.

Upstream-main discovery supports these layouts, but is separate from selected
artifact qualification: the [main Makefile](https://github.com/aquasecurity/trivy-db/blob/main/Makefile)
packages `trivy.db` and `metadata.json`; the [Java Makefile](https://github.com/aquasecurity/trivy-java-db/blob/main/Makefile)
packages `trivy-java.db` and `metadata.json`. Both the
[main metadata type](https://github.com/aquasecurity/trivy-db/blob/main/pkg/metadata/metadata.go)
and [Java metadata type](https://github.com/aquasecurity/trivy-java-db/blob/main/pkg/db/metadata.go)
describe DownloadedAt as filled after downloading.

Prepared source tests include canonical keyed feed/child verification followed
by both whole local payload inspections; pure main/Java archive fixtures; changed
compressed header bytes with unchanged gzip CRC; partial layers; exact layout,
metadata and gzip negatives; cancellation after the first complete hash; source
replacement/mtime changes; same-size content changes with restored mtime requiring
the second hash; and sanitized I/O failures. Directly constructed opaque fixtures
are explicitly unit-only and do not stand in for signature qualification. The
separate selected-Cosign test in the core covers producer-to-descriptor consumption;
its success cannot be inherited as whole supplier payload or freshness evidence.
