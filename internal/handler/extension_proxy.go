package handler

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// §HostMounts — enabled-extensions endpoint.
//
// GET /api/v1/extensions/mounts/ returns the render-only projection the host
// loader needs to mount enabled extensions: per-point manifests, mount points,
// and the derived tier (1 = declarative, 2 = signed bundle). It is a filtered
// projection of the stored manifest — no install internals, no upstream
// dataSource paths — gated by ResourceSettings:read so any viewer (not just an
// admin) may render the extension surface.
//
// An entry is returned only when the extension is enabled, compatible, and
// either Tier 1 or a verified Tier-2 bundle (bundle_verified). A point with no
// render mounts nothing and is skipped. This is the single gate that keeps an
// unverified or disabled extension out of the loader.

// ExtensionMount is one mountable extension point, render-only.
type ExtensionMount struct {
	Extension   string                 `json:"extension"`
	DisplayName string                 `json:"displayName"`
	Point       string                 `json:"point"`   // sidebar|dashboardWidget|clusterTab|settingsPage
	PointID     string                 `json:"pointId"` // widget id, sidebar path, or component name
	Title       string                 `json:"title,omitempty"`
	Tier        int                    `json:"tier"` // 1 = declarative, 2 = signed bundle
	Render      *ExtensionRender       `json:"render"`
	DataSources []ExtensionMountSource `json:"dataSources"`
}

// ExtensionMountSource exposes only a dataSource id + shape to the browser. The
// upstream proxy/method/path/rbac stay server-side (the proxy re-derives them
// from the stored manifest) so nothing internal leaks to the loader.
type ExtensionMountSource struct {
	ID    string `json:"id"`
	Shape string `json:"shape"`
}

// ExtensionMountsResponse is the §HostMounts payload, indexed by mount point.
type ExtensionMountsResponse struct {
	Sidebar          []ExtensionMount `json:"sidebar"`
	DashboardWidgets []ExtensionMount `json:"dashboardWidgets"`
	ClusterTabs      []ExtensionMount `json:"clusterTabs"`
	Settings         []ExtensionMount `json:"settings"`
}

// Mounts returns the enabled-extensions projection for the host loader.
func (h *ExtensionHandler) Mounts(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	rows, err := h.queries.ListUIExtensions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list extensions")
		return
	}
	resp := ExtensionMountsResponse{
		Sidebar:          []ExtensionMount{},
		DashboardWidgets: []ExtensionMount{},
		ClusterTabs:      []ExtensionMount{},
		Settings:         []ExtensionMount{},
	}
	for _, row := range rows {
		// Gate: only enabled + compatible extensions can mount. Tier-2 points
		// additionally require a verified bundle.
		if !row.Enabled || row.CompatibilityStatus != "compatible" {
			continue
		}
		var manifest ExtensionManifest
		if json.Unmarshal(row.Manifest, &manifest) != nil {
			continue
		}
		display := manifest.DisplayName
		if display == "" {
			display = manifest.Name
		}
		appendMounts(&resp, manifest, display, row.BundleVerified)
	}
	RespondJSON(w, http.StatusOK, resp)
}

// appendMounts projects every renderable point of one extension into the
// response, skipping legacy (no-render) points and unverified Tier-2 bundles.
func appendMounts(resp *ExtensionMountsResponse, m ExtensionManifest, display string, bundleVerified bool) {
	for _, p := range m.ExtensionPoints.Sidebar {
		if mount, ok := buildMount(m.Name, display, "sidebar", p.Path, "", p.Render, p.DataSources, bundleVerified); ok {
			resp.Sidebar = append(resp.Sidebar, mount)
		}
	}
	for _, p := range m.ExtensionPoints.Widgets {
		if mount, ok := buildMount(m.Name, display, "dashboardWidget", p.ID, p.Title, p.Render, p.DataSources, bundleVerified); ok {
			resp.DashboardWidgets = append(resp.DashboardWidgets, mount)
		}
	}
	for _, p := range m.ExtensionPoints.ClusterTabs {
		if mount, ok := buildMount(m.Name, display, "clusterTab", p.Component, p.Label, p.Render, p.DataSources, bundleVerified); ok {
			resp.ClusterTabs = append(resp.ClusterTabs, mount)
		}
	}
	for _, p := range m.ExtensionPoints.Settings {
		if mount, ok := buildMount(m.Name, display, "settingsPage", p.Component, p.Label, p.Render, p.DataSources, bundleVerified); ok {
			resp.Settings = append(resp.Settings, mount)
		}
	}
}

