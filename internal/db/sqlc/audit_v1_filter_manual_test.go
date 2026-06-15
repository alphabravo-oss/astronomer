package sqlc

import (
	"strings"
	"testing"
	"time"
)

func TestBuildAuditLogV1FilterWhereComposesFilters(t *testing.T) {
	from := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	where, args := buildAuditLogV1FilterWhere(AuditLogFilterParams{
		Actor:       "admin@example.com",
		Target:      "prod-east",
		Result:      "failure",
		ClusterID:   "cluster-123",
		ProjectID:   "project-456",
		From:        from,
		HasFrom:     true,
		To:          to,
		HasTo:       true,
		ActionClass: "mutation",
	})

	for _, want := range []string{
		"EXISTS",
		"lower(u.email) LIKE $1",
		"lower(a.resource_name) LIKE $2",
		"a.action_class = $3",
		"a.status_code >= 400",
		"a.detail->>'cluster_id' = $4",
		"a.detail->>'project_id' = $5",
		"a.created_at >= $6",
		"a.created_at <= $7",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("filter WHERE missing %q:\n%s", want, where)
		}
	}
	if strings.Contains(where, "%!") {
		t.Fatalf("filter WHERE contains formatting error:\n%s", where)
	}
	if len(args) != 7 {
		t.Fatalf("args = %d, want 7 (%#v)", len(args), args)
	}
}

func TestBuildAuditLogV1FilterWhereEmpty(t *testing.T) {
	where, args := buildAuditLogV1FilterWhere(AuditLogFilterParams{})
	if where != "" {
		t.Fatalf("where = %q, want empty", where)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
}
