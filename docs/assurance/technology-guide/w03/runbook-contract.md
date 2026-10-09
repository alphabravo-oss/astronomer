# Canonical runbook contract checks

The current runbook uses the same `releasecontract.Manifest.Validate` rules
that the application consumes. Its precompiled verifier authenticates the
caller-pinned document before validating the exact release, current
`fresh_and_upgrade` mode, seven first-party image roles and complete artifact
compatibility contract. The old six-role/fresh-only `jq` predicate is removed.
The fixture executes the exact marked shell snippet against a real generated
manifest and actual official Cosign 3.1.3 QA signatures. Valid current metadata
passes; authentic signed stale mode, missing DR, duplicated role, unknown field
and wrong expected release fail. The QA mounted key is a separate identity from
the production keyless publisher documented by the runbook.

Detached document readers now use the same owned bytes returned from canonical
authentication, rather than reopening a potentially replaced file after
verification. The fixture replaces the original file and checks that the
authenticated reader snapshot remains unchanged. Application imports retain
signature-before-archive-open and safe extraction behavior.

The runbook's registry section describes portable operator-provided storage,
TLS/authentication, quotas, garbage collection, retained rollback/signature
subjects and recovery checks. It documents exact operator-owned CNPG/Barman
and other capability prerequisites, finite approved network authority, private
CA/credentials, disabled-unavailable application catalog, internal S3 endpoints
and the currently absent presigning feature. Actual provisioner status logging
supplies only redacted source/release identity, location, verification,
inventory completion, last successful sync and blocked category. Full Helm
values are no longer a diagnostic command.

Application export/import examples describe the new reviewed profile and
closed signed archive interfaces. They do not claim production OIDC publisher
execution: local source fixtures use a QA key, and the matching signed verifier
asset, catalog OCI child publication and supplier profile remain external
qualification prerequisites. Historical v1 procedures remain scoped to their
own tags. No source build or trust download is suggested for a production
offline install. Content identity is distinct from freshness, live egress,
object-store operations and disaster recovery qualification.

The [receipt](runbook-contract-evidence.json) binds changed source files and
the exact tested snippet hash. Eight Go packages, eight application/runbook
cases, seven existing release-kit cases, three mirror cases and the docs gate
pass. The host capacity block retained by the earlier receipt still prevents
claiming the full `make release-contract-check` gate passed. W03 remains active.

The new release adapter introduces no JSON serializer. It decodes the owned
authenticated bytes with the existing strict reader into the existing release
manifest type and calls its canonical validator. Its output is a static status
line. The existing detached verifier and application reader retain W03 format
ownership; W09 can record reader/site movement after integration.
