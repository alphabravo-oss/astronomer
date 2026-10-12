package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type fixtureMember struct {
	name string
	data []byte
	kind byte
}

func archiveFixture(t *testing.T, extra []fixtureMember, mutate func(*BOM, map[string][]byte)) ([]byte, string, string) {
	t.Helper()
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	members := map[string][]byte{}
	values := map[string]map[string]any{}
	inventory := Inventory{SchemaVersion: 1}
	images := map[string]bool{}
	for index := range catalog.Components {
		component := &catalog.Components[index]
		// This bounded archive fixture qualifies chart consumption only. Scanner
		// database capabilities are exercised separately with exact QA subjects.
		if operator, ok := component.Values["operator"].(map[string]any); ok {
			operator["vulnerabilityScannerEnabled"] = false
		}
		chart := chartFixture(t, component.Slug)
		component.Source.ChartDigest = contentDigest(chart)
		members["charts/"+component.Slug+".tgz"] = chart
		values[component.Slug] = component.Values
		for _, image := range component.Images {
			if !images[image] {
				inventory.Images = append(inventory.Images, Image{Reference: image, Platforms: []string{"linux/amd64", "linux/arm64"}, Workloads: []string{component.Slug}})
				images[image] = true
			}
		}
	}
	members["catalog.json"], _ = json.Marshal(catalog)
	catalogDigest := contentDigest(members["catalog.json"])
	members["profiles/values.json"], _ = json.Marshal(values)
	members["profiles/images.json"], _ = json.Marshal(inventory)
	bom := BOM{SchemaVersion: 1, AssetContractVersion: 1, Assets: []Asset{}, Release: catalog.Release, CatalogDigest: catalogDigest, Profiles: []Profile{{Name: "builtins", Qualification: "qualified", ValuesPath: "profiles/values.json", InventoryPath: "profiles/images.json", RequiredCategories: []string{"catalog", "chart", "values", "image_inventory"}, BlockedCategories: []string{}, Capabilities: []string{}}}}
	for name, data := range members {
		category := "chart"
		if name == "catalog.json" {
			category = "catalog"
		}
		if name == "profiles/values.json" {
			category = "values"
		}
		if name == "profiles/images.json" {
			category = "image_inventory"
		}
		bom.Members = append(bom.Members, Member{Path: name, Size: int64(len(data)), Digest: contentDigest(data), Category: category})
	}
	for _, component := range catalog.Components {
		bom.Charts = append(bom.Charts, ChartSubject{Component: component.Slug, Repository: "1.1.1.1/charts", Digest: contentDigest([]byte("OCI manifest " + component.Slug)), ArchiveDigest: component.Source.ChartDigest})
	}
	if mutate != nil {
		mutate(&bom, members)
	}
	members["bom.json"], _ = json.Marshal(bom)
	var result bytes.Buffer
	compressed := gzip.NewWriter(&result)
	writer := tar.NewWriter(compressed)
	for name, data := range members {
		if err := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(data)
	}
	for _, member := range extra {
		kind := member.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := writer.WriteHeader(&tar.Header{Name: member.name, Typeflag: kind, Size: int64(len(member.data)), Linkname: "../../outside"}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(member.data)
	}
	_ = writer.Close()
	_ = compressed.Close()
	return result.Bytes(), catalog.Release, bom.CatalogDigest
}
func TestReadArchiveClosedBOM(t *testing.T) {
	payload, release, digest := archiveFixture(t, nil, nil)
	catalog, bom, err := ReadArchive(payload, release, digest, "builtins")
	if err != nil || len(catalog.Components) != 3 || len(bom.Members) != 6 {
		t.Fatalf("catalog=%d members=%d err=%v", len(catalog.Components), len(bom.Members), err)
	}
	tests := []struct {
		name   string
		extra  []fixtureMember
		mutate func(*BOM, map[string][]byte)
	}{
		{name: "extra", extra: []fixtureMember{{name: "extra", data: []byte("unsigned")}}},
		{name: "duplicate", extra: []fixtureMember{{name: "catalog.json", data: []byte("duplicate")}}},
		{name: "traversal", extra: []fixtureMember{{name: "../outside", data: []byte("unsafe")}}},
		{name: "symlink", extra: []fixtureMember{{name: "link", kind: tar.TypeSymlink}}},
		{name: "hardlink", extra: []fixtureMember{{name: "link", kind: tar.TypeLink}}},
		{name: "tamper", mutate: func(_ *BOM, m map[string][]byte) { m["catalog.json"] = []byte("tamper") }},
		{name: "missing", mutate: func(_ *BOM, m map[string][]byte) { delete(m, "profiles/values.json") }},
		{name: "unqualified", mutate: func(b *BOM, _ map[string][]byte) {
			b.Profiles[0].Qualification = "unqualified"
			b.Profiles[0].BlockedCategories = []string{"database"}
		}},
		{name: "required category", mutate: func(b *BOM, _ map[string][]byte) {
			b.Profiles[0].RequiredCategories = append(b.Profiles[0].RequiredCategories, "plugin")
		}},
		{name: "mutable image", mutate: func(b *BOM, m map[string][]byte) {
			var i Inventory
			_ = json.Unmarshal(m["profiles/images.json"], &i)
			i.Images[0].Reference = "public/image:latest"
			m["profiles/images.json"], _ = json.Marshal(i)
			for n := range b.Members {
				if b.Members[n].Path == "profiles/images.json" {
					b.Members[n].Digest = contentDigest(m["profiles/images.json"])
					b.Members[n].Size = int64(len(m["profiles/images.json"]))
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, release, digest := archiveFixture(t, test.extra, test.mutate)
			if _, _, err := ReadArchive(payload, release, digest, "builtins"); err == nil {
				t.Fatal("invalid distribution accepted")
			}
		})
	}
	for _, body := range [][]byte{append(payload, []byte("extra stream")...), payload[:len(payload)-8]} {
		if _, _, err := ReadArchive(body, release, digest, "builtins"); err == nil {
			t.Fatal("invalid compression accepted")
		}
	}
	if _, _, err := ReadArchive(payload, "v1.2.999", digest, "builtins"); err == nil {
		t.Fatal("wrong release accepted")
	}
	if _, _, err := ReadArchive(payload, release, "sha256:"+strings.Repeat("0", 64), "builtins"); err == nil {
		t.Fatal("wrong catalog accepted")
	}
}

func chartFixture(t *testing.T, slug string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	body := []byte("apiVersion: v2\nname: " + slug + "\nversion: 1.0.0\n")
	if err := writer.WriteHeader(&tar.Header{Name: slug + "/Chart.yaml", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestClosedJSONRejectsDuplicateUnknownAndExcessiveNesting(t *testing.T) {
	for _, body := range []string{`{"schema_version":1,"schema_version":1}`, `{"schema_version":1,"allow_unsigned":true}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), `{"schema_version":1} invalid`} {
		var inventory Inventory
		if decodeClosed([]byte(body), &inventory) == nil {
			t.Fatal("ambiguous or invalid schema accepted")
		}
	}
}

func TestSignedNilValuesProfileFailsWithoutMirrorPanic(t *testing.T) {
	payload, release, digest := archiveFixture(t, nil, func(bom *BOM, members map[string][]byte) {
		var catalog builtinbundles.Catalog
		_ = json.Unmarshal(members["catalog.json"], &catalog)
		slug := catalog.Components[0].Slug
		catalog.Components[0].Values = nil
		members["catalog.json"], _ = json.Marshal(catalog)
		bom.CatalogDigest = contentDigest(members["catalog.json"])
		var values map[string]map[string]any
		_ = json.Unmarshal(members["profiles/values.json"], &values)
		values[slug] = nil
		members["profiles/values.json"], _ = json.Marshal(values)
		for index := range bom.Members {
			member := &bom.Members[index]
			member.Digest = contentDigest(members[member.Path])
			member.Size = int64(len(members[member.Path]))
		}
	})
	if _, _, err := ReadArchive(payload, release, digest, "builtins"); err == nil || blockedCategory(err) != "values" {
		t.Fatalf("signed null values accepted or untyped: %v", err)
	}
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Components[0].Values = nil
	if _, err := projectMirrors(t.Context(), catalog, &MirrorPlan{}, httpclient.DestinationPolicy{}); err == nil || blockedCategory(err) != "values" {
		t.Fatalf("nil mirror values were not rejected safely: %v", err)
	}
}
