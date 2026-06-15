# Security Review & Hardening Plan — Agent‑SA Authorization Model

> **Scope:** the authorization model behind astronomer's Kubernetes management of *managed clusters* —
> the agent ServiceAccount, the k8s‑proxy identity boundary, token scopes, the kubectl/exec paths,
> and the internal cross‑pod door. Backend (`internal/…`) + deploy chart (`deploy/…`).
>
> **Method:** three independent read‑only code audits (deployed RBAC; tunnel identity boundary + proxy
> authz mapping; audit/secret/token‑scope). All findings carry `file:line` evidence.
>
> **Status:** DRAFT v1 — security/platform. This is a remediation **plan**, not applied changes.

---

## 0. The model in one paragraph (threat model)

Every user action against a managed cluster is proxied **Browser → backend k8s‑proxy → agent
WebSocket tunnel → that cluster's kube‑apiserver**. The agent authenticates to the apiserver with its
**own in‑cluster ServiceAccount token**; the end user's JWT / `Authorization` / `Impersonate‑*` headers
are stripped at the tunnel (`internal/agent/k8sproxy.go:404‑417`). Therefore **the cluster never sees
the user** — the cluster‑side ceiling is *the agent SA's RBAC*, and **astronomer's own RBAC layer +
API‑token scopes are the only real per‑user authorization**. This is a deliberate, centralized design
(simpler than per‑user impersonation, with central policy + audit). Its risk is equally clear: **a gap
in astronomer's RBAC/scope layer, or an over‑broad agent SA, is the only thing between a user and the
cluster.** This review measures exactly how wide that gap and that ceiling are today.

**Headline:** in the as‑shipped default, **the ceiling is cluster‑admin** (both the management‑cluster
agent and any managed‑cluster agent registered without an explicit privilege profile), and **several
mutation paths — including pod `exec` (RCE‑equivalent) — are reachable with a read‑scoped API token.**
The scoping machinery to fix most of this already exists; it is opt‑in and **fails open**.

---

## 1. Consolidated findings (severity‑ranked)

