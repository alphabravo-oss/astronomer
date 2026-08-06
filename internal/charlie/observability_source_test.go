package charlie

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This guard covers every Astronomer process that owns a Charlie operational
// sink. Content-free serialization is mandatory in server, worker, bridge,
// qualification, and notification failure paths—not a package-local style
// convention.
func TestCharlieProcessesCannotBypassClosedOperationalEmitters(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, root := range []string{"internal/charlie", "internal/charliequalification", "internal/handler", "internal/worker/tasks", "internal/server", "cmd/charlie-qualification-hook"} {
		err := filepath.WalkDir(filepath.Join(repositoryRoot, root), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || !isCharlieOperationalSource(repositoryRoot, path) {
				return nil
			}
			relative, _ := filepath.Rel(repositoryRoot, path)
			relative = filepath.ToSlash(relative)
			if relative == "internal/charlie/observability.go" || relative == "internal/charliequalification/observability.go" {
				return nil
			}
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			imports := map[string]string{}
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				name := filepath.Base(importPath)
				if spec.Name != nil {
					name = spec.Name.Name
				}
				if (importPath == "log" || importPath == "log/slog") && name == "." {
					t.Errorf("%s uses a logging dot import", relative)
				}
				imports[name] = importPath
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				path := imports[identifier.Name]
				if (path == "log" && isDirectStandardLog(selector.Sel.Name)) || (path == "log/slog" && isDirectSlog(selector.Sel.Name)) {
					position := fileSet.Position(call.Pos())
					t.Errorf("%s:%d bypasses the closed Charlie operational emitter", relative, position.Line)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func isCharlieOperationalSource(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	if strings.HasPrefix(relative, "internal/charlie/") || strings.HasPrefix(relative, "internal/charliequalification/") || strings.HasPrefix(relative, "cmd/charlie-qualification-hook/") {
		return true
	}
	base := filepath.Base(relative)
	return strings.HasPrefix(base, "charlie_") || base == "routes_charlie.go" || base == "charlie_runtime.go"
}

func isDirectSlog(name string) bool {
	switch name {
	case "Debug", "DebugContext", "Info", "InfoContext", "Warn", "WarnContext", "Error", "ErrorContext", "Log", "LogAttrs":
		return true
	default:
		return false
	}
}

func isDirectStandardLog(name string) bool {
	switch name {
	case "Print", "Printf", "Println", "Fatal", "Fatalf", "Fatalln", "Panic", "Panicf", "Panicln":
		return true
	default:
		return false
	}
}
