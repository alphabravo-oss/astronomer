package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const deliveryRouteMaxBodyBytes = 1 << 20

// registerDeliveryRoutes wires the first-party, Flux-native delivery control
// plane. Authentication is inherited from registerProtectedRoutes. Every route
// additionally resolves one authoritative project scope and evaluates the
// dedicated resource permission at that exact project.
func registerDeliveryRoutes(r chi.Router, deps RouterDependencies) {
	if deps.DeliverySources == nil && deps.DeliveryBundles == nil && deps.DeliveryTargets == nil &&
		deps.DeliveryRollouts == nil && deps.DeliveryDeployments == nil && deps.DeliveryInventory == nil && deps.DeliverySystem == nil {
		return
	}

	writeProjects := appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteProjects)
	// Idempotency executes after scope and RBAC on each mutation, so a cached
	// response cannot bypass a permission revocation during its short TTL.
	idempotency := appmiddleware.Idempotency(context.Background())

	r.Route("/delivery", func(r chi.Router) {
		if deps.DeliveryInventory != nil {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryInventory, rbac.VerbRead)).
				Get("/fleet/", deps.DeliveryInventory.Fleet)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbRead)).
				Get("/system/compatibility/", deps.DeliveryInventory.SystemCompatibility)
		}
		if deps.DeliverySystem != nil {
			r.Route("/system/rollouts", func(r chi.Router) {
				r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbCreate), idempotency).
					Post("/", deps.DeliverySystem.Start)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbRead)).
					Get("/{id}/", deps.DeliverySystem.Get)
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbRead)).
					Get("/{id}/clusters/", deps.DeliverySystem.Assignments)
				for _, route := range []struct {
					path    string
					handler http.HandlerFunc
				}{
					{"approve", deps.DeliverySystem.Approve}, {"pause", deps.DeliverySystem.Pause},
					{"resume", deps.DeliverySystem.Resume}, {"abort", deps.DeliverySystem.Abort},
					{"retry", deps.DeliverySystem.Retry}, {"rollback", deps.DeliverySystem.Rollback},
				} {
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbUpdate), idempotency).
						Post("/{id}/"+route.path+"/", route.handler)
				}
			})
		}

		r.Group(func(r chi.Router) {
			r.Use(deliveryProjectScope)

			if deps.DeliverySources != nil {
				r.Route("/sources", func(r chi.Router) {
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbList)).
						Get("/", deps.DeliverySources.List)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbCreate), idempotency).
						Post("/", deps.DeliverySources.Create)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbRead)).
						Get("/{id}/", deps.DeliverySources.Get)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbUpdate), idempotency).
						Patch("/{id}/", deps.DeliverySources.Update)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbDelete), idempotency).
						Delete("/{id}/", deps.DeliverySources.Delete)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbUpdate), idempotency).
						Post("/{id}/rotate-credential/", deps.DeliverySources.RotateCredential)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbUpdate), idempotency).
						Post("/{id}/verify/", deps.DeliverySources.Verify)
				})
			}

			if deps.DeliveryBundles != nil {
				r.Route("/bundles", func(r chi.Router) {
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbList)).
						Get("/", deps.DeliveryBundles.List)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbCreate), idempotency).
						Post("/", deps.DeliveryBundles.Create)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbRead)).
						Get("/{id}/", deps.DeliveryBundles.Get)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbUpdate), idempotency).
						Patch("/{id}/", deps.DeliveryBundles.Update)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbDelete), idempotency).
						Delete("/{id}/", deps.DeliveryBundles.Delete)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbList)).
						Get("/{id}/versions/", deps.DeliveryBundles.ListVersions)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbCreate), idempotency).
						Post("/{id}/versions/", deps.DeliveryBundles.CreateVersion)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbRead)).
						Get("/{id}/versions/{versionId}/", deps.DeliveryBundles.GetVersion)
				})
			}

			if deps.DeliveryTargets != nil {
				r.Route("/targets", func(r chi.Router) {
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbList)).Get("/", deps.DeliveryTargets.List)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbCreate), idempotency).Post("/", deps.DeliveryTargets.Create)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbRead)).Get("/{id}/", deps.DeliveryTargets.Get)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbUpdate), idempotency).Patch("/{id}/", deps.DeliveryTargets.Update)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbDelete), idempotency).Delete("/{id}/", deps.DeliveryTargets.Delete)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbRead)).Post("/{id}/preview/", deps.DeliveryTargets.Preview)
					if deps.DeliveryRollouts != nil {
						r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbCreate), idempotency).Post("/{id}/rollouts/", deps.DeliveryRollouts.Start)
					}
					r.With(writeProjects, requireSuperuser(deps), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryOrphans, rbac.VerbOrphan), idempotency).Post("/{id}/orphan/", deps.DeliveryTargets.Orphan)
				})
			}

			if deps.DeliveryRollouts != nil {
				r.Route("/rollouts", func(r chi.Router) {
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbList)).Get("/", deps.DeliveryRollouts.List)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbRead)).Get("/{id}/", deps.DeliveryRollouts.Get)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbRead)).Get("/{id}/clusters/", deps.DeliveryRollouts.Clusters)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbRead)).Get("/{id}/events/", deps.DeliveryRollouts.Events)
					for _, route := range []struct {
						path    string
						handler http.HandlerFunc
					}{{"pause", deps.DeliveryRollouts.Pause}, {"resume", deps.DeliveryRollouts.Resume}, {"abort", deps.DeliveryRollouts.Abort}, {"retry", deps.DeliveryRollouts.Retry}} {
						r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbUpdate), idempotency).Post("/{id}/"+route.path+"/", route.handler)
					}
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryApprovals, rbac.VerbApprove), idempotency).Post("/{id}/approve/", deps.DeliveryRollouts.Approve)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryRollbacks, rbac.VerbRollback), idempotency).Post("/{id}/rollback/", deps.DeliveryRollouts.Rollback)
				})
			}

			if deps.DeliveryDeployments != nil {
				r.Route("/deployments", func(r chi.Router) {
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbList)).Get("/", deps.DeliveryDeployments.List)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbRead)).Get("/{id}/", deps.DeliveryDeployments.Get)
					r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbRead)).Get("/{id}/events/", deps.DeliveryDeployments.Events)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbUpdate), idempotency).Post("/{id}/reconcile/", deps.DeliveryDeployments.Reconcile)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbUpdate), idempotency).Post("/{id}/suspend/", deps.DeliveryDeployments.Suspend)
					r.With(writeProjects, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbUpdate), idempotency).Post("/{id}/resume/", deps.DeliveryDeployments.Resume)
				})
			}

			if deps.DeliveryInventory != nil {
				r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceDeliveryInventory, rbac.VerbRead)).
					Get("/clusters/{clusterId}/inventory/", deps.DeliveryInventory.Cluster)
			}
		})
	})
}

