# Astronomer UI Extension Runtime — Authoritative Design (Tier 1 + Tier 2)

**Status:** authoritative. This is the single source of truth the build agents follow.
**Scope:** the host-side *runtime* that discovers, gates, mounts, and renders **enabled** extensions for a third-party customer ecosystem. It **extends** the existing control plane and **does not rebuild** install / enable / disable / validate / verify-bundle.

## Grounding (verified against the repo)

These facts were confirmed in code and the design is built on them — do not contradict them:

- **Control plane:** `internal/handler/extensions.go` — `ExtensionManifest{APIVersion,Name,DisplayName,Version,CompatibleAstronomer,Entry,Permissions,BackendAPIScopes,CSP,ExtensionPoints}`; `ExtensionCSP{ScriptSrc,ConnectSrc,FrameSrc,ImageSrc}`; points `Sidebar{Label,Path}` / `Widgets{ID,Title}` / `ClusterTabs{Label,Component}` / `Settings{Label,Component}`. `validateExtensionManifest` is the single choke point (apiVersion pinned to `extensions.astronomer.io/v1alpha1`, DNS-label name, semver, `safeExtensionEntry` = relative `.js`, `validExtensionPermission` = `resource:verb`, `validateExtensionCSP` bans `'unsafe-eval'`/`'unsafe-inline'`/`*` in `scriptSrc`). Lifecycle: `Install` only sets `enabled` when `req.Enable && CompatibilityStatus=="compatible"`; `Enable` blocks non-compatible with `IncompatibleExtension`.
- **Signed bundles:** `verifyExtensionBundle(bundle, sig, expectedChecksum, trustedKey)` — Ed25519 over raw bytes + constant-time `sha256:<hex>` checksum compare; **fails closed** when `trustedKey == nil` (`errBundleNoTrustedKey`). Installed via `SetTrustedBundleKey`. `bundleChecksum()` → `"sha256:"+hex`.
- **RBAC:** `engine.CheckPermission(bindings []RoleBinding, resource rbac.Resource, verb rbac.Verb, clusterID, projectID uuid.UUID, namespace ...string) bool` (superuser short-circuit, then global→cluster→project). Resource/Verb vocab in `internal/rbac/types.go` (`ResourceClusters`, `ResourceMonitoring`, `ResourcePods`, `ResourceSettings`, …; `VerbRead/Create/Update/Delete/List`; wildcards exist). `IsCanonicalResource`/`IsCanonicalVerb` validate strings.
- **Middleware:** `requirePermission(engine, querier, resource, verb)` → `appmiddleware.RequirePermission`, which does `user := GetAuthenticatedUser(ctx)` then `bindings := querier.GetUserBindings(ctx, user.ID)` then `CheckPermission`. `requireScope(iauth.ScopeAdmin)` gates admin install routes. Routes wired in `internal/server/routes_cluster_addons.go` under `deps.Extensions`.
- **Short-lived scoped tokens:** two reusable primitives — (a) `JWTManager.GeneratePurposeToken(userID, purpose, ttl)` mints an HS256 `Claims{TokenType:PurposeToken, Purpose}` that the **regular auth middleware refuses as a session** (it rejects any `PurposeToken`); `Claims` today has **no** `Audience` field. (b) `StreamTicketStore` (`internal/auth/stream_tickets.go`) — opaque random token, **sha256-hashed at rest, single-use (deleted on Validate), TTL'd, scope-checked** (`Kind`+`ClusterID`).
- **DB:** `ui_extensions` (migration 100): `manifest JSONB`, `enabled`, `compatibility_status CHECK(compatible|incompatible|unknown)`, index on `(enabled, compatibility_status)`. **Latest migration is 116** → next is **117**.
- **Frontend:** `frontend/src/lib/api/extensions.ts` (typed client), `frontend/src/app/dashboard/extensions/page.tsx` (admin list/install/validate).

## Design split

| | **Tier 1 — Declarative (default, ~80%)** | **Tier 2 — Signed-bundle iframe (escape hatch)** |
|---|---|---|
| Third-party JS in host | **None.** Manifest *describes* a widget; host renders it with first-party React. | Runs, but only inside a cross-origin `sandbox`ed iframe at a per-extension opaque origin. |
| Data access | Through the host **data proxy** (§DataProxy). | Through the **bridge** (§BridgeProtocol) → same data proxy. Never a direct backend call. |
| Trust gate | Manifest validation only (no code to sign). | Existing Ed25519 `verifyExtensionBundle` (fails closed) **+** SRI **+** `bundle_verified` DB gate. |
| Threat surface | A JSON spec rendered as text nodes. | Hostile JS, fully sandboxed, brokered, with a ≤60s scoped token. |

**Tier is derived, not authored:** an extension point is Tier 2 iff its `render.bundle` is present; otherwise Tier 1 (`render.declarative`) — and a point with no `render` is a legacy registry entry that mounts nothing.

**The one invariant that makes the ecosystem safe:** *both* tiers fetch data through **one** proxy that re-runs `engine.CheckPermission` against the **requesting user's own** bindings on **every** call. Effective access = `manifest.permissions[]` ∩ the current user's RBAC. **An extension can never read or write anything the logged-in user couldn't already do in the UI.** The threat model assumes the extension author is hostile.

