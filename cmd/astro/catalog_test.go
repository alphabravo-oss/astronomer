package main

import (
	"strings"
	"testing"
)

func TestCatalogInstallRequiresProjectAndImmutableVersion(t *testing.T) {
	cmd := newCatalogInstallCmd()
	cmd.SetArgs([]string{
		"--cluster", "11111111-1111-4111-8111-111111111111",
		"--namespace", "payments",
		"--release-name", "payments",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--project") || !strings.Contains(err.Error(), "--chart-version") {
		t.Fatalf("expected project and immutable chart-version validation, got %v", err)
	}
}

func TestCatalogOptionalUUID(t *testing.T) {
	if got, err := catalogOptionalUUID("", "project"); err != nil || got != nil {
		t.Fatalf("empty optional UUID = %v, %v; want nil, nil", got, err)
	}
	if _, err := catalogOptionalUUID("not-a-uuid", "project"); err == nil || !strings.Contains(err.Error(), "invalid --project") {
		t.Fatalf("invalid optional UUID error = %v", err)
	}
}
