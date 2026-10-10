package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

func TestLegacySQLPaginationParserCharacterization(t *testing.T) {
	for _, tc := range []struct {
		name, value            string
		ordinary, wide, offset int
	}{
		{"absent-or-empty", "", 20, 100, 0},
		{"normal", "42", 42, 42, 42},
		{"zero", "0", 20, 100, 0},
		{"negative", "-1", 20, 100, 0},
		{"negative-overflow", "-99999999999999999999999999", 20, 100, 0},
		{"malformed", "invalid", 20, 100, 0},
		{"signed-positive", "+42", 42, 42, 0},
		{"int32-overflow", "2147483648", 200, 500, 10000},
		{"int64-max", "9223372036854775807", 200, 500, 10000},
		{"int64-overflow", "9223372036854775808", 20, 100, 10000},
		{"uint64-overflow", "18446744073709551616", 20, 100, 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/?"+url.Values{"limit": {tc.value}, "offset": {tc.value}}.Encode(), nil)
			if got := queryLimit(r, 20); got != tc.ordinary {
				t.Fatalf("ordinary limit = %d, want %d", got, tc.ordinary)
			}
			if got := queryLimitMax(r, 100, 500); got != tc.wide {
				t.Fatalf("wide limit = %d, want %d", got, tc.wide)
			}
			if got := queryOffset(r); got != tc.offset {
				t.Fatalf("offset = %d, want %d", got, tc.offset)
			}
		})
	}
}

func TestLegacySQLPaginationDefaultsCharacterization(t *testing.T) {
	for _, tc := range []struct {
		name, query             string
		fallback, ceiling, want int
	}{
		{"missing-default", "", 20, 200, 20},
		{"zero-default", "", 0, 200, 1},
		{"negative-default", "", -1, 200, 1},
		{"oversized-default", "", 500, 200, 200},
		{"int32-max-default", "", 2147483647, 500, 500},
		{"zero-ceiling", "limit=42", 20, 0, 1},
		{"negative-ceiling", "limit=42", 20, -1, 1},
		{"int32-max-ceiling", "limit=9223372036854775807", 20, 2147483647, 2147483647},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			if got := queryLimitMax(r, tc.fallback, tc.ceiling); got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
		})
	}
}

type sqlWindowToolRecorder struct {
	*toolQueryRecorder
	window sqlc.ListClusterToolsParams
	calls  int
}

func (q *sqlWindowToolRecorder) ListClusterTools(_ context.Context, window sqlc.ListClusterToolsParams) ([]sqlc.ClusterTool, error) {
	q.window = window
	q.calls++
	return nil, nil
}

func TestToolSQLWindowMatchesWireMetadata(t *testing.T) {
	for _, query := range []string{"", "limit=42&offset=42", "limit=0&offset=-1", "limit=invalid&offset=invalid", "limit=2147483648&offset=10000", "limit=9223372036854775807", "limit=9223372036854775808", "limit=20&offset=-99999999999999999999999"} {
		t.Run(query, func(t *testing.T) {
			q := &sqlWindowToolRecorder{toolQueryRecorder: &toolQueryRecorder{}}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/tools/?"+query, nil)
			w := httptest.NewRecorder()
			NewToolHandler(q).List(w, r)
			var body paging.Response[ToolResponse]
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || q.calls != 1 || body.Data == nil {
				t.Fatalf("status=%d calls=%d data=%v", w.Code, q.calls, body.Data)
			}
			if int(q.window.Limit) != body.Pagination.Limit || int(q.window.Offset) != body.Pagination.Offset {
				t.Fatalf("SQL window %+v differs from wire metadata %+v", q.window, body.Pagination)
			}
			if body.Pagination.Limit != queryLimit(r, 20) || body.Pagination.Offset != queryOffset(r) {
				t.Fatalf("wire metadata differs from legacy parser: %+v", body.Pagination)
			}
		})
	}
}
