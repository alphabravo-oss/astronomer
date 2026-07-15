package queries_test

import (
	"os"
	"strings"
	"testing"
)

func TestDiscoveredApplicationUpsertIsStableAndBounded(t *testing.T) {
	raw, err := os.ReadFile("argocd.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "-- name: UpsertDiscoveredArgoCDApplication")
	end := strings.Index(text[start+1:], "-- name:")
	if start < 0 {
		t.Fatal("discovery upsert query missing")
	}
	block := text[start:]
	if end >= 0 {
		block = text[start : start+1+end]
	}
	for _, required := range []string{
		"ON CONFLICT (argocd_instance_id, name) DO UPDATE",
		"upstream_uid = EXCLUDED.upstream_uid",
		"application_namespace = EXCLUDED.application_namespace",
		"WHERE argocd_applications.upstream_uid = ''",
		"OR argocd_applications.upstream_uid = EXCLUDED.upstream_uid",
		"RETURNING *",
	} {
		if !strings.Contains(block, required) {
			t.Fatalf("discovery upsert missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"id = EXCLUDED.id",
		"sync_status = EXCLUDED.sync_status",
		"health_status = EXCLUDED.health_status",
		"last_synced = EXCLUDED.last_synced",
		"resource_created_count = EXCLUDED.resource_created_count",
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("discovery upsert must preserve stable/last-good state; found %q", forbidden)
		}
	}
}
