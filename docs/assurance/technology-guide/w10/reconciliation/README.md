# Plan 030 reconciliation and remaining signal qualification

The source baseline is Plan 032 W00 commit `6c3da1882c76662f1b7e01e80f89f5e2978bc7b4`. The reviewed W10 backend input is `469536d3be9fb8220f1d39c638c8850aea1b6ac3`. Plan 030's original planning baseline is `f87b189fa5ea94ad5e86392f90f810f6b6dbd07c`; its integrated history includes `be14822227f38b181ef52f5e2ac69f245897df39`. Historical deployment observations in Plan 030 retain their original source and date. They do not establish the identity or health of today's deployment.

[source-inventory.json](source-inventory.json) binds 21 source anchors at both revisions, captures literal metric constructor names in the named first-party scopes, and retains every target, field configuration and options object from all 62 dashboard panels. The literal-name extraction is a source inventory, not an exhaustive runtime metric-family denominator. All 62 panel records are unchanged between W00 and this W10 input. Only the alert template and metrics documentation differ among the 21 anchors: W10 adds exporter-failure semantics and its runbook. Collector modernization is a separate candidate; it is not represented as a qualified runtime in this inventory.

| Plan 030 work | Existing implementation retained | Remaining acceptance |
| --- | --- | --- |
| E01 application discovery | Server and worker ServiceMonitors, actual namespace/fullname selectors, explicit missing/all-down scrape alerts and opt-outs | Evaluate attributable series in the new owned collector stack; source/render tests alone do not prove scraping |
| E01 management dashboard | API latency/errors, queues/outboxes, DB waits, agent connectivity/freshness, process CPU/heap/goroutines and observation panels; missing scrape data remains unknown | Actual query readback and stored panel/query evidence for the claimed stack |
| E05 shared observations | Typed observation source, watch-health tracking, immutable projections, operation-count attribution and bounded enum measurements | Matched representative demand and memory measurements; no inherited efficiency percentage is awarded |
| E06 cached assignment state | Delivery uses the existing shared observation/assignment paths rather than introducing another telemetry inventory loop | Real asynchronous observation must correlate by operation, deployment, generation and desired digest; it is not one perpetual synchronous span |
| E09 exporter ownership | Existing application/agent telemetry paths and baseline ownership remain intact | Alloy qualification must exclude other Compose projects and must not discover or ingest arbitrary host containers |
| E04/E11 representative performance | Existing loadtest profiles, attribution and comparison controls remain available | Matched workload, uncertainty, failure drills, scale/soak and an explicitly reviewed overhead budget remain open |

## Propagation contract

The HTTP router creates an OTel server span and renames it to the resolved Chi route template. Request IDs belong in protected logs and trace correlation, never metric labels. A durable mutation uses its caller's transaction to persist domain changes, audit intent and task intent. `PrepareTaskOutbox` injects `_correlation_id`, `_traceparent` and optional `_tracestate`, then validates the final durable JSON bytes. Baggage is deliberately excluded.

The task dispatcher preserves the stored payload. The real Asynq consumer extracts its remote parent in `instrumentTask` and starts an `asynq.job` child span. PostgreSQL calls share that context through the pool's composed tracer; query text, bind values and connection credentials are omitted. The caller transaction and audit invariant remain authoritative when tracing is disabled or unavailable.

The server tunnel serializes W3C routing metadata into the Kubernetes request envelope; the agent extracts it before handling the request. Flux observation occurs later and can have an independent trace lifetime. Its durable operation/deployment identity, generation, specification digest and observation timestamp provide correlation. A successful export, healthy agent connection or accepted mutation does not establish successful Flux reconciliation.

These are source contracts. Existing real callback, HTTP wire and worker tests qualify their stated slices. The remaining complete API → persisted task → actual queue consumer → database → collector readback must have its own immutable source and real runtime receipt. The browser fixture's previous tracing-disabled runs are not retroactively described as this evidence.

## Health and cardinality boundaries

| Signal | What it establishes | What it does not establish |
| --- | --- | --- |
| Process readiness | Its configured startup/dependency checks succeeded | Successful reconciliation, fresh observed state or available trace storage |
| Dependency readiness | The checked PostgreSQL/queue endpoint is usable at the measurement time | Continued task ownership or restored historical keys |
| Queue ownership | The actual leader/lease owner is active | An empty queue or all durable work completed |
| Observed-state timestamp | Age of a particular observation | Current downstream readiness when the observation is stale |
| Flux condition plus generation/digest | The matching desired state was observed | Every unrelated deployment, or immediate durability of remote effects |
| Trace export failure | A completed SDK export returned an error | Process unavailability, queue loss, or every SDK queue drop |
| Scan result timestamp | Age of that scan result | Freshness of the vulnerability database used for it |

Metric budgets must use actual finite route, worker-task, queue-state, operation and result sets. Trace IDs, user IDs, raw URLs, SQL bind values and durable operation IDs are excluded from ordinary metric labels. The existing tunnel gauges are rebuilt from the registered non-decommissioned estate. Their connection-name and ID denominator is that estate, not the request count. Existing reconnect/message counters, registration-phase series and Vault connection-name families require a separate lifetime-retention audit: their current code has no complete deletion path. Consequently, the blanket statement that all retained cluster labels are bounded by the current registered count is not a demonstrated invariant yet. This gap remains owned by W10; it is not hidden by the new exporter metrics' finite `success|failure` result set.

Existing CNPG/backup, worker/outbox, delivery-generation and catalog-trust alerts retain their runbooks. W10's exporter alert is additive and alerts on actual failed calls. Refresh failure ratios and vulnerability-database freshness require genuine producers before any alert can be claimed; reuse-detection counters and scan-result age are different signals. No whole-stack, production deployment, human browser, sustained performance or universal secret-redaction certification is claimed by this reconciliation.
