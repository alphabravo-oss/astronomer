-- Tool drift reconciliation sweep (P1 item 16/22).
--
-- ArgoCD applications have built-in drift detection; helm-installed tools
-- did not. The periodic tool-drift sweep compares the desired state stored
-- on installed_charts (status/revision) against the live helm release and
-- stamps these columns so the catalog UI can surface a drift badge.
ALTER TABLE installed_charts
    ADD COLUMN drift_detected   BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN drift_detail     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN drift_checked_at TIMESTAMPTZ;
