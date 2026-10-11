# v2.0.0 tool UI compatibility review

Reviewed 2026-10-11 for the user-approved v2.0.0 browser isolation change.

The shared and cluster Grafana routes and generic service UI proxy routes used
to return downstream content on the console origin. A compromised downstream
UI could consequently access console APIs with the user's browser authority.
The authorized migration preserves those UIs on separate target-specific HTTPS
hostnames. It deliberately changes the old proxy HTTP contract.

The exact 53 oasdiff changes against integration commit
`5d56e7ba9df17115a2650353b368c92de4f2bd4d` are reviewed in
`deploy/contracts/openapi-current-reviews.json`:

- GET and HEAD return an Astronomer-owned HTML launch document. Their old JSON
  and binary response media types are removed. A one-use, short-lived POST
  grant opens the selected tool origin without exposing the console token.
- POST, PUT, PATCH, DELETE and OPTIONS on the old console proxy routes return
  410 once isolation is configured. Tool-origin traffic retains the selected
  downstream proxy handler and its authorization checks. These paths are UI
  proxies; backend tool management API routes remain separate.
- With isolation unconfigured, UI launch routes fail closed with 503.

Consumers must open the existing GET UI link in a browser from the console.
Clients must not treat console UI proxy URLs as stable downstream API endpoints.
The generated SDK and OpenAPI response types reflect the launch/410 contract.
The new domain and certificate requirements, migration procedure, and the
launch-time access-token session lifetime are documented in the
[operator runbook](../../runbooks/tool-ui-isolation.md).

Focused tests cover the actual router launch/redeem flow, target confinement,
RBAC revocation, forbidden cross-origin requests, host-only protected cookies,
replay rejection, and custom installed namespaces. SSO tests also reject a
valid signed flow cookie supplied under its obsolete unprotected name while
preserving the legitimate provider-specific flow.

This review accepts these exact intentional major-version API changes. It
does not waive source-security validation, authorize unrelated API changes,
or certify a live DNS/TLS deployment. The registry remains bound to exact
source and baseline digests, warning identities, an owner, and expiry.

## Go SDK migration

The same approved v2 browser isolation change removes 38 `JSON200` fields from
legacy service/Grafana proxy response structs. Those endpoints now return HTML
launch documents or reject unsafe main-origin requests. The exact removed fields
and both generated SDK digests are bound in
[`sdk-v2-reviewed-breaks.json`](../../../deploy/contracts/sdk-v2-reviewed-breaks.json).
The released-client compile fixture still checks all other exported symbols;
changing either SDK snapshot requires a new review. Callers must launch tools in
the browser rather than decode tool UI documents as control-plane JSON.
