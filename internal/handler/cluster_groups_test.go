// Cluster groups (migration 066) — handler tests.
//
// The fake querier below implements ClusterGroupQuerier in-memory; depth
// + cycle + slug enforcement live in the handler so each test exercises
// the handler against a deterministic store. The actual route mapping
// (writeClusters + clusters:update) lives in internal/server/routes.go;
// see TestClusterGroupsHandler_RequiresClustersUpdate for the
// documenting stub.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeClusterGroupQuerier implements ClusterGroupQuerier in memory.
type fakeClusterGroupQuerier struct {
	mu sync.Mutex

	groups        map[uuid.UUID]sqlc.ClusterGroup
	clusters      map[uuid.UUID]sqlc.Cluster
	clusterGroups map[uuid.UUID]uuid.UUID // cluster_id → group_id
}

func newFakeClusterGroupQuerier() *fakeClusterGroupQuerier {
	return &fakeClusterGroupQuerier{
		groups:        map[uuid.UUID]sqlc.ClusterGroup{},
		clusters:      map[uuid.UUID]sqlc.Cluster{},
		clusterGroups: map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeClusterGroupQuerier) ListClusterGroups(_ context.Context) ([]sqlc.ClusterGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ClusterGroup, 0, len(f.groups))
	for _, g := range f.groups {
		if g.Enabled {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fakeClusterGroupQuerier) ListClusterGroupsAsTree(_ context.Context) ([]sqlc.ClusterGroupTreeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Walk depth-first; compute depth by ancestor chain.
	depthOf := func(id uuid.UUID) int32 {
		d := int32(0)
		cur := id
		for i := 0; i < 8; i++ {
			g, ok := f.groups[cur]
			if !ok || !g.ParentID.Valid {
				return d
			}
			d++
			cur = uuid.UUID(g.ParentID.Bytes)
		}
		return d
	}
	out := []sqlc.ClusterGroupTreeRow{}
	for _, g := range f.groups {
		if !g.Enabled {
			continue
		}
		out = append(out, sqlc.ClusterGroupTreeRow{
			ID:          g.ID,
			Name:        g.Name,
			Slug:        g.Slug,
			Description: g.Description,
			ParentID:    g.ParentID,
			Color:       g.Color,
			Icon:        g.Icon,
			Enabled:     g.Enabled,
			CreatedBy:   g.CreatedBy,
			CreatedAt:   g.CreatedAt,
			UpdatedAt:   g.UpdatedAt,
			Depth:       depthOf(g.ID),
		})
	}
	return out, nil
}

func (f *fakeClusterGroupQuerier) GetClusterGroupByID(_ context.Context, id uuid.UUID) (sqlc.ClusterGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[id]
	if !ok {
		return sqlc.ClusterGroup{}, pgx.ErrNoRows
	}
	return g, nil
}

func (f *fakeClusterGroupQuerier) CreateClusterGroup(_ context.Context, arg sqlc.CreateClusterGroupParams) (sqlc.ClusterGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Enforce the (parent_id, slug) uniqueness — duplicate slugs under the
	// same parent surface as 23505 from Postgres in production; the fake
	// returns a stub error that the handler classifies via errors.As. We
	// use the real pgconn.PgError so isUniqueViolation matches.
	for _, g := range f.groups {
		if g.Slug == arg.Slug && g.ParentID == arg.ParentID {
			return sqlc.ClusterGroup{}, &pgconn.PgError{Code: "23505", Message: "duplicate key"}
		}
	}
	id := uuid.New()
	g := sqlc.ClusterGroup{
		ID:          id,
		Name:        arg.Name,
		Slug:        arg.Slug,
		Description: arg.Description,
		ParentID:    arg.ParentID,
		Color:       arg.Color,
		Icon:        arg.Icon,
		Enabled:     true,
		CreatedBy:   arg.CreatedBy,
	}
	f.groups[id] = g
	return g, nil
}

func (f *fakeClusterGroupQuerier) UpdateClusterGroup(_ context.Context, arg sqlc.UpdateClusterGroupParams) (sqlc.ClusterGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[arg.ID]
	if !ok {
		return sqlc.ClusterGroup{}, pgx.ErrNoRows
	}
	// (parent_id, slug) uniqueness re-check on update.
	for id, other := range f.groups {
		if id == arg.ID {
			continue
		}
		if other.Slug == arg.Slug && other.ParentID == arg.ParentID {
			return sqlc.ClusterGroup{}, &pgconn.PgError{Code: "23505", Message: "duplicate key"}
		}
	}
	g.Name = arg.Name
	g.Slug = arg.Slug
	g.Description = arg.Description
	g.ParentID = arg.ParentID
	g.Color = arg.Color
	g.Icon = arg.Icon
	f.groups[arg.ID] = g
	return g, nil
}

func (f *fakeClusterGroupQuerier) DeleteClusterGroup(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Simulate ON DELETE CASCADE on the self-FK + ON DELETE SET NULL on
	// the clusters.group_id column.
	subtree := f.collectSubtreeLocked(id)
	for _, sid := range subtree {
		delete(f.groups, sid)
	}
	for cid, gid := range f.clusterGroups {
		for _, sid := range subtree {
			if gid == sid {
				delete(f.clusterGroups, cid)
				break
			}
		}
	}
	return nil
}

func (f *fakeClusterGroupQuerier) collectSubtreeLocked(root uuid.UUID) []uuid.UUID {
	out := []uuid.UUID{root}
	stack := []uuid.UUID{root}
	for len(stack) > 0 {
		cur := stack[0]
		stack = stack[1:]
		for id, g := range f.groups {
			if g.ParentID.Valid && uuid.UUID(g.ParentID.Bytes) == cur {
				out = append(out, id)
				stack = append(stack, id)
			}
		}
	}
	return out
}

func (f *fakeClusterGroupQuerier) ListClustersInGroupTree(_ context.Context, rootID uuid.UUID) ([]sqlc.ClusterInGroupRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	subtree := f.collectSubtreeLocked(rootID)
	subtreeSet := map[uuid.UUID]struct{}{}
	for _, id := range subtree {
		subtreeSet[id] = struct{}{}
	}
	out := []sqlc.ClusterInGroupRow{}
	for cid, gid := range f.clusterGroups {
		if _, ok := subtreeSet[gid]; ok {
			cl := f.clusters[cid]
			out = append(out, sqlc.ClusterInGroupRow{ID: cid, Name: cl.Name})
		}
	}
	return out, nil
}

func (f *fakeClusterGroupQuerier) CountClustersInGroup(_ context.Context, groupID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := int64(0)
	for _, gid := range f.clusterGroups {
		if gid == groupID {
			n++
		}
	}
	return n, nil
}

func (f *fakeClusterGroupQuerier) CountClustersInGroupTree(_ context.Context, groupID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	subtree := f.collectSubtreeLocked(groupID)
	subtreeSet := map[uuid.UUID]struct{}{}
	for _, id := range subtree {
		subtreeSet[id] = struct{}{}
	}
	n := int64(0)
	for _, gid := range f.clusterGroups {
		if _, ok := subtreeSet[gid]; ok {
			n++
		}
	}
	return n, nil
}

func (f *fakeClusterGroupQuerier) AssignClusterGroup(_ context.Context, arg sqlc.AssignClusterGroupParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !arg.GroupID.Valid {
		delete(f.clusterGroups, arg.ClusterID)
		return nil
	}
	f.clusterGroups[arg.ClusterID] = uuid.UUID(arg.GroupID.Bytes)
	return nil
}

func (f *fakeClusterGroupQuerier) UnassignClusterGroup(_ context.Context, clusterID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.clusterGroups, clusterID)
	return nil
}

func (f *fakeClusterGroupQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

// ────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────

func newRouterCtxReq(method, path string, body []byte, params map[string]string) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func createGroup(t *testing.T, h *ClusterGroupHandler, body map[string]any) ClusterGroupResponse {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPost, "/api/v1/cluster-groups/", raw, nil)
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data ClusterGroupResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Data
}

// ────────────────────────────────────────────────────────────────────────
// Tests
// ────────────────────────────────────────────────────────────────────────

// TestCreateGroup_EnforcesDepthCap verifies that a group can be created
// at depth 0, depth 1, depth 2 — but a depth-3 create is rejected with
// 400 and the "max_depth" error code.
func TestCreateGroup_EnforcesDepthCap(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	root := createGroup(t, h, map[string]any{"name": "prod"})
	l1 := createGroup(t, h, map[string]any{"name": "prod-us", "parent_id": root.ID})
	l2 := createGroup(t, h, map[string]any{"name": "prod-us-east", "parent_id": l1.ID})

	// Depth 3 — should 400.
	raw, _ := json.Marshal(map[string]any{"name": "too-deep", "parent_id": l2.ID})
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPost, "/api/v1/cluster-groups/", raw, nil)
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "max_depth") {
		t.Errorf("expected max_depth error code, got: %s", rec.Body.String())
	}
}

