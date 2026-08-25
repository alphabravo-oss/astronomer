-- Durable ownership and terminal-state arbitration for cis-operator report
-- ingestion. Existing running rows become immediately recoverable by the
-- tunnel-owned repair sweep after an upgrade.
ALTER TABLE public.security_scan_results
    ADD COLUMN poll_generation bigint NOT NULL DEFAULT 1,
    ADD COLUMN poll_attempt integer NOT NULL DEFAULT 0,
    ADD COLUMN next_poll_at timestamptz,
    ADD COLUMN poll_deadline timestamptz,
    ADD COLUMN poll_owner text NOT NULL DEFAULT '',
    ADD COLUMN poll_lease_expires_at timestamptz,
    ADD COLUMN upstream_report_name text NOT NULL DEFAULT '',
    ADD COLUMN terminal_reason text NOT NULL DEFAULT '',
    ADD COLUMN cancel_requested_at timestamptz,
    ADD CONSTRAINT security_scan_results_poll_generation_valid CHECK (poll_generation > 0),
    ADD CONSTRAINT security_scan_results_poll_attempt_valid CHECK (poll_attempt >= 0);

UPDATE public.security_scan_results
SET next_poll_at = now(),
    poll_deadline = started_at + interval '35 minutes'
WHERE status IN ('pending', 'running', 'in_progress');

CREATE INDEX security_scan_results_poll_due_idx
    ON public.security_scan_results (next_poll_at, poll_lease_expires_at)
    WHERE status IN ('pending', 'running', 'in_progress')
      AND cancel_requested_at IS NULL;
