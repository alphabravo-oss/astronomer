# API stability and deprecation policy

This document is the written contract for the `/api/v1/` HTTP API.
External integrators (UI, CLI, custom scripts hitting the JSON
endpoints) can rely on it to plan migrations.

## TL;DR

- **`/api/v1/...` is stable.** No breaking changes after a route ships.
- **Additive changes are always OK** — new endpoints, new optional
  response fields, new optional query parameters.
- **Breaking changes go to `/api/v2/...`** and the old endpoint sticks
  around for at least one release cycle, marked with the headers below.
- **Deprecations** are surfaced via the `Deprecation` and `Sunset`
  response headers (RFC 8594 / draft-ietf-httpapi-deprecation-header).

---

## What "stable" means

After an endpoint ships in a release, the following are guaranteed to
hold for the entire `/api/v1/` lifetime:

| Stability axis | Guarantee |
|---|---|
| URL path | Never renamed |
| HTTP verb | Never changed |
| Status codes | New ones may appear; existing ones keep their meaning |
| Request body fields | Required fields stay required; optional stays optional |
| Response body fields | Documented fields keep their name, type, and semantics |
| Authentication | Bearer-token / cookie shape doesn't change |

What "stable" deliberately does NOT mean:

- **Performance** is not part of the contract. We optimise endpoints
  freely; if your integration depends on a specific latency profile,
  measure against the live service rather than assuming.
- **Undocumented fields** are not stable. The wire JSON sometimes
  carries internal scaffolding that isn't in the documented response
  type — relying on those fields is undefined behaviour.
- **Ordering** of array responses is not guaranteed unless the endpoint
  explicitly documents a sort.

---

## What counts as a breaking change

Each of these is a breaking change and goes to `/api/v2/`:

1. Removing a path or verb
2. Renaming a documented response field
3. Changing a response field's type (e.g. `int` → `string`)
4. Tightening a request schema (new required field, removing a
   previously-accepted enum value)
5. Changing the meaning of an existing status code
6. Removing a previously-accepted auth method

Each of these is *not* a breaking change and ships in `/api/v1/`:

1. New endpoint
2. New optional response field
3. New optional query parameter or request body field
4. New auth method that runs alongside existing ones
5. Loosening a request schema (making a required field optional)
6. New status codes for previously-unanticipated failure modes

---

## Deprecating an endpoint

When `/api/v1/foo/` is replaced by a `/api/v2/foo/` (or by a different
shape under v1 itself), the old route stays in place AND starts
emitting:

```
Deprecation: true
Sunset: Sat, 01 Jan 2027 00:00:00 GMT
Link: </api/v2/foo/>; rel="successor-version"
```

- `Deprecation: true` tells clients the endpoint is deprecated. Clients
  should log a warning when they see it.
- `Sunset` is the date the endpoint will be removed. Minimum window is
  **one minor release** (~3 months on our typical cadence), but most
  deprecations get longer.
- `Link rel="successor-version"` points at the replacement so a tool
  can migrate automatically.

CHANGELOG entries for deprecations follow this format:

```
### Deprecated
- `/api/v1/foo/` — replaced by `/api/v2/foo/`. Sunset 2027-01-01.
  Migration: clients should swap the URL; response shape is identical
  except `legacy_field` was renamed to `field`. See docs/api-foo-v2.md.
```

---

## Version bumps to the path

When a route moves to v2, we ship it alongside v1 (both registered in
`internal/server/routes.go`). v1 emits the Deprecation header
described above; v2 emits nothing special — it's the new normal.

There is no plan to retire `/api/v1/` wholesale. Removing v1 would
require all clients to migrate in lockstep, which is operationally
unfair. Individual v1 endpoints reach Sunset and disappear one by one.

---

## What's NOT covered by this policy

- **The WebSocket tunnel protocol** between the server and agents
  (`pkg/protocol/types.go`) is internal. The server and agent ship as
  a coordinated pair; we'll add messages and never remove them, but the
  semantics of an existing message can change. Agents must be
  redeployed alongside the server when this happens. See the
  upgrade-runbook for the version-skew matrix.

- **The chart's values.yaml.** The chart is an operator tool, not an
  external contract. Values may be renamed or restructured between
  chart versions. Read the release notes when upgrading.

- **Internal SDKs** (anything under `internal/...`). Go packages
  outside `pkg/` are not part of the stability contract.

---

## How to ask "is this stable?"

If you're integrating with an Astronomer endpoint and you can't tell
whether you're depending on stable behaviour:

1. Find the endpoint in `internal/server/routes.go`. The handler
   function it points to has a response struct.
2. Find that response struct (e.g. `clusterWithMetrics` in
   `internal/handler/clusters.go`). Fields with `json:"..."` tags are
   documented. Fields without tags or with `json:"-"` are internal.
3. If the field you care about is in the documented set, it's stable.
   If not, ask in #astronomer-platform before you ship.
