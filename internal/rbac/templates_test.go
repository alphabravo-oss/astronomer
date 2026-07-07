package rbac

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoadCatalog_Embedded asserts the shipped catalog loads cleanly,
// contains the Rancher-grade day-2 role template set, and has stable
// ordering. This is the boot-time contract for the operator-facing
// role-template catalog.
func TestLoadCatalog_Embedded(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	want := []string{
		"audit-viewer",
		"backup-operator",
		"catalog-admin",
		"compliance-auditor",
		"compliance-manager",
		"gitops-admin",
		"gitops-viewer",
		"logging-viewer",
		"monitoring-admin",
		"monitoring-viewer",
		"platform-admin",
		"platform-operator",
		"restore-operator",
		"security-auditor",
		"support-bundle-operator",
		"support-engineer",
		"catalog-installer",
		"cluster-backup-operator",
		"cluster-member",
		"cluster-operator",
		"cluster-owner",
		"cluster-viewer",
		"node-operator",
		"service-mesh-operator",
		"storage-manager",
		"config-manager",
		"gitops-deployer",
		"namespace-operator",
		"project-member",
		"project-owner",
		"project-viewer",
		"secret-manager",
		"service-ingress-manager",
		"workload-deployer",
		"workload-viewer",
	}
	if cat.Count() != len(want) {
		t.Fatalf("Count() = %d, want %d", cat.Count(), len(want))
	}
	// Verify every expected template is present (order checked by the
	// next assertion).
	for _, name := range want {
		if _, ok := cat.Get(name); !ok {
			t.Errorf("template %q missing from catalog", name)
		}
	}
	all := cat.All()
	// Stable order: global → cluster → project, alphabetical within.
	expectedOrder := []string{
		// global, alpha
		"audit-viewer",
		"backup-operator",
		"catalog-admin",
		"compliance-auditor",
		"compliance-manager",
		"gitops-admin",
		"gitops-viewer",
		"logging-viewer",
		"monitoring-admin",
		"monitoring-viewer",
		"platform-admin",
		"platform-operator",
		"restore-operator",
		"security-auditor",
		"support-bundle-operator",
		"support-engineer",
		// cluster, alpha
		"catalog-installer",
		"cluster-backup-operator",
		"cluster-member",
		"cluster-operator",
		"cluster-owner",
		"cluster-viewer",
		"node-operator",
		"service-mesh-operator",
		"storage-manager",
		// project, alpha
		"config-manager",
		"gitops-deployer",
		"namespace-operator",
		"project-member",
		"project-owner",
		"project-viewer",
		"secret-manager",
		"service-ingress-manager",
		"workload-deployer",
		"workload-viewer",
	}
	if len(all) != len(expectedOrder) {
		t.Fatalf("All() len = %d, want %d", len(all), len(expectedOrder))
	}
	for i, name := range expectedOrder {
		if all[i].Name != name {
			t.Errorf("ordered[%d] = %q, want %q (full order: %v)", i, all[i].Name, name, namesOf(all))
		}
	}
}

func TestLoadCatalog_TemplateMetadata(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	for _, tmpl := range cat.All() {
		if tmpl.RiskLevel == "" {
			t.Fatalf("%s risk_level empty", tmpl.Name)
		}
		if !isValidRiskLevel(tmpl.RiskLevel) {
			t.Fatalf("%s risk_level = %q", tmpl.Name, tmpl.RiskLevel)
		}
		if !tmpl.SystemManaged {
			t.Fatalf("%s SystemManaged = false, want true", tmpl.Name)
		}
	}
	secretManager, ok := cat.Get("secret-manager")
	if !ok {
		t.Fatal("secret-manager missing")
	}
	if secretManager.RiskLevel != "critical" {
		t.Fatalf("secret-manager risk_level = %q, want critical", secretManager.RiskLevel)
	}
}

