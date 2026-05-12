// Package gitops parses ClusterRegistration YAML documents (the
// astronomer.alphabravo.io/v1 Kind=ClusterRegistration shape) committed to
// operator-managed Git repos. The sync worker in
// internal/worker/tasks/gitops_sync.go pulls a repo, walks its
// .yaml/.yml files, hands each one to Parse, and reconciles the parsed
// doc into a clusters row + supporting attachments (template, registries,
// labels, project).
//
// The parser is intentionally strict:
//
//   - apiVersion MUST be exactly astronomer.alphabravo.io/v1.
//   - kind MUST be exactly ClusterRegistration.
//   - metadata.name MUST be a non-empty RFC-1123 cluster name (validation
//     is delegated to the caller via the existing validClusterName helper;
//     Parse only enforces non-emptiness).
//
// Anything that doesn't match is rejected with a typed error so the sync
// worker can log-and-skip non-ClusterRegistration YAML (e.g. a
// kustomization.yaml left in the tree) without failing the whole sync.
package gitops

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// APIVersion is the only apiVersion the parser accepts.
const APIVersion = "astronomer.alphabravo.io/v1"

// Kind is the only kind the parser accepts.
const Kind = "ClusterRegistration"

// ClusterRegistration is the parsed YAML doc. RepoPath is set by Parse so
// callers can carry source-of-truth provenance into the
// gitops_registered_clusters row without a second round-trip.
type ClusterRegistration struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Labels      map[string]string `yaml:"labels"`
		Template    string            `yaml:"template"`
		Registries  []string          `yaml:"registries"`
		ToolPresets []string          `yaml:"toolPresets"`
		Project     string            `yaml:"project"`
	} `yaml:"spec"`

	// RepoPath is the source-of-truth path inside the repo (relative to
	// the repo root, not the path_prefix). Set by Parse; ignored on
	// unmarshal.
	RepoPath string `yaml:"-"`
}

// Errors --------------------------------------------------------------

// ErrWrongAPIVersion is returned when the doc's apiVersion is not the
// expected APIVersion constant.
var ErrWrongAPIVersion = errors.New("gitops: unexpected apiVersion")

// ErrWrongKind is returned when the doc's kind is not the expected Kind
// constant.
var ErrWrongKind = errors.New("gitops: unexpected kind")

// ErrMissingName is returned when metadata.name is empty.
var ErrMissingName = errors.New("gitops: metadata.name is required")

// ErrMalformedYAML is returned when the bytes do not parse as YAML.
var ErrMalformedYAML = errors.New("gitops: malformed YAML")

// Parse decodes a single ClusterRegistration doc.
//
// repoPath is the file's path inside the repo (relative to the repo
// root). It's stored on the returned struct for downstream provenance
// but isn't validated here — callers may pass "" for unit tests.
func Parse(content []byte, repoPath string) (ClusterRegistration, error) {
	var doc ClusterRegistration
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return ClusterRegistration{}, fmt.Errorf("%w: %v", ErrMalformedYAML, err)
	}
	if doc.APIVersion != APIVersion {
		return ClusterRegistration{}, fmt.Errorf("%w: %q (want %q)", ErrWrongAPIVersion, doc.APIVersion, APIVersion)
	}
	if doc.Kind != Kind {
		return ClusterRegistration{}, fmt.Errorf("%w: %q (want %q)", ErrWrongKind, doc.Kind, Kind)
	}
	if doc.Metadata.Name == "" {
		return ClusterRegistration{}, ErrMissingName
	}
	if doc.Spec.Labels == nil {
		doc.Spec.Labels = map[string]string{}
	}
	if doc.Spec.Registries == nil {
		doc.Spec.Registries = []string{}
	}
	if doc.Spec.ToolPresets == nil {
		doc.Spec.ToolPresets = []string{}
	}
	doc.RepoPath = repoPath
	return doc, nil
}

// IsSkippable reports whether err means the file simply wasn't a
// ClusterRegistration (so the sync worker can skip-with-log rather than
// failing the tick). Malformed YAML and missing-name are NOT skippable —
// those indicate operator intent to register and need to be surfaced.
func IsSkippable(err error) bool {
	return errors.Is(err, ErrWrongAPIVersion) || errors.Is(err, ErrWrongKind)
}