---

## §Schema — exact manifest additions

All additions are **additive JSONB** inside the existing `manifest` column — **no schema migration to `ui_extensions` for the fields**. `apiVersion` stays `extensions.astronomer.io/v1alpha1` (additions are optional, so old manifests validate unchanged). A new boolean column tracks bundle verification (migration 117, see §BuildPlan).

### Go structs (add to `internal/handler/extensions.go`)

```go
// ExtensionRender is attached per extension point. Exactly one of
// Declarative (Tier 1) or Bundle (Tier 2) is set. Absent => legacy entry,
// mounts nothing.
type ExtensionRender struct {
    Declarative *DeclarativeWidget `json:"declarative,omitempty"` // Tier 1
    Bundle      *BundleDescriptor  `json:"bundle,omitempty"`      // Tier 2
}

// ---- Shared: named, RBAC-allowlisted data source (never a raw URL) --------
type DataSourceRef struct {
    ID      string            `json:"id"`              // referenced by widgets & bridge calls
    Proxy   string            `json:"proxy"`           // host allowlist: "astronomer-api"|"k8s"|"prometheus"
    Method  string            `json:"method"`          // "GET" | "POST"
    Path    string            `json:"path"`            // host template, e.g. "/api/v1/clusters/{clusterId}/pods"
    Query   map[string]string `json:"query,omitempty"` // static defaults; widget may override declared keys only
    RBAC    RBACRequirement   `json:"rbac"`            // re-checked AT CALL TIME against the user
    Shape   string            `json:"shape"`           // "list" | "object" | "series"
    Fields  []string          `json:"fields,omitempty"`// response projection allowlist (dot-paths; "*" rejected)
    MaxRows int               `json:"maxRows,omitempty"`
    CacheTTLSeconds int       `json:"cacheTtlSeconds,omitempty"`
}

type RBACRequirement struct {
    Resource string `json:"resource"` // MUST be IsCanonicalResource (no "*")
    Verb     string `json:"verb"`     // MUST be IsCanonicalVerb (no "*")
    Scope    string `json:"scope"`    // "global" | "cluster" | "project" — which ctx id binds the check
}

// ---- Tier 1: declarative widget (zero third-party JS) ---------------------
type DeclarativeWidget struct {
    Kind       string         `json:"kind"`        // "table" | "chart" | "stat" | "form"
    DataSource string         `json:"dataSource"`  // ref into the point's DataSources[].ID
    Fields     []FieldBinding `json:"fields,omitempty"`
    Chart      *ChartSpec     `json:"chart,omitempty"` // kind=chart
    Form       *FormSpec      `json:"form,omitempty"`  // kind=form
    Stat       *StatSpec      `json:"stat,omitempty"`  // kind=stat
    EmptyText  string         `json:"emptyText,omitempty"`
}

type FieldBinding struct {
    Path   string `json:"path"`             // JSONPath-lite into a proxy row, "metadata.name"
    Label  string `json:"label"`
    Format string `json:"format,omitempty"` // closed enum: text|number|bytes|datetime|duration|badge|currency
}
type ChartSpec struct {
    Type string   `json:"type"` // "line" | "bar" | "area"
    X    string   `json:"x"`
    Y    []string `json:"y"`
}
type StatSpec struct {
    Value FieldBinding  `json:"value"`
    Delta *FieldBinding `json:"delta,omitempty"`
    Label string        `json:"label"`
}
type FormSpec struct {
    Submit      string      `json:"submit"`      // a DataSources[].ID with Method=POST + write verb
    Inputs      []FormInput `json:"inputs"`
    SubmitLabel string      `json:"submitLabel"`
}
type FormInput struct {
    Name     string   `json:"name"`
    Label    string   `json:"label"`
    Type     string   `json:"type"`              // "text"|"number"|"select"|"toggle"
    Options  []string `json:"options,omitempty"` // type=select
    MaxLength int     `json:"maxLength,omitempty"`
    Required bool     `json:"required"`
}

// ---- Tier 2: signed bundle / iframe descriptor ---------------------------
type BundleDescriptor struct {
    URL           string          `json:"url"`           // https; host on operator allowlist
    SHA256        string          `json:"sha256"`        // "sha256:<64hex>" — matches bundleChecksum() format
    Integrity     string          `json:"integrity"`     // SRI "sha384-..." for the iframe <script>
    Signature     string          `json:"signature"`     // base64 Ed25519 over raw bundle; verifyExtensionBundle()
    Entry         string          `json:"entry"`         // relative .js (existing safeExtensionEntry rules)
    SandboxOrigin string          `json:"sandboxOrigin"` // per-extension origin; MUST NOT equal host origin
    Component     string          `json:"component"`     // logical view name passed in handshake (never eval'd by host)
    CSP           ExtensionCSP    `json:"csp"`           // per-iframe CSP; intersected with manifest.CSP
    DataSources   []DataSourceRef `json:"dataSources"`   // the ONLY routes the iframe may request via bridge
}
```

### Wiring into existing points

Each point struct gains `Render *ExtensionRender json:"render,omitempty"` and a `DataSources []DataSourceRef json:"dataSources,omitempty"` (Tier-1 data sources for that point; Tier-2 sources live inside `BundleDescriptor.DataSources`):

