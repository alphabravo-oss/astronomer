-- Reconcile pod streaming access after the WebSocket exec/logs authorizer moved
-- onto the dedicated pods:exec / pods:logs verbs (P1).
--
-- Until now /api/v1/ws/exec/ authorized on clusters:update and /api/v1/ws/logs/
-- on clusters:read, while the HTTP k8s-proxy path already used pods:exec and
-- pods:logs (see k8sProxyPermission in internal/server/routes.go). The two
-- halves now agree. Almost every resulting delta is the over-grant closing —
-- clusters:read is held by every audit/monitoring/GitOps viewer in the catalog
-- and it was handing them every log line in every namespace — and those roles
-- are deliberately NOT backfilled below.
--
-- Three rows are different: they lose pod-log access their OWN shipped template
-- grants. 001 seeded 'Cluster Member', 'Cluster Viewer' and 'Project Viewer'
-- before the template catalog existed, and 032/036/098 all insert with
-- `WHERE NOT EXISTS (... existing.name = v.name)`, so those rows have never
-- been reconciled against internal/rbac/templates/cluster-member.yaml
-- (pods:[read,list,watch,logs], "can manage workloads and inspect logs"),
-- cluster-viewer.yaml or project-viewer.yaml (both pods:[read,list,watch,logs]).
-- All three stream logs today via the clusters:read side door — 'Cluster Member'
-- holds clusters:read outright, the other two hold '*':[read,list,watch] which
-- matches it by wildcard. Without this migration a plain viewer's log pane
-- starts 403ing on upgrade while a fresh install's does not. That is an
-- unintended loss, so it is repaired here.
--
-- This is deliberately an ADD-IF-MISSING reconcile, not a rewrite: it touches
-- only pod streaming verbs, only on is_builtin rows, only for three named
-- roles, and only when the verb is absent. Operator-authored custom roles are
-- never modified. It grants no exec anywhere.
--
-- ── What is deliberately NOT here ──────────────────────────────────────────
--
-- 'Platform Operator' (098) and internal/rbac/templates/platform-operator.yaml
-- are NOT granted pods:exec or pods:logs. This is an intentional privilege
-- reduction, explicitly chosen, not an oversight. The role is defined as day-2
-- platform workflows *without* destructive platform administration and holds
-- ZERO pods rules of any kind — yet clusters:update alone let it open a
-- per-pod exec session in any container on any adopted cluster, and
-- clusters:read gave it every container's logs. Backfilling either verb would
-- re-grant that to a role whose own description disclaims it. Operators who
-- want that access should bind 'Cluster Operator' / 'Cluster Troubleshooter',
-- which declare pods:exec on purpose.
--
-- RESIDUAL GAP, stated plainly so nobody reads the paragraph above as more than
-- it is: 'Platform Operator' and 'Cluster Registrar' still hold the in-browser
-- break-glass kubectl shell, and this migration does not change that. That
-- shell is a separate path with its own gate — POST
-- /clusters/{id}/shell/sessions/ is gated on clusters:update in
-- internal/server/routes_cluster_addons.go, its ticket is minted on
-- clusters:update in internal/handler/stream_tickets.go, and
-- KubectlShellHandler.HandleWS authenticates the ticket itself and never calls
-- ExecConsumer.authorizeCluster, so the new pods:exec gate is not on it.
-- clusters:update additionally ELEVATES that session from read-only to k8s
-- create/update/patch (internal/handler/kubectl_shell.go). So of the two
-- RCE-equivalent paths a clusters:update holder has today, this change removes
-- one (per-pod WS exec) and leaves the other. Closing the second one — adding
-- pods:exec as a second conjunct on the shell's Open gate, mirroring
-- requireK8sProxyPermission — is a strictly-reducing change to a different
-- route and needs its own authorization; it is deliberately not smuggled in
-- here.
--
-- 'Cluster Registrar' (036) is NOT backfilled either, for the same reason as
-- Platform Operator: it holds clusters:[create,read,update,delete,list] with no
-- pods rule and is documented as "cannot edit cluster workloads". Exec into a
-- container is the most complete form of editing one.
--
-- The clusters:read-only roles that lose log streaming are NOT backfilled:
-- 'Standard User' and 'Read Only' (001), 'Catalog Maintainer' / 'Backup
-- Operator' / 'Alerts Manager' (036), 'Security Auditor', 'Compliance Manager',
-- 'GitOps Admin', 'GitOps Viewer', 'Monitoring Admin', 'Monitoring Viewer',
-- 'Restore Operator', 'Support Bundle Operator', 'Audit Viewer', 'Catalog
-- Admin' (098), 'Cluster Monitoring Admin' (036), 'Cluster Backup Operator'
-- (098). None declares a pods rule; the modern catalog expresses "may read pod
-- logs" as 'Logging Viewer' (098, pods:[read,list,logs]) and 'Auditor' (032,
-- pods:[...,logs]), both of which keep their access unchanged. Logs carry
-- bearer tokens and PII; a monitoring or GitOps viewer was never meant to read
-- them, and the HTTP proxy path has denied them for exactly that reason since
-- the pods:logs gate shipped.
--
-- 'Node Operator' (098) and 'Storage Manager' (098) are NOT backfilled: both
-- declare pods:[read,list(,watch)] and both templates omit `logs` on purpose,
-- so their rows and templates already agree. Nothing to reconcile.
--
-- Project roles ARE in scope, contrary to the "projectID = uuid.Nil so a
-- project binding never applied" reasoning that would suggest otherwise. That
-- reasoning is wrong: when namespace_scoped_rbac_enabled is on,
-- SQLCRBACQuerier.expandProjectBindings (internal/server/middleware/
-- rbac_queries.go) rewrites every project binding into synthetic Scope:'cluster'
-- bindings carrying a concrete ClusterID and Namespace, and the WS consumers
-- pass the pod's real namespace — so bindingApplies matches and the binding
-- does apply. 'Project Viewer' (001, '*':[read,list,watch]) therefore streams
-- pod logs today through the clusters:read side door and is repaired below,
-- exactly like its cluster-scope twin.
--
-- 'Project Member' (001) is NOT backfilled, even though project-member.yaml
-- declares pods:[read,list,watch,logs,exec] against its
-- pods:[read,list,watch]. This is where it differs from 'Cluster Member':
-- Cluster Member's seed carries clusters:[read] (001_initial.up.sql:693) so it
-- reads logs today and would LOSE that, while Project Member's seed has no
-- clusters rule and no wildcard, so CheckPermission(clusters, read) is false
-- for it and it has never streamed logs on either path. Adding the verb would
-- be a privilege ADDITION dressed up as a compatibility repair, and the
-- compatibility rule only obliges us to preserve access that exists. The
-- row/template divergence is real and worth reconciling deliberately; it is not
-- this migration's business. Every other seeded project role either declares
-- pods:logs already ('Project Operator', 'Project Troubleshooter', 'Project
-- Auditor', 'Project Deployer', 'Config Manager', 'GitOps Deployer', 'Workload
-- Deployer', 'Workload Viewer'), holds '*':['*'] ('Project Owner'), or holds no
-- clusters:read to lose it through ('Namespace Operator', 'Secret Manager',
-- 'Service and Ingress Manager').

-- 'Cluster Member' already carries a pods rule; add the missing `logs` verb to
-- it, matching cluster-member.yaml.
UPDATE cluster_roles
   SET rules = (
           SELECT jsonb_agg(
                      CASE
                          WHEN rule->>'resource' = 'pods'
                               AND NOT ((rule->'verbs') @> '"logs"'::jsonb)
                          THEN jsonb_set(rule, '{verbs}', (rule->'verbs') || '["logs"]'::jsonb)
                          ELSE rule
                      END
                      ORDER BY ord
                  )
             FROM jsonb_array_elements(cluster_roles.rules) WITH ORDINALITY AS elem(rule, ord)
       ),
       updated_at = now()
 WHERE is_builtin = true
   AND name = 'Cluster Member'
   AND rules @> '[{"resource":"pods"}]'::jsonb
   AND NOT rules @> '[{"resource":"pods","verbs":["logs"]}]'::jsonb;

-- 'Cluster Viewer' is seeded as a single '*':[read,list,watch] rule and has no
-- pods rule to extend, so append one. Only `logs` is actually new — read, list
-- and watch on pods are already covered by the wildcard rule, which is left
-- untouched. Appending `logs` to the wildcard rule instead would grant `logs`
-- on every resource, which is not what cluster-viewer.yaml says.
UPDATE cluster_roles
   SET rules = rules || '[{"resource":"pods","verbs":["read","list","watch","logs"]}]'::jsonb,
       updated_at = now()
 WHERE is_builtin = true
   AND name = 'Cluster Viewer'
   AND NOT rules @> '[{"resource":"pods"}]'::jsonb
   AND NOT rules @> '[{"resource":"*","verbs":["logs"]}]'::jsonb
   AND NOT rules @> '[{"resource":"*","verbs":["*"]}]'::jsonb;

-- 'Project Viewer' is the project-scope twin of 'Cluster Viewer': same
-- '*':[read,list,watch] seed, same missing pods rule, same wildcard guards, and
-- project-viewer.yaml likewise declares pods:[read,list,watch,logs].
UPDATE project_roles
   SET rules = rules || '[{"resource":"pods","verbs":["read","list","watch","logs"]}]'::jsonb,
       updated_at = now()
 WHERE is_builtin = true
   AND name = 'Project Viewer'
   AND NOT rules @> '[{"resource":"pods"}]'::jsonb
   AND NOT rules @> '[{"resource":"*","verbs":["logs"]}]'::jsonb
   AND NOT rules @> '[{"resource":"*","verbs":["*"]}]'::jsonb;
