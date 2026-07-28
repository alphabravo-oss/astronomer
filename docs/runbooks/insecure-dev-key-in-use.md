# AstronomerInsecureDevKeyInUse

`astronomer_insecure_dev_key_in_use{key="secret_key"}` or `{key="encryption_key"}`
is 1. The install is running on a key value that is **published in the Astronomer
repository**. Treat it as a credential compromise, not a config nit.

## Symptoms

- The alert fires with `key="secret_key"`: the HMAC key that signs every access,
  refresh, and purpose token is public. Anyone can mint
  `{user_id: <any admin uuid>, token_type: "access"}` and sign it; revocation
  does not help because a forged token carries whatever `jti`/`iat` the attacker
  chooses.
- The alert fires with `key="encryption_key"`: the Fernet key that wraps every
  stored credential is public — cluster kubeconfigs, agent tokens, git PATs,
  registry passwords, cloud credentials, SSO client secrets.
- The server and worker logs carry an ERROR at boot:
  `insecure development key in use; ...`.
- `GET /api/v1/admin/key-status/` (superuser) reports the same key names under
  `insecure_dev_keys`.

Older installs reached this state without doing anything wrong: the chart used
to ship both values as its defaults and only rejected them under
`config.env=production`. The chart now ships no key material at all and the
render fails until real keys are supplied.

## Triage

1. Confirm which key(s), and on which processes:

   ```
   kubectl -n <release-namespace> logs -l app.kubernetes.io/component=server \
     --tail=200 | grep insecure_dev_key
   kubectl -n <release-namespace> get secret <release>-secrets \
     -o jsonpath='{.data.SECRET_KEY}' | base64 -d
   ```

2. Decide the blast radius. If `secret_key` was public and the management plane
   was reachable from an untrusted network, assume tokens were forgeable for the
   whole exposure window: review `audit_log` for logins and privileged writes
   you cannot attribute.
3. If `encryption_key` was public, assume every credential the platform stores
   is disclosed. They must be rotated at their source (cluster, git host,
   registry, cloud, IdP), not just re-encrypted.

## Recovery

1. Rotate both keys with the multi-key procedure in
   [`../secret-rotation-runbook.md`](../secret-rotation-runbook.md) — add the new
   key as primary, keep the old one for the validation/decrypt window, run
   `cmd/keyrotate` for the Fernet columns, then drop the old key. Do not swap a
   key in place: dropping the old Fernet key before re-encryption makes every
   wrapped column undecryptable.
2. Invalidate outstanding sessions once the new signing key is primary (per-user
   invalidation cutoff / forced re-login), so any token minted under the public
   key is dead rather than merely expiring.
3. Rotate the downstream credentials disclosed by a public `encryption_key`.
4. Reinstall/upgrade with your own key material — the chart has no defaults:

   ```
   openssl rand -base64 32 > ./jwt-key
   python -c "from cryptography.fernet import Fernet; print(Fernet.generate_key().decode())" > ./fernet-key
   helm upgrade astronomer ./deploy/chart \
     --set-file secrets.secretKey=./jwt-key \
     --set-file secrets.encryptionKey=./fernet-key
   ```

   Or pre-create the Secret and set `secrets.existingSecret`, which is the
   preferred posture when an external secret manager owns the values.

### Local dev (`make helm-install`, `scripts/k3d-bootstrap.sh`)

Both paths default to their own published laptop values, so the alert is
expected there and the recovery above is overkill — but the Fernet key they pass
is **not** the one the chart used to default to. Re-running either against a
cluster bootstrapped before the chart defaults were removed swaps the key under
the existing Postgres PVC, and every credential stored by the old install stops
decrypting with no error at install time. The old value cannot be re-supplied
(the chart rejects it by design), so start clean instead of upgrading over it:
drop the release namespace together with its Postgres PVC, or
`k3d cluster delete <name>` and re-bootstrap.

## Verify

- `astronomer_insecure_dev_key_in_use` reads 0 for both `key` label values on
  every server and worker pod (the gauge is re-published on every boot).
- No `insecure development key in use` ERROR in the logs after the rollout.
- `GET /api/v1/admin/key-status/` returns `"insecure_dev_keys": []` and the
  expected `encryption_keys` / `jwt_keys` counts for the rotation stage.

## See also

- Metric: `internal/observability/insecure_key_metrics.go`
- Detection: `config.DevSentinelsInUse` (`internal/config/production.go`)
- Chart guard: `astronomer.requireSecretMaterial`
  (`deploy/chart/templates/_helpers.tpl`)
- [`../management-plane-dr-runbook.md`](../management-plane-dr-runbook.md) — key
  custody and restore implications
