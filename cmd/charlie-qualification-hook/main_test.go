package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetricSourcesLoadSeparatePrivateBearerFiles(t *testing.T) {
	directory := t.TempDir()
	firstToken := filepath.Join(directory, "first.token")
	secondToken := filepath.Join(directory, "second.token")
	config := filepath.Join(directory, "metrics.json")
	if err := os.WriteFile(firstToken, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondToken, []byte(strings.Repeat("b", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"sources":[{"url":"https://central.example/metrics","bearer_token_file":"` + firstToken + `"},{"url":"https://product.example/metrics","bearer_token_file":"` + secondToken + `"}]}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sources, err := metricSourcesFromFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0].Token != strings.Repeat("a", 32) || sources[1].Token != strings.Repeat("b", 32) {
		t.Fatalf("metric sources were not independently bound: %#v", sources)
	}
}

func TestMetricSourcesRejectPublicTokenFileAndInlineSecretField(t *testing.T) {
	directory := t.TempDir()
	token := filepath.Join(directory, "token")
	config := filepath.Join(directory, "metrics.json")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"sources":[{"url":"https://central.example/metrics","bearer_token_file":"` + token + `"}]}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := metricSourcesFromFile(config); err == nil {
		t.Fatal("group/world-readable metrics bearer file accepted")
	}
	if err := os.Chmod(token, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"sources":[{"url":"https://central.example/metrics","token":"must-not-be-inline"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := metricSourcesFromFile(config); err == nil {
		t.Fatal("inline metric secret field accepted")
	}
}
