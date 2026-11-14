# OIDC / SSO outage

Local authentication is always available. An unavailable issuer must not become a reason to disable password policy, MFA or authorization.

1. Sign in through the normal password form using an approved local administrator account and its MFA/recovery procedure. Obtain credentials from the approved secret store.
2. Check the configured `identity.issuer`, `jwksURL`, `authorizationURL`, `tokenURL`, client-secret reference and exact callback URI. Inspect server events, provider health, DNS, TCP 443 egress, certificate trust and clock synchronization without logging tokens or secrets.
3. New SSO attempts require provider connectivity. The relying-party initializer is lazy, bounded to five seconds and retries after a short failure cooldown. Local login does not initialize it. Existing product access sessions remain bounded by their expiry and require current local account/binding/revocation checks.
4. Fix the deployment configuration or Secret and restart server pods through the normal rollout. Changing `identity.mode` to `local` disables existing identity sessions as well as new SSO attempts; it leaves local accounts and API tokens available.
5. Verify a full code+PKCE login, a local password+MFA login, rejected unlinked subjects and audited logout. Keep the incident open if only the provider health endpoint is green.

Astronomer logout revokes its local session. It does not terminate the upstream provider session or sessions in other products. Provider role changes are not imported automatically into Astronomer RBAC. Use local account disablement, explicit binding removal or the existing session-revocation controls when product access must end immediately.

Configuration, cutover ordering and recovery are documented in [Shared Identity cutover](shared-identity-cutover.md). Cross-product SSO, global logout and provider federation require separate qualification.
