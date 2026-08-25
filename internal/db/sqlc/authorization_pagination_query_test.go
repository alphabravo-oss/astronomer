package sqlc

import (
	"strings"
	"testing"
)

func TestScopedFleetQueriesAuthorizeBeforePagination(t *testing.T) {
	queries := map[string]string{
		"anomaly baselines":  listAnomalyBaselinesForScopes,
		"installed charts":   listInstalledChartsForScopes,
		"tool operations":    listToolOperationsForScopes,
		"catalog operations": listCatalogOperationsForScopes,
		"logging operations": listLoggingOperationsForScopes,
	}
	for name, query := range queries {
		authz := strings.Index(query, "ANY($")
		limit := strings.LastIndex(query, "LIMIT $")
		if authz < 0 {
			t.Errorf("%s query has no authorized cluster predicate", name)
		}
		if limit < 0 {
			t.Errorf("%s query has no database page boundary", name)
		}
		if authz > limit {
			t.Errorf("%s applies authorization after LIMIT", name)
		}
		if !strings.Contains(query, "id DESC") {
			t.Errorf("%s query lacks a deterministic UUID tie-breaker", name)
		}
	}
}

func TestScopedOperationQueriesRejectMalformedEnvelopeClusterIDs(t *testing.T) {
	for name, query := range map[string]string{
		"tool":    listToolOperationsForScopes,
		"catalog": listCatalogOperationsForScopes,
		"logging": listLoggingOperationsForScopes,
	} {
		if !strings.Contains(query, "~*") || !strings.Contains(query, "::uuid") {
			t.Errorf("%s scoped query must guard its JSON-to-UUID cast", name)
		}
	}
	if !strings.Contains(listLoggingOperationsForScopes, "LEFT JOIN logging_outputs") ||
		!strings.Contains(listLoggingOperationsForScopes, "LEFT JOIN logging_pipelines") {
		t.Fatal("logging scope query must preserve authorization for legacy payloads via target ownership")
	}
}

func TestScopedOperationCountsUseSameVisibilityPredicate(t *testing.T) {
	pairs := map[string][2]string{
		"tool":    {listToolOperationsForScopes, countToolOperationsForScopes},
		"catalog": {listCatalogOperationsForScopes, countCatalogOperationsForScopes},
		"logging": {listLoggingOperationsForScopes, countLoggingOperationsForScopes},
	}
	for name, pair := range pairs {
		for _, fragment := range []string{"target_type", "target_key", "status", "ANY($"} {
			if !strings.Contains(pair[0], fragment) || !strings.Contains(pair[1], fragment) {
				t.Errorf("%s list/count predicate mismatch: missing %q", name, fragment)
			}
		}
	}
}
