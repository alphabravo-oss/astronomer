# Kubernetes Proxy Inventory

Date: 2026-06-14

This inventory tracks every current Astronomer path that forwards Kubernetes,
Argo CD, tool UI, shell, log, or tunnel traffic across a trust boundary. Keep
it in sync with `docs/security-sensitive-routes.json`, `internal/server/routes.go`,
and the proxy tests listed below.

## Summary

| Surface | Entry route or caller | Primary owner | Upstream target | Auth boundary | Audit posture | Current status |
|---------|-----------------------|---------------|-----------------|---------------|---------------|----------------|
| Generic Kubernetes API proxy | `/api/v1/clusters/{cluster_id}/k8s/*` | `internal/tunnel/proxy.go` | Adopted cluster API via agent | Session/bearer + cluster RBAC + API-token write scope for mutations; Secret reads require `secrets:read/list/watch` | Mutating requests emit `cluster.k8s_proxy.forwarded`; Secret reads emit `cluster.secret.read` | Covered |
| Generic Secret resource list | `/api/v1/clusters/{cluster_id}/resources/generic/secrets/` | `internal/handler/resources.go` | Adopted cluster API via agent | Session/bearer + `secrets:list` | Authorized Secret list requests emit `cluster.secret.read` | Covered |
| Cross-cluster Secret search | `/api/v1/resources/search/?type=secrets` | `internal/handler/resources_search.go` | Adopted cluster APIs via agent fan-out | Session/bearer + per-cluster `secrets:list` | Authorized Secret searches emit `cluster.secret.read` with filter and cluster count details | Covered |
| Argo CD Kubernetes API proxy | `/api/v1/internal/argocd/clusters/{cluster_id}/k8s/*` on authenticated internal `:8090`; compatibility route on `:8000` | `internal/server/routes.go`, `internal/tunnel/proxy.go` | Adopted cluster API via agent | Cluster-scoped Argo proxy token on both listeners; hash, purpose, cluster binding, expiry, and revocation checked; `:8090` also restricted by NetworkPolicy | Mutating requests emit `argocd.k8s_proxy.forwarded`; downstream actions also visible through Argo/Application history | Covered |
| Service proxy | `/api/v1/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/`, `/api/v1/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*` | `internal/handler/service_proxy.go`, `internal/agent/service_proxy.go` | Allowlisted in-cluster Services | Session/bearer + cluster RBAC + API-token write scope for mutations | Mutating requests emit `cluster.service_proxy.forwarded` | Covered |
| Argo CD UI/API reverse proxy | `/argocd`, `/argocd/*` | `internal/handler/argocd_ui_proxy.go` | Built-in Argo CD server | Astronomer auth before proxy; upstream Argo token injected server-side | Document opens and mutating API calls audited | Covered |
| Exec WebSocket relay | `/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/` | `internal/tunnel/exec_consumer.go`, `internal/agent/exec.go` | Pod exec SPDY stream | One-use stream ticket or session/bearer auth; higher-level shell path requires cluster update / pod exec permission | Direct route emits `pod.exec.opened` without stream payload; kubectl shell path records session/commands | Covered |
| Log WebSocket relay | `/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/` | `internal/tunnel/logs_consumer.go`, agent log stream | Pod log stream | One-use stream ticket or session/bearer auth | Direct route emits `pod.logs.opened` without log payload | Covered |
| Kubectl shell API + WebSocket | `/api/v1/clusters/{id}/shell/*`, `/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/` | `internal/handler/kubectl_shell.go` | Ephemeral shell pod through exec relay | Session/bearer + cluster update permission | `kubectl.session.opened`, `kubectl.session.closed`, command history without stdout/stderr | Covered |
| Remotedialer Kubernetes client | `/api/v1/connect/{cluster_id}/`, `/api/v1/clusters/{id}/v2/pods/`, internal `remoteproxy.K8sClient` | `internal/tunnel2`, `internal/handler/remoteproxy` | Adopted cluster API through remotedialer | Agent token authorizer; callers must still enforce user auth/RBAC before use | Demo route disabled in production; no general user-facing mutation route | Covered |
| Internal Helm over tunnel | `/internal/tunnel/helm/{cluster_id}` | `internal/tunnel/internal_helm.go` | Agent Helm executor | Internal PSK + connected agent | Operation history on calling handlers | Covered |
| Internal K8s cross-pod proxy | `/internal/tunnel/k8s/{cluster_id}` | `internal/tunnel/internal_k8s.go` | Owner server pod / connected agent | Internal PSK, used only between server pods | Shared caller route audit applies before forwarding | Covered |

## Control Requirements

