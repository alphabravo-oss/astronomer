# Supplier transfer/authentication failed baseline

Frozen source `bd4ba64c546cc716151d5681f79bac5683e05567` was actually tested with
Go 1.27.2, `GOMAXPROCS=4`, `GOFLAGS=-p=2`, and the selected checksum-bound
Cosign 3.1.3. The retained gate manifest binds exact commands, source Git tree,
before/after source bytes and raw logs. This is a failed baseline, not qualification.

The focused race run passed 31 test events and failed 19, with zero skips;
4 parent tests passed. Full distribution/resolver race passed 326 events and
failed the same 19, with zero skips and 80 passed parent tests. Resolver passed,
including the owned TLS peer's actual anonymous/private bearer challenge,
token and two immutable ranges. Distribution failed. The selected actual Cosign
signed-feed fixture passed (1.00 seconds); that does not qualify new transfer code.

Distribution transfer positives and the range/cancellation/ownership negatives
stopped before staging because their `testing.TempDir` output parents did not
satisfy the production private-directory check. Selected Go testing source uses
`os.Mkdir(dir, 0o777)` for that child; with this host umask it is not mode0700.
The fixtures need explicit private ownership/mode preparation to reach their
intended transfer controls. Production private-directory rejection stays strict.

JSON governance failed the pre-existing renamed configuration reader site:
`fetchArtifactMetadata` is actual while the older inventory still attributes
that same call to `FetchVerifiedOCIArtifactMetadata`. The exact new auth reader
is owned. No broad inventory regeneration or unrelated row change was made.

Vet, dependency boundaries, complexity (unchanged ceilings) and Node26 docs
checks passed. All eight capacity guards passed, ending at 68GiB/21% available.
Read-only documentation dependency use was byte-equal to this frontend lock.
Source before and after was byte-equal. No supplier token, whole upstream layer,
endorsement, protected OIDC publisher, durable projection, real scan-used witness
or cluster/no-egress qualification occurred. Whole W04 remains in progress.
