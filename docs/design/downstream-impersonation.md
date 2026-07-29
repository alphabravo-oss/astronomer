# Downstream per-user impersonation — design

Status: **DESIGN, not yet implemented.** Covers the remediation item
`[no-downstream-impersonation]` + `[no-downstream-user-impersonation-agent-sa-is-cluster-admin]`
in `PARITY_AND_HARDENING_REMEDIATION_2026-07-28.md` §5.2. That item is explicitly
"a design item, not a patch", and its own remediation carries a `[corrected]` note
that "the remediation understates the work". This document is that work.

Audience: an engineer who has read the plan item and disagrees with it. The plan's
step 4 (agent-side runtime materialization of downstream ClusterRoles) is rejected
here for a specific, stated reason. Section 6 says exactly where a dissenter differs.

---

## 1. The problem, as corrected

The plan item corrects its own title twice. Both corrections shrink the problem
materially, and the design has to start from the corrected version, not the headline.

**Correction 1 — it is not every path.** The kubectl-shell path already materializes
per-caller downstream enforcement. `internal/handler/kubectl_shell_scope.go`
derives a `CallerScope` from the caller's own astronomer RoleBindings
(`deriveCallerScope`, :107), fails closed when the scope is undetermined (:167),
and `internal/kubectl/session.go` `Open` provisions a per-session ServiceAccount
bound either to a cluster-wide ClusterRole (:232-242), to per-namespace `Role` +
`RoleBinding` in exactly the caller's authorized namespaces (:214-231), or to
built-in `cluster-admin` for superusers (:207-212). That is Rancher's model, in
tree, for one path. **The gap is the HTTP k8s-proxy and the WS exec/logs paths.**

**Correction 2 — the agent SA is not cluster-admin *on the `viewer` profile*, and
that qualifier is load-bearing.** `deploy/agent/template.go`
`NormalizePrivilegeProfile` (:137) returns `viewer` for the empty string and for
any unrecognized value — it fails closed to least privilege. `viewerRBACRulesYAML`
(:286) is an enumerated read-only rule set with `secrets` deliberately absent.
`*/*/*` exists only in `adminRBACRulesYAML` (:279).

**But `operator` is not a contained tier, and most real clusters run it.** An
earlier draft of this section quoted only the reassuring half of the H4 comment at
:351. The comment says, in full and deliberately:

> `"operator"` is a **PRIVILEGED, near-cluster-admin tier — not a safely-contained
> one**. It grants cluster-wide secrets READ+WRITE and pod
> exec/attach/portforward, which are textbook INDIRECT cluster-admin primitives:
> reading every namespace's secrets exposes every ServiceAccount token (incl.
> cluster-admin-bound SAs), and exec into a kube-system / control-plane pod yields
> that pod's identity. It does NOT grant `rbac.authorization.k8s.io` WRITE (no
> DIRECT self-escalation) … **but do not mistake that for containment.**

It also carries `apiGroups:["*"] resources:["*"] verbs:[get,list,watch]`
(:382-384). And `internal/server/baseline_appsets.go:464-466` selects
`astronomer.io/agent-privilege-profile In [operator, admin]`, so **every cluster
running the shipped ArgoCD baseline must be `operator` or `admin`** — the `viewer`
default applies only to clusters that do not run it.

Two consequences for this design, both material:

1. The framing "least-privilege agent is the differentiator we must not erode" is
   true for `viewer` and false for the baseline population. On an `operator`
   cluster the agent can already read every Secret and exec into any pod, so the
   marginal privilege cost of adding a `resourceNames`-bounded `impersonate` verb
   is small — much smaller than this document's first draft implied.
2. Conversely, "missing second line of defence, not an open door" (§3) is true for
   `viewer` and **overstated for `operator`/`admin`**: on those profiles a single
   control-plane authorization defect is not merely a missing second line, it is
   reachable indirect cluster-admin. That strengthens rather than weakens the case
   for this work on exactly the clusters that run the baseline.

**Third fact, verified.** The control plane authorizes every proxied request:
`requireK8sProxyPermission` (`internal/server/routes.go:1319-1400`) resolves the
caller's bindings, derives `(resource, verb)` from the parsed k8s path — never from
a query param — and additionally installs a namespace allow-set into the request
context, which `internal/tunnel/proxy.go` applies to LIST and watch responses at
`:192`, `:324`, `:628`.

