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

## Related

- `internal/auth/oidc_discovery.go` — cached discovery client
- `internal/handler/sso.go` — login flow
- `docs/oncall-onboarding.md` — local-admin fallback procedure