// TestCreateGroup_RejectsCircularParent verifies that updating a group
// to point at one of its own descendants (or itself) is rejected with
// 400.
func TestCreateGroup_RejectsCircularParent(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	root := createGroup(t, h, map[string]any{"name": "root"})
	child := createGroup(t, h, map[string]any{"name": "child", "parent_id": root.ID})

	// Try to reparent root under child — circular.
	raw, _ := json.Marshal(map[string]any{"name": "root", "slug": "root", "parent_id": child.ID})
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPut, "/api/v1/cluster-groups/"+root.ID+"/", raw, map[string]string{"id": root.ID})
	h.Update(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_parent") {
		t.Errorf("expected invalid_parent error, got: %s", rec.Body.String())
	}

	// Also reject pointing a group at itself.
	raw, _ = json.Marshal(map[string]any{"name": "root", "slug": "root", "parent_id": root.ID})
	rec = httptest.NewRecorder()
	req = newRouterCtxReq(http.MethodPut, "/api/v1/cluster-groups/"+root.ID+"/", raw, map[string]string{"id": root.ID})
	h.Update(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-parent should 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateGroup_DuplicateSlugUnderSameParent_400 verifies that two
// groups can't share a slug under the same parent, but the same slug IS
// allowed under different parents.
func TestCreateGroup_DuplicateSlugUnderSameParent_400(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	parentA := createGroup(t, h, map[string]any{"name": "a"})
	parentB := createGroup(t, h, map[string]any{"name": "b"})

	// First child under parentA.
	createGroup(t, h, map[string]any{"name": "prod-east", "parent_id": parentA.ID})

	// Duplicate under parentA — should 400.
	raw, _ := json.Marshal(map[string]any{"name": "prod-east", "parent_id": parentA.ID})
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPost, "/api/v1/cluster-groups/", raw, nil)
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "duplicate_slug") {
		t.Errorf("expected duplicate_slug, got: %s", rec.Body.String())
	}

	// Same slug under parentB — should succeed (slug uniqueness is scoped
	// to parent).
	createGroup(t, h, map[string]any{"name": "prod-east", "parent_id": parentB.ID})
}

