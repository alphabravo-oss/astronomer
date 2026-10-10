# Plan 032 W16 cryptography and rotation evidence

The supported v1 artifact retains ordinary hardened cryptography. No FIPS mode,
FIPS validation, validated operating environment, or FedRAMP certification is
claimed. The [accepted ADR](../../../architecture/fips-cryptography-boundary.md)
keeps a future compliance artifact conditional on a separate customer requirement,
artifact identity, module/provider closure, operating environment and protocol
qualification. No such artifact was selected or published here.

## Source and scope

The original three regression failures bind to `f3191eda25ce03616b59cb576927fd37e8fc007d`.
The production correction is `ac2f4d12`; the completed cancellation characterization
and full qualified source are `df5803bc8bf5905827c7e18c0009655bd3f72f9b`.
[receipt.json](receipt.json) retains exact before/after Git source fingerprints,
commands, terminal counts, failed attempts and their log hashes.
[restricted-artifacts.json](restricted-artifacts.json) identifies 103 retained
assessment artifacts. It excludes connection references and key material.

The [Go AST import inventory](go-source-imports.json) parses every one of the
2,537 tracked Go files and records 254 files with literal crypto/provider imports.
The [resolved module graph](go-module-closure.json) records 697 modules. Neither
is a complete cryptographic call graph. The
[manual boundary inventory](boundary-inventory.json) separately names TLS,
JWT/refresh, OIDC, secret envelopes, GitOps credentials, password readers, TOTP,
Python DR, signatures, physical backup, browser/NGINX and non-security/transitive
initialization boundaries, with exact source hashes and qualification limits.

## Actual rotation qualification

The frozen baseline failed all three selected parents: an incomplete ciphertext
page was counted as successful progress; the management-backup credential column
was missing; and a database error emitted untrusted message/detail content.
All three pass on the corrected source. Query, scan, iterator, close and update
fixtures assert private database content, ciphertext and plaintext are absent;
only a validated SQLSTATE is retained.

The full `keyrotate`, `auth` and `credential` race suites pass **166 parents / 252
cases, zero skips and zero failures**. The five affected/helper packages also
pass `go vet`. The vet result was captured by the tool invocation; no independent
raw vet log is claimed.

The live fence verifies the owned CNPG cluster UID, current primary/controller,
readiness, unique primary service and node endpoint immediately before each
mutating process. Only `plan032_w16_rotation` was migrated and used. Its actual
PostgreSQL major is 18, schema is 75 and clean, and reversible floor remains 66.
No shared database, role, cluster, PVC or customer key was changed.

Four required PostgreSQL parents pass under the race detector with zero skips:

- Actual catalog inventory proves all 27 plain encrypted columns are covered.
- A dry run preserves ciphertext; live rotation promotes two synthetic SMTP
  rows, preserves independently retained old ciphertext, rejects old-only reads
  of new rows, and is idempotent when resumed.
- Interrupted rotation records one acknowledged CAS before cancellation,
  observes the owned server statement through completion, and derives exact
  resume counts from the measured persisted envelope.
- A concurrent writer survives the ciphertext CAS miss; resume does not
  overwrite its credential.

The original interruption test failed because it assumed client cancellation
guaranteed rollback of an autocommit UPDATE. That failure is retained. Natural
client cancellation left the measured backend active; its statement subsequently
committed and resume correctly skipped both current rows. A separate cancellation
of only the owned blocked server backend completed the abort before gate release;
resume then rewrote exactly one old row and skipped one current row. Both paths
require final zero failures, valid current-key decryption and old-only rejection.
Two further retained fixture failures corrected operation-count versus signal-count
expectations and distinguished a returned pooled connection executing its observer
from the original interrupted UPDATE. Production transaction semantics were not
changed to satisfy a fixture.

The earlier independently qualified CNPG18 physical backup/fresh restore with
separately recovered historical/current keys is referenced by its exact advisor
receipt hash. Its source and schema 72 remain historical qualification identities;
they are not relabelled as this schema-75 rotation test. The live-row sweep and
the [rotation runbook](../../../secret-rotation-runbook.md) explicitly do not
authorize destroying keys required by retained backups, archives, delayed tasks
or rollback artifacts.

## Native Go mode assessment

[Primary snapshots](primary-sources.json) bind dated official Go documentation,
release notes, CMVP certificate 5247, its security policy and the current process
list. [Exact module bytes](module-snapshot.json) bind this compiler's installed
`v1.0.0-c2097c7c` snapshot. This identifies the assessment module; it does not
establish the Ubuntu 26.04 host as a validated operating environment or call the
snapshot globally latest. The process-list row does not identify a module version.

The small probe runs 21 direct controls across `GOFIPS140=off` and the frozen
module with runtime `on`/diagnostic `only`. Identity/build information is bound
to the immutable source and actual binaries. AES with a random GCM nonce and
direct Fernet round trips complete; bcrypt and Argon2 also complete **outside the
native module**, including in `only` mode. The arbitrary-nonce GCM control is
rejected in `only` mode. Unknown cases exit 2. No synthetic keys, tokens, hashes
or untrusted exception messages are printed.

The initial assessment validator expected a short `GOFIPS140=v1.0.0` setting,
while actual compiler metadata records `v1.0.0-c2097c7c`; default `off` is omitted
from build settings. Those failed assessment outputs remain unchanged. A separate
snapshot-binding receipt evaluates all 21 original raw outputs against the exact
installed mapping and records their qualified control results. This is a metadata
validator correction, not repeated execution or an application qualification.

The earlier whole-auth `only` attempt failed before tests at third-party Redis
Lua SHA1 initialization. That failure remains referenced. The direct-library
probe deliberately avoids that startup boundary; its success does not make
the full application work in `only` mode, which is diagnostic and unsuitable
as a production configuration.

## Work-package disposition

| Task | Disposition |
| --- | --- |
| W16.1 | Current claim recorded; complete literal Go denominator, module graph and twelve explicit provider/process boundaries inventoried. No call-graph completeness claim. |
| W16.2 | Existing accepted v1 ordinary-cryptography decision retained; no new customer compliance requirement inferred. |
| W16.3 | Native module/build/runtime/platform distinctions assessed against dated primary records; external provider and operating-environment gaps explicit. |
| W16.4 | Source-bound 21-control diagnostic spike and historical whole-auth failure retained; no certification inference. |
| W16.5 | Conditional artifact not selected; separate identity, platform, provenance, runtime and protocol obligations specified in the ADR. |
| W16.6 | Versioned and legacy readers, key IDs and multi-key decryption retained and tested; no algorithm migration introduced. |
| W16.7 | Scoped real rotation, interruption, observed cancellation outcomes, CAS conflict, mixed-key/idempotence and historical-key restore evidence qualified. Catalog coverage does not claim every column has an end-to-end row-rotation fixture. |
| W16.8 | Independent backup/archive/delayed/rollback key obligations gate retirement; a live zero count or successful sweep cannot authorize it. |
| W16.9 | Cookie/CSRF/refresh and explicit bearer channels preserved; ordinary auth regression suite passes. Incomplete browser qualification remains W12, not a new FIPS claim. |
| W16.10 | Only scoped verified capabilities published; FIPS/FedRAMP and future artifact/platform certification remain unavailable. |
