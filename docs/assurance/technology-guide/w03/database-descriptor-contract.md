# Database descriptor contract

This W03 slice adds asset contract version 2 for the two fixed Trivy database
roles. It is a metadata consumer change. The producer, streamed payload kit,
supplier payload verification, database freshness and live W04 qualification
remain separate work. The distribution format and public API version do not
change.

The authenticated parent retains each original OCI manifest and configuration.
The database role's member is its manifest, rather than the database payload.
The signed descriptor binds one layer's SHA256 digest, exact byte count and
role-specific media type. The original supplier and endorsed distribution
subjects retain the same manifest digest. Only `trivy-database` and
`trivy-java-database` can use this contract; other assets reject the additional
descriptor, and embedded asset contract version 1 rejects it.

The runtime uses the existing signature verifier before fetching the child
manifest. It checks the returned manifest and configuration against the signed
parent byte for byte, hashes the configuration, rejects duplicate or ambiguous
JSON, and compares the exact layer descriptor. External URLs and inline content
cannot supply alternate bytes or network authority. The caller's existing
destination policy still controls every repository. No layer payload is fetched
by this metadata path.

The existing compressed parent limit remains 128 MiB and its expanded limit
remains 256 MiB. The new descriptor ceiling is 2 GiB per database layer. An
8 GiB aggregate constant reserves the finite ceiling for the forthcoming
streamed kit; this slice does not implement or qualify that kit. Existing
payload fetching, prepared layouts, offline transfer and image graph limits
retain their existing bounds and will reject oversized payloads.

The loader reports `database_payload_verification=descriptor-only` and
`database_freshness=not-assessed`. Its `verification=verified` and
`inventory_complete=true` describe authenticated catalog and inventory closure,
not payload availability, payload hash verification, freshness or release
qualification. Embedded contract version 1 reports `verified-payload-bytes`
after its existing bounded payload checks, and still reports freshness as
`not-assessed`. These fields are internal status and redacted logs; this change
does not add an API field or a UI claim.

The genuine QA key fixtures sign the parent, chart children and database
subjects. They authenticate the same sizes recorded in
[the supplier metadata discovery](trivy-size-discovery.json), including the
980,383,808-byte Java layer, while refusing payload requests. Negative controls
cover missing claims, changed digest and size, oversized or wrong media claims,
member collision, embedded payload substitution, changed configuration or
subject, and an untrusted child key. The resolver separately checks changed or
partial configuration, duplicate JSON and external or inline layer locations.
Invalid or untrusted children are never cached. These local signatures identify
the QA key, not the production GitHub OIDC publisher.

The real supplier payloads have not been downloaded, hashed, unpacked or tested
for freshness here. The size discovery retains only manifest metadata and is
not supplier runtime or signature qualification. A publisher and offline kit
must verify the streamed payload bytes and preserve the original signed graph;
W04 must assess actual database metadata and freshness before a release claim.
