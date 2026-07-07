// Custom Gatekeeper constraint authoring (P-04).
//
// Mount point: /api/v1/clusters/{id}/gatekeeper/constraints/*
//
//   - GET    …/constraints/            — list the embedded bundle constraints
//     plus operator-authored custom constraints (with best-effort live
//     violation counts).
//   - POST   …/constraints/validate/   — validate YAML only, no apply.
//   - POST   …/constraints/            — validate + server-side-apply through
//     the agent tunnel + persist the authored record. RBAC-gated + audited.
//   - DELETE …/constraints/{name}/     — remove the authored record and delete
//     the resource from the cluster. RBAC-gated + audited.
//
// Reuses internal/gatekeeperpolicy bundle apply mechanics (ParseManifest,
// Manifest.APIPath, kubeutil server-side-apply) and the same K8sRequester the
// alerting handler uses to reach the cluster through the tunnel.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/gatekeeperpolicy"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const gatekeeperAuthoredFieldManager = "astronomer-authored-constraint"

// GatekeeperConstraintQuerier is the narrow DB surface the handler needs.
type GatekeeperConstraintQuerier interface {
	ListAuthoredConstraintsForCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.AuthoredConstraint, error)
	GetAuthoredConstraintByName(ctx context.Context, arg sqlc.GetAuthoredConstraintByNameParams) (sqlc.AuthoredConstraint, error)
	UpsertAuthoredConstraint(ctx context.Context, arg sqlc.UpsertAuthoredConstraintParams) (sqlc.AuthoredConstraint, error)
	DeleteAuthoredConstraint(ctx context.Context, arg sqlc.DeleteAuthoredConstraintParams) error
}

// GatekeeperConstraintsHandler owns the custom-constraint authoring routes.
type GatekeeperConstraintsHandler struct {
	queries   GatekeeperConstraintQuerier
	requester K8sRequester
	authz     authorizationSupport
	audit     any
}

// NewGatekeeperConstraintsHandler constructs the handler. Both deps are
// nil-safe: list/validate degrade to 503 when the requester or queries are
// missing rather than panicking.
func NewGatekeeperConstraintsHandler(queries GatekeeperConstraintQuerier, requester K8sRequester) *GatekeeperConstraintsHandler {
	return &GatekeeperConstraintsHandler{queries: queries, requester: requester}
}

// SetAuthorization wires the RBAC engine + binding querier used to fail-closed
// gate create/delete at the handler layer (in addition to route middleware).
func (h *GatekeeperConstraintsHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

// SetAuditWriter wires the audit-log writer used to record create/delete.
func (h *GatekeeperConstraintsHandler) SetAuditWriter(audit any) {
	h.audit = audit
}

// ConstraintValidationResponse is the create/validate response shape.
type ConstraintValidationResponse struct {
	Valid   bool     `json:"valid"`
	Errors  []string `json:"errors"`
	Applied bool     `json:"applied"`
	Name    string   `json:"name,omitempty"`
	Kind    string   `json:"kind,omitempty"`
}

// ConstraintYAMLRequest is the create/validate request body.
type ConstraintYAMLRequest struct {
	YAML string `json:"yaml" validate:"required"`
}

// ListConstraints handles GET /api/v1/clusters/{id}/gatekeeper/constraints/.
func (h *GatekeeperConstraintsHandler) ListConstraints(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.clusterID(w, r)
	if !ok {
		return
	}
	// Fail-closed read authorization at the handler layer, matching the
	// create/delete gate — the route middleware fail-opens when the RBAC engine
	// is unconfigured, so anchor the read here too.
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbRead) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Gatekeeper constraint store is not available")
		return
	}

	// Embedded starter bundle (source of truth for the built-in policy set).
	bundleItems := make([]map[string]any, 0)
	if manifests, err := gatekeeperpolicy.Manifests(); err == nil {
		for _, m := range manifests {
			bundleItems = append(bundleItems, map[string]any{
				"name":        m.Name,
				"kind":        m.Kind,
				"api_version": m.Group + "/" + m.Version,
			})
		}
	}

	authored, err := h.queries.ListAuthoredConstraintsForCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list authored constraints")
		return
	}
	customItems := make([]map[string]any, 0, len(authored))
	for _, c := range authored {
		item := map[string]any{
			"name":        c.Name,
			"kind":        c.Kind,
			"api_version": c.ApiVersion,
			"created_by":  nullableUUID(c.CreatedBy),
			"created_at":  c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		// Best-effort live violation count: only for Constraint instances (a
		// ConstraintTemplate has no status.totalViolations), and only when the
		// tunnel requester is wired and the cluster answers. A failure just
		// omits the count rather than failing the whole list.
		if count, ok := h.violationCount(r, clusterID, c); ok {
			item["violation_count"] = count
		}
		customItems = append(customItems, item)
	}

	RespondJSON(w, http.StatusOK, map[string]any{
		"bundle": bundleItems,
		"custom": customItems,
	})
}

