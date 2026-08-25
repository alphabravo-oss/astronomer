package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type securityScopeBindings struct{ bindings []rbac.RoleBinding }

func (q securityScopeBindings) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return q.bindings, nil
}

type securityScopeQuerier struct {
	SecurityQuerier
	scans          []sqlc.SecurityScanResult
	listScopeCalls int
	lastClusterIDs []uuid.UUID
	tombstoned     map[uuid.UUID]bool
}

func (q *securityScopeQuerier) activeVisible(clusterIDs []uuid.UUID) []sqlc.SecurityScanResult {
	allowed := make(map[uuid.UUID]bool, len(clusterIDs))
	for _, id := range clusterIDs {
		allowed[id] = true
	}
	rows := make([]sqlc.SecurityScanResult, 0, len(q.scans))
	for _, scan := range q.scans {
		if allowed[scan.ClusterID] && !q.tombstoned[scan.ClusterID] {
			rows = append(rows, scan)
		}
	}
	return rows
}

func (q *securityScopeQuerier) ListSecurityScanResultsForScopes(_ context.Context, arg sqlc.ListSecurityScanResultsForScopesParams) ([]sqlc.SecurityScanResult, error) {
	q.listScopeCalls++
	q.lastClusterIDs = append([]uuid.UUID(nil), arg.ClusterIds...)
	rows := q.activeVisible(arg.ClusterIds)
	start := int(arg.QueryOffset)
	if start >= len(rows) {
		return []sqlc.SecurityScanResult{}, nil
	}
	end := start + int(arg.QueryLimit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], nil
}

func (q *securityScopeQuerier) CountSecurityScanResultsForScopes(_ context.Context, clusterIDs []uuid.UUID) (int64, error) {
	return int64(len(q.activeVisible(clusterIDs))), nil
}

func (q *securityScopeQuerier) GetActiveSecurityScanResultByID(_ context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error) {
	for _, scan := range q.scans {
		if scan.ID == id && !q.tombstoned[scan.ClusterID] {
			return scan, nil
		}
	}
	return sqlc.SecurityScanResult{}, pgx.ErrNoRows
}

func (q *securityScopeQuerier) GetActiveSecurityScanResultByIDForScopes(_ context.Context, arg sqlc.GetActiveSecurityScanResultByIDForScopesParams) (sqlc.SecurityScanResult, error) {
	for _, scan := range q.activeVisible(arg.ClusterIds) {
		if scan.ID == arg.ID {
			return scan, nil
		}
	}
	return sqlc.SecurityScanResult{}, pgx.ErrNoRows
}

func (q *securityScopeQuerier) GetSecurityScanResultByClusterAndID(_ context.Context, arg sqlc.GetSecurityScanResultByClusterAndIDParams) (sqlc.SecurityScanResult, error) {
	for _, scan := range q.scans {
		if scan.ID == arg.ID && scan.ClusterID == arg.ClusterID && !q.tombstoned[scan.ClusterID] {
			return scan, nil
		}
	}
	return sqlc.SecurityScanResult{}, pgx.ErrNoRows
}

func (q *securityScopeQuerier) CountSecurityScanResultsByCluster(_ context.Context, clusterID uuid.UUID) (int64, error) {
	return int64(len(q.activeVisible([]uuid.UUID{clusterID}))), nil
}

func authenticatedSecurityRequest(method, target string, userID uuid.UUID) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	ctx := middleware.SetAuthenticatedUserForTest(request.Context(), &middleware.AuthenticatedUser{ID: userID.String(), AuthMethod: "jwt"})
	return request.WithContext(ctx)
}

func TestSecurityEstatePaginationFiltersBeforeLimitAndCount(t *testing.T) {
	clusterA, clusterB := uuid.New(), uuid.New()
	queries := &securityScopeQuerier{scans: []sqlc.SecurityScanResult{
		{ID: uuid.New(), ClusterID: clusterB, ScanType: "unauthorized-first"},
		{ID: uuid.New(), ClusterID: clusterB, ScanType: "unauthorized-second"},
		{ID: uuid.New(), ClusterID: clusterA, ScanType: "visible-first"},
		{ID: uuid.New(), ClusterID: clusterA, ScanType: "visible-second"},
	}}
	bindings := []rbac.RoleBinding{{
		Scope: "cluster", ClusterID: clusterA.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
	}}
	h := NewSecurityHandler(queries)
	h.SetAuthorization(rbac.NewEngine(), securityScopeBindings{bindings: bindings})
	request := authenticatedSecurityRequest(http.MethodGet, "/api/v1/security/scans/?limit=1&offset=0", uuid.New())
	response := httptest.NewRecorder()

	h.ListAllScans(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data  []sqlc.SecurityScanResult `json:"data"`
		Count int64                     `json:"count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0].ClusterID != clusterA {
		t.Fatalf("page = %+v, want first authorized row on %s", envelope.Data, clusterA)
	}
	if envelope.Count != 2 {
		t.Fatalf("count = %d, want authorized total 2", envelope.Count)
	}
	if queries.listScopeCalls != 1 || len(queries.lastClusterIDs) != 1 || queries.lastClusterIDs[0] != clusterA {
		t.Fatalf("scoped query calls/ids = %d/%v", queries.listScopeCalls, queries.lastClusterIDs)
	}
}

func TestSecurityScanObjectReadsRejectWrongClusterAndTombstones(t *testing.T) {
	clusterA, clusterB := uuid.New(), uuid.New()
	scan := sqlc.SecurityScanResult{ID: uuid.New(), ClusterID: clusterA, ScanType: "cis"}
	queries := &securityScopeQuerier{scans: []sqlc.SecurityScanResult{scan}, tombstoned: map[uuid.UUID]bool{}}
	h := NewSecurityHandler(queries)

	for _, tc := range []struct {
		name      string
		clusterID uuid.UUID
		tombstone bool
		want      int
	}{
		{name: "matching cluster", clusterID: clusterA, want: http.StatusOK},
		{name: "route and scan cluster disagree", clusterID: clusterB, want: http.StatusNotFound},
		{name: "tombstoned cluster", clusterID: clusterA, tombstone: true, want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries.tombstoned[clusterA] = tc.tombstone
			request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+tc.clusterID.String()+"/security/scans/"+scan.ID.String()+"/", nil)
			routeContext := chi.NewRouteContext()
			routeContext.URLParams.Add("cluster_id", tc.clusterID.String())
			routeContext.URLParams.Add("id", scan.ID.String())
			request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
			response := httptest.NewRecorder()
			h.GetScan(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

func TestSecurityEstateObjectReadIntersectsVisibleClusters(t *testing.T) {
	clusterA, clusterB := uuid.New(), uuid.New()
	scan := sqlc.SecurityScanResult{ID: uuid.New(), ClusterID: clusterB, ScanType: "cis"}
	queries := &securityScopeQuerier{scans: []sqlc.SecurityScanResult{scan}, tombstoned: map[uuid.UUID]bool{}}
	bindings := []rbac.RoleBinding{{
		Scope: "cluster", ClusterID: clusterA.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
	}}
	h := NewSecurityHandler(queries)
	h.SetAuthorization(rbac.NewEngine(), securityScopeBindings{bindings: bindings})
	request := authenticatedSecurityRequest(http.MethodGet, "/api/v1/security/scans/"+scan.ID.String()+"/", uuid.New())
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", scan.ID.String())
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	response := httptest.NewRecorder()

	h.GetScanFull(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", response.Code, response.Body.String())
	}
}
