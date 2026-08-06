ALTER TABLE charlie_findings
    DROP CONSTRAINT IF EXISTS charlie_finding_workflow_consistency,
    DROP CONSTRAINT IF EXISTS charlie_finding_workflow_state,
    DROP COLUMN IF EXISTS workflow_state;

UPDATE global_roles
SET rules = (
    SELECT COALESCE(jsonb_agg(rule), '[]'::jsonb)
    FROM jsonb_array_elements(rules) AS rule
    WHERE NOT (rule->>'resource' = 'charlie' AND rule->'verbs' = '["update"]'::jsonb)
), updated_at = now()
WHERE name IN ('Platform Operator', 'Charlie Approver');
