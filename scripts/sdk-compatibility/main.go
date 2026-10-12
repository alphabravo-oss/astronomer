// Command sdk-compatibility generates an external compile fixture from a released SDK.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: sdk-compatibility baseline.go fixture.go current.go")
	}
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, os.Args[1], nil, 0)
	if err != nil {
		panic(err)
	}
	currentFile, err := parser.ParseFile(fs, os.Args[3], nil, 0)
	if err != nil {
		panic(err)
	}
	currentFields := map[string]map[string]ast.Expr{}
	for _, decl := range currentFile.Decls {
		if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.TYPE {
			for _, spec := range d.Specs {
				t := spec.(*ast.TypeSpec)
				if st, ok := t.Type.(*ast.StructType); ok {
					currentFields[t.Name.Name] = map[string]ast.Expr{}
					for _, f := range st.Fields.List {
						for _, n := range f.Names {
							currentFields[t.Name.Name][n.Name] = f.Type
						}
					}
				}
			}
		}
	}
	local := map[string]bool{}
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.TYPE {
			for _, spec := range d.Specs {
				local[spec.(*ast.TypeSpec).Name.Name] = true
			}
		}
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = path
	}
	used := map[string]bool{}
	expression := func(expr ast.Expr) string {
		var original bytes.Buffer
		if err := format.Node(&original, fs, expr); err != nil {
			panic(err)
		}
		parsed, err := parser.ParseExpr(original.String())
		if err != nil {
			panic(err)
		}
		var rewrite func(ast.Expr) ast.Expr
		rewrite = func(e ast.Expr) ast.Expr {
			switch v := e.(type) {
			case *ast.Ident:
				if local[v.Name] {
					return &ast.SelectorExpr{X: ast.NewIdent("client"), Sel: ast.NewIdent(v.Name)}
				}
			case *ast.SelectorExpr:
				if i, ok := v.X.(*ast.Ident); ok {
					used[i.Name] = true
				}
			case *ast.StarExpr:
				v.X = rewrite(v.X)
			case *ast.ArrayType:
				v.Elt = rewrite(v.Elt)
			case *ast.MapType:
				v.Key = rewrite(v.Key)
				v.Value = rewrite(v.Value)
			case *ast.ChanType:
				v.Value = rewrite(v.Value)
			case *ast.Ellipsis:
				v.Elt = rewrite(v.Elt)
			case *ast.FuncType:
				for _, list := range []*ast.FieldList{v.Params, v.Results} {
					if list == nil {
						continue
					}
					var fields []*ast.Field
					for _, f := range list.List {
						count := len(f.Names)
						if count == 0 {
							count = 1
						}
						for range count {
							fields = append(fields, &ast.Field{Type: rewrite(f.Type)})
						}
					}
					list.List = fields
				}
			case *ast.StructType:
				for _, f := range v.Fields.List {
					f.Type = rewrite(f.Type)
				}
			case *ast.InterfaceType:
				for _, f := range v.Methods.List {
					f.Type = rewrite(f.Type)
				}
			}
			return e
		}
		parsed = rewrite(parsed)
		var result bytes.Buffer
		if err := format.Node(&result, token.NewFileSet(), parsed); err != nil {
			panic(err)
		}
		return result.String()
	}
	var body strings.Builder
	fields, functions, methods, types, constants := 0, 0, 0, 0, 0
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					types++
					fmt.Fprintf(&body, "var _ client.%s\n", s.Name.Name)
					if st, ok := s.Type.(*ast.StructType); ok {
						for _, field := range st.Fields.List {
							for _, name := range field.Names {
								if !name.IsExported() {
									continue
								}
								fields++
								private := false
								ast.Inspect(field.Type, func(n ast.Node) bool {
									if st, ok := n.(*ast.StructType); ok {
										for _, f := range st.Fields.List {
											for _, name := range f.Names {
												if !name.IsExported() {
													private = true
												}
											}
										}
									}
									return true
								})
								if private {
									var oldType, currentType bytes.Buffer
									if err := format.Node(&oldType, fs, field.Type); err != nil {
										panic(err)
									}
									if err := format.Node(&currentType, fs, currentFields[s.Name.Name][name.Name]); err != nil {
										panic(err)
									}
									if oldType.String() != currentType.String() {
										panic("nonconstructible private anonymous field changed: " + s.Name.Name + "." + name.Name)
									}
									fmt.Fprintf(&body, "func field%d(current client.%s) { _ = current.%s }\n", fields, s.Name.Name, name.Name)
									continue
								}
								fmt.Fprintf(&body, "func field%d(current client.%s) { var legacy %s; legacy = current.%s; current.%s = legacy }\n", fields, s.Name.Name, expression(field.Type), name.Name, name.Name)
							}
						}
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							constants++
							fmt.Fprintf(&body, "var _ = client.%s\n", name.Name)
						}
					}
				}
			}
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			signature := expression(d.Type)
			if d.Recv == nil {
				functions++
				fmt.Fprintf(&body, "var _ %s = client.%s\n", signature, d.Name.Name)
			} else {
				methods++
				recv := expression(d.Recv.List[0].Type)
				signature = strings.Replace(signature, "func(", "func("+recv+", ", 1)
				// An empty parameter list cannot contain a trailing comma before ')'.
				signature = strings.Replace(signature, ", )", ")", 1)
				fmt.Fprintf(&body, "var _ %s = (%s).%s\n", signature, recv, d.Name.Name)
			}
		}
	}
	var out strings.Builder
	out.WriteString("// Generated released-client compile fixture.\npackage legacyfixture\nimport (\nclient \"github.com/alphabravocompany/astronomer-go/pkg/astroclient\"\n")
	for name, path := range imports {
		if used[name] {
			fmt.Fprintf(&out, "%s %q\n", name, path)
		}
	}
	out.WriteString(")\n")
	out.WriteString(body.String())
	formatted, err := format.Source([]byte(out.String()))
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(os.Args[2], formatted, 0600); err != nil {
		panic(err)
	}
	fmt.Printf("Legacy fixture: %d types, %d fields, %d functions, %d methods, %d constants\n", types, fields, functions, methods, constants)
}
