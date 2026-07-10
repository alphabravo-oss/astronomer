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
  --set tls.source=letsEncrypt \
  --set tls.letsEncrypt.email=ops@example.com \
  --set postgres.external.dsnSecretRef.name=astronomer-postgres-dsn \
  --set postgres.external.dsnSecretRef.key=dsn \
  --set redis.external.address=redis.astronomer.svc.cluster.local:6379 \
  --set 'networkPolicy.externalPostgresEgressCIDRs={10.20.0.0/16}' \
  --set 'networkPolicy.externalRedisEgressCIDRs={10.30.0.0/16}' \
  --set 'networkPolicy.kubernetesAPIEgressCIDRs={10.43.0.1/32,10.40.0.0/16}' \
  --set bootstrap.email=admin@example.com \
  --set managementBackup.s3.bucket=my-astronomer-backups \
  --set managementBackup.s3.credentialsSecretRef.name=astronomer-backup-aws \
  --set managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap \
  --set-file secrets.encryptionKey=./prod-fernet-key \
  --set-file secrets.secretKey=./prod-jwt-key \
  --set-file bootstrap.password=./prod-bootstrap-password
```

> **GitOps note:** pin `bootstrap.password` (or `bootstrap.existingSecret`) and
> the key-wrap Secret name on every render. Empty bootstrap password re-rolls
> under `helm template` / Argo CD; empty wrap name fails production preflight
> while backups are enabled. See [Bootstrap credentials](#bootstrap-credentials).

`values.schema.json` validates common types before Helm renders templates, and
`values-production.yaml` will fail if any of the following are still unset —
they're the bare minimum a production install needs:

- `postgres.external.dsnSecretRef.name` or `postgres.external.dsn`
- `redis.external.address` (and `passwordSecretRef.name` if your Redis is
  password-gated)
- `networkPolicy.externalPostgresEgressCIDRs` and
  `networkPolicy.externalRedisEgressCIDRs` when NetworkPolicy is enabled
- `networkPolicy.kubernetesAPIEgressCIDRs`, covering both the
  `kubernetes.default` Service ClusterIP and API endpoint/node networks
- `gateway.hosts` (at least one hostname)
- `tls.source` — must be `selfSigned`, `letsEncrypt`, or `secret` (not `none`)
- `tls.letsEncrypt.email` — required when `tls.source=letsEncrypt`
- `secrets.encryptionKey` — must be a real Fernet key; generate with
  `python -c "from cryptography.fernet import Fernet; print(Fernet.generate_key().decode())"`
- `secrets.secretKey` — JWT signing material
- `config.serverURL` — external URL; seeds the Argo self-management hostname
- `bootstrap.email` — the bootstrap admin login email
- the retained Dex runtime Secret is created metadata-only by the chart and is
  populated from Fernet-encrypted DB state by `POST /api/v1/auth/dex/apply/`
- `managementBackup.s3.bucket` and `managementBackup.s3.credentialsSecretRef.name`
  when the production backup CronJob is enabled
- `managementBackup.encryptionKeyBackup.wrappingSecretRef.name` when backups are
  enabled (or set `encryptionKeyBackup.enabled=false` to explicitly opt out —
  restored Fernet data will be undecryptable on a new cluster)
- `bootstrap.password` or `bootstrap.existingSecret` (GitOps pin; see below)

### Known Helm 3.21 value-coalescing warning

With exactly Helm v3.21.0 and bundled `argo-cd` 9.5.21, lint and template may
emit `destination for argo-cd.server.env is a table`. Helm's nested-dependency
evaluation temporarily compares Astronomer's map-shaped `server.env` with
Argo CD's list-shaped value while inspecting `redis-ha`; the rendered
Deployments remain correctly isolated. `TestServerEnvironmentValuesRemainIsolatedFromArgoCD`
enforces that contract for default, custom, combined, and production values.

Do not filter this warning. Chart maintainers must review this note and the
contract test on the next Helm or Argo CD dependency change, or by 2026-10-09,
whichever occurs first.

### Bootstrap credentials

The bootstrap admin logs in with `bootstrap.email`. If `bootstrap.password` is
set, the server uses that initial password. If it is empty, the chart generates
a random password once, stores it in the `<release>-bootstrap` Secret, and keeps
that Secret across upgrades **when Helm can `lookup` the live Secret**.

#### GitOps / `helm template` pin (required in production)

Under pure GitOps (Argo CD, Flux) or any offline `helm template` path, Helm's
`lookup` returns nil, so an empty `bootstrap.password` re-rolls `randAlphaNum`
on **every** sync and silently rotates the admin password. Production
preflight therefore **refuses** the render unless you pin one of:

- `bootstrap.password` — pass via `--set-file` / sealed-secret / external
  secret operator (do **not** commit the value to git), or
- `bootstrap.existingSecret` — name of a pre-created Secret with a `password`
  key (chart does not manage the Secret; server wires
  `ASTRONOMER_BOOTSTRAP_PASSWORD` to it).

`values-production.yaml` leaves both empty on purpose so a bare
`-f values-production.yaml` without pins fails closed.

Retrieve a chart-generated (dev) password with:

```bash
kubectl -n <namespace> get secret <release>-bootstrap \
  -o jsonpath='{.data.password}' | base64 -d
