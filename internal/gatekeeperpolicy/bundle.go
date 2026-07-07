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

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

// kindPattern is the set of characters allowed in a Kubernetes Kind. Kinds are
// PascalCase alphanumerics; anything else in an authored manifest is rejected so
// it can never reach the SSA API path (defense-in-depth against path injection).
func isValidKind(kind string) bool {
	if kind == "" {
		return false
	}
	for _, r := range kind {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

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

// The two Gatekeeper API groups an authored resource may belong to: a
// ConstraintTemplate under templates.gatekeeper.sh, and every Constraint kind
// under constraints.gatekeeper.sh (the CRD group Gatekeeper generates).
const (
	constraintTemplateGroup = "templates.gatekeeper.sh"
	constraintGroup         = "constraints.gatekeeper.sh"
)

// IsConstraintTemplate reports whether this manifest is a Gatekeeper
// ConstraintTemplate (as opposed to a Constraint instance).
func (m Manifest) IsConstraintTemplate() bool {
	return m.Kind == "ConstraintTemplate" && m.Group == constraintTemplateGroup
}

// ParseManifest parses a single authored ConstraintTemplate or Constraint YAML
// document into an apply-ready Manifest, reusing the same apiVersion/kind →
// resource mapping the embedded bundle uses. It fails when the document is not
// a Gatekeeper ConstraintTemplate or Constraint, or is missing kind /
// apiVersion / metadata.name. It does NOT validate embedded Rego — the handler
// layers that on top for ConstraintTemplates.
func ParseManifest(raw []byte) (Manifest, error) {
	jsonBody, err := yaml.YAMLToJSON(raw)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse yaml: %w", err)
	}
	var meta rawMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return Manifest{}, fmt.Errorf("parse metadata: %w", err)
	}
	if strings.TrimSpace(meta.Kind) == "" {
		return Manifest{}, fmt.Errorf("missing kind")
	}
	if strings.TrimSpace(meta.APIVersion) == "" {
		return Manifest{}, fmt.Errorf("missing apiVersion")
	}
	if strings.TrimSpace(meta.Metadata.Name) == "" {
		return Manifest{}, fmt.Errorf("missing metadata.name")
	}
	// The name and kind are interpolated into the cluster-scoped SSA API path
	// (APIPath), so constrain them to their valid Kubernetes shapes before use —
	// a name/kind with path characters must never reach the apply/delete request.
	if errs := k8svalidation.IsDNS1123Subdomain(strings.TrimSpace(meta.Metadata.Name)); len(errs) > 0 {
		return Manifest{}, fmt.Errorf("invalid metadata.name %q: %s", meta.Metadata.Name, strings.Join(errs, "; "))
	}
	if !isValidKind(strings.TrimSpace(meta.Kind)) {
		return Manifest{}, fmt.Errorf("invalid kind %q", meta.Kind)
	}
	group, version := splitAPIVersion(meta.APIVersion)
	if version == "" {
		return Manifest{}, fmt.Errorf("invalid apiVersion %q", meta.APIVersion)
	}
	if !isGatekeeperManifest(group, meta.Kind) {
		return Manifest{}, fmt.Errorf("apiVersion %q kind %q is not a Gatekeeper ConstraintTemplate or Constraint", meta.APIVersion, meta.Kind)
	}
	return Manifest{
		Group:    group,
		Version:  version,
		Resource: resourceForKind(meta.Kind),
		Kind:     meta.Kind,
		Name:     meta.Metadata.Name,
		JSON:     jsonBody,
	}, nil
}

// isGatekeeperManifest reports whether (group, kind) identifies a Gatekeeper
// ConstraintTemplate or a Constraint (which lives under constraints.gatekeeper.sh).
func isGatekeeperManifest(group, kind string) bool {
	if kind == "ConstraintTemplate" && group == constraintTemplateGroup {
		return true
	}
	return group == constraintGroup
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
