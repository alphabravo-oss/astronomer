package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type argoCDClusterOwnershipResponse struct {
	ClusterID       string                             `json:"cluster_id"`
	ClusterName     string                             `json:"cluster_name"`
	Registered      bool                               `json:"registered"`
	ManagedClusters []argoCDManagedClusterSummary      `json:"managed_clusters"`
	Components      []argoCDBaselineComponentOwnership `json:"components"`
	GeneratedAt     string                             `json:"generated_at"`
}

type argoCDManagedClusterSummary struct {
	ArgocdInstanceID  string            `json:"argocd_instance_id"`
	ClusterSecretName string            `json:"cluster_secret_name"`
	ServerURL         string            `json:"server_url"`
	Labels            map[string]string `json:"labels"`
	UpdatedAt         string            `json:"updated_at"`
}

type argoCDBaselineComponentOwnership struct {
	Slug               string                                  `json:"slug"`
	Name               string                                  `json:"name"`
	Namespace          string                                  `json:"namespace"`
	ApplicationSetName string                                  `json:"application_set_name"`
	DesiredOwner       string                                  `json:"desired_owner"`
	ObservedOwner      string                                  `json:"observed_owner"`
	State              string                                  `json:"state"`
	Options            []string                                `json:"options"`
	Decision           *argoCDBaselineOwnershipDecisionSummary `json:"decision,omitempty"`
}

type argoCDBaselineOwnershipDecisionSummary struct {
	ID          string  `json:"id"`
	Decision    string  `json:"decision"`
	Reason      string  `json:"reason"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
	DecidedByID *string `json:"decided_by_id,omitempty"`
	UpdatedAt   string  `json:"updated_at"`
}

type argoCDBaselineOwnershipDecisionRequest struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expires_at"`
}

func (h *ArgoCDHandler) ClusterOwnership(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireArgoCluster(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	resp, err := h.clusterOwnershipResponse(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.OwnershipError, "Failed to load ArgoCD ownership state")
		return
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *ArgoCDHandler) SetClusterOwnershipDecision(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := h.requireArgoCluster(w, r, rbac.VerbUpdate)
	if !ok {
		return
	}
	componentSlug := strings.TrimSpace(chi.URLParam(r, "component_slug"))
	if !isKnownBaselineComponent(componentSlug) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ComponentInvalid, "Unknown baseline component")
		return
	}
	var req argoCDBaselineOwnershipDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if !validArgoOwnershipDecision(decision) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidDecision, "decision must be adopt, leave_local, or replace")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	expiresAt := pgtype.Timestamptz{}
	if strings.TrimSpace(req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidExpiresAt, "expires_at must be RFC3339")
			return
		}
		if !t.After(time.Now()) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidExpiresAt, "expires_at must be in the future")
			return
		}
		expiresAt = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if ok := h.validateArgoOwnershipDecision(w, r, clusterID, componentSlug, decision, reason); !ok {
		return
	}
	row, err := h.queries.UpsertArgoCDBaselineOwnershipDecision(r.Context(), sqlc.UpsertArgoCDBaselineOwnershipDecisionParams{
		ClusterID:     clusterID,
		ComponentSlug: componentSlug,
		Decision:      decision,
		Reason:        reason,
		ExpiresAt:     expiresAt,
		DecidedByID:   currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DecisionError, "Failed to record ownership decision")
		return
	}
	h.publishArgoCDChanged(clusterID, row.ID.String(), "ownership")
	recordAudit(r, h.queries, "argocd.baseline_ownership.decision", "cluster", clusterID.String(), componentSlug, map[string]any{
		"component":  componentSlug,
		"decision":   decision,
		"reason":     reason,
		"expires_at": req.ExpiresAt,
	})
	RespondJSON(w, http.StatusOK, decisionSummary(row))
}

// argoCDOwnershipAppQuerier is the optional batch-query seam used to tell
// "component was never installed here" from "component is installed and running
// under ArgoCD". Optional so the many ArgoCDQuerier test stubs don't all have to
// grow the method; the real *sqlc.Queries implements it, so production gets the
// check. Same idiom as loadArgoCDPageData's argoCDBatchQuerier.
type argoCDOwnershipAppQuerier interface {
	ListArgoCDApplicationsByManagedClusterTargets(ctx context.Context, arg sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams) ([]sqlc.ArgocdApplication, error)
}

