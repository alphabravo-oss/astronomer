// sql-governance discovers runtime SQL syntax and execution candidates.
// It deliberately reports unresolved expressions rather than proving dataflow.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type site struct {
	Path        string   `json:"path"`
	Function    string   `json:"function"`
	Operation   string   `json:"operation"`
	Expression  string   `json:"expression"`
	SHA256      string   `json:"sha256"`
	ScopeSHA256 string   `json:"scope_sha256"`
	Targets     []string `json:"delete_targets"`
	Dynamic     bool     `json:"dynamic_delete"`
	Line        int      `json:"line"`
}

type inventory struct {
	SchemaVersion int    `json:"schema_version"`
	Sites         []site `json:"sites"`
}

var sqlStart = regexp.MustCompile(`(?is)^\s*(?:--[^\n]*\n\s*)*(?:SELECT\s|INSERT\s+INTO\b|UPDATE\s+[a-z_][a-z0-9_.]*\s+SET\b|DELETE\s+FROM\b|WITH\s+[a-z_][a-z0-9_]*\s+AS\s*\(|CREATE\s+(?:TABLE|SCHEMA|FUNCTION|TRIGGER|INDEX|VIEW|SEQUENCE|TYPE)\b|ALTER\s+(?:TABLE|SCHEMA|TYPE|SEQUENCE)\b|DROP\s+(?:TABLE|SCHEMA|FUNCTION|INDEX|VIEW|SEQUENCE|TYPE)\b|TRUNCATE\s|COPY\s|SET\s+[a-z_][a-z0-9_.]*\s*(?:=|TO\b)|SHOW\s+[a-z_][a-z0-9_]*\s*$|(?:BEGIN|COMMIT|ROLLBACK)\s*;?\s*$)`)
var deleteFrom = regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+(?:(?:public|"public")\.)?(?:"([a-z_][a-z0-9_]*)"|([a-z_][a-z0-9_]*|%[sv]))`)
var deletePrefix = regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`)
var stopped = map[string]bool{"PruneOldImageVulnerabilityReportSnapshots": true, "DeleteClusterServiceMesh": true}
var methods = map[string]bool{"Exec": true, "ExecContext": true, "Query": true, "QueryContext": true, "QueryRow": true, "QueryRowContext": true, "Queue": true, "Prepare": true, "PrepareContext": true, "SendBatch": true, "CopyFrom": true}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	result, err := discover(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func discover(root string) (inventory, error) {
	result := inventory{SchemaVersion: 1, Sites: []site{}}
	for _, directory := range []string{"internal", "cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if filepath.Clean(path) == filepath.Join(root, "internal", "db", "sqlc") {
					return filepath.SkipDir
				}
				return nil
			}
			if (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".go.tmpl")) || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			sites, err := scanFile(path, filepath.ToSlash(relative))
			result.Sites = append(result.Sites, sites...)
			return err
		})
		if err != nil {
			return result, err
		}
	}
	sort.Slice(result.Sites, func(i, j int) bool {
		a, b := result.Sites[i], result.Sites[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	return result, nil
}

func scanFile(path, relative string) ([]site, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var sites []site
	for _, declaration := range file.Decls {
		function := "package"
		if fn, ok := declaration.(*ast.FuncDecl); ok {
			function = fn.Name.Name
			if fn.Recv != nil {
				var receiver bytes.Buffer
				if err := printer.Fprint(&receiver, set, fn.Recv.List[0].Type); err != nil {
					return nil, err
				}
				function = receiver.String() + "." + function
			}
		}
		var scope bytes.Buffer
		if err := printer.Fprint(&scope, set, declaration); err != nil {
			return nil, err
		}
		scopeHash := fmt.Sprintf("%x", sha256.Sum256(scope.Bytes()))
		var printErr error
		directCalls := map[*ast.SelectorExpr]bool{}
		add := func(row site) { row.ScopeSHA256 = scopeHash; sites = append(sites, row) }
		ast.Inspect(declaration, func(node ast.Node) bool {
			if printErr != nil {
				return false
			}
			if call, ok := node.(*ast.CallExpr); ok {
				if selected, ok := call.Fun.(*ast.SelectorExpr); ok && methods[selected.Sel.Name] {
					directCalls[selected] = true
					var source bytes.Buffer
					if err := printer.Fprint(&source, set, call); err != nil {
						printErr = err
						return false
					}
					add(makeSite(relative, function, selected.Sel.Name, source.String(), set.Position(call.Pos()).Line))
				}
			}
			if selected, ok := node.(*ast.SelectorExpr); ok && methods[selected.Sel.Name] && !directCalls[selected] {
				var source bytes.Buffer
				if err := printer.Fprint(&source, set, selected); err != nil {
					printErr = err
					return false
				}
				add(makeSite(relative, function, "MethodValue."+selected.Sel.Name, source.String(), set.Position(selected.Pos()).Line))
			}

			if selected, ok := node.(*ast.SelectorExpr); ok && stopped[selected.Sel.Name] {
				var source bytes.Buffer
				if err := printer.Fprint(&source, set, selected); err != nil {
					printErr = err
					return false
				}
				add(makeSite(relative, function, "StoppedActivation", source.String(), set.Position(selected.Pos()).Line))
			}
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value, err := strconv.Unquote(literal.Value)
				if err == nil && sqlStart.MatchString(value) {
					add(makeSite(relative, function, "SQLLiteral", value, set.Position(literal.Pos()).Line))
				}
			}
			return true
		})
		if printErr != nil {
			return nil, printErr
		}
	}
	return sites, nil
}

func makeSite(path, function, operation, expression string, line int) site {
	result := site{Path: path, Function: function, Operation: operation, Expression: expression, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(expression))), Targets: []string{}, Line: line}
	for _, match := range deleteFrom.FindAllStringSubmatch(expression, -1) {
		target := match[1]
		if target == "" {
			target = match[2]
		}
		if strings.HasPrefix(target, "%") {
			result.Dynamic = true
		} else {
			result.Targets = append(result.Targets, strings.ToLower(target))
		}
	}
	if deletePrefix.MatchString(expression) && len(result.Targets) == 0 {
		result.Dynamic = true
	}
	sort.Strings(result.Targets)
	return result
}
