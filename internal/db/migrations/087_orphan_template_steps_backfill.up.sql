-- Sprint 087 — data-fix for orphan template_applying steps.
--
-- Background: cluster f19ccaf0-ecdb-49c6-bb9b-82fa424b6a8b accumulated
-- 12 cluster_registration_steps rows with step_name='template_applying'
-- and status='running' over multiple retry cycles. The phase-machine
-- self-heal patch (CloseRunningStepsForCluster) closes these going
-- forward, but rows that drifted before the patch shipped still survive
-- on any fleet that upgraded mid-incident. This is a one-shot data fix
-- so other installs auto-heal on the next deploy.
--
-- Strategy: any 'template_applying running' row that has a later
-- step_order row for the same cluster is, by definition, no longer the
-- live attempt — the wizard moved on to a subsequent step. Mark those
-- as failed with a sentinel error message. We deliberately leave alone
-- the genuinely-in-flight row (no later step exists), so a cluster
-- mid-registration when the migration runs is not disrupted.

UPDATE cluster_registration_steps s
   SET status        = 'failed',
       completed_at  = COALESCE(s.completed_at, now()),
       error_message = CASE
                         WHEN s.error_message = ''
                         THEN 'superseded by retry — backfill 087'
                         ELSE s.error_message
                       END
 WHERE s.step_name = 'template_applying'
   AND s.status    = 'running'
   AND EXISTS (
        SELECT 1
          FROM cluster_registration_steps s2
         WHERE s2.cluster_id = s.cluster_id
           AND s2.step_order > s.step_order
   );
