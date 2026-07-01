# Passthrough proxy namespace-scoped RBAC — Design (2026-07-01)

Extends `namespace_scoped_rbac_enabled` from the typed resource-browser handlers to the generic `/api/v1/clusters/{cluster_id}/k8s/*` passthrough proxy.

## Grounded facts
- **Namespaced passthrough requests already work** for scoped users: `requireK8sProxyPermission` (`internal/server/routes.go` ~L1195) extracts the path namespace via `parseK8sProxyObjectRef` and calls `engine.CheckPermission` with the SAME `GetUserBindings` that the earlier change enriched with project→namespace synthetic bindings. `/k8s/api/v1/namespaces/team-a/pods` for a project-scoped user (flag on) is already authorized; `/k8s/api/v1/namespaces/other-ns/pods` already 403s.
- **Cluster-wide lists/watches/mutations** by scoped users currently 403 (fail closed): `parseK8sProxyObjectRef` yields `namespace==""`, the synthetic namespace bindings are skipped (`bindingApplies` L153), and absent a cluster-wide grant `CheckPermission` denies.
- **Unary responses are fully buffered**: agent `io.ReadAll` → 256KiB frames → server `reassembleK8sResponse` (`internal/tunnel/internal_k8s_assemble.go`) into ONE `bytes.Buffer` → single `protocol.K8sResponsePayload.Body` (base64) → single `writeK8sResponse` `w.Write` (`internal/tunnel/proxy.go` ~L398-428). No streaming on the unary path (cross-pod `forwardToOwnerPod` filters on the OWNER pod which runs its own buffered `HandleK8sProxy`). Body is standard `{"kind":"...List","items":[...]}`.
- **Watches** take the separate `consumeStreamingResponse` path — newline-delimited event stream, 16KiB frames NOT aligned to event boundaries, long-lived, no server timeout. Filtering = a stateful cross-frame re-framer. OUT OF SCOPE (stays 403).

## Design (flag-gated `namespace_scoped_rbac_enabled`, default off, byte-identical when off)

### Part A — authz gate (routes.go `requireK8sProxyPermission`)
After the coarse `CheckPermission` AND native-allow both deny, add one more branch BEFORE the 403 — the "allow-through-and-filter" gate. Grant the request (and stash the authorized-namespace allow-set in the request context) IFF ALL hold:
- flag on (`deps.NamespaceScopedRBAC`),
- `r.Method == GET`,
- `verb == rbac.VerbList` (from the existing `k8sProxyPermission(r)` — i.e. a list, not a named GET, not a mutation),
- NOT a watch (`!isK8sProxyWatchRequest(r)` and `ref["watch"] != "true"`),
- `namespace == ""` (cluster-wide path — namespaced paths are already handled above by CheckPermission),
- `engine.HasAnyNamespaceAccess(bindings, resource, verb, clusterID)` is true (the user has this (resource, list) in at least one namespace on this cluster).

Otherwise → 403 as today. Watches, mutations, named GETs, and users with no namespace access all keep failing closed. Stash via a new `tunnel.WithNamespaceFilter(ctx, allowSet)` (allow-set from `engine.AuthorizedNamespaces`) so the proxy package can read it.

### Part B — response filter (tunnel `HandleK8sProxy` unary path)
After `reassembleK8sResponse` returns and BEFORE `writeK8sResponse`, if the context carries a namespace allow-set AND the response is a 2xx:
- base64-decode `resp.Body`, `json.Unmarshal` to a generic object.
- If `kind` ends with `"List"` and has an `items` array: keep each item whose namespace-key is in the allow-set — `metadata.namespace` for namespaced resources, OR `metadata.name` when `kind == "NamespaceList"` (so `kubectl get ns` returns only the caller's namespaces, matching the typed ListNamespaces). Items with no namespace-key and not a Namespace (e.g. NodeList, PV) are DROPPED (fail closed → cluster-scoped resources yield an empty list for scoped users — safe). Re-marshal, re-base64, recompute `Content-Length` (it's in the response header allowlist).
- If it is NOT a recognizable `*List` (e.g. a Table from `?as=Table`, or a single object) on a filtered request → FAIL CLOSED: return an empty list of the same kind, or 403. Never pass an unfilterable 2xx body through when a filter was requested.
- Non-2xx (apiserver error) → pass through unchanged.

Only the OWNER pod buffers, so filtering there covers the cross-pod path; verify the allow-set survives (re-derived by the middleware on the owner pod, or forwarded) and FAIL CLOSED if absent.

## Verification (adversarial — this is the authz hot path)
Skeptics attempt: (i) LEAK — a scoped user receives an item from a namespace not in their allow-set (via a List shape the filter misses, Table output, a nested/embedded object, or a non-List 2xx passed through); (ii) BYPASS — a crafted path/verb/`?watch`/`?as=Table` that reaches unfiltered data or defeats the gate; (iii) REGRESSION/LOCKOUT — flag off changes anything, or a cluster-wide reader/superuser loses access, or a legit namespaced request breaks, or the cross-pod path leaks.
