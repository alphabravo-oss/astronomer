# Adopted cloud-provider acceptance

Run `make validate-live-cloud-acceptance` only against the protected acceptance
environment. The target JSON must contain exactly one pre-existing EKS, GKE,
AKS, and DOKS cluster plus its already-linked credential and project UUID. The
driver has no provisioning operation.

Required variables are `ASTRO_BASE_URL`, the identical
`ASTRO_EXPECTED_BASE_URL`, `ASTRO_AUTH_TOKEN`, `CLOUD_ACCEPTANCE_TARGETS`, and a
globally routable host-only `CLOUD_ACCEPTANCE_CIDR` (`/32` or `/128`). Prefer the
manual `credentialed adopted-cloud acceptance` workflow: its
`cloud-acceptance` environment should require reviewers and stores the API
token and test CIDR as environment secrets.

The driver verifies cluster/provider/name and credential/project/target
bindings before mutation. It snapshots the existing policy and provider
effective ranges privately, adds the test host, replays one reconcile with the
same idempotency key, waits for durable convergence, then restores every
changed target in reverse order even after failure. Acceptance passes only when
the exact original policy and provider-effective ranges return.

Upload only `cloud-acceptance.json` and its Sigstore bundle. The `.recovery`
directory contains raw policy material and must remain private. The public
report contains provider names, digests, and booleans—never credentials,
cluster UUIDs, CIDRs, API URLs, role ARNs, or provider response bodies.