// TestCustomResources_TemplateGrants asserts the built-in role templates
// gate CRD/CR proxy access via the dedicated custom_resources resource
// (F2 follow-up): viewer/read-only roles get read+list, operator roles get
// the write verbs, and the wildcard admin templates cover it implicitly.
func TestCustomResources_TemplateGrants(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	verbsFor := func(tmplName string) []string {
		tmpl, ok := cat.Get(tmplName)
		if !ok {
			t.Fatalf("template %q missing", tmplName)
		}
		for _, r := range tmpl.Rules {
			if r.Resource == string(ResourceCustomResources) {
				return r.Verbs
			}
			if r.Resource == string(ResourceWildcard) {
				return r.Verbs // wildcard admin: covers custom_resources implicitly
			}
		}
		t.Fatalf("template %q has no custom_resources (or wildcard) grant", tmplName)
		return nil
	}
	has := func(verbs []string, want Verb) bool {
		for _, v := range verbs {
			if v == string(want) || v == string(VerbWildcard) {
				return true
			}
		}
		return false
	}

	// Viewer/read-only roles: read + list, but NOT write. platform-operator is
	// read-only here too — it is read-only on built-in workloads/services, so
	// global-scope write across every cluster's custom resources would exceed
	// its non-destructive day-2 posture.
	for _, name := range []string{"cluster-viewer", "project-viewer", "platform-operator"} {
		verbs := verbsFor(name)
		if !has(verbs, VerbRead) || !has(verbs, VerbList) {
			t.Errorf("%s custom_resources verbs = %v, want read+list", name, verbs)
		}
		if has(verbs, VerbCreate) || has(verbs, VerbUpdate) || has(verbs, VerbDelete) {
			t.Errorf("%s custom_resources verbs = %v, must not include write verbs", name, verbs)
		}
	}

	// Operator + admin roles: write verbs (create/update/delete).
	for _, name := range []string{"cluster-operator", "cluster-owner", "platform-admin"} {
		verbs := verbsFor(name)
		if !has(verbs, VerbCreate) || !has(verbs, VerbUpdate) || !has(verbs, VerbDelete) {
			t.Errorf("%s custom_resources verbs = %v, want create+update+delete", name, verbs)
		}
		if !has(verbs, VerbRead) || !has(verbs, VerbList) {
			t.Errorf("%s custom_resources verbs = %v, want read+list too", name, verbs)
		}
	}
}