// buildMount derives the tier and render-only projection for one point. It
// returns ok=false for a legacy point (no render) or an unverified Tier-2
// bundle — neither of which the loader may mount.
func buildMount(name, display, point, pointID, title string, render *ExtensionRender, dataSources []DataSourceRef, bundleVerified bool) (ExtensionMount, bool) {
	if render == nil || (render.Declarative == nil && render.Bundle == nil) {
		return ExtensionMount{}, false // legacy entry: mounts nothing
	}
	tier := 1
	sources := dataSources
	projected := render
	if render.Bundle != nil {
		tier = 2
		// Fail closed: an unverified bundle never reaches the loader.
		if !bundleVerified {
			return ExtensionMount{}, false
		}
		// Tier-2 dataSources live inside the bundle descriptor.
		sources = render.Bundle.DataSources
		// Project the descriptor so the browser sees url/sandboxOrigin/
		// sha256/integrity/csp/component but NOT the upstream dataSource
		// paths/methods/rbac — those stay server-side for the proxy.
		bundle := *render.Bundle
		bundle.DataSources = nil
		projected = &ExtensionRender{Bundle: &bundle}
	}
	return ExtensionMount{
		Extension:   name,
		DisplayName: display,
		Point:       point,
		PointID:     pointID,
		Title:       title,
		Tier:        tier,
		Render:      projected,
		DataSources: mountSources(sources),
	}, true
}

// mountSources strips a dataSource down to its id + shape; the upstream
// path/method/rbac never crosses to the browser.
func mountSources(in []DataSourceRef) []ExtensionMountSource {
	out := make([]ExtensionMountSource, 0, len(in))
	for _, ds := range in {
		out = append(out, ExtensionMountSource{ID: ds.ID, Shape: ds.Shape})
	}
	return out
}

// §DataProxy — the Tier-1 data-proxy endpoint.
//
// POST /api/v1/extensions/{name}/data/{dataSourceId}/
//
// It fetches a manifest-DECLARED data source on behalf of a declarative widget
// (Tier 1) or, with an X-Extension-Ticket, the sandboxed bridge (Tier 2). The
// call is enforced under TWO independent gates that the extension can only
// NARROW, never widen:
//
//   - the REQUESTING USER's own RBAC (engine.CheckPermission over the user's own
//     bindings — the same engine/bindings as appmiddleware.RequirePermission), and
//   - the manifest allowlist (the dataSource must be declared in the STORED,
//     validated manifest of the enabled+compatible extension; the upstream URL is
//     rebuilt server-side from that manifest, so no client field can redirect it).
//
// Effective access = manifest.permissions[] ∩ the user's RBAC, re-derived on
// every call. An extension can never read or write anything the logged-in user
// couldn't already do in the UI.

// extProxyRequest is the request body. The client names a dataSource id (in the
// path) — never a URL — and supplies only context ids, declared query overrides,
// and (for a POST form submit) a body. The proxy validates each against the
// stored DataSourceRef and discards anything not declared.
type extProxyRequest struct {
	Context struct {
		ClusterID string `json:"clusterId"`
		ProjectID string `json:"projectId"`
		Namespace string `json:"namespace"`
	} `json:"context"`
	PathParams map[string]string `json:"pathParams"`
	Query      map[string]string `json:"query"`
	Body       json.RawMessage   `json:"body"`
}

type extProxyResponse struct {
	Data  any          `json:"data"`
	Shape string       `json:"shape"`
	Meta  extProxyMeta `json:"meta"`
}

type extProxyMeta struct {
	DataSourceID string `json:"dataSourceId"`
	Rows         int    `json:"rows"`
	RBACScope    string `json:"rbacScope"`
	Cached       bool   `json:"cached"`
	TTLSeconds   int    `json:"ttlSeconds,omitempty"`
	Truncated    bool   `json:"truncated"`
}

