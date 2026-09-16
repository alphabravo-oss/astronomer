# Authentication, cryptography, and API boundary hardening plan

**Status:** IN PROGRESS — priority 2 of 5

**Planned at:** `32100314e081e635e89f43fcf14673a25449ef63` on 2026-09-16, against the current dirty working tree

**Branch:** `advisor/012-auth-api-hardening`

**Depends on:** Plan 011 release-green baseline

**Reference:** technology guide security and surface chapters; existing repository security invariants remain authoritative where stricter.

## Implementation progress (2026-09-16)

- JWT revocation lookup failures now fail closed with a retryable 503 boundary,
  and access-token verification requires exactly HS256.
- Interactive access-token configuration is bounded to 5–15 minutes; SSO state
  cookies are secure, HttpOnly, SameSite, and callback-scoped.
- Production database TLS checks now parse the effective pgx configuration and
  reject ambiguous/duplicate settings; public 5xx responses redact internal
  details behind opaque request identifiers.
- Durable refresh-token families, cookie-only browser sessions, complete JWT
  context claims, and versioned ciphertext envelopes remain outstanding.

## Objective

Close the ten most important session, token, cryptography, and error-boundary gaps. The target is a cookie-based, replay-resistant session system that validates exact token context, fails closed when revocation cannot be established, never exposes bearer material to application JavaScript, and never returns internal error detail to clients.

## Findings covered (global rank 11–20)

| Rank | ID | Finding | Primary evidence |
|---:|---|---|---|
| 11 | AUTH-01 | Refresh tokens are replaced without consuming the old JTI or detecting family reuse | `internal/handler/auth_sessions.go:23-91`; `docs/secret-rotation-runbook.md:205` |
| 12 | AUTH-02 | Revocation lookup failures explicitly fail open | `internal/auth/jwt.go:510-540`; `internal/auth/jwt_revocation_test.go:195-217` |
| 13 | AUTH-03 | JWT validation accepts any HMAC method, not exactly the configured method | `internal/auth/jwt.go:435-444` |
| 14 | AUTH-04 | JWTs have no required issuer, audience, subject, or session-family binding | `internal/auth/jwt.go:52-63,453,796-816`; `internal/auth/jwt_session.go:43-47` |
| 15 | AUTH-05 | Default access-token lifetime is 60 minutes and may reach seven days | `internal/sessionpolicy/session_timeout.go:19-23` |
| 16 | AUTH-06 | Login/refresh responses expose access and refresh bearer tokens to JavaScript | `internal/handler/auth.go:486-490`; `internal/handler/auth_sessions.go:87-91`; `frontend/src/routes/dashboard/account-security.tsx:144-200` |
| 17 | AUTH-07 | SSO state cookies omit `Secure` | `internal/handler/sso.go:148-155,205` |
| 18 | API-03 | Dozens of 5xx call sites return `err.Error()` verbatim | `internal/handler/response.go:25-49`; examples `internal/handler/kubectl_shell_sessions.go:34-40,86-88` |
| 19 | CONFIG-01 | Production database TLS enforcement is substring matching over a DSN | `internal/config/production.go:55-63,146-148` |
| 20 | CRYPTO-01 | Encrypted values lack explicit per-ciphertext key/algorithm metadata | `internal/auth/crypto.go`; encrypted columns in `internal/db/migrations/001_initial.up.sql` |

## Out of scope

- Replacing project/cluster RBAC with organization tenancy.
- Adding an external identity provider or changing the existing SSO product contract.
- A wholesale switch from REST/OpenAPI to Connect RPC.
- Removing authenticated Fernet immediately. A safe envelope migration and an ADR are acceptable; destructive ciphertext rewrites are not.
- Secret values, production credentials, or token material in fixtures, logs, commits, or plan evidence.

## Preflight and compatibility decisions

