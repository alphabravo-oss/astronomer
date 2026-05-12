# Runbook — License expiry

**Severity**: warning (T-30d), urgent (T-7d), page (T-0)
**Component**: license validation in `internal/auth` or feature gates

## Symptoms

- UI banner: "Your license expires in N days" / "License expired"
- Audit log entries `license.check.failed`
- Feature gates flip to denied for licensed-only capabilities

> **Note**: Astronomer-go does not currently ship paid-tier license
> gating; this runbook is forward-looking. If a license module is added
> later, update this doc with the actual code paths.

## Triage

1. **Confirm the dates** — pull the license blob from
   `platform_configuration.license` (or wherever the licensing module
   stores it) and inspect `not_before` / `not_after`.

2. **Was a renewal issued but not applied?** Customer-side billing
   portal may show "Renewed" while the app holds the stale blob.

3. **System clock correct?** Same check as
   [oidc-outage.md](oidc-outage.md) — wall-clock drift can simulate
   expiry.

## Recovery

### Apply a renewed license

Upload the new license JWT / blob via the admin UI or:
```bash
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "$(cat new-license.json)" \
  "https://${HOST}/api/v1/admin/license/"
```
(Endpoint shape depends on the licensing module; update this runbook
when the module lands.)

### Emergency grace period

If a license module supports a grace mode (typical: read-only +
existing features keep working, no new clusters can be onboarded), set
that explicitly while you wait for the renewed license.

## Verify

- License banner shows new expiry date far in the future
- Licensed features re-enabled in the UI
- No `license.check.failed` audit rows in the last 5 minutes

## Prevention

- Renewal reminder cron at T-60d / T-30d / T-14d / T-7d
- Document the renewal contact + procedure separately from this runbook

## Related

- `docs/api-stability.md` — API contract policy
