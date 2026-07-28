package handler

// Destination-scoped authorization for the ArgoCD lifecycle routes.
//
// Every test here fails before the fix: the routes authorized against the
// cluster the Argo CD INSTANCE runs on, so a caller holding workloads:update
// there could sync, patch, delete, register and unregister against every other
// cluster's delivery. The fixture models exactly that topology — one instance
// on the management cluster, one tenant cluster registered into it — and gives
// the caller full rights on the management cluster and nothing on the tenant.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const tenantArgoServerURL = "https://tenant.example:6443"

// destinationAuthzQuerier adds the cluster + registration reads the destination
// resolver needs on top of the shared ArgoCD recorder.
type destinationAuthzQuerier struct {
	*argoCDQueryRecorder
	clusters map[uuid.UUID]sqlc.Cluster
	managed  []sqlc.ArgocdManagedCluster
}

func (q *destinationAuthzQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	cluster, ok := q.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return cluster, nil
}

func (q *destinationAuthzQuerier) ListArgoCDManagedClusters(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return q.managed, nil
}

func (q *destinationAuthzQuerier) GetArgoCDManagedCluster(_ context.Context, arg sqlc.GetArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error) {
	for _, row := range q.managed {
		if row.ClusterID == arg.ClusterID {
			return row, nil
		}
	}
	return sqlc.ArgocdManagedCluster{}, pgx.ErrNoRows
}

// destinationFixture is the shipped topology in miniature: one Argo CD instance
// bound to the management cluster, with a separate tenant cluster registered
// into it.
type destinationFixture struct {
	h        *ArgoCDHandler
	q        *destinationAuthzQuerier
	mgmtID   uuid.UUID
	tenantID uuid.UUID
}

func newDestinationFixture(t *testing.T, upstream http.HandlerFunc) *destinationFixture {
	t.Helper()
	t.Cleanup(httpclient.DisableGuardForTest())
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)

	mgmtID, tenantID, instanceID := uuid.New(), uuid.New(), uuid.New()
	rec := &argoCDQueryRecorder{
		app: sqlc.ArgocdApplication{
			ID:                   uuid.New(),
			ArgocdInstanceID:     instanceID,
			Name:                 "tenant-app",
			UpstreamUid:          "tenant-app-uid",
			DestinationCluster:   tenantArgoServerURL,
			DestinationNamespace: "default",
		},
		instance: sqlc.ArgocdInstance{
			ID:        instanceID,
			Name:      "local",
			ClusterID: mgmtID,
			ApiUrl:    srv.URL,
			VerifySsl: true,
		},
	}
	q := &destinationAuthzQuerier{
		argoCDQueryRecorder: rec,
		clusters: map[uuid.UUID]sqlc.Cluster{
			mgmtID:   {ID: mgmtID, Name: "management", IsLocal: true},
			tenantID: {ID: tenantID, Name: "tenant-prod", ApiServerUrl: tenantArgoServerURL},
		},
		managed: []sqlc.ArgocdManagedCluster{
			{ID: uuid.New(), ArgocdInstanceID: instanceID, ClusterID: mgmtID, ServerUrl: "https://kubernetes.default.svc", Labels: argoManagedLabels(mgmtID)},
			{ID: uuid.New(), ArgocdInstanceID: instanceID, ClusterID: tenantID, ServerUrl: tenantArgoServerURL, Labels: argoManagedLabels(tenantID)},
		},
	}
	h := NewArgoCDHandler(q)
	h.http = srv.Client()
	return &destinationFixture{h: h, q: q, mgmtID: mgmtID, tenantID: tenantID}
}

func argoManagedLabels(clusterID uuid.UUID) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{
		astronomerManagedByLabelKey: astronomerManagedByLabelValue,
		astronomerClusterIDLabelKey: clusterID.String(),
	})
	return raw
}

// grant binds rules at one cluster scope. Multiple calls compose so a test can
// say "everything on management, nothing on the tenant" precisely.
func (f *destinationFixture) grant(bindings ...rbac.RoleBinding) {
	f.h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: bindings})
}