So the defect is **a missing second line of defence plus an attribution gap**, not
an open door. Concretely, what is actually broken today:

1. `internal/agent/k8sproxy.go` builds one client from `rest.InClusterConfig()`
   (:99) and reuses it for every proxied request (`executeUpstream` :441,
   `HandleStreamRequest` :191). Exec (`internal/agent/exec.go:81`), logs
   (`internal/agent/logs.go`) and helm (`internal/agent/helm.go:56`) share the same
   clientset/`rest.Config` via `K8sProxy.Client()` / `RESTConfig()` (:130, :135).
2. Caller identity is deliberately erased at both ends
   (`pkg/proxyhdr/proxyhdr.go`, `internal/tunnel/proxy.go:437-465`) — correctly, and
   that must not change; see §7.
3. `CallerScope.ImpersonationHeaders()` (`kubectl_shell_scope.go:258-265`) mints the
   canonical identity `astronomer:user:<uuid>` and is **dead code** — `rg` finds only
   its definition and `kubectl_shell_scope_test.go:50,89`.
4. Therefore the downstream apiserver audit log, `managedFields`, and any
   OPA/Gatekeeper `userInfo`-based policy see
   `system:serviceaccount:astronomer-system:astronomer-agent` for every action. A
   customer cannot forensically attribute a destructive change to a person, and
   cannot express a downstream policy that denies astronomer a capability.

---

## 2. The central tension, quantified

Astronomer's strongest differentiator over Rancher is that the agent runs
least-privilege by default: a no-annotation registration yields an enumerated,
read-only `viewer` ClusterRole with no secrets, no exec, no RBAC write, and no
mutation surface at all. `docs/agent-privilege-profiles.md` and the H4 comment
block at `template.go:351` sell exactly this property.

Kubernetes impersonation requires the impersonating principal to hold the
`impersonate` verb. The naive grant is:

```yaml
- apiGroups: [""]
  resources: ["users", "groups"]
  verbs: ["impersonate"]
```

**Unbounded `impersonate` on `groups` is cluster-admin.** `Impersonate-Group:
system:masters` bypasses authorization entirely. Unbounded `impersonate` on `users`
reaches every human and — with `serviceaccounts` — every SA in the cluster. A design
that adds those two rules to the `viewer` profile has converted the product's best
property into its worst: a read-only agent that can become root on demand.

