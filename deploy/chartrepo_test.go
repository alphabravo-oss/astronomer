package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
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
		"astronomer/templates/server-deployment.yaml",
	} {
		if !seen[want] {
			t.Fatalf("archive missing %s", want)
		}
	}
}
