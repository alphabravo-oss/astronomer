# Prerequisite closure review followup

The source-bound [followup receipt](prerequisite-review-evidence.json) binds
exact captured database OCI config blobs as regular BOM members, alongside
manifest and single payload layer. Original descriptor hashes and sizes must
match; missing or changed config, URL fallbacks and inline descriptor data fail.
Descriptor negatives use a matching recalculated manifest subject digest, so
their rejection exercises the descriptor policy itself. No config bytes are
invented by production preparation or import. The local QA fixture's captured
config is explicitly fixture data.

The known Trivy chart configuration rejects nonobject operator/trivy values,
nonboolean scanner enablement and nonstring database locations. Capability
derivation validates every applicable component before returning its scope.
Actual renderer fixtures reject both string `"true"` and string `"false"`;
Helm's truthy string semantics cannot bypass the database role requirement.
An independent chart fixture verifies `values.schema.json` validation through
the existing `ToRenderValues` path. Valid integer values and schema-permitted
additive fields continue to render. Schema validation is not duplicated.

The existing asset input schema gains required `artifact_config_file`; the
strict Go reader requires it for database artifacts and rejects it for ordinary
runtime image roles. The signed `distribution.Asset` gains the config member
path. These are extensions of the existing W03 BOM serialization site, with no
new serializer call or public API/database format. W09 receives the exact owner,
schema and reader change for its integrated site inventory.

These controls complete the bounded source review followup, not full W03
qualification. Supplier payload freshness, actual local OCI publication/import,
pipeline/runbook wiring and live disconnected runtime qualification remain
outstanding.
