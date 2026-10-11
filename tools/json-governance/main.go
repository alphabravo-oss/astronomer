// json-governance discovers syntax-bound HTTP decoders, JSON serializers and
// canonical sqlc JSON columns. It does not claim whole-program dataflow proof.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type site struct {
	Path          string   `json:"path"`
	Function      string   `json:"function"`
	Operation     string   `json:"operation"`
	Arguments     []string `json:"arguments"`
	Line          int      `json:"line"`
	ErrorHandling string   `json:"error_handling,omitempty"`
}
type column struct {
	Model   string `json:"model"`
	Field   string `json:"field"`
	Column  string `json:"column"`
	Storage string `json:"storage"`
}
type inventory struct {
	SchemaVersion int      `json:"schema_version"`
	Requests      []site   `json:"requests"`
	JSONCalls     []site   `json:"json_calls"`
	Columns       []column `json:"columns"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--rows" {
		if err := runRowInventory(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
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
	result := inventory{SchemaVersion: 1, Requests: []site{}, JSONCalls: []site{}, Columns: []column{}}
	var err error
	for _, directory := range []string{"internal", "cmd", "pkg", "deploy/bundles"} {
		err = filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".go.tmpl")) || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// Generated sqlc models are the canonical SQL parser's effective columns.
			if strings.HasSuffix(filepath.ToSlash(path), "internal/db/sqlc/models.go") {
				columns, err := discoverColumns(path)
				result.Columns = append(result.Columns, columns...)
				return err
			}
			if strings.Contains(filepath.ToSlash(path), "/sqlc/") || strings.Contains(path, ".gen.go") || strings.Contains(path, "generated.go") {
				return nil
			}
			requests, calls, err := discoverSites(path, filepath.ToSlash(relative))
			result.Requests = append(result.Requests, requests...)
			result.JSONCalls = append(result.JSONCalls, calls...)
			return err
		})
		if err != nil {
			return result, err
		}
	}
	sort.Slice(result.Columns, func(i, j int) bool {
		a, b := result.Columns[i], result.Columns[j]
		return a.Model+"."+a.Column < b.Model+"."+b.Column
	})
	return result, err
}

func imports(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = path
	}
	return aliases
}
func expression(set *token.FileSet, node ast.Node) string {
	var out bytes.Buffer
	_ = format.Node(&out, set, node)
	return out.String()
}
func qualified(expr ast.Expr, aliases map[string]string, path, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Obj == nil && aliases[ident.Name] == path
}
func requestNames(fn *ast.FuncDecl, aliases map[string]string) map[string]*ast.Object { //nolint:staticcheck // Bounded syntax inventory uses parser object identity for local shadowing, not whole-program type resolution.
	names := map[string]*ast.Object{} //nolint:staticcheck // Bounded syntax inventory uses parser object identity for local shadowing, not whole-program type resolution.
	for _, field := range fn.Type.Params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok || !qualified(star.X, aliases, "net/http", "Request") {
			continue
		}
		for _, name := range field.Names {
			names[name.Name] = name.Obj
		}
	}
	return names
}
func bodyBinding(expr ast.Expr, names map[string]*ast.Object) bool { //nolint:staticcheck // Bounded syntax inventory uses parser object identity for local shadowing, not whole-program type resolution.
	bound := false
	ast.Inspect(expr, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Body" {
			return true
		}
		name, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if binding, exists := names[name.Name]; exists && name.Obj == binding {
			bound = true
		}
		return true
	})
	return bound
}
func discoverSites(path, relative string) ([]site, []site, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		return nil, nil, err
	}
	aliases := imports(file)
	requests, calls := []site{}, []site{}
	helpers := map[string]bool{"decodeV1JSONBody": true, "decodeStrictJSONBody": true, "decodeAndValidate": true, "decodeOptionalJSON": true, "decodeRequest": true}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil {
			name = expression(set, fn.Recv.List[0].Type) + "." + name
		}
		requestBindings := requestNames(fn, aliases)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			op := expression(set, call.Fun)
			item := site{Path: relative, Function: name, Operation: op, Arguments: []string{}, Line: set.Position(call.Pos()).Line}
			for _, arg := range call.Args {
				item.Arguments = append(item.Arguments, expression(set, arg))
			}
			jsonCall := qualified(call.Fun, aliases, "github.com/alphabravocompany/astronomer-go/internal/jsoncontract", "Decode")

			for _, method := range []string{"Marshal", "Unmarshal", "NewDecoder", "NewEncoder", "Valid"} {
				if qualified(call.Fun, aliases, "encoding/json", method) {
					jsonCall = true
					break
				}
			}
			if jsonCall {
				item.ErrorHandling = "return_or_local_binding"
				ast.Inspect(fn.Body, func(parent ast.Node) bool {
					assignment, ok := parent.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for _, right := range assignment.Rhs {
						if right == call {
							for _, left := range assignment.Lhs {
								if ident, ok := left.(*ast.Ident); ok && ident.Name == "_" {
									item.ErrorHandling = "discarded_result_requires_domain_review"
								}
							}
						}
					}
					return true
				})
				calls = append(calls, item)
			}
			requestCall := qualified(call.Fun, aliases, "encoding/json", "NewDecoder") && len(call.Args) > 0 && bodyBinding(call.Args[0], requestBindings)

			helper := call.Fun
			switch indexed := helper.(type) {
			case *ast.IndexExpr:
				helper = indexed.X
			case *ast.IndexListExpr:
				helper = indexed.X
			}
			ident, ok := helper.(*ast.Ident)
			if ok && helpers[ident.Name] && len(call.Args) > 0 {
				// Typed generic helper calls use IndexExpr/IndexListExpr, found below.
				for _, arg := range call.Args {
					if id, ok := arg.(*ast.Ident); ok && requestBindings[id.Name] != nil && id.Obj == requestBindings[id.Name] {
						requestCall = true
					}
				}
			}
			if qualified(call.Fun, aliases, "github.com/alphabravocompany/astronomer-go/internal/jsoncontract", "DecodeRequest") {
				requestCall = true
			}
			if requestCall {
				requests = append(requests, item)
			}
			return true
		})
	}
	return requests, calls, nil
}
func discoverColumns(path string) ([]column, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		return nil, err
	}
	aliases := imports(file)
	columns := []column{}
	for _, decl := range file.Decls {
		generic, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range generic.Specs {
			typ, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typ.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structure.Fields.List {
				if field.Tag == nil {
					continue
				}
				tag, _ := strconv.Unquote(field.Tag.Value)
				key := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
				storage := ""
				switch {
				case qualified(field.Type, aliases, "encoding/json", "RawMessage"):
					storage = "json_slot"
				case expression(set, field.Type) == "[]byte":
					storage = "byte_slot"
				case strings.HasSuffix(key, "_encrypted"):
					storage = "encrypted_slot"
				}
				if storage == "" {
					continue
				}
				for _, name := range field.Names {
					columns = append(columns, column{Model: typ.Name.Name, Field: name.Name, Column: key, Storage: storage})
				}
			}
		}
	}
	return columns, nil
}