1. Preserve the dirty working tree and re-open every cited location.
2. Inventory every token consumer: browser cookie auth, API/CLI bearer auth, WebSocket/tunnel handshakes, SSO, tests, and documented automation.
3. Write an ADR before implementation for the target token/session model and the bounded compatibility window. The ADR must name owners, rollout metrics, rollback, and the date old token formats stop being accepted.
4. Decide whether the supported signing method remains HS256 or moves to an asymmetric method. Validation must permit exactly the selected algorithm; never use a broad HMAC family check.
5. Design migrations as additive/expand-contract. Do not invalidate all active sessions without an explicit operator-approved release note.

## Implementation steps

### 1. Add durable refresh-session families

- Add a session-family table holding a random family identifier, user/tenant identity, hashed current refresh-token identifier, issued/expiry times, consumed/revoked times, replacement link, and minimal security metadata. Never store raw tokens.
- Mint refresh JWTs with a subject and session-family claim. In one database transaction, lock the current session row, prove the presented identifier is current and unused, mark it consumed, and insert/update the replacement.
- If a consumed identifier is presented again, revoke the whole family and associated access-session epoch. Emit a security audit event and metric without token contents.
- Resolve simultaneous refreshes deterministically: one succeeds, all losing replays fail and trigger the documented family policy.
- Logout, password reset, account disable, permission-sensitive session invalidation, and administrator force-logout must revoke the appropriate families.
- Add retention for expired family rows, leader-gated or lease-owned as repository conventions require.

Tests: normal rotation, replay, concurrent rotation, logout, expiry, disabled user, family compromise, database rollback, and cross-tenant isolation.

### 2. Fail closed on revocation uncertainty

- Change both JTI and per-user invalidation lookup errors to a typed authentication-dependency failure.
- Return 503/401 according to the established middleware contract, with a stable public code and a retry hint; do not authenticate the request.
- Remove any optional/nil revocation-checker path from production composition. Construction must fail when the dependency is absent.
- Bound positive cache TTL below the revocation SLA and actively invalidate cache entries on revoke. Document the maximum enforcement delay.
- Add readiness/metrics for the revocation dependency and tests for store error, cache-coordinator error, stale positive cache, and recovery.

### 3. Require exact JWT context

- Validate exactly the configured signing method, issuer, audience, expiration, issued-at, subject, token type, JTI, and session-family identifier.
- Mint the same required claims. Use distinct audience and/or purpose for access, refresh, SSO challenge, and other one-time tokens.
- Enforce clock skew from one bounded configuration value.
- During migration, accept the legacy shape only through a separately instrumented validator with a fixed removal release/date; never silently relax the new validator.
- Add negative tests for algorithm substitution, missing/wrong issuer, missing/wrong audience, wrong purpose, expired/not-yet-valid values, absent subject/session, and key rotation.

### 4. Enforce short access-token lifetime

- Change defaults and validation so interactive access tokens are 5–15 minutes, matching the guide. Keep refresh/session lifetime independently configurable within documented bounds.
- Update the chart schema, runtime config, examples, runbooks, and tests together.
- Provide a compatibility warning/error for an existing out-of-range production value; do not silently clamp it.

### 5. Make the browser session cookie-only

- Stop returning access and refresh tokens in login, refresh, or account-security JSON responses for browser flows. Set/rotate `HttpOnly`, `Secure`, appropriately scoped `SameSite` cookies instead.
- Keep any supported CLI/API token flow separate and explicit; never make a browser refresh token readable by JavaScript.
- Update OpenAPI, generated clients, frontend adapters, and tests. Remove frontend code that reads, displays, copies, logs, or stores session bearer material.
- Apply CSRF protection appropriate to the chosen SameSite/cross-origin contract for state-changing cookie-authenticated requests.
- Verify that refresh failures clear invalid cookies and that logout clears every relevant path/domain variant.

### 6. Secure SSO state cookies

