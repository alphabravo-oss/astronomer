> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Identity plan — Zitadel as the AlphaBravo identity layer

**Decided 2026-10-20 by the owner.** Supersedes D-N (Dex as shared IdP) in `PLAN-UPDATE-tech-guide-and-execution.md`.
**Validated 2026-10-20 by the identity spike: GO with adjustments** (`exec-idp-spike.md`, artifacts in `idp-spike/`). This version incorporates all 11 adjustments.
**Goals:** one sign-in for Astronomer, Constellation and every catalog tool; lighter and cleaner than today's per-product auth stacks; local break-glass logins retained; efficient, optimized, clean code.

## Architecture

- **Zitadel** v4.19.x (Go, AGPL-3.0, OIDC-certified, Postgres) runs in-cluster as a subchart of the Astronomer chart (official chart 10.4.x, image tag overridden — the chart default lags) and is the default identity layer of the platform. Constellation's chart can point at it or at any external OIDC provider for standalone installs.
- **Customer identity providers** (Entra ID, Okta, Google, AD/LDAP, SAML, an existing Keycloak) federate into Zitadel once. Every app behind it gets single sign-on. Air-gapped installs work because nothing leaves the cluster.
- **Both products are plain OIDC relying parties.** Authentication lives in Zitadel; authorization stays in each product. Roles arrive in a compact `alphabravo_roles` claim emitted by a Zitadel Action; each product maps it to its own permissions. The claim name is configurable so the products also work directly against other providers.
- **Per-product tokens:** each product requests its own audience and token (no cross-project `…:project:id:<id>:aud` scopes in URLs). With an existing session the second product's silent sign-in takes ~69 ms.
- **Token lifetime 10 minutes** for access and ID tokens, so a revoked role is gone within 10 minutes.
- **Tenancy mapping:** Zitadel organization = customer; Zitadel project = Astronomer project / Constellation org.
- **Auto-provisioning (the differentiator):** when Astronomer installs Constellation or another catalog tool, it calls `zprov.Ensure(Spec{Org, Project, App, Roles, RedirectURIs, LoginBaseURI})` (spike code, official Go SDK, machine user with `IAM_ORG_MANAGER` and a JWT-profile key, in-cluster Service address). Creation takes ~110–130 ms; re-runs are no-ops with the same client id. **The client secret is returned only on creation**, so the installer stores it in the release Secret immediately. The tool arrives already SSO-wired.

## Zitadel is invisible (owner direction)

- Users never see the name, console, logo, emails or error pages of Zitadel. It is an internal engine.
- **Our products expose identity capabilities** in their own settings pages: sign-in methods, identity providers (OIDC, SAML, LDAP/AD, Entra, Google, GitHub…), MFA and passkey policy, users, roles, sessions. Those pages call a small internal identity package (`zprov.Configure{LDAP,OIDC,SAML}Provider` and siblings) that configures Zitadel through its APIs.
- **Sign-in is our own service**: a shared Go sign-in service on Zitadel's Session API v2 and OIDC auth-request API, rendered with the shared UI kit, branded per product by `client_id`. **Zitadel Login V2 is not deployed** (`login.enabled: false`, instance feature `loginV2 {required: true, baseUri: https://<domain>/<signin>}`) — it can't brand per product and measured p95 907 ms vs 303 ms for ours.
- **External-IdP group → role mapping** is done by our sign-in service on every sign-in, because Zitadel has no IdP group mapping. Removing a user from an upstream group removes the role on their next sign-in (proven with Keycloak).
- Password reset, verification and invitation emails use our templates and sender; Zitadel sends no email.
- **Exposure:** the Zitadel console and all of its APIs are never routed publicly. Only `/.well-known/openid-configuration`, `/oauth/v2`, `/oidc/v1` and `/idps` (upstream callbacks) are on the ingress; everything else returns 404 from outside. Products and the installer call the in-cluster Service.
- **Leaks normalized** (all found and fixed in the spike): cookie names (`UserAgentCookie.Name: ab.ua`, language and CSRF cookies renamed), TOTP issuer `AlphaBravo` in authenticator apps, instance and org names `AlphaBravo`, role claims replaced by `alphabravo_roles`, issuer on our own domain.

## Sign-in modes (owner direction 2026-10-21)

Each product supports three sign-in modes, chosen per install by the customer (chart values, editable by an admin in settings):

