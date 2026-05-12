package gitops

import (
	"errors"
	"testing"
)

func TestParser_AcceptsValidYAML(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: prod-east
spec:
  labels:
    tier: prod
    region: us-east
  template: prod-platform-baseline
  registries:
    - dockerhub-mirror
  toolPresets:
    - cert-manager-v1.14
  project: platform
`)
	doc, err := Parse(content, "clusters/prod-east.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Metadata.Name != "prod-east" {
		t.Fatalf("metadata.name = %q, want %q", doc.Metadata.Name, "prod-east")
	}
	if doc.Spec.Labels["tier"] != "prod" || doc.Spec.Labels["region"] != "us-east" {
		t.Fatalf("labels not parsed: %#v", doc.Spec.Labels)
	}
	if doc.Spec.Template != "prod-platform-baseline" {
		t.Fatalf("template = %q", doc.Spec.Template)
	}
	if len(doc.Spec.Registries) != 1 || doc.Spec.Registries[0] != "dockerhub-mirror" {
		t.Fatalf("registries = %#v", doc.Spec.Registries)
	}
	if len(doc.Spec.ToolPresets) != 1 || doc.Spec.ToolPresets[0] != "cert-manager-v1.14" {
		t.Fatalf("toolPresets = %#v", doc.Spec.ToolPresets)
	}
	if doc.Spec.Project != "platform" {
		t.Fatalf("project = %q", doc.Spec.Project)
	}
	if doc.RepoPath != "clusters/prod-east.yaml" {
		t.Fatalf("repoPath not propagated: %q", doc.RepoPath)
	}
}

func TestParser_RejectsBadAPIVersion(t *testing.T) {
	content := []byte(`
apiVersion: rancher.io/v1
kind: ClusterRegistration
metadata:
  name: prod-east
`)
	_, err := Parse(content, "clusters/prod-east.yaml")
	if !errors.Is(err, ErrWrongAPIVersion) {
		t.Fatalf("err = %v, want ErrWrongAPIVersion", err)
	}
	if !IsSkippable(err) {
		t.Fatalf("wrong apiVersion must be skippable so the sync ignores foreign docs")
	}
}

func TestParser_RejectsBadKind(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: Kustomization
metadata:
  name: prod-east
`)
	_, err := Parse(content, "clusters/prod-east.yaml")
	if !errors.Is(err, ErrWrongKind) {
		t.Fatalf("err = %v, want ErrWrongKind", err)
	}
	if !IsSkippable(err) {
		t.Fatalf("wrong kind must be skippable")
	}
}

func TestParser_RejectsMissingMetadataName(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata: {}
`)
	_, err := Parse(content, "clusters/anon.yaml")
	if !errors.Is(err, ErrMissingName) {
		t.Fatalf("err = %v, want ErrMissingName", err)
	}
	if IsSkippable(err) {
		t.Fatalf("missing name is operator intent and must NOT be skippable")
	}
}

func TestParser_RejectsMalformedYAML(t *testing.T) {
	content := []byte("apiVersion: astronomer.alphabravo.io/v1\nkind: : :::")
	_, err := Parse(content, "clusters/broken.yaml")
	if !errors.Is(err, ErrMalformedYAML) {
		t.Fatalf("err = %v, want ErrMalformedYAML", err)
	}
}

func TestParser_DefaultsNilMaps(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: bare
`)
	doc, err := Parse(content, "clusters/bare.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Spec.Labels == nil {
		t.Fatalf("labels should default to empty map, not nil")
	}
	if doc.Spec.Registries == nil {
		t.Fatalf("registries should default to empty slice")
	}
	if doc.Spec.ToolPresets == nil {
		t.Fatalf("toolPresets should default to empty slice")
	}
}