// componentIsArgoManaged reports whether an ArgoCD Application for this baseline
// component currently targets this cluster — i.e. whether the component is
// really out there running under ArgoCD's control.
func (h *ArgoCDHandler) componentIsArgoManaged(ctx context.Context, cluster sqlc.Cluster, managedRows []sqlc.ArgocdManagedCluster, componentSlug string) bool {
	q, ok := h.queries.(argoCDOwnershipAppQuerier)
	if !ok {
		return false
	}
	instanceIDs, targets := argoCDManagedClusterApplicationTargets(cluster, managedRows)
	if len(instanceIDs) == 0 || len(targets) == 0 {
		return false
	}
	apps, err := q.ListArgoCDApplicationsByManagedClusterTargets(ctx, sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams{
		ArgocdInstanceIds:   instanceIDs,
		DestinationClusters: targets,
	})
	if err != nil {
		// Best-effort, matching the rest of the ArgoCD read paths: if we cannot
		// prove the component is running we do not block the operator.
		return false
	}
	for _, app := range apps {
		if item, ok := baselineComponentForApplicationName(app.Name); ok && item.Slug == componentSlug {
			return true
		}
	}
	return false
}

func (h *ArgoCDHandler) validateArgoOwnershipDecision(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, componentSlug, decision, reason string) bool {
	if decision == "leave_local" {
		return h.validateArgoLeaveLocalDecision(w, r, clusterID, componentSlug, reason)
	}
	if decision != "replace" {
		return true
	}
	if reason == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ReasonRequired, "replace decisions require a reason")
		return false
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return false
	}
	if cluster.IsLocal {
		RespondRequestError(w, r, http.StatusConflict, apierror.UnsafeReplacementBlocked, "local management cluster baseline components cannot be replaced by ArgoCD")
		return false
	}
	managedRows, err := h.queries.ListArgoCDManagedClustersByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.OwnershipError, "Failed to load ArgoCD registration state")
		return false
	}
	if len(managedRows) == 0 {
		RespondRequestError(w, r, http.StatusConflict, apierror.UnsafeReplacementBlocked, "cluster must be registered with ArgoCD before replace can be recorded")
		return false
	}
	return true
}

// validateArgoLeaveLocalDecision gates the per-cluster opt-out.
//
// "Leave local" does not uninstall anything: it drops the component out of the
// cluster's desired state and out of the baseline ApplicationSet's generator. On
// a component that was never installed here that is exactly right — it is how an
// operator says "I run my own kube-state-metrics on this cluster, keep yours
// off". On a component ArgoCD is *currently running*, the workload stays up with
// nothing left to reconcile, upgrade or repair it: an orphan that looks healthy
// until it silently rots. So require a reason (as `replace` does — this is just
// as consequential) and refuse outright once the component is really running.
// The way to remove a running component is to uninstall it or disable the
// baseline component, both of which take the workload with them.
func (h *ArgoCDHandler) validateArgoLeaveLocalDecision(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, componentSlug, reason string) bool {
	if reason == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ReasonRequired, "leave_local decisions require a reason")
		return false
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return false
	}
	managedRows, err := h.queries.ListArgoCDManagedClustersByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.OwnershipError, "Failed to load ArgoCD registration state")
		return false
	}
	if h.componentIsArgoManaged(r.Context(), cluster, managedRows, componentSlug) {
		RespondRequestError(w, r, http.StatusConflict, apierror.UnsafeLeaveLocalBlocked,
			"component is running under ArgoCD on this cluster; uninstall it or disable the baseline component instead of leaving the workload unmanaged")
		return false
	}
	return true
}

func (h *ArgoCDHandler) requireArgoCluster(w http.ResponseWriter, r *http.Request, verb rbac.Verb) (uuid.UUID, bool) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return uuid.Nil, false
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return uuid.Nil, false
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceArgoCD, verb) {
		return uuid.Nil, false
	}
	return clusterID, true
}