// extNamespaceRE guards the namespace path placeholder (DNS-label, no traversal).
var extNamespaceRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ProxyData implements the §DataProxy algorithm, failing closed at each step.
func (h *ExtensionHandler) ProxyData(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil || h.engine == nil || h.bindings == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension data proxy is not configured")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	dataSourceID := strings.TrimSpace(chi.URLParam(r, "dataSourceId"))
	if !extensionNameRE.MatchString(name) || dataSourceID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Invalid extension or data source")
		return
	}

	var req extProxyRequest
	if r.Body != nil {
		// An empty body is valid (GET-style source with no overrides).
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}

	// Step 1 — load + gate the extension from the STORED manifest.
	row, err := h.findExtension(r.Context(), name)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Extension not found")
		return
	}
	if !row.Enabled || row.CompatibilityStatus != "compatible" {
		RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Extension is not enabled or compatible")
		return
	}
	var manifest ExtensionManifest
	if json.Unmarshal(row.Manifest, &manifest) != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Extension manifest is unreadable")
		return
	}

	// Step 2 — resolve the dataSource id in the stored manifest. This IS the
	// allowlist: an extension can only reach a route it shipped in its manifest.
	// A Tier-2 ticket-authed call additionally requires the source to be a
	// bundle dataSource (isBundle); a browser-session call requires a Tier-1
	// point dataSource.
	ticket := strings.TrimSpace(r.Header.Get("X-Extension-Ticket"))
	ds, isBundle, found := findDataSource(manifest, dataSourceID, ticket != "")
	if !found {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Unknown data source")
		return
	}

	// Step 3 — authenticate the caller to a concrete user id. The extension has
	// no identity of its own.
	userID, clusterID, ok := h.resolveCaller(r, req, ds, name, dataSourceID, ticket, isBundle, w)
	if !ok {
		return
	}
	projectID := parseUUIDOrNil(req.Context.ProjectID)
	if v, okp := req.PathParams["projectId"]; okp {
		projectID = parseUUIDOrNil(v)
	}
	namespace := strings.TrimSpace(req.Context.Namespace)

	// Scope ids per ds.RBAC.Scope — only the id the scope binds participates in
	// the check (a cluster-scoped source checks clusterID, etc.).
	scopeCluster, scopeProject := scopeIDs(ds.RBAC.Scope, clusterID, projectID)

	// Step 4 — RBAC under the USER's own bindings. The load-bearing line: same
	// engine, same bindings, same call as appmiddleware.RequirePermission.
	bindings, err := h.bindings.GetUserBindings(r.Context(), userID.String())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to read user permissions")
		return
	}
	allowed := h.engine.CheckPermission(bindings, rbac.Resource(ds.RBAC.Resource), rbac.Verb(ds.RBAC.Verb), scopeCluster, scopeProject, namespace)

	// Step 5 — re-assert the install invariant ds.RBAC ∈ permissions[] at
	// runtime (defends against a manifest mutated in the DB out of band).
	if allowed && !permissionSet(manifest.Permissions)[ds.RBAC.Resource+":"+ds.RBAC.Verb] {
		allowed = false
	}

	if !allowed {
		recordAuditAs(r, h.auditor, currentUserUUID(r), "extension.data.denied", "ui_extension", row.ID.String(), name, map[string]any{
			"dataSourceId": dataSourceID, "resource": ds.RBAC.Resource, "verb": ds.RBAC.Verb,
			"clusterId": clusterID.String(), "allowed": false,
		})
		RespondRequestError(w, r, http.StatusForbidden, apierror.ExtensionRBACDenied, "Your permissions do not allow this extension data source")
		return
	}

	// Writes (method=POST form submit) additionally require the host CSRF
	// double-submit. For a browser session this is already enforced upstream by
	// the Auth middleware on every unsafe-method cookie request; we re-assert it
	// here so a misrouted/ticket-less POST cannot mutate without the token.
	if ds.Method == "POST" && ticket == "" && !middleware.ValidateCSRF(r) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "CSRF validation failed")
		return
	}

	// Step 6 — build the upstream request server-side only. Placeholders are
	// filled from validated context/pathParams; query is narrowed to declared
	// keys. No client field redirects the upstream.
	filledPath, perr := buildUpstreamPath(ds.Path, clusterID, projectID, namespace, req.PathParams)
	if perr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFormat, perr.Error())
		return
	}
	query := mergeDeclaredQuery(ds.Query, req.Query)

	// Step 7 — dispatch in-process (the second RBAC gate). Project + truncate.
	if h.upstream == nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, "Extension upstream is not configured")
		return
	}
	payload, uerr := h.upstream(r.Context(), ExtensionUpstreamRequest{
		Proxy: ds.Proxy, Method: ds.Method, Path: filledPath, Query: query, Body: req.Body,
		UserID: userID, ClusterID: clusterID, ProjectID: projectID, Namespace: namespace,
	})
	if uerr != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, "Extension upstream request failed")
		return
	}
	projected, rows, truncated := projectPayload(payload, ds.Shape, ds.Fields, ds.MaxRows)

	// Step 8 — audit the allowed call.
	recordAuditAs(r, h.auditor, currentUserUUID(r), "extension.data.proxied", "ui_extension", row.ID.String(), name, map[string]any{
		"dataSourceId": dataSourceID, "resource": ds.RBAC.Resource, "verb": ds.RBAC.Verb,
		"clusterId": clusterID.String(), "allowed": true, "rows": rows,
	})

	RespondJSON(w, http.StatusOK, extProxyResponse{
		Data:  projected,
		Shape: ds.Shape,
		Meta: extProxyMeta{
			DataSourceID: dataSourceID, Rows: rows, RBACScope: ds.RBAC.Scope,
			Cached: false, TTLSeconds: ds.CacheTTLSeconds, Truncated: truncated,
		},
	})
}

