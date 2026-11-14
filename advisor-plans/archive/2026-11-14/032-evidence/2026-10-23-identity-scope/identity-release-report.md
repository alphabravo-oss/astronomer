> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# AlphaBravo identity initial release — 2026-10-21

Repository: https://github.com/alphabravo-oss/identity . Initial release: [`v0.1.0`](https://github.com/alphabravo-oss/identity/tree/v0.1.0), source commit `3156bfc100abe6b323f0f124a1693e76ebfab666`.
Implementation runtime source: `cc77ef4c3bbeb210b46956df9421a78772da591c`; the release tag also contains tested artifact pins, browser-proof improvements and this release's README measurements. All commits use conventional subjects and the requested Codex co-author trailer. License: AGPL-3.0. Go: 1.27.3.

## Published artifacts

| Artifact | Immutable digest |
|---|---|
| `ghcr.io/alphabravo-oss/identity/signin:0.1.0` | `sha256:8e64ef08a2be1e6b058980889524de0b9a58ccedf758070ef87e6bd7f89e4938` |
| `ghcr.io/alphabravo-oss/identity/bootstrap:0.1.0` | `sha256:2fa038dc8a57dc8ef86e9f12acaf424f0a068c61445b7222b4c514f666caa5b7` |
| `oci://ghcr.io/alphabravo-oss/charts/identity:0.1.0` | `sha256:8b7d44fdfe9f3a9d11c36a3431ab6a39e43d0aa69d64dc0a0fba99c69bbbf09d` |

The installed primary release uses that published chart and those image digests, not local development images. Anonymous OCI pulls succeeded. Actions is enabled: https://github.com/alphabravo-oss/identity/actions/runs/39532631028 . It tested, built exactly two owned images, scanned both with Grype (zero matches), published, and keyless-signed both images and the chart. No fallback signing key was copied or generated. Cosign verification checked certificate identity `https://github.com/alphabravo-oss/identity/.github/workflows/release.yml@refs/heads/main`, issuer `https://token.actions.githubusercontent.com`, signatures and transparency-log inclusion. Evidence: `/root/work/identity-proof/signatures/` and `release-final/`.

The chart contains a pinned official Zitadel 10.4.0 dependency and engine v4.19.5 digest, not an engine fork. The public service and product-facing Go types hide the engine. No Keycloak image ships in the chart. Demo-product is an additional binary in the bootstrap image, keeping the owned image count at two.

## Repository layout and implementation

- `cmd/signin`: server-rendered shared UI kit, self-hosted Inter Variable, product branding, light/dark; password/TOTP, enrollment, passkeys, OIDC/SAML/LDAP federation, reset and invitations; own SMTP sender/templates. Session API v2 and OIDC auth-request APIs remain private.
- `cmd/bootstrap`: idempotent project/roles/OIDC app registration, durable per-product Secrets, provider configuration, scoped login capability and claims Action. Cookie/mail/claim/SAML-branding keys persist across reruns.
- `pkg/provision`: capability-shaped public API, `Ensure`, durable credential store, OIDC/SAML/LDAP provider configuration and user/role administration. Internal engine SDK details stay under `internal`.
- `pkg/rp`: jwt/v5, last-good JWKS cache, rate-limited unknown-key refresh, 15-minute refresh, bounded immutable verify-once memo, issuer/audience/time/algorithm checks, configurable roles claim, bearer middleware and browser code-flow helper with PKCE/state/secure cookies. External OIDC bypass uses the same verifier.
- `chart/identity`: optional CNPG Cluster or existing Postgres configuration/password Secret, bootstrap, two sign-in replicas, two engine replicas/PDB/preStop/GOMEMLIMIT/argon2id, restrictive public ingress, network policies, optional ServiceMonitor/PrometheusRule, JSON schema and air-gap image inventory. Login V2, phone-home telemetry and ServicePing are disabled. OTP issuer/cookies are branded.
- `examples/demo-product`: tiny app that delegates browser authentication to `pkg/rp.NewWeb`; deployed three times with distinct registrations for the proof.
- `docs/decisions/0001`–`0004`, `docs/integrating-a-product.md`, `docs/air-gap.md`, `docs/ui.md`, README, AGENTS/CLAUDE, Makefile, golangci v2 config and release workflow document the conventions.

