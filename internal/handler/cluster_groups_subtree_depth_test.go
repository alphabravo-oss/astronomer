package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Reparenting a group must account for the depth its *descendants* carry, not
// just the moved node's own new depth. With MaxClusterGroupDepth=2, moving
// B(0)->C(1)->D(2) under another top-level group A(0) makes B depth 1 — which
// on its own passes — but pushes D to depth 3, breaking the cap. The DB has no
// CHECK constraint, so the over-deep rows would silently persist. Update must
// reject the move.
func TestUpdateGroup_RejectsReparentThatOverflowsDescendants(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	// A separate top-level group to reparent under.
	a := createGroup(t, h, map[string]any{"name": "a"})
	// B -> C -> D chain (depths 0,1,2 — at the cap).
	b := createGroup(t, h, map[string]any{"name": "b"})
	c := createGroup(t, h, map[string]any{"name": "c", "parent_id": b.ID})
	createGroup(t, h, map[string]any{"name": "d", "parent_id": c.ID})

	// Move B under A: newDepth(B)=1 (passes the own-depth check) but D would
	// land at depth 3.
	raw, _ := json.Marshal(map[string]any{"name": "b", "slug": "b", "parent_id": a.ID})
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPut, "/api/v1/cluster-groups/"+b.ID+"/", raw, map[string]string{"id": b.ID})
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for subtree overflow, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "max_depth") {
		t.Errorf("expected max_depth error code, got: %s", rec.Body.String())
	}
}

// A reparent that keeps the whole subtree within the cap must still succeed:
// moving a leaf C(1) (no descendants) under another top-level group A(0) leaves
// C at depth 1 with height 0 — well within the cap.
func TestUpdateGroup_AllowsReparentWithinCap(t *testing.T) {
	q := newFakeClusterGroupQuerier()
	h := NewClusterGroupHandler(q)

	a := createGroup(t, h, map[string]any{"name": "a"})
	b := createGroup(t, h, map[string]any{"name": "b"})
	c := createGroup(t, h, map[string]any{"name": "c", "parent_id": b.ID})

	// Move leaf C under A — C stays depth 1, no descendants.
	raw, _ := json.Marshal(map[string]any{"name": "c", "slug": "c", "parent_id": a.ID})
	rec := httptest.NewRecorder()
	req := newRouterCtxReq(http.MethodPut, "/api/v1/cluster-groups/"+c.ID+"/", raw, map[string]string{"id": c.ID})
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for in-cap reparent, got %d body=%s", rec.Code, rec.Body.String())
	}
}
