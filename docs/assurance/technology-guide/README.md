# Technology-guide assurance baseline

This is W00 evidence for [Plan 032](../../../advisor-plans/032-technology-guide-alignment-and-latest-stable-master-plan.md), prepared on 2026-10-09 from application commit `6c3da1882c76662f1b7e01e80f89f5e2978bc7b4` and catalog commit `3e24ab98f0805bd54d6591d9b9b961bbfd1f5db5`. The application worktree was clean before W00 changes. No runtime source drift exists from the plan baseline `3b378ef16141b9eaac35a1e5fe292e061b69f5f1`; intervening changes concern advisor records. Evidence describes that input commit; later commits must renew affected checks.

| Record | Purpose |
| --- | --- |
| [Baseline JSON](baseline.json) | Repository identities, dirty paths, released identity, safe live projections and timestamps |
| [Component JSON](components.json) and [ownership](components.md) | Go/npm graphs, binaries, built-ins, Flux, image consumers, input digests and external prerequisites |
| [Reconciliation](reconciliation.md) | E01–E24 dispositions and prior-plan reuse |
| [Guide decisions](decision-matrix.md) | Literal prescription, product decision, ADR, review trigger, enforcement |
| [Compatibility](compatibility-surfaces.md) | Public/generated/durable/trust/upgrade surfaces captured before changes |
| [Invariant contracts](invariants.md) | Existing coverage, new behavioral contract, remaining integration assertions |
| [Execution handoff](execution.md) | Serial owners, prerequisites, existing and proposed commands |
| [Verification records](verification.json) | Source-bound commands, outcomes, log digests and limitations |
| [Evidence schema](evidence.schema.json) and [validator](validate-evidence.py) | Closed records, source/file binding, timestamps, counts and result semantics |

Run `python3 docs/assurance/technology-guide/validate-evidence.py` to check the records. It requires the already-installed Python `jsonschema` library and makes no network requests. The candidate tree digest is compared to its captured identity (excluding this evidence directory), and input digests are compared to the recorded Git commit rather than a changed checkout. Evidence is unsigned; it does not imply reviewer approval or release qualification.

Read-only native k3s inspection confirms Helm revision 233, application 1.2.1 and Kubernetes `v1.35.7+k3s1`. Its database is a standalone PostgreSQL StatefulSet and cache standalone Valkey. CNPG/Barman CRDs and operators are absent. **This environment cannot supply CNPG qualification evidence.** Preserve its PVC identities; W12 needs a separate owned CNPG environment. No live mutation, Secret/private Helm-value read, database-content query or backup-history inspection occurred.

Three application deployments were ready. Flux source-controller was 1/2 ready, and the Charlie StatefulSet reported zero ready replicas. These are point-in-time observations, not diagnoses or a whole-product health verdict. W17 must re-observe and investigate before claiming candidate health.

GitHub's latest release remains v1.2.1 with 38 assets. Successful historical run `37845920964` reports source `3b378ef16141b9eaac35a1e5fe292e061b69f5f1`; the local tag points to a different commit, recorded in `baseline.json`. This distinguishes tag/build identities without claiming newly verified provenance. W17 must inspect the signed manifest/provenance before selecting artifacts; released bytes remain immutable.

W00 prepares evidence and a render-level contract. It does not close W01–W17, qualify latest versions, or attribute historical browser/cloud/restore results to this candidate. The integration reviewer owns plan/index changes and completion verdicts.
