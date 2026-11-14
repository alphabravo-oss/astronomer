package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestDeliveryIdempotencyBindsResolvedProjectAndConcreteTarget(t *testing.T) {
	t.Parallel()
	projectA, projectB, deniedProject := uuid.New(), uuid.New(), uuid.New()
	resourceA, resourceB := uuid.New(), uuid.New()
	bindings := append(deliveryProjectBinding(projectA, rbac.ResourceDeliveryTargets, rbac.VerbUpdate),
		deliveryProjectBinding(projectB, rbac.ResourceDeliveryTargets, rbac.VerbUpdate)...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{ID: "scope-test-user"})))
		})
	})
	router.Use(deliveryProjectScope)
	router.Use(requirePermission(rbac.NewEngine(), deliveryRouteRBACQuerier{bindings: bindings}, rbac.ResourceDeliveryTargets, rbac.VerbUpdate))
	calls := map[string]int{}
	router.With(appmiddleware.Idempotency(ctx)).Post("/targets/{id}/orphan/", func(w http.ResponseWriter, r *http.Request) {
		id, err := reqctx.RouteUUID(r, "id")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		project, err := reqctx.ProjectID(r)
		if err != nil {
			t.Fatal(err)
		}
		calls[project.String()+"/"+id.String()]++
		w.WriteHeader(http.StatusAccepted)
	})
	for _, project := range []uuid.UUID{projectA, projectB} {
		for _, resource := range []uuid.UUID{resourceA, resourceB} {
			for attempt := range 2 {
				request := httptest.NewRequest(http.MethodPost, "/targets/"+resource.String()+"/orphan/", strings.NewReader(`{}`))
				request.Header.Set("X-Project-ID", project.String())
				request.Header.Set("Idempotency-Key", "same-key")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != http.StatusAccepted || (response.Header().Get("Idempotent-Replayed") == "true") != (attempt == 1) {
					t.Fatalf("project=%s resource=%s attempt=%d status=%d replay=%q", project, resource, attempt, response.Code, response.Header().Get("Idempotent-Replayed"))
				}
			}
			if calls[project.String()+"/"+resource.String()] != 1 {
				t.Fatal("distinct authorized target did not execute exactly once")
			}
		}
	}
	for _, tc := range []struct {
		name, path, header string
		status             int
	}{
		{"scope mismatch", "/targets/" + resourceA.String() + "/orphan/?project_id=" + projectB.String(), projectA.String(), http.StatusBadRequest},
		{"invalid project", "/targets/" + resourceA.String() + "/orphan/", "invalid", http.StatusBadRequest},
		{"unauthorized project", "/targets/" + resourceA.String() + "/orphan/", deniedProject.String(), http.StatusForbidden},
		{"invalid target", "/targets/not-a-uuid/orphan/", projectA.String(), http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{}`))
		request.Header.Set("X-Project-ID", tc.header)
		request.Header.Set("Idempotency-Key", "same-key")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != tc.status || response.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("%s: status=%d replay=%q", tc.name, response.Code, response.Header().Get("Idempotent-Replayed"))
		}
	}
	for target, count := range calls {
		if count != 1 {
			t.Fatalf("target %s mutated %d times", target, count)
		}
	}
}
