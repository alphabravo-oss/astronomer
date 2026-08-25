// Custom Gatekeeper constraint authoring (P-04).
//
// Mount point: /api/v1/clusters/{id}/gatekeeper/constraints/*
//
//   - GET    …/constraints/            — list the embedded bundle constraints
//     plus operator-authored custom constraints (with best-effort live
//     violation counts).
//   - POST   …/constraints/validate/   — validate YAML only, no apply.
//   - POST   …/constraints/            — validate and atomically queue desired
//     state, reconciliation, and audit records. RBAC-gated.
//   - DELETE …/constraints/{name}/     — atomically queue absent desired state,
//     reconciliation, and audit records. RBAC-gated.
//
// Reuses internal/gatekeeperpolicy bundle apply mechanics (ParseManifest,
// Manifest.APIPath, kubeutil server-side-apply) and the same K8sRequester the
// alerting handler uses to reach the cluster through the tunnel.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/gatekeeperpolicy"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// GatekeeperConstraintQuerier is the narrow DB surface the handler needs.
type GatekeeperConstraintQuerier interface {
	ListAuthoredConstraintsForCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.AuthoredConstraint, error)
	GetAuthoredConstraintByName(ctx context.Context, arg sqlc.GetAuthoredConstraintByNameParams) (sqlc.AuthoredConstraint, error)
	UpsertAuthoredConstraint(ctx context.Context, arg sqlc.UpsertAuthoredConstraintParams) (sqlc.AuthoredConstraint, error)
	DeleteAuthoredConstraint(ctx context.Context, arg sqlc.DeleteAuthoredConstraintParams) error
}