SAML required an additional privacy fix beyond the spike: the stock SP certificate contains the engine name in its subject/issuer and travels in browser XML. The sign-in proxy reissues only that certificate envelope with the identical SP public key and neutral names, using a persisted branding issuer key. SignedInfo is untouched; RSA-SHA256 AuthnRequest verification succeeds in the real upstream. The proxy exposes only exact callback/metadata/ACS/SLO paths. Decoded certificate subjects and issuers were inspected, not merely raw base64 strings.

Product registry changes hot-reload with last-good fallback. Upstream group grants are replaced on each fresh upstream authentication, including removals. Silent SSO reuses its authenticated session; it does not pretend to re-read an upstream directory. Issued claims live at most ten minutes. Product-local accounts and an independent offline break-glass database remain product responsibilities, as specified by the sign-in-mode design.

## Live installation and boundaries

Only the isolated k3d `identity-dev` cluster was changed. Kubeconfig: `/root/work/identity-proof/kubeconfig`. Server/agent memory caps are 1800/2200 MiB. Installed prerequisites: CNPG 1.30.1 (chart 0.29.1), cert-manager v1.20.1. Namespace `identity`; final primary Helm revision 9. Engine and sign-in each have two ready replicas; all three example registrations, Postgres and the private customer fixtures are running. Leave this cluster running.

Host k3s, site-demo/ui-demo, production Astronomer/Constellation repos and the excluded infrastructure directories were untouched. The `astronomer`/`constellation` proof deployments are the tiny example app under those registrations, not modifications to production products. Keycloak/OpenLDAP/Mailpit are private test fixtures with persisted state. Keycloak is a customer-provider stand-in only. The temporary `identity-lifecycle` namespace was removed after testing. Root filesystem finished at 79%, below the requested 80%; Go/build caches were cleaned during builds.

Hosts inside the fixture: `identity.identity.test`, `demo.identity.test`, `astronomer.identity.test`, `constellation.identity.test`, `kc.identity.test`. HTTPS is exposed on host port 18443, HTTP on 18080. A private CA is at `/root/work/identity-proof/ca.crt`. Browser automation maps those hostnames to `127.0.0.1:18443`; no host hosts-file modification is required. All kubectl commands must explicitly use the isolated kubeconfig.

Install overlays, fixture manifests, API/benchmark helpers and logs are under `/root/work/identity-proof/`; credentials are confined to its mode-700 `secrets/` directory with mode-600 files. Do not publish that directory. Registry inputs: `live-values.yaml`, `ca-values.yaml`, `providers-values.yaml`. Reinstallation example:

```sh
KUBECONFIG=/root/work/identity-proof/kubeconfig helm upgrade --install identity \
  oci://ghcr.io/alphabravo-oss/charts/identity --version 0.1.0 -n identity \
  -f /root/work/identity-proof/live-values.yaml \
  -f /root/work/identity-proof/ca-values.yaml \
  -f /root/work/identity-proof/providers-values.yaml --wait --timeout 5m
```

## Measured budgets

All times below are observations on this host, not guarantees for different infrastructure. Final HA HTTP measurements use 100 independent local accounts, real TLS ingress, password plus TOTP, OIDC callback/PKCE token exchange and the deployed relying party. A replica deletion starts two seconds into the login loop; preStop runs during the loop. Final RSS is sampled after that load using container process VmRSS.

| Measurement | Budget | Observed |
|---|---|---|
| Sign-in RSS, two replicas | ≤32 MiB each | 14.28 / 14.46 MiB |
| Engine RSS, two replicas | ≤256 MiB each | 186.68 / 172.83 MiB |
| Password + TOTP login HTTP p95 | ≤400 ms | 123.87 ms; p50 108.87, max 184.53 |
| Second-product silent SSO HTTP p95 | ≤150 ms | 63.76 ms; p50 54.58, max 78.40 |
| Browser application login p95, 40 samples | ≤400 ms | 377.76 ms |
| Browser second-product SSO p95, 40 samples | ≤150 ms | 129.75 ms |
| RP memoized verification | ≤50 µs | 0.890 µs |
| RP first sighting, RS256 | ≤75 µs | 57.26 µs |
| RP network in warm verification loop | 0 per request | 0; external-provider loop also 0/1000 |
| Go image filesystem payload | ≤40 MB | sign-in 12.67 MiB / ~13.3 MB; bootstrap 34.41 MiB / ~36.1 MB |
| Compressed image layers | informational | 4.73 / 11.16 MiB |
| Sign-in JavaScript | ≤30 KB (also below generic 250 KB gzip) | 1,708 bytes total uncompressed, no SPA |
| List API at 10k rows / N+1 | product budget | N/A: no owned list API/database query surface |
| Product API idle RSS ≤150 MiB | downstream product budget | N/A for production products; their repos were untouched |

