package handler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCertManagerChartCoordinates(t *testing.T) {
	got := CertManagerChartCoordinates()
	if got.ChartName != "cert-manager" {
		t.Fatalf("ChartName = %q, want cert-manager", got.ChartName)
	}
	if got.RepoURL != "https://charts.jetstack.io" {
		t.Fatalf("RepoURL = %q, want Jetstack repo", got.RepoURL)
	}
	if got.Namespace != "astronomer-cert-manager" {
		t.Fatalf("Namespace = %q, want astronomer-cert-manager", got.Namespace)
	}
}

func TestCertManagerToolMigrationContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller path")
	}
	path := filepath.Join(filepath.Dir(file), "..", "db", "migrations", "033_cert_manager_tool.up.sql")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(body)
	for _, want := range []string{
		"'cert-manager'",
		"https://charts.jetstack.io",
		"cert-manager-webhook",
		`"crds:\n  enabled: true\nprometheus:\n  enabled: true\n"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}
