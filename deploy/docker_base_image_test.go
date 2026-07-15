package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileBaseImagesAreDigestPinned(t *testing.T) {
	dockerfiles := []string{
		"docker/Dockerfile.agent",
		"docker/Dockerfile.migrate",
		"docker/Dockerfile.server",
		"docker/Dockerfile.shell",
		"docker/Dockerfile.worker",
		"nginx/Dockerfile.nginx",
		"../frontend/Dockerfile",
	}

	for _, dockerfile := range dockerfiles {
		t.Run(dockerfile, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(dockerfile))
			if err != nil {
				t.Fatalf("read Dockerfile: %v", err)
			}
			stageNames := map[string]bool{}
			for lineNo, line := range strings.Split(string(raw), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 2 || strings.ToUpper(fields[0]) != "FROM" {
					continue
				}
				image := fields[1]
				if strings.Contains(image, ":latest") {
					t.Fatalf("%s:%d uses floating latest base image %q", dockerfile, lineNo+1, image)
				}
				if image == "scratch" || stageNames[image] {
					continue
				}
				if !strings.Contains(image, "@sha256:") {
					t.Fatalf("%s:%d external base image %q is not digest pinned", dockerfile, lineNo+1, image)
				}
				if len(fields) >= 4 && strings.EqualFold(fields[2], "AS") {
					stageNames[fields[3]] = true
				}
			}
		})
	}
}

func TestShellImageContainsOfflineDexPreflightToolchain(t *testing.T) {
	raw, err := os.ReadFile("docker/Dockerfile.shell")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		"COPY cmd/dexconfigcheck", "COPY internal/dexconfig", "CGO_ENABLED=0 go build", "apk add --no-cache", "coreutils", "jq", "COPY --from=dex-validator", "dexconfigcheck --version", "base64 --version",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("offline preflight toolchain missing %q", required)
		}
	}
	if strings.Contains(text, "curl | sh") || strings.Contains(text, "wget | sh") {
		t.Fatal("shell image performs an undeclared runtime tool download")
	}
}

func TestShellDockerfileCopiesDexValidatorLocalDependencyClosure(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", `{{if .Module}}{{if eq .Module.Path "github.com/alphabravocompany/astronomer-go"}}{{.Dir}}{{end}}{{end}}`, "./cmd/dexconfigcheck")
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list dexconfigcheck dependencies: %v", err)
	}
	dockerfile, err := os.ReadFile("docker/Dockerfile.shell")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range strings.Fields(string(out)) {
		rel, err := filepath.Rel(root, directory)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains(string(dockerfile), "COPY "+rel+" ./"+rel) {
			t.Fatalf("Dockerfile.shell does not copy local dexconfigcheck dependency %s", rel)
		}
	}
}

func TestFirstPartyDockerfilesDeclareFinalImageIdentity(t *testing.T) {
	dockerfiles := []string{
		"docker/Dockerfile.agent",
		"docker/Dockerfile.migrate",
		"docker/Dockerfile.server",
		"docker/Dockerfile.shell",
		"docker/Dockerfile.worker",
		"../frontend/Dockerfile",
	}
	for _, dockerfile := range dockerfiles {
		t.Run(dockerfile, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(dockerfile))
			if err != nil {
				t.Fatal(err)
			}
			upper := strings.ToUpper(string(raw))
			lastFrom := strings.LastIndex(upper, "\nFROM ")
			if lastFrom < 0 && strings.HasPrefix(upper, "FROM ") {
				lastFrom = 0
			}
			if lastFrom < 0 {
				t.Fatal("missing final FROM stage")
			}
			finalStage := string(raw)[lastFrom:]
			for _, required := range []string{
				"ARG VERSION=", "ARG GIT_COMMIT=", "ARG BUILD_DATE=",
				`org.opencontainers.image.source="https://github.com/alphabravo-oss/astronomer"`,
				`org.opencontainers.image.revision="${GIT_COMMIT}"`,
				`org.opencontainers.image.version="${VERSION}"`,
				`org.opencontainers.image.created="${BUILD_DATE}"`,
			} {
				if !strings.Contains(finalStage, required) {
					t.Fatalf("final image stage missing %q", required)
				}
			}
		})
	}
}
