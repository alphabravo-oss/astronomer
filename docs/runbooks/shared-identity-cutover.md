# Local authentication and optional shared Identity

Local password login, MFA, recovery, password policy, lockout and API tokens remain available in both supported modes. `identity.mode: local` is the default and has no provider, client-secret or CA dependency. `identity.mode: both` adds one trusted OIDC issuer through `github.com/alphabravo-oss/identity/pkg/rp` v0.1.0. SSO-only mode is rejected.

The issuer can be an independently operated AlphaBravo Identity deployment or a customer's OIDC service. This chart does not install an identity server. Provider HA, backups, telemetry, federation and offline artifacts belong to that separately operated service.

## Configure optional SSO

Register a confidential client with authorization-code and PKCE S256 support. Register the exact public callback `https://<astronomer-host>/api/v1/auth/callback/identity/`. The provider must issue signed JWT access tokens with the configured issuer and client audience; opaque access tokens are unsupported. Set:

```yaml
identity:
  mode: both
  issuer: https://identity.example.com
  clientID: astronomer
  clientSecretRef:
    name: astronomer-identity-client
    key: client-secret
  jwksURL: https://identity.example.com/keys
  authorizationURL: https://identity.example.com/authorize
  tokenURL: https://identity.example.com/token
  redirectURI: https://astronomer.example.com/api/v1/auth/callback/identity/
  ca:
    existingSecret: "" # Optional PEM bundle Secret; key defaults to ca.crt.
networkPolicy:
  identityProviderEgressCIDRs: [] # Allow the issuer/JWKS/token destinations on TCP 443.
```

Provision the client-secret Secret through the approved secret store. Credentials are referenced only by server pods and never copied into Helm values or a shared ConfigMap. HTTPS is required. A private CA augments system trust; verification is never disabled. Existing general egress rules also apply, so review them when restricting provider access. Nonstandard HTTPS ports require an operator-managed NetworkPolicy.

Use Authentication settings to link an **existing local account** to the exact issuer subject. The authenticated administrator needs `sso:create` to link, `sso:delete` to unlink and `sso:read` to list; API tokens also need the admin scope. Links and audit events commit in one transaction. Email matching, automatic account creation, external directory discovery and implicit privilege assignment are not supported. An existing binding cannot silently transfer to another account. Subject links are issuer-specific.

## Session behavior and limits

The shared library handles PKCE, state and provider JWT verification. The callback consumes the provider token on the server and issues an Astronomer access cookie bounded by both product session lifetime and provider token expiry. Provider access/refresh credentials are not sent to the browser. SSO sessions have no product refresh credential; expiry requires another authorization-code flow.

Every identity-session validation rechecks the issuer/subject binding, active account and revocation cutoff, including streaming checks. Unlinking or disabling the account denies subsequent validation. Astronomer RBAC is authoritative; upstream role claims do not create roles or grant administrator access. Existing SCIM/group-mapping facilities are separate; shared OIDC login does not synchronize provider groups. Upstream role removal alone therefore does not alter local RBAC.

Logout revokes the Astronomer session. This integration does **not** terminate the provider session or sessions in other products, and does not accept upstream back-channel logout. LDAP/SAML may be handled by an independently qualified upstream provider; Astronomer exposes OIDC only. Cross-product provisioning, federation and global logout remain separate acceptance work.

## Upgrade from Dex: ordered cutover

Migration 87 is forward-only and drops the retired broker tables. Do not roll old and new application versions concurrently across it.

