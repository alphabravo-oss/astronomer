# Stale downstream delivery status

Alert: `AstronomerDeliveryStatusStale`

This alert fires when normalized downstream controller status for a connected
cluster is older than `delivery.monitoring.staleClusterStatusSeconds` (180
seconds by default) for five minutes. Desired state may still be reconciling in
the cluster, but operators no longer have timely evidence of its outcome.

## Immediate checks

1. Identify the affected cluster, deployment, last observed generation, last
   status sequence, last transition time, and last agent heartbeat. Determine
   whether only status is stale or the entire agent connection is down.
2. Inspect agent status-queue depth, retry counts, coalescing counters, rejected
   snapshots, payload-size failures, and tunnel reconnects. Coalescing may
   replace intermediate observations but must preserve the newest generation.
3. In the managed cluster, verify the pinned source, customization, and Helm
   controllers are Ready and that their watched resources are not suspended.
   Check controller events and resource conditions without exporting Secret
   contents.
4. Check management server/worker readiness, Redis, PostgreSQL, status consumer
   errors, clock skew, and recent schema or protocol changes.
5. Compare the cluster Kubernetes, agent protocol, and Flux versions with the
   release compatibility manifest and controller inventory reported by the
   agent.

## Remediation

- Restore agent tunnel, DNS, and TLS connectivity, then let the agent resend its
  newest complete snapshot. Do not fabricate status or advance a receipt
  sequence manually.
- If the status queue is saturated, pause new large rollouts, restore worker and
  queue capacity, and verify that bounded coalescing retains the latest state.
- If a downstream controller is unhealthy, remediate its signed pinned
  distribution or the failing source/resource. Do not install an unverified
  controller image or change the management chart to host controllers.
- If a payload violates size or schema limits, reduce resource/event volume or
  upgrade through a compatible protocol release; do not disable validation.

## Resolution and escalation

Resolve after status age remains below threshold for at least 15 minutes, the
reported generation matches the latest accepted assignment, and a canary
transition becomes visible within the configured SLO. Escalate as a potential
integrity incident if status sequences regress, a cluster reports another
tenant's deployment, signature or identity checks fail, or management and
cluster observations disagree after reconnection. Capture redacted agent and
controller logs, cluster/deployment IDs, generations, sequences, release
versions, and metric snapshots.
