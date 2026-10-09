# Immutable distribution publication

The operator producer now copies exact prepared OCI chart subjects, signs each
child before its parent, and reads them back through the same production
distribution loader before reporting success. A copy, signer exit code or
recorded digest alone cannot qualify the result. Registry credentials use the
normal protected Docker credential store and decorate the same bounded,
DNS-pinned HTTP client. Production requires the exact Astronomer tagged
`release.yaml` OIDC identity. QA key mode has a separate explicit policy.

Preparation metadata, index references, manifest/configuration/layer hashes and
sizes must agree. A bounded closed snapshot rejects extra files, missing blobs,
links and an unknown OCI layout version before any write. Publication consumes
owned revalidated bytes, preserving content identity if the original operator
input changes later.

The actual official Cosign 3.1.3 QA fixture publishes a parent and three chart
children to an owned loopback registry, verifies them with mounted QA trust,
and repeats the operation without changing child identities. It rejects missing
child signatures and a QA key presented as a production publisher. A working
external proxy trap receives one control request and zero requests during
signing/verification. The initial fixture failure was an ORAS origin check
against a test-rewritten response URL; the correction preserves the original
response request in the test transport. Production origin checks are unchanged.

The [source-bound receipt](publication-evidence.json) retains actual commands
and results. Eight Go packages, existing kit/mirror tests, docs and the full
release-contract gate pass. The earlier host capacity failure remains historical;
the full gate now passes with 61 Python fixtures, release artifact/chart checks
and Go release contracts. The prepared publisher selects the characterized
legacy Cosign signature interface. Production OIDC execution, offline graph
transfer, all prerequisite child subjects, mirror/pipeline wiring, current
supplier profiles, live disconnected operation and object-store recovery remain
separate incomplete qualifications. W03 is active.

No new JSON serializer is introduced by this slice. Typed metadata and OCI
layout readers reuse the existing bounded strict distribution reader; W03 owns
those schemas and their publisher/runtime consumers.

The [private CA review receipt](publication-ca-review-evidence.json) exercises
the actual signer adapter against an owned TLS registry. Without its approved
CA signing fails; `--registry-cacert` with the owned immutable CA snapshot makes
signing succeed, and the canonical verifier authenticates readback. The CLI
composition supplies the same captured CA bytes to ORAS/readback and signing.
An absolute regular executable is checked before layout preparation or the
first write. This closes the earlier adapter coverage gap: the initial publisher
fixture injected a QA signer to route its logical origin to an owned listener.
