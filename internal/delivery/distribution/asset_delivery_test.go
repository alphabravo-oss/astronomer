package distribution

import (
	"strings"
	"testing"
)

func TestSplitScannerDatabaseLocationsUseExactMirroredSubject(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	values := map[string]any{"dbRegistry": "vendor.example.test", "dbRepository": "distribution/assets/trivy-database@" + digest, "javaDbRegistry": "vendor.example.test", "javaDbRepository": "distribution/assets/trivy-java-database@" + digest}
	locations := map[string]string{"vendor.example.test/distribution/assets/trivy-database@" + digest: "mirror.corp.local/release/db@" + digest, "vendor.example.test/distribution/assets/trivy-java-database@" + digest: "mirror.corp.local/release/java@" + digest}
	projected, err := rewriteValues(values, exactTable(locations))
	if err != nil {
		t.Fatal(err)
	}
	result := projected.(map[string]any)
	if result["dbRegistry"] != "mirror.corp.local" || result["dbRepository"] != "release/db@"+digest || result["javaDbRegistry"] != "mirror.corp.local" || result["javaDbRepository"] != "release/java@"+digest {
		t.Fatal("runtime split database fetch retained original location or changed content digest")
	}
	for _, targets := range []map[string]string{{}, {"vendor.example.test/db@" + digest: "mirror.corp.local/db@sha256:" + strings.Repeat("b", 64)}} {
		values := map[string]any{"dbRegistry": "vendor.example.test", "dbRepository": "db@" + digest}
		if _, err := rewriteDatabaseLocations(values, exactTable(targets)); err == nil {
			t.Fatal("missing or changed database subject was rewritten")
		}
	}
}

func TestEndorsedDatabaseSubjectPreservesOriginalDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	asset := Asset{ID: "trivy-database", MemberPath: "assets/db", ArtifactManifestPath: "assets/manifest", ArtifactConfigPath: "assets/config", ArtifactSubject: "supplier.example.test/db@" + digest, PublishedSubject: "vendor.example.test/assets/db@" + digest, Images: []string{}}
	members := map[string]string{"assets/db": "vulnerability_database", "assets/manifest": "vulnerability_database", "assets/config": "vulnerability_database"}
	if err := validateAssetDeclarations(EmbeddedAssetContract, []Asset{asset}, members); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{"", "vendor.example.test/assets/db@sha256:" + strings.Repeat("b", 64), "invalid?target@" + digest} {
		changed := asset
		changed.PublishedSubject = subject
		if err := validateAssetDeclarations(EmbeddedAssetContract, []Asset{changed}, members); err == nil {
			t.Fatal("missing/changed/invalid endorsed subject accepted")
		}
	}
}