func argoClusterGrant(clusterID uuid.UUID, resource rbac.Resource, verbs ...rbac.Verb) rbac.RoleBinding {
	vs := make([]string, 0, len(verbs))
	for _, v := range verbs {
		vs = append(vs, string(v))
	}
	return rbac.RoleBinding{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(resource), Verbs: vs}},
	}
}

func argoDestReq(method, target string, params map[string]string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	route := chi.NewRouteContext()
	for k, v := range params {
		route.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: uuid.NewString()})
	return req.WithContext(ctx)
}

// liveAppHandler serves one Application with the given destination block, and
// counts every non-GET request so a test can prove a denial never reached the
// upstream mutation.
func liveAppHandler(destination string, mutations *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && mutations != nil {
			mutations.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"tenant-app","uid":"tenant-app-uid","namespace":"argocd"},` +
			`"spec":{"project":"default","source":{"repoURL":"https://example.test/repo.git","path":"app","targetRevision":"main"},"destination":{` + destination + `}},` +
			`"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Healthy"}}}`))
	}
}

// liveAppSetHandler serves one fleet-wide ApplicationSet — a `clusters`
// generator selecting every Astronomer-managed cluster, with the `{{server}}`
// template destination the platform's own sets use — and counts every non-GET
// request, so a test can prove a denial never reached the upstream delete.
func liveAppSetHandler(mutations *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && mutations != nil {
			mutations.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"fanout","namespace":"argocd"},"spec":{` +
			`"generators":[{"clusters":{"selector":{"matchLabels":{"` + astronomerManagedByLabelKey + `":"` + astronomerManagedByLabelValue + `"}}}}],` +
			`"template":{"metadata":{"name":"{{name}}-app"},"spec":{"project":"default",` +
			`"source":{"repoURL":"https://example.test/repo.git","path":"app","targetRevision":"main"},` +
			`"destination":{"server":"{{server}}","namespace":"default"}}}}}`))
	}
}

// TestSyncAppDeniesDestinationClusterWithoutGrant is the core assertion: a
// caller with workloads:update on the ArgoCD instance's own cluster — and
// nothing on the destination — must not be able to sync (and therefore prune)
// an Application that deploys to another cluster. Before the fix this returned
// 202 and enqueued the sync.
func TestSyncAppDeniesDestinationClusterWithoutGrant(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`","namespace":"default"`, nil))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	rr := httptest.NewRecorder()
	f.h.SyncApp(rr, argoDestReq(http.MethodPost, "/api/v1/argocd/applications/x/sync/", map[string]string{"id": f.q.app.ID.String()}, `{"prune":true}`))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-cluster sync: want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(f.q.created) != 0 {
		t.Fatalf("denied sync must not enqueue an operation: %d created", len(f.q.created))
	}
}

// TestSyncAppAllowsAuthorizedDestinationCluster is the positive half — the
// legitimate flow must keep working. The same route, for a caller who also
// holds workloads:update on the destination, still enqueues the sync.
func TestSyncAppAllowsAuthorizedDestinationCluster(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`","namespace":"default"`, nil))
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate),
		argoClusterGrant(f.tenantID, rbac.ResourceWorkloads, rbac.VerbUpdate),
	)

	rr := httptest.NewRecorder()
	f.h.SyncApp(rr, argoDestReq(http.MethodPost, "/api/v1/argocd/applications/x/sync/", map[string]string{"id": f.q.app.ID.String()}, `{"prune":true}`))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("authorized sync: want 202, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(f.q.created) != 1 {
		t.Fatalf("authorized sync must enqueue exactly one operation, got %d", len(f.q.created))
	}
}

