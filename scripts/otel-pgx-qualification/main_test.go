package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestProtectedDatabaseReferenceRejectsUnsafeTargetsAndFiles(t *testing.T) {
	directory := t.TempDir()
	good := filepath.Join(directory, "good.json")
	raw, _ := json.Marshal(databaseReference{URL: "postgres://synthetic:synthetic@localhost:5432/" + ownedDatabase})
	if err := os.WriteFile(good, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReference(good); err != nil {
		t.Fatal("valid private owned reference rejected")
	}
	public := filepath.Join(directory, "public.json")
	_ = os.WriteFile(public, raw, 0644)
	link := filepath.Join(directory, "symlink.json")
	_ = os.Symlink(good, link)
	for _, path := range []string{public, link, directory, filepath.Join(directory, "missing.json")} {
		if _, err := readReference(path); err == nil {
			t.Error("unsafe reference accepted")
		}
	}
	for _, value := range []string{"postgres://synthetic:synthetic@localhost/source_recovery", "postgres://synthetic:synthetic@localhost/" + ownedDatabase + "?dbname=source_recovery", "postgres://synthetic:synthetic@localhost/" + ownedDatabase + "?options=-csearch_path=other", "https://synthetic:synthetic@localhost/" + ownedDatabase} {
		path := filepath.Join(directory, "candidate.json")
		data, _ := json.Marshal(databaseReference{URL: value})
		_ = os.WriteFile(path, data, 0600)
		if _, err := readReference(path); err == nil {
			t.Error("foreign database or override accepted")
		} else if strings.Contains(err.Error(), "synthetic") {
			t.Error("reference error disclosed credentials")
		}
	}
	oversized := filepath.Join(directory, "oversized.json")
	_ = os.WriteFile(oversized, []byte(strings.Repeat("x", 16385)), 0600)
	if _, err := readReference(oversized); err == nil {
		t.Error("oversized reference accepted")
	}
}

func TestReferenceOptionsRejectMalformedAndDuplicateQueries(t *testing.T) {
	for _, query := range []string{"sslmode=require&sslmode=disable", "sslmode=%zz", "sslmode=require;dbname=other", "application_name=synthetic&application_name=other"} {
		path := filepath.Join(t.TempDir(), "reference.json")
		raw, _ := json.Marshal(databaseReference{URL: "postgres://localhost/" + ownedDatabase + "?" + query})
		_ = os.WriteFile(path, raw, 0600)
		if _, err := readReference(path); err == nil {
			t.Error("ambiguous options accepted")
		}
	}
}

func TestCaptureHandlesPasswordlessReferences(t *testing.T) {
	for _, connection := range []string{"postgres://localhost/" + ownedDatabase, "postgres://synthetic@localhost/" + ownedDatabase} {
		if _, err := validateCapture(new(capture), trace.TraceID{}, connection, 2, 180006); err == nil {
			t.Error("empty capture accepted")
		}
	}
}

func TestServerMajorFenceRejectsOtherServers(t *testing.T) {
	for _, version := range []string{"170011", "190000", "180006malformed", "", "-180006"} {
		if _, err := requireServerVersion(version); err == nil {
			t.Error("unsupported server accepted")
		}
	}
	if n, err := requireServerVersion("180006"); err != nil || n != 180006 {
		t.Fatal("actual PostgreSQL18 rejected")
	}
}
