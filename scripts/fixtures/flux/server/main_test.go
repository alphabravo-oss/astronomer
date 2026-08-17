package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureHandlerServesSmartGitAndHelmRepository(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	repository := filepath.Join(root, "git", "repository.git")
	helmDir := filepath.Join(root, "helm")
	for _, directory := range []string{worktree, filepath.Dir(repository), helmDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, worktree, "init", "--initial-branch=main")
	runGit(t, worktree, "config", "user.name", "Flux Integration")
	runGit(t, worktree, "config", "user.email", "flux-integration@example.invalid")
	if err := os.WriteFile(filepath.Join(worktree, "fixture.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "fixture.txt")
	runGit(t, worktree, "commit", "-m", "fixture")
	runGit(t, root, "clone", "--bare", worktree, repository)
	if err := os.WriteFile(filepath.Join(helmDir, "index.yaml"), []byte("apiVersion: v1\nentries: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler, err := newFixtureHandler(root, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	command := exec.Command("git", "ls-remote", server.URL+"/git/repository.git", "refs/heads/main")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "refs/heads/main") {
		t.Fatalf("smart Git fixture failed: output=%s err=%v", output, err)
	}
	response, err := http.Get(server.URL + "/helm/index.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "apiVersion: v1") {
		t.Fatalf("Helm fixture status=%d body=%s", response.StatusCode, body)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %s: %v", arguments, output, err)
	}
}
