package crd

import (
	"strings"
	"testing"
)

// TestComponentBundleHelmSourceRequiresTargetRevision — an omitted helm
// targetRevision used to render as "*", so a ComponentBundle fanned out by an
// automated ApplicationSet tracked whatever the Helm repo published last. It is
// now a validation error at both entry points, plus a backstop in the renderer.
func TestComponentBundleHelmSourceRequiresTargetRevision(t *testing.T) {
	spec := ComponentBundleSpec{
		Version:          "1.0.0",
		DefaultNamespace: "ingress-nginx",
		Source: ComponentBundleSourceSpec{
			Type:    "helm",
			RepoURL: "https://kubernetes.github.io/ingress-nginx",
			Chart:   "ingress-nginx",
		},
	}
	problems := validateComponentBundleSpec(spec)
	if !containsProblem(problems, "source.targetRevision is required for helm bundles") {
		t.Fatalf("expected a targetRevision problem, got %v", problems)
	}

	// The renderer refuses too, so a caller that skips validation cannot emit a
	// floating source.
	if _, err := componentBundleApplicationSource(spec); err == nil {
		t.Fatal("expected componentBundleApplicationSource to reject an unset helm targetRevision")
	}

	spec.Source.TargetRevision = "4.12.0"
	if problems := validateComponentBundleSpec(spec); len(problems) != 0 {
		t.Fatalf("pinned bundle should validate cleanly, got %v", problems)
	}
	source, err := componentBundleApplicationSource(spec)
	if err != nil {
		t.Fatalf("componentBundleApplicationSource: %v", err)
	}
	if source["targetRevision"] != "4.12.0" {
		t.Fatalf("targetRevision = %v, want 4.12.0", source["targetRevision"])
	}
}

// Git-backed sources keep defaulting to HEAD: a git revision is a branch or tag
// the operator already controls, not an upstream publisher's latest release.
func TestGitBackedSourceKeepsHeadDefault(t *testing.T) {
	source, err := applicationSourceFromComponentBundleSource(ComponentBundleSourceSpec{
		Type:    "git-path",
		RepoURL: "https://github.com/example/platform.git",
		Path:    "apps/platform",
	})
	if err != nil {
		t.Fatalf("applicationSourceFromComponentBundleSource: %v", err)
	}
	if source["targetRevision"] != "HEAD" {
		t.Fatalf("targetRevision = %v, want HEAD", source["targetRevision"])
	}
}

func TestGitOpsTemplateHelmSourceRequiresTargetRevision(t *testing.T) {
	spec := gitOpsTemplateSpec{
		Source: ComponentBundleSourceSpec{
			Type:    "helm",
			RepoURL: "https://kubernetes.github.io/ingress-nginx",
			Chart:   "ingress-nginx",
		},
	}
	if !containsProblem(validateGitOpsTemplateSpec(spec), "source.targetRevision is required for helm templates") {
		t.Fatalf("expected a targetRevision problem, got %v", validateGitOpsTemplateSpec(spec))
	}
	spec.Source.TargetRevision = "4.12.0"
	if problems := validateGitOpsTemplateSpec(spec); len(problems) != 0 {
		t.Fatalf("pinned template should validate cleanly, got %v", problems)
	}
}

func containsProblem(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}