| ID | Sev | Finding | Evidence | Real gap vs DiD |
|---|---|---|---|---|
| **C1** | Critical | **Management‑cluster agent SA is unconditional `*/*/*` cluster‑admin**, no values knob to scope it | `deploy/chart/templates/serviceaccount.yaml:33‑37` | Real (by‑design but unbounded) |
| **C2** | Critical | **Default managed‑cluster agent profile fails OPEN to `admin` (cluster‑admin)** when the privilege annotation is absent/empty/unrecognized | `deploy/agent/template.go:71‑73`; `internal/agent/config.go:42`; fallbacks in `argolabels/labels.go:182,186`, `handler/agent_fleet.go:994,998`, `handler/clusters.go:1736` | Real — fail‑open default |
| **H1** | High | **Read‑scoped API tokens can mutate via TYPED routes** — workload scale/restart/delete, pod delete, node cordon/drain/taint/label, resource create/delete carry **no scope middleware** (only RBAC) | `internal/server/routes.go:2148‑2175, 2255‑2266`; `api_token_scope.go:26` | Real |
| **H2** | High | **Pod `exec`/shell reachable with ANY valid token incl. read‑scoped** — WS exec/logs routes have only `rateLimit`; authz is delegated to ticket issuance, but a raw `Authorization: Bearer astro_…` bypasses ticket issuance, and neither path checks token **scope**. Exec = `/bin/sh` in any pod | `routes.go:929‑934`; `streamauth.go:73‑95`; `exec_consumer.go:97,187`; `stream_tickets.go:73‑79` | Real (RCE‑equiv) |
| **H3** | High | **`X‑Remote‑User/Group/Extra` headers are NOT stripped** (denylist omits them) → apiserver front‑proxy identity spoofing **iff** the cluster uses `--requestheader‑*` auth | `internal/tunnel/proxy.go:313‑326`; `internal/agent/k8sproxy.go:404‑417` | Real (conditional) |
| **H4** | High | **Internal PSK door `/internal/tunnel/k8s` & `/helm` is externally reachable** when `frontend.enabled=false` (catch‑all `/`) or dev nginx; PSK‑only, **no per‑user authz, unaudited**, reaches any cluster | `routes.go:916,923`; `deploy/chart/templates/ingress.yaml:62‑74`, `httproute.yaml:71‑77`; `internal/tunnel/internal_k8s.go:86‑168` | Real (topology‑dependent) |
| **H5** | High | **Default break‑glass shell = cluster‑wide read/write on `*/*` for any operator** (superuser = real `cluster‑admin`); SA created in `kube-system` | `internal/handler/kubectl_shell.go:185,193‑195`; `internal/kubectl/manifests.go:44,176‑182,209` | Real |
| **H6** | High | **`operator` agent profile grants RBAC `roles`/`rolebindings` write + secrets write** — a namespace‑level privilege‑escalation primitive | `deploy/agent/template.go:191‑220` | Real |
| **M1** | Med | Header filtering is a **denylist** (opt‑out), **duplicated** in two files → drift risk | `proxy.go:313‑326`; `k8sproxy.go:404‑417` | DiD |
| **M2** | Med | `isHighRiskPodProxySubresource` is brittle; for path shapes it misses, `exec/attach/portforward` **degrade to a generic pod write verb**, bypassing the `pods:exec` gate | `routes.go:1467‑1479` | Real (mapping) |
| **M3** | Med | Non‑resource URLs + **unknown CRDs/apigroups collapse to the generic `clusters` permission** — per‑resource RBAC doesn't extend to custom resources | `routes.go:1228‑1245, 1333‑1338` | Real (mapping) |
| **M4** | Med | remoteproxy **v2 transport is `InsecureSkipVerify`** (gated to non‑prod today, but it's the future direction) | `internal/handler/remoteproxy/proxy.go:74‑99`; gate `routes.go:877‑882` | DiD / future |
| **M5** | Med | Rate limiters are **per‑user‑global, not per‑cluster** — N users can saturate one cluster's single agent tunnel | `internal/middleware/api_rate_limit.go:117,214‑220` | Real (DoS) |
| **M6** | Med | Internal PSK fallback routes are **unaudited** — mutations there produce no user‑attributable audit row | `routes.go:916,923` | DiD |
| **L1** | Low | POST‑to‑subresource + **eviction classified as `update`** not delete | `routes.go:1182‑1192` | DiD |
| **L2** | Low | Internal door forwards caller‑supplied headers (compounds H3 if H4 exposed) | `internal_k8s.go:107‑144` | DiD |
| **L3** | Low | **Shell input recording is best‑effort/lossy** (chan cap 64, drops under load), outputs never captured, alternate frame shapes evade it | `kubectl_shell.go:513‑548,664‑676` | DiD (audit‑evasion) |

**What's already solid (do not regress — see [Appendix A](#appendix-a--confirmed‑solid-baseline)):** credential/impersonation stripping, response‑header filtering, GET/HEAD/OPTIONS‑only read classification, strict per‑cluster RBAC scoping, PSK constant‑time + key‑derived + fail‑closed, `/v2/pods/` hard‑gated out of prod, **user‑attributed** audit, secret‑read auditing behind a dedicated `secrets:read` verb, exec/shell session auditing + ownership checks, namespace‑scoped agent profiles, agent pod hardening, shell reaper/deadlines/random names.

---

## 2. Priority gating

```
P0  Close the actively‑exploitable / fail‑open‑to‑cluster‑admin set   ── GATE 0 ──
P1  Shrink the ceiling + identity boundary + mapping correctness      ── GATE 1 ──
P2  Defense‑in‑depth, DoS, audit completeness                          ── GATE 2 ──
P3  Future‑proofing (v2 transport)                                     ── DONE ──
```

| Gate | Pass criteria |
|---|---|
| **GATE 0** | A read‑scoped token can no longer mutate, exec, or open a shell (H1/H2); a cluster registered with no profile no longer becomes cluster‑admin (C2); the internal PSK door is unreachable from any external ingress in every shipped topology (H4). Each proven by an automated negative test. |
| **GATE 1** | `X‑Remote‑*` stripped + header filter is an allowlist in one shared place (H3/M1); break‑glass shell defaults to least‑privilege (H5); `operator` profile no longer grants RBAC‑write by default (H6); exec subresource always maps to `pods:exec` (M2); management agent scoped as far as feasible + documented (C1). |
| **GATE 2** | CRD/non‑resource RBAC mapping decided & enforced (M3); per‑cluster rate limits (M5); internal door audited (M6); eviction classified correctly (L1); shell recording back‑pressured + frame contract closed (L3). |
| **DONE** | v2 transport verifies TLS before it graduates to prod (M4). |

> **Non‑negotiable:** every task ships with a **negative test** (the attack it closes must fail) and
> must not weaken any item in Appendix A. Add tests under `internal/server/*_test.go` /
> `internal/handler/*_test.go` and the route‑security gate suite already run in CI
> (`go test ./internal/server -run 'Test(...)'`, see `.github/workflows/pr-validation.yaml`).

---

## 3. Workstream A — Make a read‑only token actually read‑only (P0)

**Closes H1, H2.** Today "scope" is enforced only on the generic `/k8s/*` proxy; the typed mutation
routes and the exec/logs/shell WS routes are scope‑blind, so a `["read"]` token + an operator's RBAC
can scale/delete workloads, drain nodes, and **open `/bin/sh` in any pod**.

### A1 — Group‑level write‑scope fallthrough for mutating typed routes (P0)
- **Severity:** H1 · **Depends‑on:** — · **Files:** `internal/server/routes.go` (the `r.Route`/`r.Group`
  that holds workload, node, and resource mutation routes), `internal/middleware/api_token_scope.go`.
- **Reasoning:** Per‑route opt‑in (`APITokenScopeEnforce`) guarantees gaps. Add a **default** rule: any
  non‑GET/HEAD/OPTIONS request carrying an API token must present a write scope, unless a route opts
  into something narrower. Defense in depth on top of RBAC, and it makes the read‑token boundary real.
- **Sketch:**
  ```go
  // middleware: applied at the authenticated API group root
  func RequireWriteScopeForMutations(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      if isMutatingMethod(r.Method) && tokenAuth(r) && !tokenHasScope(r, ScopeWriteClusters) {
        forbidden(w, "api token lacks write scope"); return
      }
      next.ServeHTTP(w, r)
    })
  }
  ```
  Apply to the workload/node/resource mutation subtrees (`routes.go:2148‑2175, 2255‑2266`). Keep RBAC
  as the primary gate; this is the scope backstop.
- **DoD:** a token minted with `["read"]` gets `403` on scale/restart/delete/cordon/drain/taint/create‑resource even when its owner holds the matching RBAC verb.
- **Tests:** table‑driven negative test per route: `read` token → 403; `write:clusters` token + RBAC → 202/200; missing RBAC → 403 regardless of scope.

### A2 — Require write/exec scope on exec, logs, and shell WS + ticket issuance (P0) ⭐
- **Severity:** H2 · **Depends‑on:** — · **Files:** `routes.go:929‑934`, `internal/tunnel/stream_auth.go`,
  `internal/.../streamauth.go:73‑95`, `internal/handler/stream_tickets.go:73‑79`.
- **Reasoning:** Exec is cluster write / RCE. It must not be reachable by a read token, via either entry
  (browser ticket **or** raw bearer). Introduce a dedicated **`clusters:exec`** scope (or reuse
  `write:clusters`) and enforce it in **both** `AuthorizeStreamRequestWithTickets` (raw‑token branch)
  and at ticket issuance. Resolving identity is not enough — add the scope check next to the existing
  RBAC `clusters:update` check.
- **DoD:** `read`‑scoped token → `403/401` on `/ws/exec`, `/ws/logs`, and on POST `…/shell/sessions`
  ticket issuance; only a token carrying the exec/write scope **and** the `clusters:update` RBAC (or
  `pods:exec`) succeeds.
- **Tests:** WS handshake negative tests (read token rejected on exec+logs+shell); raw‑bearer path and
  ticket path both covered; confirm logs (read‑ish) vs exec (write) scope decision is deliberate and
  documented.

---

## 4. Workstream B — Fail the agent SA CLOSED, not open (P0/P1)

**Closes C2; mitigates C1, H6.** The blast radius of *every* other finding is "whatever the agent SA
can do," which today defaults to cluster‑admin.

### B1 — Default privilege profile = `viewer`, and refuse silent `admin` (P0) ⭐
- **Severity:** C2 · **Depends‑on:** — · **Files:** `deploy/agent/template.go:71‑73`,
  `internal/agent/config.go:42`, and the annotation‑read fallbacks
  (`argolabels/labels.go:182,186`, `handler/agent_fleet.go:994,998`, `handler/clusters.go:1736`).
- **Reasoning:** `NormalizePrivilegeProfile("")` → `admin` means *forgetting* the annotation grants
  cluster‑admin. Invert the default to least privilege. Registration should **require an explicit
  profile choice** (UI + API), and an unknown/empty value must resolve to `viewer` (read‑only), never
  `admin`. Upgrading to a broader profile should be a deliberate, audited action.
- **Sketch:** `NormalizePrivilegeProfile` returns `viewer` for empty/unknown; the registration flow
  rejects (or warns‑and‑defaults‑to‑viewer) when no profile is set; the fleet self‑test promotes the
  current "warning" (`agent_fleet.go:606‑607`) to a **failing** posture check.
- **DoD:** a cluster registered without specifying a profile runs a **read‑only** agent; choosing
  `operator`/`admin` is explicit and recorded in the audit log.
- **Tests:** unit on `NormalizePrivilegeProfile` (empty/garbage → `viewer`); an integration/posture test
  asserting the default‑rendered agent manifest contains no `*/*/*` rule.
- **Migration note:** existing clusters already running `admin` are unaffected by the code change —
  ship a one‑time **posture report** (Workstream E item) listing every cluster whose agent is
  cluster‑admin so operators can re‑profile deliberately.

### B2 — Drop RBAC‑write from the `operator` profile by default (P1)
- **Severity:** H6 · **Depends‑on:** B1 · **Files:** `deploy/agent/template.go:191‑220`.
- **Reasoning:** `operator` granting `roles`/`rolebindings` create/update/delete is a self‑escalation
  primitive (mint a RoleBinding → escalate in‑namespace). Most day‑2 ops (scale/restart/delete/exec,
  configmap/secret edits) don't need RBAC‑write. Remove `rbac.authorization.k8s.io` write from
  `operator`; if some workflow truly needs it, gate it behind a separate explicit profile/flag.
- **DoD:** the default `operator` ruleset contains no `roles`/`rolebindings`/`clusterroles` write verbs;
  the curated typed operations still function.
- **Tests:** golden‑file test of the rendered `operator` ruleset; smoke that scale/restart/delete still work under `operator`.

### B3 — Scope the management‑cluster agent + document the irreducible core (P1)
- **Severity:** C1 · **Depends‑on:** — · **Files:** `deploy/chart/templates/serviceaccount.yaml:33‑37`.
- **Reasoning:** The management plane legitimately needs broad rights *on its own cluster* (it installs
  the platform), but `*/*/*` + all nonResourceURLs is broader than necessary and has **no values knob**.
  Replace the wildcard with an explicit, reviewable ClusterRole enumerating the API groups/resources the
  server actually uses, and expose a values override. Where full breadth is genuinely required, document
  *why* per rule so it's auditable rather than a blanket grant.
- **DoD:** the management ClusterRole is an explicit allowlist (no `*/*/*`), overridable via Helm values,
  with a comment per rule‑group justifying it.
- **Tests:** the platform's own e2e (install + register + proxy) passes under the tightened role;
  `helm template` golden test asserts no `["*"]/["*"]/["*"]` rule remains.

---

## 5. Workstream C — Close the internal PSK door from the outside (P0)

**Closes H4; supports M6, L2.**

### C1‑task — Make `/internal/*` unreachable from any external ingress (P0) ⭐
- **Severity:** H4 · **Depends‑on:** — · **Files:** `routes.go:916,923`,
  `deploy/chart/templates/ingress.yaml`, `httproute.yaml`, dev `nginx.conf:61`.
- **Reasoning:** `/internal/tunnel/k8s|helm/{cluster_id}` performs real cluster mutations with **no
  per‑user authz** (PSK only) against *any* cluster. It's only safe because the default ingress doesn't
  route it — but the `frontend.enabled=false` catch‑all `/` and dev nginx **do**. PSK secrecy must not
  be the sole control. Pick at least one structural fix:
  1. **Separate listener/port** for `/internal/*` bound to the pod network only (best), or
  2. register internal routes under a path the ingress/HTTPRoute **never** forwards and add an explicit
     `deny /internal/` to every ingress template + the catch‑all + dev nginx, or
  3. enforce a NetworkPolicy that only sibling server pods can reach the internal port, *and* an
     in‑handler source‑IP/identity check.
- **DoD:** in **all** shipped topologies (`frontend.enabled` true *and* false, dev nginx), an external
  request to `/internal/tunnel/k8s/{id}` cannot reach the handler (proven by a routing test +
  `helm template | grep` assertion that no ingress path forwards `/internal`).
- **Tests:** rendered‑manifest assertions for both ingress and HTTPRoute across both `frontend.enabled`
  values; a handler test that a request without the sibling‑pod source identity is rejected even with a
  valid PSK (defense in depth).

### C2‑task — Audit the internal door (P2)
- **Severity:** M6 · **Depends‑on:** C1‑task · **Reasoning:** even sibling‑pod‑only mutations should
  emit an audit row attributable to the *originating* user (propagate the user UUID through the internal
  payload and log it). **DoD:** every mutation via the internal door produces a `cluster.k8s_proxy.forwarded`
  row with the real user. **Tests:** audit assertion on an internal‑door mutation.

---

## 6. Workstream D — Identity‑boundary hardening (P1)

**Closes H3, M1; supports L2.**

### D1 — Strip `X‑Remote‑*` and convert the header filter to an allowlist, in one shared function (P1)
- **Severity:** H3, M1 · **Depends‑on:** — · **Files:** `internal/tunnel/proxy.go:313‑326`,
  `internal/agent/k8sproxy.go:404‑417` (dedupe into one shared helper).
- **Reasoning:** A denylist forwards any future apiserver‑trusted header by default; it already omits the
  front‑proxy `X‑Remote‑User/Group/Extra` headers, which spoof identity on clusters using
  `--requestheader‑*` auth. Safest is an **allowlist** of headers the proxy *intends* to forward
  (content‑type, accept, content‑length, the SSA/dry‑run/field‑manager query is in the path, etc.),
  dropping everything else — and collapse the two copies into one function so it can't drift.
- **Sketch:**
  ```go
  // shared, allowlist
  var forwardableK8sHeaders = map[string]bool{"content-type": true, "accept": true, "content-length": true, "user-agent": true}
  func filterUpstreamHeaders(h http.Header) http.Header { /* keep only allowlisted; drop the rest */ }
  ```
  If a full allowlist is too risky short‑term, the minimum is adding `x-remote-` to the prefix denylist
  in the shared helper.
- **DoD:** a request carrying `X-Remote-User: system:admin`, `Impersonate-User`, `Authorization`, or a
  novel `X-Whatever` header reaches the apiserver with **none** of them; one function, used by both proxy
  and agent.
- **Tests:** unit on the shared filter (allowlisted kept, everything else dropped, case‑insensitive);
  an end‑to‑end proxy test asserting the upstream request omits `X-Remote-*`.

---

## 7. Workstream E — Break‑glass shell least‑privilege + visibility (P1/P2)

**Closes H5; supports L3; delivers the C2 migration posture report.**

### E1 — Default shell SA to least‑privilege, not cluster‑wide write (P1)
- **Severity:** H5 · **Depends‑on:** — · **Files:** `internal/handler/kubectl_shell.go:185`,
  `internal/kubectl/manifests.go:176‑182`.
- **Reasoning:** A break‑glass shell granting every operator cluster‑wide `create/update/patch` on `*/*`
  (incl. secrets) is far beyond what a debug session needs. Default the per‑session ClusterRole to
  **read‑only** (`get/list/watch`), and require an explicit, audited opt‑in (and the matching astronomer
  RBAC, e.g. `clusters:update`) to obtain a write‑capable shell. Consider namespace‑scoping the session
  (the v1 limitation is acknowledged in `manifests.go:108‑122`). Superuser → `cluster-admin` stays, but
  becomes the *deliberate* high‑privilege path rather than the default.
- **DoD:** a normal operator's shell defaults to read‑only; write/cluster‑admin requires explicit
  elevation that's recorded in the audit log with the granted verbs.
- **Tests:** the rendered per‑session ClusterRole for a non‑elevated operator contains only read verbs;
  elevation path produces the audit row; reaper/cleanup unaffected.

### E2 — Back‑pressure shell command recording + close the frame contract (P2)
- **Severity:** L3 · **Depends‑on:** — · **Files:** `internal/handler/kubectl_shell.go:513‑548,664‑676`.
- **Reasoning:** Recording silently drops keystrokes when the channel (cap 64) is full, and only
  recognizes `type:"stdin"|"input"` frames — both let an attacker defeat command logging. For an
  audited break‑glass feature, either **block/slow** the session when the recorder can't keep up, or
  treat recording as advisory and say so in compliance docs. Confirm the browser↔agent input‑frame
  contract is closed so no executed keystroke is unrecorded.
- **DoD:** under sustained input the recorder either keeps up or the session is throttled; a documented,
  closed set of input frame types; compliance doc states the guarantee honestly.
- **Tests:** load test asserting no silent drop (or explicit throttle); fuzz of frame `type` values.

### E3 — Cluster‑admin posture report (P1, supports C2 rollout)
- **Severity:** C1/C2 follow‑through · **Reasoning:** ship an admin view/report enumerating every
  managed cluster whose agent runs `admin` (cluster‑admin), so operators can re‑profile after B1.
- **DoD:** an admin can see "N clusters running cluster‑admin agents" with a re‑profile action.
- **Tests:** report unit test over a fixture of mixed profiles.

---

## 8. Workstream F — Proxy authz‑mapping correctness (P1/P2)

**Closes M2, M3, L1.**

### F1 — Always map pod exec/attach/portforward to `pods:exec` (P1)
- **Severity:** M2 · **Files:** `routes.go:1467‑1479` (`isHighRiskPodProxySubresource`),
  `k8sProxyVerb`/`namedResourcePermission`.
- **Reasoning:** The brittle segment matcher lets some path shapes fall through to a generic pod *write*
  verb, bypassing the dedicated `pods:exec` gate. Detect the `exec`/`attach`/`portforward` subresource
  robustly from the parsed object ref (subresource field) regardless of core‑vs‑apis prefix or trailing
  shape, and force `VerbExec`.
- **DoD:** every exec/attach/portforward path (any well‑formed variant) requires `pods:exec`; a unit
  test enumerates path variants.
- **Tests:** table of exec path variants → all require `pods:exec`; portforward likewise.

### F2 — Decide & enforce RBAC for CRDs / unknown apigroups (P2)
- **Severity:** M3 · **Files:** `routes.go:1228‑1245,1333‑1338`.
- **Reasoning:** Today unknown resource types (all CRDs, arbitrary apigroups, non‑resource URLs) collapse
  to a generic `clusters:read`/`clusters:update` — so per‑resource RBAC doesn't apply to custom
  resources. Decide the intended policy (e.g. a `customresources` resource verb, or per‑group mapping)
  and enforce it; at minimum document that CRD access is governed by the generic cluster verb. This pairs
  naturally with the proposed **dynamic CRD explorer** feature.
- **DoD:** CRD access policy is explicit and tested; non‑resource discovery URLs map to a read‑only verb.
- **Tests:** proxy permission tests for a sample CRD GET/POST and `/version`/`/apis`.

### F3 — Classify eviction as delete (P2)
- **Severity:** L1 · **Files:** `routes.go:1182‑1192`. **Reasoning:** POST `.../pods/{name}/eviction`
  deletes a pod but maps to `update`; classify the eviction subresource as `VerbDelete` for honest RBAC
  + audit. **DoD/Tests:** eviction → `pods:delete`; unit covers it.

---

## 9. Workstream G — DoS / fairness (P2)

### G1 — Per‑cluster rate limiting on proxy + exec/logs (P2)
- **Severity:** M5 · **Files:** `internal/middleware/api_rate_limit.go:117,214‑220`.
- **Reasoning:** Keys are `"<class>:u:<userID>"` — per‑user‑global, with no per‑cluster cap, so many
  users (or tokens) can saturate one cluster's single agent tunnel within policy. Add a **per‑cluster**
  dimension and/or an aggregate per‑cluster ceiling to `ClassK8sProxy`/`ClassExecLogs`.
- **DoD:** a synthetic load from many users against one cluster is bounded by a per‑cluster cap; other
  clusters are unaffected.
- **Tests:** limiter unit test with per‑cluster keys; an integration assertion that cluster B stays
  responsive while cluster A is hammered.

---

## 10. Workstream H — Future‑proofing (P3)

### H1‑task — Verify TLS before the v2 remoteproxy transport graduates (P3)
- **Severity:** M4 · **Files:** `internal/handler/remoteproxy/proxy.go:74‑99`.
- **Reasoning:** v2 uses `InsecureSkipVerify` end‑to‑end; it's hard‑gated out of prod today, but it's the
  intended future path. Before it ships to prod, pin/verify the apiserver CA (the agent already has it
  in‑cluster) so the migration doesn't graduate a TLS‑verification regression.
- **DoD:** v2 transport verifies the apiserver certificate; a test asserts a bad cert is rejected.
- **Tests:** transport test with a mismatched CA → connection refused.

---

## 11. Testing & validation strategy (program‑level)

1. **Negative tests are mandatory.** Each P0/P1 task lands the *attack it closes* as a failing‑then‑passing
   test (read token → 403 on mutate/exec; no‑profile → viewer; external `/internal` → unrouTable;
   `X‑Remote‑User` → stripped). These join the existing **route‑security gate suite** in
   `.github/workflows/pr-validation.yaml` (`go test ./internal/server -run 'Test(AdminRouteRegistrations…|HighRiskRoutes…|MutatingRoutes…|BrowserCookieMutatingRoutes…)'`).
2. **Rendered‑manifest assertions** (`helm template …`) for B1/B3/C1: no `*/*/*` in default agent roles;
   no ingress/HTTPRoute path forwards `/internal`, for both `frontend.enabled` values.
3. **RBAC matrix test**: a table of (scope × astronomer‑RBAC × route) → expected allow/deny, covering
   generic proxy + typed + exec/shell, so scope and RBAC are both proven orthogonally.
4. **Audit assertions**: every mutation path (generic, typed, internal door, exec/shell) emits a
   user‑attributed row; secret reads audited.
5. **No‑regression gate**: a checklist test that Appendix A behaviors still hold (impersonation stripping,
   per‑cluster scoping, PSK fail‑closed, `/v2` prod‑gating).
6. **Posture/CI lint** (optional): a check that fails CI if any chart/profile introduces a new `*/*/*`
   ClusterRole rule without an allowlisted exception.

---

## 12. Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Tightening default profile to `viewer` breaks existing automation expecting write | Med | Med | Code change doesn't touch already‑registered clusters; ship the E3 posture report; require explicit profile on *new* registrations with a clear error. |
| Allowlisting headers (D1) drops a header k8s needs (e.g. `Content-Type` variants, SSA) | Med | Med | Start from the known‑needed set; gate behind a feature flag; e2e the SSA/dry‑run/apply paths before defaulting on. |
| Scoping the management ClusterRole (B3) under‑grants and breaks platform install | Med | High | Derive the allowlist from observed API usage; keep a Helm values escape hatch; run full install e2e under the tightened role. |
| Separate internal listener (C1) complicates deployment | Low | Low | Prefer the "register under non‑forwarded path + explicit ingress deny + NetworkPolicy + source check" combo if a second port is operationally heavy. |
| Exec scope (A2) locks out existing read‑token automations that (incorrectly) exec | Low | Low | Intended — but announce; provide a clear path to mint an exec‑scoped token. |

---

## 13. Execution checklist

```
P0  (close the exploitable / fail‑open set)            ── GATE 0
  [ ] A1  Write‑scope fallthrough on typed mutation routes            (H1)
  [ ] A2  Exec/logs/shell + ticket issuance require exec/write scope  (H2) ⭐
  [ ] B1  Default agent profile → viewer; no silent admin             (C2) ⭐
  [ ] C1  Internal /internal/* unreachable from external ingress      (H4) ⭐

P1  (shrink the ceiling + boundary + mapping)          ── GATE 1
  [ ] D1  Strip X‑Remote‑*; allowlist; one shared helper              (H3,M1)
  [ ] E1  Break‑glass shell defaults to read‑only                     (H5)
  [ ] B2  operator profile drops RBAC‑write by default                (H6)
  [ ] F1  exec/attach/portforward always → pods:exec                  (M2)
  [ ] B3  Scope management agent ClusterRole + values knob            (C1)
  [ ] E3  Cluster‑admin posture report (C2 rollout aid)

P2  (defense‑in‑depth / DoS / audit)                   ── GATE 2
  [ ] F2  CRD / non‑resource RBAC policy decided + enforced           (M3)
  [ ] G1  Per‑cluster rate limits                                     (M5)
  [ ] C2t Audit the internal door                                    (M6)
  [ ] F3  Eviction classified as delete                              (L1)
  [ ] E2  Back‑pressure shell recording; close frame contract        (L3)

P3
  [ ] H1t v2 remoteproxy transport verifies TLS before prod          (M4)
```

---

## Appendix A — Confirmed‑solid baseline (do not regress)

These were verified during the review and are the load‑bearing controls the plan must preserve:

- **Credential & impersonation stripping** — `Authorization`, `Cookie`, `Host`, `X‑Forwarded‑*`,
  `Impersonate‑*` removed case‑insensitively before forwarding (`k8sproxy.go:404‑417`,
  `proxy.go:313‑326`); no `Impersonate` passthrough; no token‑creation rules in any profile/chart.
- **Response‑header filtering** — `Set‑Cookie`/`WWW‑Authenticate` etc. stripped from proxied responses.
- **Read vs write classification** — only GET/HEAD/OPTIONS are reads; everything else needs the write
  scope on the generic proxy (`routes.go:1445‑1452`); SSA PATCH is caught.
- **Strict per‑cluster scoping** — `bindingApplies` requires exact `ClusterID` match; no cross‑cluster
  reach without a global/superuser binding (`engine.go:61‑83`); ArgoCD internal door binds token→cluster.
- **PSK** — `subtle.ConstantTimeCompare`, key‑derived from the encryption key (rotates with it, never on
  the wire), **fail‑closed** when unset (`internal_k8s.go:51‑92`).
- **`/v2/pods/` demo** — hard‑gated out of production (`routes.go:877‑882`, `server.go:94‑102`).
- **User‑attributed audit** — mutations + secret reads + exec/shell opens stamped with the real user UUID
  + auth method, not the agent SA (`audit_helpers.go`, `tunnel/audit.go`, `exec_consumer.go:124`).
- **Secret RBAC** — `secrets` mapped to a dedicated `secrets:read` (critical‑rated) verb; plain
  `clusters:read` does **not** expose secrets; reads audited on both proxy and generic‑list surfaces.
- **Namespace‑scoped agent profiles** — `namespace-viewer`/`namespace-operator` correctly switch to a
  RoleBinding confined to `astronomer-system` (genuinely least‑privilege when selected).
- **Agent pod hardening** — non‑root, read‑only rootfs, no privilege escalation, all caps dropped,
  egress NetworkPolicy.
- **Shell lifecycle** — random unguessable names, 4h hard cap + 30m idle reaper, orphan‑pod sweep,
  per‑session SA/Role/Binding teardown, session‑ownership checks, admin‑only cross‑session visibility.

## Appendix B — Architectural note: agent‑SA vs user impersonation

Rancher impersonates the end user against the downstream cluster, so the *cluster* enforces that user's
real k8s RBAC. Astronomer centralizes authorization in its own layer with a shared agent SA. The
centralized model is legitimate and has real advantages (no per‑user kubeconfig/impersonation plumbing,
one policy + audit surface). But it concentrates the entire authorization burden in astronomer's RBAC +
token‑scope code and makes the agent SA's breadth the blast radius. The two strategic, optional
directions to consider beyond this plan:
1. **Tighten the default ceiling** (this plan: fail‑closed profiles, least‑privilege shell, scoped
   management role) — high ROI, no architecture change.
2. **Offer optional user impersonation** for clusters that want the cluster itself to be the final
   authority (the agent SA would need `impersonate` on `users/groups`, traded for true per‑user
   cluster‑side RBAC). Larger change; evaluate only if customers require cluster‑side enforcement.