```go
type ExtensionWidgetPoint struct {
    ID          string           `json:"id"`
    Title       string           `json:"title"`
    Render      *ExtensionRender `json:"render,omitempty"`
    DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}
// ...identical Render+DataSources additions on ExtensionSidebarPoint,
//    ExtensionClusterTab, ExtensionSettingsPage.
```

For `ClusterTab`/`Settings`, the existing `Component` string is **ignored for Tier 1** (the `Render.Declarative` drives rendering) and becomes the **bridge mount name for Tier 2** (`BundleDescriptor.Component` may override it).

### JSON shape (illustrative — Tier 1 widget + Tier 2 cluster tab)

```jsonc
{
  "apiVersion": "extensions.astronomer.io/v1alpha1",
  "name": "cost-insights",
  "displayName": "Cost Insights",
  "version": "0.2.0",
  "compatibleAstronomer": ">=0.9.0 <1.0.0",
  "entry": "index.js",
  "permissions": ["monitoring:read", "clusters:read"],
  "csp": { "connectSrc": ["'self'"], "imageSrc": ["'self'", "data:"], "frameSrc": ["'self'"] },
  "extensionPoints": {
    "widgets": [{
      "id": "cost-summary",
      "title": "Cost summary",
      "dataSources": [{
        "id": "podCost", "proxy": "astronomer-api", "method": "GET",
        "path": "/api/v1/clusters/{clusterId}/monitoring/cost",
        "query": { "window": "30d" },
        "rbac": { "resource": "monitoring", "verb": "read", "scope": "cluster" },
        "shape": "list", "fields": ["namespace", "usd", "trend"],
        "maxRows": 500, "cacheTtlSeconds": 60
      }],
      "render": { "declarative": {
        "kind": "table", "dataSource": "podCost",
        "fields": [
          { "path": "namespace", "label": "Namespace", "format": "text" },
          { "path": "usd", "label": "Cost (USD)", "format": "currency" }
        ]
      }}
    }],
    "clusterTabs": [{
      "label": "Cost", "component": "ClusterCostTab",
      "render": { "bundle": {
        "url": "https://cdn.vendor.example/cost-insights/0.2.0/bundle.js",
        "sha256": "sha256:9f2b…", "integrity": "sha384-…",
        "signature": "<base64 ed25519>", "entry": "index.js",
        "sandboxOrigin": "https://ext-cost-insights.sandbox.astronomer.local",
        "component": "ClusterCostTab",
        "csp": { "scriptSrc": ["'self'"], "connectSrc": ["'self'"], "frameSrc": ["'none'"], "imageSrc": ["'self'", "data:"] },
        "dataSources": [{ "id": "podCost", "proxy": "astronomer-api", "method": "GET",
          "path": "/api/v1/clusters/{clusterId}/monitoring/cost",
          "rbac": { "resource": "monitoring", "verb": "read", "scope": "cluster" }, "shape": "list" }]
      }}
    }]
  }
}
```

### Validation additions (extend `validateExtensionManifest`, same finding style)

Walk every point's `Render` and `DataSources`:

- **Tier exclusivity:** a `Render` with both `declarative` and `bundle` set → error `"render: a point is either declarative or bundle, not both"`.
- **DataSourceRef:** `rbac.resource` must pass `rbac.IsCanonicalResource` **and not be `"*"`**; `rbac.verb` must pass `IsCanonicalVerb` **and not be `"*"`**; `scope ∈ {global,cluster,project}`; `proxy ∈ {astronomer-api,k8s,prometheus}` (host allowlist); `method ∈ {GET,POST}`. `path` must start `/` (and for `astronomer-api`, `/api/v1/`), contain no `..`, and every `{token}` must be one of the allowed placeholders `{clusterId}`,`{projectId}`,`{namespace}` (reuse `safeExtensionEntry` posture for traversal/scheme rejection). `fields[]` are dot-paths; `*` rejected.
- **RBAC ceiling (install half):** every `DataSourceRef.RBAC` of the form `resource:verb` **MUST appear in `permissions[]`** → else error `"dataSources[i].rbac: not declared in permissions[]"`. (Surfaces in the permission column the admin already sees.)
- **Write sources:** `method=POST` requires a write verb (`create|update|delete`) in `rbac.verb`; only referenceable from a `FormSpec.Submit`.
- **Tier 1:** `kind ∈ {table,chart,stat,form}`; `declarative.dataSource` and `form.submit` must resolve to a declared `DataSources[].ID`; `field.format`/`input.type` are closed enums.
- **Tier 2 bundle:** `sha256` matches `^sha256:[0-9a-f]{64}$`; `integrity` matches `^sha384-`; `signature` base64-decodes; `url` scheme `https` with host on the operator allowlist; `sandboxOrigin` is an absolute `https` origin that **MUST NOT equal the host origin**; `entry` passes `safeExtensionEntry`. A Tier-2 point whose effective CSP lacks `frameSrc` → error `"bundle requires csp.frameSrc"`. Bundle `csp.connectSrc` must not contain `*` or any `/api/` host (**error**, not warning) — Tier-2 code makes **no** direct backend calls. Reuse `validateExtensionCSP` for the `scriptSrc` bans.
- **Fail-closed signing:** if any point has a `bundle` **and** `h.trustedKey == nil`, emit warning `"executable bundles gated: no trusted key"` and force `enabled=false` at install (mirrors existing posture). `bundle_verified` stays false until `verify-bundle` succeeds for that descriptor.

