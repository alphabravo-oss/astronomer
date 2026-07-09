package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

const (
	argoCDOrphanReasonMissingDestination       = "missing_destination"
	argoCDOrphanReasonStaleDestination         = "stale_destination_cluster"
	argoCDOrphanReasonLiveMissingDestination   = "live_missing_destination"
	argoCDOrphanReasonLiveStaleDestination     = "live_stale_destination_cluster"
	argoCDOrphanReasonStaleApplicationSetOwner = "stale_applicationset_metadata"

	argoCDOrphanSourceCache = "cache"
	argoCDOrphanSourceLive  = "live"

	argoCDApplicationManagedByLabel                = "app.kubernetes.io/managed-by"
	argoCDApplicationManagedByAstronomer           = "astronomer"
	argoCDApplicationBaselineLabel                 = "astronomer.io/baseline"
	argoCDApplicationBaselinePlatform              = "platform"
	argoCDApplicationToolSlugLabel                 = "astronomer.io/tool-slug"
	argoCDApplicationBaselineTargetLabel           = "astronomer.io/baseline-target"
	argoCDApplicationBaselineTargetAdoptedClusters = "adopted-clusters"
)

type argoCDOrphanReport struct {
	InstanceID             string                    `json:"instance_id"`
	ApplicationCount       int                       `json:"application_count"`
	CachedApplicationCount int                       `json:"cached_application_count"`
	LiveApplicationCount   int                       `json:"live_application_count"`
	ManagedTargetCount     int                       `json:"managed_target_count"`
	OrphanApplicationCount int                       `json:"orphan_application_count"`
	OrphanApplications     []argoCDOrphanApplication `json:"orphan_applications"`
	LiveError              string                    `json:"live_error,omitempty"`
	GeneratedAt            string                    `json:"generated_at"`
}

type argoCDOrphanApplication struct {
	ID                   string `json:"id,omitempty"`
	Name                 string `json:"name"`
	ComponentSlug        string `json:"component_slug,omitempty"`
	ApplicationSetName   string `json:"application_set_name,omitempty"`
	DestinationCluster   string `json:"destination_cluster"`
	DestinationNamespace string `json:"destination_namespace,omitempty"`
	Reason               string `json:"reason"`
	Source               string `json:"source"`
	Message              string `json:"message"`
}

