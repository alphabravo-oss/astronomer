# CRD Finalizer Recovery

Use this runbook when a management CR is stuck in `Terminating` and the normal controller retry loop is not making progress.

Manual finalizer removal is a last-resort recovery step. Prefer restoring the controller, fixing RBAC, or deleting/reconciling child resources first. Removing finalizers early can orphan Argo CD ApplicationSets, leave adopted clusters registered, or hide a failed decommission.

## Scope

Current finalizers:

- `management.astronomer.io/decommission` on `Cluster`
- `management.astronomer.io/cleanup` on `Project`
- `management.astronomer.io/clusterbaseline-cleanup` on `ClusterBaseline`
- `management.astronomer.io/componentbundle-cleanup` on `ComponentBundle`
- `management.astronomer.io/agentprofile-cleanup` on `AgentProfile`
- `management.astronomer.io/gitopstarget-cleanup` on `GitOpsTarget`

`ClusterBaseline` and `GitOpsTarget` finalizers delete generated Argo CD ApplicationSets before releasing the CR. `Cluster` deletion delegates to the normal decommission flow.

## Triage

Set the namespace once:

```sh
NS=astronomer-mgmt
ARGO_NS=argocd
```

Check the stuck object:

```sh
kubectl get clusterbaseline -n "$NS"
kubectl get gitopstarget -n "$NS"
kubectl get cluster -n "$NS"
kubectl describe <kind> -n "$NS" <name>
```

If cleanup or decommission has been blocked for more than 15 minutes, the
controller should keep the finalizer and surface the timeout in status:

```sh
kubectl get <kind> -n "$NS" <name> \
  -o jsonpath='{.status.phase}{"\n"}{range .status.conditions[*]}{.type}{" "}{.status}{" "}{.reason}{"\n"}{end}'
```

Expected timeout signal:

- `status.phase` is `DeletingTimedOut`.
- `Ready` and `Reconciled` are `False` with reason `FinalizerTimeout`.
- The relevant finalizer is still present.

`DeletingTimedOut` is an escalation signal, not a cleanup bypass. Continue with
the recovery steps below and remove finalizers only as a last resort.

Check the controller pod:

```sh
kubectl get pods -n astronomer-system -l app.kubernetes.io/name=astronomer
kubectl logs -n astronomer-system deploy/astronomer-server --tail=200
```

For Argo-owning CRDs, list generated children:

```sh
kubectl get applicationsets -n "$ARGO_NS" \
  -l app.kubernetes.io/managed-by=astronomer,astronomer.io/crd-kind=ClusterBaseline

kubectl get applicationsets -n "$ARGO_NS" \
  -l app.kubernetes.io/managed-by=astronomer,astronomer.io/crd-kind=GitOpsTarget
```

Narrow to one CR:

```sh
kubectl get applicationsets -n "$ARGO_NS" \
  -l app.kubernetes.io/managed-by=astronomer,astronomer.io/crd-kind=<Kind>,astronomer.io/crd-namespace=<namespace>,astronomer.io/crd-name=<name>
```

Use DNS-label-normalized values for `astronomer.io/crd-namespace` and `astronomer.io/crd-name`.

## Preferred Recovery

1. Restore the server/controller deployment if it is down.
2. Fix controller RBAC if logs show `forbidden` on ApplicationSets, Applications, ConfigMaps, CRDs, or management resources.
3. Delete generated ApplicationSets manually only when the CR owner labels and source annotations match the stuck CR.
4. Re-check the CR. The controller should remove the finalizer on its next reconcile.

Delete generated ApplicationSets for one stuck CR:

```sh
kubectl delete applicationsets -n "$ARGO_NS" \
  -l app.kubernetes.io/managed-by=astronomer,astronomer.io/crd-kind=<Kind>,astronomer.io/crd-namespace=<namespace>,astronomer.io/crd-name=<name>
```

Confirm no generated children remain:

```sh
kubectl get applicationsets -n "$ARGO_NS" \
  -l app.kubernetes.io/managed-by=astronomer,astronomer.io/crd-kind=<Kind>,astronomer.io/crd-namespace=<namespace>,astronomer.io/crd-name=<name>
```

## Last-Resort Finalizer Removal

Only remove finalizers after child resources are gone or after you have explicitly accepted the orphaning risk.

Patch one object:

```sh
kubectl patch <kind> -n "$NS" <name> --type=merge -p '{"metadata":{"finalizers":[]}}'
```

For `Cluster`, also verify the decommission state through the API or database before patching. Removing the CR finalizer does not prove the adopted cluster was decommissioned.

## Validation

After recovery:

```sh
kubectl get <kind> -n "$NS" <name>
kubectl get applicationsets -n "$ARGO_NS" -l app.kubernetes.io/managed-by=astronomer
```

Expected result:

- The stuck CR is deleted.
- No generated ApplicationSet remains for that CR.
- The server logs stop repeating finalizer cleanup errors.
- For `Cluster`, the product row is decommissioned or intentionally preserved according to the recovery decision.