- **Local accounts:** a first-class normal login, not an emergency path. Users, roles, TOTP MFA (required by default), password policy with an offline breached-password list, rate limits and backoff, all managed in the product. Needs no identity layer, so it is the lightest install.
- **Single sign-on:** OIDC against the bundled AlphaBravo identity layer, or against the customer's own provider (Keycloak, Entra, Okta, LDAP/SAML through the identity layer).
- **Both** side by side.

An admin can turn local login off for an SSO-only policy. In that case one local admin flagged break-glass still works when the provider is down. A fresh install with local enabled bootstraps an initial admin.

**Constellation never depends on Astronomer to have a login.** Standalone it uses local accounts and/or its own bundled identity layer; when installed by Astronomer it can point at Astronomer's identity layer for cross-product SSO.

**Shared identity component:** Zitadel plus our sign-in service plus the provisioning library are packaged as one shared component that both product charts depend on. Its repo location is pending an owner decision (proposed: a new repo, `alphabravo-oss/identity`).

## Break-glass local logins (kept)

- Each product keeps exactly one local admin path, **disabled by default**, enabled only by a chart value or a Secret, for when Zitadel or the customer IdP is down.
- Local login: argon2id password, mandatory TOTP, short session TTL, rate-limited, every use emits a high-severity audit event and a Prometheus counter that alerts.
- Break-glass is a local account with a break-glass flag (see Sign-in modes). What gets deleted is the per-product SAML, LDAP, SCIM and identity-provider connector code, which the identity layer replaces. Local accounts themselves stay.
- With Zitadel at 0 replicas, already-issued tokens keep validating for the rest of their lifetime (≤ 10 min); recovery after scale-up takes ~3 s.

## Code to delete (cleanup)

- **Astronomer:** OAuth/OIDC connector management, SCIM server, group-sync connectors, password policy / lockout beyond break-glass, Dex templates and values, opaque/SSO logout plumbing that Zitadel now owns.
- **Constellation:** SAML and LDAP sign-in, multiple auth-server types, password policy / lockout beyond break-glass, the dead `internal/astronomer` JWKS adapter and its `"jwks-ready"` claim.
- Expected: several thousand lines of auth code per product removed; one small OIDC relying-party package + break-glass package each.

## Relying-party library (both products)

`github.com/golang-jwt/jwt/v5` + our own `jwks` package (97 lines, from `idp-spike/src/rp-bench/jwks/jwks.go`) + verify-once memoization of each token until expiry. `go-oidc` and `zitadel/oidc` measured 90–113 µs per check and are not used. The JWKS cache refetches only on an unknown `kid` or every 15 min, rate-limited, and **keeps the last good keys when a refresh fails** — that is what keeps the products running while Zitadel is down. The same code validates Keycloak tokens directly (bypass mode).

## Zitadel chart values (tested in the spike, see `idp-spike/values.yaml`)

- `image.tag: v4.19.5@sha256:…`; `replicaCount: 2`; `pdb: {enabled: true, minAvailable: 1}`; `lifecycle.preStop.sleep.seconds: 5` (0 failed logins out of 1,272 during a replica kill; 1 of ~640 without it)
- `env`: `GOMEMLIMIT=150MiB`, `GOGC=75`; DB credentials from the CNPG Secret; `SSL_CERT_FILE` plus a mounted CA bundle when upstream IdPs use a private CA
- `resources`: requests `100m / 160Mi`, limit `320Mi`
- `zitadel.masterkeySecretName`; `initJob.command: zitadel`; `Database.Postgres.{User,Admin}.Username: <db owner>` (no superuser needed), `SSL.Mode: require`, `MaxOpenConns: 10`
- `SystemDefaults.PasswordHasher: {Hasher: {Algorithm: argon2id, Time: 2, Memory: 19456, Threads: 1}, Verifiers: [bcrypt]}` (the bcrypt-14 default alone costs ~1.1 s per login)
- `SystemDefaults.Multifactors.OTP.Issuer: AlphaBravo`; cookie renames as above
- `ServicePing.Enabled: false`; `Telemetry.Enabled: false` (Zitadel phones home daily by default)
- `HTTPClient.DenyList`: the default list with the cluster Service CIDR carved out
- `FirstInstance.InstanceName` and `Org.Name` = AlphaBravo; OIDC token lifetimes 10 min via `FirstInstance`/`DefaultInstance` OIDC settings
- `login.enabled: false` + the `loginV2` instance feature pointing at our sign-in service
- Ingress paths limited as above; namespace NetworkPolicies (egress deny except Postgres, DNS and configured upstream IdPs); a slim kubectl image for the setup job

