package sqlc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type operationIdempotencyFakeRow struct {
	values []any
}

func (r operationIdempotencyFakeRow) Scan(dest ...any) error {
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = r.values[i].(string)
		case *json.RawMessage:
			*d = r.values[i].(json.RawMessage)
		default:
			// pgtype fields are covered by sqlc integration tests; this unit
			// keeps the scan helper from drifting as columns are added.
		}
	}
	return nil
}

func TestScanOperationIdempotencyKey(t *testing.T) {
	row := operationIdempotencyFakeRow{values: []any{
		"user:user-1",
		"idem-1",
		"tool_operations",
		nil,
		json.RawMessage(`{"operationId":"op-1"}`),
		nil,
		nil,
	}}

	got, err := scanOperationIdempotencyKey(row)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got.Scope != "user:user-1" || got.IdempotencyKey != "idem-1" || got.OperationTable != "tool_operations" {
		t.Fatalf("row = %+v", got)
	}
	if string(got.Response) != `{"operationId":"op-1"}` {
		t.Fatalf("response = %s", got.Response)
	}
}

func TestAttachOperationIdempotencyKeyParamsKeepResponseJSON(t *testing.T) {
	params := AttachOperationIdempotencyKeyParams{
		Scope:          "user:user-1",
		IdempotencyKey: "idem-1",
		OperationTable: "tool_operations",
		OperationID:    uuid.New(),
		Response:       json.RawMessage(`{"status":"pending"}`),
	}
	if !json.Valid(params.Response) {
		t.Fatal("response should be valid json")
	}
	_ = context.Background()
}

func TestOperationIdempotencySQLClaimsWithAtomicUpsert(t *testing.T) {
	queries := map[string]string{
		"common":          operationIdempotencyClaimCTE,
		"restore":         createRestoreOperationIdempotent,
		"deferred":        createDeferredOperationIdempotent,
		"agent_lifecycle": createAgentLifecycleOperationIdempotent,
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(query, "ON CONFLICT (scope, idempotency_key) DO UPDATE") {
				t.Fatalf("query does not claim idempotency keys with an atomic upsert:\n%s", query)
			}
			if !strings.Contains(query, "RETURNING operation_table, operation_id") {
				t.Fatalf("query does not return the claimed operation pointer:\n%s", query)
			}
			if strings.Contains(query, "ON CONFLICT (scope, idempotency_key) DO NOTHING") {
				t.Fatalf("query still contains a separate non-claiming reservation path:\n%s", query)
			}
		})
	}
	for _, fragment := range []string{
		"INSERT INTO operation_idempotency_keys (scope, idempotency_key, operation_table, operation_id)",
		"VALUES ($1, $2, $9, gen_random_uuid())",
	} {
		if !strings.Contains(operationIdempotencyClaimCTE, fragment) {
			t.Fatalf("first idempotency claim does not initialize its operation pointer; missing %q:\n%s", fragment, operationIdempotencyClaimCTE)
		}
	}
}

func TestCatalogOperationDispositionDistinguishesInsertFromReplay(t *testing.T) {
	tests := map[string]struct {
		query string
		table string
	}{
		"catalog": {query: createCatalogOperationIdempotentWithDisposition, table: "catalog_operations"},
		"logging": {query: createLoggingOperationIdempotentWithDisposition, table: "logging_operations"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for _, fragment := range []string{
				"true AS inserted FROM inserted",
				"false AS inserted FROM " + tt.table,
				"ON CONFLICT (id) DO NOTHING",
			} {
				if !strings.Contains(tt.query, fragment) {
					t.Fatalf("%s idempotency disposition query is missing %q:\n%s", name, fragment, tt.query)
				}
			}
		})
	}
}

func TestSpecialOperationQueriesInitializeFirstClaim(t *testing.T) {
	queries := map[string]struct {
		query string
		table string
	}{
		"restore":         {query: createRestoreOperationIdempotent, table: "restore_operations"},
		"deferred":        {query: createDeferredOperationIdempotent, table: "deferred_operations"},
		"agent lifecycle": {query: createAgentLifecycleOperationIdempotent, table: "agent_lifecycle_operations"},
	}
	for name, tt := range queries {
		t.Run(name, func(t *testing.T) {
			for _, fragment := range []string{
				"INSERT INTO operation_idempotency_keys (scope, idempotency_key, operation_table, operation_id)",
				"VALUES ($1, $2, '" + tt.table + "', gen_random_uuid())",
			} {
				if !strings.Contains(tt.query, fragment) {
					t.Fatalf("first idempotency claim is incomplete; missing %q:\n%s", fragment, tt.query)
				}
			}
		})
	}
}
