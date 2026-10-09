# Exact prerequisite OCI children

Trivy and Java database assets now retain an original supplier subject and a
publisher-endorsed subject with the same manifest digest. Preparation emits
the exact captured manifest, configuration and payload as standard OCI child
graphs, without rebuilding upstream bytes. The signed parent binds both
locations and the captured member hashes. Metadata/index validation rejects an
omitted child, changed payload identity or a different emitted subject.

Publication signs database children before the parent. Canonical runtime and
offline loaders authenticate each child with the same mandatory publisher
policy and compare its payload with the signed captured archive. A signed
parent cannot authorize an unsigned database child. Original upstream source
identity remains audit metadata; it is not a public fetch fallback.

Effective Trivy values reconstruct the exact split registry/repository subject.
They first project to the publisher-endorsed immutable child and then to an
authenticated mirror's `artifact` rewrite. Missing mappings or changed digests
fail. Database fields are rewritten once, preserving relative repository field
shape and exact digest. Mirror network authority remains independently injected;
signed location metadata does not grant destination permission. Existing image
and chart rewrites continue to use their canonical path.

The actual official Cosign 3.1.3 QA fixture exercises a parent, three charts and
two database children, repeat publication, canonical offline capture/import,
runtime value projection and unsigned database rejection. The mounted QA key
is distinct from production OIDC. Payloads are small QA data, not functional
Trivy databases; actual supplier freshness, archive sizes and startup remain
unqualified. The fixed parent archive, per-layer and total graph bounds are
preserved. An oversized real profile must fail or use a reviewed versioned
descriptor-only child contract, rather than increasing bounds without analysis.

The [source-bound receipt](prerequisite-oci-evidence.json) retains both Go package
race checks, eight Go package results, 11 metadata fixtures, docs and the full
release-contract gate with 61 Python fixtures and release/chart/Go checks.
Shared host capacity now permits the full gate; the earlier blocked retry
remains historical. W03 is active. Trusted downstream Flux private CA delivery,
complete mirror and workflow wiring, real supplier profiles, signed tool
availability and later live qualifications remain explicit pending work.

The added fields belong to existing W03 BOM and emitted subject metadata.
Existing serializers are reused; the new asset delivery helper uses the same
strict captured OCI manifest reader. No public API or database schema changed.
