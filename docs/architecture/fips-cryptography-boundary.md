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
