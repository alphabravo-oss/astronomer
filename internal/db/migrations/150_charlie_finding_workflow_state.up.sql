ALTER TABLE charlie_findings
    ADD COLUMN workflow_state VARCHAR(32) NOT NULL DEFAULT 'manual_remediation_required';

UPDATE charlie_findings
SET workflow_state = CASE
    WHEN status = 'dismissed' THEN 'dismissed'
    WHEN status = 'expired' OR execution_block_code = 'approval_expired' THEN 'expired'
    WHEN status = 'resolved' AND execution_block_code = 'approval_rejected' THEN 'rejected'
    WHEN status = 'resolved' THEN 'resolved'
    WHEN execution_block_code = 'approval_required'
         AND approval_id IS NOT NULL
         AND expires_at IS NOT NULL
         AND expires_at > now() THEN 'approval_pending'
    WHEN execution_block_code = 'verification_failed' THEN 'verification_pending'
    WHEN status = 'acknowledged' THEN 'remediation_in_progress'
    ELSE 'manual_remediation_required'
END;

ALTER TABLE charlie_findings
    ADD CONSTRAINT charlie_finding_workflow_state CHECK (workflow_state IN (
        'approval_pending', 'manual_remediation_required', 'remediation_in_progress',
        'verification_pending', 'resolved', 'rejected', 'dismissed', 'expired'
    )),
    ADD CONSTRAINT charlie_finding_workflow_consistency CHECK (
        (workflow_state = 'approval_pending' AND status IN ('open', 'acknowledged') AND
            execution_block_code = 'approval_required' AND approval_id IS NOT NULL) OR
        (workflow_state = 'manual_remediation_required' AND status IN ('open', 'acknowledged')) OR
        (workflow_state IN ('remediation_in_progress', 'verification_pending') AND status = 'acknowledged') OR
        (workflow_state = 'resolved' AND status = 'resolved') OR
        (workflow_state = 'rejected' AND status = 'resolved' AND execution_block_code = 'approval_rejected') OR
        (workflow_state = 'dismissed' AND status = 'dismissed') OR
        (workflow_state = 'expired' AND status = 'expired')
    );

UPDATE global_roles
SET rules = rules || '[{"resource":"charlie","verbs":["update"]}]'::jsonb,
    updated_at = now()
WHERE name IN ('Platform Operator', 'Charlie Approver')
  AND NOT rules @> '[{"resource":"charlie","verbs":["update"]}]'::jsonb;