1. Record the exact candidate images/chart, previous Helm revision, release values, workload ownership and schema version. Verify the candidate's signatures and release compatibility before production use.
2. Take and verify a management database backup plus the previous release's recovery assets. Export old provider/user-link mappings and broker operation state into protected operator storage. Never put client secrets or connector credentials in public evidence.
3. Verify at least two authorized local administrators can authenticate with the required MFA/recovery procedures. For external-only historical users, establish a local account recovery/password path before retiring their provider. Preserve their existing user IDs and review their retained role assignments.
4. Prepare the optional replacement issuer/client if desired. Record each explicitly approved mapping from the new issuer's exact subject to the existing Astronomer user ID. Old broker subjects cannot be inferred at the new issuer. Local-only is a valid cutover target.
5. Enter a maintenance window. Stop old server replicas and drain/stop old workers, including retries and scheduled broker jobs. Remove only the retired `dex:apply_operation` and `dex:recover_operations` jobs from the approved queue inventory; never flush a shared queue. Capture completion/ownership evidence before proceeding.
6. Apply the candidate schema/chart with the old processes stopped. Migration 86 creates `shared_identity_bindings`; migration 87 removes `external_principals`, `dex_operations`, `dex_connectors`, `dex_settings`, the generation function and obsolete JSON-schema entries. It removes the Dex provider/session/tool registration records. Local users, password hashes, MFA/recovery and role bindings are preserved. Accounts linked to the retired broker have their existing access and refresh sessions revoked and must sign in again; unrelated local accounts are unaffected. Existing shared bindings are preserved; old subjects are not automatically relinked.
7. Verify the old chart-owned broker Deployment, Service, routes, policies and RBAC are absent. Helm-retained Secrets/ConfigMaps and separately installed broker releases may survive a template removal: inventory their owner/release labels, retain recovery material securely, and delete only resources confirmed to belong to the retired broker. Do not use namespace-wide cleanup or delete an unrelated identity provider.
8. Establish approved subject bindings through Authentication settings. Test local password+MFA, SSO, unlink/revocation, least-privilege access, outage fallback and logs/audit. Confirm the application schema is 88 and no broker task is scheduled or running. Scan rendered manifests and image inventory for Dex before closing the cutover.
9. If verification fails after migration 87, stop the new processes and restore the verified pre-cutover database/release as one recovery unit. An old binary against schema 87 is not a rollback. Account for post-cutover writes before restoring; retain incident evidence.

No live cutover or deletion is implied by source tests. Record the deployed artifact identities and actual resource cleanup separately.

## Catalog provisioning implementation status

The provisioning adapter in `internal/identity/provisioning` implements the shared
`pkg/provision.SecretStore` contract with immutable Kubernetes Secrets whose
contents are additionally encrypted with the product keyring. Authenticated
payloads bind issuer, product project, installation, generation and namespace.
Repeating the same write is safe; a different credential or copied ciphertext is
rejected. The keyring and these Secrets must be backed up together. This storage
format is an internal authority record, not a Secret that a catalog application
can consume directly.

Registration, recovery, rotation and cleanup must take `WithRegistrationLock` for
the installation. All generations share one PostgreSQL transaction lock. The
callback has a two-minute deadline and must honor cancellation. This prevents
ordinary concurrent work; it does not make an upstream mutation atomic with
Kubernetes or PostgreSQL. A durable operation intent and recovery state are still
required before enabling the workflow.

On shared-library `Ensure` persistence failure, recover its returned create-only
credential by retrying `Put`; do not call `Ensure` again hoping to recover the
secret. A crash between upstream creation and persistence is ambiguous. Stop
automatic retries until an explicit recovery operation resolves it. Never log
the returned result. Never delete credential material to roll back a timed-out
write: that write may have succeeded.

Credential cleanup checks authenticated ownership and Kubernetes UID/resource
version preconditions. Invoke it only after upstream revocation and consumer
retirement have been verified. Rotation uses a new immutable generation, retains
the upstream project and its grants, and keeps the old generation until consumer
cutover succeeds. The workflow, upstream rotation/deletion capabilities and
per-product Secret delivery are not wired yet; catalog SSO remains unqualified.

Migration 88 adds `identity_provisioning_operations`; current candidate chart and
release schema targets are 88. Migration 87 remains the destructive broker
retirement boundary. The new table contains scope, desired-spec digest, actor,
correlation, revision, state, upstream identifiers and a Secret reference, never
the client secret. Records survive catalog deletion to preserve recovery and
cleanup provenance.

The journal service verifies the current installation/project/cluster relation
when preparing intent and rechecks it when claiming pending work. It atomically
records each transition with an audit outbox event; audit failure rolls back the
transition. Preparation is idempotent for the same scope and immutable intent.
Claiming is a revision compare-and-swap that must commit before upstream creation.
An interrupted claim cannot be claimed again. Under the installation lock, move
it to `recovery_required`; this advances its revision and rejects late completion
from the previous worker. Completion requires an authenticated read of the
correct encrypted credential Secret and the current revision.

This journal is not yet wired to the catalog worker or an administrator recovery
API. Callers still need project/cluster authorization and desired-spec verification
before invoking it. It does not resolve a secret lost before persistence or revoke
an upstream application. Those cases remain explicit recovery/lifecycle work.
