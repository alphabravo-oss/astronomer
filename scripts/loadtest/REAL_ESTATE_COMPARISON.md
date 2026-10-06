# Offline estate comparison

Run the existing entrypoint with `-compare-estate-baseline before.json
-compare-estate-candidate after.json -out comparison.md`. This mode reads local
files only, before authentication, profile loading, provisioning or HTTP setup.
Other workload flags and nonempty `LOADTEST_*` settings except `LOADTEST_OUT` are
rejected. It writes Markdown, `.json`, and `.sha256` files. Checksum entries use the actual output basenames; run
`sha256sum -c comparison.md.sha256` from the output directory. Basenames containing
a newline, carriage return or backslash are rejected. A successful command means artifacts were written,
not that optimization criteria passed. `qualified` is always false.

Input is the existing v2 estate report. Decoding rejects duplicate keys, unknown
fields, trailing JSON, nesting over 32, more than one million tokens, oversized
strings, reports over 32 MiB, and inconsistent bounded accounting. Complete
series deltas are rebuilt from Last minus First; group and family sums must agree.
Partial adjacent deltas are producer-reported diagnostics: raw samples are absent,
so this is consistency validation, not authentication or independent reconstruction.

Every member/phase has separate phase workload and member scheduled/completed/
success/failed counts. Member requested means actually scheduled, not an invented
share of phase RPS; unallocated search work remains phase-wide. Rates use the
first-to-last successful scrape interval, not nominal phase duration. Missing
windows stay unavailable; zero baseline percentages are undefined. All seven
transport outcomes are retained. Combined `delivery_lists` includes LIST calls
from shared_observation, delivery_inventory and delivery_assignment_observation.
`delivery_observation_total` includes all operations from those three consumers,
including assignment cache LIST/WATCH. `total_family` additionally includes other.
These are instrumented shared-client costs, not all Kubernetes traffic.

Descriptive numbers can be emitted for failed historical reports. Eligibility
requires the unchanged strict report comparator, complete work without errors,
every member, unchanged driver/target/frozen environment provenance, required
freshness evidence, and no transport failure outcomes. Successful phase and
per-request rates must match within 1% (comparison default, not a production SLO),
in addition to the existing achieved-work floor. Sample intervals must cover at
least 98% of each measured phase, with each edge within 30 seconds (two nominal
15-second scrapes), and no samples outside the phase. Short intervals can still
provide descriptive rates. No member failure is averaged away. The tool does
not award the 80% acceptance criterion or replace outstanding live drills.

Optional `-compare-estate-images review.json` supplies a strict, bounded review:

```json
{"schema_version":"astronomer-estate-image-review-v1","baseline_inventory":"before-images.json","candidate_inventory":"after-images.json","allowed_changed_components":["agent"],"baseline_consumer_attribution":"legacy_callbacks_other","candidate_consumer_attribution":"tagged_callbacks_v1"}
```

Each inventory is a JSON object with `schema_version` equal to
`astronomer-image-inventory-v1` and `images` mapping 1–64 DNS component names to
`sha256:` digests. Files are at most 64 KiB; their exact byte hashes must match
report `images_sha256` declarations. Component sets must match; changed images
must be explicitly allowed. This is user-declared review, not verified live
provenance. Missing inventories remain unverified. Attribution declarations may
be `unknown`, `legacy_callbacks_other`, or `tagged_callbacks_v1`.

Historical shared callbacks were tagged `other`, while candidate callbacks use
`shared_observation`. The same instrumentation implementation does not prove the
same callback tagging. Unknown or mismatched attribution blocks eligibility and
consumer-category percentages; total-family percentages remain descriptive only.
The total includes `other`, preventing relabeling alone from looking like savings.
Reports expose hashes and fixed diagnostics; arbitrary input report text is not
copied into Markdown or errors. Inventory provenance does not authenticate the
report producer, rule out hidden restarts, or prove uninstrumented-client costs.
