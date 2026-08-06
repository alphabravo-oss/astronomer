ALTER TABLE charlie_action_approvals
    DROP CONSTRAINT IF EXISTS charlie_approval_decision_request_unique,
    DROP CONSTRAINT IF EXISTS charlie_approval_decision_valid,
    DROP COLUMN IF EXISTS decision,
    DROP COLUMN IF EXISTS decision_request_id;
