-- Give the two default-on platform baseline components an explicit chart
-- version pin (P1).
--
-- The baseline ApplicationSet generator rendered `targetRevision: "*"` for
-- every component, with `automated: {prune: true, selfHeal: true}`, fanned out
-- to every adopted cluster and rewritten every 30s. That meant an upstream
-- release of kube-state-metrics or prometheus-node-exporter was applied
-- fleet-wide within one reconcile tick, unreviewed. It is not hypothetical:
-- kube-state-metrics 8.0.0 published on 2026-07-20 and every managed cluster
-- took the major bump on its own.
--
-- internal/baseline/registry.go now carries a compiled-in pin per component,
-- but a compiled-in constant is not an operator knob. cluster_tools is:
-- internal/server/baseline_appsets.go resolves the ApplicationSet revision as
-- charts[0].version -> version_constraint -> the compiled-in pin, so this
-- migration puts the pin where an operator can see and change it with one
-- UPDATE and no redeploy.
--
-- version_constraint (not a new column, not charts[].version) is deliberate:
-- handler/tools.go and handler/fleet_dispatcher.go ALREADY pass this column as
-- the helm version for a Tools-view install of the same slug. Seeding it means
-- one catalog row states one version for both delivery paths, instead of the
-- ApplicationSet path floating while the Tools path floats separately.
--
-- The values are the versions "*" resolved to on 2026-07-28, deliberately.
-- Every existing install is already running them, so this changes the
-- mechanism without moving deployed state; pinning to anything older would,
-- with prune+selfHeal on, roll every adopted cluster backwards on the next
-- tick. They match internal/baseline/registry.go and are bumped the same way:
-- a reviewed change, not an upstream publish.
--
-- ── What this deliberately does NOT do ─────────────────────────────────────
--
-- It does not touch a row whose version_constraint is already set. That value
-- is operator intent (the column is writable and has been since 001) and a
-- rewrite would silently re-pin a cluster the operator had deliberately held
-- at another version. Same add-if-missing, is_builtin-only shape as 141/142.
--
-- It does not pin the opt-in components (trivy-operator, fluent-bit,
-- ingress-nginx, cert-manager, gatekeeper). None of them is DefaultEnabled, so
-- none is auto-delivered as a baseline ApplicationSet on any cluster; they are
-- installed per cluster from the Tools view, where an unset version_constraint
-- means "resolve latest at install time" and is a per-install operator choice
-- rather than a standing fleet-wide auto-upgrade. Pinning them here would
-- change the meaning of an existing, working UI affordance for no security
-- gain. If one of them is ever promoted to DefaultEnabled it must gain a pin
-- in internal/baseline/registry.go, and TestBaselineRegistryPinsEveryDefault
-- fails until it does.
--
-- It does not need to reconcile already-created ApplicationSets in ArgoCD:
-- ensureBaselineApplicationSets does a Get + full Update on every 30s tick, so
-- an appset created with "*" is rewritten with the pin on the first tick after
-- upgrade. There is no forward-only gap for existing installs.

UPDATE cluster_tools
   SET version_constraint = '8.0.0',
       updated_at = now()
 WHERE is_builtin = true
   AND slug = 'kube-state-metrics'
   AND coalesce(trim(version_constraint), '') = '';

UPDATE cluster_tools
   SET version_constraint = '4.56.1',
       updated_at = now()
 WHERE is_builtin = true
   AND slug = 'prometheus-node-exporter'
   AND coalesce(trim(version_constraint), '') = '';