type GatekeeperConstraintMutationTx interface {
	GatekeeperConstraintQuerier
	GetAuthoredConstraintByNameForUpdate(context.Context, sqlc.GetAuthoredConstraintByNameForUpdateParams) (sqlc.AuthoredConstraint, error)
	MarkAuthoredConstraintDeleted(context.Context, sqlc.MarkAuthoredConstraintDeletedParams) (sqlc.AuthoredConstraint, error)
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type gatekeeperConstraintRunTxFunc func(context.Context, func(GatekeeperConstraintMutationTx) error) error

// GatekeeperConstraintsHandler owns the custom-constraint authoring routes.
type GatekeeperConstraintsHandler struct {
	queries   GatekeeperConstraintQuerier
	requester K8sRequester
	authz     authorizationSupport
	audit     any
	runTx     gatekeeperConstraintRunTxFunc
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

func (h *GatekeeperConstraintsHandler) SetRunTx(runTx gatekeeperConstraintRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *GatekeeperConstraintsHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

// ConstraintValidationResponse is the create/validate response shape.
type ConstraintValidationResponse struct {
	Valid   bool     `json:"valid"`
	Errors  []string `json:"errors"`
	Applied bool     `json:"applied"`
	Name    string   `json:"name,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Status  string   `json:"status,omitempty"`
	TaskID  string   `json:"task_id,omitempty"`
}

type GatekeeperConstraintMutationResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	TaskID string `json:"task_id,omitempty"`
}

// ConstraintYAMLRequest is the create/validate request body.
// openapi:request ConstraintYAMLRequest
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
				"name":               m.Name,
				"kind":               m.Kind,
				"api_version":        m.Group + "/" + m.Version,
				"enforcement_action": constraintEnforcementAction(m.JSON),
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
			"name":                c.Name,
			"kind":                c.Kind,
			"api_version":         c.ApiVersion,
			"yaml":                c.Yaml,
			"created_by":          nullableUUID(c.CreatedBy),
			"created_at":          c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"desired_state":       c.DesiredState,
			"sync_status":         c.SyncStatus,
			"generation":          c.Generation,
			"observed_generation": c.ObservedGeneration,
		}
		if c.LastError != "" {
			item["last_error"] = c.LastError
		}
		if c.LastReconciledAt.Valid {
			item["last_reconciled_at"] = c.LastReconciledAt.Time.UTC().Format(time.RFC3339)
		}
		if manifest, parseErr := gatekeeperpolicy.ParseManifest([]byte(c.Yaml)); parseErr == nil {
			item["enforcement_action"] = constraintEnforcementAction(manifest.JSON)
		}
		// Best-effort live violation count: only for Constraint instances (a
		// ConstraintTemplate has no status.totalViolations), and only when the
		// tunnel requester is wired and the cluster answers. A failure just
		// omits the count rather than failing the whole list.
		if c.DesiredState != "absent" {
			if count, ok := h.violationCount(r, clusterID, c); ok {
				item["violation_count"] = count
			}
		}
		customItems = append(customItems, item)
	}

	RespondJSON(w, http.StatusOK, map[string]any{
		"bundle": bundleItems,
		"custom": customItems,
	})
}

func constraintEnforcementAction(document []byte) string {
	var manifest struct {
		Spec struct {
			EnforcementAction string `json:"enforcementAction"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(document, &manifest); err != nil {
		return ""
	}
	return manifest.Spec.EnforcementAction
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

// CreateConstraint handles POST …/constraints/ — validate and queue desired state.
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
	if !RequireOperationIdempotencyKey(w, r) {
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
	if h.queries == nil || h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnwired, "Gatekeeper apply path is not available")
		return
	}

	params := sqlc.UpsertAuthoredConstraintParams{
		ClusterID: clusterID, Name: manifest.Name, Kind: manifest.Kind,
		ApiVersion: manifest.Group + "/" + manifest.Version, Yaml: req.YAML,
		CreatedBy: currentUserUUID(r),
	}
	r = r.WithContext(withOperationIdempotency(r, "gatekeeper_constraint_create"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID string `json:"cluster_id"`
		YAML      string `json:"yaml"`
	}{ClusterID: clusterID.String(), YAML: req.YAML})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode Gatekeeper constraint request")
		return
	}
	if h.runTx != nil {
		receipt := ConstraintValidationResponse{Valid: true, Errors: []string{}, Applied: false, Name: manifest.Name, Kind: manifest.Kind}
		err := h.runTx(r.Context(), func(q GatekeeperConstraintMutationTx) error {
			idemQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return errors.New("Gatekeeper idempotency store is not configured")
			}
			_, stored, replay, claimErr := claimOperationReceipt[ConstraintValidationResponse](r.Context(), idemQ, "gatekeeper_constraint_creates", digest)
			if claimErr != nil {
				return claimErr
			}
			if replay {
				receipt = stored
				return nil
			}
			var mutationErr error
			row, mutationErr := q.UpsertAuthoredConstraint(r.Context(), params)
			if mutationErr != nil {
				return mutationErr
			}
			taskRow, mutationErr := enqueueGatekeeperConstraintReconcile(r, q, row)
			if mutationErr != nil {
				return mutationErr
			}
			receipt.Status, receipt.TaskID = row.SyncStatus, taskRow.ID.String()
			if auditErr := recordAuditOutbox(r, q, "gatekeeper.constraint.create", "gatekeeper_constraint", clusterID.String()+"/"+manifest.Name, manifest.Name, http.StatusAccepted, map[string]any{
				"cluster_id": clusterID.String(), "kind": manifest.Kind,
				"generation": row.Generation, "task_id": taskRow.ID.String(),
			}); auditErr != nil {
				return auditErr
			}
			return attachOperationReceipt(r.Context(), idemQ, "gatekeeper_constraint_creates", row.ID, digest, receipt)
		})
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different Gatekeeper constraint create request")
			return
		}
		if err != nil {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to queue Gatekeeper constraint")
			return
		}
		RespondAcceptedOperation(w, "/api/v1/clusters/"+clusterID.String()+"/gatekeeper/constraints/", receipt)
		return
	}

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
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.queries == nil || h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Gatekeeper constraint store is not available")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Constraint name is required")
		return
	}
	getParams := sqlc.GetAuthoredConstraintByNameParams{
		ClusterID: clusterID,
		Name:      name,
	}
	r = r.WithContext(withOperationIdempotency(r, "gatekeeper_constraint_delete"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID string `json:"cluster_id"`
		Name      string `json:"name"`
	}{ClusterID: clusterID.String(), Name: name})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode Gatekeeper constraint delete request")
		return
	}
	if h.runTx != nil {
		receipt := GatekeeperConstraintMutationResponse{Name: name}
		err := h.runTx(r.Context(), func(q GatekeeperConstraintMutationTx) error {
			idemQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return errors.New("Gatekeeper idempotency store is not configured")
			}
			_, stored, replay, claimErr := claimOperationReceipt[GatekeeperConstraintMutationResponse](r.Context(), idemQ, "gatekeeper_constraint_deletes", digest)
			if claimErr != nil {
				return claimErr
			}
			if replay {
				receipt = stored
				return nil
			}
			locked, lockErr := q.GetAuthoredConstraintByNameForUpdate(r.Context(), sqlc.GetAuthoredConstraintByNameForUpdateParams(getParams))
			if lockErr != nil {
				return lockErr
			}
			authored, lockErr := q.MarkAuthoredConstraintDeleted(r.Context(), sqlc.MarkAuthoredConstraintDeletedParams(getParams))
			if lockErr != nil {
				return lockErr
			}
			taskRow, lockErr := enqueueGatekeeperConstraintReconcile(r, q, authored)
			if lockErr != nil {
				return lockErr
			}
			receipt.Status, receipt.TaskID = authored.SyncStatus, taskRow.ID.String()
			if auditErr := recordAuditOutbox(r, q, "gatekeeper.constraint.delete", "gatekeeper_constraint", clusterID.String()+"/"+name, name, http.StatusAccepted, map[string]any{
				"cluster_id": clusterID.String(), "kind": locked.Kind,
				"generation": authored.Generation, "task_id": taskRow.ID.String(),
			}); auditErr != nil {
				return auditErr
			}
			return attachOperationReceipt(r.Context(), idemQ, "gatekeeper_constraint_deletes", authored.ID, digest, receipt)
		})
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different Gatekeeper constraint delete request")
			return
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Authored constraint not found")
				return
			}
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to queue Gatekeeper constraint deletion")
			return
		}
		RespondAcceptedOperation(w, "/api/v1/clusters/"+clusterID.String()+"/gatekeeper/constraints/", receipt)
		return
	}
}

func enqueueGatekeeperConstraintReconcile(r *http.Request, q tasks.TaskOutboxWriter, row sqlc.AuthoredConstraint) (sqlc.TaskOutbox, error) {
	task, err := tasks.NewGatekeeperConstraintReconcileTask(row.ClusterID, row.Name, row.Generation)
	if err != nil {
		return sqlc.TaskOutbox{}, err
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(5))
	return tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
		DedupeKey: fmt.Sprintf("gatekeeper_constraint:%s:%s:%d", row.ClusterID, row.Name, row.Generation),
		QueueName: tasks.ClusterTemplateApplyQueueName, MaxRetry: 5,
		Timeout: gatekeeperConstraintReconcileTimeout, MaxDeliveryAttempts: 20,
	})
}

const gatekeeperConstraintReconcileTimeout = 2 * time.Minute

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