The only sound bound is Rancher's: `resourceNames`. `rancher/pkg/impersonation/
impersonation.go:300-350` `rulesForUser` emits `impersonate` on `users` with
`ResourceNames: [userInfo.GetUID()]` and on `groups` with `ResourceNames: groups`,
leaving only `userextras/*` unbounded (extras confer no authority — RBAC never
authorizes on them). `resourceNames` has **no wildcard and no prefix matching**,
so somebody has to maintain the enumeration. *Who maintains it, and with what
privilege*, is the entire design question.

Rancher's answer is: a controller in the downstream cluster with RBAC write.
`impersonation.go` `createRole`/`checkAndUpdateRole` (:277, :252) create and update
`ClusterRole`s, and `ForCluster` provisions ServiceAccounts and Secrets in
`cattle-impersonation-system`. **RBAC write is a strictly larger privilege increase
than `impersonate` itself** — a principal that can create ClusterRoleBindings can
bind itself `cluster-admin` in one call. Adopting Rancher's answer wholesale means
every impersonation-enabled astronomer cluster runs an agent that is
cluster-admin-equivalent, which is precisely the property we beat Rancher on.

That is the trade every option below has to price.

---

## 3. Options

### Option A — Per-user `Impersonate-User` + agent-side runtime role materialization

The plan item's approach (steps 1-5), i.e. Rancher's, adapted to one shared agent SA.
Payload carries `ImpersonateUser: astronomer:user:<uuid>` and
`ImpersonateGroups: [astronomer:role:<slug>...]`; the agent stamps them on the
outgoing request; an agent-side reconciler materializes a downstream ClusterRole/Role
+ Binding per astronomer role-template grant per subject.

*Agent SA must gain:* `impersonate` on `users` and `groups`; **and** `create`,
`update`, `delete` on `clusterroles`, `clusterrolebindings`, `roles`,
`rolebindings`, plus `escalate`/`bind` or an equivalent superset of every right it
grants (the apiserver refuses to create a role granting rights the creator lacks
unless the creator holds `escalate`). To keep `resourceNames` honest the reconciler
must also rewrite the agent's own ClusterRole as the binding set changes — the agent
editing the ClusterRole that bounds the agent.

*Works on `viewer`?* Only by making `viewer` a different profile. The delta is
RBAC write + `escalate`, i.e. cluster-admin-equivalent. The read-only guarantee is gone.
Worse, the impersonated request's rights are the union of the customer-authored
downstream roles, which may exceed the agent profile's own rights — **the profile
stops being a ceiling.** Today an operator can say "worst case, astronomer reads".
Under A they cannot say anything.

*If the customer has not created the downstream roles:* nothing breaks — the agent
creates them. That is A's one real advantage and it is bought with the grant above.

*Failure mode on 403:* the apiserver returns a 403 naming the impersonated subject;
the proxy passes the status through, so the UI shows a generic "forbidden" that the
user cannot distinguish from an astronomer-side denial.

*Blast radius of a bug:* catastrophic tail. A `resourceNames` enumeration bug that
admits `system:masters`, an `escalate` misuse, or a reconciler that binds the wrong
subject is instant cluster-admin on every enabled cluster. A fail-open bug (empty
identity field silently not stamped) degrades to today's behaviour — harmless.

*Migration:* new agent image + re-applied RBAC on every cluster, plus a capability
handshake so the server does not send identity to an agent that will ignore it.

*Does not solve:* helm, kubectl-shell (separate model), LIST filtering (§9),
service proxy, agent-originated background work.

*Files:* `pkg/protocol/types.go`; `internal/tunnel/proxy.go`;
`internal/handler/k8s_requester.go`; `internal/agent/k8sproxy.go`;
`internal/agent/exec.go`, `logs.go`; **new** `internal/agent/impersonation_reconciler.go`
(hook: `internal/agent/project_reconciler.go`); `deploy/agent/template.go` (all profiles).

---

### Option B — Extend the kubectl-shell per-session ServiceAccount model to proxy/exec/logs

Reuse `internal/kubectl` rather than build a parallel mechanism: per caller×cluster,
materialize a ServiceAccount + ClusterRole (or per-namespace Roles) derived from the
caller's astronomer bindings, mint a token via `TokenRequest`, and use it as the
`Authorization` bearer for proxied requests. This is Rancher's
`impersonatorAccountTokenGetter` shape (`proxy_server.go:288-296`) minus the headers.

*Agent SA must gain:* everything A needs **except** `impersonate`, **plus**
`serviceaccounts` create and `serviceaccounts/token` create. That is worse, not
better: `serviceaccounts/token` on a cluster where any SA is bound to `cluster-admin`
is a direct path to `cluster-admin` with no `escalate` required.

*Works on `viewer`?* No, and it never can. Verified: the existing shell path already
requires this grant — `session.go:192-242` POSTs a ServiceAccount, ClusterRole, and
ClusterRoleBinding through the k8s proxy using the agent SA's credentials. `viewer`
has `serviceaccounts` get/list/watch only and no `rbac.authorization.k8s.io` rules;
`operator` can create ServiceAccounts but has RBAC read-only. **The in-tree
per-caller enforcement precedent is an `admin`-profile-only feature.** Option B is
honest about generalizing an admin-only mechanism; it is not a path to per-user
enforcement on the profiles that constitute the differentiator.

*If the customer has not created downstream roles:* irrelevant — we author them.

*Failure mode on 403:* same as A, plus a revocation-lag hole: a revoked astronomer
binding leaves a live downstream SA and a valid token until the reconciler catches
up. Rancher has this same hole.

*Blast radius of a bug:* same catastrophic tail as A, via a different primitive.
Additionally the derived role is generated from *the same binding data the control
plane already evaluated* — so **B is not an independent second line of defence, it is
the same line drawn twice.** A bug in `rbac.Engine` binding evaluation propagates
identically to both. That is the decisive objection: it buys attribution (audit shows
`system:serviceaccount:...:astronomer-caller-<uuid>`, which is genuinely useful) but
close to zero defence-in-depth, at the largest privilege cost of any option.

*Also does not solve:* the customer still cannot deny astronomer a capability
downstream, because the enforcing role is minted by astronomer from astronomer's own
model. Under A and D the customer authors the downstream role and can shrink it.

*Files:* `internal/kubectl/manifests.go`, `session.go` (generalize away from
sessions); **new** identity-SA cache + `TokenRequest` client in `internal/agent`;
`internal/agent/k8sproxy.go` (per-identity transport cache);
`deploy/agent/template.go`.

---

### Option C — Attribution-only, zero RBAC change

Propagate identity for the downstream record without touching enforcement. Three
mechanisms, none of which needs any new verb:

1. **`User-Agent`** — the apiserver audit event records `userAgent` verbatim. Stamp
   `astronomer/<ver> (user=astronomer:user:<uuid>; role=<slug>; req=<request-id>)`.
2. **`?fieldManager=`** — honoured on POST/PUT/PATCH and persisted into
   `metadata.managedFields` on the object itself. This is *durable, on-object*
   attribution that outlives audit-log rotation and is visible in
   `kubectl get -o yaml`. Constraint: ≤128 chars, so carry the uuid, not the email.
3. The control plane's own audit trail, which already records the actor and is
   already the authoritative record.

*Agent SA must gain:* nothing.
*Works on `viewer`?* Yes, unchanged. The differentiator is fully preserved.
*If the customer has not created downstream roles:* not applicable.
*Failure mode:* none — no request can be denied by this change.
*Blast radius of a bug:* a wrong or absent string in a log line.
*Migration:* agent image bump; old agents ignore the field. No RBAC re-apply.

*Does not solve:* it is not a second line of defence at all. A control-plane authz
bug is still full agent-SA-level access. And critically, **admission cannot see it**:
`AdmissionReview.request.userInfo` carries username/uid/groups/extra and has no
`userAgent` field, so OPA/Gatekeeper user-based policies still cannot discriminate.
Half the requirement, cheaply and safely.

*Files:* `pkg/protocol/types.go`; `internal/tunnel/proxy.go`;
`internal/handler/k8s_requester.go`; `internal/agent/k8sproxy.go`.

---

### Option D — Pinned subject + role-group authority, roles materialized at install time (**recommended**)

D is A with two corrections. State it that way, because that is what it is.

**Correction (i): impersonate a pinned subject and per-role groups, not per-user
identities.** The outgoing request carries:

```
Impersonate-User:                    astronomer:proxy
Impersonate-Group:                   astronomer:role:cluster-operator      (0..n, one per applicable astronomer role)
Impersonate-Extra-Astronomer-User:   <uuid>
Impersonate-Extra-Astronomer-Request:<request-id>
```

and the agent's ClusterRole gains exactly:

```yaml
- apiGroups: [""]
  resources: ["users"]
  verbs: ["impersonate"]
  resourceNames: ["astronomer:proxy"]           # ONE name. Never churns.
- apiGroups: [""]
  resources: ["groups"]
  verbs: ["impersonate"]
  resourceNames: ["astronomer:role:<slug>", ...] # the built-in role catalogue; ~10 names, stable
- apiGroups: ["authentication.k8s.io"]
  resources: ["userextras/astronomer-user", "userextras/astronomer-request"]
  verbs: ["impersonate"]                         # unbounded; extras confer no authority
```

Kubernetes requires a user when groups are impersonated (`Impersonate-Group` alone is
rejected), hence the pinned `astronomer:proxy`. Authority comes from the groups.
Because the group list is the astronomer **role catalogue** — not the user set — it is
small, stable, known at build time, and **never has to be rewritten as users and
bindings change.** That is what removes the need for a runtime RBAC writer.

The per-human identity rides in `Impersonate-Extra-Astronomer-User`, which lands in
`userInfo.extra` and is therefore visible to **both** the apiserver audit log **and
admission webhooks** — closing the one thing Option C cannot do. Rancher grants
`userextras/*` with unbounded `resourceNames` for the same reason
(`impersonation.go:308-322`, with the explicit comment "to avoid constantly updating
the ClusterRole we allow all values here").

**Correction (ii): materialize the downstream roles at install time, in the manifest,
not at runtime, in the agent.** The generated `astronomer-agent.yaml` gains an
opt-in, clearly-delimited section containing one `ClusterRole` +
`ClusterRoleBinding` per astronomer role group, rendered from the same role catalogue
`deploy/agent/template.go` already renders profiles from. Consequences:

- The agent never needs RBAC write, `escalate`, `bind`, or SA-token minting. The
  privilege delta over `viewer` is *only* the three `impersonate` rules above.
- The rules are `resourceNames`-bounded to names the customer can enumerate by
  reading the manifest. The agent cannot reach `system:masters`, cannot reach any
  human's account, cannot reach any ServiceAccount, cannot write RBAC.
- **The customer authors the downstream authority and can shrink it.** Deleting the
  `astronomer:role:cluster-operator` ClusterRole is a supported, one-command way to
  deny astronomer a capability downstream — the property the plan item names as
  missing, delivered without giving us the ability to undo it.
- A namespace-scoped astronomer binding composes correctly: the server sends the role
  group, the customer's namespaced `RoleBinding` in that namespace grants it there,
  and the absence of a `ClusterRoleBinding` makes cluster-wide calls 403. No new
  vocabulary needed.

*Works on `viewer`?* Yes — and the ceiling is preserved *by default*, because the
generated role bodies default to mirroring the installed profile's own rules. An
operator who wants the impersonated identity to do more than the agent profile does
must edit the manifest, which is a visible, reviewed, auditable act.

*If the customer has not created the downstream roles:* the impersonated request has
the rights of `system:authenticated` only — i.e. `system:basic-user` /
`system:public-info-viewer`, effectively nothing — so it 403s. Fail-closed by
construction; it can never be an escalation. Mitigated by the preflight in §8.

*Failure mode on 403:* same raw 403 as A/B unless we disambiguate. §8 item 9 makes
that an approach-independent deliverable.

*Blast radius of a bug:* bounded, and this is D's main argument. The worst
identity-forgery bug still lands inside `resourceNames`, so the ceiling is "acts as
some astronomer role group the customer explicitly authored a role for" — never
`system:masters`, never another agent, never RBAC write. There is no catastrophic
tail. Two footguns to document: (1) a customer who binds a ClusterRole to **user**
`astronomer:proxy` grants it to *every* caller — the generated manifest never does
this, and a lint check should flag it; (2) impersonated requests implicitly gain
`system:authenticated`, so the floor is that group's bindings, not zero.

*Migration:* agent image bump (payload fields + stamping), plus an opt-in RBAC
re-apply per cluster to enable `enforce`. `attribute` mode (§8) needs the image bump
only. Old agents that never advertise the capability are never sent enforcing traffic.

*Does not solve:* per-human *enforcement* granularity — two users holding the same
astronomer role are indistinguishable to downstream RBAC and to any policy that keys
on `groups` (they remain distinguishable in audit and in admission via the extra).
If a customer genuinely needs per-human downstream RBAC, D cannot give it and A must
be revisited. Plus everything in §9.

*Files:* `pkg/protocol/types.go`; `internal/tunnel/proxy.go`;
`internal/handler/k8s_requester.go`; `internal/tunnel/originators.go`,
`exec_consumer.go`; `internal/handler/kubectl_shell_scope.go` (promote the identity
minter); `internal/agent/k8sproxy.go`, `exec.go`, `logs.go`;
`deploy/agent/template.go` (three rules + the generated role section);
`internal/server/routes.go` (machine-origin markers).

---

## 4. Comparison

| | **A** per-user + runtime materialization | **B** per-session SA (extend shell) | **C** attribution-only | **D** pinned subject + role groups |
|---|---|---|---|---|
| Agent SA gains | `impersonate` users+groups (unbounded or self-rewritten), RBAC write, `escalate` | RBAC write, `escalate`, `serviceaccounts`, `serviceaccounts/token` | nothing | `impersonate` on 1 user name + ~10 group names + 2 extras |
| Effectively cluster-admin? | **yes** | **yes** | no | **no** |
| Works on `viewer` | only by redefining `viewer` | never (admin-only mechanism) | yes | yes |
| Profile stays a ceiling | no | no | yes | yes, by default |
| Independent 2nd line | yes | **no** (same binding data twice) | no | yes |
| Customer can deny us downstream | yes | no | no | yes |
| Admission-visible identity | yes (per human) | yes (per human, as an SA) | **no** | yes (per human, via extra) |
| Enforcement granularity | per human | per human | none | per role |
| No downstream roles ⇒ | works (we create them) | works (we create them) | n/a | 403, fail-closed |
| Catastrophic-bug tail | yes | yes | none | none |
| Per-request state | reconciler | SA + token cache, revocation lag | none | none |

---

## 5. Recommendation

**Ship D, with C's attribution as its first, separately-enableable mode.**

The flag is tri-state, not boolean:

| `astronomer.io/downstream-impersonation` | behaviour |
|---|---|
| absent / `off` (**default**) | identical to today. Identity is carried in the payload and dropped by the agent. |
| `attribute` | agent stamps `User-Agent` + `fieldManager` only. No RBAC change, no request can be denied. |
| `enforce` | agent additionally stamps the `Impersonate-*` headers. Requires the RBAC re-apply and the capability advertisement. |

Stored in `clusters.annotations` (JSONB, `001_initial.up.sql:80-81`) — no migration —
and settable by superusers only.

## 6. Reasoning, and where a dissenter differs

The decision rests on one claim: **the `impersonate` verb is cheap when
`resourceNames`-bounded; maintaining the `resourceNames` enumeration at runtime is
what is expensive.** Rancher pays that cost with a downstream RBAC writer. Astronomer
does not have to, because we can choose an identity vocabulary — the *role* catalogue
rather than the *user* set — whose enumeration is finite, known at build time, and
never churns. Once you make that substitution, every remaining requirement is met
without the agent gaining a single write verb.

If you disagree, it will be at one of exactly four points:

1. **You believe per-human downstream RBAC is a requirement, not a nice-to-have.**
   Then D is insufficient and you must accept A's grant. My position: astronomer's own
   authority model is user→role→scope; the *role* is the unit of authority, so
   projecting roles downstream loses nothing that astronomer itself expresses. If a
   customer's downstream policy needs the human, the extra carries it into admission.
2. **You believe install-time materialization is operationally worse than a
   reconciler**, because adding a role template later requires re-applying the
   manifest. True, and the cost is real: a new role template needs a manifest bump.
   My position: that is a rare, versioned, reviewable event, and paying for it with a
   permanently cluster-admin-equivalent agent is a bad trade.
3. **You believe B's reuse of `internal/kubectl` outweighs its cost.** The reuse is
   genuine and the code is good. But B is not an independent second line — the role it
   materializes is derived from the same `rbac.Engine` evaluation the control plane
   already ran, so it backstops nothing — and it requires the largest grant of the four.
   Reuse of a mechanism that does not deliver the property is not a reason to ship it.
4. **You believe the `viewer` differentiator is marketing, not engineering.** Then A is
   fine and this whole document is over-thought. I would want that argued explicitly
   before it is decided implicitly by a merge.

---

## 7. Invariants that hold under every option

These are non-negotiable and any implementation that violates one is wrong:

1. **Identity travels as a typed payload field, never as a forwarded header.**
   `pkg/proxyhdr.forwardableHeaders` must remain a hard deny for `Impersonate-*`,
   `X-Remote-*` and `Authorization`. `pkg/proxyhdr/proxyhdr_test.go` must keep
   asserting `ShouldForwardRequestHeader("Impersonate-User") == false`. See
   `[strength-proxy-header-allowlist]`.
2. **The agent strips inbound `Impersonate-*` from `req.Headers` before setting its
   own.** Today the `proxyhdr` allowlist in `executeUpstream` (:466) and
   `HandleStreamRequest` (:227) already achieves this; a test must pin it so a future
   allowlist edit cannot silently re-open header-carried identity.
3. **Identity is populated server-side from `appmiddleware.GetAuthenticatedUser`
   only** — never from a client header, query param, or body field.
4. **Machine origin is an explicit positive marker, not the absence of a user.**
   Inferring "no user ⇒ machine" fails open the moment an authenticated route forgets
   to populate the field.
5. **Superuser scopes are not impersonated** (mirrors the existing
   `ImpersonationHeaders` contract at `kubectl_shell_scope.go:259`).
6. **Fail closed on a user-originated request with no identity when the mode is
   `enforce`** — return a 403 `Status` object from the agent without touching upstream.

---

## 8. Approach-independent work — the implementation phase builds exactly this

Every item below is byte-identical under A, B, C and D, and every item is inert with
the flag at its `off` default. This is the whole of what may be built before the
design decision is ratified.

1. **Typed payload fields.** `pkg/protocol/types.go`: add to `K8sRequestPayload`
   (:429) and to `ExecStartPayload` (:550) / `LogStartPayload` (:571) a single
   embedded `CallerIdentity` struct — `User`, `Groups`, `RequestID`, `Origin`. One
   struct, not four copies. Nothing reads it yet.
2. **Origin discriminator.** `Origin` is `user` | `machine` with no zero-value default
   that means "user". Stamped positively by:
   `requireArgoCDClusterProxyToken` (`routes.go:2000`, both the internal listener at
   `:1533` and the compatibility route at `:1096`); the PSK-gated internal fallbacks
   `POST /internal/tunnel/k8s/{cluster_id}` and `/internal/tunnel/helm/{cluster_id}`
   (`routes.go:1103`, `:1110`); and every internal `TunnelK8sRequester` caller that is
   not acting for a human.
3. **Server-side population.** `internal/tunnel/proxy.go` `buildK8sRequestPayload`
   (:437); `internal/handler/k8s_requester.go` `Do` (:129) **and `forwardToOwner`
   (:321)** — the HA forward path rebuilds the payload from scratch and will silently
   drop identity on multi-replica installs if it is not updated in the same change;
   `internal/tunnel/originators.go` `StartExecSession` (:297) and
   `exec_consumer.go:245`, both of which already have `userID` in hand
   (`exec_consumer.go:160-173`).
4. **One identity minter.** Promote `CallerScope.ImpersonationHeaders()`
   (`kubectl_shell_scope.go:258`) from dead code into an exported, tested function in
   a package both the handler and tunnel layers can import — `astronomer:user:<uuid>`
   for the user form, `astronomer:role:<slug>` for the group form. Delete the
   duplicate rather than adding a second spelling of the canonical identity.
5. **Header-hygiene regression tests.** Invariants 1 and 2 of §7, pinned.
6. **Capability handshake.** The agent probes its own rights with a
   `SelfSubjectAccessReview` — the machinery exists (`internal/handler/agent_fleet.go:1305-1331`,
   and the chart already grants `selfsubjectaccessreviews`,
   `deploy/chart/templates/serviceaccount.yaml:157-160`) — and advertises
   `impersonation` in `HeartbeatPayload.EnabledFeatures` / `DeniedFeatures`
   (`pkg/protocol/types.go:542`, surfaced at `internal/tunnel/handler.go:142`).
   The server refuses to move a cluster to `enforce` unless the agent has advertised
   it. This is what prevents both the version-skew failure and the
   "flag on, every request 403s" failure the plan's acceptance criteria call out.
7. **Flag plumbing.** Tri-state read from `clusters.annotations`, superuser-gated
   write, surfaced in the cluster detail API. Always resolves to `off` until Phase 1.
8. **Attribution stamping** (`attribute` mode; Option C's payload, additive and safe
   under all four). `User-Agent` and `?fieldManager=` in `executeUpstream` and
   `HandleStreamRequest`.
9. **403 disambiguation.** When the mode is not `off` and upstream returns 403, tag
   the response so the UI can say "denied by the downstream cluster" rather than
   rendering a bare Forbidden that is indistinguishable from an astronomer-side denial.
10. **One `machineOrigin` predicate**, in one place, with the exemption list from §10
    as data. Not scattered `if` statements across four files.

Not in this list, and therefore not in the implementation phase: any
`deploy/agent/template.go` RBAC change, any downstream ClusterRole materialization,
and any code path that actually sets an `Impersonate-*` header.

---

## 9. Phased rollout

- **Phase 0 — flag off.** §8 only. Runtime behaviour is unchanged; the identity
  fields are populated and discarded. Gate: `make verify` + the header-hygiene
  regressions.
- **Phase 1 — `attribute` on internal clusters, ≥2 weeks.** Zero RBAC change and no
  request can be denied, so this soaks the payload plumbing, the HA forward path, and
  above all the machine-origin exemptions under real load, with the blast radius of a
  log line. Exit criterion: zero machine-origin requests observed carrying a user
  identity, and zero user-origin requests observed carrying none.
- **Phase 2 — the design decision is ratified; ship the RBAC.** The three
  `impersonate` rules and the generated downstream-role section, both opt-in in the
  install manifest. Enable `enforce` on exactly one internal cluster.
  **Gate: the LIST-fanout question in §11 must be answered before any customer
  cluster is enabled**, because it is not a bug, it is a semantic incompatibility.
- **Phase 3 — general availability, default still `off`.** Per-cluster, operator-
  initiated, documented as a posture upgrade with a stated prerequisite. There is no
  version at which this flips on globally.

---

## 10. Machine identities that must never be impersonated

Each of these must be `Origin: machine`, must be exempt from the fail-closed check,
and must be covered by a test that fails if the exemption is dropped.

1. **The ArgoCD internal k8s proxy** — `NewInternalArgoCDProxyRouter`
   (`internal/server/routes.go:1520-1533`) and the compatibility route on the public
   listener (`:1091-1096`). Token-gated by `requireArgoCDClusterProxyToken`, no human
   in the request. Non-negotiable for a second reason: ArgoCD's cluster cache LISTs
   every resource type registered in the target cluster (see the H4 note at
   `deploy/agent/template.go:351-386`), so an impersonated identity would fail the
   whole cache sync and pin every Application at `sync=Unknown` — silently.
2. **Self-management into `AstronomerOwnedNamespaces`** — `internal/agent/reconcile.go`
   (safety contract at :19-28, bounds at :443, :489), `internal/agent/rbac.go:226`,
   `internal/handler/projects.go:1412`. The list is `deploy/agent/template.go:222`.
3. **The cross-pod internal fallbacks** — `POST /internal/tunnel/k8s/{cluster_id}`
   and `/internal/tunnel/helm/{cluster_id}` (`routes.go:1103`, `:1110`). Subtlety:
   these are *transport*, not origin. A user-originated request that lands on a
   non-owner replica arrives here still user-originated, so `forwardToOwner`
   (`k8s_requester.go:308-321`) must **forward** the identity fields verbatim rather
   than re-deriving or clearing them.
4. **kubectl-shell session provisioning and teardown** — `internal/kubectl/session.go`
   `Open` / `Close` / `Reap`. These create the ServiceAccount, Role(s), Binding(s) and
   Pod that *are* the shell's enforcement, and must run as the agent SA. The shell's
   per-caller enforcement is already delivered by `CallerScope`; impersonation does not
   apply to it and must not be layered on top.
5. **Agent lifecycle** — self-upgrade (`internal/agent/upgrade_watchdog.go`),
   decommission (`internal/agent/decommission.go`), heartbeat/health
   (`internal/agent/health.go`), all informers, and the SSAR self-probes.
6. **Baseline / CRD reconcile** — `internal/server/baseline_appsets.go`,
   `internal/crd/controller.go`.

---

## 11. What this does not solve

1. **k8s RBAC allows or denies a request; it does not filter a response.** This is the
   single biggest unresolved item and it is a semantic incompatibility, not a bug.
   Today the control plane authorizes the request (`requireK8sProxyPermission`,
   `routes.go:1319`) and then *filters* LIST and watch payloads to the caller's
   authorized namespaces (`internal/tunnel/proxy.go:192`, `:324`, `:628`, fed by
   `AuthorizedNamespaces` at `routes.go:1414-1431`). Downstream RBAC has no such
   behaviour: a cluster-wide `GET /api/v1/pods` from an identity holding only
   namespaced rights returns **403**, not a filtered list. So for any caller whose
   astronomer bindings are namespace-scoped, `enforce` mode converts a working
   filtered list into a hard failure. Either the flag is restricted to callers with
   cluster-wide bindings, or the proxy learns to fan a cluster-scoped LIST out into
   per-namespace calls and merge. **That decision gates Phase 2.**
2. **Helm.** `internal/agent/helm.go` drives all six actions through
   `actionConfig` (:56) over the shared `rest.Config`, and Helm writes its release
   Secrets as whoever holds that config. Helm actions remain agent-SA-attributed.
3. **kubectl-shell.** Separate model (§1, correction 1); the session ServiceAccount
   is the enforcement point and stays so.
4. **Watch streams after establishment.** Authorization is evaluated at stream open.
   A binding revoked mid-stream does not tear the stream down — true today and true
   after; independent of this design.
5. **Service proxy** (`internal/agent/service_proxy.go`) — proxies to a Service, not
   to the apiserver. Impersonation has no meaning there.
6. **`kubectl get events` attribution.** The downstream event stream still shows the
   agent SA unless the agent additionally *emits* Events, which needs `events` create
   — a write verb `viewer` does not have. Deliberately out of scope.
7. **The agent's own standing privilege is not reduced.** This adds a bounded new
   grant; it removes nothing. The profile still bounds what the agent does on its own
   behalf, and under D it still bounds what impersonated callers do by default.
8. **It does not repair the authz defects it backstops.** `exec-logs-verb-mismatch`,
   `cluster-project-list-unfiltered` and the two P0 items are the *reason* a second
   line is wanted; they must be fixed on their own terms first. Plan sequencing note
   34 says the same thing.
