# Browser engineering sidecar

The opt-in `efficiency.live.spec.ts` journeys attach sanitized
`astronomer-browser-engineering/v1` JSON beside the existing benchmark recorder.
They do not modify the closed Rancher comparison schema, define performance
budgets, or qualify Plan 030. Functional assertions can pass while performance
collection remains partial or missing.

Run from the repository root with the existing live fixture/credential setup:

```sh
LIVE_BROWSER_ENGINEERING=1 scripts/test-live-browser.sh
```

Without that flag, these journeys attach `collection_status: "not_run"` and skip.
Skipped journeys provide no live gate coverage. Enabled execution requires the
existing cluster, project, backup namespace, and backup ConfigMap fixture variables; the denied
journey also requires `LIVE_RESTRICTED_EMAIL` and `LIVE_RESTRICTED_PASSWORD`.
Missing fixtures fail explicitly. Existing runner prerequisites still apply.

Optionally supply `LIVE_BROWSER_ENGINEERING_PROVENANCE` as JSON with exactly
`source_commit` (40 or 64 hexadecimal characters), `images_sha256`,
`chart_values_sha256`, `dataset_sha256`, and `hardware_sha256` (each 64 hexadecimal
characters). These are immutable identity declarations, marked
`supplied_unverified`; the collector does not independently verify them. Omission
is explicitly `missing`. Extra keys, partial sets, and non-hash values fail with a
fixed error. No fixture names, credentials, URLs, headers, or response bodies are
written into the sidecar. Screenshot, video, and tracing capture are disabled for
these journeys; ordinary runner diagnostics are outside this artifact contract.

Each page window lasts up to 45 seconds, including instrumentation setup. This
is an engineering sample, not the sustained E04 window or E11 soak. Network
listeners freeze before asynchronous collectors. Requests that start inside the
window are grouped using fixed labels; completions, transport failures, and
unfinished requests are distinct. Completion is not HTTP success: observed
2xx/3xx/4xx/5xx/other response counts are separate, and an open 200 stream can remain
unfinished. Pre-window completions are counted separately; pre-window unfinished
requests are unknown. At most 2048 active request identities are retained;
overflow is explicit and not misclassified as carry-in.

Resource Timing reports completed same-origin entries whose start and end fit
the window. Transfer, encoded-body, and decoded-body bytes are separate; cached
zeros remain valid. Cross-origin/invalid sizes are opaque. This covers only the
final main document, excludes iframe/worker and previous-document entries, and
has a different denominator from network lifecycle counts. Numeric entry
retention is capped at 4096 with overflow reported. Unsupported observers remain
explicitly unavailable. Long-task timing is browser work, not React render or
commit timing. Visibility is observed, not inferred from which tab was created;
visibility samples are bounded at 256.

Interactions measure action through the journey's DOM/response assertion. Samples
are bounded at 256, with nearest-rank p50/p95 and maximum; missing or overflowed
samples have null percentiles. Failed and unfinished interactions are separate.
A single observation is not statistically representative. CDP reports page-target
JS heap snapshots, never total browser memory. Duration deltas are omitted across
navigation or counter reset; snapshots are collected after the frozen window.
Two tabs produce separate primary/secondary artifacts: do not sum heaps into a
total memory claim. CDP and page collection waits are bounded; late sessions are
detached. One generic navigation bootstrap per page reads the latest numeric
window; prior observers are stopped and expired windows cannot restart.

Journeys cover cluster list, ConfigMap list, delivery inventory, scoped search, two tabs,
rapid namespace changes, stream-open, stream-only blocked fallback, recovery, and
denied authorization. Data journeys await completed response bodies and actual fixture rows. Search
requires zero cluster failures, no errors, no truncation, and the exact fixture
cluster/namespace/name. Incomplete results fail the functional assertion.
Rapid changes assert the final requested scope and fixture row; debounce
may eliminate the prior request, so this does not establish actual transport
cancellation. Stream fallback blocks only the SSE route before navigation and
asserts repeated successful list reads; recovery reloads after removing the block.
This is not an in-place stream fault/recovery or full reconnect qualification.
Functional assertions are separate from engineering measurements and no synthetic
performance pass thresholds are supplied.

Offline verification:

```sh
cd frontend
npm test -- src/test/engineering-metrics.test.ts src/test/engineering-recorder.test.ts
npm run type-check
npm run lint
npx playwright test --project=live --list tests/e2e-live/efficiency.live.spec.ts
```

Offline tests exercise numeric privacy projection, response classifications,
finite-window freezing, overflow, capability gaps, cleanup, sequential page
windows, late CDP acquisition, and navigation-reset handling. They do not provide
live browser, cluster, comparison, or sustained-load evidence.
