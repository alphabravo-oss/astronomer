package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealEstateManifestReadBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadEstateManifest(path); err == nil || err.Error() != "estate manifest exceeds 1MiB" {
		t.Fatalf("oversized manifest: %v", err)
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if _, _, err := loadEstateManifest(path); err == nil || err.Error() != "cannot read estate manifest" {
			t.Fatalf("read failure: %v", err)
		}
	}
}
