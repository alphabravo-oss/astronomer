# Plan: "Argo Owns It All" — GitOps-managed remote-cluster lifecycle

**Status:** proposed
**Author:** generated for the Astronomer platform
**Scope:** make ArgoCD (in the management plane, reaching through the agent tunnel) the single owner of the entire Astronomer footprint on every managed cluster — the agent itself, its RBAC/config, and all platform components — with continuous reconciliation and central, GitOps-driven lifecycle.

---

## 1. Vision (the target the user stated)

```
1. Install command runs on the remote cluster        ← thin bootstrap (kubectl apply, once)
2. Cluster connects back and starts reporting        ← outbound WS tunnel + heartbeat
3. Remote cluster becomes managed by ArgoCD          ← registered as an Argo destination (through the tunnel)
4. Argo deploys & manages the lifecycle of ALL       ← agent + baseline + platform components,
   Astronomer components on the remote cluster          continuously reconciled, centrally versioned
```

After step 1, **nothing else is ever applied to the remote with kubectl by a human.** Desired state lives centrally; Argo converges every cluster to it, self-heals drift, and rolls upgrades when the central version changes.

---

## 2. Current state (code-grounded)

| Capability | Mechanism today | Files |
|---|---|---|
| Bootstrap | install command `kubectl apply` of the agent manifest (Deployment + SA + ClusterRole/Binding + scoped token Role + config) | `deploy/agent/install.yaml.template`, `internal/handler/clusters.go` (`renderAgentInstallManifest`, ~1918) |
| Connect / report | agent opens an **outbound** WS tunnel; heartbeats state + metrics | `internal/agent/tunnel.go`, `internal/agent/health.go` |
| Argo reaches remote | mgmt-cluster ArgoCD registers the remote as a destination **through the tunnel** (internal `:8090` listener / tunnel proxy) | provisioning architecture; `internal/tunnel/*`, `internal/server/self_manage_argocd.go` |
| Components on remote | baseline (kube-state-metrics, node-exporter) deployed as Argo Applications via `cluster_templates` | `cluster_template:apply` task; `internal/handler/cluster_registration.go` (~246) |
| **Agent lifecycle** | **tunnel self-upgrade** — `POST /agents/fleet/{id}/upgrade/` → `agent_lifecycle_operations` row → on heartbeat the server pushes `MsgAgentUpgrade` → agent **patches its own Deployment** | `internal/handler/agent_fleet.go` (~734), `internal/tunnel/handler.go` (~192), `internal/agent/self_upgrade.go` (~35) |

**Verified live this session:** adopting a cluster creates Argo Applications `astronomer-ksm-<cluster>` / `astronomer-node-exporter-<cluster>` that target the remote through the tunnel and report Synced/Healthy. So **steps 1–4 already exist for baseline components.** The agent itself is the exception.

---

## 3. The two structural gaps

### Gap A — the agent's own lifecycle is NOT Argo-managed
It bootstraps via kubectl and updates via the tunnel self-upgrade path, not GitOps. To realize the vision, **Argo must adopt and own the agent Deployment** after bootstrap.

### Gap B — RBAC: Argo applies *as the agent ServiceAccount*
ArgoCD reaches the remote kube API **through the tunnel**, and the agent forwards those API calls authenticated with **its own in-cluster ServiceAccount token**. Therefore everything Argo applies on the remote executes with the **agent SA's RBAC**.