// TestSyncAppInClusterDestinationResolvesToInstanceCluster proves the
// management-cluster operator did not lose their own apps: `in-cluster` is
// Argo CD's name for the cluster it runs on, which IS the instance's cluster.
func TestSyncAppInClusterDestinationResolvesToInstanceCluster(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster","namespace":"astronomer"`, nil))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	rr := httptest.NewRecorder()
	f.h.SyncApp(rr, argoDestReq(http.MethodPost, "/api/v1/argocd/applications/x/sync/", map[string]string{"id": f.q.app.ID.String()}, `{}`))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("in-cluster sync: want 202, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestSyncAppDeniesUnresolvableDestination pins the fail-closed rule: a
// destination that maps to no cluster we know about is denied even for a caller
// who holds workloads:update on every cluster that does exist.
func TestSyncAppDeniesUnresolvableDestination(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"https://unregistered.example:6443","namespace":"default"`, nil))
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate),
		argoClusterGrant(f.tenantID, rbac.ResourceWorkloads, rbac.VerbUpdate),
	)

	rr := httptest.NewRecorder()
	f.h.SyncApp(rr, argoDestReq(http.MethodPost, "/api/v1/argocd/applications/x/sync/", map[string]string{"id": f.q.app.ID.String()}, `{}`))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("unresolvable destination: want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	if len(f.q.created) != 0 {
		t.Fatalf("unresolvable destination must not enqueue an operation")
	}
}

// TestSyncAppByNameDeniesDestinationClusterWithoutGrant covers the second sync
// route, which reaches the same chokepoint through the instance-scoped URL.
func TestSyncAppByNameDeniesDestinationClusterWithoutGrant(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`","namespace":"default"`, nil))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	rr := httptest.NewRecorder()
	f.h.SyncAppByName(rr, argoDestReq(http.MethodPost, "/sync", map[string]string{"id": f.q.instance.ID.String(), "name": "tenant-app"}, `{}`))

	if rr.Code != http.StatusForbidden || len(f.q.created) != 0 {
		t.Fatalf("cross-cluster sync-by-name: want 403 and no operation, got %d created=%d body=%s", rr.Code, len(f.q.created), rr.Body.String())
	}
}

// TestRefreshAppDeniesDestinationClusterWithoutGrant covers the refresh route,
// which re-evaluates the Application against its destination cluster.
func TestRefreshAppDeniesDestinationClusterWithoutGrant(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`","namespace":"default"`, &mutations))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	rr := httptest.NewRecorder()
	f.h.RefreshApp(rr, argoDestReq(http.MethodPost, "/refresh", map[string]string{"id": f.q.app.ID.String()}, ""))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("cross-cluster refresh: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestCreateApplicationDeniesUnauthorizedDestination proves the create route
// gates on the destination in the submitted spec, before the Application exists
// upstream.
func TestCreateApplicationDeniesUnauthorizedDestination(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`"`, &mutations))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbCreate))

	body := `{"name":"new-app","spec":{"project":"default","source":{"repoURL":"https://example.test/repo.git","path":"app","targetRevision":"main"},"destination":{"server":"` + tenantArgoServerURL + `","namespace":"default"}}}`
	rr := httptest.NewRecorder()
	f.h.CreateApplication(rr, argoDestReq(http.MethodPost, "/applications", map[string]string{"id": f.q.instance.ID.String()}, body))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("cross-cluster create: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestDeleteApplicationDeniesUnauthorizedDestination proves the delete route
// resolves the LIVE Application's destination — the request itself names only
// the Application.
func TestDeleteApplicationDeniesUnauthorizedDestination(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`","namespace":"default"`, &mutations))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbDelete))

	rr := httptest.NewRecorder()
	f.h.DeleteApplication(rr, argoDestReq(http.MethodDelete, "/applications/tenant-app?cascade=true", map[string]string{"id": f.q.instance.ID.String(), "name": "tenant-app"}, ""))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("cross-cluster delete: want 403 and no upstream delete, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestPatchApplicationDeniesRepointingToUnauthorizedDestination covers the
// other direction: the Application currently deploys where the caller IS
// authorized, and the patch would move it onto a cluster they hold nothing on.
// Authorizing only the current destination would let auto-sync finish the job.
func TestPatchApplicationDeniesRepointingToUnauthorizedDestination(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster","namespace":"astronomer"`, &mutations))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	body := `{"spec":{"destination":{"server":"` + tenantArgoServerURL + `","namespace":"default"}}}`
	rr := httptest.NewRecorder()
	f.h.PatchApplication(rr, argoDestReq(http.MethodPatch, "/applications/tenant-app", map[string]string{"id": f.q.instance.ID.String(), "name": "tenant-app"}, body))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("re-pointing patch: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestRegisterManagedClusterDeniesUnauthorizedCluster is scenario (a): the URL
// names the victim cluster, and before the fix nothing authorized it — the only
// check was on the instance's cluster.
func TestRegisterManagedClusterDeniesUnauthorizedCluster(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster"`, &mutations))
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate),
		argoClusterGrant(f.mgmtID, rbac.ResourceClusters, rbac.VerbUpdate),
	)

	body := `{"server":"https://attacker.example:6443","bearer_token":"t","insecure":true}`
	rr := httptest.NewRecorder()
	f.h.RegisterManagedCluster(rr, argoDestReq(http.MethodPost, "/register", map[string]string{"id": f.q.instance.ID.String(), "cluster_id": f.tenantID.String()}, body))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("cross-cluster register: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestRegisterManagedClusterAllowsAuthorizedCluster is the positive half: an
// operator holding clusters:update on the target cluster still registers it.
func TestRegisterManagedClusterAllowsAuthorizedCluster(t *testing.T) {
	f := newDestinationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":"` + tenantArgoServerURL + `","name":"tenant-prod"}`))
	})
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate),
		argoClusterGrant(f.tenantID, rbac.ResourceClusters, rbac.VerbUpdate),
	)

	body := `{"bearer_token":"tenant-token"}`
	rr := httptest.NewRecorder()
	f.h.RegisterManagedCluster(rr, argoDestReq(http.MethodPost, "/register", map[string]string{"id": f.q.instance.ID.String(), "cluster_id": f.tenantID.String()}, body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("authorized register: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUnregisterManagedClusterDeniesUnauthorizedCluster is scenario (b):
// dropping a tenant's registration breaks their GitOps delivery.
func TestUnregisterManagedClusterDeniesUnauthorizedCluster(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster"`, &mutations))
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate),
		argoClusterGrant(f.mgmtID, rbac.ResourceClusters, rbac.VerbUpdate),
	)

	rr := httptest.NewRecorder()
	f.h.UnregisterManagedCluster(rr, argoDestReq(http.MethodDelete, "/register", map[string]string{"id": f.q.instance.ID.String(), "cluster_id": f.tenantID.String()}, ""))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("cross-cluster unregister: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestRefreshManagedClusterLabelsDeniesUnauthorizedCluster covers the third
// registration route — re-stamping labels changes which ApplicationSets adopt
// the cluster.
func TestRefreshManagedClusterLabelsDeniesUnauthorizedCluster(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster"`, nil))
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate),
		argoClusterGrant(f.mgmtID, rbac.ResourceClusters, rbac.VerbUpdate),
	)

	rr := httptest.NewRecorder()
	f.h.RefreshManagedClusterLabels(rr, argoDestReq(http.MethodPost, "/refresh-labels", map[string]string{"id": f.q.instance.ID.String(), "cluster_id": f.tenantID.String()}, ""))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-cluster refresh-labels: want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestRetryOperationDeniesDestinationClusterWithoutGrant closes the obvious
// bypass of the sync gate: a retry re-runs the sync, so it must be authorized
// against the same destination cluster.
func TestRetryOperationDeniesDestinationClusterWithoutGrant(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`"`, nil))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))
	payload, _ := json.Marshal(argocdOperationEnvelope{
		ApplicationID: f.q.app.ID.String(),
		InstanceID:    f.q.instance.ID.String(),
		UpstreamUID:   f.q.app.UpstreamUid,
	})
	f.q.operation = sqlc.ArgocdOperation{
		ID:            uuid.New(),
		TargetType:    "application",
		TargetKey:     f.q.app.ID.String(),
		OperationType: "sync",
		Payload:       payload,
		Status:        OpStatusFailed,
	}

	rr := httptest.NewRecorder()
	f.h.RetryOperation(rr, argoDestReq(http.MethodPost, "/retry", map[string]string{"id": f.q.operation.ID.String()}, ""))

	if rr.Code != http.StatusForbidden || len(f.q.requeued) != 0 {
		t.Fatalf("cross-cluster retry: want 403 and no requeue, got %d requeued=%d body=%s", rr.Code, len(f.q.requeued), rr.Body.String())
	}
}

