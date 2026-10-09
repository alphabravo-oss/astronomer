# Source-bound candidate OIDC prerequisite

The production server constructor currently requires the exact vendor tag
workflow identity and GitHub issuer. The keyed constructor fixture proves
consumer composition only. Neither path authorizes an unsigned production
startup or allows a QA key to masquerade as a vendor OIDC publisher.

A source-bound production candidate needs a separately reviewed
`workflow_dispatch` action on a protected branch. It can publish immutable
digest subjects into a candidate namespace without creating a future tag,
GitHub Release or replacement historical artifact. Its independently reviewed
consumer authority must pin the exact branch/workflow certificate identity,
GitHub issuer and immutable source commit. Broad identity regular expressions,
mutable branch-only trust and reuse of historical qualification are unsuitable.

Signed candidate metadata must bind the source commit, workflow/run identity,
actual version surfaces, immutable parent/child subjects and explicit
candidate/unreleased classification. It is not a final release manifest or an
assertion that any historical v1.2.0/v1.2.1 artifact changed. Candidate version
semantics and the closed metadata schema need an explicit reviewed contract
before adding a workflow or consumer selector.

The selected Cosign producer's real branch certificate and source-commit claims
must be captured and characterized. The canonical verifier must compare those
claims with the independent candidate authority before the same loader,
provisioner, provider and agent paths consume bytes. The existing tag production
constructor and issuer/identity checks remain unchanged. A signed metadata
source-commit field alone cannot substitute for independently checked publisher
claims.

Actual protected GitHub execution and the vendor-signed parent/children remain
external candidate prerequisites. This document is design preparation, with no
workflow execution, publication, deployment, new tag, selector or qualification
claim. The W03 mirror source slice does not implement this candidate policy.