Consequence: **"Argo owns it all" requires the agent SA to have write access to whatever Argo manages.** But the user-facing **viewer** profile is read-only — so today a viewer cluster's agent can't deploy *anything* (we saw this: viewer baseline apps sit `Unknown`, the agent can't create the workloads).

**Resolution (core design decision):** split RBAC into two independent axes:

1. **Astronomer self-management scope** — a fixed Role/ClusterRole granting the agent **write within Astronomer-owned namespaces only** (`astronomer-system`, `astronomer-baseline`, `astronomer-logging`, …) + write on its *own* Deployment. Present for **every** profile, including viewer. This is what lets Argo manage Astronomer's own footprint regardless of profile.
2. **User-cluster scope** — the existing `viewer | operator | admin | namespace-*` profile, which governs access to the **rest** of the cluster (the customer's workloads). Unchanged in meaning.

So: *viewer* = "Astronomer observes your cluster read-only, but still fully manages its own components." *admin* = "Astronomer can also act on your workloads." Argo always owns the Astronomer footprint; the profile only scopes reach into the customer's resources. This also fixes the current "viewer can't get baseline metrics" limitation.

---

## 4. Target architecture

```
            ┌─────────────────────── management cluster ───────────────────────┐
            │  server ──┐                                                       │
            │  worker   │   ArgoCD (astro-argocd)                               │
            │  postgres │     ├── App: astronomer-agent-<cluster>   ──┐         │
            │           │     ├── App: astronomer-baseline-<cluster> ─┤ through │
            │           │     └── App: astronomer-<component>-<clu> ──┤ tunnel  │
            └───────────┼──────────────────────────────────────────── ┼────────┘
                        │  WS tunnel (agent-initiated, outbound)        │
            ┌───────────┼──────────────────────────────────────────────┼──────┐
            │  remote   ▼  agent (bootstrap)  ◀── Argo syncs/owns ───────▼      │
            │  cluster     ├ Deployment (Argo-owned after handoff)              │
            │              ├ SA + self-mgmt Role (write in astronomer-* ns)     │
            │              ├ profile ClusterRole (viewer/admin → rest of cluster)│
            │              └ baseline / platform components (Argo-owned)         │
            └────────────────────────────────────────────────────────────────────┘
```

- **App-of-apps per cluster:** one parent Argo Application per managed cluster owns child Applications for `agent`, `baseline`, and each platform `component`. Single place to see/manage a cluster's whole footprint.
- **Source of truth:** a small **agent Helm chart** (or the existing rendered template promoted to an app source) + per-cluster values (cluster id, server URL, CA, profile, **image tag**). The image tag comes from the management plane's configured agent version.
- **Lifecycle:** bump the central agent version → update each `astronomer-agent-<cluster>` App's target → Argo syncs through the tunnel → rolling update (maxUnavailable=0) → agent reconnects → Argo marks Healthy.

---

## 5. Phased implementation

### Phase 0 — RBAC split (unblocks everything; ship first)
- Add a **self-management Role/ClusterRole** to the agent manifest, present for all profiles: write within `astronomer-*` namespaces + get/update/patch on the agent's own `Deployment` (resourceName-scoped) + the namespaces it manages.
- Keep the profile ClusterRole (viewer/operator/admin) for the rest of the cluster.
- **Test:** viewer agent can now create baseline workloads in `astronomer-baseline`; can patch its own Deployment; still cannot touch `default`/`kube-system` user workloads.
- *Files:* `deploy/agent/template.go` (new `selfManagementRBACRulesYAML`, always emitted), `deploy/agent/install.yaml.template`, tests.

### Phase 1 — Argo adopts the agent (the keystone)
- On connect/confirm, the server ensures an Argo `Application` `astronomer-agent-<cluster>` whose source renders the **full agent manifest** with per-cluster values and the central image tag, destination = the remote (through tunnel).
- Argo **adopts** the already-applied agent resources (same names → server-side apply hands ownership to Argo). `syncPolicy: automated { selfHeal: true, prune: false }` — **prune off on the agent app** so a sync glitch can never delete the running agent.
- **Test:** after adopt, the Application is Synced/Healthy and owns the agent Deployment; editing the Deployment out-of-band is reverted by selfHeal.
- *Files:* `internal/server` connect/handoff path, an agent app-source (chart or template promotion), `cluster_templates` or a dedicated handoff task.

### Phase 2 — Central version → roll all (Argo-driven lifecycle)
- A management-plane "agent version" (already `image.agent.tag`) drives every agent App's target. Changing it (new release) updates the Apps → Argo rolls every agent.
- Keep the existing tunnel self-upgrade as a **fallback** only (e.g. bootstrap-version skew before the first Argo sync). Not the primary path.
- **Test:** bump version centrally → all agents roll via Argo → reconnect → Healthy; no kubectl, no fleet API call.

### Phase 3 — Unify components under the per-cluster app-of-apps
- Fold baseline + platform components into one parent App per cluster (`astronomer-<cluster>`) that owns `agent` + `baseline` + `component` children. One object = a cluster's entire desired state.
- Baseline now deploys for **viewer** clusters too (Phase 0 RBAC makes it possible).

### Phase 4 — Drift, self-heal, decommission ordering
- selfHeal on all child apps (prune ON for components, OFF for the agent app).
- **Decommission ordering:** delete component apps first (Argo prunes through tunnel), then the agent app **last** (removing the agent drops the tunnel — so it must be the final step), then a namespace sweep. Wire into the existing decommission reconciler so connected-delete is clean and disconnected-delete tombstones safely.

---

## 6. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Argo updates the agent → tunnel drops mid-sync → Argo can't confirm | Rolling update `maxUnavailable: 0` keeps the old pod until the new one is Ready & reconnected; Argo is eventually-consistent and re-syncs on reconnect |
| A sync glitch prunes the running agent (self-eviction) | `prune: false` on the agent App; never auto-prune the agent's own Deployment/namespace |
| Bootstrap/version skew before first Argo sync | Bootstrap manifest pins a known-good tag; Argo converges to central version on first sync; self-upgrade fallback covers the window |
| Viewer agent can't manage Astronomer footprint | Phase 0 self-management RBAC scope (write in `astronomer-*` ns) — independent of the read-only user profile |
| Argo prune deletes user namespaces it shouldn't | Component apps scoped to `astronomer-*` namespaces only; never target user namespaces |
| Decommission removes the agent before components (tunnel dies, components orphaned) | Strict ordering: components first, agent last, then sweep |

---

## 7. Testing strategy (k3d e2e, both profiles)

1. Fresh k3d → run bootstrap install command → agent connects.
2. Assert Argo `astronomer-agent-<cluster>` App created, Synced/Healthy, owns the agent Deployment.
3. Out-of-band edit the agent Deployment → assert selfHeal reverts it.
4. Bump central agent version → assert Argo rolls the agent → reconnect → Healthy (no kubectl).
5. Viewer cluster: assert baseline + agent are Argo-managed and Healthy; assert agent still cannot write user namespaces.
6. Decommission → assert components pruned first, agent last, namespace swept, zero orphans.

---

## 8. Rollback

- Gate the handoff behind a feature flag (`config.argoOwnsAgent`). If an Argo-owned agent misbehaves, disable the flag → the server stops creating/updating the agent App and the existing tunnel self-upgrade path remains fully functional. No data migration; the agent Deployment is unchanged in shape.

---

## 9. Open questions (resolve during Phase 0/1)

- Agent app **source**: package a dedicated `astronomer-agent` Helm chart (cleanest, versioned with the release) vs. promote the existing Go template output into an app source (less new surface). Lean: small chart, published to the same OCI registry as the platform chart.
- Where the per-cluster values live (cluster id/url/CA/profile/tag): an Argo `Application` `helm.valuesObject` written by the server at handoff, vs. a generated values ConfigMap. Lean: `valuesObject` on the Application (server is already the writer for baseline apps).
- Whether the parent app-of-apps lives in-cluster (mgmt) per managed cluster, or one global ApplicationSet templated over the cluster inventory. Lean: ApplicationSet over the cluster list — least bespoke server code.
