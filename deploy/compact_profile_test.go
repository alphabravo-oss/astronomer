package deploy

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompactProfileUsesSingleCNPGAndBoundedPools(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplateWithValueFiles(t, []string{"chart/values-single-node.yaml"}))
	db := findRenderedDoc(t, docs, "Cluster", "astronomer-postgres")
	config := findRenderedDoc(t, docs, "ConfigMap", "astronomer-config")
	if stringAt(config, "data", "DB_MAX_CONNS") != "12" || stringAt(config, "data", "DB_MIN_CONNS") != "2" {
		t.Fatal("compact profile must bound application connection pools")
	}
	if fmt.Sprint(nestedMap(db, "spec")["instances"]) != "1" {
		t.Fatal("compact CNPG must have one instance")
	}
	if nestedMap(db, "spec", "postgresql")["synchronous"] != nil {
		t.Fatal("one instance cannot require synchronous standby")
	}
	for _, component := range []string{"server", "worker", "frontend"} {
		d := findRenderedDoc(t, docs, "Deployment", "astronomer-"+component)
		if fmt.Sprint(nestedMap(d, "spec")["replicas"]) != "1" {
			t.Fatalf("%s replicas", component)
		}
	}
	for _, d := range docs {
		if stringValue(d["kind"]) == "StatefulSet" && stringAt(d, "metadata", "name") == "astronomer-postgres" {
			t.Fatal("legacy PostgreSQL rendered")
		}
	}
}

func TestCNPGUpgradeValidatesTargetBeforeAcceptingRetainedPVC(t *testing.T) {
	profile := []string{"chart/values-single-node.yaml"}
	upgrade := helmTemplateWithValueFilesAndFlags(t, profile, []string{"--is-upgrade"})
	docs := parseRenderedDocs(t, upgrade)
	job := findRenderedDoc(t, docs, "Job", "astronomer-preflight")
	db := findContainer(t, podSpecFor(job), "initContainers", "db-preflight")
	ref := nestedMap(findEnvVar(t, db, "DATABASE_URL"), "valueFrom", "secretKeyRef")
	if stringValue(ref["name"]) != "astronomer-postgres-app" || stringValue(ref["key"]) != "uri" {
		t.Fatalf("CNPG upgrade must validate its generated application Secret: %v", ref)
	}
	if !strings.Contains(upgrade, "CloudNativePG target schema validated; retained source PVCs are allowed.") {
		t.Fatal("CNPG upgrade cannot retain rollback storage")
	}
	if !strings.Contains(upgrade, "refusing an empty cutover") {
		t.Fatal("changing database mode must not silently cut over to an empty CNPG database")
	}
	policy := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-postgres")
	ingress := nestedMap(policy, "spec")["ingress"].([]any)
	allowed := false
	for _, rule := range ingress {
		for _, peer := range rule.(map[string]any)["from"].([]any) {
			if stringAt(peer.(map[string]any), "podSelector", "matchLabels", "app.kubernetes.io/component") == "preflight" {
				allowed = true
			}
		}
	}
	if !allowed {
		t.Fatal("CNPG must accept the upgrade schema check through its ingress policy")
	}
	fresh := helmTemplateWithValueFiles(t, profile)
	if strings.Contains(fresh, "name: db-preflight") || strings.Contains(fresh, "CloudNativePG target schema validated;") {
		t.Fatal("fresh CNPG installation must not require or claim validation of a nonexistent database")
	}
}
