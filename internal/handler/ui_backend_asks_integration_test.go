//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

// TestPublicBrandingPrimaryColorDefaultsToUnconfigured: on a freshly migrated
// database the public branding reader returns primary_color "" (not the old
// seeded #0066CC), so the UI can treat "" as "operator has not chosen".
func TestPublicBrandingPrimaryColorDefaultsToUnconfigured(t *testing.T) {
	t.Parallel()
	pool := testdb.Pool(t)
	h := NewPlatformSettingsHandler(sqlc.New(pool))
	rec := httptest.NewRecorder()
	h.PublicBranding(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings/branding/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("GET /api/v1/settings/branding/ -> %s", rec.Body.String())
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	var found bool
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "branding.primary_color" || k == "primary_color" {
					found = true
					if s, ok := val.(string); !ok || s != "" {
						if m, ok := val.(map[string]any); !ok || m["value"] != "" {
							t.Fatalf("%s = %v, want \"\"", k, val)
						}
					}
				}
				walk(val)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(body)
	if !found {
		t.Fatalf("primary_color missing from branding response: %s", raw)
	}
}

// TestViewedAuditRowsAreReadClass: a *.viewed audit written with the
// mutation default (what recordAudit passes) is stored as action_class=read.
func TestViewedAuditRowsAreReadClass(t *testing.T) {
	t.Parallel()
	pool := testdb.Pool(t)
	q := sqlc.New(pool)
	ctx := context.Background()
	for _, tc := range []struct{ action, stored, want string }{
		{"admin.queues.viewed", "", "read"},
		{"compliance.baseline.viewed", "mutation", "read"},
		{"cluster.delete", "", "mutation"},
	} {
		if err := q.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
			Source: "http", Action: tc.action, ResourceType: "platform", Detail: json.RawMessage(`{}`), ActionClass: tc.stored,
		}); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := pool.QueryRow(ctx, `SELECT action_class FROM audit_log WHERE action = $1`, tc.action).Scan(&got); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s stored=%q -> action_class=%s", tc.action, tc.stored, got)
		if got != tc.want {
			t.Fatalf("%s action_class = %q, want %q", tc.action, got, tc.want)
		}
	}
}