- Always set SSO state cookies `HttpOnly` and `Secure` in supported production flows, with narrowly scoped path, SameSite, and short Max-Age.
- Derive secure-request state only from trusted proxy configuration; do not trust arbitrary forwarded headers.
- Make the clear-cookie attributes exactly match the set-cookie attributes.
- Add direct HTTPS and trusted-proxy tests plus a production-config assertion.

### 7. Centralize public error responses

- Replace raw `err.Error()` 5xx responses with stable public codes/messages and an opaque correlation identifier.
- Log the full wrapped error server-side with request/trace identifiers and existing redaction.
- Audit all production `RespondError`/JSON error call sites; allow detailed client text only for explicit, typed, safe 4xx validation errors.
- Add a static test/lint rule that rejects new 5xx paths containing `err.Error()` or `%v` of an internal error.
- Preserve actionable field validation without exposing SQL, filesystem, upstream URL, secret, or Kubernetes error detail.

### 8. Parse database DSNs structurally

- Use the pgx connection parser, inspect the effective TLS configuration/sslmode, and reject disabled/insecure modes in production.
- Cover URL and keyword/value DSNs, percent encoding, duplicate parameters, case, whitespace, and misleading substrings in unrelated values.
- Keep redacted error messages; never echo the DSN.

### 9. Introduce versioned ciphertext envelopes

- Define a storage envelope containing at least version, key identifier, algorithm, nonce/metadata as needed, and ciphertext. Continue using an authenticated construction only.
- Extend the keyring API to select by key ID rather than trying every key. Keep a bounded legacy Fernet reader solely for migration.
- New writes use only the new envelope. Reads of legacy values should opportunistically or asynchronously re-encrypt, with metrics that reach zero before removing compatibility.
- Inventory every encrypted column and outbox/backup path. Add per-domain migration checkpoints, rollback behavior, unknown-key handling, and corruption tests.
- Write an accepted ADR explaining the selected envelope (age or another AEAD), threat model, rotation, backup recovery, and FIPS implications.

## Verification

Run focused auth/handler/config tests, then:

```bash
go test -race ./internal/auth ./internal/sessionpolicy ./internal/handler ./internal/config ./internal/server -count=1
go vet ./internal/... ./cmd/...
go test ./... -count=1
cd frontend
npm run type-check
npm run lint
npm test
```

Add browser tests that assert:

- no session token is present in JSON, local/session storage, page text, console, or application-readable cookies;
- refresh rotates the cookie and replay fails;
- forced logout/revocation remains enforced during database failure;
- SSO state cookies have the expected secure attributes.

## Commit sequence

1. `docs(adr): define browser session and jwt contract`
2. `feat(auth): add refresh session families`
3. `fix(auth): fail closed on revocation uncertainty`
4. `fix(auth): validate exact token context`
5. `fix(auth): constrain access token lifetime`
6. `fix(auth): make browser sessions cookie only`
7. `fix(auth): secure sso state cookies`
8. `fix(api): redact internal server errors`
9. `fix(config): parse postgres transport security`
10. `feat(crypto): add versioned ciphertext envelopes`

## Done criteria

- A refresh token is single-use; reuse revokes its family and is audited.
- Revocation-state failures never authenticate a request.
- Every JWT type validates an exact algorithm, issuer, audience, purpose, subject, and session context.
- Interactive access tokens cannot be configured outside 5–15 minutes without an explicit documented non-browser exception.
- Browser JavaScript never receives session bearer material.
- SSO cookies are securely scoped and production-enforced.
- No untyped 5xx returns internal error text.
- Production DSN validation cannot be bypassed by string tricks.
- Every new ciphertext identifies its format and key; legacy migration is measurable and reversible.
- Full backend/frontend gates pass and no secret material is logged or committed.

## Stop conditions

- Stop for product/security approval before invalidating existing sessions or changing supported CLI authentication.
- Stop if a legacy encrypted value cannot be deterministically mapped to an owning key; preserve it and produce a redacted migration report.
- Stop if cookie-only auth would break a documented cross-origin deployment without an agreed CSRF/CORS design.
