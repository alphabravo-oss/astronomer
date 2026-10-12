package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeSQLDiscoveryFindsEmbeddedDeleteAndUnresolvedCalls(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime.go")
	source := "package fixture\nfunc run(ctx any, db any) {\n" +
		"// DELETE FROM comment_only\n" +
		"db.Exec(ctx, `DELETE FROM public.events WHERE tenant_id=$1`, tenant)\n" +
		"db.Exec(ctx, `DELETE FROM `+identifier(table), tenant)\n" +
		"db.QueryRow(ctx, unknownQuery, tenant)\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := scanFile(path, "runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	var static, dynamic, unresolved bool
	for _, row := range rows {
		for _, target := range row.Targets {
			if target == "comment_only" {
				t.Fatal("Go comment treated as runtime SQL")
			}
			if target == "events" {
				static = true
			}
		}
		dynamic = dynamic || row.Dynamic
		unresolved = unresolved || row.Operation == "QueryRow"
		if row.SHA256 == "" || row.Function != "run" {
			t.Fatal("source binding absent")
		}
	}
	if !static || !dynamic || !unresolved {
		t.Fatalf("static=%t dynamic=%t unresolved=%t", static, dynamic, unresolved)
	}
}

func TestRuntimeSQLDiscoveryBindsChangedLiteralContent(t *testing.T) {
	before := makeSite("file.go", "delete", "SQLLiteral", "DELETE FROM events WHERE id=$1", 1)
	after := makeSite("file.go", "delete", "SQLLiteral", "DELETE FROM sessions WHERE id=$1", 1)
	if before.SHA256 == after.SHA256 || before.Targets[0] == after.Targets[0] {
		t.Fatal("changed destructive target retained old source identity")
	}
}

func TestRuntimeSQLDiscoveryBindsLocalBuilderAndReceiver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.go")
	before := `package fixture
 type Store struct{}
 func (s *Store) Delete(ctx any,db any) { query := "DELETE" + " FROM events WHERE id=$1"; db.Exec(ctx,query,id) }
 func Delete(ctx any,db any) { db.Exec(ctx,query,id) }
 `
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := scanFile(path, "fixture.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(before, "FROM events", "FROM sessions", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := scanFile(path, "fixture.go")
	if err != nil {
		t.Fatal(err)
	}
	var matched bool
	for i, row := range a {
		if row.Operation == "Exec" && row.Function == "*Store.Delete" {
			matched = true
			if row.SHA256 != b[i].SHA256 || row.ScopeSHA256 == b[i].ScopeSHA256 {
				t.Fatal("local builder change not detected independently of call expression")
			}
		}
	}
	if !matched {
		t.Fatal("method receiver binding absent")
	}
}
func TestRuntimeSQLDiscoveryIncludesTemplateAndRejectsInvalidSyntax(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"internal", "cmd", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "internal", "query.go.tmpl")
	if err := os.WriteFile(path, []byte("package fixture\nfunc run(){db.Exec(ctx,`DELETE FROM events WHERE id=$1`,id)}"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := discover(root)
	if err != nil || len(rows.Sites) != 2 {
		t.Fatalf("template sites=%d error=%v", len(rows.Sites), err)
	}
	if err := os.WriteFile(path, []byte("package fixture\nfunc broken("), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(root); err == nil {
		t.Fatal("invalid template silently disappeared from inventory")
	}
}

func TestRuntimeSQLDiscoveryFindsSQLMethodAliasAndQuotedDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.go")
	if err := os.WriteFile(path, []byte("package fixture\nfunc run(){ execute:=db.Exec; execute(ctx, `DELETE FROM \"public\".\"private_rows\" WHERE id=$1`,id) }"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := scanFile(path, "alias.go")
	if err != nil {
		t.Fatal(err)
	}
	var alias, quoted bool
	for _, row := range rows {
		alias = alias || row.Operation == "MethodValue.Exec"
		for _, target := range row.Targets {
			quoted = quoted || target == "private_rows"
		}
	}
	if !alias || !quoted {
		t.Fatalf("alias=%t quoted=%t", alias, quoted)
	}
}

func TestRuntimeSQLDiscoveryDoesNotExemptNestedSQLCDirectoryOrTestdata(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"internal/handler/sqlc", "internal/service/testdata", "cmd", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{"internal/handler/sqlc", "internal/service/testdata"} {
		path := filepath.Join(root, directory, "runtime.go")
		if err := os.WriteFile(path, []byte("package fixture\nfunc run(){db.Exec(ctx,`DELETE FROM private_rows WHERE id=$1`,id)}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Sites) != 4 {
		t.Fatalf("directory naming hid runtime SQL: %d candidates", len(rows.Sites))
	}
}
