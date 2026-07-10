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
		"astronomer/charts/argo-cd-9.5.21.tgz",
		"astronomer/licenses/argo-helm-APACHE-2.0.txt",
	} {
		if !seen[want] {
			t.Fatalf("archive missing %s", want)
		}
	}
}

func TestAstronomerChartArchiveRendersBundledArgoOffline(t *testing.T) {
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
	if !strings.Contains(output.String(), "name: astro-argocd-application-controller") {
		t.Fatal("embedded archive did not render the pinned bundled Argo controller")
	}
}

func TestPinnedArgoApplicationCRDHasNoStatusSubresource(t *testing.T) {
	archive, err := os.ReadFile(filepath.Join("chart", "charts", "argo-cd-9.5.21.tgz"))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatal("pinned argo-cd archive has no Application CRD template")
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name != "argo-cd/templates/crds/crd-application.yaml" {
			continue
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(text, "subresources: {}") || strings.Contains(text, "subresources:\n      status:") {
			t.Fatal("pinned Application CRD status semantics changed; update the scrub migration before upgrading")
		}
		return
	}
}
