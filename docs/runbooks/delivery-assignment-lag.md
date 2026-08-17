# Delivery assignment acknowledgement latency

Alert: `AstronomerDeliveryAssignmentAckSlow`

This alert fires when the ten-minute p99 time between assignment publication
and agent acknowledgement exceeds `delivery.monitoring.assignmentAckSecondsP99`
(60 seconds by default). It indicates that connected clusters are not accepting
new desired state within the delivery SLO; it does not by itself prove that a
downstream reconciliation failed.

## Immediate checks

1. Check whether the alert is isolated to one cluster, agent version, region,
   provider, or rollout. Compare the p50, p95, p99, rate, and sample count so a
   low-volume outlier is not mistaken for a fleet-wide incident.
2. Check server and worker readiness, worker queue depth, delivery scheduler
   latency, Redis health, PostgreSQL pool saturation, and recent deploys.
3. Check the affected agents' tunnel connection, last heartbeat, protocol
   version, assignment generation, and acknowledgement sequence. Never paste
   enrollment tokens, source credentials, kubeconfigs, or assignment payloads
   into an incident channel.
4. Verify that the cluster Kubernetes minor, agent protocol, and Flux version
   match the release compatibility manifest.
5. Inspect rollout concurrency and provider rate-limit events. A large rollout
   should remain within the configured global and per-rollout caps.

## Remediation

- For management-plane saturation, stop starting new rollouts, lower rollout
  concurrency, restore database or queue capacity, and allow durable pending
  assignments to drain. Do not delete assignment or receipt rows.
- For a tunnel or agent outage, restore network/DNS/TLS reachability and verify
  that the agent resumes from its last accepted sequence. Rotate credentials
  only when compromise or expiry is established.
- For a compatibility mismatch, pause the affected rollout and upgrade through
  a signed, supported release path. Do not override version admission.
- For a provider rate limit, reduce concurrency and honor the provider's retry
  window. Confirm provider hooks are idempotent before replaying them.

## Resolution and escalation

The alert can be closed when acknowledgement p99 remains below threshold for at
least 20 minutes, the backlog is draining, and a canary assignment advances
from publication to acknowledgement exactly once. Escalate immediately if
acknowledgements regress or skip sequence numbers, assignments cross tenant
boundaries, signatures fail, or durable state appears lost. Preserve the
rollout ID, cluster ID, assignment generation, release version, redacted logs,
and metric snapshots for the incident record.

