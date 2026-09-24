package server

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type scopedDiscoveryRequester struct {
	calls    int
	identity protocol.CallerIdentity
}

func (q *scopedDiscoveryRequester) Do(ctx context.Context, _ string, _, _ string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	q.calls++
	q.identity = callerid.Resolve(ctx)
	return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(`{"metadata":{},"items":[]}`))}, nil
}

func TestCanonicalResourceDiscoveryAllowsClusterReaderWithoutRawCRDGrant(t *testing.T) {
	jwt := auth.MustNewJWTManager("discovery-scope-secret", 60)
	user, cluster := uuid.New(), uuid.New()
	token := nsRBACProxyToken(t, jwt, user)
	requester := &scopedDiscoveryRequester{}
	bindings := append(namespaceScopedListBindings(cluster, "project-a", rbac.ResourceCustomResources), rbac.RoleBinding{ClusterID: cluster.String(), RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{"read"}}}})
	router := NewRouter(&config.Config{}, RouterDependencies{CoreAuth: CoreAuthDependencies{JWT: jwt, RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{bindings: bindings}}, ClusterResources: ClusterResourceDependencies{Resources: handler.NewResourceHandlerWithRequester(requester)}, StreamingInternal: StreamingInternalDependencies{Proxy: tunnel.NewProxyHandler(tunnel.NewHub(nil), nil)}})
	for _, tt := range []struct {
		path   string
		status int
	}{
		{"/api/v1/clusters/" + cluster.String() + "/resources/discovery/?crd_continue=cursor", 200},
		{"/api/v1/clusters/" + cluster.String() + "/k8s/apis/apiextensions.k8s.io/v1/customresourcedefinitions", 403},
		{"/api/v1/clusters/" + uuid.NewString() + "/resources/discovery/?crd_continue=cursor", 403},
	} {
		req := httptest.NewRequest("GET", tt.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tt.status {
			t.Fatalf("%s status%d want%d: %s", tt.path, rec.Code, tt.status, rec.Body.String())
		}
		if tt.status == 200 && !strings.Contains(rec.Body.String(), `"crd_continue":""`) {
			t.Fatalf("missing completion marker: %s", rec.Body.String())
		}
	}
	if requester.calls != 1 || requester.identity.User != callerid.UserSubject(user) || requester.identity.IsMachine() {
		t.Fatalf("calls%d identity%+v", requester.calls, requester.identity)
	}
}
