package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestNewPaginationEmitsOnlyExactTotals(t *testing.T) {
	exact := NewPagination(11, 5, 5, 5)
	if exact.Total == nil || *exact.Total != 11 {
		t.Fatalf("exact total = %v, want 11", exact.Total)
	}
	if !exact.HasMore || exact.NextOffset == nil || *exact.NextOffset != 10 {
		t.Fatalf("exact next page metadata = %+v", exact)
	}

	unknown := NewPaginationFromPage(5, 0, 5)
	if unknown.Total != nil {
		t.Fatalf("unknown total = %v, want omitted", *unknown.Total)
	}

	recorder := httptest.NewRecorder()
	RespondList(recorder, []string{"one"}, unknown)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(body["pagination"], &metadata); err != nil {
		t.Fatal(err)
	}
	if _, exists := metadata["total"]; exists {
		t.Fatal("unknown total must not be serialized")
	}
}

func TestNewPaginationLastPageHasNoNextOffset(t *testing.T) {
	pagination := NewPagination(7, 5, 5, 2)
	if pagination.HasMore || pagination.NextOffset != nil {
		t.Fatalf("last page metadata = %+v", pagination)
	}
}
