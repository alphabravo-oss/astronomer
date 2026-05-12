# Runbook — cert-manager renewal stuck

**Severity**: warning (cert expiring < 7d) → page (cert expired)
**Component**: cert-manager + Gateway API TLS

## Symptoms

- TLS handshake errors at the gateway
- Browser warns about expired certificate
- `kubectl get certificate -n astronomer` shows `READY=False` or
  `NotAfter` < 7d in the future without a new request issued
- cert-manager controller logs include ACME rate-limit, DNS-01 failure,
  or HTTP-01 challenge-not-reachable

## Triage

1. **What state is the Certificate in?**
   ```bash
   kubectl -n astronomer get certificate
   kubectl -n astronomer describe certificate astronomer-tls
   ```
2. **CertificateRequest progress?**
   ```bash
   kubectl -n astronomer get certificaterequest
   kubectl -n astronomer describe certificaterequest <latest>
   ```
3. **Order + Challenge state (ACME)?**
   ```bash
   kubectl -n astronomer get order,challenge
   kubectl -n astronomer describe challenge <name>
   ```
   Common failure modes:
   - HTTP-01: the well-known path isn't reachable (gateway misroute)
   - DNS-01: missing DNS provider creds OR slow propagation
   - ACME rate limit: too many cert issuances against the same domain

## Recovery

### Force a fresh issuance

```bash
kubectl -n astronomer delete certificaterequest --all
kubectl -n astronomer annotate certificate astronomer-tls \
  cert-manager.io/issue-temporary-certificate=true --overwrite
# cert-manager creates a new CertificateRequest on the next reconcile
```

### Use a manually-uploaded Secret while you debug

The chart supports `tls.mode=secret` with `tls.secret.name`:
```yaml
tls:
  mode: secret
  secret:
    name: astronomer-tls-manual
```
Create the Secret with `tls.crt` + `tls.key`, helm upgrade, and the
gateway uses it directly while you fix cert-manager.

### Bypass ACME rate-limit

Switch the Issuer from `letsencrypt-prod` to `letsencrypt-staging` for
testing, then back once you've confirmed the issuance path works.

## Verify

- `kubectl get certificate -n astronomer` → `READY=True`
- `openssl s_client -connect ${HOST}:443 -servername ${HOST} </dev/null 2>/dev/null | openssl x509 -noout -dates`
  shows `notAfter` ~90 days out
- Browser shows green padlock, no warning

## Prevention

- cert-manager renewal happens at 2/3 of cert lifetime by default —
  monitor the `certmanager_certificate_expiration_timestamp_seconds`
  metric and alert at < 7d
- Pre-stage DNS provider creds when bootstrapping a new install
- Keep `tls.mode=secret` available as a fallback

## Related

- `deploy/chart/templates/tls.yaml` — chart cert-manager wiring
- [secret-rotation-runbook.md](../secret-rotation-runbook.md) —
  full procedure for manual cert refresh
