package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestDockerBuildTargetsPassImmutableIdentityArguments(t *testing.T) {
	raw, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(raw)
	for _, buildArg := range []string{
		"--build-arg VERSION=$(VERSION)",
		"--build-arg GIT_COMMIT=$(GIT_COMMIT)",
		"--build-arg BUILD_DATE=$(BUILD_DATE)",
	} {
		if !strings.Contains(makefile, buildArg) {
			t.Fatalf("DOCKER_BUILD_ARGS missing %q", buildArg)
		}
	}
	for _, target := range []string{
		"docker-build-server",
		"docker-build-worker",
		"docker-build-migrate",
		"docker-build-frontend",
		"docker-build-shell",
		"docker-build-agent",
	} {
		recipe := makeTargetRecipe(t, makefile, target)
		if !strings.Contains(recipe, "$(DOCKER_BUILD_ARGS)") {
			t.Fatalf("%s does not pass VERSION, GIT_COMMIT, and BUILD_DATE", target)
		}
	}
}

func TestDockerBuildTargetsUseDockerfileCompatibleContexts(t *testing.T) {
	raw, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(raw)
	if recipe := makeTargetRecipe(t, makefile, "docker-build-shell"); !strings.HasSuffix(strings.TrimSpace(recipe), " .") {
		t.Fatalf("docker-build-shell must use repository-root context, got %q", recipe)
	}
	if recipe := makeTargetRecipe(t, makefile, "docker-build-frontend"); !strings.HasSuffix(strings.TrimSpace(recipe), " frontend") {
		t.Fatalf("docker-build-frontend must use frontend context, got %q", recipe)
	}
}

func makeTargetRecipe(t *testing.T, makefile, target string) string {
	t.Helper()
	marker := target + ":"
	start := strings.Index(makefile, marker)
	if start < 0 {
		t.Fatalf("missing Make target %s", target)
	}
	lines := strings.Split(makefile[start:], "\n")
	var recipe []string
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "\t") {
			recipe = append(recipe, line)
			continue
		}
		if strings.TrimSpace(line) != "" {
			break
		}
	}
	if len(recipe) == 0 {
		t.Fatalf("target %s has no recipe", target)
	}
	return strings.Join(recipe, "\n")
}