// TestCreateApplicationSetDeniesUnauthorizedGeneratorFanout proves the
// fan-out primitive is bounded by the caller's grants: a selector matching
// every Astronomer-managed cluster requires the verb on every one of them.
func TestCreateApplicationSetDeniesUnauthorizedGeneratorFanout(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster"`, &mutations))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbCreate))

	body := `{"name":"fanout","spec":{"generators":[{"clusters":{"selector":{"matchLabels":{"` + astronomerManagedByLabelKey + `":"` + astronomerManagedByLabelValue + `"}}}}],` +
		`"template":{"metadata":{"name":"{{name}}-app"},"spec":{"project":"default","source":{"repoURL":"https://example.test/repo.git","path":"app","targetRevision":"main"},"destination":{"server":"{{server}}","namespace":"default"}}}}}`
	rr := httptest.NewRecorder()
	f.h.CreateApplicationSet(rr, argoDestReq(http.MethodPost, "/applicationsets", map[string]string{"id": f.q.instance.ID.String()}, body))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("fleet-wide appset: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestCreateApplicationSetAllowsBoundedGeneratorFanout is the positive half: a
// selector narrowed to the cluster the caller is granted still succeeds.
func TestCreateApplicationSetAllowsBoundedGeneratorFanout(t *testing.T) {
	f := newDestinationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"fanout"},"spec":{"generators":[]}}`))
	})
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbCreate))

	body := `{"name":"fanout","spec":{"generators":[{"clusters":{"selector":{"matchLabels":{"` + astronomerManagedByLabelKey + `":"` + astronomerManagedByLabelValue + `","` + astronomerClusterIDLabelKey + `":"` + f.mgmtID.String() + `"}}}}],` +
		`"template":{"metadata":{"name":"{{name}}-app"},"spec":{"project":"default","source":{"repoURL":"https://example.test/repo.git","path":"app","targetRevision":"main"},"destination":{"server":"{{server}}","namespace":"default"}}}}}`
	rr := httptest.NewRecorder()
	f.h.CreateApplicationSet(rr, argoDestReq(http.MethodPost, "/applicationsets", map[string]string{"id": f.q.instance.ID.String()}, body))

	if rr.Code != http.StatusCreated {
		t.Fatalf("bounded appset: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestDeleteApplicationSetDeniesUnauthorizedGeneratorFanout is the delete half
// of the fan-out gate. Deleting an ApplicationSet cascades to its generated
// Applications, which carry a resources-finalizer, so it removes the delivered
// workloads from every cluster the set reaches — a caller blocked from
// creating such a set must not be able to delete one either.
func TestDeleteApplicationSetDeniesUnauthorizedGeneratorFanout(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppSetHandler(&mutations))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbDelete))

	rr := httptest.NewRecorder()
	f.h.DeleteApplicationSet(rr, argoDestReq(http.MethodDelete, "/applicationsets/fanout", map[string]string{"id": f.q.instance.ID.String(), "name": "fanout"}, ""))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("fleet-wide appset delete: want 403 and no upstream delete, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestDeleteApplicationSetAllowsBoundedGeneratorFanout is the positive half: a
// set whose selector only reaches the cluster the caller holds delete on is
// still deletable.
func TestDeleteApplicationSetAllowsBoundedGeneratorFanout(t *testing.T) {
	f := newDestinationFixture(t, liveAppSetHandler(nil))
	f.grant(
		argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbDelete),
		argoClusterGrant(f.tenantID, rbac.ResourceWorkloads, rbac.VerbDelete),
	)

	rr := httptest.NewRecorder()
	f.h.DeleteApplicationSet(rr, argoDestReq(http.MethodDelete, "/applicationsets/fanout", map[string]string{"id": f.q.instance.ID.String(), "name": "fanout"}, ""))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("bounded appset delete: want 204, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestRefreshAppReadsLiveDestinationNotCachedRow pins the staleness fix:
// RefreshApp runs no discovery of its own, so the cached destination_cluster
// can name the cluster the Application USED to deploy to. A hard refresh makes
// Argo CD reconcile against the live destination, so that is what is gated.
func TestRefreshAppReadsLiveDestinationNotCachedRow(t *testing.T) {
	var mutations atomic.Int32
	f := newDestinationFixture(t, liveAppHandler(`"server":"`+tenantArgoServerURL+`","namespace":"default"`, &mutations))
	// Stale row: the last discovery saw the app on the management cluster.
	f.q.app.DestinationCluster = "https://kubernetes.default.svc"
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	rr := httptest.NewRecorder()
	f.h.RefreshApp(rr, argoDestReq(http.MethodPost, "/refresh?hard=true", map[string]string{"id": f.q.app.ID.String()}, ""))

	if rr.Code != http.StatusForbidden || mutations.Load() != 0 {
		t.Fatalf("stale-cache refresh: want 403 and no upstream write, got %d upstream=%d body=%s", rr.Code, mutations.Load(), rr.Body.String())
	}
}

// TestPatchApplicationAllowsNamespaceOnlyRetarget guards the other side of the
// patch gate. A merge patch whose destination block carries neither `name` nor
// `server` does not move the Application off the cluster the caller was already
// authorized on, so it must not be read as "no destination" and denied.
func TestPatchApplicationAllowsNamespaceOnlyRetarget(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"name":"in-cluster","namespace":"astronomer"`, nil))
	f.grant(argoClusterGrant(f.mgmtID, rbac.ResourceWorkloads, rbac.VerbUpdate))

	rr := httptest.NewRecorder()
	f.h.PatchApplication(rr, argoDestReq(http.MethodPatch, "/applications/tenant-app", map[string]string{"id": f.q.instance.ID.String(), "name": "tenant-app"}, `{"spec":{"destination":{"namespace":"other"}}}`))

	if rr.Code != http.StatusOK {
		t.Fatalf("namespace-only re-target: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestDestinationAuthzSuperuserPassesUnresolvableDestination is the escape
// hatch the file's contract promises. bindingsForContext reports EVERY
// authenticated user as restricted — superuser is a binding, not that flag — so
// without an explicit short-circuit a superuser was denied on any destination
// with no registration row (`argocd cluster add`, or a `name` override we never
// persisted), locking the product's most privileged principal out of sync,
// refresh, patch and delete with no way back.
func TestDestinationAuthzSuperuserPassesUnresolvableDestination(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"https://unregistered.example:6443","namespace":"default"`, nil))
	f.grant(rbac.RoleBinding{IsSuperuser: true})

	rr := httptest.NewRecorder()
	f.h.SyncApp(rr, argoDestReq(http.MethodPost, "/api/v1/argocd/applications/x/sync/", map[string]string{"id": f.q.app.ID.String()}, `{}`))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("superuser sync of an out-of-band destination: want 202, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestDestinationAuthzSkippedForUnrestrictedCallers pins the machine-path half
// of that contract: the reconciler and self-manage flows carry no
// authenticated user at all, and must not be blocked by a destination that has
// no registration row yet. (The superuser half is the test above.)
func TestDestinationAuthzSkippedForUnrestrictedCallers(t *testing.T) {
	f := newDestinationFixture(t, liveAppHandler(`"server":"https://unregistered.example:6443","namespace":"default"`, nil))
	// No SetAuthorization and no authenticated user: bindingsForContext
	// reports "not restricted", exactly as it does for the reconciler.
	req := httptest.NewRequest(http.MethodPost, "/sync", strings.NewReader(`{}`))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", f.q.app.ID.String())
	rr := httptest.NewRecorder()
	f.h.SyncApp(rr, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route)))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("unrestricted sync: want 202, got %d body=%s", rr.Code, rr.Body.String())
	}
}
