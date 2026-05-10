# Astronomer Helm Chart

## High Availability Defaults

The base `values.yaml` is now tuned for a minimal HA posture for the control
plane:

- `server.replicaCount=2`
- `worker.replicaCount=2`
- `frontend.replicaCount=2` when the frontend is enabled
- PodDisruptionBudgets, anti-affinity, and topology spread are enabled for all
  three components

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