// TestResolveInherited_TransitiveUnion verifies a template's effective grants
// are the flattened union of its own rules plus every rule reachable through
// its Inherits chain (transitively), with direct grants marked direct and
// inherited grants attributed to the template that declared them.
func TestResolveInherited_TransitiveUnion(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/base.yaml": &fstest.MapFile{Data: []byte(`name: base-viewer
scope: project
rules:
  - resource: pods
    verbs: [read, list]
`)},
		"templates/mid.yaml": &fstest.MapFile{Data: []byte(`name: mid-operator
scope: project
inherits: [base-viewer]
rules:
  - resource: workloads
    verbs: [create]
`)},
		"templates/top.yaml": &fstest.MapFile{Data: []byte(`name: top-admin
scope: project
inherits: [mid-operator]
rules:
  - resource: workloads
    verbs: [delete]
`)},
	}
	cat, err := loadCatalogFrom(fsys, "templates")
	if err != nil {
		t.Fatalf("loadCatalogFrom: %v", err)
	}
	top, ok := cat.Get("top-admin")
	if !ok {
		t.Fatal("top-admin missing")
	}
	// Expected flattened set: workloads:delete (direct), workloads:create
	// (inherited transitively from mid-operator), pods:read + pods:list
	// (inherited transitively from base-viewer).
	type key struct{ resource, verb string }
	got := map[key]EffectiveGrant{}
	for _, g := range top.EffectiveGrants() {
		got[key{g.Resource, g.Verb}] = g
	}
	if len(got) != 4 {
		t.Fatalf("effective grants = %d (%+v), want 4", len(got), top.EffectiveGrants())
	}
	if g := got[key{"workloads", "delete"}]; g.Inherited {
		t.Errorf("workloads:delete should be direct, got %+v", g)
	}
	if g := got[key{"workloads", "create"}]; !g.Inherited || g.InheritedFrom != "mid-operator" {
		t.Errorf("workloads:create should be inherited from mid-operator, got %+v", g)
	}
	for _, verb := range []string{"read", "list"} {
		if g := got[key{"pods", verb}]; !g.Inherited || g.InheritedFrom != "base-viewer" {
			t.Errorf("pods:%s should be inherited from base-viewer, got %+v", verb, g)
		}
	}
	// EffectiveRules is the flattened rule shape used for risk/preview.
	var podsVerbs []string
	for _, r := range top.EffectiveRules() {
		if r.Resource == "pods" {
			podsVerbs = r.Verbs
		}
	}
	if len(podsVerbs) != 2 {
		t.Errorf("EffectiveRules pods verbs = %v, want read+list", podsVerbs)
	}
}

// TestResolveInherited_DirectWinsOverInherited verifies a permission a template
// declares directly is reported as direct even when an inherited template also
// grants it.
func TestResolveInherited_DirectWinsOverInherited(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/base.yaml": &fstest.MapFile{Data: []byte(`name: base
scope: cluster
rules:
  - resource: pods
    verbs: [read]
`)},
		"templates/child.yaml": &fstest.MapFile{Data: []byte(`name: child
scope: cluster
inherits: [base]
rules:
  - resource: pods
    verbs: [read]
`)},
	}
	cat, err := loadCatalogFrom(fsys, "templates")
	if err != nil {
		t.Fatalf("loadCatalogFrom: %v", err)
	}
	child, _ := cat.Get("child")
	grants := child.EffectiveGrants()
	if len(grants) != 1 {
		t.Fatalf("grants = %+v, want a single deduped pods:read", grants)
	}
	if grants[0].Inherited {
		t.Errorf("pods:read should be direct (direct wins over inherited), got %+v", grants[0])
	}
}

// TestResolveInherited_RejectsCycle verifies an inheritance cycle is a hard
// load error rather than an infinite loop.
func TestResolveInherited_RejectsCycle(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/a.yaml": &fstest.MapFile{Data: []byte(`name: a
scope: project
inherits: [b]
rules:
  - resource: pods
    verbs: [read]
`)},
		"templates/b.yaml": &fstest.MapFile{Data: []byte(`name: b
scope: project
inherits: [a]
rules:
  - resource: workloads
    verbs: [read]
`)},
	}
	_, err := loadCatalogFrom(fsys, "templates")
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

// TestResolveInherited_RejectsScopeMismatch verifies inheriting a template of a
// different scope fails closed.
func TestResolveInherited_RejectsScopeMismatch(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/g.yaml": &fstest.MapFile{Data: []byte(`name: global-base
scope: global
rules:
  - resource: pods
    verbs: [read]
`)},
		"templates/p.yaml": &fstest.MapFile{Data: []byte(`name: project-child
scope: project
inherits: [global-base]
rules:
  - resource: workloads
    verbs: [read]
`)},
	}
	_, err := loadCatalogFrom(fsys, "templates")
	if err == nil || !strings.Contains(err.Error(), "incompatible scope") {
		t.Fatalf("expected scope mismatch error, got %v", err)
	}
}

// TestResolveInherited_RejectsUnknown verifies inheriting a name that resolves
// to no template fails closed.
func TestResolveInherited_RejectsUnknown(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/c.yaml": &fstest.MapFile{Data: []byte(`name: child
scope: project
inherits: [ghost]
rules:
  - resource: pods
    verbs: [read]
`)},
	}
	_, err := loadCatalogFrom(fsys, "templates")
	if err == nil || !strings.Contains(err.Error(), "unknown template") {
		t.Fatalf("expected unknown-template error, got %v", err)
	}
}

func namesOf(ts []Template) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

// TestLoadCatalog_RejectsDuplicates verifies the loader fails fast
// when two templates share a name — preventing the silent-shadow bug
// that would otherwise let one accidental rename swallow another.
func TestLoadCatalog_RejectsDuplicates(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/a.yaml": &fstest.MapFile{Data: []byte(`name: dupe
scope: global
rules:
  - resource: "*"
    verbs: ["read"]
`)},
		"templates/b.yaml": &fstest.MapFile{Data: []byte(`name: dupe
scope: cluster
rules:
  - resource: "*"
    verbs: ["read"]
`)},
	}
	_, err := loadCatalogFrom(fsys, "templates")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

// TestLoadCatalog_RejectsInvalidScope verifies the loader fails fast
// when a template declares a scope the engine cannot route — so a
// typo in scope ("globle") becomes a boot failure, not a silently-
// inapplicable template.
func TestLoadCatalog_RejectsInvalidScope(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/x.yaml": &fstest.MapFile{Data: []byte(`name: x
scope: globle
rules:
  - resource: "*"
    verbs: ["read"]
`)},
	}
	_, err := loadCatalogFrom(fsys, "templates")
	if err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Fatalf("expected invalid scope error, got %v", err)
	}
}

// TestLoadCatalog_RejectsEmptyRules verifies the loader refuses a
// template with no rules — that's almost certainly a copy-paste
// mistake and the resulting role would be a privilege no-op anyway.
func TestLoadCatalog_RejectsEmptyRules(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/x.yaml": &fstest.MapFile{Data: []byte(`name: x
scope: global
rules: []
`)},
	}
	_, err := loadCatalogFrom(fsys, "templates")
	if err == nil || !strings.Contains(err.Error(), "at least one entry") {
		t.Fatalf("expected empty rules error, got %v", err)
	}
}

// TestPlatformAdmin_HasWildcards is a snapshot test on the most
// privileged template. We pin the wildcard resource+verb so a future
// edit that accidentally narrows it (e.g. swapping "*" for an
// enumerated list that drops a new resource) gets caught.
func TestPlatformAdmin_HasWildcards(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	pa, ok := cat.Get("platform-admin")
	if !ok {
		t.Fatalf("platform-admin missing")
	}
	if len(pa.Rules) != 1 {
		t.Fatalf("platform-admin rules len = %d, want 1", len(pa.Rules))
	}
	if pa.Rules[0].Resource != "*" {
		t.Errorf("platform-admin rules[0].Resource = %q, want *", pa.Rules[0].Resource)
	}
	if len(pa.Rules[0].Verbs) != 1 || pa.Rules[0].Verbs[0] != "*" {
		t.Errorf("platform-admin rules[0].Verbs = %v, want [*]", pa.Rules[0].Verbs)
	}
}
