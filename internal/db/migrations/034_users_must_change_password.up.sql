-- Optional forced password-rotation flow.
--
-- A user with `must_change_password = true` is forced through a password
-- change before the dashboard will let them anywhere else. The bootstrap
-- admin is created with the default false value; this flag is for explicit
-- admin/operator-driven rotations and is cleared after a successful change.

ALTER TABLE users
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;
