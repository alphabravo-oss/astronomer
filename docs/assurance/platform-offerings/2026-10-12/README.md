# Platform Tools / Applications validation — 2026-10-12

The frontend verification archive contains the original evidence manifest and
all original logs, including their byte-for-byte formatting. Extract it to a
separate directory and verify each artifact against the SHA256 in `evidence.json`.
`frontend-verification.json` is a convenient identical copy of that manifest.
All 10 subgates passed and the source tree remained stable during execution.
The plan's completion note and this evidence packaging were finalized afterward;
implementation code was not changed after the passing run.

`producer-consumer.log` covers the actual regenerated catalog through the existing
Go consumer signed-OCI interoperability test. The two PNG files are T3 browser
captures of the real shared components in an explicitly synthetic fixture.
They are not proof of a live application install or cluster qualification.

The catalog repository retains its own pinned-tool audit, SBOM and upstream
version report in `docs/evidence/033/`. No application is promoted to Assured by
these source and component checks.