Browser application time includes initial navigation and form submission through the final product page, excluding typing/Playwright field-fill time. The separate complete browser wall-time p95 INCLUDING automated credential entry is **426.63 ms**, above 400 ms if that broader metric is chosen. A cold first browser sample was 604 ms application time; p95 includes it in the 40-sample distribution. The HTTP numbers and application-only browser numbers must not be presented as complete wall time. Individual interactive SSO runs also had slower outliers; the reported budget comparison is the measured p95 series.

Evidence: `ha-published-final.log`, `ha-replica-deletion.json`, `httpbench.go`, `browser-timing-final.log`, `memory-final.json`, `rp-bench-final.txt`, `image-sizes-final.json`. The RP benchmark preloads keys; unknown key refresh can perform a rate-limited network fetch. Normal warm verification never introspects.

## Functional and failure proofs

| Proof | Result and evidence relative to `/root/work/identity-proof/` |
|---|---|
| Local password + TOTP, then two products without login | Same subject; demo `viewer`, astronomer `operator`, constellation `analyst`; `local-final.log`, `e2e-final.log` |
| New invitation and mandatory TOTP enrollment | Branded email, invite code accepted, initial password set, authenticator key enrolled, successful product login; `invitation-enroll-final.log` |
| Passkeys | Real browser virtual CTAP2 authenticator registration and subsequent authentication; `passkey-final.log` |
| Password reset | Mailpit captured our sender/templates; emailed link sets new password; `recovery-final.log` |
| LDAP group mapping/removal | Same dave subject initially admin, then no roles on next fresh login; `published-ldap.log`, `published-ldap-revoked.log` |
| OIDC federation group mapping/removal | Same kim subject initially admin, then no roles; `published-oidc.log`, `published-oidc-revoked.log` |
| SAML federation | Real signed redirect/POST through Keycloak, viewer grant, neutral certificate; `published-saml-neutral.log`, `saml-certificate-subjects.txt`, `saml-deep-leak-check.txt` |
| External OIDC bypass | Actual Keycloak token, 1000 verifications, zero verification network requests, total 1.555 ms; `external-bypass.log` |
| Public privacy crawl | 74 public responses, invalid-method/input/error cases, discovery, metadata, callbacks, headers: zero case-insensitive engine-name hits; `public-crawl-final.log`, `public-crawl.json` |
| Deep browser/email/token privacy | Cookies, decoded JWT claims, captured reset/invite emails and decoded SAML XML/certificate subjects: zero hits; browser proof logs and private captured messages |
| Replica death during login | 100 successful / 0 failed logins while deleting an engine replica; `ha-published-final.log` |
| Engine outage | Both engine replicas scaled to zero; 10/10 relying-party requests validate an issued token first seen while engine is down; replicas restored to two; `engine-zero-final.log`, `engine-restored.log` |
| Egress denied | Default namespace egress deny with explicit internal/DNS/API allowances; external TCP connection blocked after CNI policy applied; all local/passkey/reset/LDAP/OIDC/SAML flows run with this policy; `egress-denied.log`, `networkpolicies-live.yaml` and login logs |
| Ensure idempotency | Same client ID on rerun, durable store called once, create 180.88 ms; `provision-integration.log`, `integration-final.log` |
| Chart standalone + dependency | Strict lint/render and nested Wolf parent render succeed; `helm-final.log`, `parent-chart.log` |
| Retained-data uninstall/reinstall | CNPG UID, master-key hash, admin-PAT hash and org ID identical; `lifecycle-before.json`, `lifecycle-after.json`, lifecycle logs |