```

The bootstrap admin is not forced through a first-login password reset. Rotate
the password later through the profile or admin password-change flow.

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

### Quorum-safe drain math (T29)

With `replicaCount=3` + `minAvailable=2`:

- **Voluntary disruption** (cordon + drain, rolling update, cluster
  autoscaler scale-down): Kubernetes will evict at most **1** pod at a
  time per component. The PDB blocks any eviction that would leave fewer
  than 2 pods Ready, so `kubectl drain` on the third node waits for
  reschedule + readiness before completing. Operators get a predictable
  serial drain instead of a stampede.
- **Involuntary disruption** (node power-loss, kernel panic, hardware
  failure): PDBs don't apply, but with 3 replicas spread across nodes
  via the chart's anti-affinity rules, losing one node still leaves 2
  Ready pods — same `minAvailable` floor by construction.
- **What this guarantees end-to-end**: every API request, worker task,
  and frontend page-load has at least 2 live pods accepting traffic
  during any controlled maintenance window. Combined with the agent
  tunnel's auto-reconnect (jittered exponential backoff, T10) and the
  asynq queue's at-least-once delivery, in-flight work survives a single
  pod restart without operator action.

To verify the gate is active in a deployed environment:

```bash
kubectl -n astronomer get pdb
# NAME                     MIN AVAILABLE   ALLOWED DISRUPTIONS
# astronomer-server-pdb    2               1
# astronomer-worker-pdb    2               1
# astronomer-frontend-pdb  2               1
```

`ALLOWED DISRUPTIONS = 1` confirms the cluster currently has the headroom
the PDB requires (3 ready − 2 minAvailable = 1). If it drops to 0, the
next drain on a hosting node will block — that's a signal to investigate
the missing replica before initiating any rolling change.

The local `values-k3d.yaml` override intentionally scales these back down to
single replicas and disables the frontend HA scheduling helpers to keep smoke
tests lightweight.

## External Postgres

Disable the bundled Postgres StatefulSet and provide either a literal DSN or a
secret-backed DSN. The chart no longer supports the old `postgres.enabled`
alias; use `postgres.bundled.enabled`.

Production installs must use an external managed Postgres service or an HA
Postgres operator with automated failover. The bundled StatefulSet is intended
for development, CI, and single-node smoke tests only. Production DSNs must use
TLS with `sslmode=require`, `sslmode=verify-ca`, or `sslmode=verify-full`; the
chart's production preflight checks both inline DSNs and secret-backed DSNs,
and the server also rejects non-TLS external Postgres in production.

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

### Production Postgres Contract

Postgres is Astronomer's durable control-plane store. Kubernetes/etcd is the
reconciliation substrate, not the product database. For production:

| Requirement | Expected posture |
|-------------|------------------|
| Availability | Managed Postgres or HA Postgres with automated failover. |
| Transport security | TLS required; DSN uses `sslmode=require`, `verify-ca`, or `verify-full`. |
| Pool sizing | Tune `postgres.pool` for server + worker replica count and managed-cluster load. Defaults are conservative; 50+ managed clusters should raise `maxConns`. |
| Backup | Enable `managementBackup` for logical S3 dumps and retain at least 30 daily, 12 weekly, and 6 monthly backups. |
| PITR | Use provider WAL/PITR for tighter RPO. Logical dumps are the cold-backup fallback, not a WAL substitute. |
| Restore proof | Enable `managementRestoreDrill` or run the drill manually on a schedule. A backup that is never restored is not considered valid. |
| Runbook | Follow `docs/management-plane-dr-runbook.md`, including the encryption-key preservation step. |

The declarative knobs under `postgres.productionRequirements` in
`values.yaml` document this contract for policy tooling. Enforcement is split
between Helm preflight, server startup validation, backup CronJobs, and the
restore drill.

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

The Job uses a dedicated, least-privilege ServiceAccount and RBAC hooks. Helm
creates those prerequisites at hook weight `-10`, before the Job at weight
`-5`, and retains them after success. This retention is intentional: Helm
considers non-Job hooks complete as soon as they are created, so a
`hook-succeeded` policy would remove the ServiceAccount and RBAC before the Job
could use them. On every subsequent install or upgrade,
`before-hook-creation` replaces the retained resources with the rules from the
new chart before running the Job. The permissions are limited to `get` on the
exact CRDs and GatewayClass enabled by the rendered configuration, the exact
referenced namespace-local Secrets, and the exact legacy PVC name checked in
external-Postgres mode. Rules for disabled checks are not rendered.

When default deny is enabled, a retained preflight NetworkPolicy is created at
the same `-10` hook weight and replaced by stable name on every release. It
selects only the preflight pod labels and permits the flows that pod actually
uses: DNS on TCP/UDP 53, external Postgres on the configured Postgres port when
the database connectivity init container is enabled, and Kubernetes API access
on TCP 443/6443. Development may source the narrow port rules from the legacy
`externalEgressCIDRs` bucket. Production requires dedicated Postgres and API
CIDRs and rejects `0.0.0.0/0` and `::/0` rather than falling back to unrestricted
egress.

NetworkPolicy handling of Kubernetes Service DNAT varies by CNI. Populate
`kubernetesAPIEgressCIDRs` with CIDR coverage for both the `kubernetes.default`
Service ClusterIP and every API endpoint or node network the Service can
translate to. One appropriately scoped CIDR may cover both; two CIDRs can still
be wrong. Helm cannot inspect live addresses to prove semantic coverage. Port
443 covers evaluation before DNAT; port 6443 covers the common post-DNAT API
endpoint. Validate the CIDRs against the production cluster before upgrading.

Kubernetes authorization caches can take a few seconds to observe newly
created hook RBAC. To avoid reporting that propagation window as a missing
prerequisite, every preflight API read is bounded to 10 attempts, one second
apart. Only an explicit Kubernetes `NotFound` response means an object is
absent. Authorization failures, API discovery failures, and transport errors
retain their server diagnostic, are retried, and then fail closed as an API
read failure. These semantics apply to Gateway and cert-manager CRDs,
GatewayClasses, referenced Secrets, and the legacy Postgres PVC. Genuine PVC
absence is the only optional-object case. Secret data, including the Postgres
DSN, is never printed in retry or failure output.

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

## TLS

TLS is configured through a single top-level `tls:` block that drives both the
Gateway listener (`httproute.yaml`) and the optional Ingress (`ingress.yaml`).
There are four modes, selected by `tls.source`:

| `tls.source`   | Behavior                                                                                                                | Auto-rotates | cert-manager |
|----------------|-------------------------------------------------------------------------------------------------------------------------|--------------|--------------|
| `none`         | HTTP only. Local dev / k3d default.                                                                                     | –            | no           |
| `selfSigned`   | Chart renders a cert-manager `Issuer{selfSigned}` + `Certificate` writing to `tls.secretName`. Browsers warn on the CA. | yes          | **required** |
| `letsEncrypt`  | Chart renders a cert-manager ACME `Issuer` (HTTP-01) + `Certificate`. Requires public DNS + Let's Encrypt reachability. | yes          | **required** |
| `secret`       | BYO. Operator pre-creates the named Secret out of band with keys `tls.crt` + `tls.key`. No cert-manager involvement.    | no (manual)  | no           |

### cert-manager is the operator's responsibility

This chart **does not install cert-manager** and does not bundle it as a Helm
dependency, mirroring Rancher's posture. When `tls.source` is `selfSigned` or
`letsEncrypt`, the preflight Job aborts the release with a clear error if the
`issuers.cert-manager.io` CRD isn't present:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
```

If you're installing cert-manager via Argo CD sync waves immediately before this
chart, set `tls.requireCertManager=false` to skip the preflight check and let
the apply order resolve normally.

### Examples

Self-signed (internal-only installs):

```yaml
tls:
  source: selfSigned
```

Let's Encrypt production (publicly reachable hostnames):

```yaml
tls:
  source: letsEncrypt
  letsEncrypt:
    email: ops@example.com
    environment: production   # or "staging" while testing
```

Bring your own certificate Secret:

```yaml
tls:
  source: secret
  secretName: astronomer-tls   # operator pre-creates this Secret
```

### Additional trusted CAs

When the **server pod** needs to make outbound TLS calls to upstreams signed by
a private CA (private container registries, internal SAML / OIDC IdPs), supply
the CA bundle through `tls.additionalTrustedCAs`:

```yaml
tls:
  additionalTrustedCAs:
    enabled: true
    existingSecret: my-internal-ca   # Secret with key tls.crt holding a PEM bundle
```

The Secret is mounted at `/astronomer/trust/extra/ca-additional.pem` and Go's
`SSL_CERT_DIR` is set so `crypto/x509` reads it alongside the system pool. This
is independent of the Gateway/Ingress TLS — it only affects the server's
outbound trust store.