// ValidateConstraint handles POST …/constraints/validate/ — no apply.
func (h *GatekeeperConstraintsHandler) ValidateConstraint(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.clusterID(w, r)
	if !ok {
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbRead) {
		return
	}
	var req ConstraintYAMLRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	manifest, errs := validateConstraintYAML(req.YAML)
	resp := ConstraintValidationResponse{Valid: len(errs) == 0, Errors: errs, Applied: false}
	if len(errs) == 0 {
		resp.Name = manifest.Name
		resp.Kind = manifest.Kind
	}
	RespondJSON(w, http.StatusOK, resp)
}

// CreateConstraint handles POST …/constraints/ — validate + apply + persist.
func (h *GatekeeperConstraintsHandler) CreateConstraint(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.clusterID(w, r)
	if !ok {
		return
	}
	// SECURITY: fail-closed RBAC gate at the handler layer (clusters:update),
	// in addition to the route middleware. Never apply on an unauthorized or
	// unprovable identity.
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbUpdate) {
		return
	}
	if h.queries == nil || h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnwired, "Gatekeeper apply path is not available")
		return
	}
	var req ConstraintYAMLRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	manifest, errs := validateConstraintYAML(req.YAML)
	if len(errs) > 0 {
		// SECURITY: never apply unvalidated YAML.
		RespondJSON(w, http.StatusBadRequest, ConstraintValidationResponse{Valid: false, Errors: errs, Applied: false})
		return
	}

	// Server-side apply through the agent tunnel (same mechanics as the bundle
	// reconciler).
	path := kubeutil.ServerSideApplyPath(manifest.APIPath(), kubeutil.ApplyOptions{
		FieldManager: gatekeeperAuthoredFieldManager,
		Force:        true,
	})
	resp, err := h.requester.Do(r.Context(), clusterID.String(), http.MethodPatch, path, manifest.JSON, kubeutil.ApplyPatchHeaders())
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ApplyError, "Failed to apply constraint to cluster: "+err.Error())
		return
	}
	if resp == nil || resp.StatusCode >= http.StatusBadRequest {
		msg := "cluster rejected the constraint"
		if body, derr := decodeResponseBody(resp); derr == nil && len(body) > 0 {
			msg = string(body)
		}
		RespondJSON(w, http.StatusBadRequest, ConstraintValidationResponse{
			Valid:   true,
			Errors:  []string{msg},
			Applied: false,
			Name:    manifest.Name,
			Kind:    manifest.Kind,
		})
		return
	}

	if _, err := h.queries.UpsertAuthoredConstraint(r.Context(), sqlc.UpsertAuthoredConstraintParams{
		ClusterID:  clusterID,
		Name:       manifest.Name,
		Kind:       manifest.Kind,
		ApiVersion: manifest.Group + "/" + manifest.Version,
		Yaml:       req.YAML,
		CreatedBy:  currentUserUUID(r),
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Constraint applied but failed to persist record")
		return
	}

	recordAuditWithWriter(r, h.audit, "gatekeeper.constraint.create", "gatekeeper_constraint", clusterID.String()+"/"+manifest.Name, manifest.Name, map[string]any{
		"cluster_id": clusterID.String(),
		"kind":       manifest.Kind,
	})
	RespondJSON(w, http.StatusCreated, ConstraintValidationResponse{
		Valid:   true,
		Errors:  []string{},
		Applied: true,
		Name:    manifest.Name,
		Kind:    manifest.Kind,
	})
}

