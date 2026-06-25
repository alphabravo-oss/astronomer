package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// §BridgeProtocol — Tier-2 scoped-token issuance.
//
// POST /api/v1/extensions/{name}/token/   backs the iframe's ext/token.request.
//
// The sandboxed bundle NEVER receives the session JWT. Instead the host mints a
// short-lived, single-use, scope-checked ExtensionTicketStore ticket — bound to
// {userID, extension, dataSourceId, clusterID}, ≤60s TTL — and the iframe sends
// it as X-Extension-Ticket to §DataProxy. The ticket conveys identity for one
// dataSource briefly; it grants NO permission of its own (§DataProxy re-derives
// the user's RBAC on every call).
//
// Issuance is gated by the SAME two checks as the data path, so a ticket can
// never be minted for something the caller couldn't already reach:
//
//   - the dataSource must be a Tier-2 bundle source declared in the STORED,
//     validated manifest of the enabled+compatible extension (the handshake
//     allowlist, enforced server-side from the manifest — not client-supplied),
//     and
//   - engine.CheckPermission must pass for the requesting user's own bindings
//     (the same engine/bindings/call as §DataProxy step 4).

// extTokenRequest is the ext/token.request payload: the iframe names a
// dataSource id (also in the path) and the cluster context it is mounted in. No
// URL, verb, or scope is client-supplied — all are re-derived from the stored
// manifest.
type extTokenRequest struct {
	DataSource string `json:"dataSource"`
	Context    struct {
		ClusterID string `json:"clusterId"`
		ProjectID string `json:"projectId"`
		Namespace string `json:"namespace"`
	} `json:"context"`
}

// extTokenResponse is the host/token.grant payload. The opaque ticket is the
// only copy the iframe ever holds; scope mirrors §BridgeProtocol.
type extTokenResponse struct {
	Token      string `json:"token"`
	DataSource string `json:"dataSource"`
	ExpiresAt  string `json:"expiresAt"`
	Scope      string `json:"scope"`
}

// IssueTicket mints a Tier-2 bridge data ticket, failing closed at each gate.
func (h *ExtensionHandler) IssueTicket(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil || h.engine == nil || h.bindings == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension bridge is not configured")
		return
	}
	if h.issuer == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension tickets are not configured")
		return
	}

	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if !extensionNameRE.MatchString(name) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Invalid extension name")
		return
	}

	var req extTokenRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	dataSourceID := strings.TrimSpace(req.DataSource)
	if dataSourceID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "dataSource is required")
		return
	}

	// Authenticate the requesting user. Only a real browser session may ask for
	// a ticket — the iframe relays ext/token.request through the host shell,
	// which carries the session cookie. The ticket is then the ONLY credential
	// the iframe sees.
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid user identity")
		return
	}

	// Load + gate the extension from the STORED manifest (same as §DataProxy
	// step 1). A disabled/incompatible extension issues no tickets.
	row, err := h.findExtension(r.Context(), name)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Extension not found")
		return
	}
	if !row.Enabled || row.CompatibilityStatus != "compatible" {
		RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Extension is not enabled or compatible")
		return
	}
	// A Tier-2 ticket only makes sense for a verified bundle — the loader never
	// mounts an unverified bundle, so it can never legitimately ask for a ticket.
	if !row.BundleVerified {
		RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Extension bundle is not verified")
		return
	}
	var manifest ExtensionManifest
	if json.Unmarshal(row.Manifest, &manifest) != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Extension manifest is unreadable")
		return
	}

	// Resolve the dataSource in the Tier-2 bundle allowlist of the stored
	// manifest. wantBundle=true ⇒ only BundleDescriptor.DataSources are searched,
	// so a ticket can never be minted for a Tier-1 (browser-session) source.
	ds, _, found := findDataSource(manifest, dataSourceID, true)
	if !found {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Unknown data source")
		return
	}

	clusterID := parseUUIDOrNil(req.Context.ClusterID)
	projectID := parseUUIDOrNil(req.Context.ProjectID)
	namespace := strings.TrimSpace(req.Context.Namespace)
	scopeCluster, scopeProject := scopeIDs(ds.RBAC.Scope, clusterID, projectID)

	// RBAC under the USER's own bindings — the SAME gate as §DataProxy step 4.
	// The ticket is issued only when the user could already make this call.
	bindings, err := h.bindings.GetUserBindings(r.Context(), userID.String())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to read user permissions")
		return
	}
	allowed := h.engine.CheckPermission(bindings, rbac.Resource(ds.RBAC.Resource), rbac.Verb(ds.RBAC.Verb), scopeCluster, scopeProject, namespace)
	// Re-assert the install invariant ds.RBAC ∈ permissions[] at runtime.
	if allowed && !permissionSet(manifest.Permissions)[ds.RBAC.Resource+":"+ds.RBAC.Verb] {
		allowed = false
	}
	if !allowed {
		recordAuditAs(r, h.auditor, currentUserUUID(r), "extension.data.denied", "ui_extension", row.ID.String(), name, map[string]any{
			"dataSourceId": dataSourceID, "resource": ds.RBAC.Resource, "verb": ds.RBAC.Verb,
			"clusterId": clusterID.String(), "allowed": false, "phase": "token",
		})
		RespondRequestError(w, r, http.StatusForbidden, apierror.ExtensionRBACDenied, "Your permissions do not allow this extension data source")
		return
	}

	// Mint the scoped, single-use, ≤60s ticket bound to exactly this call.
	token, expiresAt, err := h.issuer.IssueToken(userID, name, dataSourceID, clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TicketError, "Failed to issue extension ticket")
		return
	}

	recordAuditAs(r, h.auditor, currentUserUUID(r), "extension.token.issued", "ui_extension", row.ID.String(), name, map[string]any{
		"dataSourceId": dataSourceID, "resource": ds.RBAC.Resource, "verb": ds.RBAC.Verb,
		"clusterId": clusterID.String(), "expiresAt": expiresAt.UTC().Format(time.RFC3339),
	})

	RespondJSON(w, http.StatusCreated, extTokenResponse{
		Token:      token,
		DataSource: dataSourceID,
		ExpiresAt:  expiresAt.UTC().Format(time.RFC3339),
		Scope:      "ext:" + name + ":data:" + dataSourceID,
	})
}
