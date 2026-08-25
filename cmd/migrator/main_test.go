package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseOptionsPreservesMigrateCLIContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts, done, err := parseOptions([]string{
		"-database", "postgres://user:secret@database/astronomer",
		"-path", "internal/db/migrations",
		"-lock-timeout", "42",
		"up", "3",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("parse unexpectedly completed without a command")
	}
	if opts.sourceURL != "file://internal/db/migrations" || opts.command != "up" || len(opts.args) != 1 || opts.args[0] != "3" {
		t.Fatalf("unexpected options: %#v", opts)
	}
	if opts.lockTimeout != 42*time.Second {
		t.Fatalf("lock timeout = %s, want 42s", opts.lockTimeout)
	}
}

func TestParseOptionsRejectsAmbiguousSource(t *testing.T) {
	var output bytes.Buffer
	_, _, err := parseOptions([]string{"-source", "file://one", "-path", "two", "up"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive source error, got %v", err)
	}
}

func TestParseDownRequiresBoundOrExplicitAllConfirmation(t *testing.T) {
	var output bytes.Buffer
	if _, err := parseDown(nil, strings.NewReader("y\n"), &output); err == nil {
		t.Fatal("unbounded down without -all unexpectedly succeeded")
	}
	if _, err := parseDown([]string{"-all"}, strings.NewReader("n\n"), &output); err == nil {
		t.Fatal("rejected -all confirmation unexpectedly succeeded")
	}
	n, err := parseDown([]string{"-all"}, strings.NewReader("y\n"), &output)
	if err != nil || n != -1 {
		t.Fatalf("confirmed -all = %d, %v; want -1, nil", n, err)
	}
}

func TestRedactDatabaseCredentials(t *testing.T) {
	databaseURL := "postgres://operator:p%40ssword@database/astronomer?sslmode=require"
	message := "connect " + databaseURL + ": password p@ssword and p%40ssword were rejected"
	got := redactDatabaseCredentials(message, databaseURL)
	for _, secret := range []string{databaseURL, "p@ssword", "p%40ssword"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted error still contains %q: %s", secret, got)
		}
	}
}

func TestParseVersionAndCountBounds(t *testing.T) {
	if _, err := parseUintArg("goto", []string{"4294967296"}); err == nil {
		t.Fatal("out-of-range migration version unexpectedly succeeded")
	}
	if _, _, err := parseOptionalCount([]string{"-1"}); err == nil {
		t.Fatal("negative migration count unexpectedly succeeded")
	}
	if version, err := parseForceVersion([]string{"-1"}); err != nil || version != -1 {
		t.Fatalf("force -1 = %d, %v", version, err)
	}
}
