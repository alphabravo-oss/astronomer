# Database update producer ordering

This is an implementation design, not producer qualification. The signed update
reader and payload inspector are consumer boundaries. They must not be bootstrapped
with a fabricated authenticated feed or a public authentication bool. A refresh
must also work for a genuinely new upstream snapshot that is absent from the
immutable base release and has no existing Astronomer signature.

## Supplier authority and new content

The operator producer constructor owns the reviewed main/Java supplier repository,
schema discovery tag, scanner compatibility, candidate/base/profile binding,
destination policy, guarded HTTP client, bounded transfer policy and output path.
For the current upstream formats the main and Java schema tags are 2 and 1.
These tags select a discovery candidate only. Signed update data, supplier
manifest fields and runtime catalog data cannot expand the operator's network
origins, CIDRs, redirect pairs, credentials, publisher identity or trust roots.

For a new snapshot, supplier source authentication consists of the independently
reviewed origin, TLS, immutable manifest resolution and complete content hashes.
There is no invented supplier Cosign signature. The producer's supplier proof
must describe that limited scope explicitly and stay separate from the canonical
signed runtime consumer. Production must still require Astronomer's configured
publisher endorsement of the resulting unchanged child and signed update feed.
No production AllowUnsigned option is introduced.

An already distribution-signed child is a valid initial source path. It cannot
be the only prerequisite for each refresh, since that would require an endorsement
before the producer had established the content it is endorsing.

## Required state and trust order

1. Resolve the constructor-owned supplier discovery tag once and capture an
   immutable subject. Obtain bounded original manifest and configuration bytes;
   require their exact digests, sizes and media types, one fixed main/Java layer,
   no external descriptor URLs/data and compatible schemas. Retain the original
   supplier repository and all raw content identities. A later changed tag cannot
   change this attempt's captured subject.
2. Transfer the complete exact layer using the canonical guarded client and
   existing finite transfer bounds. No mirror/public fallback is selected by
   content. Save only an owned regular file; verify exact layer size and SHA256.
   Partial, oversize, changed, unsupported Range or disallowed redirect results
   remain failures, with no accepted output.
3. Use one shared two-pass/no-write inspection core. Pin the regular descriptor
   with no-follow/nonblocking open, hash every compressed byte before interpreting
   metadata, then rewind and decode with finite expansion/member/metadata bounds.
   Rehash and check inode/size/mtime after decoding. Validate the exact main/Java
   archive layout, schema and UTC clocks against owned freshness policy. Derive
   the exact metadata member digest/size from these verified original bytes.
   Local DownloadedAt and scan completion never replace upstream UpdatedAt.
4. Return an opaque producer payload/metadata proof that distinguishes reviewed
   supplier transport/content authentication from vendor signature verification.
   No unsigned update document is promoted to the consumer's authenticated type.
   Existing signed-child and new-supplier adapters must share this core rather
   than introducing a second weaker archive or metadata parser.
5. Recheck constructor-owned candidate/base/profile/scanner identity and
   applicable release/catalog revocation state before publication. Generation
   allocation is operator/durable-state authority, never a supplier field. A
   production composed producer must retain its exact source and tool authority;
   a source SHA inside an unsigned receipt does not establish that authority.
6. Publish the unchanged original manifest/config/layer under the finite approved
   distribution repository. Read back the exact subject; sign its unchanged digest
   with the configured Astronomer publisher identity. A source already signed by
   that identity preserves original signature bytes through mirror/offline copy.
   A new supplier child receives a new Astronomer endorsement, not a forged
   assertion that the supplier signed it. Child copy/sign/readback failure prevents
   publishing a feed that claims the child is available.
7. Only after those proofs and endorsed child readbacks may the producer serialize
   the closed bounded update document. Require the exact base distribution,
   candidate source, catalog, profile, scanner, owned generation, finite publication
   and expiry clocks, original supplier and published child subjects, and verified
   manifest/config/layer/metadata claims. Sign the immutable feed with the same
   configured publisher policy. Discovery-channel publication is a separate
   authorized action; it cannot replace immutable feed identity in the consumer.
8. Reconsume the published immutable feed using the canonical signed update
   reader. The readback must preserve all expected source and child identities.
   Its descriptor result does not inherit whole payload or scan-used provenance;
   retain the producer payload proofs separately. Offline update packaging must
   preserve the signed feed and endorsed children/original bytes and use the same
   bounded acceptance path with a no-network store.
9. Before desired-state projection, the runtime transaction must check actual
   active base/catalog revocation state, current candidate/scanner/profile and
   assignment ownership. Require monotonic generation, idempotent exact replay,
   and reject same-generation/different-subject and stale concurrent assignment
   updates. Project only owned exact DB locators and advance normal assignment
   generation. A failed attempt preserves previous accepted desired state.
10. Invalidate older clean reports and schedule a bounded rescan. A later measured
    owned job/report must match the desired image, assignment generation, scanner
    and accepted exact DB identities. Publisher or inspector success alone does
    not establish that the workload scan actually used these bytes. Mirror/feed
    outage and stale/unknown provenance remain explicit, and known findings remain
    visible while a new scan is pending or failed.

The next source adapter must implement these distinctions. Current whole supplier
payloads, expansion/metadata cadence, protected OIDC publication, durable
transitions, actual scan-used provenance and fresh-cache/no-egress runtime cases
remain unqualified. No publication or external state mutation has occurred.