## Efficiency budgets (apply to this work and every later stream)

| Area | Budget | Spike measurement |
|---|---|---|
| Zitadel footprint | 2 replicas ≤ 256 MiB RSS each, with `GOMEMLIMIT` set and argon2id hashing; shares the existing CNPG cluster (own database) | 178–213 MiB after ~3,000 logins |
| Sign-in service | ≤ 32 MiB RSS | 14 MiB |
| Login latency | p95 ≤ 400 ms in-cluster (excluding the upstream IdP) | 303 ms in the browser, 118 ms API-level |
| Second-product silent SSO | ≤ 150 ms | 69 ms |
| JWKS cache | refetch on unknown `kid` or every 15 min, rate-limited, keep last-good keys on failure; zero network calls per request | as built |
| Session check cost | ≤ 50 µs memoized per token; ≤ 75 µs on first sighting (RS256 floor ~38 µs on this CPU) | 0.12 µs memoized, ~56 µs first |
| API p95 | list endpoints ≤ 150 ms at 10k rows; no N+1 queries (pgx tracer span counts asserted in integration tests) | — |
| Images | distroless/static, ≤ 40 MB for Go services where tooling allows | — |
| Frontend | initial JS ≤ 250 KB gzip per product, route-level code splitting, bundle budget enforced in `make check` | — |
| Memory | each product's API ≤ 150 MiB RSS at idle | — |

Budgets are proven with benchmarks and live measurements in the report, not asserted.

## Air-gap

- ServicePing and Telemetry disabled; namespace egress NetworkPolicy; all login paths proven working with egress denied.
- On k3s, a NetworkPolicy takes about 1 s to apply to a new pod, so clients (the sign-in service, setup jobs) retry during their first seconds after start.
- Every image is digest-pinned and listed in the chart's image inventory for mirroring.

## Execution order (after Astronomer 2.1.0 and Constellation 0.3.0 land)

1. Zitadel subchart in Astronomer's chart with the values above (own DB on CNPG, 2 replicas, PDB, NetworkPolicies, ServiceMonitor, restricted ingress, backups covered by the existing CNPG backup).
2. Shared sign-in service (Go, Session API v2, shared UI kit, per-product branding by `client_id`, upstream group → role mapping) and the `alphabravo_roles` Action.
3. Astronomer → OIDC relying party of Zitadel + break-glass local admin; identity settings pages backed by `zprov.Configure*`; delete replaced auth code.
4. Constellation → OIDC relying party (Zitadel or any OIDC, configurable roles claim) + break-glass; delete SAML/LDAP/adapter code.
5. Auto-provisioning: Astronomer's catalog installer calls `zprov.Ensure` for Constellation and stores the secret; catalog entry regenerated from the signed 0.3.0 chart.
6. Tool launch: "Open Constellation" from Astronomer, silent SSO.
7. Proof (e2e, live clusters): log into Astronomer → open Constellation with no second login → roles mapped → revoke role → access removed within 10 min; an external OIDC provider (Keycloak stand-in) signs in with group → role mapping; break-glass works with Zitadel scaled to 0 and alerts fire; no "Zitadel" string reachable by a user (crawl the public paths, emails, cookies, token claims); budgets measured.
8. Efficiency pass across both products against the table above.

## External identity providers (e.g. Keycloak)

We ship Zitadel only. We never ship Keycloak; it is only an identity provider a customer may already run, which we connect to and leverage. Keycloak appears in the spike solely as a stand-in for a customer's existing install.

- **Federated (default):** an admin adds Keycloak in our identity-provider settings as generic OIDC or SAML (both proven). Users sign in through Keycloak, are created on first login, and Keycloak groups map to product roles via our sign-in service. SSO, catalog auto-provisioning and break-glass are unaffected. Logout ends our session but not the Keycloak session (expected for federated logins; document it).
- **Bypass:** because both products are plain OIDC relying parties, a customer can point them directly at Keycloak (or any OIDC provider) and skip the bundled identity layer, losing only catalog auto-provisioning. Proven: the same relying-party code validates Keycloak tokens directly.
