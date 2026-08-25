# API-server allow-list drift or provider authorization failure

## Symptoms

- `AstronomerApiserverAllowlistDriftProlonged` fires after effective provider
  CIDRs differ from desired state for 30 minutes.
- `AstronomerApiserverAllowlistProviderAuthorizationFailures` fires after at
  least three provider HTTP 401/403 responses in 15 minutes.
- The cluster network-access page reports `drifting` or `failed`, with the
  detected provider and last successful enforcement timestamp.

## Triage

1. Identify the cluster/provider labels on the alert and inspect the cluster's
   API-server allow-list status and recent snapshots. Do not paste credentials
   or raw provider responses into an incident channel.
2. Confirm whether the row is `monitor` or `enforce`. Drift is informational in
   monitor mode until an operator adopts the desired state; in enforce mode it
   indicates reconciliation is blocked or still converging.
3. For authorization failures, verify the selected cloud credential is active,
   unexpired, assigned to this cluster, and has the documented least-privilege
   permission to read/update API-server network restrictions.
4. Check worker logs for the cluster ID and provider operation. Provider error
   bodies are bounded and sanitized, but still handle them as operational data.
5. Compare desired and effective CIDRs. Confirm Astronomer egress and emergency
   access ranges before changing either side; removing the only reachable CIDR
   can lock operators and tunnels out of the cluster.

## Recovery

- Rotate or repair the cloud credential, preserving the same target reference,
  then run an on-demand reconcile.
- If an external system owns the provider allow-list, either restore the
  Astronomer desired set or explicitly switch to monitor mode; do not leave two
  controllers fighting the field.
- If a new desired generation superseded the failing task, do not replay the
  stale task. Trigger reconciliation from the current API row so generation/CAS
  protection remains effective.
- For an unsupported self-managed provider, keep monitor mode and follow the
  provider-specific integration contract rather than bypassing the capability
  check.

## Verify

- `astronomer_apiserver_allowlist_drift{cluster="..."}` returns to `0`.
- The latest snapshot has identical canonical desired/effective CIDR sets.
- A fresh reconcile records `synced` (or `applied` followed by `synced`).
- No authorization-failure counter increase occurs for at least 15 minutes.
- Operator and tunnel access remain healthy from the intended networks.
