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
	if deps.Delivery.Sources == nil && deps.Delivery.Bundles == nil && deps.Delivery.Targets == nil &&
		deps.Delivery.Rollouts == nil && deps.Delivery.Deployments == nil && deps.Delivery.Inventory == nil && deps.Delivery.System == nil {
		return
	}

	writeProjects := appmiddleware.RequireWriteScopeForMutations(iauth.ScopeWriteProjects)
	// Idempotency executes after scope and RBAC on each mutation, so a cached
	// response cannot bypass a permission revocation during its short TTL.
	idempotency := appmiddleware.Idempotency(context.Background())

	r.Route("/delivery", func(r chi.Router) {
		if deps.Delivery.Inventory != nil {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryInventory, rbac.VerbRead)).
				Get("/estate/", deps.Delivery.Inventory.Estate)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbRead)).
				Get("/system/compatibility/", deps.Delivery.Inventory.SystemCompatibility)
		}
		if deps.Delivery.System != nil {
			r.Route("/system/rollouts", func(r chi.Router) {
				r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbCreate), idempotency).
					Post("/", deps.Delivery.System.Start)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbRead)).
					Get("/{id}/", deps.Delivery.System.Get)
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbRead)).
					Get("/{id}/clusters/", deps.Delivery.System.Assignments)
				for _, route := range []struct {
					path    string
					handler http.HandlerFunc
				}{
					{"approve", deps.Delivery.System.Approve}, {"pause", deps.Delivery.System.Pause},
					{"resume", deps.Delivery.System.Resume}, {"abort", deps.Delivery.System.Abort},
					{"retry", deps.Delivery.System.Retry}, {"rollback", deps.Delivery.System.Rollback},
				} {
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryPlatform, rbac.VerbUpdate), idempotency).
						Post("/{id}/"+route.path+"/", route.handler)
				}
			})
		}

		r.Group(func(r chi.Router) {
			r.Use(deliveryProjectScope)

			if deps.Delivery.Sources != nil {
				r.Route("/sources", func(r chi.Router) {
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbList)).
						Get("/", deps.Delivery.Sources.List)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbCreate), idempotency).
						Post("/", deps.Delivery.Sources.Create)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbRead)).
						Get("/{id}/", deps.Delivery.Sources.Get)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbUpdate), idempotency).
						Patch("/{id}/", deps.Delivery.Sources.Update)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbDelete), idempotency).
						Delete("/{id}/", deps.Delivery.Sources.Delete)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbUpdate), idempotency).
						Post("/{id}/rotate-credential/", deps.Delivery.Sources.RotateCredential)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliverySources, rbac.VerbUpdate), idempotency).
						Post("/{id}/verify/", deps.Delivery.Sources.Verify)
				})
			}

			if deps.Delivery.Bundles != nil {
				r.Route("/bundles", func(r chi.Router) {
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbList)).
						Get("/", deps.Delivery.Bundles.List)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbCreate), idempotency).
						Post("/", deps.Delivery.Bundles.Create)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbRead)).
						Get("/{id}/", deps.Delivery.Bundles.Get)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbUpdate), idempotency).
						Patch("/{id}/", deps.Delivery.Bundles.Update)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbDelete), idempotency).
						Delete("/{id}/", deps.Delivery.Bundles.Delete)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbList)).
						Get("/{id}/versions/", deps.Delivery.Bundles.ListVersions)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbCreate), idempotency).
						Post("/{id}/versions/", deps.Delivery.Bundles.CreateVersion)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryBundles, rbac.VerbRead)).
						Get("/{id}/versions/{versionId}/", deps.Delivery.Bundles.GetVersion)
				})
			}

			if deps.Delivery.Targets != nil {
				r.Route("/targets", func(r chi.Router) {
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbList)).Get("/", deps.Delivery.Targets.List)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbCreate), idempotency).Post("/", deps.Delivery.Targets.Create)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbRead)).Get("/{id}/", deps.Delivery.Targets.Get)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbUpdate), idempotency).Patch("/{id}/", deps.Delivery.Targets.Update)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbDelete), idempotency).Delete("/{id}/", deps.Delivery.Targets.Delete)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryTargets, rbac.VerbRead)).Post("/{id}/preview/", deps.Delivery.Targets.Preview)
					if deps.Delivery.Rollouts != nil {
						r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbCreate), idempotency).Post("/{id}/rollouts/", deps.Delivery.Rollouts.Start)
					}
					r.With(writeProjects, requireSuperuser(deps), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryOrphans, rbac.VerbOrphan), idempotency).Post("/{id}/orphan/", deps.Delivery.Targets.Orphan)
				})
			}

			if deps.Delivery.Rollouts != nil {
				r.Route("/rollouts", func(r chi.Router) {
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbList)).Get("/", deps.Delivery.Rollouts.List)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbRead)).Get("/{id}/", deps.Delivery.Rollouts.Get)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbRead)).Get("/{id}/clusters/", deps.Delivery.Rollouts.Clusters)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbRead)).Get("/{id}/events/", deps.Delivery.Rollouts.Events)
					for _, route := range []struct {
						path    string
						handler http.HandlerFunc
					}{{"pause", deps.Delivery.Rollouts.Pause}, {"resume", deps.Delivery.Rollouts.Resume}, {"abort", deps.Delivery.Rollouts.Abort}, {"retry", deps.Delivery.Rollouts.Retry}} {
						r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollouts, rbac.VerbUpdate), idempotency).Post("/{id}/"+route.path+"/", route.handler)
					}
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryApprovals, rbac.VerbApprove), idempotency).Post("/{id}/approve/", deps.Delivery.Rollouts.Approve)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryRollbacks, rbac.VerbRollback), idempotency).Post("/{id}/rollback/", deps.Delivery.Rollouts.Rollback)
				})
			}

			if deps.Delivery.Deployments != nil {
				r.Route("/deployments", func(r chi.Router) {
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbList)).Get("/", deps.Delivery.Deployments.List)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbRead)).Get("/{id}/", deps.Delivery.Deployments.Get)
					r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbRead)).Get("/{id}/events/", deps.Delivery.Deployments.Events)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbUpdate), idempotency).Post("/{id}/reconcile/", deps.Delivery.Deployments.Reconcile)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbUpdate), idempotency).Post("/{id}/suspend/", deps.Delivery.Deployments.Suspend)
					r.With(writeProjects, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryDeployments, rbac.VerbUpdate), idempotency).Post("/{id}/resume/", deps.Delivery.Deployments.Resume)
				})
			}

			if deps.Delivery.Inventory != nil {
				r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceDeliveryInventory, rbac.VerbRead)).
					Get("/clusters/{clusterId}/inventory/", deps.Delivery.Inventory.Cluster)
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
