package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestK8sProxyForceConflictRequiresManage(t *testing.T) {
	request := func(rawQuery string) *http.Request {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/clusters/c/k8s/apis/apps/v1/namespaces/default/deployments/web?"+rawQuery, nil)
		route := chi.NewRouteContext()
		route.URLParams.Add("*", "apis/apps/v1/namespaces/default/deployments/web")
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	}
	resource, verb := k8sProxyPermission(request("fieldManager=astronomer"))
	if resource != rbac.ResourceWorkloads || verb != rbac.VerbUpdate {
		t.Fatalf("ordinary apply = %s:%s", resource, verb)
	}
	resource, verb = k8sProxyPermission(request("fieldManager=astronomer&force=true"))
	if resource != rbac.ResourceWorkloads || verb != rbac.VerbManage {
		t.Fatalf("forced apply = %s:%s, want workloads:manage", resource, verb)
	}
	_, verb = k8sProxyPermission(request("fieldManager=astronomer&force=true&dryRun=All"))
	if verb != rbac.VerbUpdate {
		t.Fatalf("dry-run force should not require manage, got %s", verb)
	}
}
