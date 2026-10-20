# ADR: FIPS cryptography is not a v1 artifact claim

- Status: Accepted
- Date: 2026-09-17

## Decision

Astronomer v1 does not claim FIPS 140-2 or FIPS 140-3 validation, a FIPS mode,
or FedRAMP certification. The standard Go and Node/NGINX release artifacts are
the only supported artifacts. They are not built against a separately validated
cryptographic module and are not qualified as a FIPS boundary.

The `fedramp_moderate` compliance baseline is an operator configuration preset:
it applies selected retention, authentication, and workload-policy settings. It
does not make the product or an installation compliant or certified.

## Consequences

- Release notes, UI, documentation, and sales material must not imply a FIPS or
  certification claim for these artifacts.
- Operators that require FIPS must treat v1 as unsupported for that requirement.
- A future FIPS artifact requires a separate build definition, dependency and
  cryptographic-module inventory, runtime enforcement, test matrix, artifact
  identity, and release evidence. It cannot reuse the standard artifact's claim.

## Native Go assessment, 2026-10-10

[Go's native module documentation](https://go.dev/doc/security/fips140) separates
an exact frozen `GOFIPS140` module selection from runtime mode. The standard
artifact retains `GOFIPS140=off`. Neither `latest` nor the moving `certified`
selector is an immutable artifact identity. `GODEBUG=fips140=only` is a diagnostic
assessment mode with intentional errors and panics, not a production setting.
The older Go+BoringCrypto mechanism is unsupported and cannot be combined with
the native mode; it is not the proposed direction.

The installed selected compiler's module files identify `v1.0.0-c2097c7c` as
its certified snapshot. [CMVP certificate 5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247)
and its linked security policy define the module and operating environments;
the local Ubuntu 26.04 assessment host has not been established as one of those
validated environments. The current
[CMVP process list](https://csrc.nist.gov/Projects/cryptographic-module-validation-program/modules-in-process/modules-in-process-list),
updated October 9, lists the Geomys Go module at Comment Resolution. That row
does not identify a module version; it does not establish validation of the
installed `v1.26.0` in-process snapshot. Source snapshots and hashes are retained
in the W16 assessment evidence.

The assessment must distinguish native module operations from external age (X25519 + ChaCha20-Poly1305, not FIPS-approved; see ADR-0010),
password, TOTP, JWT/OIDC, Python DR, PostgreSQL, Node/NGINX and signing-tool
boundaries. A successful library round trip does not qualify those boundaries
or the full application. Existing historical and versioned ciphertext readers
and cookie/CSRF/bearer contracts remain required.

## Conditional future artifact

No optional FIPS artifact is selected by this decision. A customer requirement
would need an approved separate artifact and platform matrix: exact compiler,
module snapshot and checksum, certificate/security-policy revision, module
source and dependency closure, operating environment, runtime configuration,
algorithm inventory and end-to-end protocol evidence. It must also account for
external providers and the product's existing password and backup formats.
Changing a build flag alone cannot supply this evidence, and removing old
encrypted data readers to make an assessment pass is not acceptable.
