# Owned CloudNativePG pool failover qualification

This driver writes only a newly named schema in a dedicated `plan032_w12_*`
database. It uses `internal/db.ConnectWithConfig`, keeps the same pool open,
and writes two synthetic tenants with atomic ledger/audit transactions and
deliberate idempotency replays. It does not mutate Kubernetes.

Build the driver from a reviewed immutable commit. Supply a regular private
0600 JSON reference containing `database_url`; do not print that field. The
URL must point to the stable read/write service endpoint of the owned CNPG
cluster. A direct primary-pod endpoint would defeat the reconnect test.

```bash
go build -trimpath -o /private/qualification/cnpg-failover ./scripts/cnpg-failover
/private/qualification/cnpg-failover \
  --database-ref /private/qualification/database.json \
  --cluster-uid <reviewed-owned-cluster-uid> \
  --source-commit <full-reviewed-source-commit> \
  --artifact-dir /private/qualification/new-run
```

Before any fault, the operator must verify the cluster UID, namespace,
three ready instances, synchronous replication, stable service routing and
the identity of its current primary. Wait for `ready.json` (20 acknowledged
writes). Then the separately authorized operator may delete only that owned
primary pod, retaining its PVC and Cluster identity. Record the actual fault
time and original pod UID atomically as `fault.json` in the run directory:

```json
{
  "clusterUID": "<same-owned-cluster-uid>",
  "podUID": "<original-primary-pod-uid>",
  "fault": "owned-primary-pod-deletion",
  "at": "<actual-RFC3339-fault-time>"
}
```

The marker is evidence, not permission. This program never deletes a pod or
pauses a node. It fails if the fault is unbound, arrives before readiness,
does not produce a different primary identity, loses an acknowledged row,
duplicates a ledger/audit effect, or fails to commit 20 writes on the new
primary within its three-minute deadline. Its schema remains available for
an independently owned backup/restore comparison. Cleanup must verify the
database, schema name and schema ownership comment from the receipt.

The receipt reports observed successful-write gaps and fault-to-new-primary
commit time in nanoseconds, sample counts and exact fault identity. These are
one synthetic workload sample, not a whole application RTO, production SLA,
PITR, physical restore or encryption-key recovery certification. Application
business handlers and historical key usability require their separate tests.
