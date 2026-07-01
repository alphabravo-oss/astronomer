package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// batchArgoClusterQuerier reuses the full ClusterQuerier fake and adds the
// batched ArgoCD surface, counting how the List path issues its ArgoCD queries.
type batchArgoClusterQuerier struct {
	*fakeAutoAttachClusterQuerier
	clustersList    []sqlc.Cluster
	managed         []sqlc.ArgocdManagedCluster
	apps            []sqlc.ArgocdApplication
	batchCalls      int
	perClusterCalls int
	appsCalls       int
}

func (q *batchArgoClusterQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return q.clustersList, nil
}

func (q *batchArgoClusterQuerier) CountClusters(context.Context) (int64, error) {
	return int64(len(q.clustersList)), nil
}

func (q *batchArgoClusterQuerier) ListArgoCDManagedClustersByClusterIDs(_ context.Context, _ []uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	q.batchCalls++
	return q.managed, nil
}

func (q *batchArgoClusterQuerier) ListArgoCDManagedClustersByCluster(_ context.Context, _ uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	q.perClusterCalls++
	return nil, nil
}

func (q *batchArgoClusterQuerier) ListArgoCDApplicationsByManagedClusterTargets(_ context.Context, _ sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams) ([]sqlc.ArgocdApplication, error) {
	q.appsCalls++
	return q.apps, nil
}

// The clusters-list dashboard must enrich ArgoCD state for a whole page with
// two queries (one managed-cluster batch + one apps batch), not ~2 per cluster.
// It must also stay correct: each cluster's drift is filtered to its own
// (instance, target) set from the page-wide candidate app set.
func TestClusterList_BatchesArgoCDEnrichment(t *testing.T) {
	base := newFakeAutoAttachClusterQuerier()
	c1 := sqlc.Cluster{ID: uuid.New(), Name: "c1"}
	c2 := sqlc.Cluster{ID: uuid.New(), Name: "c2"}
	instID := uuid.New()

	q := &batchArgoClusterQuerier{
		fakeAutoAttachClusterQuerier: base,
		clustersList:                 []sqlc.Cluster{c1, c2},
		managed: []sqlc.ArgocdManagedCluster{
			{ID: uuid.New(), ClusterID: c1.ID, ArgocdInstanceID: instID, ServerUrl: "https://c1", ClusterSecretName: "c1-secret"},
		},
		apps: []sqlc.ArgocdApplication{
			{ID: uuid.New(), ArgocdInstanceID: instID, DestinationCluster: "c1", SyncStatus: "OutOfSync", HealthStatus: "Healthy"},
			{ID: uuid.New(), ArgocdInstanceID: instID, DestinationCluster: "c2", SyncStatus: "Synced", HealthStatus: "Healthy"},
		},
	}
	h := NewClusterHandler(q)

	req := newRouterCtxReq(http.MethodGet, "/api/v1/clusters/?limit=50", nil, nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if q.batchCalls != 1 {
		t.Errorf("expected exactly 1 batched managed-cluster query, got %d", q.batchCalls)
	}
	if q.perClusterCalls != 0 {
		t.Errorf("expected 0 per-cluster managed-cluster queries, got %d", q.perClusterCalls)
	}
	if q.appsCalls != 1 {
		t.Errorf("expected exactly 1 batched apps query, got %d", q.appsCalls)
	}

	var resp struct {
		Data []ClusterResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]ClusterResponse{}
	for _, c := range resp.Data {
		byName[c.Name] = c
	}
	if got := byName["c1"]; !got.ArgoCD.Registered {
		t.Errorf("c1 should be registered")
	}
	// c1's drift must include only the app targeting "c1" (not c2's app), even
	// though both came back in the single page-wide apps query.
	if got := byName["c1"].ArgoCD.Drift; got.AppCount != 1 || got.OutOfSyncCount != 1 {
		t.Errorf("c1 drift wrong (should filter to its own targets): %+v", got)
	}
	if got := byName["c2"]; got.ArgoCD.Registered {
		t.Errorf("c2 has no managed rows and must not be registered")
	}
}

// tokenMintingClusterQuerier reuses the full ClusterQuerier fake and adds the
// API-token minter surface used by the kubeconfig download.
type tokenMintingClusterQuerier struct {
	*fakeAutoAttachClusterQuerier
	created []sqlc.CreateAPITokenParams
}

func (q *tokenMintingClusterQuerier) CreateAPIToken(_ context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error) {
	q.created = append(q.created, arg)
	return sqlc.ApiToken{ID: uuid.New(), UserID: arg.UserID, Name: arg.Name, TokenHash: arg.TokenHash, Prefix: arg.Prefix}, nil
}

// The downloaded kubeconfig must carry a real, minted, caller-scoped token —
// not the REPLACE_WITH_API_TOKEN placeholder that made one-click download
// non-functional.
func TestGenerateKubeconfig_EmbedsMintedToken(t *testing.T) {
	base := newFakeAutoAttachClusterQuerier()
	clusterID := uuid.New()
	base.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod", ApiServerUrl: "https://prod.example"}
	base.config = sqlc.PlatformConfiguration{ServerUrl: "https://astronomer.example.com"}

	q := &tokenMintingClusterQuerier{fakeAutoAttachClusterQuerier: base}
	h := NewClusterHandler(q)

	userID := uuid.New()
	req := newRouterCtxReq(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/generate-kubeconfig/", nil, map[string]string{"id": clusterID.String()})
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID.String(), Email: "op@example.com"}))
	rec := httptest.NewRecorder()

	h.GenerateKubeconfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "REPLACE_WITH_API_TOKEN") {
		t.Fatalf("kubeconfig still ships the placeholder token: %s", body)
	}
	if !strings.Contains(body, "astro_") {
		t.Errorf("expected a minted astro_ token embedded, got: %s", body)
	}
	if len(q.created) != 1 {
		t.Fatalf("expected exactly 1 token minted, got %d", len(q.created))
	}
	tok := q.created[0]
	if tok.UserID != userID {
		t.Errorf("token not scoped to the calling user: got %s want %s", tok.UserID, userID)
	}
	if !tok.ExpiresAt.Valid {
		t.Errorf("kubeconfig token must be short-lived (expiry set), got no expiry")
	}
}
