package contract

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPinnedContractChecksumsAndVersions(t *testing.T) {
	manifest, err := os.ReadFile("checksums.sha256")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksums(".", manifest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("pin.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin map[string]any
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}
	wants := map[string]any{
		"bridge_protocol":                  "bridge/v1",
		"bridge_openapi_version":           "1.0.0",
		"agent_protocol_version":           "1.0.0",
		"agent_chart_version":              "1.0.30",
		"minimum_central_api_version":      "1.0.0",
		"direct_central_transport_allowed": false,
	}
	for field, want := range wants {
		if pin[field] != want {
			t.Errorf("pin %s = %v, want %v", field, pin[field], want)
		}
	}
	if AgentChartVersion != wants["agent_chart_version"] {
		t.Errorf("compiled AgentChartVersion = %q, want pinned %q", AgentChartVersion, wants["agent_chart_version"])
	}
}

func TestPinnedContractDriftIsRejected(t *testing.T) {
	temporary := t.TempDir()
	if err := os.MkdirAll(filepath.Join(temporary, "pinned"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(strings.Repeat("0", 64) + "  pinned/bridge.openapi.yaml\n")
	if err := os.WriteFile(filepath.Join(temporary, "pinned", "bridge.openapi.yaml"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksums(temporary, manifest); err == nil || !strings.Contains(err.Error(), "contract drift") {
		t.Fatalf("drift error = %v", err)
	}
}

func TestNoCharlieCentralTransportImports(t *testing.T) {
	repositoryRoot := filepath.Clean("../../..")
	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.Contains(strings.ToLower(name), "alphabravocompany/charlie") {
				t.Errorf("direct Charlie transport dependency in %s: %s", path, name)
			}
			if !strings.Contains(path, filepath.Join("internal", "charlie", "contract")) &&
				strings.HasPrefix(name, "github.com/alphabravocompany/astronomer-go/internal/charlie/contract/internal/") {
				t.Errorf("package outside bridge client imports generated transport in %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedWirePackageIsGoInternal(t *testing.T) {
	if _, err := os.Stat(filepath.Join("internal", "wire", "client.gen.go")); err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "client.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundConstructor := false
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "NewLocalClient" {
			foundConstructor = true
		}
	}
	if !foundConstructor {
		t.Fatal("local-only bridge constructor is missing")
	}
}
