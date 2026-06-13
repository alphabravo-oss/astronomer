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
		RespondRequestError(w, r, http.StatusInternalServerError, "ownership_error", "Failed to load ArgoCD ownership state")
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
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_component", "Unknown baseline component")
		return
	}
	var req argoCDBaselineOwnershipDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	decision := strings.TrimSpace(req.Decision)
	if !validArgoOwnershipDecision(decision) {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_decision", "decision must be adopt, leave_local, or replace")
		return
	}
	expiresAt := pgtype.Timestamptz{}
	if strings.TrimSpace(req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_expires_at", "expires_at must be RFC3339")
			return
		}
		expiresAt = pgtype.Timestamptz{Time: t, Valid: true}
	}
	row, err := h.queries.UpsertArgoCDBaselineOwnershipDecision(r.Context(), sqlc.UpsertArgoCDBaselineOwnershipDecisionParams{
		ClusterID:     clusterID,
		ComponentSlug: componentSlug,
		Decision:      decision,
		Reason:        strings.TrimSpace(req.Reason),
		ExpiresAt:     expiresAt,
		DecidedByID:   currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "decision_error", "Failed to record ownership decision")
		return
	}
	recordAudit(r, h.queries, "argocd.baseline_ownership.decision", "cluster", clusterID.String(), componentSlug, map[string]any{
		"component":  componentSlug,
		"decision":   decision,
		"reason":     strings.TrimSpace(req.Reason),
		"expires_at": req.ExpiresAt,
	})
	RespondJSON(w, http.StatusOK, decisionSummary(row))
}

func (h *ArgoCDHandler) requireArgoCluster(w http.ResponseWriter, r *http.Request, verb rbac.Verb) (uuid.UUID, bool) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
		return uuid.Nil, false
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
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
			Options:            argoOwnershipOptions(state),
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

func argoOwnershipOptions(state string) []string {
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