// DeleteConstraint handles DELETE …/constraints/{name}/.
func (h *GatekeeperConstraintsHandler) DeleteConstraint(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.clusterID(w, r)
	if !ok {
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbUpdate) {
		return
	}
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Gatekeeper constraint store is not available")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Constraint name is required")
		return
	}
	authored, err := h.queries.GetAuthoredConstraintByName(r.Context(), sqlc.GetAuthoredConstraintByNameParams{
		ClusterID: clusterID,
		Name:      name,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Authored constraint not found")
		return
	}

	// Best-effort delete from the cluster before dropping the record. A tunnel
	// error surfaces to the caller so the record is NOT removed while the
	// in-cluster resource lingers.
	if h.requester != nil {
		if manifest, perr := gatekeeperpolicy.ParseManifest([]byte(authored.Yaml)); perr == nil {
			resp, derr := h.requester.Do(r.Context(), clusterID.String(), http.MethodDelete, manifest.APIPath(), nil, requestHeaders(""))
			if derr != nil {
				RespondRequestError(w, r, http.StatusBadGateway, apierror.K8sError, "Failed to delete constraint from cluster: "+derr.Error())
				return
			}
			// A 404 means the resource is already gone — proceed to drop the record.
			if resp != nil && resp.StatusCode >= http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
				RespondRequestError(w, r, http.StatusBadGateway, apierror.K8sError, "Cluster rejected the constraint deletion")
				return
			}
		}
	}

	if err := h.queries.DeleteAuthoredConstraint(r.Context(), sqlc.DeleteAuthoredConstraintParams{
		ClusterID: clusterID,
		Name:      name,
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete authored constraint")
		return
	}

	recordAuditWithWriter(r, h.audit, "gatekeeper.constraint.delete", "gatekeeper_constraint", clusterID.String()+"/"+name, name, map[string]any{
		"cluster_id": clusterID.String(),
		"kind":       authored.Kind,
	})
	w.WriteHeader(http.StatusNoContent)
}

// clusterID parses and validates the {id} path param.
func (h *GatekeeperConstraintsHandler) clusterID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return uuid.UUID{}, false
	}
	return id, true
}

// violationCount fetches a Constraint instance's status.totalViolations from
// the cluster. Returns ok=false (and omits the count) for ConstraintTemplates,
// when the requester is unwired, or on any tunnel/parse error.
func (h *GatekeeperConstraintsHandler) violationCount(r *http.Request, clusterID uuid.UUID, c sqlc.AuthoredConstraint) (int, bool) {
	if h.requester == nil {
		return 0, false
	}
	manifest, err := gatekeeperpolicy.ParseManifest([]byte(c.Yaml))
	if err != nil || manifest.IsConstraintTemplate() {
		return 0, false
	}
	resp, err := h.requester.Do(r.Context(), clusterID.String(), http.MethodGet, manifest.APIPath(), nil, requestHeaders(""))
	if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
		return 0, false
	}
	var obj struct {
		Status struct {
			TotalViolations *int `json:"totalViolations"`
		} `json:"status"`
	}
	if err := parseJSONResponse(resp, &obj); err != nil || obj.Status.TotalViolations == nil {
		return 0, false
	}
	return *obj.Status.TotalViolations, true
}