// InstanceOrphanReport handles GET /api/v1/argocd/instances/{id}/orphan-report/.
func (h *ArgoCDHandler) InstanceOrphanReport(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	// Orphan reports legitimately page wider than the ordinary list cap (200)
	// so large Argo CD instances aren't silently truncated mid-fleet. Ceiling
	// matches the previous author intent (5000) via queryLimitMax rather than
	// queryLimit's hard 200 clamp (PERF-04).
	limit := int32(queryLimitMax(r, 1000, 5000))

	apps, err := h.queries.ListAppsByInstance(r.Context(), sqlc.ListAppsByInstanceParams{
		ArgocdInstanceID: instance.ID,
		Limit:            limit,
		Offset:           0,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list cached ArgoCD applications")
		return
	}
	rows, err := h.queries.ListArgoCDManagedClusters(r.Context(), instance.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list ArgoCD managed clusters")
		return
	}
	liveApps, liveErr := h.argoCDClient(instance).ListApplications(r.Context())
	report := buildArgoCDOrphanReport(instance.ID, apps, liveApps, rows)
	if liveErr != nil {
		report.LiveError = liveErr.Error()
	}
	RespondJSON(w, http.StatusOK, report)
}

func buildArgoCDOrphanReport(instanceID uuid.UUID, apps []sqlc.ArgocdApplication, liveApps []argocdclient.Application, rows []sqlc.ArgocdManagedCluster) argoCDOrphanReport {
	targets := argoCDManagedClusterTargetSet(rows)
	orphans := make([]argoCDOrphanApplication, 0)
	liveNames := make(map[string]struct{}, len(liveApps))
	for _, app := range liveApps {
		liveNames[strings.TrimSpace(app.Metadata.Name)] = struct{}{}
		orphans = append(orphans, liveArgoApplicationOrphans(app, targets)...)
	}
	for _, app := range apps {
		if _, hasLiveApp := liveNames[strings.TrimSpace(app.Name)]; hasLiveApp {
			continue
		}
		component, ok := baselineComponentForApplicationName(app.Name)
		if !ok {
			continue
		}
		destination := strings.TrimSpace(app.DestinationCluster)
		if destination != "" {
			if _, ok := targets[destination]; ok {
				continue
			}
		}
		reason := argoCDOrphanReasonStaleDestination
		message := "Astronomer-generated baseline Application destination does not match a managed ArgoCD cluster registration for this instance."
		if destination == "" {
			reason = argoCDOrphanReasonMissingDestination
			message = "Astronomer-generated baseline Application has no destination cluster recorded in the cache."
		}
		orphans = append(orphans, argoCDOrphanApplication{
			ID:                   app.ID.String(),
			Name:                 app.Name,
			ComponentSlug:        component.Slug,
			ApplicationSetName:   component.ApplicationSetName,
			DestinationCluster:   destination,
			DestinationNamespace: strings.TrimSpace(app.DestinationNamespace),
			Reason:               reason,
			Source:               argoCDOrphanSourceCache,
			Message:              message,
		})
	}
	return argoCDOrphanReport{
		InstanceID:             instanceID.String(),
		ApplicationCount:       len(apps) + len(liveApps),
		CachedApplicationCount: len(apps),
		LiveApplicationCount:   len(liveApps),
		ManagedTargetCount:     len(targets),
		OrphanApplicationCount: len(orphans),
		OrphanApplications:     orphans,
		GeneratedAt:            time.Now().UTC().Format(time.RFC3339),
	}
}

func liveArgoApplicationOrphans(app argocdclient.Application, targets map[string]struct{}) []argoCDOrphanApplication {
	name := strings.TrimSpace(app.Metadata.Name)
	component, nameMatchesBaseline := baselineComponentForApplicationName(name)
	managedByAstronomer := app.Metadata.Labels[argoCDApplicationManagedByLabel] == argoCDApplicationManagedByAstronomer ||
		app.Metadata.Labels[argoCDApplicationBaselineLabel] == argoCDApplicationBaselinePlatform ||
		app.Metadata.Labels[argoCDApplicationBaselineTargetLabel] == argoCDApplicationBaselineTargetAdoptedClusters
	ownerName, hasApplicationSetOwner := argoCDApplicationSetOwnerName(app)
	ownedByKnownBaselineApplicationSet := hasApplicationSetOwner && isBaselineApplicationSetName(ownerName)
	if !nameMatchesBaseline && !managedByAstronomer && !ownedByKnownBaselineApplicationSet {
		return nil
	}

	var out []argoCDOrphanApplication
	destination := ""
	namespace := ""
	if app.Spec.Destination != nil {
		destination = strings.TrimSpace(firstNonEmptyString(app.Spec.Destination.Server, app.Spec.Destination.Name))
		namespace = strings.TrimSpace(app.Spec.Destination.Namespace)
	}
	if destination == "" {
		out = append(out, argoCDOrphanApplication{
			Name:                 name,
			ComponentSlug:        component.Slug,
			ApplicationSetName:   component.ApplicationSetName,
			DestinationCluster:   destination,
			DestinationNamespace: namespace,
			Reason:               argoCDOrphanReasonLiveMissingDestination,
			Source:               argoCDOrphanSourceLive,
			Message:              "Live Astronomer-managed ArgoCD Application has no destination cluster.",
		})
	} else if _, ok := targets[destination]; !ok {
		out = append(out, argoCDOrphanApplication{
			Name:                 name,
			ComponentSlug:        component.Slug,
			ApplicationSetName:   component.ApplicationSetName,
			DestinationCluster:   destination,
			DestinationNamespace: namespace,
			Reason:               argoCDOrphanReasonLiveStaleDestination,
			Source:               argoCDOrphanSourceLive,
			Message:              "Live Astronomer-managed ArgoCD Application destination does not match a managed cluster registration for this instance.",
		})
	}

	if nameMatchesBaseline {
		if staleApplicationSetMetadata(app, component, ownerName, hasApplicationSetOwner) {
			out = append(out, argoCDOrphanApplication{
				Name:                 name,
				ComponentSlug:        component.Slug,
				ApplicationSetName:   component.ApplicationSetName,
				DestinationCluster:   destination,
				DestinationNamespace: namespace,
				Reason:               argoCDOrphanReasonStaleApplicationSetOwner,
				Source:               argoCDOrphanSourceLive,
				Message:              "Live baseline Application has stale ApplicationSet ownership or baseline labels.",
			})
		}
	}
	return out
}

func staleApplicationSetMetadata(app argocdclient.Application, component baselineComponentCatalogItem, ownerName string, hasApplicationSetOwner bool) bool {
	if slug := strings.TrimSpace(app.Metadata.Labels[argoCDApplicationToolSlugLabel]); slug != "" && slug != component.Slug {
		return true
	}
	if target := strings.TrimSpace(app.Metadata.Labels[argoCDApplicationBaselineTargetLabel]); target != "" && target != argoCDApplicationBaselineTargetAdoptedClusters {
		return true
	}
	return hasApplicationSetOwner && ownerName != "" && ownerName != component.ApplicationSetName
}

func argoCDApplicationSetOwnerName(app argocdclient.Application) (string, bool) {
	for _, owner := range app.Metadata.OwnerReferences {
		if owner.Kind != "ApplicationSet" {
			continue
		}
		return strings.TrimSpace(owner.Name), true
	}
	return "", false
}

func isBaselineApplicationSetName(name string) bool {
	name = strings.TrimSpace(name)
	for _, item := range platformBaselineComponentCatalog {
		if name == item.ApplicationSetName {
			return true
		}
	}
	return false
}

func argoCDManagedClusterTargetSet(rows []sqlc.ArgocdManagedCluster) map[string]struct{} {
	targets := make(map[string]struct{}, len(rows)*4)
	for _, row := range rows {
		addArgoCDTarget(targets, row.ServerUrl)
		addArgoCDTarget(targets, row.ClusterSecretName)
		var labels map[string]string
		if err := json.Unmarshal(row.Labels, &labels); err == nil {
			addArgoCDTarget(targets, labels["astronomer.io/cluster-name"])
			addArgoCDTarget(targets, labels["astronomer.io/cluster-id"])
		}
	}
	return targets
}

func addArgoCDTarget(targets map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	targets[value] = struct{}{}
}

func baselineComponentForApplicationName(name string) (baselineComponentCatalogItem, bool) {
	name = strings.TrimSpace(name)
	for _, item := range platformBaselineComponentCatalog {
		prefix := strings.TrimSpace(item.ApplicationPrefix)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(name, prefix+"-") {
			return item, true
		}
	}
	return baselineComponentCatalogItem{}, false
}
