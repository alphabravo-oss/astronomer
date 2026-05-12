-- API Tokens

-- name: GetAPITokenByID :one
SELECT * FROM api_tokens WHERE id = $1;

-- name: GetTokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = $1 AND is_revoked = false;

-- name: ListAPITokens :many
SELECT * FROM api_tokens ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListTokensByUser :many
SELECT * FROM api_tokens WHERE user_id = $1 AND is_revoked = false ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllTokensByUser :many
SELECT * FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash, prefix, expires_at, scopes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: RevokeAPIToken :exec
UPDATE api_tokens SET is_revoked = true WHERE id = $1;

-- name: UpdateAPITokenLastUsed :exec
UPDATE api_tokens SET last_used_at = now() WHERE id = $1;

-- name: DeleteAPIToken :exec
DELETE FROM api_tokens WHERE id = $1;

-- name: CountAPITokens :one
SELECT count(*) FROM api_tokens;

-- name: CountTokensByUser :one
SELECT count(*) FROM api_tokens WHERE user_id = $1 AND is_revoked = false;

-- Account lockout (NIST 800-53 AC-7).
--
-- The Login handler increments on every bcrypt miss, locks the account
-- when the running count exceeds the chart-tuned threshold, and resets
-- on a successful auth. Auto-unlock is implicit: a locked_until in the
-- past behaves like "not locked".

-- name: IncrementFailedLoginCount :exec
UPDATE users
SET failed_login_count = failed_login_count + 1,
    failed_login_at    = $2,
    updated_at         = now()
WHERE id = $1;

-- name: ResetFailedLoginCount :exec
-- Called on a successful login. Also clears any expired lock so the next
-- failed-attempt cycle starts from a clean state.
UPDATE users
SET failed_login_count = 0,
    failed_login_at    = NULL,
    locked_until       = NULL,
    locked_reason      = '',
    updated_at         = now()
WHERE id = $1;

-- name: LockUser :exec
UPDATE users
SET locked_until  = $2,
    locked_reason = $3,
    updated_at    = now()
WHERE id = $1;

-- name: UnlockUser :exec
UPDATE users
SET failed_login_count = 0,
    failed_login_at    = NULL,
    locked_until       = NULL,
    locked_reason      = '',
    updated_at         = now()
WHERE id = $1;

-- JWT session revocation.
--
-- Two layers:
--   1. Per-JTI deny list. Used by Logout. ON CONFLICT lets the same JTI
--      be submitted multiple times (idempotent).
--   2. Per-user `tokens_invalidated_at` cutoff. Used by admin force-
--      logout to invalidate ALL active tokens for a user without having
--      to enumerate them — the JWT validator rejects any token whose
--      iat predates the cutoff.

-- name: RevokeJWT :exec
INSERT INTO jwt_revocations (jti, user_id, expires_at, reason)
VALUES ($1, $2, $3, $4)
ON CONFLICT (jti) DO NOTHING;

-- name: IsJWTRevoked :one
SELECT EXISTS (SELECT 1 FROM jwt_revocations WHERE jti = $1) AS revoked;

-- name: InvalidateAllTokens :exec
UPDATE users
SET tokens_invalidated_at = $2,
    updated_at            = now()
WHERE id = $1;

-- name: PurgeExpiredJWTRevocations :execrows
-- Called by the nightly retention worker so the deny list doesn't grow
-- without bound. Returning the rowcount lets the worker emit it as a
-- metric.
DELETE FROM jwt_revocations WHERE expires_at < now();

-- SSO Configurations

-- name: GetSSOConfigurationByID :one
SELECT * FROM sso_configurations WHERE id = $1;

-- name: GetSSOConfigurationByProvider :one
SELECT * FROM sso_configurations WHERE provider = $1;

-- name: ListSSOConfigurations :many
SELECT * FROM sso_configurations ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: GetEnabledSSOProviders :many
SELECT * FROM sso_configurations WHERE is_enabled = true ORDER BY provider ASC;

-- name: CreateSSOConfiguration :one
INSERT INTO sso_configurations (provider, is_enabled, display_name, config, client_id, client_secret_encrypted, allowed_organizations, allowed_domains, auto_create_users, default_global_role_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateSSOConfiguration :one
UPDATE sso_configurations SET
    is_enabled = $2,
    display_name = $3,
    config = $4,
    client_id = $5,
    client_secret_encrypted = $6,
    allowed_organizations = $7,
    allowed_domains = $8,
    auto_create_users = $9,
    default_global_role_id = $10
WHERE id = $1
RETURNING *;

-- name: DeleteSSOConfiguration :exec
DELETE FROM sso_configurations WHERE id = $1;

-- name: CountSSOConfigurations :one
SELECT count(*) FROM sso_configurations;
