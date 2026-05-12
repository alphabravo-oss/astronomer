-- Single sign-out (SLO) session tracking — migration 054.
--
-- The Astronomer JWT is revoked locally on Logout (migration 039); this
-- table additionally holds the upstream id_token + cached
-- end_session_endpoint so the Logout HTTP response can drive
-- RP-initiated logout at Dex / the upstream IdP.
--
-- Lifetime is bounded by expires_at (= the Astronomer JWT's exp). The
-- nightly retention worker piggy-backs on the jwt_revocations purge.

-- name: InsertSSOSession :exec
-- Called by the SSO Callback after the Astronomer JWT pair is minted.
-- jti is the access JWT's JTI; upstream_id_token_encrypted is Fernet-
-- ciphertext (the caller wraps before calling). ON CONFLICT replaces
-- the row so a fresh login re-uses the JTI key (which it shouldn't —
-- JTIs are uuid.New per token — but the upsert protects against a
-- pathological repeat without an extra round-trip).
INSERT INTO sso_sessions (
    jti, user_id, provider_name, upstream_id_token_encrypted,
    end_session_endpoint, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (jti) DO UPDATE SET
    user_id                     = EXCLUDED.user_id,
    provider_name               = EXCLUDED.provider_name,
    upstream_id_token_encrypted = EXCLUDED.upstream_id_token_encrypted,
    end_session_endpoint        = EXCLUDED.end_session_endpoint,
    expires_at                  = EXCLUDED.expires_at;

-- name: GetSSOSession :one
-- Lookup the upstream session row for an Astronomer JWT's JTI. Used by
-- Logout to mint the end-session redirect URL. Returns sql.ErrNoRows
-- when the user logged in via local password (no upstream session) —
-- the handler treats that as "no redirect_url in the response".
SELECT * FROM sso_sessions WHERE jti = $1;

-- name: ListSSOSessionsByUser :many
-- Used by admin force-logout to enumerate the user's active upstream
-- sessions for back-channel logout. Ordered by created_at DESC so the
-- most-recent device fires first (best-effort relevance for the
-- operator-facing audit row that records how many were torn down).
SELECT * FROM sso_sessions WHERE user_id = $1 ORDER BY created_at DESC;

-- name: DeleteSSOSession :exec
-- Drops a single row by JTI. Called by Logout after the end-session
-- redirect URL is built, so the upstream session can't be redirected
-- twice (which some IdPs treat as a CSRF attempt).
DELETE FROM sso_sessions WHERE jti = $1;

-- name: DeleteSSOSessionsByUser :exec
-- Used by admin force-logout to clear every row for a user after
-- firing the back-channel end-session POSTs. Matches the semantics of
-- InvalidateAllTokens: the user is logged out everywhere.
DELETE FROM sso_sessions WHERE user_id = $1;

-- name: PurgeExpiredSSOSessions :execrows
-- Called by the same nightly retention task that GCs jwt_revocations.
-- Bounded by the JWT's natural expiry — once the JWT is unusable, the
-- row's id_token_hint is moot too.
DELETE FROM sso_sessions WHERE expires_at < now();
