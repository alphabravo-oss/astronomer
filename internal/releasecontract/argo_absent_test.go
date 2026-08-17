package releasecontract

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Runtime Argo/Fleet identifiers must stay gone from the v1 product surface.
// Historical advisor-plans and archived docs may still mention the old stack.
var forbiddenRuntimeArgo = regexp.MustCompile(`(?i)(argocd|argo-cd|argoproj|/argocd)`)

func TestRuntimeHasNoArgoCD(t *testing.T) {
	root := findModuleRoot(t)
	roots := []string{
		filepath.Join(root, "cmd"),
		filepath.Join(root, "internal"),
		filepath.Join(root, "pkg"),
		filepath.Join(root, "deploy", "chart"),
		filepath.Join(root, "frontend", "src"),
	}
	skipDir := map[string]bool{
		"testdata":     true,
		"node_modules": true,
	}
	var hits []string
	for _, dir := range roots {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDir[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".go", ".ts", ".tsx", ".yaml", ".yml", ".json":
			default:
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if strings.Contains(rel, "releasecontract/argo_absent_test.go") {
				return nil
			}
			if strings.Contains(rel, "initial_schema_test.go") ||
				strings.Contains(rel, "catalog_oci_test.go") ||
				strings.Contains(rel, "chartrepo_test.go") ||
				strings.Contains(rel, "release_image_contract_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if forbiddenRuntimeArgo.Match(body) {
				hits = append(hits, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) != 0 {
		t.Fatalf("Argo/Fleet identifiers remain in runtime paths:\n  %s", strings.Join(hits, "\n  "))
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
