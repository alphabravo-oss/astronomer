# API versioning

This document describes the `/api/v1` contract from the *versioning*
angle: what the version number promises, what counts as a breaking
change (and therefore forces a new version), and the response-header
conventions a client uses to detect deprecation, pace itself against
rate limits, and retry safely.

It is the companion to [`api-stability.md`](./api-stability.md). That
doc is the per-endpoint stability guarantee and the Deprecation/Sunset
policy in prose; this doc is the versioning model and the on-the-wire
header conventions a client integrates against. Where they overlap,
`api-stability.md` is the normative source for the deprecation policy
and this doc defers to it.

---

## The `/api/v1` contract

- Every JSON endpoint is mounted under `/api/v1/...`. The version is in
  the **URL path**, not a header or query parameter, so it is visible
  in logs, proxies, and bookmarks, and a client pins a version simply
  by using its prefix.
- A route that has shipped under `/api/v1` does not change shape. The
  full list of axes that stay fixed (path, verb, status-code meanings,
  required/optional request fields, documented response fields, auth
  shape) is the table in `api-stability.md`.
- The machine-readable surface is the OpenAPI document served at
  `GET /api/v1/openapi.yaml` (source of truth: `docs/openapi.yaml`,
  embedded at `internal/handler/assets/openapi.yaml`). A browsable
  Swagger UI is at `GET /api/v1/docs`. Both are public so an integrator
  can read the surface before they hold a token.

## What counts as a breaking change

A change is **breaking** — and so requires a new major version
(`/api/v2`) rather than an in-place edit of `/api/v1` — if an existing,
well-behaved client could observe different behaviour after the change.
Concretely, breaking changes include:

- Renaming or removing a route, or changing its HTTP verb.
- Removing a documented response field, renaming it, narrowing its type,
  or changing its meaning.
- Adding a new **required** request field, or making a previously
  optional field required.
- Tightening validation so input that used to be accepted is now
  rejected.
- Changing the meaning of an existing status code, or replacing a
  success code with a different one for the same outcome.
- Changing the authentication shape (token format, cookie name, header).

Changes that are **NOT breaking** and ship straight into `/api/v1`:

- Adding a new endpoint.
- Adding a new **optional** request field or query parameter (with a
  backward-compatible default).
- Adding a new field to a response body. Clients must ignore unknown
  fields; see "undocumented fields are not stable" in `api-stability.md`.
- Adding a new status code for a genuinely new condition.
- Performance and internal-implementation changes — performance is
  explicitly not part of the contract.

When a breaking change is unavoidable, the new behaviour ships under the
next version prefix and the `/api/v1` endpoint stays in place for at
least one release cycle, flagged with the deprecation headers below.

## Deprecation / Sunset header convention

Deprecation is advertised on the response, per RFC 8594
(`draft-ietf-httpapi-deprecation-header`). A client never has to poll a
changelog — the endpoints tell it directly:

| Header | Meaning |
|---|---|
| `Deprecation` | Present on a deprecated endpoint. Either `true` or an IMF-fixdate marking when deprecation took effect. |
| `Sunset` | IMF-fixdate after which the endpoint may stop responding. Always on or after the `Deprecation` date. |
| `Link` | `rel="successor-version"` points at the replacement route (e.g. the `/api/v2` equivalent); `rel="deprecation"` points at the human-readable notice. |

Client guidance: treat any response carrying a `Deprecation` header as a
migration signal, and schedule the move before the `Sunset` date. A
deprecated endpoint keeps working — same status codes, same body — until
its sunset; the headers are advisory, not an error.

## Rate-limit headers

Rate-limited endpoints (the API endpoint-class limiter and the login
limiter) advertise the caller's standing quota on **every** response, so
a client can self-pace instead of discovering the limit only at the
first `429`. The headers follow the IETF
`draft-ietf-httpapi-ratelimit-headers` naming:

| Header | Meaning |
|---|---|
| `RateLimit-Limit` | The quota ceiling for this caller/class (token-bucket burst, or login attempts per window). |
| `RateLimit-Remaining` | Whole units left before the next request is throttled. `0` on a `429`. |
| `RateLimit-Reset` | Seconds until the quota refills to full (`0` when already full). |

On a throttled request the response is `429 Too Many Requests` with a
JSON `{"code":"rate_limited", ...}` body and a `Retry-After` header (the
seconds to wait) in addition to the `RateLimit-*` headers above.

Note on accuracy: the API limiter is a token bucket, so `RateLimit-Reset`
is the time to fully refill the bucket, and `RateLimit-Remaining` is the
whole tokens currently available — a best-effort snapshot, not a precise
reservation. The login limiter is a fixed window, so its `Reset` is the
seconds left in the current window.

## Idempotency-Key

Mutating requests (`POST` / `PUT` / `PATCH` / `DELETE`) may carry an
`Idempotency-Key` request header so a retried request is not applied
twice. There are two cooperating layers, both keyed by the same header:

1. **Durable, DB-backed** (handler layer): the source of truth for
   operations that need cross-replica, restart-surviving guarantees
   (tool install/upgrade/uninstall, catalog, clusters, fleet operations,
   backups, monitoring). A completed operation's outcome is recorded and
   replayed.
2. **In-memory short-TTL guard** (middleware layer): a lightweight retry
   guard for the typed resource/workload/node mutations. Within a short
   TTL it replays the cached status + body of a request already in
   flight or just completed for the same key, and tags the replay with
   `Idempotent-Replayed: true`.

Client guidance: set a stable, unique `Idempotency-Key` (a UUID is
ideal) per logical mutation and reuse it across automatic retries of
that same mutation. Use a fresh key for a genuinely new mutation.

Ceiling of the in-memory layer (by design, not a defect): it is
single-process and TTL-bounded — it is not shared across replicas and
does not survive a restart. A retry that lands on a different replica, or
after a process bounce, falls through to the handler (and, for the
operations that need it, the DB-backed layer). Do not rely on the
in-memory layer for durability; rely on it only to absorb fast
client-side retries. For durable guarantees, the operation must be on
the DB-backed path.
