package gitops

import (
	"errors"
	"testing"
)

// TestParseAll_MultipleRegistrationsInOneFile is the core regression for
// the "silently drops all but the first document" bug: a file with several
// ClusterRegistration docs separated by `---` must yield one registration
// per doc, not just the first.
func TestParseAll_MultipleRegistrationsInOneFile(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: prod-east
spec:
  template: baseline
---
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: prod-west
---
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: prod-central
`)
	docs, err := ParseAll(content, "clusters/prod.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 registrations, got %d", len(docs))
	}
	names := map[string]bool{}
	for _, d := range docs {
		names[d.Metadata.Name] = true
		if d.RepoPath != "clusters/prod.yaml" {
			t.Fatalf("repoPath not propagated: %q", d.RepoPath)
		}
	}
	for _, want := range []string{"prod-east", "prod-west", "prod-central"} {
		if !names[want] {
			t.Fatalf("missing registration %q; got %v", want, names)
		}
	}
}

// TestParseAll_SkipsNonRegistrationLeadingDoc covers the second half of the
// bug: a file whose FIRST doc is an unrelated resource (a Namespace)
// followed by a ClusterRegistration must still register the cluster instead
// of skipping the whole file on the leading apiVersion mismatch.
func TestParseAll_SkipsNonRegistrationLeadingDoc(t *testing.T) {
	content := []byte(`
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
---
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: trailing-cluster
`)
	docs, err := ParseAll(content, "clusters/mixed.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 registration after skipping the Namespace, got %d", len(docs))
	}
	if docs[0].Metadata.Name != "trailing-cluster" {
		t.Fatalf("wrong registration parsed: %q", docs[0].Metadata.Name)
	}
}

// TestParseAll_AllForeignDocsYieldEmpty confirms a file with zero
// ClusterRegistration docs returns an empty slice + nil error (the walker
// treats it as skip-with-log, same as the old IsSkippable path).
func TestParseAll_AllForeignDocsYieldEmpty(t *testing.T) {
	content := []byte(`
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - a.yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
`)
	docs, err := ParseAll(content, "kustomization.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 registrations, got %d", len(docs))
	}
}

// TestParseAll_MatchingDocMissingNameIsError ensures operator intent to
// register (matching apiVersion+kind) with an empty metadata.name is
// surfaced, not silently skipped.
func TestParseAll_MatchingDocMissingNameIsError(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata: {}
`)
	_, err := ParseAll(content, "clusters/anon.yaml")
	if !errors.Is(err, ErrMissingName) {
		t.Fatalf("err = %v, want ErrMissingName", err)
	}
}

// TestParseAll_MalformedYAMLIsError ensures a broken document is surfaced
// rather than swallowed.
func TestParseAll_MalformedYAMLIsError(t *testing.T) {
	content := []byte("apiVersion: astronomer.alphabravo.io/v1\nkind: : :::")
	_, err := ParseAll(content, "clusters/broken.yaml")
	if !errors.Is(err, ErrMalformedYAML) {
		t.Fatalf("err = %v, want ErrMalformedYAML", err)
	}
}

// TestParseAll_IgnoresEmptyDocuments confirms a trailing `---` / blank doc
// does not produce a spurious registration or error.
func TestParseAll_IgnoresEmptyDocuments(t *testing.T) {
	content := []byte(`
apiVersion: astronomer.alphabravo.io/v1
kind: ClusterRegistration
metadata:
  name: only-one
---
`)
	docs, err := ParseAll(content, "clusters/one.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(docs))
	}
}
