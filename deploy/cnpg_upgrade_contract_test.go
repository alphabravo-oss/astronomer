package deploy

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// An application image update must not change the identity or data/recovery
// configuration of an operator-managed database. This compares actual renders;
// it does not prove live failover, PVC preservation, or PostgreSQL major upgrades.
func TestApplicationImageUpgradePreservesCNPGAndSharedOperatorOwnership(t *testing.T) {
	sets := append([]string{}, productionWiringSets...)
	sets = append(sets,
		"managementBackup.s3.bucket=upgrade-contract-backups",
		"managementBackup.s3.credentialsSecretRef.name=backup-creds",
		"postgres.cloudNativePG.clusterName=persistent-database",
		"postgres.cloudNativePG.storage.storageClassName=database-storage",
		"postgres.cloudNativePG.walStorage.enabled=true",
		"postgres.cloudNativePG.walStorage.storageClassName=wal-storage",
		"postgres.cloudNativePG.barmanCloud.enabled=true",
		"postgres.cloudNativePG.barmanCloud.objectStoreName=shared-backups",
		"postgres.cloudNativePG.barmanCloud.scheduledBackup.enabled=true",
	)
	render := func(tag, digest string) []renderedDoc {
		t.Helper()
		versionSets := append([]string{}, sets...)
		for _, role := range []string{"server", "worker", "agent", "migrate"} {
			versionSets = append(versionSets, "image."+role+".tag="+tag)
			versionSets = append(versionSets, "image."+role+".digest="+digest)
		}
		versionSets = append(versionSets, "frontend.image.tag="+tag)
		versionSets = append(versionSets, "frontend.image.digest="+digest)
		return parseRenderedDocs(t, helmTemplateWithValueFiles(t,
			[]string{filepath.Join("chart", "values-production.yaml")}, versionSets...))
	}
	before := render("v1.2.1", productionTestImageDigest)
	after := render("v1.2.2", "sha256:"+strings.Repeat("2", 64))
	oldServer := containerList(podSpecFor(findRenderedDoc(t, before, "Deployment", "astronomer-server")), "containers")
	newServer := containerList(podSpecFor(findRenderedDoc(t, after, "Deployment", "astronomer-server")), "containers")
	if len(oldServer) == 0 || len(newServer) == 0 || oldServer[0]["image"] == newServer[0]["image"] {
		t.Fatal("upgrade fixture must change the actual server image digest")
	}
	for _, kind := range []string{"Cluster", "ScheduledBackup"} {
		old := findRenderedDoc(t, before, kind, "persistent-database")
		updated := findRenderedDoc(t, after, kind, "persistent-database")
		if !reflect.DeepEqual(old["spec"], updated["spec"]) {
			t.Fatalf("application image update changed %s database configuration", kind)
		}
		if got := stringAt(updated, "metadata", "annotations", "helm.sh/hook"); got != "" {
			t.Fatalf("%s database resource became a lifecycle hook: %q", kind, got)
		}
	}
	cluster := findRenderedDoc(t, after, "Cluster", "persistent-database")
	if got := fmt.Sprint(nestedMap(cluster, "spec")["instances"]); got != "3" {
		t.Fatalf("production CNPG instances = %s, want 3", got)
	}
	for _, doc := range after {
		kind, api := stringValue(doc["kind"]), stringValue(doc["apiVersion"])
		if kind == "ObjectStore" || kind == "CustomResourceDefinition" && api == "apiextensions.k8s.io/v1" &&
			(stringAt(doc, "spec", "group") == "postgresql.cnpg.io" || stringAt(doc, "spec", "group") == "barmancloud.cnpg.io") {
			t.Fatalf("application chart took ownership of shared database resource %s/%s", kind, stringAt(doc, "metadata", "name"))
		}
		if kind == "StatefulSet" && stringAt(doc, "metadata", "name") == "astronomer-postgres" {
			t.Fatal("production application update rendered a standalone database")
		}
	}
}