---

## §DataProxy — Tier-1 data-proxy endpoint contract

One new endpoint. The browser (Tier 1) and the bridge (Tier 2) name a **dataSource id**, never a URL. The proxy re-derives everything from the **stored, validated** manifest of the **enabled** extension and the **requesting user's own** RBAC bindings.

### Route (add in `routes_cluster_addons.go`, under `deps.Extensions`)

```go
// Tenant-user data path — NOT ScopeAdmin. ResourceSettings:read just gates
// "may use the extensions surface at all"; the real check is per-dataSource below.
r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).
  Post("/extensions/{name}/data/{dataSourceId}/", deps.Extensions.ProxyData)
```

### Request

```
POST /api/v1/extensions/{name}/data/{dataSourceId}/
Auth: the user's normal browser session (Tier 1)  OR  X-Extension-Ticket: <opaque ticket> (Tier 2 bridge, §BridgeProtocol)
```
```jsonc
{
  "context": { "clusterId": "uuid|null", "projectId": "uuid|null", "namespace": "string|null" },
  "pathParams": { "clusterId": "<uuid>" },     // validated against the path template placeholders only
  "query": { "window": "7d" },                 // only keys declared in DataSourceRef.Query are kept
  "body": { }                                  // only for method=POST form submit, against declared input schema
}
```

### Server algorithm (`ProxyData`) — fail closed at each step, in order

1. **Load + gate the extension** by `{name}` from `ui_extensions`. Reject if not found, `enabled=false`, or `compatibility_status != "compatible"` → `409 incompatible_extension`. The **stored** manifest is the source of truth; never trust a client-supplied manifest.
2. **Resolve `{dataSourceId}`** in that point's `DataSources` (or, for a ticket-authed Tier-2 call, in `BundleDescriptor.DataSources`). Unknown id → `404`. **This is the allowlist** — an extension can only reach routes it shipped in its signed/stored manifest.
3. **Authenticate the caller.** Session cookie → `user := GetAuthenticatedUser(ctx)`. Or `X-Extension-Ticket` → `ExtensionTicketStore.Validate(token, name, dataSourceId, clusterID)` (single-use, ≤60s; yields `userID`). Either way we now have a concrete user identity. The extension has **no identity of its own**.
4. **RBAC under the user's bindings** — the load-bearing line:
   ```go
   bindings, _ := deps.RBACQueries.GetUserBindings(ctx, userID)        // the USER, not the extension
   if !engine.CheckPermission(bindings,
        rbac.Resource(ds.RBAC.Resource), rbac.Verb(ds.RBAC.Verb),
        clusterID, projectID, namespace) {                            // ids per ds.RBAC.Scope
       respond(403, "extension_rbac_denied")
   }
   ```
   Same engine, same bindings, same call as `appmiddleware.RequirePermission`. The manifest `permissions[]` is the **ceiling**, the user's RBAC is the **actual grant**, effective access is the **intersection**.
5. **Re-assert the install invariant** `ds.RBAC ∈ permissions[]` at runtime (defends against a manifest mutated in the DB out of band).
6. **Build the upstream URL server-side only.** Fill `{clusterId}/{projectId}/{namespace}` from validated `context`/`pathParams` (UUIDs parsed, namespace regex-checked); merge `query` overrides for declared keys only, dropping unknowns. **No client field can redirect the upstream** — no SSRF, no path traversal.
7. **Dispatch in-process** to the same internal handler the UI already uses (`proxy ∈ astronomer-api|k8s|prometheus`), reusing that handler's own RBAC middleware as a **second** gate. Project the response to `ds.Fields`, truncate to `ds.MaxRows`, apply `ds.CacheTTLSeconds` with a **per-(user,params)** cache key (no cross-user poisoning).
8. **Audit + rate-limit.** `recordAudit(r, h.auditor, "extension.data.proxied", "ui_extension", id, name, {dataSourceId, resource, verb, clusterId, allowed})`; on deny emit `extension.data.denied`. Per-(user,extension) rate limit via the existing API rate-limit middleware.

### Response

```jsonc
{ "data": { "rows": [ /* projected per fields[] */ ] },
  "shape": "list",
  "meta": { "dataSourceId": "podCost", "rows": 42, "rbacScope": "cluster",
            "cached": true, "ttlSeconds": 60, "truncated": false } }
```
Errors use the existing `apierror` envelope. **Writes** (`method=POST` form submit) run the identical pipeline, additionally require the host's CSRF/double-submit, and are restricted to a `FormSpec.Submit` dataSource — no arbitrary mutation.

**Why it cannot exceed the user:** two independent gates — (a) caller RBAC via `CheckPermission` on the caller's own bindings, (b) the manifest allowlist (server-fixed path + verb). The manifest only *narrows*; it can never grant.

---

## §BridgeProtocol — Tier-2 iframe postMessage protocol

