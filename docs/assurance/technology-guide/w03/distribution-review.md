# W03 core review followup

The source-bound test receipt records eight passing Go packages with the selected
official Cosign 3.1.3 binary enabled. This is a local QA identity qualification,
not Astronomer publisher authentication or a disconnected cluster qualification.

The actual CLI signs the parent and every child using an ephemeral key, isolated
HOME and Docker configuration, and an external proxy trap. The consumer verifies
the signatures before interpreting the archive, loads the verified catalog, and
passes the projected source and values to the actual agent BuildAssignment.
Every resulting OCIRepository retains the child manifest digest and mandatory
Cosign verification. The HelmRelease consumes that OCIRepository; an HTTP-only
agent rejects the assignment. No external proxy request occurred.

The qualified producer interface explicitly selects deprecated legacy simple
signing with `--new-bundle-format=false --registry-referrers-mode=legacy` and
disables transparency upload and signing configuration with `--tlog-upload=false
--use-signing-config=false`. The fixture permits its own loopback HTTP registry;
that flag is not a production transport permission. Default Cosign 3 bundle
output is not qualified by this test. Future publication must retain the exact
qualified interface or requalify a replacement. The first agent fixture attempt
omitted its required upgrade remediation setting and failed validation; the
retained passing run supplies the actual rollback policy.

Review fixes share one strict JSON decoder between BOMs and fetched OCI
manifests, including duplicate-key, depth, unknown-field and trailing-document
rejection. Direct manifest negatives use valid subject hashes and assert no
payload read, so their rejection is not an unrelated signature mismatch.
Authenticated nil component values fail with a blocked values category before
mirror projection. Producer catalog/chart inputs use a bounded regular-file
reader before allocation, and oversized, directory and symlink inputs fail.

The remaining asset capability closure, authenticated transfer/import,
application catalog packaging, chart/pipeline v2 wiring, runbooks and external
supplier qualification remain active W03 work. Historical v1 verification is
retained separately; these tests do not approve historical unsigned artifacts
for the new production loader.
