# Vault integration (migration 067)

Astronomer can resolve `${vault://...}` references in helm install /
tool preset / cluster-template values blobs against one or more
HashiCorp Vault servers. The secret value is fetched at install time,
substituted in memory, and shipped to the target cluster. The original
blob (with the markers still in it) is what we persist — secrets never
sit at rest in our Postgres or audit log.

## Reference syntax

Two forms:

```
${vault://<connection>/<engine>/<path>#<key>}
${vault:<engine>/<path>#<key>}
```

* The first form names an explicit Vault connection by its
  `vault_connections.name`.
* The second uses the project's default connection (configured at
  `/dashboard/projects/{id}/settings`). When no project context is
  available (e.g. catalog installs at cluster scope), only the
  explicit form is allowed.

Defaults:

* `<engine>` defaults to the connection's `default_mount` (typically
  `secret` or `kv`).
* `<key>` is mandatory — it selects which field inside the secret
  Astronomer substitutes.
* KV v2 detection is automatic: Astronomer tries
  `<engine>/data/<path>` first and falls back to `<engine>/<path>` for
  KV v1. The operator doesn't need to declare engine version.

Example values blob:

```yaml
postgres:
  username: app
  password: ${vault://prod-vault/secret/postgres#password}
api:
  token: ${vault:kv/api-keys/billing#token}
```

## Auth methods

Configure under **Settings → Vault connections** (superuser-only).

### token

Static token. Easiest for dev; not recommended for production.

```json
{ "token": "hvs.AAAAAAAA..." }
```

### approle

Two-piece credential — `role_id` (semi-public) plus `secret_id`
(rotated). Best for static workloads that can hold a long-lived
secret_id.

```json
{ "role_id": "...", "secret_id": "..." }
```

Vault-side setup:

```bash
vault auth enable approle
vault write auth/approle/role/astronomer \
    token_policies=astronomer-read \
    secret_id_ttl=24h \
    secret_id_num_uses=0
vault read auth/approle/role/astronomer/role-id
vault write -f auth/approle/role/astronomer/secret-id
```

Paste both values into the connection form.

### kubernetes

In-cluster Service Account JWT exchanged for a Vault token. The
recommended production pattern: nothing static, no rotation burden.

```json
{ "role": "astronomer", "jwt_path": "/var/run/secrets/kubernetes.io/serviceaccount/token" }
```

Vault-side setup:

```bash
vault auth enable kubernetes
vault write auth/kubernetes/config \
    kubernetes_host="https://kubernetes.default.svc:443"
vault write auth/kubernetes/role/astronomer \
    bound_service_account_names=astronomer-server \
    bound_service_account_namespaces=astronomer \
    policies=astronomer-read \
    ttl=1h
```

The astronomer server pod's Service Account must match
`bound_service_account_names` / `bound_service_account_namespaces`.
This is set in the chart values — see `astronomer-go/chart/values.yaml`
`server.serviceAccount.name`.

## Policy

A minimal Vault policy granting read access to the paths Astronomer
will pull from:

```hcl
path "secret/data/astronomer/*" {
  capabilities = ["read"]
}
```

## Token lifecycle

The resolver caches the Vault client token per-connection in process
memory. On a 403 (token expired / revoked) the cache entry is dropped
and authentication retried once. On a 5xx from Vault the install path
fails fast — the operator's job is to fix Vault, not for astronomer to
retry indefinitely.

## What is and isn't persisted

| Stored where                                | Contents                              |
|---------------------------------------------|----------------------------------------|
| `helm_installations.values_override`        | Original blob with `${vault://...}` markers |
| `cluster_template_applications.spec_snapshot` | Same — markers preserved |
| Audit log (`audit_logs`)                    | Reference path only (never the value) |
| `vault_connections.auth_encrypted`          | Fernet-encrypted auth blob |
| Cluster manifest (in-cluster)               | Resolved cleartext (k8s RBAC + at-rest encryption gates this) |

The fact that the resolved value never lives in our DB is the
foundational property: rotating a Vault secret takes effect on the
next install/upgrade with no operator action on the astronomer side.

## Metrics

* `astronomer_vault_resolves_total{connection,outcome}` — counter,
  every reference resolution.
* `astronomer_vault_resolve_duration_seconds{connection}` — histogram,
  fetch latency for successful resolutions.
* `astronomer_vault_connection_health{connection}` — gauge, 1 when the
  most recent /health/ or /test/ probe succeeded.

## Audit events

* `admin.vault_connection.created` / `.updated` / `.deleted`
* `admin.vault_connection.tested` / `.healthchecked`
* `vault.reference.resolved` / `.failed` (emitted via the Observer hook
  on the resolver — see `internal/observability/vault_metrics.go`).

None of these contain the secret VALUE. The reference PATH is recorded
so a missing key shows up clearly in the audit log.
