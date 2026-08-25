package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductionRuntimeCompositionStaysBoundedAndOrdered(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	directory := filepath.Dir(current)
	expected := map[string]string{
		"app_runtime.go":            "startProductionRuntime",
		"app_runtime_charlie.go":    "composeCharlieLifecycles",
		"app_runtime_foundation.go": "startRuntimeFoundation",
		"app_runtime_tasks.go":      "composeRuntimeTasks",
		"app_runtime_services.go":   "startRuntimeServices",
	}
	for name, function := range expected {
		path := filepath.Join(directory, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if lines := strings.Count(string(body), "\n") + 1; lines > 800 {
			t.Errorf("%s has %d lines; limit is 800", name, lines)
		}
		fileset := token.NewFileSet()
		parsed, err := parser.ParseFile(fileset, path, body, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, declaration := range parsed.Decls {
			candidate, ok := declaration.(*ast.FuncDecl)
			if !ok || candidate.Name.Name != function {
				continue
			}
			found = true
			start := fileset.Position(candidate.Pos()).Line
			end := fileset.Position(candidate.End()).Line
			if lines := end - start + 1; lines > 240 {
				t.Errorf("%s:%s has %d lines; limit is 240", name, function, lines)
			}
		}
		if !found {
			t.Errorf("%s does not define %s", name, function)
		}
	}

	entrypoint, err := os.ReadFile(filepath.Join(directory, "app_runtime.go"))
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, call := range []string{
		"composeCharlieLifecycles", "startRuntimeFoundation", "composeRuntimeTasks", "startRuntimeServices",
	} {
		position := strings.Index(string(entrypoint), call)
		if position <= previous {
			t.Fatalf("runtime phase %s is missing or out of order", call)
		}
		previous = position
	}
}