- Public user-facing proxy routes must require Astronomer authentication.
- Mutating public proxy requests must require server-side RBAC and API-token write scope when authenticated by API token.
- Browser-cookie mutating proxy routes must satisfy CSRF middleware.
- Proxy code must strip caller-supplied `Authorization`, `Cookie`, `Proxy-*`, `Host`, `X-Forwarded-*`, hop-by-hop, and Kubernetes `Impersonate-*` headers unless a future controlled impersonation feature explicitly owns that behavior.
- Service proxy targets must be allowlisted by enabled tool/subservice metadata and must reject sensitive control-plane namespaces by default.
- Cross-pod and machine-to-machine proxy routes must use internal credentials that are never accepted by general user auth.
- Stream routes must prefer one-use stream tickets over long-lived query tokens.

## Test Coverage

| Requirement | Representative tests |
|-------------|----------------------|
| Generic Kubernetes proxy auth/RBAC/scope/audit | `TestK8sProxyRequiresAuth`, `TestK8sProxyMutationsRequireClusterUpdate`, `TestK8sProxyMutationsAreAudited`, `TestK8sProxySecretReadsRequireSecretsRBACAndAudit`, `TestK8sProxyPodExecRequiresPodExecPermission`, `TestK8sProxyExecuteUpstreamStripsClientAuthHeaders` |
| Generic and cross-cluster Secret reads | `TestGenericSecretResourcesRequireSecretsListAndAudit`, `TestResourcesSearchSecretsRequireSecretsListAndAudit` |
| Kubernetes proxy response/header sanitization and streaming | `TestHandleK8sProxyStreamingWatchSanitizesResponseHeaders`, `TestForwardToOwnerPodSanitizesResponseHeaders`, `TestWriteK8sResponseSanitizesUnsafeHeaders` |
| Argo CD internal cluster proxy fail-closed behavior, token status/scope, rate limit, and mutation audit | `TestInternalArgoCDProxyRouterFailsClosedWithoutTokenQuerier`, `TestInternalArgoCDProxyRouterRejectsInvalidTokenForms`, `TestInternalArgoCDProxyRouterRejectsInactiveOrMismatchedRows`, `TestInternalArgoCDProxyRouterAcceptsValidClusterToken`, `TestInternalArgoCDProxyRouterRetainsRateLimit`, `TestArgoCDInternalK8sProxyRequiresClusterScopedToken`, `TestArgoCDInternalK8sProxyMutationsAreAudited` |
| Service proxy allowlist, namespace, mutation, response, and agent header behavior | `TestServiceProxyAllowsEnabledToolService`, `TestServiceProxyRejectsTargetsOutsideAllowlist`, `TestServiceProxyBlocksSensitiveNamespace`, `TestServiceProxyMutationsRequireClusterUpdate`, `TestServiceProxyAuditsMutatingRequests`, `TestServiceProxySanitizesResponseHeaders`, `TestServiceProxyStripsClientAuthHeaders` |
| Argo CD UI proxy path, response, cookie, credential, and audit behavior | `TestArgoCDUIProxy_ForwardsPathUnchanged`, `TestArgoCDUIProxySanitizesResponseHeadersAndCookies`, `TestArgoCDUIProxyAuditsDocumentAndMutatingRequests` |
| Exec/log stream ticket behavior, audit, and translation | `TestRegistrationEventsRejectQueryJWT` covers the shared no-query-JWT stance for SSE events; `TestExecAndLogsRejectQueryJWT` covers the direct WebSocket routes; `TestDirectExecAndLogsStreamsAuditOpen` covers accepted stream audit; `TestTranslateLogLine` covers log translation |
| Kubectl shell session ownership and bridge behavior | `TestKubectlHandler_WSEndpointBridgesToExecProxy`, shell session create/close/history tests in `internal/handler/kubectl_shell_test.go` |
| Internal K8s/Helm routes | `TestInternalK8sHandler_DisabledWhenPSKEmpty`, `TestInternalK8sHandler_ForbidsBadPSK`, `TestInternalHelmHandler_DisabledWhenPSKEmpty`, `TestInternalHelmHandler_ForbidsBadPSK`, `TestInternalHelmHandler_RoundTrip` |
| Remotedialer bridge | `TestEndToEnd_RemotedialerListPods`, `TestRemotedialerPodDemoRouteDisabledInProduction` |
| Registry and inventory coverage | `TestHighRiskRoutesDenyUnauthenticatedRequests`, `TestMutatingRoutesHaveSecurityClassification`, `TestBrowserCookieMutatingRoutesRequireCSRF`, `TestForwardingRoutesAreDocumentedInProxyInventory` |

## Remaining Backlog

- Expand route inventory metadata to include handler owner and proxy classification for every forwarding route.
- Keep `docs/security-sensitive-routes.json` synchronized when changing route auth, RBAC, CSRF, or audit behavior.
