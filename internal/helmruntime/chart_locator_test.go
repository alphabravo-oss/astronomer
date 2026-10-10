package helmruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"helm.sh/helm/v4/pkg/chart/v2/loader"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

func TestHelmLocatorPreservesLocalChartPathAndCallerDeadline(t *testing.T) {
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "Chart.yaml"), []byte("apiVersion: v2\nname: fixture\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := LocateRepositoryChart(context.Background(), Config{}, path, "", "2.0.0")
	if err != nil || actual != path {
		t.Fatal("existing local path or local version behavior changed")
	}
	chart, err := loader.Load(actual)
	if err != nil || chart.Metadata.Version != "1.0.0" {
		t.Fatal("standard local chart loader contract changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LocateRepositoryChart(ctx, Config{}, path, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal("expired operation admitted a cached/local chart")
	}
	if _, err := LocateRepositoryChart(context.Background(), Config{}, filepath.Join(path, "missing"), "", ""); err == nil {
		t.Fatal("missing absolute chart path admitted")
	}
}

func TestHelmRepositoryOwnerPreservesNamedCredentialsAndTLSWithoutURLGrant(t *testing.T) {
	root := t.TempDir()
	runtime := Config{RepositoryConfig: filepath.Join(root, "repositories.yaml"), RepositoryCache: root}
	settings := runtime.Settings()
	entry := &repo.Entry{Name: "owned", URL: "https://charts.example/repository", Username: "fixture-user", Password: "fixture-password", CAFile: "/owned/ca.pem", CertFile: "/owned/client.pem", KeyFile: "/owned/client.key"}
	file := repo.NewFile()
	file.Add(entry)
	if err := file.WriteFile(settings.RepositoryConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, coordinates := range [][2]string{{"owned/fixture", ""}, {"fixture", entry.URL}} {
		actual, err := chartRepositoryOwner(context.Background(), settings, coordinates[0], coordinates[1])
		if err != nil || actual == nil || *actual != *entry {
			t.Fatal("operator named repository credential/TLS snapshot was lost")
		}
	}
	actual, err := chartRepositoryOwner(context.Background(), settings, "fixture", "https://other.example/repository")
	if err != nil || actual != nil {
		t.Fatal("caller repository URL acquired unrelated credentials")
	}
	if _, err := chartRepositoryOwner(context.Background(), settings, "missing/fixture", ""); err == nil {
		t.Fatal("missing named repository admitted")
	}
}

func TestHelmRepositoryCredentialsRequireOwnedOriginOrExplicitOperatorFlag(t *testing.T) {
	entry := &repo.Entry{URL: "https://charts.example:8443/repository", Username: "fixture-user", Password: "fixture-password"}
	transfer := repositoryGetter{ctx: context.Background(), owner: entry}
	if !transfer.credentialsFor("https://charts.example:8443/chart.tgz") {
		t.Fatal("same operator origin lost credentials")
	}
	for _, address := range []string{"https://charts.example:8444/chart.tgz", "http://charts.example:8443/chart.tgz", "https://other.example/chart.tgz"} {
		if transfer.credentialsFor(address) {
			t.Fatal("chart URL broadened operator credential origin")
		}
	}
	entry.PassCredentialsAll = true
	if !transfer.credentialsFor("https://other.example/chart.tgz") {
		t.Fatal("explicit operator repository flag was lost")
	}
	// Destination permission is still checked independently by the canonical
	// client. The flag supplies no URL/CIDR grant and redirects strip headers.
	transfer.owner = nil
	if transfer.credentialsFor("https://charts.example:8443/chart.tgz") {
		t.Fatal("an unowned URL invented authentication")
	}
}
