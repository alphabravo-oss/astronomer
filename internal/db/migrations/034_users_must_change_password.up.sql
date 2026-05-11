-- Rancher-style first-login flow.
--
-- A user with `must_change_password = true` is forced through a password
-- change before the dashboard will let them anywhere else. Set to true when
-- the server auto-creates the bootstrap admin on a fresh install (see
-- internal/server/ensure_admin.go); cleared after a successful change.

ALTER TABLE users
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;
