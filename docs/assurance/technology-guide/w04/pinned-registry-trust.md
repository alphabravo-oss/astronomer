# Optional database registry transport trust — applied source

This optional source is applied but not compiled or runtime-qualified. It extends the approved
physical signing-trust authority source while keeping ordinary distribution
loading and its existing system-root plus private-CA composition unchanged.

The optional durable database path explicitly requires an independently supplied
operator root bundle. It uses a pool containing only that captured PEM material,
not ambient system roots. The caller may supply a bounded selected public root
or private registry CA; neither artifact contents nor a feed can choose it.
There is no default system-root or public-network fallback for this constructor.

`CaptureDatabaseRegistryTransport` requires a restricted destination policy with
finite explicit HTTPS origins/CIDRs. It rejects legacy wildcard/private-host
exceptions and arbitrary HTTP destinations. It reads the configured regular
`RootsRef.pem` through the same bounded no-follow/nonblocking, stable-FD reader
used for signing trust; validates up to 256 KiB and 64 canonical CA certificates;
and captures the exact root-byte SHA, root mount origin hash, and framed
constructor transport policy fingerprint. The policy includes exact origins,
CIDRs, pinned-root mode, TLS floor, fixed timeout/body cap, same-origin redirects
and no proxy. No new JSON reader/writer or schema is introduced.

The canonical destination client owns the frozen roots/policy and retains the
existing DNS-answer validation and IP-pinned connections. Its 128 MiB response
cap bounds catalog/archive and update metadata requests, not an entire large
supplier database. Large transfer must compose existing `StreamVerifiedOCIDatabase` or
`StreamSupplierDatabaseLayer` through this SAME captured client/root/destination
authority. Those helpers bound 64 MiB ranges, at most 32 chunks/2 GiB, a
15-minute aggregate deadline and the final whole-layer hash. There is no second TLS trust path
or increased parent archive cap. Actual supplier transfer remains unqualified. The TLS pool and concrete
transport are private. Returned client shells and policy copies cannot
change the owned constructor. Before every request, the forwarding transport
revalidates the current mounted root bytes, including requests that might reuse
an existing TLS connection. A root rotation, removal, symlink substitution,
invalid PEM or canceled operation fails closed. Idle-connection cleanup is
forwarded; Close does not cancel or join an active response body. Callers own
request context cancellation, body Close and worker/body joins before lifecycle
cleanup.

`NewPinnedDatabaseAuthority` rejects a caller-selected HTTP client and captures
this operator registry transport before creating the private canonical catalog
loader. The opaque verified base captures immutable historical registry bindings at
Base verification and combines signing trust, exact registry roots,
root mount origin and transport policy into its physical fingerprint. Base
verification and update verification revalidate both mounted signing material
and mounted registry roots before and after canonical loading. Durable reads,
acceptance and use still must call revalidation before authority reads and at
the owned transaction commit boundary. The signing-only constructor remains
available for its existing scoped provenance tests; its empty registry binding
must not satisfy the future durable constructor's registry authority requirement.
After Close, the historical proof retains its original root/policy/origin hashes;
current Revalidate still rejects it.

The existing provider CA validation delegates to the shared strict PEM parser
with the same 256 KiB/64-CA limits. This removes duplicate parsing while
preserving the provider's existing captured bytes, SHA, eligibility and signing
authorization. The server's ordinary system-root composition is unchanged.

Proposed actual TLS tests bind a uniquely owned server to an existing private
interface and use an ephemeral CA/leaf certificate for that precise address.
They exercise the real canonical client, not an injected routing transport,
loopback exception or TLS skip. Missing private-interface prerequisites fail
explicitly. Tests cover actual TLS success, caller policy/client mutations,
root-byte rotation before pooled reuse, wrong CA, missing/symlink/invalid roots,
unrestricted/HTTP/legacy-private constructor policies, closure and cancellation.
The keyed signed-catalog test exercises private TLS through the pinned authority,
then rejects root rotation, caller transport, wrong CA and an unsigned parent.
Its scanner stays disabled; it proves neither production OIDC nor database
payload, live job provenance or durable acceptance.

The original and refined review artifacts remain under
`/tmp/plan032-w04-registry-trust-draft/`; source application grants no runtime
qualification.
Applying/generating migration 079, actual PostgreSQL 17/18 transactions, provider
session-lock/serializable composition and real scan-used witness are separate
required work. This draft adds no public v1 SDK fields and grants no caller
authority boolean.