The bundle runs in `sandbox="allow-scripts"` (no `allow-same-origin` ⇒ **opaque origin, no host cookies/localStorage/DOM, no credentialed calls, no top-navigation**) served from `bundle.sandboxOrigin` (a per-extension origin, never the host origin). The **only** egress is `postMessage` to the host. All data ultimately flows through §DataProxy.

### Envelope

```ts
interface BridgeMsg {
  astronomerBridge: true;  // discriminator — drop any message lacking it
  v: 1;                    // protocol version
  ext: string;             // extension name (must equal the mounted extension)
  mount: string;           // point id / component name
  type: string;            // see message table
  id?: string;             // correlation id for request/response pairs
  payload?: unknown;
}
```

### Origin checks (both directions, non-negotiable, no `"*"` on inbound trust)

- **Host → iframe:** `iframe.contentWindow.postMessage(msg, EXPECTED_IFRAME_ORIGIN)` (the exact `sandboxOrigin`), never `"*"`.
- **Iframe → host:** the host `message` listener accepts **only** when `event.source === iframe.contentWindow` **&&** `event.origin === EXPECTED_IFRAME_ORIGIN` **&&** `msg.astronomerBridge === true` **&&** `msg.ext === <mounted name>`. Anything else → drop + `recordAudit("extension.bridge.rejected", …)`.
- **Iframe SDK → host:** symmetric — pins the `hostOrigin` delivered in the handshake and ignores all other senders.
- Unknown `type`, bad `v`, mismatched/unsolicited `id`, oversize payload (hard caps per type) → dropped + audited.

### Handshake sequence

