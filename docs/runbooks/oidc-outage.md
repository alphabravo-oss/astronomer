# Runbook — OIDC / SSO outage

**Severity**: warning to page (depends on whether local admin works)
**Component**: `internal/auth/oidc_discovery`, `internal/handler/sso.go`

## Symptoms

- Users can't log in via SSO (Okta / Azure AD / Google Workspace / etc.)
- "Authentication failed" or "Invalid issuer" errors on login redirect
- Logs show OIDC discovery (`/.well-known/openid-configuration`) failing

## Triage

1. **Is the provider up?**
   ```bash
   curl -fsS "$OIDC_ISSUER/.well-known/openid-configuration" | jq '.issuer'
   ```
   Check the provider's status page. If they're down, all you can do
   is fall back to local admin.

2. **Discovery cache stale?** OIDC discovery is cached for 10 minutes
   (see `internal/auth/oidc_discovery.go` `defaultDiscoveryTTL`).
   Recent provider config change (new signing key, new redirect URI)
   won't be picked up until the TTL expires.

3. **Clock skew?** JWT validation rejects tokens outside the issued-at
   / exp window. Check pod time vs NTP:
   ```bash
   kubectl -n astronomer exec deploy/astronomer-server -- date -u
   ```

## Recovery

### Force a fresh discovery fetch

Restart server pods so the OIDC discovery cache resets:
```bash
kubectl -n astronomer rollout restart deploy/astronomer-server
```

### Fall back to local admin

The bootstrap admin user (from `ASTRONOMER_BOOTSTRAP_PASSWORD` or the
randomly-generated one on first boot) is independent of SSO. Log in
with that account, fix the SSO config in
`platform_configuration.sso_providers` via the UI, then save.

Live env credentials live in
[project_live_env.md](../../../.claude/memory/project_live_env.md) for
the dev environment; production should retrieve from a secret store.

### Repair stuck state

If the SSO provider config in the DB is broken (bad client_secret, bad
issuer URL):
```sql
-- Inspect (Encryptor-encrypted JSON):
SELECT id, type, configuration FROM auth_providers ORDER BY priority;
-- Disable via UI (or set enabled=false here as a hotfix)
UPDATE auth_providers SET enabled = false WHERE id = '<id>';
```

## Verify

- `curl -fsS "$OIDC_ISSUER/.well-known/openid-configuration"` works
- A test login redirects, returns to the app, issues a JWT
- `astronomer_auth_login_total{provider="oidc",status="success"}` rate
  resumes

## Prevention

- Add a synthetic test that runs the full OIDC handshake against
  the provider every few minutes
- Watch the SSO provider's status page subscription
- Document the local-admin fallback in onboarding so on-call doesn't
  scramble for it during an outage

## Single sign-out (SLO) — migration 054

The dashboard's Logout endpoint (`POST /api/v1/auth/logout/`) drives
RP-initiated logout against the upstream IdP for users who logged in
via OIDC. The response includes a `redirect_url` that the SPA follows
with a top-level navigation; the IdP tears down its session and
bounces back to `/api/v1/auth/logout-done/`.

### What SLO covers

| Flow | Local JWT revoked | Upstream session torn down |
|------|------------------|----------------------------|
| OIDC with `end_session_endpoint` advertised | yes | yes (RP-initiated redirect) |
| OIDC without `end_session_endpoint` | yes | no — IdP doesn't support RP-init logout |
| Dex with OIDC connector (Okta-OIDC, Auth0, Google, etc.) | yes | yes (Dex clears its own session + relies on connector's back-channel) |
| Dex with SAML connector | yes | **Dex session yes; upstream IdP no** — Dex does NOT support SAML SLO natively |
| Dex with LDAP / static-password connector | yes | yes (no upstream IdP to log out of) |
| GitHub / Google built-in providers | yes | no — those providers don't expose an end-session API |
| Local-password users | yes | n/a — no upstream session |
| Admin force-logout | yes (per-user cutoff) | best-effort upstream POST (`backchannel_*` metrics) |

The compliance gap with SAML connectors behind Dex is **documented and
expected**: SAML SLO requires the IdP to support `SingleLogoutService`
bindings, Dex doesn't implement that, and there is no path through the
RP that can bridge it. The Astronomer JWT cutoff means the user can't
do anything in the dashboard after force-logout, and Dex's own cookie
is cleared, but the upstream IdP cookie (e.g. Okta SAML) survives
until its natural expiry. Operators using SAML for SOC 2-grade SLO
should switch to OIDC connectors where possible.

### Triage when SLO appears broken

1. **Logout redirect not happening**
   ```bash
   curl -X POST -H "Authorization: Bearer <jwt>" \
     "$SERVER/api/v1/auth/logout/" | jq .
   ```
   `redirect_url` should be present for OIDC-originated sessions. If it
   isn't, check:
   - `auth.SetEncryptor` is wired (server boot requires
     `ASTRONOMER_ENCRYPTION_KEY` — see `secret-rotation-runbook.md`).
   - The user's `sso_sessions` row exists for the JWT's JTI:
     ```sql
     SELECT jti, provider_name, end_session_endpoint
     FROM sso_sessions WHERE user_id = '<uuid>';
     ```
   - The IdP advertises `end_session_endpoint` in its discovery doc.

2. **Metric to watch**: `astronomer_auth_sso_logouts_total{outcome=...}`
   - `redirected` — happy path
   - `no_endpoint` — IdP doesn't support RP-init logout
   - `no_session` — local-password user, or row already cleaned up
   - `encrypt_error` — decrypt failed (key rotation mid-flight?)
   - `backchannel_ok` / `backchannel_failed` — admin force-logout
     fan-out outcomes

3. **`sso_sessions` table is growing**
   The retention worker GCs rows where `expires_at < now()` on the
   same daily cron as `jwt_revocations`. If the table is growing
   monotonically, check the worker logs for
   `purge expired sso sessions failed` warnings.

### Deferred work

RP-initiated logout (this design) is the user-driven redirect flow.
The OIDC spec also defines **back-channel logout** via
`backchannel_logout_uri` — the IdP POSTs a logout token to the RP
when ANY session for the user ends, anywhere. That's a different
direction of integration and isn't wired today; the admin
force-logout path fires upstream POSTs in the opposite direction
(RP → IdP) for the same effect on a per-user basis.

## Related

- `internal/auth/oidc_discovery.go` — cached discovery client
- `internal/handler/sso.go` — login flow + sso_sessions persistence
- `internal/handler/auth.go` — Logout / LogoutDone, end_session URL
- `internal/handler/sso_backchannel.go` — admin force-logout POSTer
- `internal/db/migrations/054_sso_sessions.up.sql` — table schema
- `docs/oncall-onboarding.md` — local-admin fallback procedure
