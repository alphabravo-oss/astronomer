# CRD finalizer recovery

Use this runbook when a `Cluster`, `Project`, or `AgentProfile` in
`management.astronomer.io/v1alpha1` remains `Terminating` after its normal
controller retry window. Manual finalizer removal is a last resort: it can
orphan a durable project record or bypass an adopted cluster's audited
decommission workflow.

## Symptoms

- `metadata.deletionTimestamp` is set but the resource remains present.
- the generation-current `Ready` condition is false or absent;
- server logs show ownership, persistence, or decommission errors.

The active finalizers are:

- `management.astronomer.io/decommission` on `Cluster`;
- `management.astronomer.io/cleanup` on `Project`;
- `management.astronomer.io/agentprofile-cleanup` on `AgentProfile`.

## Triage

Set the exact namespace and object; never run a broad finalizer rewrite:

```bash
NS=astronomer-mgmt
KIND=cluster
NAME=payments-production

kubectl -n "$NS" get "$KIND" "$NAME" -o json |
  jq '{deletionTimestamp: .metadata.deletionTimestamp,
       finalizers: .metadata.finalizers,
       generation: .metadata.generation,
       status: .status}'
kubectl -n astronomer logs deployment/astronomer-server --tail=300
```

For a `Cluster`, inspect its decommission operation and agent connection in the
dashboard or API. A disconnected agent, unavailable Kubernetes API, or pending
credential revocation can legitimately keep deletion in progress. For a
`Project`, resolve cluster membership and ownership conflicts before retrying.
For an `AgentProfile`, confirm the embedded controller is running and can patch
the watched namespace.

Check controller permissions without printing Secrets:

```bash
kubectl auth can-i get clusters.management.astronomer.io \
  --as system:serviceaccount:astronomer:astronomer
kubectl auth can-i patch clusters.management.astronomer.io/status \
  --as system:serviceaccount:astronomer:astronomer
```

## Recovery

1. Restore the server/controller deployment and database connectivity.
2. Correct a same-namespace `AgentProfile` reference, project ownership
   conflict, or controller RBAC failure.
3. For a cluster, let the audited decommission operation finish or retry it
   through the supported API. Do not delete operation rows or agent tokens.
4. Re-read the exact object and confirm the controller has made progress.

Only when the owning product record has already been handled, the controller
cannot be restored in time, and the incident commander accepts the orphan risk,
remove the one known finalizer from the one recorded object:

```bash
kubectl -n "$NS" patch "$KIND" "$NAME" --type=json \
  -p='[{"op":"remove","path":"/metadata/finalizers"}]'
```

Record the full object identity, finalizer, reason, approver, and durable object
state in the incident. Never patch every resource or every namespace.

## Verify

- the exact resource is gone;
- the server/controller is healthy and reconciling new generation changes;
- a deleted cluster is disconnected, credentials are revoked, and its durable
  decommission operation is terminal;
- a deleted project has no unintended cluster membership or retained policy;
- no unrelated resource lost a finalizer.
