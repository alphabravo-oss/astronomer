# Astronomer Helm Chart

## Dev vs. Production Posture

The base `values.yaml` is tuned for **first-touch development** — it boots a
working management plane on a single laptop/k3d cluster with bundled Postgres
+ Redis StatefulSets, TLS disabled, and a known-dev Fernet key. **Do not run
this profile in production.** Three value files ship with the chart:

| File | Use case | Notable settings |
|------|----------|------------------|
| `values.yaml`            | dev / first install / CI | bundled Postgres + Redis, TLS off, replicas=2, debug=true |
| `values-k3d.yaml`        | k3d laptop testing | replicas=1, scheduling helpers off, dev Fernet key |
| `values-production.yaml` | real installs | bundled DBs **off**, TLS required, replicas=3, env=production, debug=false |

For a production install, layer the production override on top of the base:

```bash
helm upgrade --install astronomer ./deploy/chart \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml \
  --set image.server.tag=<git-sha> \
  --set image.worker.tag=<git-sha> \
  --set image.agent.tag=<git-sha> \
  --set image.migrate.tag=<git-sha> \
  --set frontend.image.tag=<git-sha> \
  --set config.serverURL=https://astronomer.example.com \
  --set config.corsAllowedOrigins=https://astronomer.example.com \
  --set 'gateway.hosts={astronomer.example.com}' \
  --set gateway.tls.secretName=astronomer-tls \
  --set postgres.external.dsnSecretRef.name=astronomer-postgres-dsn \
  --set postgres.external.dsnSecretRef.key=dsn \
  --set redis.external.address=redis.astronomer.svc.cluster.local:6379 \
  --set-file secrets.encryptionKey=./prod-fernet-key \
  --set-file secrets.secretKey=./prod-jwt-key \
  --set-file bootstrap.password=./prod-bootstrap-password
```

`values-production.yaml` will **fail to render** sensibly if any of the
following are still unset — they're the bare minimum a production install
needs:

- `postgres.external.dsnSecretRef.name` or `postgres.external.dsn`
- `redis.external.address` (and `passwordSecretRef.name` if your Redis is
  password-gated)
- `gateway.hosts` (at least one hostname)
- `gateway.tls.secretName` **or** `gateway.tls.certManager.{enabled,issuerName}`
- `secrets.encryptionKey` — must be a real Fernet key; generate with
  `python -c "from cryptography.fernet import Fernet; print(Fernet.generate_key().decode())"`
- `secrets.secretKey` — JWT signing material
- `config.serverURL` — external URL; seeds the Argo self-management hostname

### Management-plane disaster recovery

`values-production.yaml` enables `managementBackup` by default — a nightly
`pg_dump --format=custom` of Astronomer's own Postgres, pushed to S3 with
daily / weekly / monthly retention tiers. The operator still has to supply
the bucket and an AWS credentials Secret:

```bash
--set managementBackup.s3.bucket=my-astronomer-backups \
--set managementBackup.s3.region=us-east-1 \
--set managementBackup.s3.credentialsSecretRef.name=astronomer-backup-aws
```

The matching restore procedure — including the Secrets that must survive a
restore for SSO and agent decryption to keep working — lives in
[`docs/management-plane-dr-runbook.md`](../../docs/management-plane-dr-runbook.md).

This is for the **management plane's own DB only**. Velero-driven backups of
managed workload clusters are unrelated and live in the `Backup` /
`BackupRestore` CRs handled by the server (`internal/handler/backups.go`).

## High Availability Defaults

The base `values.yaml` is tuned for a minimal HA posture out of the box:

- `server.replicaCount=2`
- `worker.replicaCount=2`
- `frontend.replicaCount=2` when the frontend is enabled
- PodDisruptionBudgets, anti-affinity, and topology spread are enabled for all
  three components

`values-production.yaml` lifts these to `replicaCount=3` with PDB
`minAvailable=2` so a single node loss never drops capacity below quorum.

The local `values-k3d.yaml` override intentionally scales these back down to
single replicas and disables the frontend HA scheduling helpers to keep smoke
tests lightweight.

## External Postgres

Disable the bundled Postgres StatefulSet and provide either a literal DSN or a
secret-backed DSN. The chart no longer supports the old `postgres.enabled`
alias; use `postgres.bundled.enabled`.

```yaml
postgres:
  bundled:
    enabled: false
  external:
    dsn: postgres://astronomer:secret@my-rds.cluster-xyz.us-east-1.rds.amazonaws.com:5432/astronomer?sslmode=require
```

Secret-backed DSN:

```yaml
postgres:
  bundled:
    enabled: false
  external:
    dsnSecretRef:
      name: astronomer-external-postgres
      key: DATABASE_URL
```

Example secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-external-postgres
type: Opaque
stringData:
  DATABASE_URL: postgres://astronomer:secret@my-rds.cluster-xyz.us-east-1.rds.amazonaws.com:5432/astronomer?sslmode=require
```

## External Redis

Disable the bundled Redis StatefulSet and point the chart at an external Redis
address. The chart no longer supports the old `redis.enabled` alias. When the
password is stored in a secret, the chart expands it into `REDIS_URL` at
container runtime.

```yaml
redis:
  bundled:
    enabled: false
  external:
    address: redis.example.internal:6379
    tls: true
    database: 0
    passwordSecretRef:
      name: astronomer-external-redis
      key: password
```

If Redis does not require a password, omit `passwordSecretRef`.

## Upgrade safety

When `postgres.bundled.enabled=false`, the chart's pre-upgrade hook checks for
the legacy bundled Postgres PVC (`data-<release>-astronomer-postgres-0`). If it
still exists, Helm fails the upgrade and points to:

- `docs/migrations/2026-05-postgres-cnpg.md`

## Gateway controller prerequisites

This chart renders Gateway API resources, but it does not install a Gateway
controller. That controller is treated as cluster infrastructure and should be
bootstrapped separately.

Before install or upgrade, the preflight hook validates:

- the `gateways.gateway.networking.k8s.io` CRD exists
- the `httproutes.gateway.networking.k8s.io` CRD exists
- `gateway.className` resolves to an existing `GatewayClass`

If any of those checks fail, Helm stops with a clear error instead of creating
an unusable release. The intended model is:

- Astronomer chart owns `Gateway` and `HTTPRoute`
- cluster bootstrap owns the Gateway controller and `GatewayClass`

## Audit retention

The worker enforces `audit_log` monthly partition retention through the
`AUDIT_LOG_RETENTION_MONTHS` environment variable. When unset, the worker keeps
`13` months.

Example:

```yaml
worker:
  env:
    AUDIT_LOG_RETENTION_MONTHS: "13"
```

## Prometheus Operator

The chart always renders dedicated metrics Services for the server and worker
on port `9090`. To have Prometheus Operator discover them automatically, enable
the optional `ServiceMonitor` resources:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      release: kube-prometheus-stack
```

## cert-manager TLS automation

The chart already supports manual TLS by pointing the Gateway listener at an
existing Secret via `gateway.tls.secretName`. To let cert-manager provision and
renew that Secret automatically, annotate the Gateway and keep the same Secret
name in `certificateRefs`:

```yaml
gateway:
  enabled: true
  hosts:
    - astronomer.example.com
  tls:
    enabled: true
    secretName: astronomer-gateway-tls
    certManager:
      enabled: true
      issuerKind: ClusterIssuer
      issuerName: letsencrypt-prod
```

This emits `cert-manager.io/cluster-issuer: letsencrypt-prod` on the Gateway.
For namespaced issuers, set `issuerKind: Issuer`. For external issuer types,
set `issuerKind` and `issuerGroup` explicitly.
