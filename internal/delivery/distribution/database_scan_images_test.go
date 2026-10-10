package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func scanImageIndexFixture(t *testing.T, repository string) (string, []byte) {
	t.Helper()
	// Synthetic index bytes exercise the parent-member/hash contract. They are
	// not a published helper/scanner image or runtime/platform qualification.
	data := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"%s","size":100,"platform":{"os":"linux","architecture":"amd64"}},{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"%s","size":101,"platform":{"os":"linux","architecture":"arm64","variant":"v8"}}]}`, contentDigest([]byte(repository+"amd64")), contentDigest([]byte(repository+"arm64"))))
	return repository + "@" + contentDigest(data), data
}

func TestDatabaseScanImageGraphBindsOriginalIndexAndBothLinuxPlatforms(t *testing.T) {
	reference, original := scanImageIndexFixture(t, "93.184.216.34/qa-helper")
	image, err := databaseScanImageGraph(reference, original)
	if err != nil || image.Reference != reference || len(image.Platforms) != 2 || image.Platforms[0].Architecture != "amd64" || image.Platforms[1].Architecture != "arm64" {
		t.Fatal("original closed index did not yield exact platform DATA")
	}
	changed := append([]byte(nil), original...)
	changed[len(changed)-1] = ' '
	if _, err := databaseScanImageGraph(reference, changed); err == nil {
		t.Fatal("changed original index inherited an authenticated image digest")
	}
}

func TestDatabaseScanImageGraphRejectsAmbiguousOrUnboundedPlatformClaims(t *testing.T) {
	_, original := scanImageIndexFixture(t, "93.184.216.34/qa-helper")
	for name, changed := range map[string][]byte{
		"duplicate-key":          []byte(strings.Replace(string(original), `"schemaVersion":2`, `"schemaVersion":2,"schemaVersion":2`, 1)),
		"unknown-field":          []byte(strings.Replace(string(original), `"schemaVersion":2`, `"schemaVersion":2,"unreviewed":true`, 1)),
		"duplicate-architecture": []byte(strings.Replace(string(original), `"architecture":"arm64","variant":"v8"`, `"architecture":"amd64"`, 1)),
		"unknown-os":             []byte(strings.Replace(string(original), `"os":"linux"`, `"os":"windows"`, 1)),
		"unknown-arch":           []byte(strings.Replace(string(original), `"architecture":"arm64"`, `"architecture":"riscv64"`, 1)),
		"external-location":      []byte(strings.Replace(string(original), `"size":100`, `"size":100,"urls":["https://unreviewed.invalid/manifest"]`, 1)),
		"inline-descriptor":      []byte(strings.Replace(string(original), `"size":100`, `"size":100,"data":"e30="`, 1)),
		"zero-size":              []byte(strings.Replace(string(original), `"size":100`, `"size":0`, 1)),
		"oversize-descriptor":    []byte(strings.Replace(string(original), `"size":100`, `"size":4194305`, 1)),
		"oversize-index":         append(original, make([]byte, 1<<20)...),
		"trailing-document":      append(append([]byte(nil), original...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			// Rebind the outer hash so the actual closed descriptor/parser boundary
			// is tested rather than merely failing the changed-content hash.
			if _, err := databaseScanImageGraph("93.184.216.34/qa-helper@"+contentDigest(changed), changed); err == nil {
				t.Fatal("invalid platform/index claim supplied scanner image DATA")
			}
		})
	}
}

func TestMeasuredScanCapabilityRequiresHelperWithoutChangingLegacyClosure(t *testing.T) {
	assets := []Asset{{ID: "trivy-database"}, {ID: "trivy-java-database"}}
	if err := validateCapabilityAssets([]string{"vulnerability-scanning"}, assets); err != nil {
		t.Fatal("legacy scanner prerequisite contract changed")
	}
	if err := validateCapabilityAssets([]string{"measured-vulnerability-scanning"}, assets); err == nil {
		t.Fatal("measured mode silently inferred a missing helper")
	}
	assets = append(assets, Asset{ID: "database-scan-helper"})
	if err := validateCapabilityAssets([]string{"measured-vulnerability-scanning"}, assets); err != nil {
		t.Fatal("exact measured prerequisite roles refused")
	}
}

func TestDatabaseScanImagesRequireExactProfileScannerAndMirrorImage(t *testing.T) {
	helper, helperBytes := scanImageIndexFixture(t, "93.184.216.34/qa-helper")
	scanner, scannerBytes := scanImageIndexFixture(t, "93.184.216.34/qa-scanner")
	_, scannerDigest, _ := strings.Cut(scanner, "@")
	var member databaseScanImageMember
	member.SchemaVersion, member.Kind = 1, "AstronomerMeasuredDatabaseScanImages"
	member.Helper.Reference, member.Helper.Index = helper, helperBytes
	member.Scanner.Reference, member.Scanner.Index = scanner, scannerBytes
	data, err := json.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	component := builtinbundles.Component{DefaultEnabled: true, Images: []string{scanner}, Values: map[string]any{"operator": map[string]any{"vulnerabilityScannerEnabled": true}, "trivy": map[string]any{"image": map[string]any{"registry": "93.184.216.34", "repository": "qa-scanner", "tag": "0.75.0@" + scannerDigest}}}}
	component.Source.Chart = "trivy-operator"
	catalog := builtinbundles.Catalog{DistributionIdentity: contentDigest([]byte("qa-parent")), Components: []builtinbundles.Component{component}}
	bom := BOM{Profiles: []Profile{{Name: "builtins", Capabilities: []string{"measured-vulnerability-scanning"}}}, Assets: []Asset{{ID: "database-scan-helper", MemberPath: "roles/helper.json", Images: []string{helper}}}}
	loader := &Loader{options: Options{Profile: "builtins", Client: &http.Client{}}}
	images, err := loader.prepareDatabaseScanImages(t.Context(), catalog, bom, map[string][]byte{"roles/helper.json": data})
	if err != nil || images.Helper.Reference != helper || images.Scanner.Reference != scanner || images.DistributionIdentity != catalog.DistributionIdentity {
		t.Fatal("exact parent/profile/scanner DATA failed")
	}
	legacy := bom
	legacy.Profiles = []Profile{{Name: "builtins", Capabilities: []string{"vulnerability-scanning"}}}
	if images, err := loader.prepareDatabaseScanImages(t.Context(), catalog, legacy, nil); err != nil || images != nil {
		t.Fatal("legacy profile unexpectedly enabled a measured helper")
	}
	wrong := catalog
	wrong.Components = append([]builtinbundles.Component(nil), catalog.Components...)
	wrong.Components[0].Values = map[string]any{"operator": map[string]any{"vulnerabilityScannerEnabled": true}, "trivy": map[string]any{"image": map[string]any{"registry": "93.184.216.34", "repository": "qa-scanner", "tag": "0.75.0@" + contentDigest([]byte("different scanner"))}}}
	if _, err := loader.prepareDatabaseScanImages(t.Context(), wrong, bom, map[string][]byte{"roles/helper.json": data}); err == nil {
		t.Fatal("another configured scanner inherited helper platform DATA")
	}
	for _, name := range []string{"wrong-repository", "wrong-registry", "scanner-disabled", "component-disabled", "ambiguous-components", "match-across-components"} {
		t.Run(name, func(t *testing.T) {
			changed := catalog
			changed.Components = append([]builtinbundles.Component(nil), catalog.Components...)
			changed.Components[0].Values = map[string]any{"operator": map[string]any{"vulnerabilityScannerEnabled": true}, "trivy": map[string]any{"image": map[string]any{"registry": "93.184.216.34", "repository": "qa-scanner", "tag": "0.75.0@" + scannerDigest}}}
			image := changed.Components[0].Values["trivy"].(map[string]any)["image"].(map[string]any)
			switch name {
			case "wrong-repository":
				image["repository"] = "different-scanner"
			case "wrong-registry":
				image["registry"] = "1.1.1.1"
			case "scanner-disabled":
				changed.Components[0].Values["operator"].(map[string]any)["vulnerabilityScannerEnabled"] = false
			case "component-disabled":
				changed.Components[0].DefaultEnabled = false
			case "ambiguous-components":
				changed.Components = append(changed.Components, component)
			case "match-across-components":
				changed.Components[0].Images = nil
				other := component
				other.Source.Chart = "different-chart"
				changed.Components = append(changed.Components, other)
			}
			if _, err := loader.prepareDatabaseScanImages(t.Context(), changed, bom, map[string][]byte{"roles/helper.json": data}); err == nil {
				t.Fatal("wrong or disabled component supplied measured scanner DATA")
			}
		})
	}
	loader.options.MirrorPlan = &MirrorPlan{Rewrites: []Rewrite{{Kind: "image", Source: scanner, Target: scanner}}}
	loader.options.Network = httpclient.DestinationPolicy{Restricted: true}
	if _, err := loader.prepareDatabaseScanImages(t.Context(), catalog, bom, map[string][]byte{"roles/helper.json": data}); err == nil {
		t.Fatal("mirror without exact helper rewrite supplied measured image DATA")
	}
}

func TestDatabaseScanImagesCannotUseCallerSnapshotOrNilPhysicalProof(t *testing.T) {
	var authority *DatabaseAuthority
	if _, err := authority.DatabaseScanImages(context.Background(), nil); err == nil {
		t.Fatal("missing physical owner supplied measured image DATA")
	}
}
