# 2026-05 Postgres HA Migration Runbook

This runbook covers the safe path away from the legacy bundled single-instance
Postgres StatefulSet used by older `astronomer-go` installs.

## When this applies

Use this runbook when:

- your existing release created the legacy PVC `data-<release>-astronomer-postgres-0`
- you want to upgrade to a chart configuration that disables bundled Postgres
- you are moving to an external managed Postgres service now, with a future HA
  backend such as CloudNativePG possible later

The chart's pre-upgrade hook blocks this upgrade path when the legacy PVC is
still present.

## Option A: Externalize to managed Postgres

1. Create a fresh external Postgres database.
2. Export the old bundled database:

```bash
kubectl exec -n <namespace> statefulset/<release>-astronomer-postgres -- \
  pg_dump -U astronomer -d astronomer > astronomer.sql
```

3. Restore into the external database:

```bash
psql "<external DATABASE_URL>" < astronomer.sql
```

4. Create a secret with the external DSN:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-external-postgres
type: Opaque
stringData:
  DATABASE_URL: postgres://astronomer:<password>@<host>:5432/astronomer?sslmode=require
```

5. Upgrade the chart with bundled Postgres disabled:

```bash
helm upgrade <release> deploy/chart \
  -n <namespace> \
  --set postgres.bundled.enabled=false \
  --set postgres.external.dsnSecretRef.name=astronomer-external-postgres \
  --set postgres.external.dsnSecretRef.key=DATABASE_URL
```

6. After the upgrade is healthy, remove the old StatefulSet and PVC manually.

## Option B: Fresh install plus restore

1. Export the old database with `pg_dump`.
2. Install a fresh release pointed at the new Postgres backend.
3. Restore the dump into the new database before bringing traffic over.

## Validation

- `kubectl get pods -n <namespace>` shows healthy server/worker pods
- `kubectl logs -n <namespace> job/<release>-astronomer-preflight` shows a pass
- the application can load `/health/` and `/metrics`
- a sample login and read/write action succeed against the restored data
