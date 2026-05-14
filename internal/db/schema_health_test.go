package db

// SchemaHealth check unit tests. The function lives on *DB and would
// ideally be exercised against a live Postgres; these tests pin the
// pure helpers (isUndefinedTable, indexOf) and the nil-pool error
// path, which is enough to prove the parsing logic doesn't regress.
// End-to-end coverage lives in the chart-installed preflight job and
// the fresh-cluster CI smoke test.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIsUndefinedTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlstate", errors.New(`ERROR: relation "schema_migrations" does not exist (SQLSTATE 42P01)`), true},
		{"plain_undefined", errors.New("undefined_table"), true},
		{"name_match", errors.New(`relation "schema_migrations" does not exist`), true},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isUndefinedTable(c.err); got != c.want {
				t.Errorf("isUndefinedTable(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestIndexOf(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        int
	}{
		{"hello world", "world", 6},
		{"hello", "", 0},
		{"abc", "xyz", -1},
		{"short", "longer-than-haystack", -1},
		{"prefix", "pre", 0},
	}
	for _, c := range cases {
		if got := indexOf(c.hay, c.needle); got != c.want {
			t.Errorf("indexOf(%q,%q)=%d want %d", c.hay, c.needle, got, c.want)
		}
	}
}

func TestSchemaHealth_NilPool(t *testing.T) {
	var d *DB
	if err := d.SchemaHealth(context.Background()); err == nil {
		t.Errorf("nil receiver should error")
	}
	d = &DB{}
	err := d.SchemaHealth(context.Background())
	if err == nil {
		t.Fatalf("nil pool should error")
	}
	if !strings.Contains(err.Error(), "nil pool") {
		t.Errorf("error should mention nil pool, got %v", err)
	}
}
