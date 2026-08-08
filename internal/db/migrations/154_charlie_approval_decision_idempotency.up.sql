ALTER TABLE charlie_action_approvals
    ADD COLUMN decision_request_id UUID,
    ADD COLUMN decision VARCHAR(8);

-- Existing development rows predate exact decision-request binding. Give each
-- one a non-reusable request identity and derive the only decision consistent
-- with its terminal state before making the boundary mandatory.
UPDATE charlie_action_approvals
SET decision_request_id = gen_random_uuid(),
    decision = CASE WHEN state = 'rejected' THEN 'reject' ELSE 'approve' END;

ALTER TABLE charlie_action_approvals
    ALTER COLUMN decision_request_id SET NOT NULL,
    ALTER COLUMN decision SET NOT NULL,
    ADD CONSTRAINT charlie_approval_decision_valid
        CHECK (decision IN ('approve', 'reject')),
    ADD CONSTRAINT charlie_approval_decision_request_unique
        UNIQUE (connection_id, decision_request_id);