The iframe is inert until the host speaks first (it doesn't know the host origin until told, then freezes it).

```jsonc
// 1. host -> iframe  (after iframe load)
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab", "type": "host/hello",
  "payload": { "hostOrigin": "https://app.astronomer.local",
               "extension": { "name": "cost-insights", "version": "0.2.0", "manifestSha": "sha256:…" },
               "mount": { "point": "clusterTab", "component": "ClusterCostTab",
                          "context": { "clusterId": "<uuid>", "projectId": "<uuid>", "namespace": "team-a" } },
               "capabilities": ["data", "navigate", "theme"],
               "dataSources": ["podCost"],          // ids the iframe may request — the handshake allowlist
               "theme": { "mode": "dark", "tokens": { "--background": "…", "--primary": "…" } } } }

// 2. iframe -> host
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab", "type": "ext/ready",
  "payload": { "sdkVersion": "1.0.0", "acceptsProtocol": [1], "manifestSha": "sha256:…" } }
// host verifies manifestSha === installed; if acceptsProtocol omits host v -> render "incompatible SDK" placeholder + teardown.
```

`compatibleAstronomer` gates the *Astronomer* version; `acceptsProtocol` gates the *bridge* version independently.

### Theme (push, on handshake + on every host theme toggle)

```jsonc
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab", "type": "host/theme",
  "payload": { "mode": "light", "tokens": { "--background": "…", "--foreground": "…", "--primary": "…",
                                            "--muted": "…", "--radius": "…", "--font-sans": "…" } } }
```
The SDK applies tokens as CSS variables so a zero-config bundle matches host theming.

### Scoped-token issuance (the iframe NEVER gets the session JWT)

Modeled on `StreamTicketStore`: a new **`ExtensionTicketStore`** (opaque random token, **sha256-hashed at rest, single-use, ≤60s TTL, scope-checked**). The iframe asks; the host issues a ticket **only** when the `dataSourceId` is in the handshake allowlist **and** `CheckPermission` passes for the current user (same gate as §DataProxy step 4).

```jsonc
// iframe -> host
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab",
  "id": "r1", "type": "ext/token.request", "payload": { "dataSource": "podCost" } }
// host -> iframe
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab",
  "id": "r1", "type": "host/token.grant",
  "payload": { "token": "<opaque>", "dataSource": "podCost", "expiresAt": "2026-06-25T12:00:60Z",
               "scope": "ext:cost-insights:data:podCost" } }
```

**Ticket scope/TTL:** bound to `{userID, extension, dataSourceId, clusterID}`, TTL **≤ 60s**, **single-use** (deleted on `Validate`). The iframe sends it as `X-Extension-Ticket` to `POST /api/v1/extensions/{name}/data/{dataSourceId}/`; `ProxyData` validates+consumes it and **re-derives RBAC from `userID` on every call** — the ticket proves "this user, for this extension+dataSource, briefly," and grants **no permission of its own**. A leaked ticket is good for ≤60s, one call, one dataSource, one user. (Rationale for the ticket-store over `GeneratePurposeToken`: single-use + at-rest hashing + scope-to-dataSource match the third-party threat model better than a replayable bearer JWT; `Claims` also has no `Audience` field today, so a JWT path would require extending the claims struct. `GeneratePurposeToken` remains the fallback if a stateless token is later required.)

### Data request / response

```jsonc
// iframe -> host (host runs §DataProxy on the iframe's behalf with the ticket)
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab",
  "id": "r2", "type": "ext/data.request", "payload": { "dataSource": "podCost", "query": { "window": "7d" }, "body": null } }
// host -> iframe (ok)
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab",
  "id": "r2", "type": "host/data.response", "payload": { "ok": true, "data": { "rows": [ … ] }, "shape": "list", "meta": { "cached": false } } }
// host -> iframe (deny)
{ "astronomerBridge": true, "v": 1, "ext": "cost-insights", "mount": "ClusterCostTab",
  "id": "r2", "type": "host/data.response", "payload": { "ok": false, "error": { "code": "extension_rbac_denied" } } }
```
`ext/data.request` is rejected for any `dataSource` outside the handshake `dataSources` allowlist.

### Navigation, resize, lifecycle

```jsonc
{ "astronomerBridge": true, "v": 1, "type": "ext/navigate", "payload": { "to": "/dashboard/clusters/{id}", "params": { "id": "<uuid>" } } }
// host validates: `to` starts with "/dashboard/", no scheme, no "//", and the user holds RBAC for the target route -> router.push; else refuse.
// External links: SDK openExternal(url) -> host renders a user-confirmed <a target=_blank rel="noopener noreferrer">.

{ "astronomerBridge": true, "v": 1, "type": "ext/resize", "payload": { "height": 640 } }   // host clamps min/max
{ "astronomerBridge": true, "v": 1, "type": "ext/toast",  "payload": { "level": "info", "message": "<=140 chars, text-only" } }
{ "astronomerBridge": true, "v": 1, "type": "host/teardown" }   // host unmounting; iframe should flush
```
The iframe **cannot** initiate top-level navigation (sandbox denies it); `ext/navigate` only routes within the host allowlist.

---

## §HostMounts — host-side React architecture

### Enabled-extensions endpoint (viewer-readable, render-only)

```
GET /api/v1/extensions/mounts/   ->  { sidebar:[…], dashboardWidgets:[…], clusterTabs:[…], settings:[…] }
```
Gated by `requirePermission(ResourceSettings, VerbRead)` (any viewer, not admin). Each entry:
```jsonc
{ "extension": "cost-insights", "displayName": "Cost Insights", "point": "clusterTab",
  "pointId": "ClusterCostTab", "tier": 1,
  "render": { /* declarative widget OR bundle descriptor (url, sandboxOrigin, sha256, csp, dataSource ids) */ },
  "dataSources": [ /* ids + shapes only — no upstream paths leaked to the browser */ ] }
```
**Returned only when** `enabled && compatibility_status=="compatible" && (tier==1 || bundle_verified)` — the loader never sees an unverified bundle. Implemented as a filtered projection of the stored manifest (no install internals). Cached client-side via React Query (`queryKeys.extensions.mounts`).

### New frontend modules

```
frontend/src/lib/extensions/registry.ts        // useEnabledExtensions(): point -> [mount], indexed by point
frontend/src/lib/extensions/proxy.ts            // fetchExtensionData(name, dataSourceId, ctx) -> POST /extensions/{name}/data/{id}/
frontend/src/components/extensions/
  ExtensionProvider.tsx     // context: loads /mounts/, exposes registry + host theme tokens + nav guard
  ExtensionSlot.tsx         // the ONE integration point host pages add (per mount location)
  DeclarativeWidget.tsx     // switch(kind){table->ExtTable, chart->ExtChart, stat->ExtStat, form->ExtForm}
  SandboxedExtension.tsx    // sandboxed iframe + §BridgeProtocol (origin checks, handshake, ticket broker, teardown)
  ExtTable.tsx / ExtChart.tsx / ExtStat.tsx / ExtForm.tsx   // first-party, TEXT-ONLY renderers over @/components/ui
```

- **`ExtensionProvider`** wraps the dashboard shell once, fetches `/mounts/`, builds the registry index `{ sidebar:[], dashboardWidget:{id->…}, clusterTab:[], settingsPage:[] }`, subscribes to the host theme, and owns the navigation allowlist.
- **`ExtensionSlot`** is the single line a host page adds: `<ExtensionSlot point=… context={{clusterId,projectId,namespace}} />`. It reads the registry, picks Tier-1 vs Tier-2 per mount, supplies `context` from the current route, and wraps **each** extension in an **error boundary** + render timeout so a broken/hostile extension degrades to a placeholder and can never crash the host shell.
  - `tier===1` → `<DeclarativeWidget>` — fetches via §DataProxy (React Query key `['ext', name, dataSourceId, context]`), renders `table|chart|stat|form` with first-party `@/components/ui` primitives. Values rendered as **text nodes only** — no `dangerouslySetInnerHTML`. Zero third-party JS.
  - `tier===2` → `<SandboxedExtension>` — builds the `sandbox="allow-scripts"` iframe at `bundle.sandboxOrigin`, owns the bridge (handshake, theme subscription, ticket broker, data broker, nav guard, teardown on unmount).

### The 4 mount points

| Point | Host location | Tier 1 | Tier 2 |
|---|---|---|---|
| `sidebar{label,path}` | nav list appends items from `registry.sidebar`; route under `/dashboard/extensions/{name}` (validator already forces this prefix) | full-page `DeclarativeWidget` | full-page `SandboxedExtension` |
| `dashboardWidget{id,title}` | dashboard grid: `<ExtensionSlot point="dashboardWidget" />` appends cards | `DeclarativeWidget` matching `widgets[id]` | `SandboxedExtension` |
| `clusterTab{label,component}` | cluster detail tab bar: `<ExtensionSlot point="clusterTab" context={{clusterId}} />` after built-in tabs | declarative widget, `context.clusterId` injected | `SandboxedExtension`, `clusterId` in handshake |
| `settingsPage{label,component}` | settings nav: `<ExtensionSlot point="settingsPage" />` | declarative form/table | `SandboxedExtension` |

For Tier 2, `component` is only a logical view name carried in `host/hello.mount.component` — **the host never `eval`s it**.

### `SandboxedExtension` iframe (host-built `srcdoc`, never extension-built)

```html
<iframe
  src="https://ext-cost-insights.sandbox.astronomer.local/index.html"
  sandbox="allow-scripts"            <!-- NO allow-same-origin / top-navigation / forms / popups -->
  referrerpolicy="no-referrer"
  allow=""                           <!-- deny all powerful features -->
  loading="lazy">
```
The bundle is served from `sandboxOrigin` with the per-extension CSP as a **response header** and the entry `<script integrity="{bundle.integrity}" crossorigin>`. The SRI hash **and** the row's `bundle_verified` gate must both pass or the script never executes.

### Local-dev story

An env-gated dev source lets an author point at `http://localhost:5173`, force `compatibility_status=compatible`, and run the bridge in `devUnsignedBundle` mode (signature check bypassed **only** behind a dev flag — production install still runs `verifyExtensionBundle`). Ship a published `@astronomer/extension-sdk` implementing the iframe side (handshake/theme/data/navigate) and a `manifest validate` CLI hitting `POST /extensions/validate/`.

---

## §Security — model, mitigations, fail-closed defaults

**Trust gates (install → enable → mount → render):** signed+checksummed bundle vs `trustedKey` (`verifyExtensionBundle`, fails closed when `trustedKey==nil`, already built) → manifest CSP validated (`'unsafe-eval'`/`'unsafe-inline'`/`*` script rejected, already built) → `enabled && compatible` → `bundle_verified` gate in `/mounts/` → per-render origin + per-call ticket checks. An unsigned/tampered Tier-2 bundle can never reach `enabled=true` or appear in `/mounts/`.

| Vector | Control |
|---|---|
| Extension exceeds the user's data access | §DataProxy re-runs `engine.CheckPermission` with the **caller's** `GetUserBindings` — same engine/bindings as `appmiddleware.RequirePermission`. Manifest RBAC narrows only. Effective = `permissions[]` ∩ user RBAC, every call. |
| Extension hits an arbitrary endpoint (SSRF/traversal) | Proxy serves only `DataSourceRef`s in the **stored** manifest; URL built from a **server-side** template; no client field redirects it; `proxy ∈ {astronomer-api,k8s,prometheus}`; `..`/scheme/host rejected at validation. |
| Third-party JS touches host DOM/cookies | Tier 2 = `sandbox="allow-scripts"` only ⇒ opaque per-extension origin, no `allow-same-origin`, no cookies/localStorage/DOM, no top-nav. Tier 1 runs **no** third-party JS. One origin per extension ⇒ no cross-extension storage/message bleed. |
| Unsigned/tampered bundle executes | `verifyExtensionBundle` (Ed25519, fails closed) **+** SRI `integrity` (sha384) on the iframe `<script>` **+** `bundle_verified` DB gate. Two independent hash checks; any mismatch ⇒ no mount. |
| Long-lived credential in the iframe | Session JWT **never** crosses the bridge. `ExtensionTicketStore`: opaque, sha256-hashed at rest, **single-use**, **≤60s** TTL, scoped to `{user,extension,dataSourceId,cluster}`; accepted only by the data endpoint; RBAC re-checked at use. Worst case: what the *current user* can do, on *declared* sources, for ≤60s. |
| postMessage spoofing | Host trusts only `event.source===iframe.contentWindow && event.origin===sandboxOrigin && astronomerBridge===true && ext===mounted`. Iframe pins injected `hostOrigin`. No `"*"` inbound. Correlation `id` required; unknown `type`/oversize dropped + `extension.bridge.rejected`. |
| CSP escape / exfiltration | `validateExtensionCSP` bans `'unsafe-eval'`/`'unsafe-inline'`/`*` in `scriptSrc`. Per-iframe CSP **intersected** with manifest CSP; bundle `connectSrc` may not be `*` or any `/api/` host (**error**). `connectSrc 'self'` = the iframe origin only; all data via the brokered bridge — direct exfil is a CSP violation with no network path anyway. Tier-2 without `frameSrc` rejected at validation. |
| Tier-1 XSS | No third-party JS, no `dangerouslySetInnerHTML`; cell/series/stat values rendered as **text nodes** through closed-enum formatters. |
| Privilege via navigation | `ext/navigate` validated host-side against `/dashboard/*` prefix + the user's route RBAC before `router.push`; iframe can't self-navigate the top frame. |
| Blast radius | Per-extension error boundaries + render timeouts; `Disable` (existing) instantly drops the mount from `/mounts/` and stops ticket issuance; ≤60s ticket TTL bounds a leaked grant. |

**Fail-closed defaults preserved:** no trusted key ⇒ no Tier-2 mount (warning + `enabled=false`); incompatible version ⇒ existing enable block (`IncompatibleExtension`) + proxy `409`; unknown dataSource ⇒ `404`; missing user RBAC ⇒ `403`.

**Auditing (reuse `recordAudit`):** install/enable/disable already audited; add `extension.data.proxied`, `extension.data.denied`, `extension.token.issued`, `extension.bridge.rejected`.

**Residual risks to flag to operators:** (a) hostile CDN swapping bytes — mitigated by `sha256`+signature+SRI pinned in the signed manifest, re-verifiable; (b) a careless admin enabling an over-broad-`permissions` extension — surfaced in the existing permission column at install, still bounded by per-user RBAC at runtime; (c) bridge DoS via message floods — per-extension rate-limit + payload caps.

---

## §BuildPlan — ordered, file-level work breakdown

### Phase 0 — DB (1 migration)
1. **`internal/db/migrations/117_ui_extension_bundle_verified.up.sql`** (+ `.down.sql`): `ALTER TABLE ui_extensions ADD COLUMN bundle_verified BOOLEAN NOT NULL DEFAULT false;`. Set `true` only after `verify-bundle` succeeds for a Tier-2 descriptor. (No change for the new JSONB manifest fields — they live in the existing `manifest` column.) Add `migration_117_test.go` mirroring the existing migration tests.

### Phase 1 — Backend (extend, don't rebuild)
2. **`internal/handler/extensions.go`** — add `ExtensionRender`, `DataSourceRef`, `RBACRequirement`, `DeclarativeWidget`, `FieldBinding`, `ChartSpec`, `StatSpec`, `FormSpec`, `FormInput`, `BundleDescriptor`; add `Render`+`DataSources` to the 4 point structs; extend `validateExtensionManifest` (§Schema validation rules); flip install to require `verifyExtensionBundle` success (set `bundle_verified`) before a Tier-2 extension can be enabled; extend `normalizeExtensionManifest`/`sampleExtensionManifest`.
3. **`internal/handler/extension_proxy.go`** (new) — `ProxyData` (§DataProxy algorithm) and `Mounts` (filtered `/mounts/` projection). Add `GetUserBindings`/`RBACEngine` deps to `ExtensionHandler`.
4. **`internal/auth/extension_tickets.go`** (new) — `ExtensionTicketStore` copied from `stream_tickets.go`, scoped to `{UserID,Extension,DataSourceID,ClusterID}`, TTL 60s, single-use, sha256-hashed. Add `extension_tickets_test.go`.
5. **`internal/handler/extension_bridge.go`** (new) — ticket issuance handler backing `ext/token.request` (RBAC-gated `Issue`); `extension.token.issued` audit.
6. **`internal/server/routes_cluster_addons.go`** — register `GET /extensions/mounts/` (`ResourceSettings:read`), `POST /extensions/{name}/data/{dataSourceId}/` (`ResourceSettings:read`, **no** `ScopeAdmin`), and the ticket-issuance route. Wire the new deps (`RBACEngine`, `RBACQueries`, `ExtensionTicketStore`) into `ExtensionHandler` construction.
7. **`internal/auth/jwt.go`** — *optional fallback only*: a thin `purpose="ext-data"` wrapper over `GeneratePurposeToken` if a stateless token path is later needed. Primary path is the ticket store; do **not** add an `Audience` field unless that fallback is chosen.

### Phase 2 — Frontend
8. **`frontend/src/lib/api/extensions.ts`** — add types `ExtensionRender`, `DataSourceRef`, `DeclarativeWidget`, `BundleDescriptor`, `ExtensionMountsResponse`; add `getExtensionMounts()` and `fetchExtensionData(name, dataSourceId, ctx)`.
9. **`frontend/src/lib/extensions/registry.ts`** + **`proxy.ts`** — `useEnabledExtensions()` (React Query over `/mounts/`, indexed by point) and `fetchExtensionData`.
10. **`frontend/src/components/extensions/`** — `ExtensionProvider.tsx`, `ExtensionSlot.tsx`, `DeclarativeWidget.tsx`, `SandboxedExtension.tsx` (+ bridge), `ExtTable.tsx`/`ExtChart.tsx`/`ExtStat.tsx`/`ExtForm.tsx`.
11. **Wire `<ExtensionSlot>` at 4 sites** — dashboard sidebar nav, dashboard widget grid, cluster-detail tab bar, settings nav; mount `<ExtensionProvider>` in the dashboard shell.

### Phase 3 — Ship order
- **Tier 1 first** (steps 1–3, 6, 8–11 minus `SandboxedExtension`): declarative table/chart/stat/form at all 4 mount points, fully RBAC-fenced through the proxy. Shippable with **no** iframe/ticket/bridge.
- **Tier 2 second** (steps 4–5, `SandboxedExtension`+bridge): iframe + ticket + bridge on top, behind the existing signing gate.

### Reused unchanged
`ui_extensions` table + install/enable/disable/validate lifecycle, `verifyExtensionBundle`/`SetTrustedBundleKey`, `rbac.Engine`/`GetUserBindings`, `requirePermission`/`requireScope`, `StreamTicketStore` pattern, `recordAudit`, the admin `extensions/page.tsx`.
