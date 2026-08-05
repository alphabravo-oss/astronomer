# Charlie Integration Degraded

Charlie is optional and separately deployed. A Charlie alert must never cause
Astronomer core readiness to fail or justify a direct Astronomer-to-Charlie
Central connection.

1. Open **Settings → Charlie → Diagnostics** and identify whether activation,
   local mTLS, Product Bridge, enrollment, leader, central, or OCI is degraded.
2. For suspected unsafe behavior or uncertainty, use **Emergency Disable**.
   This control remains available when the feature is off. The local latch
   closes write admission, cancels cooperative executors, drains the
   cross-replica write fence, and stops new sessions, triggers, bridge traffic,
   and MCP actions before any remote confirmation. If the API reports a pending
   drain, do not treat disable as complete; the latch remains closed while the
   non-cooperative executor is investigated.
3. Do not clear emergency disable until the product agent independently reports
   `disabled`. Requested/verified mode drift always uses the less permissive
   value.
4. For trigger dead letters, inspect the durable trigger and task-outbox rows;
   do not rely on Redis/asynq alone. Retry through the admin API using the
   immutable dead source event ID and a fresh UUID request ID. The API retains
   the source/fingerprint and creates one correlated retry attempt; never invent
   an unrelated event or edit/revive the dead row to bypass idempotency.
5. Never print onboarding packages, mounted Secrets, credentials, prompts,
   evidence, action arguments, or upstream error bodies while troubleshooting.

The complete procedures, safety contract, stop conditions, rotation, rollback,
and disconnect guidance are in
[Charlie Integration Operations and Runbooks](../charlie-operations.md).
