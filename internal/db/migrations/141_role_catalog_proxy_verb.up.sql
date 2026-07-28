-- Reconcile the `proxy` verb on the built-in roles the catalog designates as
-- proxy-capable (P1).
--
-- The k8s proxy authorizer now maps the apiserver's `proxy` subresource to the
-- dedicated `proxy` verb instead of degrading it to the target's generic
-- read/update verb. Until now `pods:proxy` (032) and `services:proxy` (098)
-- were dead grants that gated nothing, so an install whose rows predate those
-- migrations under the same name — 032/098 both insert with
-- `WHERE NOT EXISTS (... existing.name = v.name)` and therefore skip an
-- already-present row — can be missing the verb without anyone noticing. Once
-- the gate is live that same row silently loses pod/service proxy.
--
-- This is deliberately an ADD-IF-MISSING reconcile, not a rewrite: it touches
-- only the single rule whose resource is pods/services, only on is_builtin
-- rows, and only when the verb is absent. Operator-authored custom roles are
-- never modified.
--
-- Note what is NOT here: no template gains `nodes:proxy`. The node proxy
-- subresource tunnels to the kubelet, whose /run/ endpoint executes arbitrary
-- commands in any container on the node, so it is cluster-admin-adjacent and
-- belongs to the wildcard owner/admin roles only. In particular 'Node
-- Operator' (098) keeps `nodes:[read,list,update,manage]` — cordon, drain,
-- label and taint need no kubelet proxy, and widening that rule would re-open
-- the fleet-wide RCE this change closes. Roles that previously reached the node
-- proxy via `nodes:update` are intentionally NOT backfilled.

UPDATE cluster_roles
   SET rules = (
           SELECT jsonb_agg(
                      CASE
                          WHEN rule->>'resource' = 'pods'
                               AND NOT ((rule->'verbs') @> '"proxy"'::jsonb)
                          THEN jsonb_set(rule, '{verbs}', (rule->'verbs') || '["proxy"]'::jsonb)
                          ELSE rule
                      END
                      ORDER BY ord
                  )
             FROM jsonb_array_elements(cluster_roles.rules) WITH ORDINALITY AS elem(rule, ord)
       ),
       updated_at = now()
 WHERE is_builtin = true
   AND name IN ('Cluster Operator', 'Cluster Troubleshooter')
   AND rules @> '[{"resource":"pods"}]'::jsonb
   AND NOT rules @> '[{"resource":"pods","verbs":["proxy"]}]'::jsonb;

UPDATE project_roles
   SET rules = (
           SELECT jsonb_agg(
                      CASE
                          WHEN rule->>'resource' = 'pods'
                               AND NOT ((rule->'verbs') @> '"proxy"'::jsonb)
                          THEN jsonb_set(rule, '{verbs}', (rule->'verbs') || '["proxy"]'::jsonb)
                          ELSE rule
                      END
                      ORDER BY ord
                  )
             FROM jsonb_array_elements(project_roles.rules) WITH ORDINALITY AS elem(rule, ord)
       ),
       updated_at = now()
 WHERE is_builtin = true
   AND name IN ('Project Operator', 'Project Troubleshooter')
   AND rules @> '[{"resource":"pods"}]'::jsonb
   AND NOT rules @> '[{"resource":"pods","verbs":["proxy"]}]'::jsonb;

UPDATE project_roles
   SET rules = (
           SELECT jsonb_agg(
                      CASE
                          WHEN rule->>'resource' = 'services'
                               AND NOT ((rule->'verbs') @> '"proxy"'::jsonb)
                          THEN jsonb_set(rule, '{verbs}', (rule->'verbs') || '["proxy"]'::jsonb)
                          ELSE rule
                      END
                      ORDER BY ord
                  )
             FROM jsonb_array_elements(project_roles.rules) WITH ORDINALITY AS elem(rule, ord)
       ),
       updated_at = now()
 WHERE is_builtin = true
   AND name = 'Service and Ingress Manager'
   AND rules @> '[{"resource":"services"}]'::jsonb
   AND NOT rules @> '[{"resource":"services","verbs":["proxy"]}]'::jsonb;
