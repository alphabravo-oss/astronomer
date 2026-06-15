package deploy

import (
	"os"
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
