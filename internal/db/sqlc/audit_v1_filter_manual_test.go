package sqlc

import (
	"strings"
	"testing"
	"time"
)

func TestResolveAuditActionClass(t *testing.T) {
	cases := []struct {
		action, source, stored, want string
	}{
		{"auth.login", "service", "", "auth"},
		{"catalog.repo.sync_failed", "worker", "", "system"},
		{"request.post", "http", "mutation", "mutation"},
		{"read.audit", "http", "read", "read"},
		{"agent.connected", "tunnel", "", "system"},
	}
	for _, tc := range cases {
		got := resolveAuditActionClass(tc.action, tc.source, tc.stored)
		if got != tc.want {
			t.Errorf("resolveAuditActionClass(%q,%q,%q)=%q want %q",
				tc.action, tc.source, tc.stored, got, tc.want)
		}
	}
}

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
		auditEffectiveClassSQL + " = $3",
		"a.status_code >= 400",
		"a.detail @> jsonb_build_object('cluster_id', $4::text)",
		"a.detail @> jsonb_build_object('project_id', $5::text)",
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

func TestBuildAuditLogV1FilterWherePeopleAudienceHidesSystem(t *testing.T) {
	where, args := buildAuditLogV1FilterWhere(AuditLogFilterParams{Audience: "people"})
	if !strings.Contains(where, "a.source = 'worker'") || !strings.Contains(where, "NOT") {
		t.Fatalf("people audience WHERE:\n%s", where)
	}
	if len(args) != 0 {
		t.Fatalf("people audience should not bind args, got %#v", args)
	}
}

func TestBuildAuditLogV1FilterWhereQSearchesActionAndActor(t *testing.T) {
	where, args := buildAuditLogV1FilterWhere(AuditLogFilterParams{Q: "login"})
	for _, want := range []string{
		"lower(a.action) LIKE $1",
		"lower(a.path) LIKE $1",
		"lower(u.email) LIKE $1",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("Q filter WHERE missing %q:\n%s", want, where)
		}
	}
	if len(args) != 1 || args[0] != "%login%" {
		t.Fatalf("args = %#v, want [%%login%%]", args)
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

func TestBuildAuditLogV1PageQueryUsesBoundedLookaheadWithoutCount(t *testing.T) {
	query, args, limit := buildAuditLogV1PageQuery(AuditLogFilterParams{Limit: 50, Offset: 25})
	for _, forbidden := range []string{"MATERIALIZED", "count(*)", "jsonb_agg"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("interactive page query contains %q:\n%s", forbidden, query)
		}
	}
	if !strings.Contains(query, "ORDER BY a.created_at DESC, a.id DESC") || !strings.Contains(query, "LIMIT $1 OFFSET $2") {
		t.Fatalf("interactive page query is not a stable bounded page:\n%s", query)
	}
	if limit != 50 || len(args) != 2 || args[0] != int32(51) || args[1] != int32(25) {
		t.Fatalf("limit=%d args=%#v, want limit=50 args=[51 25]", limit, args)
	}
}
