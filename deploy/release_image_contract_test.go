package deploy

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseWorkflow struct {
	Jobs map[string]struct {
		Strategy struct {
			Matrix struct {
				Component []struct {
					Name       string `yaml:"name"`
					Context    string `yaml:"context"`
					Dockerfile string `yaml:"dockerfile"`
					ImageName  string `yaml:"imageName"`
				} `yaml:"component"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			Name string         `yaml:"name"`
			Uses string         `yaml:"uses"`
			With map[string]any `yaml:"with"`
			Run  string         `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

var expectedFirstPartyReleaseImages = map[string]struct {
	context, dockerfile, imageName string
}{
	"server":   {context: ".", dockerfile: "deploy/docker/Dockerfile.server", imageName: "astronomer-go-server"},
	"worker":   {context: ".", dockerfile: "deploy/docker/Dockerfile.worker", imageName: "astronomer-go-worker"},
	"agent":    {context: ".", dockerfile: "deploy/docker/Dockerfile.agent", imageName: "astronomer-go-agent"},
	"migrate":  {context: ".", dockerfile: "deploy/docker/Dockerfile.migrate", imageName: "astronomer-go-migrate"},
	"shell":    {context: ".", dockerfile: "deploy/docker/Dockerfile.shell", imageName: "astronomer-shell"},
	"frontend": {context: "frontend", dockerfile: "frontend/Dockerfile", imageName: "astronomer-frontend"},
}

func TestReleasePublishesSixTrueMultiPlatformImages(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	job, ok := workflow.Jobs["build-sign"]
	if !ok {
		t.Fatal("release workflow has no build-sign job")
	}
	if len(job.Strategy.Matrix.Component) != len(expectedFirstPartyReleaseImages) {
		t.Fatalf("release image matrix has %d entries, want %d", len(job.Strategy.Matrix.Component), len(expectedFirstPartyReleaseImages))
	}
	seen := map[string]bool{}
	for _, component := range job.Strategy.Matrix.Component {
		want, ok := expectedFirstPartyReleaseImages[component.Name]
		if !ok {
			t.Fatalf("unexpected release component %q", component.Name)
		}
		if seen[component.Name] {
			t.Fatalf("duplicate release component %q", component.Name)
		}
		seen[component.Name] = true
		if component.Context != want.context || component.Dockerfile != want.dockerfile || component.ImageName != want.imageName {
			t.Fatalf("release component %s = context %q dockerfile %q image %q, want %#v", component.Name, component.Context, component.Dockerfile, component.ImageName, want)
		}
	}

	qemuIndex := releaseStepIndex(job.Steps, "docker/setup-qemu-action@v3")
	buildxIndex := releaseStepIndex(job.Steps, "docker/setup-buildx-action@v3")
	buildIndex := releaseStepIndex(job.Steps, "docker/build-push-action@v6")
	if qemuIndex < 0 || buildxIndex < 0 || buildIndex < 0 || !(qemuIndex < buildxIndex && buildxIndex < buildIndex) {
		t.Fatalf("release must configure QEMU, then Buildx, then build-push; indexes qemu=%d buildx=%d build=%d", qemuIndex, buildxIndex, buildIndex)
	}
	qemuPlatforms := strings.ReplaceAll(stringValue(job.Steps[qemuIndex].With["platforms"]), " ", "")
	if qemuPlatforms != "arm64" {
		t.Fatalf("QEMU platforms = %q, want arm64", qemuPlatforms)
	}
	build := job.Steps[buildIndex]
	platforms := strings.ReplaceAll(stringValue(build.With["platforms"]), " ", "")
	if platforms != "linux/amd64,linux/arm64" {
		t.Fatalf("release platforms = %q, want linux/amd64,linux/arm64", platforms)
	}
	if push, ok := build.With["push"].(bool); !ok || !push {
		t.Fatalf("release build push = %#v, want true", build.With["push"])
	}

	verify := releaseNamedStep(t, job.Steps, "Verify multi-platform manifest")
	for _, required := range []string{"docker buildx imagetools inspect", "linux/amd64", "linux/arm64", "steps.build.outputs.digest"} {
		text := verify.Run
		if required == "steps.build.outputs.digest" {
			// The digest is carried by IMAGE_REF in the step environment, which
			// is intentionally outside Run; inspect the source workflow too.
			raw, err := os.ReadFile("../.github/workflows/release.yaml")
			if err != nil {
				t.Fatal(err)
			}
			text = string(raw)
		}
		if !strings.Contains(text, required) {
			t.Fatalf("manifest verification missing %q", required)
		}
	}
}

func TestSixImageReleaseAndOfflineImportInventoriesMatch(t *testing.T) {
	wantVars := []string{"IMG_AGENT", "IMG_FRONTEND", "IMG_MIGRATE", "IMG_SERVER", "IMG_SHELL", "IMG_WORKER"}

	makeBytes, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(makeBytes)
	assertStringSet(t, makeImageVariables(makeTargetRecipe(t, makefile, "k3d-import-all")), wantVars, "Make k3d-import-all image inventory")
	allTarget := makeTargetLine(t, makefile, "docker-build-all")
	wantTargets := []string{"docker-build-agent", "docker-build-frontend", "docker-build-migrate", "docker-build-server", "docker-build-shell", "docker-build-worker"}
	assertStringSet(t, strings.Fields(strings.TrimSpace(strings.SplitN(strings.SplitN(allTarget, ":", 2)[1], "##", 2)[0])), wantTargets, "Make docker-build-all dependency inventory")

	bootstrapBytes, err := os.ReadFile("../scripts/k3d-bootstrap.sh")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := string(bootstrapBytes)
	importStart := strings.Index(bootstrap, "k3d image import \\")
	if importStart < 0 {
		t.Fatal("k3d bootstrap has no image import")
	}
	importEnd := strings.Index(bootstrap[importStart:], `-c "${CLUSTER}"`)
	if importEnd < 0 {
		t.Fatal("k3d bootstrap image import has no cluster terminator")
	}
	assertStringSet(t, shellImageVariables(bootstrap[importStart:importStart+importEnd]), wantVars, "k3d bootstrap image inventory")
	for _, imageVar := range wantVars {
		if !strings.Contains(bootstrap, imageVar+`="${IMG_REGISTRY}/`) {
			t.Fatalf("k3d bootstrap %s is not registry-qualified", imageVar)
		}
	}
	if strings.Contains(bootstrap, "bitnami/kubectl") {
		t.Fatal("k3d bootstrap still preloads obsolete bitnami/kubectl instead of astronomer-shell")
	}
	for _, required := range []string{
		`make IMG_TAG="${IMG_TAG}" IMG_REGISTRY="${IMG_REGISTRY}" docker-build-all`,
		`--set preflight.image.registry="${IMG_REGISTRY}"`,
		`--set frontend.image.registry="${IMG_REGISTRY}"`,
	} {
		if !strings.Contains(bootstrap, required) {
			t.Fatalf("k3d bootstrap missing registry/import contract %q", required)
		}
	}
}

func TestFrontendVersionIsBakedIntoShippedStaticOutput(t *testing.T) {
	dockerfileBytes, err := os.ReadFile("../frontend/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(dockerfileBytes)
	ordered := []string{
		"ARG VERSION=0.2.0-dev",
		"ENV NEXT_PUBLIC_APP_VERSION=$VERSION",
		"RUN npm run build",
		`grep -R -F -q -- "${VERSION}" .next/static`,
		"FROM base AS runner",
		"COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static",
	}
	last := -1
	for _, required := range ordered {
		at := strings.Index(dockerfile, required)
		if at < 0 {
			t.Fatalf("frontend Dockerfile missing %q", required)
		}
		if at <= last {
			t.Fatalf("frontend build contract %q is out of order", required)
		}
		last = at
	}
	sidebar, err := os.ReadFile("../frontend/src/components/layout/sidebar.tsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sidebar), "process.env.NEXT_PUBLIC_APP_VERSION") {
		t.Fatal("shipped frontend does not consume NEXT_PUBLIC_APP_VERSION")
	}
}

func readReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	raw, err := os.ReadFile("../.github/workflows/release.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	return workflow
}

func releaseStepIndex(steps []struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Run  string         `yaml:"run"`
}, uses string) int {
	for index, step := range steps {
		if step.Uses == uses {
			return index
		}
	}
	return -1
}

func releaseNamedStep(t *testing.T, steps []struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Run  string         `yaml:"run"`
}, name string) struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Run  string         `yaml:"run"`
} {
	t.Helper()
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("release workflow has no step %q", name)
	return struct {
		Name string         `yaml:"name"`
		Uses string         `yaml:"uses"`
		With map[string]any `yaml:"with"`
		Run  string         `yaml:"run"`
	}{}
}

func makeImageVariables(recipe string) []string {
	matches := regexp.MustCompile(`\$\((IMG_[A-Z]+)\)`).FindAllStringSubmatch(recipe, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match[1])
	}
	return result
}

func shellImageVariables(script string) []string {
	matches := regexp.MustCompile(`\$\{(IMG_[A-Z]+)\}`).FindAllStringSubmatch(script, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match[1])
	}
	return result
}

func makeTargetLine(t *testing.T, makefile, target string) string {
	t.Helper()
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, target+":") {
			return line
		}
	}
	t.Fatalf("missing Make target %s", target)
	return ""
}

func assertStringSet(t *testing.T, got, want []string, label string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}