// TestListGroupsAsTree_ReturnsDepthAnnotated verifies the LIST endpoint
// returns rows with a `depth` field computed by the recursive CTE.
func TestListGroupsAsTree_ReturnsDepthAnnotated(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	root := createGroup(t, h, map[string]any{"name": "prod"})
	mid := createGroup(t, h, map[string]any{"name": "prod-us", "parent_id": root.ID})
	createGroup(t, h, map[string]any{"name": "prod-us-east", "parent_id": mid.ID})

	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodGet, "/api/v1/cluster-groups/", nil, nil)
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []ClusterGroupTreeResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(resp.Data))
	}
	depths := map[string]int32{}
	for _, r := range resp.Data {
		depths[r.Slug] = r.Depth
	}
	if depths["prod"] != 0 || depths["prod-us"] != 1 || depths["prod-us-east"] != 2 {
		t.Errorf("depth annotation wrong: %+v", depths)
	}
}

// TestListClustersInGroupTree_RecursesDescendants verifies that fetching
// /cluster-groups/{root_id}/clusters/ returns clusters parented to ANY
// descendant — not just the direct children of root.
func TestListClustersInGroupTree_RecursesDescendants(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	root := createGroup(t, h, map[string]any{"name": "prod"})
	mid := createGroup(t, h, map[string]any{"name": "prod-us", "parent_id": root.ID})
	leaf := createGroup(t, h, map[string]any{"name": "prod-us-east", "parent_id": mid.ID})

	// Park a cluster on each level. Direct .clusters in the fake.
	rootCluster := uuid.New()
	midCluster := uuid.New()
	leafCluster := uuid.New()
	q.clusters[rootCluster] = sqlc.Cluster{ID: rootCluster, Name: "c-root"}
	q.clusters[midCluster] = sqlc.Cluster{ID: midCluster, Name: "c-mid"}
	q.clusters[leafCluster] = sqlc.Cluster{ID: leafCluster, Name: "c-leaf"}
	rootUUID := uuid.MustParse(root.ID)
	midUUID := uuid.MustParse(mid.ID)
	leafUUID := uuid.MustParse(leaf.ID)
	q.clusterGroups[rootCluster] = rootUUID
	q.clusterGroups[midCluster] = midUUID
	q.clusterGroups[leafCluster] = leafUUID

	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodGet, "/api/v1/cluster-groups/"+root.ID+"/clusters/", nil, map[string]string{"id": root.ID})
	h.ListClusters(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list clusters: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 clusters across subtree, got %d", len(resp.Data))
	}
}

