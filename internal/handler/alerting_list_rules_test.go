package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListRulesInvalidClusterIDMatchesNothing(t *testing.T) {
	h := NewAlertingHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alerting/rules/?clusterId=not-a-uuid", nil)
	h.ListRules(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Data       []any `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Pagination.Total != 0 || len(body.Data) != 0 {
		t.Fatalf("unparseable clusterId should match nothing, got total=%d data=%d", body.Pagination.Total, len(body.Data))
	}
}