// resolveCaller authenticates the request to a concrete (userID, clusterID).
// A browser session uses GetAuthenticatedUser; an X-Extension-Ticket (Tier 2)
// is validated single-use against the ticket store and is scoped to this
// extension+dataSource+cluster. On failure it writes the response and returns
// ok=false.
func (h *ExtensionHandler) resolveCaller(r *http.Request, req extProxyRequest, ds DataSourceRef, name, dataSourceID, ticket string, isBundle bool, w http.ResponseWriter) (uuid.UUID, uuid.UUID, bool) {
	clusterID := parseUUIDOrNil(req.Context.ClusterID)
	if v, ok := req.PathParams["clusterId"]; ok {
		clusterID = parseUUIDOrNil(v)
	}
	if ticket != "" {
		if h.tickets == nil {
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Extension tickets are not configured")
			return uuid.Nil, uuid.Nil, false
		}
		uid, err := h.tickets.Validate(ticket, name, dataSourceID, clusterID)
		if err != nil || uid == uuid.Nil {
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid extension ticket")
			return uuid.Nil, uuid.Nil, false
		}
		return uid, clusterID, true
	}
	// Browser session (Tier 1). A ticket-only (bundle) source cannot be reached
	// without a ticket, and findDataSource already restricts the search side,
	// but guard here too: never let a session caller hit a bundle source.
	if isBundle {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Unknown data source")
		return uuid.Nil, uuid.Nil, false
	}
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return uuid.Nil, uuid.Nil, false
	}
	uid, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid user identity")
		return uuid.Nil, uuid.Nil, false
	}
	return uid, clusterID, true
}

// findDataSource resolves a dataSource id in the stored manifest. When the call
// is ticket-authed (wantBundle), only Tier-2 BundleDescriptor.DataSources are
// searched; otherwise only Tier-1 point dataSources. This keeps the Tier-1 and
// Tier-2 allowlists separate — a session caller can never reach a bundle-only
// source, and vice versa.
func findDataSource(m ExtensionManifest, id string, wantBundle bool) (DataSourceRef, bool, bool) {
	match := func(sources []DataSourceRef) (DataSourceRef, bool) {
		for _, ds := range sources {
			if ds.ID == id {
				return ds, true
			}
		}
		return DataSourceRef{}, false
	}
	walk := func(render *ExtensionRender, tier1 []DataSourceRef) (DataSourceRef, bool, bool) {
		if wantBundle {
			if render != nil && render.Bundle != nil {
				if ds, ok := match(render.Bundle.DataSources); ok {
					return ds, true, true
				}
			}
			return DataSourceRef{}, false, false
		}
		if ds, ok := match(tier1); ok {
			return ds, false, true
		}
		return DataSourceRef{}, false, false
	}
	for _, p := range m.ExtensionPoints.Sidebar {
		if ds, b, ok := walk(p.Render, p.DataSources); ok {
			return ds, b, true
		}
	}
	for _, p := range m.ExtensionPoints.Widgets {
		if ds, b, ok := walk(p.Render, p.DataSources); ok {
			return ds, b, true
		}
	}
	for _, p := range m.ExtensionPoints.ClusterTabs {
		if ds, b, ok := walk(p.Render, p.DataSources); ok {
			return ds, b, true
		}
	}
	for _, p := range m.ExtensionPoints.Settings {
		if ds, b, ok := walk(p.Render, p.DataSources); ok {
			return ds, b, true
		}
	}
	return DataSourceRef{}, false, false
}

