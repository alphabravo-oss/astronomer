# Support-bundle continuation baseline

The frozen `df5598e2` baseline executed three parent tests and five leaf cases.
All five cases failed for the expected behavior, with no skips or compilation
errors. Actual typed Kubernetes clients contacted the fixture HTTP server; the
assertions inspected ZIP JSON output and request continuation.

Events, NetworkPolicies, and Ingresses returned only the first 500 items while
the fixture supplied a second page. A referenced TLS Secret on the second page
was reported absent. When the second Secret page would return an expired
continuation, the collector still emitted the first-page certificate projection
instead of an incomplete section. The source remained clean and unchanged.

[Receipt](receipt.json) binds the source, exact command, maintained capacity
checks, raw event log, and runner. The selected runtime was Go 1.27.2 with four
runtime workers and two compiler workers. Every maintained capacity check
passed. This packet characterizes the defect before a production change; it
does not qualify a fix or complete W07.
