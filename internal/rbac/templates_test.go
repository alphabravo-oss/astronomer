package rbac

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoadCatalog_Embedded asserts the shipped catalog loads cleanly,
// contains all 8 expected templates, and has stable ordering. This is
// the boot-time contract from T1.1 acceptance criteria:
// "All 8 templates load at startup without panic" and
// "GET /rbac/templates/ returns 8 entries with stable order".
func TestLoadCatalog_Embedded(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	want := []string{
		"platform-admin",
		"compliance-auditor",
		"support-engineer",
		"cluster-operator",
		"cluster-viewer",
		"project-member",
		"project-owner",
		"project-viewer",
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
		"compliance-auditor",
		"platform-admin",
		"support-engineer",
		// cluster, alpha
		"cluster-operator",
		"cluster-viewer",
		// project, alpha
		"project-member",
		"project-owner",
		"project-viewer",
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