Monitoring templates are supplied; this proof does not claim a Prometheus deployment or fired-alert validation. Air-gap operation means mirror the complete pinned inventory and prerequisites first; an offline first install cannot fetch public registry images. Real customer upstream IdPs must be reachable on the explicitly allowed private paths. Group removals require a fresh upstream authentication; previously issued JWT claims expire within ten minutes.

Canonical checks passed: `make test` (race), `make test-integration` with live credentials, `make lint` (0 issues), `make helm-lint`, `make security` (govulncheck: no vulnerabilities; gosec clean), `make e2e`. The successful publish workflow independently ran race tests and Grype scans. Earlier exploratory test failures remain in historical logs; repeated TOTP use within the same counter is correctly rejected. The final HA run uses separate accounts and an explicit counter-boundary wait. Docker restarted during the work; only the isolated cluster was recovered, and fixture identity state was moved to persistent volumes.

## Screenshots and workstation copy

Six final 1280×900 screenshots are at `/root/work/identity-proof/shots/`:
`astronomer-light.png`, `astronomer-dark.png`, `constellation-light.png`, `constellation-dark.png`, `demo-product-light.png`, `demo-product-dark.png`. Accent, product logo/name and dark/light treatment differ correctly. Shared tokens, Inter Variable and per-product accent follow the handoff UI kit.

This process runs on astronomer-dev itself (5.78.179.107). The requested SCP source address is this host; no workstation reverse destination/access was supplied. Screenshots therefore remain on the box, not copied to the workstation-local scratchpad. Run the supplied pull FROM the workstation:

```sh
mkdir -p /tmp/claude-1000/-home-alphabravo-code/48647ec0-5c10-4f82-8bd7-f3a02c128d62/scratchpad/identity-repo/shots
scp -i ~/.ssh/ab-devops-hetzner-042122 \
  'root@5.78.179.107:/root/work/identity-proof/shots/*.png' \
  /tmp/claude-1000/-home-alphabravo-code/48647ec0-5c10-4f82-8bd7-f3a02c128d62/scratchpad/identity-repo/shots/
```

## Adopting the layer: Wolf

Follow `docs/integrating-a-product.md` (~45 minutes with an existing release/TLS). Add a `products` entry for Wolf with slug/display name, branding/legal URLs, roles, exact redirect URIs and group-to-role mappings. Upgrade the identity chart or configure it under a parent chart's dependency values. Bootstrap writes the per-product client-id/client-secret/project-id/issuer Secret. Inject only Wolf's credentials, not the administration or mail capability.

Import `github.com/alphabravo-oss/identity/pkg/rp`; initialize `NewWeb` with issuer/audience/JWKS URL/product, client secret and redirect URI; attach its Login, Callback and Session handlers. For APIs use the verifier's bearer middleware and `HasRole`. The example already does this—products need no copied OAuth flow. Authorization remains in the product. Browser tokens have a 3,800-byte cookie budget; larger customer tokens need product server-side sessions; bearer token cap is 32 KiB.

For direct customer OIDC bypass, supply the customer's trusted issuer/JWKS/authorization/token URLs and configurable role claim. For dynamically installed tools import `pkg/provision`, supply a durable SecretStore and call `Ensure`; serialize concurrent registration of the same slug. Select local-only, SSO-only, or both in product settings; independent local accounts/break-glass are owned by that product and must remain usable without this engine. Existing issued SSO tokens already continue to validate during an engine outage, as proved above.

## Ten-line summary

1. Built and published AlphaBravo identity v0.1.0 from the public identity repository.
2. Published exactly two owned images and one digest-pinned OCI Helm chart.
3. GitHub Actions keyless-signed all three artifacts; Cosign verification passed.
4. Three registered products share one session and receive distinct product roles and branding.
5. Password/TOTP, fresh enrollment, passkeys, reset, invitations, LDAP, OIDC and SAML passed live.
6. LDAP/OIDC group removal removes mapped roles on the next fresh upstream sign-in.
7. Final replica-kill loop completed 100 logins with zero failures; engine-zero validation passed 10/10.
8. RSS was 14.28–14.46 MiB sign-in and 172.83–186.68 MiB engine; HTTP login/SSO p95 123.87/63.76 ms.
9. Public privacy crawl found zero hits; six screenshots remain on the host for workstation SCP pull.
10. identity-dev remains running; Wolf adoption and evidence paths are documented above.
