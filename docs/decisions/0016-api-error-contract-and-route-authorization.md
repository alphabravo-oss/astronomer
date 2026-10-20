# 0016 — One API error contract (apiutil) and one route authorizer (static route → permission table)

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide ch.03 (errors through a single apiutil
  helper, closed code set → connect.Code, Internal scrubbed) and ch.06
  (authorization interceptor after authentication, static route → role map,
  denial audit)

## Context

Errors were written by two writers with two body shapes:
`handler.RespondRequestError` (catalog codes in `internal/handler/apierror`,
an `= string` alias, so any literal compiled) and the delivery package's
`respondError` (~298 sites, no request id, no 5xx scrubbing), plus a dozen
hand-rolled envelopes in the route and middleware layers (rate limits wrote a
top-level `{"code","message"}`).

Authorization was ~365 gate mounts spread over `internal/server/routes*.go`
(`requirePermission` and eleven variants, `ClusterScopeFromIDParam` subtree
markers, `requireSuperuser`) plus `handler.RequireSuperuser` inside handlers.
A route without a mount was silently open to any authenticated user.

## Decision — errors

- `internal/api/apiutil` owns the error contract. The catalog moved there from
  `internal/handler/apierror` (deleted; no parallel enum). `Code` is a
  **defined type**, so the set is closed at compile time;
  `TestNoCodeOutsideCatalog` also rejects untyped literals and
  `apiutil.Code(...)` conversions anywhere under `internal/` and `cmd/`.
- `catalog.go` maps **every** code to exactly one `connect.Code` and one
  canonical HTTP status (`TestCatalogCoversEveryCode` proves both directions).
  The canonical status of a pre-existing code is the status its call sites
  used most often when the catalog was built; the connect.Code follows from
  that status (`ConnectCodeForHTTPStatus`) except where the code is narrower
  than its status (AlreadyExists, Aborted, Canceled, ResourceExhausted).
- REST call sites keep passing an explicit status (it is today's wire
  contract; 33 codes are legitimately used under more than one status). The
  connect.Code is a property of the code, not of the status. Phase 2 Connect
  handlers use `apiutil.ConnectError`.
- `FromError` maps a closed set of domain errors: the apiutil sentinels
  (`ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidArgument`,
  `ErrFailedPrecondition`, `ErrConflict`, `ErrPermissionDenied`,
  `ErrUnauthenticated`, `ErrUnavailable`), `pgx.ErrNoRows`, context errors,
  `*pgconn.PgError` by SQLSTATE (23505 → AlreadyExists; 23503 on
  insert/update → InvalidReference/InvalidArgument, otherwise and 23001 →
  ResourceInUse/FailedPrecondition; check/not-null/data → ValidationError;
  40001/40P01 → ConcurrentModification/Aborted; 57014 → DeadlineExceeded;
  08xxx/53300/57P01 → DBUnavailable) and `*apiutil.Error`. Everything else is
  Internal with the original error kept as the log-only cause.
- `apiutil.WriteError` / `apiutil.Respond` are the only error writers. One
  body shape: `{"error":{"code","message","request_id"?,"fields"?,"details"?}}`
  (`fields` for validation violations, `details` for structured context such
  as quota limits and maintenance windows; both dropped on 5xx). Every 5xx
  message is replaced with a fixed public message and the original is logged
  with the request id returned to the client. Every error increments
  `astronomer_request_errors_total{route,reason}`.
- `connectrpc.com/connect` v1.19.1 is a dependency only for `connect.Code` /
  `connect.Error` until Phase 2 mounts Connect.

## Decision — authorization

- `routePermissions` (`internal/server/route_permissions_table.go`) is a
  static `map[string]Permission` keyed by `"METHOD chi-pattern"` (the
  fully-qualified RPC name in Phase 2). `Permission{Public, Authenticated,
  Superuser, Resource, Verb, Scope, RouteParam, Requirements, IDNamesCluster,
  DeliveryProject, Also}`; `Scope` enumerates the scope resolvers the old
  gates used (route params, query namespace, collection, namespace list,
  any/all-of, namespace picker, k8s proxy, typed resources).
- `routeAuthorizer` is the one middleware. Every authenticator
  (`requireAuth`, `enrollChallengeOrAuth`, `requireStreamTicketOrAuth`) is
  composed as `authn → authorizer`, so it runs immediately after
  authentication wherever authentication is mounted (authentication itself
  is still mounted per subtree; collapsing it to a single mount point is
  Phase 2's interceptor chain). It resolves the route pattern and params
  against the root router, **denies any authenticated route without a row**,
  evaluates the row with the existing rbac engine evaluators, and on every
  403 logs `authorization denied {sub, route, required, roles}` and writes an
  `auth.authorization_denied` audit row with the same detail.
- Superuser-only routes are rows with `Superuser: true`, evaluated by
  `rbac.Engine.CheckSuperuser` on the binding the RBAC store synthesizes for
  `is_superuser` users. `handler.RequireSuperuser` is deleted. The one
  body-dependent elevation (changing a cluster's downstream-impersonation
  mode) asks the same engine through `authorizationSupport.authorizeSuperuser`.
- Isolated tool-UI hosts never reach the root router; they authorize through
  `toolUIPermissions` rows with the same authorizer.
- The first table was generated mechanically: an instrumented build of the
  previous commit walked the production router (optional families on) and
  the fully wired route-security router, recorded every route's gate chain
  (905 routes; the two routers agreed on every shared route) and emitted the
  rows. Gates mounted with `Use` saw fewer URL params than the authorizer
  now resolves; the only affected rows gate `sso:update`, which is not
  scope-sensitive, so decisions are unchanged.
- `TestRoutePermissionTableMatchesRouter` walks both routers with `chi.Walk`
  and fails on a route without a row, a stale row, or a row whose `Public`
  flag disagrees with the presence of the authorizer in the route's chain.

## Consequences

- Behaviour changes, all deliberate: the authorizer now runs before
  feature gates, token-scope checks, idempotency replay and rate limiting
  that used to precede some gates (a denied caller sees 403 before a 404
  feature_disabled or 403 scope_denied); superuser denials are
  `permission_denied` instead of `forbidden`; rate-limit 429s use the error
  envelope; quota/maintenance extras moved under `error.details`; delivery
  5xx messages are scrubbed; a lost database connection is 503
  `db_unavailable`.
- Not done here: about 60 call sites in ~25 admin handlers still call the package-private
  `handler.requireSuperuser` (or their own `h.requireSuperuser`) at the top
  of admin endpoints whose rows are `Authenticated: true`; they are
  route-level checks that belong in the table and are the next mechanical
  step. Handler-level RBAC used for result filtering (`authorizedScopeIDs`,
  `authorizedNamespaces`) is data filtering, not authorization, and stays.
