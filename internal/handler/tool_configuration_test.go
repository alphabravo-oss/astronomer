package handler

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"
)

func TestCombineInstalledToolValuesReconstructsValuesKeys(t *testing.T) {
	tool := sqlc.ClusterTool{
		Slug:             "istio",
		DefaultNamespace: "istio-system",
		Charts: json.RawMessage(`[
			{"order":0,"chart_name":"base","repo_url":"https://example.test","namespace":"istio-system","release_name":"istio-base","version":"1.0.0","values_key":"base"},
			{"order":1,"chart_name":"istiod","repo_url":"https://example.test","namespace":"istio-system","release_name":"istiod","version":"1.0.0","values_key":"istiod"}
		]`),
	}
	rows := []sqlc.InstalledChart{
		{ID: uuid.New(), ReleaseName: "istiod", Namespace: "istio-system", ValuesOverride: "replicaCount: 2\nadvanced:\n  enabled: true\n", Revision: 4, PresetUsed: pgtype.Text{String: "production", Valid: true}},
		{ID: uuid.New(), ReleaseName: "istio-base", Namespace: "istio-system", ValuesOverride: "defaultRevision: stable\n", Revision: 2, PresetUsed: pgtype.Text{String: "production", Valid: true}},
	}
	raw, preset, releases, err := combineInstalledToolValues(tool, rows)
	if err != nil {
		t.Fatal(err)
	}
	if preset != "production" || len(releases) != 2 {
		t.Fatalf("preset=%q releases=%d", preset, len(releases))
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatal(err)
	}
	istiod := values["istiod"].(map[string]any)
	if istiod["replicaCount"] != float64(2) {
		t.Fatalf("values=%#v", values)
	}
}

func TestCombineInstalledToolValuesPrefersMainRelease(t *testing.T) {
	tool := sqlc.ClusterTool{
		Slug:             "neuvector",
		DefaultNamespace: "cattle-neuvector-system",
		Charts: json.RawMessage(`[
			{"order":0,"chart_name":"neuvector-crd","repo_url":"https://example.test","namespace":"cattle-neuvector-system","release_name":"neuvector-crd","version":"1.0.0"},
			{"order":1,"chart_name":"neuvector","repo_url":"https://example.test","namespace":"cattle-neuvector-system","release_name":"neuvector","version":"1.0.0"}
		]`),
	}
	rows := []sqlc.InstalledChart{
		{ID: uuid.New(), ReleaseName: "neuvector-crd", Namespace: "cattle-neuvector-system", ValuesOverride: "controller:\n  replicas: 1\n", Revision: 1},
		{ID: uuid.New(), ReleaseName: "neuvector", Namespace: "cattle-neuvector-system", ValuesOverride: "controller:\n  replicas: 3\n", Revision: 2},
	}
	raw, _, _, err := combineInstalledToolValues(tool, rows)
	if err != nil {
		t.Fatal(err)
	}
	if raw != rows[1].ValuesOverride {
		t.Fatalf("values=%q", raw)
	}
}
