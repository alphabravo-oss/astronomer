# Downstream identity and authorization boundary

Date: 2026-08-17

Astronomer agents authenticate to a managed cluster with their own Kubernetes
ServiceAccount. Browser sessions, Astronomer API tokens, internal credentials,
cookies, and caller-provided impersonation headers never become downstream
Kubernetes credentials. Product authorization happens centrally; target-cluster
RBAC is the independent capability ceiling.

This is deliberate service-account delegation, not Kubernetes user
impersonation. The target API server normally sees the agent ServiceAccount,
while Astronomer's audit log retains the human or service identity, authorized
project/cluster, action, request ID, and result.

## Request path

```text
browser / API client
        |
        | Astronomer authentication, token scope, project/cluster RBAC, audit
        v
management server owning the cluster's fenced agent session
        |
        | typed tunnel request; caller credentials and impersonation stripped
        v
cluster agent
        |
        | in-cluster ServiceAccount credential
        v
target Kubernetes API
```

When the connected agent session is owned by another server replica, the
original replica forwards through the private internal listener. That hop uses
the internal PSK, cluster owner lookup, loop prevention, NetworkPolicy, and the
same cluster-bound tunnel session. It does not weaken or replace the original
user authorization decision.

## Required controls

- Normalize one cluster and project scope before RBAC. Conflicting path, query,
  header, or body scopes fail before persistence or downstream I/O.
- Classify Kubernetes methods and subresources conservatively. Unknown writes
  need write scope; `exec`, `attach`, and `portforward` need the dedicated
  high-risk permission.
- Remove caller `Authorization`, cookies, forwarding headers, hop-by-hop
  headers, and every `Impersonate-*` header before the agent constructs the
  Kubernetes request.
- Sanitize response headers and keep watch/log/exec streaming bounded by
  cancellation, idle, and hard deadlines.
- Bind every tunnel request and response to the expected cluster and live
  session fence. A stale or mismatched session is an error, never a reroute.
- Emit value-free audit for material reads and mutations before returning a
  successful result. Secret reads use dedicated `secrets:*` permissions and
  audit actions.
- Keep the internal listener out of public ingress. A missing/invalid internal
  credential fails closed even if NetworkPolicy is misconfigured.
- Render the narrowest agent privilege profile that supports the intended
  workflows. `viewer` is the default; `admin` is explicit break-glass risk.

## Flux-native delivery is a separate path

Delivery does not replay a human Kubernetes request and does not give a central
controller a remote kubeconfig. PostgreSQL owns delivery intent, frozen
placement, rollout policy, approval, generation, audit, and normalized status.
The authenticated agent receives only its cluster's accepted snapshot.

The agent validates each typed assignment and materializes a deterministic,
closed object graph:

- one reserved project control namespace;
- optional provider/trust Secrets containing only the assignment projection;
- an applier ServiceAccount and bounded Role/RoleBinding, or the one fixed
  platform-applier ClusterRoleBinding for reviewed platform scope;
- one typed Flux source and one `Kustomization` or `HelmRelease`.

Cross-namespace references, remote Kustomize bases, arbitrary service accounts,
user-supplied RBAC, controller objects, and unknown fields are rejected. Flux
runs locally under the signed release-pinned distribution. The agent observes
conditions/inventory, sends bounded cluster/session/generation-bound status,
and prunes only objects recorded in the accepted checkpoint after replacement
apply succeeds.

The human or automation identity remains central and cannot be inferred from a
Flux object. Conversely, a Flux ServiceAccount cannot call Astronomer's user
API or another cluster.

## Failure and compromise assumptions

- A management-server compromise is high impact because it can authorize work
  within each connected agent's live RBAC ceiling. Separate identities,
  least-privilege profiles, project RBAC, audit export, network boundaries, and
  short-lived/fenced sessions reduce but do not erase that risk.
- An agent compromise is bounded to its cluster ServiceAccount and explicitly
  permitted egress. It receives no browser token, management database
  credential, Charlie central credential, or another cluster's assignment.
- A Flux controller compromise is bounded by its hardened local RBAC and
  per-assignment applier identities. It has no placement or approval authority.
- A model/Charlie output grants no permission. Every read/write is revalidated
  by current product feature, mode, RBAC, scope, approval, safety, budget,
  idempotency, and fencing state.
- During disconnection, the agent and Flux retain the last accepted generation.
  Missing central state never authorizes new work or checkpoint-free pruning.

## Review checklist

- Does the new route authenticate and normalize project/cluster scope before
  any tunnel/provider I/O?
- Are token scope, RBAC, CSRF, audit, rate/concurrency, and high-risk
  classifications explicit and tested?
- Can any caller credential, userinfo-bearing URL, Secret value, kubeconfig, or
  impersonation header cross the boundary?
- Is every request bound to one cluster and current session/generation?
- Does an agent/Flux RBAC change widen the target-cluster ceiling? If so, is it
  reflected in the privilege profile, delivery scope, threat model, and tests?
- Does failure retain the last accepted desired state and refuse stale replay,
  takeover, ambiguous pruning, and cross-project observations?

See [agent privilege profiles](../agent-privilege-profiles.md),
[Kubernetes proxy inventory](../kubernetes-proxy-inventory.md),
[Flux-native delivery ADR](../architecture/decisions/flux-native-delivery.md),
and [delivery control-plane runbook](../runbooks/delivery-control-plane.md).
