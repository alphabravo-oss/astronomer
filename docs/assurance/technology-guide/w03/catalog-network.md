# W03 catalog runtime and chart composition

Both scheduler compositions and the interactive catalog handler use the same
fixed canonical destination authority as the distribution loader. OCI token,
manifest, blob, tag and optional catalog requests inherit the injected bounded
client. Database repository URLs and signed content cannot grant network access.
Connected public CDN redirects retain DNS/connect validation and strip secrets;
restricted clients accept only finite operator origins and reject cross-origin
redirects. A private-mirror flag without explicit origins is invalid.

Disconnected first boot never schedules the legacy repository bootstrap. A
disabled catalog reports `application_catalog` unavailable. An enabled catalog
requires a digest-pinned OCI source, expected payload digest and mandatory
signature policy. The server rejects direct configuration lacking those inputs
before scheduling or fetching. Scheduler and interactive hydration fixtures
deny public, unapproved private and metadata URLs before using a fallback
transport. Repository failures retain opaque IDs and static categories rather
than source URLs, tokens or upstream library diagnostics.

Actual chart renders mount delivery/catalog CA and public trust material into
the consumers and pass the destination policy through their shared ConfigMap.
The server additionally mounts the operator's detached mirror endorsement.
Partial endorsement, unsigned disconnected catalog and unknown policy settings
fail rendering. Changing the endorsement creates a new immutable ConfigMap name.
Secret values are not required by the producer contract or retained evidence.

The final frozen Helm gate passes lint, development and production renders and
all deployment contract tests. The initial gate failure is retained: the new
content hash invalidated an old literal assertion, and an existing frontend test
expected a direct nginx FROM instead of the actual named stage. The replacement
checks the digest-pinned selected nginx authority, shared runtime base, stage
ordering and baked-version copy. A subsequent gate had passing checks but failed
its source-stability receipt because source was edited while it ran; it was
repeated with external logs and no source mutation. No gate was weakened.

Catalog/server/scheduler suites, the hydration regression, config documentation,
dependency boundaries and complexity budget pass. One new first-boot fixture
initially supplied an invalid empty-origin restricted policy; its corrected
finite-origin fixture passes. The worker function ceiling decreases from 452
to 451 lines. No source ceiling increases.

This slice does not qualify a supplier catalog, signed application archive,
fresh-cache disconnected cluster or RustFS data operations. Those package and
runtime conditions remain explicit in the source-bound receipt.
