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
