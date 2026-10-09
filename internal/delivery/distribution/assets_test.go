package distribution

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrerequisiteCapabilitiesRequireExactRoles(t *testing.T) {
	members := map[string]string{}
	assets := []Asset{}
	for role, category := range assetRoles {
		if category == "vulnerability_database" {
			continue
		}
		path := "assets/" + role + ".yaml"
		members[path] = category
		images := []string{"registry.example.test/" + role + "@sha256:" + strings.Repeat("a", 64)}
		if role == "cnpg-crds" || role == "application-catalog" {
			images = []string{}
		}
		assets = append(assets, Asset{ID: role, MemberPath: path, Images: images})
	}
	if err := ValidateAssetClosure(assets, members, []string{"platform-core", "application-catalog", "distribution-registry"}); err != nil {
		t.Fatal(err)
	}
	for index, asset := range assets {
		if asset.ID == "application-catalog" || asset.ID == "distribution-registry" {
			continue
		}
		missing := append([]Asset{}, assets[:index]...)
		missing = append(missing, assets[index+1:]...)
		if ValidateAssetClosure(missing, members, []string{"platform-core"}) == nil {
			t.Fatalf("missing exact role %s accepted through category presence", asset.ID)
		}
	}
	for _, capabilities := range [][]string{nil, {"unknown"}, {"database-cnpg", "database-cnpg"}} {
		if ValidateAssetClosure(assets, members, capabilities) == nil {
			t.Fatal("invalid capability declaration accepted")
		}
	}
	malformed := append([]Asset{}, assets...)
	malformed[0].ID = "some-other-operator"
	if ValidateAssetClosure(malformed, members, []string{}) == nil {
		t.Fatal("unclassified prerequisite role accepted")
	}
}

func TestDatabaseArtifactBindsExactManifestAndPayload(t *testing.T) {
	payload := []byte("local QA database payload")
	config := []byte(`{"qa":"captured config"}`)
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": map[string]any{"mediaType": "application/vnd.oci.empty.v1+json", "digest": contentDigest(config), "size": len(config)}, "layers": []any{map[string]any{"mediaType": "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", "digest": contentDigest(payload), "size": len(payload)}}})
	asset := Asset{ID: "trivy-database", MemberPath: "db/archive.tgz", ArtifactSubject: "registry.example.test/db@" + contentDigest(manifest), ArtifactManifestPath: "db/manifest.json", ArtifactConfigPath: "db/config.json", Images: []string{}}
	members := map[string][]byte{asset.MemberPath: payload, asset.ArtifactManifestPath: manifest, asset.ArtifactConfigPath: config}
	if err := validateArtifactAssets([]Asset{asset}, members); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(){func() { members[asset.MemberPath] = []byte("changed payload") }, func() { members[asset.ArtifactManifestPath] = append(manifest, []byte("{}")...) }, func() { delete(members, asset.ArtifactConfigPath) }, func() { members[asset.ArtifactConfigPath] = []byte("changed config") }, func() { asset.ArtifactSubject = "registry.example.test/db@sha256:" + strings.Repeat("f", 64) }} {
		members[asset.MemberPath] = payload
		members[asset.ArtifactManifestPath] = manifest
		members[asset.ArtifactConfigPath] = config
		change()
		if validateArtifactAssets([]Asset{asset}, members) == nil {
			t.Fatal("changed database identity accepted")
		}
	}
	for _, descriptor := range []string{"config", "layer"} {
		for _, field := range []string{"urls", "data"} {
			var raw map[string]any
			_ = json.Unmarshal(manifest, &raw)
			target := raw["config"].(map[string]any)
			if descriptor == "layer" {
				target = raw["layers"].([]any)[0].(map[string]any)
			}
			if field == "urls" {
				target[field] = []string{"https://public.example.test/fallback"}
			} else {
				target[field] = "aW5saW5l"
			}
			changed, _ := json.Marshal(raw)
			members[asset.ArtifactManifestPath] = changed
			members[asset.ArtifactConfigPath] = config
			members[asset.MemberPath] = payload
			asset.ArtifactSubject = "registry.example.test/db@" + contentDigest(changed)
			if validateArtifactAssets([]Asset{asset}, members) == nil {
				t.Fatalf("authentic manifest accepted %s %s", descriptor, field)
			}
		}
	}
}

func TestEffectiveScannerCannotOmitDatabaseCapability(t *testing.T) {
	payload, release, digest := archiveFixture(t, nil, func(bom *BOM, members map[string][]byte) {
		var catalog map[string]any
		_ = json.Unmarshal(members["catalog.json"], &catalog)
		component := catalog["components"].([]any)[0].(map[string]any)
		values := component["values"].(map[string]any)
		values["operator"] = map[string]any{"vulnerabilityScannerEnabled": true}
		members["catalog.json"], _ = json.Marshal(catalog)
		bom.CatalogDigest = contentDigest(members["catalog.json"])
		var allValues map[string]any
		_ = json.Unmarshal(members["profiles/values.json"], &allValues)
		allValues[component["slug"].(string)] = values
		members["profiles/values.json"], _ = json.Marshal(allValues)
		for index := range bom.Members {
			member := &bom.Members[index]
			member.Digest = contentDigest(members[member.Path])
			member.Size = int64(len(members[member.Path]))
		}
	})
	if _, _, err := ReadArchive(payload, release, digest, "builtins"); err == nil || blockedCategory(err) != "vulnerability_database" {
		t.Fatalf("scanner bypassed DB capability: %v", err)
	}
}