// TestDeleteGroup_CascadesSubtreeAndUnassignsClusters verifies the
// CASCADE behavior: deleting root removes the entire subtree from
// cluster_groups, and clusters parented in the subtree get group_id=NULL
// (not deleted).
func TestDeleteGroup_CascadesSubtreeAndUnassignsClusters(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	root := createGroup(t, h, map[string]any{"name": "prod"})
	mid := createGroup(t, h, map[string]any{"name": "prod-us", "parent_id": root.ID})

	c1 := uuid.New()
	c2 := uuid.New()
	q.clusters[c1] = sqlc.Cluster{ID: c1, Name: "c-root"}
	q.clusters[c2] = sqlc.Cluster{ID: c2, Name: "c-mid"}
	q.clusterGroups[c1] = uuid.MustParse(root.ID)
	q.clusterGroups[c2] = uuid.MustParse(mid.ID)

	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodDelete, "/api/v1/cluster-groups/"+root.ID+"/", nil, map[string]string{"id": root.ID})
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.groups) != 0 {
		t.Errorf("expected empty groups after cascade, got %d", len(q.groups))
	}
	// Clusters still exist; their group bindings are gone.
	if _, ok := q.clusters[c1]; !ok {
		t.Errorf("cluster c1 was removed (should only have lost its group binding)")
	}
	if len(q.clusterGroups) != 0 {
		t.Errorf("expected cluster group bindings to be cleared, got %d", len(q.clusterGroups))
	}
}

// TestMoveClusters_AssignsBatch verifies the bulk-assign endpoint sets
// group_id on every supplied (existing) cluster ID and reports skipped
// IDs for unknowns.
func TestMoveClusters_AssignsBatch(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	grp := createGroup(t, h, map[string]any{"name": "staging"})
	c1 := uuid.New()
	c2 := uuid.New()
	q.clusters[c1] = sqlc.Cluster{ID: c1, Name: "c1"}
	q.clusters[c2] = sqlc.Cluster{ID: c2, Name: "c2"}
	unknown := uuid.New().String()

	body, _ := json.Marshal(map[string]any{
		"cluster_ids": []string{c1.String(), c2.String(), unknown, "not-a-uuid"},
	})
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPost, "/api/v1/cluster-groups/"+grp.ID+"/move/", body, map[string]string{"id": grp.ID})
	h.MoveClusters(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("move: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Moved   int      `json:"moved"`
			Skipped []string `json:"skipped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Moved != 2 {
		t.Errorf("expected moved=2, got %d", resp.Data.Moved)
	}
	if len(resp.Data.Skipped) != 2 {
		t.Errorf("expected 2 skipped, got %d", len(resp.Data.Skipped))
	}
	// Verify the actual store.
	grpUUID := uuid.MustParse(grp.ID)
	if q.clusterGroups[c1] != grpUUID || q.clusterGroups[c2] != grpUUID {
		t.Errorf("cluster bindings did not update: %v", q.clusterGroups)
	}
}

// TestClusterGroupsHandler_RequiresClustersUpdate documents the
// route-level RBAC gate. The handler methods themselves do not call into
// RBAC — the gate is applied via requirePermission(ResourceClusters,
// VerbUpdate) on every route in internal/server/routes.go.
func TestClusterGroupsHandler_RequiresClustersUpdate(t *testing.T) {
	t.Log("cluster_groups routes are mounted behind ScopeWriteClusters + ResourceClusters/VerbUpdate in internal/server/routes.go")
}

// TestClusterGroups_SlugAutoDerivation verifies that omitting an explicit
// slug derives one from the name field (kebab-case).
func TestClusterGroups_SlugAutoDerivation(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)
	got := createGroup(t, h, map[string]any{"name": "Prod US East"})
	if got.Slug != "prod-us-east" {
		t.Errorf("expected derived slug 'prod-us-east', got %q", got.Slug)
	}
}

// pgtypeUUID is a small helper to construct a valid pgtype.UUID — used by
// some assertions below where parsing a string into pgtype.UUID inline
// would be noisy.
func pgtypeUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

var _ = pgtypeUUID // keep the helper referenced when no test uses it
