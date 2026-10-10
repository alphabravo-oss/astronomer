package charlie

import (
	"context"
	"math"
	"testing"
)

// Direct adapter invocation is an internal composition boundary, distinct from
// the already-bounded signed ActionGuard input. It must not narrow unchecked.
func TestCapabilitySQLPageRejectsInvalidDirectAdapterArgumentsBeforeQuery(t *testing.T) {
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
			q := &sqlPageQueries{}
			adapter, err := NewClusterAgentCapabilityAdapter(q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{tt.field: tt.value})); err == nil {
				t.Fatal("invalid direct adapter pagination was accepted")
			}
			if q.calls != 0 {
				t.Fatal("invalid direct adapter pagination reached SQL")
			}
		})
	}
}
