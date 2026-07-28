-- Give a chart repository somewhere to record why its last sync failed (P1).
--
-- The `catalog:sync` sweep (@every 6h) returned on the FIRST repository error,
-- so one unreachable or misconfigured repo silently froze the catalog for
-- every repo after it in the list. It is being changed to isolate failures per
-- repository and carry on — but "carry on" is only an improvement if the
-- failure lands somewhere an operator can see it. Without these columns the
-- only trace of a repository that has not refreshed in weeks is a worker log
-- line, and `last_synced_at` alone cannot distinguish "never configured to
-- sync" from "has been failing since Tuesday".
--
-- The shape is the one ~20 other tables in this schema already use for exactly
-- this (`last_error TEXT NOT NULL DEFAULT ''` — see 038 cluster_decommission,
-- 053 cloud_credentials, 056 fleet_operations, 070 apiserver_allowlist), so
-- the API and UI treat it the way they treat every other per-resource sync
-- error. It is deliberately NOT a new operations queue: a repository sync has
-- no lifecycle to track, just a most-recent outcome.
--
-- last_sync_attempted_at is separate from last_synced_at on purpose.
-- last_synced_at keeps its existing meaning — the last time an ingest actually
-- succeeded, which is what the catalog's freshness is measured against — and
-- must not be advanced by a failed attempt, or a permanently broken repo would
-- look permanently fresh. last_sync_attempted_at answers the other question,
-- "is the sweep still reaching this row at all", which is how you tell a
-- failing repo from one the sweep has stopped visiting.
--
-- ── What this deliberately does NOT do ─────────────────────────────────────
--
-- It is additive DDL only: two nullable/defaulted columns, ADD COLUMN IF NOT
-- EXISTS, no UPDATE of any existing row. Every pre-existing repository starts
-- with last_sync_error = '' and last_sync_attempted_at = NULL, which reads as
-- "no failure recorded yet" — not as a fabricated success and not as a
-- fabricated failure. The first sweep after upgrade stamps the truth.
--
-- It does not backfill last_sync_attempted_at from last_synced_at. They are
-- different facts and copying one into the other would assert an attempt
-- history this database never observed.
--
-- It does not add a status enum ('ok'/'failed'/'syncing'). last_sync_error =
-- '' is the status; a third representation of the same fact is one more thing
-- to keep consistent, and there is no in-flight state to model because the
-- sweep is synchronous per repository.
--
-- It does not touch, re-type, or re-key auth_config. Chart-repository
-- credentials are stored as plaintext JSONB and have been since 001; wrapping
-- them in the Fernet encryptor is a real and separate finding, and doing it
-- here would silently invalidate every operator-authored credential in the
-- table on upgrade.

ALTER TABLE helm_repositories
    ADD COLUMN IF NOT EXISTS last_sync_error TEXT NOT NULL DEFAULT '';

ALTER TABLE helm_repositories
    ADD COLUMN IF NOT EXISTS last_sync_attempted_at TIMESTAMPTZ;
