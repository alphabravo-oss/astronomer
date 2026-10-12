# Trace exporter health and SDK diagnostics

Frozen `33b47ec8` passed all 32 observability race parents/cases, vet and one rendered alert parent with three cases. The source tree stayed unchanged. `receipt.json` binds the exact source files, source-tree hash, commands and retained raw log hashes.

The prior `ebf5e27b` tracing initializer was overlaid onto the same test source. A real background SDK batch received HTTP 400 and printed the synthetic collector-controlled body through the default SDK stderr handler; the safe-event regression failed as expected. The retained baseline contains only a synthetic marker and loopback endpoint. The corrected initializer emits a fixed `otel.sdk.error` event, while returned exporter errors retain their original identity and export counters distinguish success from failure.

The configured gauge stays enabled with sampling zero, including a sampled remote parent, without inventing successful exports. Empty endpoint configuration disables it. The rendered failure alert matches this release's server/worker metrics Services and namespace. Counters describe completed export calls; accepted protocol responses do not establish durable trace delivery. The 2048-span SDK queue, 512-span batch, nonblocking saturation and bounded shutdown remain documented diagnostic limits.

Collector runtime readback, full workflow propagation and representative overhead measurement remain separate open W10 gates.
