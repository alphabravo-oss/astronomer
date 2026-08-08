CREATE TABLE charlie_finding_decisions (
    request_id              UUID PRIMARY KEY,
    finding_id              UUID NOT NULL REFERENCES charlie_findings(id) ON DELETE CASCADE,
    actor_ref               VARCHAR(44) NOT NULL,
    decision                VARCHAR(32) NOT NULL,
    result_status           VARCHAR(16) NOT NULL,
    result_workflow_state   VARCHAR(32) NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_finding_decision_value CHECK (decision IN (
        'acknowledge', 'start_remediation', 'request_verification', 'dismiss', 'resolve'
    )),
    CONSTRAINT charlie_finding_decision_actor_ref CHECK (actor_ref ~ '^productuser_[a-f0-9]{32}$'),
    CONSTRAINT charlie_finding_decision_status CHECK (result_status IN (
        'open', 'acknowledged', 'dismissed', 'resolved', 'expired'
    )),
    CONSTRAINT charlie_finding_decision_workflow CHECK (result_workflow_state IN (
        'approval_pending', 'manual_remediation_required', 'remediation_in_progress',
        'verification_pending', 'resolved', 'rejected', 'dismissed', 'expired'
    ))
);

CREATE INDEX charlie_finding_decisions_finding_idx
    ON charlie_finding_decisions (finding_id, created_at DESC);
