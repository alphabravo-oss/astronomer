package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoUnclampedLimitParsing enforces F5: list endpoints must not parse a
// client ?limit straight into SQL via a raw queryInt(r, "limit", …). Use
// queryLimit / queryLimitMax (which clamp to a hard ceiling) instead, so a
// hostile ?limit=10000000 cannot amplify memory/DB load. The only allowed raw
// sites are the clamp helpers themselves and display-only paginators that slice
// an already-fetched in-memory list.
func TestNoUnclampedLimitParsing(t *testing.T) {
	allow := map[string]bool{
		"response.go": true, // defines the canonical queryLimit/queryLimitMax/queryLimitOffset parsers
	}
	raw := regexp.MustCompile(`queryInt\(r,\s*"limit"`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allow[name] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if raw.Match(b) {
			t.Errorf("%s parses ?limit with a raw queryInt(r, \"limit\", …); use queryLimit/queryLimitMax so the client value is clamped (F5)", name)
		}
	}
}

// TestNoRawOffsetParsing keeps every SQL-backed list on queryOffset. Direct
// queryInt parsing followed by int32 conversion can turn an oversized client
// value into a negative PostgreSQL OFFSET and a 500 response.
func TestNoRawOffsetParsing(t *testing.T) {
	raw := regexp.MustCompile(`queryInt\(r,\s*"offset"`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if raw.Match(b) {
			t.Errorf("%s parses ?offset with raw queryInt; use queryOffset so values remain in the non-negative int32 range", name)
		}
	}
}