func (h *ArgoCDHandler) clusterOwnershipResponse(ctx context.Context, clusterID uuid.UUID) (argoCDClusterOwnershipResponse, error) {
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return argoCDClusterOwnershipResponse{}, err
	}
	managedRows, err := h.queries.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	if err != nil {
		return argoCDClusterOwnershipResponse{}, err
	}
	decisions, err := h.queries.ListArgoCDBaselineOwnershipDecisions(ctx, clusterID)
	if err != nil {
		return argoCDClusterOwnershipResponse{}, err
	}
	decisionBySlug := make(map[string]sqlc.ArgocdBaselineOwnershipDecision, len(decisions))
	for _, decision := range decisions {
		decisionBySlug[decision.ComponentSlug] = decision
	}
	managed := make([]argoCDManagedClusterSummary, 0, len(managedRows))
	for _, row := range managedRows {
		managed = append(managed, managedClusterSummary(row))
	}
	registered := len(managedRows) > 0
	components := make([]argoCDBaselineComponentOwnership, 0, len(platformBaselineComponentCatalog))
	for _, item := range platformBaselineComponentCatalog {
		var decision *argoCDBaselineOwnershipDecisionSummary
		rawDecision, hasDecision := decisionBySlug[item.Slug]
		// This panel reports what Astronomer auto-provisions on the cluster, so
		// it only lists the DefaultEnabled set (the two metrics exporters).
		// The opt-in components (trivy, fluent-bit, ingress-nginx, cert-manager,
		// gatekeeper) are add-ons installed from the Tools view; they surface
		// here only once an operator has actually made an ownership decision for
		// one. Without this gate the panel claimed ArgoCD owned five components
		// that were never installed, naming ApplicationSets that do not exist.
		// baselineComponentOwnership applies the same policy to the cluster
		// response; orphan detection still sweeps the full catalog.
		if !item.DefaultEnabled && !hasDecision {
			continue
		}
		if hasDecision {
			summary := decisionSummary(rawDecision)
			decision = &summary
		}
		observedOwner, state := argoOwnershipState(cluster, registered, rawDecision, hasDecision)
		components = append(components, argoCDBaselineComponentOwnership{
			Slug:               item.Slug,
			Name:               item.Name,
			Namespace:          item.Namespace,
			ApplicationSetName: item.ApplicationSetName,
			DesiredOwner:       "argocd",
			ObservedOwner:      observedOwner,
			State:              state,
			Options:            argoOwnershipOptions(cluster, state),
			Decision:           decision,
		})
	}
	return argoCDClusterOwnershipResponse{
		ClusterID:       cluster.ID.String(),
		ClusterName:     firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		Registered:      registered,
		ManagedClusters: managed,
		Components:      components,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func managedClusterSummary(row sqlc.ArgocdManagedCluster) argoCDManagedClusterSummary {
	labels := map[string]string{}
	_ = json.Unmarshal(row.Labels, &labels)
	return argoCDManagedClusterSummary{
		ArgocdInstanceID:  row.ArgocdInstanceID.String(),
		ClusterSecretName: row.ClusterSecretName,
		ServerURL:         row.ServerUrl,
		Labels:            labels,
		UpdatedAt:         row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func argoOwnershipState(cluster sqlc.Cluster, registered bool, decision sqlc.ArgocdBaselineOwnershipDecision, hasDecision bool) (string, string) {
	if cluster.IsLocal {
		return "local", "local_manual"
	}
	if hasDecision {
		switch decision.Decision {
		case "leave_local":
			return "legacy_helm", "local_manual"
		case "replace":
			if registered {
				return "legacy_helm", "migration_required"
			}
			return "unmanaged", "migration_required"
		case "adopt":
			if registered {
				return "argocd", "argocd_owned"
			}
			return "legacy_helm", "migration_required"
		}
	}
	if registered {
		return "argocd", "argocd_owned"
	}
	return "legacy_helm", "migration_required"
}

func argoOwnershipOptions(cluster sqlc.Cluster, state string) []string {
	if cluster.IsLocal {
		return []string{"leave_local"}
	}
	switch state {
	case "argocd_owned":
		return []string{"leave_local"}
	case "local_manual":
		return []string{"adopt", "replace"}
	default:
		return []string{"adopt", "leave_local", "replace"}
	}
}

func isKnownBaselineComponent(slug string) bool {
	for _, item := range platformBaselineComponentCatalog {
		if item.Slug == slug {
			return true
		}
	}
	return false
}

func validArgoOwnershipDecision(decision string) bool {
	return decision == "adopt" || decision == "leave_local" || decision == "replace"
}

func decisionSummary(row sqlc.ArgocdBaselineOwnershipDecision) argoCDBaselineOwnershipDecisionSummary {
	out := argoCDBaselineOwnershipDecisionSummary{
		ID:        row.ID.String(),
		Decision:  row.Decision,
		Reason:    row.Reason,
		UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.ExpiresAt.Valid {
		s := row.ExpiresAt.Time.UTC().Format(time.RFC3339)
		out.ExpiresAt = &s
	}
	if row.DecidedByID.Valid {
		s := uuid.UUID(row.DecidedByID.Bytes).String()
		out.DecidedByID = &s
	}
	return out
}
