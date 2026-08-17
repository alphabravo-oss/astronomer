# Kubernetes proxy inventory

Date: 2026-08-17

This inventory tracks active Astronomer paths that forward Kubernetes, service,
shell, log, or tunnel traffic across a trust boundary. Keep it synchronized
with `docs/security-sensitive-routes.json`, server route registration, and the
representative tests below.

## Active surfaces

| Surface | Entry route or caller | Upstream | Authorization and audit |
| --- | --- | --- | --- |
| Kubernetes API proxy | `/api/v1/clusters/{cluster_id}/k8s/*` | Adopted API through its agent | Authenticated caller, cluster-scoped RBAC, API-token write scope for mutations; Secret reads require dedicated verbs; mutations and Secret reads are audited. |
| Generic Secret list | `/api/v1/clusters/{cluster_id}/resources/generic/secrets/` | Adopted API through its agent | `secrets:list`; authorized reads emit value-free `cluster.secret.read`. |
| Cross-cluster Secret search | `/api/v1/resources/search/?type=secrets` | Authorized adopted APIs through agents | Per-cluster `secrets:list`; audit records filters/counts, never Secret data. |
| Service proxy | `/api/v1/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*` | Allowlisted in-cluster Service | Cluster RBAC, API-token write scope for mutations, target allowlist and protected-namespace checks; mutations are audited. |
| Exec relay | `/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/` | Pod exec through agent | One-use stream ticket or authenticated caller plus `pods:exec`; open is audited without stream content. |
| Log relay | `/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/` | Pod logs through agent | One-use stream ticket or authenticated caller plus log permission; open is audited without log content. |
| Kubectl shell | `/api/v1/clusters/{id}/shell/*` and `/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/` | Ephemeral shell pod through agent | Cluster update/shell permission, owner-bound session, TTL; lifecycle and input commands are audited, stdout/stderr is not stored. |
| Remotedialer client | `/api/v1/connect/{cluster_id}/`, internal client, and production-disabled probe `/api/v1/clusters/{id}/v2/pods` | Adopted API | Agent-token connection authorization; every caller must independently enforce user auth/RBAC. Demo routes remain disabled in production. |
| Internal Helm relay | `/internal/tunnel/helm/{cluster_id}` | Agent Helm executor | Private listener/NetworkPolicy plus internal PSK; calling operation owns user auth and audit. |
| Internal Kubernetes relay | `/internal/tunnel/k8s/{cluster_id}` | Owner server pod or connected agent | Private listener/NetworkPolicy plus internal PSK and owner routing; calling route owns user auth and audit. |

Flux-native delivery does not use a central Kubernetes reverse proxy. The
cluster agent receives a cluster-bound, generation-fenced assignment and
materializes a fixed object graph locally. See the delivery threat model and
control-plane runbook for that separate boundary.

## Control requirements

- Public proxy routes require Astronomer authentication and project/cluster
  authorization before any downstream request.
- Mutations authenticated by API token require write scope. Cookie-authenticated
  mutations require CSRF protection.
- Callers cannot supply upstream `Authorization`, `Cookie`, `Proxy-*`, `Host`,
  `X-Forwarded-*`, hop-by-hop, or Kubernetes `Impersonate-*` headers.
- Service targets must be enabled/allowlisted and protected management or
  control-plane namespaces remain denied by default.
- Machine routes use credentials that general user middleware never accepts and
  remain absent from public ingress.
- Browser streams use one-use tickets, never query-string JWT/API tokens.
- Audit/log/support surfaces never capture Secret values, kubeconfigs, stream
  payloads, or reusable credentials.

## Representative coverage

- Generic proxy auth, RBAC, API-token scope, Secret-read audit, pod-exec
  permission, header stripping, response sanitation, and watch streaming.
- Generic and cross-cluster Secret list authorization and value-free audit.
- Service proxy allowlist, protected namespace, mutation scope/audit, and
  request/response header sanitation.
- Exec/log one-use ticket rejection/replay and open-event audit.
- Shell ownership, bridge, expiry, cleanup, and command-history tests.
- Internal Helm/Kubernetes fail-closed PSK and round-trip tests.
- Remotedialer cluster identity and production demo-route denial.
- High-risk route registry, authentication, CSRF, audit, and inventory coverage.