// validateConstraintYAML parses the YAML into a Gatekeeper manifest and, for a
// ConstraintTemplate, structurally validates the embedded Rego. It returns the
// parsed manifest and a (possibly empty) list of human-readable errors.
func validateConstraintYAML(yamlDoc string) (gatekeeperpolicy.Manifest, []string) {
	if strings.TrimSpace(yamlDoc) == "" {
		return gatekeeperpolicy.Manifest{}, []string{"yaml is empty"}
	}
	manifest, err := gatekeeperpolicy.ParseManifest([]byte(yamlDoc))
	if err != nil {
		return gatekeeperpolicy.Manifest{}, []string{err.Error()}
	}
	if manifest.IsConstraintTemplate() {
		if errs := constraintTemplateRegoErrors(manifest.JSON); len(errs) > 0 {
			return manifest, errs
		}
	}
	return manifest, nil
}

// constraintTemplateRegoErrors extracts spec.targets[].rego from a
// ConstraintTemplate and structurally validates each Rego source. A full OPA
// compile is out of scope (no OPA dependency vendored), so this is a
// lightweight sanity check: a package declaration must be present and the
// delimiters must balance — enough to reject empty / obviously malformed Rego
// before it is applied to a cluster.
func constraintTemplateRegoErrors(manifestJSON []byte) []string {
	var ct struct {
		Spec struct {
			Targets []struct {
				Rego string `json:"rego"`
			} `json:"targets"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(manifestJSON, &ct); err != nil {
		return []string{"invalid ConstraintTemplate spec: " + err.Error()}
	}
	if len(ct.Spec.Targets) == 0 {
		return []string{"ConstraintTemplate has no spec.targets"}
	}
	var errs []string
	sawRego := false
	for i, target := range ct.Spec.Targets {
		if strings.TrimSpace(target.Rego) == "" {
			continue
		}
		sawRego = true
		if regoErr := regoSourceError(target.Rego); regoErr != "" {
			errs = append(errs, "spec.targets["+strconv.Itoa(i)+"].rego: "+regoErr)
		}
	}
	if !sawRego {
		errs = append(errs, "ConstraintTemplate has no rego source")
	}
	return errs
}

var regoPackageRE = regexp.MustCompile(`(?m)^\s*package\s+\S+`)

// regoSourceError returns a non-empty message when the Rego source is
// structurally invalid, or "" when it passes the lightweight checks.
func regoSourceError(rego string) string {
	trimmed := strings.TrimSpace(rego)
	if trimmed == "" {
		return "rego source is empty"
	}
	if !regoPackageRE.MatchString(trimmed) {
		return "missing package declaration"
	}
	if !delimitersBalanced(trimmed) {
		return "unbalanced braces, brackets, or parentheses"
	}
	return ""
}

// delimitersBalanced reports whether (), [] and {} are balanced, ignoring
// characters inside double-quoted strings and line comments.
func delimitersBalanced(s string) bool {
	var stack []rune
	inString := false
	inComment := false
	var prev rune
	pairs := map[rune]rune{')': '(', ']': '[', '}': '{'}
	for _, ch := range s {
		if inComment {
			if ch == '\n' {
				inComment = false
			}
			prev = ch
			continue
		}
		if inString {
			if ch == '"' && prev != '\\' {
				inString = false
			}
			prev = ch
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '#':
			inComment = true
		case '(', '[', '{':
			stack = append(stack, ch)
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != pairs[ch] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
		prev = ch
	}
	return len(stack) == 0 && !inString
}

// recordAuditWithWriter writes an audit row through an explicit writer (the
// handler's injected audit dep) rather than a querier field, mirroring
// recordAudit's best-effort, never-fail-the-request contract.
func recordAuditWithWriter(r *http.Request, writer any, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	if writer == nil {
		return
	}
	recordAudit(r, writer, action, resourceType, resourceID, resourceName, detail)
}
