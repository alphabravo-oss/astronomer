package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestK3DBootstrapPinsSupportedGatewayAPIBundle(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "k3d-bootstrap.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read k3d bootstrap: %v", err)
	}
	script := string(scriptBytes)

	for _, want := range []string{
		`GW_API_VER="${GW_API_VER:-v1.4.1}"`,
		`releases/download/${GW_API_VER}/standard-install.yaml`,
		`default: v1.4.1`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("k3d bootstrap missing pinned Gateway API contract %q", want)
		}
	}
	for _, forbidden := range []string{
		"v1.3.0",
		"releases/latest/",
		"/main/config/crd/",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("k3d bootstrap contains unpinned or stale Gateway API source %q", forbidden)
		}
	}

	for _, relative := range []string{
		filepath.Join("deploy", "chart", "README.md"),
		filepath.Join("docs", "upgrade-runbook.md"),
	} {
		contents, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		text := string(contents)
		if !strings.Contains(text, "v1.4.1") {
			t.Errorf("%s does not document the pinned Gateway API bundle", relative)
		}
		if strings.Contains(text, "kubernetes-sigs/gateway-api/releases/latest/") {
			t.Errorf("%s recommends a moving Gateway API release URL", relative)
		}
	}
}
