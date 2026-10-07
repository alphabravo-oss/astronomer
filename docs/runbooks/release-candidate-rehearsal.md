# Release-candidate qualification and promotion

The release workflow qualifies every image reachable from the signed release
manifest: first-party workloads, chart runtime dependencies, Flux controllers,
built-in bundle images, and containerized Charlie artifacts. Each exact digest
must publish `linux/amd64` and `linux/arm64`, pass the fixed high/critical Trivy
gate, and produce an SPDX SBOM. For tags other than v1.2.0, licenses must be in
`deploy/release/license-policy.json`. A waiver must name the exact image digest,
category and finding IDs, approver, reason, and future expiry. Stale, duplicate,
expired, mutable-reference, and unused waivers fail the release.

For **v1.2.0 only**, release and recovery workflows automatically run the
rehearsal below against v1.1.0 after all three clean-cluster Kubernetes checks
pass. Promotion requires that job to succeed, verifies its signed evidence
against the current producer run and original build run, and checks that both
the runtime-image report and rehearsal bind the same signed release manifest.
The release attaches the rehearsal evidence, its signature and
`release-qualification.json`. Cloud, scale, Rancher and human accessibility
certifications and license qualification are explicitly deferred. Unresolved
license findings and per-image pending-review status remain in the signed runtime
report and retained SBOMs; vulnerability failures still block publication.
A separately named approver is not
required for this tag. The changelog discloses this qualification scope.

For subsequent releases, run the manual `pre-promotion release candidate rehearsal` workflow with the
target tag, source release-workflow run ID, and previous published tag. The
`release-candidate` environment should require reviewers. The workflow creates
one unique k3d cluster derived from its run ID and refuses to reuse an existing
cluster. Deletion requires the matching `destroy-astronomer-rc-<run-id>` fence
and occurs only after this invocation records that it created the cluster.

The rehearsal installs the previous signed release, inserts a disposable
Fernet proof, captures the full private Helm/Secret/PostgreSQL backup, restores
the dump into a clean database, verifies schema and the restored proof under
the original key, upgrades using the pre-promotion signed manifest, and checks
readiness. Private artifacts live only under an owner-readable `mktemp`
directory and are destroyed with the owned cluster. Only the closed-schema,
digest-only `rc-rehearsal-evidence.json` and Sigstore bundle are retained.

For tags other than v1.2.0, promotion runs in the protected `release-production` environment. Its
`RELEASE_APPROVAL_JSON` must conform to
`deploy/release/release-approval.schema.json` and bind the exact tag, commit,
source run, runtime-image report digest, RC report digest, cloud acceptance
digest, named approver, and external NVDA, Narrator, and VoiceOver results.
The runtime-image digest is recomputed from the downloaded release artifact;
missing, mismatched, unknown, or not-passed evidence fails before publication.