// deliveryProjectScope normalizes the delivery API's documented
// query/header/body project scope into a chi {project_id} URL parameter before
// ordinary RBAC middleware runs. All supplied locations must agree. Mutation
// bodies are read under the same 1 MiB bound as the handler and restored byte
// for byte; this middleware never logs or retains credential-bearing content.
func deliveryProjectScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyProjectID, err := deliveryBodyProjectID(w, r)
		if err != nil {
			writeRouteAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		projectID, err := resolveDeliveryProjectID(r, bodyProjectID)
		if err != nil {
			writeRouteAuthError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
			return
		}

		routeContext := chi.RouteContext(r.Context())
		if routeContext == nil {
			writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "delivery route context is unavailable")
			return
		}
		cloned := *routeContext
		cloned.URLParams.Keys = append([]string(nil), routeContext.URLParams.Keys...)
		cloned.URLParams.Values = append([]string(nil), routeContext.URLParams.Values...)
		cloned.URLParams.Add("project_id", projectID.String())
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, &cloned)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func deliveryBodyProjectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, error) {
	if r == nil || (r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch) {
		return uuid.Nil, nil
	}
	if r.Body == nil {
		return uuid.Nil, nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deliveryRouteMaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return uuid.Nil, errors.New("request body exceeds 1048576 bytes")
		}
		return uuid.Nil, errors.New("request body could not be read")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return uuid.Nil, nil
	}
	var scope struct {
		ProjectID uuid.UUID `json:"project_id"`
	}
	if err := json.Unmarshal(body, &scope); err != nil {
		return uuid.Nil, errors.New("request body must be one valid JSON object")
	}
	return scope.ProjectID, nil
}

func resolveDeliveryProjectID(r *http.Request, bodyProjectID uuid.UUID) (uuid.UUID, error) {
	values := make([]string, 0, 3)
	if queryValues, ok := r.URL.Query()["project_id"]; ok {
		if len(queryValues) != 1 {
			return uuid.Nil, errors.New("project_id query parameter must occur exactly once")
		}
		values = append(values, queryValues[0])
	}
	if headerValues := r.Header.Values("X-Project-ID"); len(headerValues) != 0 {
		if len(headerValues) != 1 {
			return uuid.Nil, errors.New("X-Project-ID header must occur exactly once")
		}
		values = append(values, headerValues[0])
	}
	if bodyProjectID != uuid.Nil {
		values = append(values, bodyProjectID.String())
	}
	if len(values) == 0 {
		return uuid.Nil, errors.New("project scope is required")
	}

	var projectID uuid.UUID
	for _, value := range values {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return uuid.Nil, errors.New("project scope must be a non-zero UUID")
		}
		if projectID != uuid.Nil && projectID != parsed {
			return uuid.Nil, errors.New("project scopes do not match")
		}
		projectID = parsed
	}
	return projectID, nil
}
