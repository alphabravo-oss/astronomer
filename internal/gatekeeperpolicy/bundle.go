// Package gatekeeperpolicy embeds the starter Gatekeeper policy bundle and
// exposes it as apply-ready, cluster-scoped manifests. The bundle is the single
// source of truth: it is both committed for review (bundle/*.yaml) and applied
// automatically to clusters that have Gatekeeper installed.
package gatekeeperpolicy

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed bundle/*.yaml
var bundleFS embed.FS

// Manifest is one cluster-scoped Gatekeeper resource ready to Server-Side-Apply.
type Manifest struct {
	Group    string
	Version  string
	Resource string // CRD plural, for the API path
	Kind     string
	Name     string
	JSON     []byte // JSON-encoded object body (valid YAML, accepted by apply-patch)
}

// APIPath is the cluster-scoped SSA target path for this resource.
func (m Manifest) APIPath() string {
	return fmt.Sprintf("/apis/%s/%s/%s/%s", m.Group, m.Version, m.Resource, m.Name)
}

type rawMeta struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
}

// Manifests parses the embedded bundle in filename order (templates before
// their constraints, which matters because a constraint's CRD only exists once
// Gatekeeper has reconciled its template).
func Manifests() ([]Manifest, error) {
	entries, err := fs.Glob(bundleFS, "bundle/*.yaml")
	if err != nil {
		return nil, err
	}
	out := make([]Manifest, 0, len(entries)) // fs.Glob returns sorted order
	for _, e := range entries {
		raw, err := bundleFS.ReadFile(e)
		if err != nil {
			return nil, err
		}
		jsonBody, err := yaml.YAMLToJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e, err)
		}
		var meta rawMeta
		if err := yaml.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("%s: %w", e, err)
		}
		group, version := splitAPIVersion(meta.APIVersion)
		out = append(out, Manifest{
			Group:    group,
			Version:  version,
			Resource: resourceForKind(meta.Kind),
			Kind:     meta.Kind,
			Name:     meta.Metadata.Name,
			JSON:     jsonBody,
		})
	}
	return out, nil
}

func splitAPIVersion(av string) (group, version string) {
	if i := strings.LastIndex(av, "/"); i >= 0 {
		return av[:i], av[i+1:]
	}
	return "", av
}

// resourceForKind maps a Gatekeeper kind to its CRD plural. ConstraintTemplate
// is "constrainttemplates"; a constraint kind's CRD plural is the lowercased
// kind, which is the convention Gatekeeper uses when it generates the constraint
// CRD from a ConstraintTemplate.
func resourceForKind(kind string) string {
	if kind == "ConstraintTemplate" {
		return "constrainttemplates"
	}
	return strings.ToLower(kind)
}
