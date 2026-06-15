// Package rbac — built-in role-templates catalog (T1.1).
//
// Role templates are read-only, pre-authored rule sets that an operator
// can apply to a project, cluster, or global scope. Rancher ships ~20
// of these and it is the single most-cited buyer gap in comparison.md.
//
// Storage model: templates live as YAML files under
// internal/rbac/templates/*.yaml and are embedded into the binary via
// embed. They are NOT persisted to the DB — applying a template
// creates real cluster_roles / project_roles / cluster_role_bindings
// rows from the embedded definition, so the live state stays in
// Postgres while the catalog stays version-controlled.
//
// Loader: LoadCatalog parses every embedded YAML once at startup. The
// returned Catalog is immutable and indexed by template name. A
// duplicate-name or schema-invalid template panics at boot so a bad
// template never silently degrades the catalog at runtime.
//
// Verified contract:
//   - Each template has a stable `name` (kebab-case slug, used as the
//     URL path segment and DB lookup key).
//   - `scope` ∈ {global, cluster, project}; the apply endpoint will
//     refuse to apply a template at the wrong scope.
//   - `rules` parses into []Rule (same shape persisted by cluster_roles).
package rbac

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"gopkg.in/yaml.v3"
)

//go:embed templates/*.yaml
var embeddedTemplates embed.FS

// Scope enumerates the binding scopes a template can target.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeCluster Scope = "cluster"
	ScopeProject Scope = "project"
)

// IsValidScope returns true when s is one of the three recognised
// binding scopes.
func IsValidScope(s Scope) bool {
	switch s {
	case ScopeGlobal, ScopeCluster, ScopeProject:
		return true
	}
	return false
}

// Template is the parsed representation of one templates/*.yaml file.
// Field tags use yaml; the same struct is rendered to JSON by the
// handler (default lowercase field names match the existing handler
// conventions).
type Template struct {
	Name          string   `yaml:"name" json:"name"`
	DisplayName   string   `yaml:"display_name" json:"display_name"`
	Description   string   `yaml:"description" json:"description"`
	Scope         Scope    `yaml:"scope" json:"scope"`
	RiskLevel     string   `yaml:"risk_level" json:"risk_level"`
	Inherits      []string `yaml:"inherits" json:"inherits,omitempty"`
	SystemManaged bool     `yaml:"system_managed" json:"system_managed"`
	// Category is a free-form tag (admin/project/cluster/audit/support)
	// the frontend uses for grouping. Not load-bearing for permission
	// checks.
	Category string `yaml:"category" json:"category"`
	Rules    []Rule `yaml:"rules" json:"rules"`
}

// Catalog is the immutable, indexed registry of templates loaded at
// startup. Index lookups are O(1) by name; the ordered list is stable
// for deterministic API output.
type Catalog struct {
	byName  map[string]Template
	ordered []Template
}

// All returns every template in stable order: scope groups first
// (global → cluster → project), then alphabetically within each group.
// The stable order is part of the public API surface so frontend
// snapshot tests don't churn on reorderings.
func (c *Catalog) All() []Template {
	if c == nil {
		return nil
	}
	out := make([]Template, len(c.ordered))
	copy(out, c.ordered)
	return out
}

// Get returns the template by name. Second return is false when no
// such template exists.
func (c *Catalog) Get(name string) (Template, bool) {
	if c == nil {
		return Template{}, false
	}
	t, ok := c.byName[name]
	return t, ok
}

// Count returns the number of templates loaded.
func (c *Catalog) Count() int {
	if c == nil {
		return 0
	}
	return len(c.ordered)
}

// LoadCatalog parses every templates/*.yaml under the embedded
// filesystem. Returns an error on duplicate names, unknown scopes, or
// empty rule lists. Callers wire this at startup; a returned error is
// considered fatal so a busted template can't ship to production
// silently.
func LoadCatalog() (*Catalog, error) {
	return loadCatalogFrom(embeddedTemplates, "templates")
}

