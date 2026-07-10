package deploy

import (
	"bytes"
	"os"
	"os/exec"
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
		`NGF_VERSION="${NGF_VERSION:-2.6.0}"`,
		`"${NGF_VERSION}:${GW_API_VER}" != "2.6.0:v1.4.1"`,
		`releases/download/${GW_API_VER}/standard-install.yaml`,
		`wait_for_gatewayclass nginx 30 2`,
		`default: v1.4.1`,
		`default: 2.6.0`,
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
		if !strings.Contains(text, "2.6.0") {
			t.Errorf("%s does not document the paired NGF version", relative)
		}
		if strings.Contains(text, "kubernetes-sigs/gateway-api/releases/latest/") {
			t.Errorf("%s recommends a moving Gateway API release URL", relative)
		}
	}
}

func TestGatewayClassReadinessRequiresCurrentAcceptedAndSupportedConditions(t *testing.T) {
	for _, tt := range []struct {
		name       string
		snapshots  []string
		attempts   string
		wantOK     bool
		wantOutput []string
	}{
		{
			name:      "ready immediately",
			snapshots: []string{"3\tAccepted=True/3;SupportedVersion=True/3;"},
			attempts:  "1",
			wantOK:    true,
			wantOutput: []string{
				"generation=3",
				"Accepted=True",
				"SupportedVersion=True",
			},
		},
		{
			name: "stale then current",
			snapshots: []string{
				"4\tAccepted=True/3;SupportedVersion=True/3;",
				"4\tAccepted=True/4;SupportedVersion=True/4;",
			},
			attempts: "2",
			wantOK:   true,
			wantOutput: []string{
				"generation=4",
				"SupportedVersion=True",
			},
		},
		{
			name:      "accepted alone fails closed",
			snapshots: []string{"5\tAccepted=True/5;"},
			attempts:  "2",
			wantOutput: []string{
				"after 2 attempts",
				"Accepted=True observedGeneration=5",
				"SupportedVersion=Missing",
			},
		},
		{
			name:      "unsupported fails closed",
			snapshots: []string{"6\tAccepted=True/6;SupportedVersion=False/6;"},
			attempts:  "1",
			wantOutput: []string{
				"SupportedVersion=False observedGeneration=6",
			},
		},
		{
			name:      "stale true conditions fail closed",
			snapshots: []string{"7\tAccepted=True/6;SupportedVersion=True/6;"},
			attempts:  "1",
			wantOutput: []string{
				"generation=7",
				"Accepted=True observedGeneration=6",
				"SupportedVersion=True observedGeneration=6",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output, err := runGatewayClassReadiness(t, tt.snapshots, tt.attempts)
			if tt.wantOK && err != nil {
				t.Fatalf("readiness failed: %v\n%s", err, output)
			}
			if !tt.wantOK && err == nil {
				t.Fatalf("readiness unexpectedly succeeded:\n%s", output)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("readiness output missing %q:\n%s", want, output)
				}
			}
		})
	}
}

func TestGatewayClassReadinessRejectsUnboundedOrMalformedAttempts(t *testing.T) {
	for _, attempts := range []string{"0", "-1", "forever"} {
		output, err := runGatewayClassReadiness(t, []string{"1\tAccepted=True/1;SupportedVersion=True/1;"}, attempts)
		if err == nil {
			t.Errorf("attempts=%q unexpectedly succeeded: %s", attempts, output)
		}
		if !strings.Contains(output, "must be a positive integer") {
			t.Errorf("attempts=%q did not report bounded-input contract: %s", attempts, output)
		}
	}
}

func TestK3DBootstrapRejectsUntestedGatewayStackBeforeClusterMutation(t *testing.T) {
	root := repoRoot(t)
	tempDir := t.TempDir()
	marker := filepath.Join(tempDir, "unexpected-command")
	for _, name := range []string{"k3d", "kubectl", "docker", "helm"} {
		path := filepath.Join(tempDir, name)
		contents := "#!/usr/bin/env bash\nprintf '%s' \"$0 $*\" >\"${MUTATION_MARKER}\"\n"
		if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}

	cmd := exec.Command("bash", filepath.Join(root, "scripts", "k3d-bootstrap.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+tempDir+":"+os.Getenv("PATH"),
		"GW_API_VER=v1.5.0",
		"NGF_VERSION=2.6.0",
		"MUTATION_MARKER="+marker,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err == nil {
		t.Fatalf("bootstrap accepted an untested Gateway stack:\n%s", output.String())
	}
	for _, want := range []string{"unsupported Gateway stack", "2.6.0:v1.5.0", "NGF 2.6.0 + Gateway API v1.4.1"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("bootstrap rejection missing %q:\n%s", want, output.String())
		}
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("bootstrap invoked a cluster-mutating command before rejecting the pair (stat err=%v)", statErr)
	}
}

func runGatewayClassReadiness(t *testing.T, snapshots []string, attempts string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	tempDir := t.TempDir()
	snapshotPath := filepath.Join(tempDir, "snapshots")
	statePath := filepath.Join(tempDir, "state")
	if err := os.WriteFile(snapshotPath, []byte(strings.Join(snapshots, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write snapshots: %v", err)
	}
	kubectlPath := filepath.Join(tempDir, "kubectl")
	kubectl := `#!/usr/bin/env bash
set -euo pipefail
index=1
if [[ -f "${FAKE_KUBECTL_STATE}" ]]; then
  index=$(<"${FAKE_KUBECTL_STATE}")
fi
line=$(sed -n "${index}p" "${FAKE_KUBECTL_SNAPSHOTS}")
if [[ -z "${line}" ]]; then
  line=$(tail -n 1 "${FAKE_KUBECTL_SNAPSHOTS}")
fi
printf '%s' "$((index + 1))" >"${FAKE_KUBECTL_STATE}"
printf '%s' "${line}"
`
	if err := os.WriteFile(kubectlPath, []byte(kubectl), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}

	library := filepath.Join(root, "scripts", "lib", "gatewayclass-readiness.sh")
	cmd := exec.Command("bash", "-c", `source "$1"; wait_for_gatewayclass nginx "$2" 0`, "_", library, attempts)
	cmd.Env = append(os.Environ(),
		"PATH="+tempDir+":"+os.Getenv("PATH"),
		"FAKE_KUBECTL_SNAPSHOTS="+snapshotPath,
		"FAKE_KUBECTL_STATE="+statePath,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}
