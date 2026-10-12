package charlie

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type sqlPageQueries struct {
	clusterAgentQueriesFake
	calls int
}

func (q *sqlPageQueries) CharlieClusterAgentList(ctx context.Context, params sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error) {
	q.calls++
	return q.clusterAgentQueriesFake.CharlieClusterAgentList(ctx, params)
}

func TestCapabilitySQLPagePreservesValidatedQueriesAndMetadata(t *testing.T) {
	descriptor, exists := capabilityByName("astronomer.cluster_agents.list")
	if !exists {
		t.Fatal("canonical list descriptor unavailable")
	}
	for _, tt := range []struct {
		name       string
		arguments  map[string]any
		page, size int64
	}{
		{"default", map[string]any{}, 1, 50},
		{"first", map[string]any{"page": 1, "page_size": 1}, 1, 1},
		{"middle", map[string]any{"page": 3, "page_size": 20}, 3, 20},
		{"maximum", map[string]any{"page": 10000, "page_size": 100}, 10000, 100},
		{"default-size", map[string]any{"page": 10}, 10, 50},
		{"default-page", map[string]any{"page_size": 25}, 1, 25},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := rawArguments(t, tt.arguments)
			if err := validateCapabilityArguments(descriptor, args); err != nil {
				t.Fatal("fixture violates canonical schema", err)
			}
			q := &sqlPageQueries{}
			adapter, err := NewClusterAgentCapabilityAdapter(q)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(context.Background(), descriptor, args)
			if err != nil {
				t.Fatal(err)
			}
			if q.calls != 1 || int64(q.listParams.PageLimit) != tt.size || int64(q.listParams.PageOffset) != (tt.page-1)*tt.size {
				t.Fatal("validated query pagination changed")
			}
			var metadata struct {
				Page int64 `json:"page"`
				Size int64 `json:"page_size"`
			}
			if err := json.Unmarshal(result, &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Page != tt.page || metadata.Size != tt.size {
				t.Fatal("public pagination metadata changed")
			}
		})
	}
}

func TestCapabilityPaginationSchemaRejectsUnsupportedNumericBounds(t *testing.T) {
	descriptor, exists := capabilityByName("astronomer.cluster_agents.list")
	if !exists {
		t.Fatal("canonical list descriptor unavailable")
	}
	for _, tt := range []struct {
		name, field string
		value       int64
	}{
		{"zero-page", "page", 0}, {"negative-page", "page", -1}, {"oversized-page", "page", 10001}, {"overflow-page", "page", math.MaxInt64},
		{"zero-size", "page_size", 0}, {"negative-size", "page_size", -1}, {"oversized-size", "page_size", 101}, {"overflow-size", "page_size", math.MaxInt64},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if validateCapabilityArguments(descriptor, rawArguments(t, map[string]any{tt.field: tt.value})) == nil {
				t.Fatal("canonical schema accepted unsupported pagination")
			}
		})
	}
}
