# Exact inline configuration graph qualification

This packet qualifies source slice `36138b21` plus its one-line diagnostic
correction `21e1bb3e`. It preserves the original failed full race run. It does
not qualify whole W03/W04, an upstream supplier payload, a production publisher,
database freshness, actual scan-used provenance, or durable database projection.

The supplier manifests previously captured by the metadata-only observer use
the OCI empty configuration media type, exact `{}` bytes, size 2 and SHA-256
`44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a`.
The graph now retains this exact inline descriptor and original manifest while
requiring the same separately stored configuration blob. It never substitutes
a fetched configuration or allows arbitrary inline content. The embedded
database asset contract version 1 still rejects inline descriptors.

| Retained run | Actual outcome |
| --- | --- |
| Unchanged production `ad2aaa28` with the new positive tests | Four intended inline-positive leaf failures, four failed parents, one passing control; 9 terminals, no skips. |
| Original `36138b21` focused race | 7 parents, 91 terminals, no failures or skips. |
| Original `36138b21` full three-package race | 123 parents, 604 terminals; two rejected-descriptor diagnostic assertion failures and their failed parent, no skips. |
| Corrected `21e1bb3e` focused race | 7 parents, 91 terminals, no failures or skips. |
| Corrected `21e1bb3e` full three-package race | 123 parents, 604 terminals, no failures or skips; all six required actual Cosign fixture parents passed. |
| Corrected vet, JSON governance, complexity, dependency boundaries, docs, CLI docs and release contract | All passed. These gates did not run after the original full-race failure. |

The original failures were `config-url` and `config-inline` under
`TestOfflineStoreOwnsBytesAndCannotDelegateNetwork`. Both invalid descriptors
were rejected, but the diagnostic lacked the word `descriptor` required by the
existing assertion. The correction changes only the diagnostic to
`offline OCI configuration descriptor missing or changed`; assertions and
descriptor enforcement remain unchanged.

Commands, exact selectors and before/after source fingerprints are retained in
each receipt. The full race command was:

```sh
go test -json -race ./internal/delivery/distribution ./internal/delivery/resolver ./deploy/bundles/cmd/distribution -count=1
```

Actual tool probes returned Go 1.27.2 and Node 26.11.1. The environment used
`GOTOOLCHAIN=go1.27.2`, `GOMAXPROCS=4`, `GOFLAGS=-p=2`, `GOWORK=off`, and the
qualified Cosign 3.1.3 binary with SHA-256
`4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71`.
The Node path and read-only `NODE_PATH` used matching W07 package/lock bytes;
no dependency tree was modified. Actual Cosign fixtures use local QA keys,
not production GitHub OIDC identity.

The finite driver checks capacity before launch, every ten seconds, after each
gate and in `finally`. It retains an unreaped leader until its owned process
group drains, uses bounded TERM/KILL cleanup, and restores the five baseline
production files only after joined cleanup. Seven controls cover timeout,
capacity/prelaunch failure, baseline restoration, unconfirmed drain, overall
deadline and a fast-exiting leader with a surviving pipe child. Both real runs
retained equal final source snapshots. The original final capacity was 68 GiB,
21%; the corrected final capacity was 67 GiB, 20%, meeting the 20 GiB/20% floor.

Raw receipts and logs are stored as deterministic gzip files preserving their
original decompressed bytes. `bindings.json` records both stored and raw hashes
and sizes. The private originals remain under
`/root/.cache/astronomer-032/qualification/w04-inline-graph-361/` and
`/root/.cache/astronomer-032/qualification/w04-inline-graph-21e1/`.

Run the consistency checker from the repository:

```sh
python3 docs/assurance/technology-guide/w04/inline-graph-qualification/check.py --self-test
```

The checker validates both retained statuses, raw terminal counts and required
fixture names, finite process cleanup, capacity logs, exact Git tree and source
membership, baseline overrides, file hashes and preserved scope limitations.
Its 20 negative assertions reject changed scope claims, tool/compiler identity,
source/tree binding, skipped/failed tests, altered counts, missing drain and
failed capacity. This unsigned consistency packet does not authorize a release.
