package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAstronomerChartRepoBuildsIndexAndArchive(t *testing.T) {
	repo, err := AstronomerChartRepo()
	if err != nil {
		t.Fatalf("AstronomerChartRepo error: %v", err)
	}
	if repo.Name() != "astronomer" {
		t.Fatalf("repo.Name = %q", repo.Name())
	}
	if repo.Version() == "" {
		t.Fatal("repo.Version empty")
	}
	if got := repo.ArchiveName(); got != "astronomer-"+repo.Version()+".tgz" {
		t.Fatalf("archive name = %q", got)
	}
	index := string(repo.IndexYAML())
	if !strings.Contains(index, "apiVersion: v1") {
		t.Fatalf("index missing apiVersion: %s", index)
	}
	if !strings.Contains(index, "astronomer-"+repo.Version()+".tgz") {
		t.Fatalf("index missing archive reference: %s", index)
	}
}

func TestAstronomerChartArchiveContainsChartFiles(t *testing.T) {
	repo, err := AstronomerChartRepo()
	if err != nil {
		t.Fatalf("AstronomerChartRepo error: %v", err)
	}
	gzr, err := gzip.NewReader(bytes.NewReader(repo.ArchiveTGZ()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() {
		_ = gzr.Close()
	}()

	tr := tar.NewReader(gzr)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		seen[hdr.Name] = true
	}

	for _, want := range []string{
		"astronomer/Chart.yaml",
		"astronomer/values.yaml",
		"astronomer/values.schema.json",
		"astronomer/DEPENDENCIES.md",
		"astronomer/templates/server-deployment.yaml",
	} {
		if !seen[want] {
			t.Fatalf("archive missing %s", want)
		}
	}
	for _, unwanted := range []string{
		"astronomer/Chart.lock",
		"astronomer/charts/",
	} {
		for name := range seen {
			if strings.HasPrefix(name, unwanted) {
				t.Fatalf("dependency-free archive unexpectedly contains %s", name)
			}
		}
	}
}

func TestAstronomerChartArchiveRendersOffline(t *testing.T) {
	repo, err := AstronomerChartRepo()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), repo.ArchiveName())
	if err := os.WriteFile(archive, repo.ArchiveTGZ(), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("helm", "template", "astronomer", archive,
		"--set", "bootstrap.existingSecret=bootstrap-credentials",
		"--set", "secrets.existingSecret=core-credentials")
	cmd.Env = append(os.Environ(), "HELM_REPOSITORY_CACHE="+t.TempDir(), "HELM_REPOSITORY_CONFIG="+filepath.Join(t.TempDir(), "repositories.yaml"))
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatalf("offline embedded chart render failed: %v", err)
	}
	if !strings.Contains(output.String(), "name: astronomer-server") {
		t.Fatal("embedded archive did not render the management-plane server")
	}
	if strings.Contains(strings.ToLower(output.String()), "argoproj") {
		t.Fatal("embedded archive unexpectedly rendered a legacy delivery controller")
	}
}