// loadCatalogFrom is the testable inner loader; LoadCatalog provides
// the production embed.FS but unit tests can swap in a fixture FS to
// pin error-path behavior.
func loadCatalogFrom(fsys fs.FS, dir string) (*Catalog, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read templates dir %q: %w", dir, err)
	}
	cat := &Catalog{byName: map[string]Template{}}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Skip anything that isn't a *.yaml — defensive against an
		// editor swap-file landing in the embed.
		if name := e.Name(); len(name) < 5 || name[len(name)-5:] != ".yaml" {
			continue
		}
		path := dir + "/" + e.Name()
		data, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			return nil, fmt.Errorf("read %s: %w", path, rerr)
		}
		var t Template
		if uerr := yaml.Unmarshal(data, &t); uerr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, uerr)
		}
		if t.Name == "" {
			return nil, fmt.Errorf("%s: name is required", path)
		}
		if !IsValidScope(t.Scope) {
			return nil, fmt.Errorf("%s: invalid scope %q (want global|cluster|project)", path, t.Scope)
		}
		if t.RiskLevel == "" {
			t.RiskLevel = inferRiskLevel(t.Rules)
		}
		if !isValidRiskLevel(t.RiskLevel) {
			return nil, fmt.Errorf("%s: invalid risk_level %q (want low|medium|high|critical)", path, t.RiskLevel)
		}
		// Embedded templates are platform-owned by default. If/when we add
		// user-authored templates, they should flow through a separate storage
		// path instead of this version-controlled catalog.
		t.SystemManaged = true
		if len(t.Rules) == 0 {
			return nil, fmt.Errorf("%s: rules must contain at least one entry", path)
		}
		for i, rule := range t.Rules {
			if rule.Resource == "" {
				return nil, fmt.Errorf("%s: rules[%d].resource is required", path, i)
			}
			if len(rule.Verbs) == 0 {
				return nil, fmt.Errorf("%s: rules[%d].verbs must contain at least one entry", path, i)
			}
		}
		if _, dup := cat.byName[t.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate template name %q", path, t.Name)
		}
		cat.byName[t.Name] = t
	}
	if len(cat.byName) == 0 {
		return nil, fmt.Errorf("no templates loaded from %q", dir)
	}
	cat.ordered = make([]Template, 0, len(cat.byName))
	for _, t := range cat.byName {
		cat.ordered = append(cat.ordered, t)
	}
	sort.SliceStable(cat.ordered, func(i, j int) bool {
		// Scope ordering: global < cluster < project. Inside each
		// group, alphabetical by name.
		if cat.ordered[i].Scope != cat.ordered[j].Scope {
			return scopeOrder(cat.ordered[i].Scope) < scopeOrder(cat.ordered[j].Scope)
		}
		return cat.ordered[i].Name < cat.ordered[j].Name
	})
	return cat, nil
}

func isValidRiskLevel(level string) bool {
	switch level {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

// RiskLevelForRules returns the highest-risk category implied by a ruleset.
// It is used by both the embedded catalog loader and permission-preview APIs.
func RiskLevelForRules(rules []Rule) string {
	return inferRiskLevel(rules)
}

func inferRiskLevel(rules []Rule) string {
	highest := "low"
	for _, rule := range rules {
		for _, verb := range rule.Verbs {
			if rule.Resource == "*" || verb == "*" {
				return "critical"
			}
			if rule.Resource == string(ResourceSecrets) && (verb == string(VerbRead) || verb == string(VerbList)) {
				highest = maxRisk(highest, "critical")
				continue
			}
			if rule.Resource == string(ResourceBackups) && verb == string(VerbManage) {
				highest = maxRisk(highest, "critical")
				continue
			}
			if rule.Resource == string(ResourceNodes) && verb == string(VerbManage) {
				highest = maxRisk(highest, "critical")
				continue
			}
			switch verb {
			case string(VerbDelete), string(VerbManage), string(VerbExec), string(VerbProxy), string(VerbSync):
				highest = maxRisk(highest, "high")
			case string(VerbCreate), string(VerbUpdate), string(VerbScale), string(VerbRestart):
				highest = maxRisk(highest, "medium")
			}
		}
	}
	return highest
}

func maxRisk(a, b string) string {
	if riskRank(b) > riskRank(a) {
		return b
	}
	return a
}

func riskRank(level string) int {
	switch level {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	default:
		return 0
	}
}

func scopeOrder(s Scope) int {
	switch s {
	case ScopeGlobal:
		return 0
	case ScopeCluster:
		return 1
	case ScopeProject:
		return 2
	}
	return 99
}
