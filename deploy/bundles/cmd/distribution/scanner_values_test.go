package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"io"
	"testing"
)

func TestActualRendererRejectsAmbiguousScannerValues(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	component := catalog.Components[0]
	image := "registry.example.test/scanner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	chart := testChart(t, component, "{{ if .Values.operator.vulnerabilityScannerEnabled }}apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n{{ end }}")
	for _, values := range []map[string]any{
		{"image": image, "operator": map[string]any{"vulnerabilityScannerEnabled": "true"}},
		{"image": image, "operator": map[string]any{"vulnerabilityScannerEnabled": "false"}},
		{"image": image, "operator": "nonobject"},
		{"image": image, "operator": map[string]any{"vulnerabilityScannerEnabled": true}, "trivy": "nonobject"},
		{"image": image, "operator": map[string]any{"vulnerabilityScannerEnabled": true}, "trivy": map[string]any{"dbRepository": true}},
		{"image": image, "operator": map[string]any{"vulnerabilityScannerEnabled": nil}},
	} {
		if _, err := renderReviewedChart(chart, values, "qa", "qa", "1.35.0"); err == nil {
			t.Fatal("actual renderer accepted malformed owned scanner configuration")
		}
	}
	values := map[string]any{"image": image, "operator": map[string]any{"vulnerabilityScannerEnabled": false}, "customReviewedAdditiveValue": true}
	if _, err := renderReviewedChart(chart, values, "qa", "qa", "1.35.0"); err != nil {
		t.Fatalf("typed false/additive value rejected: %v", err)
	}
}

func TestActualRendererHonorsReviewedChartValuesSchema(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	component := catalog.Components[1] // independent chart, no owned scanner special case
	original := testChart(t, component, "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - image: {{ .Values.image }}\n")
	input, err := gzip.NewReader(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reader := tar.NewReader(input)
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	writer := tar.NewWriter(compressed)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(data)
	}
	schema := []byte(`{"type":"object","additionalProperties":true,"properties":{"reviewedInteger":{"type":"integer"}}}`)
	if err := writer.WriteHeader(&tar.Header{Name: component.Source.Chart + "/values.schema.json", Typeflag: tar.TypeReg, Size: int64(len(schema)), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write(schema)
	_ = writer.Close()
	_ = compressed.Close()
	values := map[string]any{"image": "registry.example.test/workload@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "reviewedInteger": "wrong-type"}
	// ToRenderValues performs recursive ValidateAgainstSchema in the pinned Helm;
	// this fixture verifies that actual path without duplicate validation.
	if _, err := renderReviewedChart(output.Bytes(), values, "qa", "qa", "1.35.0"); err == nil {
		t.Fatal("actual render path skipped chart schema")
	}
	values["reviewedInteger"] = 1
	values["reviewedAdditiveValue"] = true
	if _, err := renderReviewedChart(output.Bytes(), values, "qa", "qa", "1.35.0"); err != nil {
		t.Fatalf("valid/additive chart values rejected: %v", err)
	}
}
