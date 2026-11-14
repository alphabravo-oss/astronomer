> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Handoff: Astronomer identity on Zitadel (invisible), shared sign-in service, break-glass, auto-provisioning

Work autonomously; don't ask questions. Owner priorities: greenfield, nobody uses it, move fast; super efficient, clean, optimized code; delete old code; prove everything with e2e (real Postgres, live cluster, Playwright), not unit tests.

## Read first (all under /root/handoff/)
- PLAN-identity-zitadel.md: the spec. Follow it exactly: architecture, Zitadel invisible, break-glass, code to delete, RP library, chart values, budgets, execution order, external IdPs.
- exec-idp-spike.md, plus the spike source in /root/idp-spike/: provision/ (zprov.Ensure and Configure*Provider), rp-bench/jwks/jwks.go (the RP JWKS cache), login-kit/, claims-hook/, values.yaml, manifests/. Reuse that code; it's proven.
- exec-astronomer-release.md: current state of main, 2.1.0 and the live cluster.
- exec-astronomer-phase1.md and exec-astronomer-ui.md: the codebase conventions. The repo's CLAUDE.md/AGENTS.md and the shared UI kit are documented in frontend/src/ui and docs/ui/KIT.md.

## Rules
- Repo: create worktree /root/work/astronomer-identity on a new branch `feat/identity-zitadel` from the latest `origin/main`. Commit often, conventional commits ending with `Co-Authored-By: Codex <noreply@openai.com>`. Push the branch, open a PR to main with gh, and merge it when every proof is green (the owner says skip CI waits). Never `git push --tags`.
- Zitadel's name never appears anywhere a user can see: UI copy, emails, error pages, cookies, token claims, page titles. Our settings pages expose identity capabilities only.
- Use the Go version from go.mod (1.27.3 or newer); every Go binary must be go ≥ 1.27.3 (eMudhra CA advisory).
- Don't touch /root/ghost-ha-iac, `astronomer-charlie`, or the k3d `site-demo` cluster's agent. When you're done with the idp-spike cluster, delete it: `k3d cluster delete idp-spike && docker image rm idp-spike/tools:dev`; its source is preserved elsewhere.

## Build, in order (PLAN §Execution order steps 1–3 and 5–6 for Astronomer)
1. Zitadel subchart in deploy/chart with the spike's tested values: own DB on the chart's CNPG, 2 replicas, PDB, preStop 5s, GOMEMLIMIT, argon2id, ServicePing/Telemetry off, cookie renames, OTP issuer AlphaBravo, ingress limited to the OIDC/IdP paths, NetworkPolicies, ServiceMonitor, image digest-pinned, mirrored image list updated.
2. A shared sign-in service, `cmd/signin`, in Go: Session API v2 and OIDC auth requests; renders with the shared UI kit, branded per product by client_id; handles password + TOTP + passkeys + federated IdPs; maps upstream groups to roles on every sign-in; ≤ 32 MiB RSS. The `alphabravo_roles` claims Action ships with it.
3. Astronomer becomes a plain OIDC relying party: golang-jwt/v5 + the spike's jwks cache + verify-once memoization, with a benchmark committed that meets ≤ 50 µs memoized / ≤ 75 µs first sighting. Add the break-glass local admin: disabled by default, argon2id + mandatory TOTP, audit event + Prometheus counter + alert rule. Build the identity settings pages (sign-in methods, identity providers OIDC/SAML/LDAP, MFA/passkey policy, users, roles, sessions) on a small internal identity package backed by zprov.Configure*.
4. Delete the replaced auth code: OAuth/OIDC connector management, SCIM server, group-sync connectors, password policy/lockout beyond break-glass, Dex templates/values, old SSO plumbing, and their tests/routes/migrations (forward-only drop migrations). Report lines deleted.
5. Auto-provisioning: the catalog installer calls zprov.Ensure when installing Constellation (or any catalog app flagged sso: true), stores the secret in the release Secret, injects the OIDC settings into the release values.
6. Deploy to the live cluster on this box with helm upgrade. Take a CNPG backup first.

## Proof (all required, all live)
- Log in to Astronomer through our sign-in page via the public URL, as a local Zitadel user with TOTP, then as an LDAP user (OpenLDAP container) and an OIDC-federated user (a throwaway Keycloak stand-in; we never ship Keycloak) with group → role mapping. Revoke a role and confirm access is gone within 10 min.
- Break-glass: scale Zitadel to 0, log in with the break-glass admin, confirm the alert fires, scale back.
- No-Zitadel-leak crawl: fetch every public path, the login pages, error pages, emails (capture via webhookie or a test SMTP), and Set-Cookie headers, decode tokens, then grep for "zitadel" (case-insensitive). Expect 0 hits.
- Budgets from PLAN measured and reported.
- Playwright live suite green, plus new specs for login, break-glass and the identity settings pages.

## Output
Write /root/handoff/exec-identity-astronomer.md: per step, files changed, proofs with command output, budgets vs measured, lines deleted, PR URL and merge commit, deploy evidence, leftovers. End with a 10-line summary.