// scopeIDs returns the cluster/project ids that participate in the RBAC check
// for the declared scope. A global source binds neither; a cluster source binds
// only the cluster; a project source binds only the project. This prevents a
// caller from satisfying a cluster-scoped check by supplying an unrelated
// project they happen to own.
func scopeIDs(scope string, clusterID, projectID uuid.UUID) (uuid.UUID, uuid.UUID) {
	switch scope {
	case "cluster":
		return clusterID, uuid.Nil
	case "project":
		return uuid.Nil, projectID
	default: // global
		return uuid.Nil, uuid.Nil
	}
}

// buildUpstreamPath fills the {clusterId}/{projectId}/{namespace} placeholders
// from validated ids, rejecting any unfilled/unknown placeholder. pathParams
// override the context ids for the same keys (already parsed into clusterID/
// projectID by the caller); namespace is regex-checked. The path itself was
// traversal-checked at manifest validation, so no '..' can survive here.
func buildUpstreamPath(template string, clusterID, projectID uuid.UUID, namespace string, pathParams map[string]string) (string, error) {
	var rerr error
	out := pathPlaceholderRE.ReplaceAllStringFunc(template, func(tok string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(tok, "{"), "}")
		switch key {
		case "clusterId":
			if clusterID == uuid.Nil {
				rerr = errProxyMissingPlaceholder("clusterId")
				return tok
			}
			return clusterID.String()
		case "projectId":
			if projectID == uuid.Nil {
				rerr = errProxyMissingPlaceholder("projectId")
				return tok
			}
			return projectID.String()
		case "namespace":
			if !extNamespaceRE.MatchString(namespace) {
				rerr = errProxyMissingPlaceholder("namespace")
				return tok
			}
			return namespace
		default:
			rerr = errProxyMissingPlaceholder(key)
			return tok
		}
	})
	if rerr != nil {
		return "", rerr
	}
	if strings.Contains(out, "..") {
		return "", errProxyMissingPlaceholder("path")
	}
	return out, nil
}

type proxyPlaceholderError struct{ key string }

func (e proxyPlaceholderError) Error() string {
	return "missing or invalid path parameter: " + e.key
}

func errProxyMissingPlaceholder(key string) error { return proxyPlaceholderError{key: key} }

// mergeDeclaredQuery starts from the manifest's static query defaults and
// applies client overrides ONLY for keys the manifest declared. Unknown keys
// are dropped — a widget may override declared keys, never introduce new ones.
func mergeDeclaredQuery(declared, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(declared))
	for k, v := range declared {
		out[k] = v
	}
	for k, v := range overrides {
		if _, ok := declared[k]; ok {
			out[k] = v
		}
	}
	return out
}

// projectPayload applies the field projection + row truncation per the stored
// DataSourceRef. For a list shape it projects each row to fields[] (when set)
// and truncates to maxRows; object/series shapes pass through. Values are never
// interpreted — the proxy only narrows.
func projectPayload(payload any, shape string, fields []string, maxRows int) (any, int, bool) {
	switch shape {
	case "list":
		rows, _ := payload.([]any)
		truncated := false
		if maxRows > 0 && len(rows) > maxRows {
			rows = rows[:maxRows]
			truncated = true
		}
		if len(fields) > 0 {
			projected := make([]any, 0, len(rows))
			for _, row := range rows {
				projected = append(projected, projectRow(row, fields))
			}
			rows = projected
		}
		return map[string]any{"rows": rows}, len(rows), truncated
	default:
		return map[string]any{"value": payload}, 0, false
	}
}

// projectRow keeps only the allowlisted dot-path fields of one row. A row that
// is not an object passes through unchanged.
func projectRow(row any, fields []string) any {
	obj, ok := row.(map[string]any)
	if !ok {
		return row
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, found := lookupDotPath(obj, f); found {
			out[f] = v
		}
	}
	return out
}

func lookupDotPath(obj map[string]any, dotPath string) (any, bool) {
	parts := strings.Split(dotPath, ".")
	var cur any = obj
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func parseUUIDOrNil(s string) uuid.UUID {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil
	}
	return id
}
