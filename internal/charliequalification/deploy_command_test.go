package charliequalification

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandCandidateDeployerPassesOnlyImmutableCandidate(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "args")
	script := filepath.Join(directory, "deploy")
	contents := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" >\"$DEPLOY_TEST_OUTPUT\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEPLOY_TEST_OUTPUT", output)
	deployer, err := NewCommandCandidateDeployer(script, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	candidate := qualificationCandidate()
	if err = deployer.Deploy(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"--ref", candidate.Ref, "--commit", candidate.Commit, "--version", candidate.Version,
		"--central-image-digest", candidate.CentralImageDigest,
		"--agent-image-digest", candidate.AgentImageDigest,
		"--central-chart-digest", candidate.CentralChartDigest,
		"--agent-chart-digest", candidate.AgentChartDigest,
	}, "\n") + "\n"
	if string(arguments) != want {
		t.Fatalf("arguments = %q, want %q", arguments, want)
	}
}

func TestCommandCandidateDeployerRejectsWritableOrRelativeExecutable(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "deploy")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o722); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o722); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommandCandidateDeployer(script, time.Minute); err == nil {
		t.Fatal("group-writable command accepted")
	}
	if _, err := NewCommandCandidateDeployer("deploy", time.Minute); err == nil {
		t.Fatal("relative command accepted")
	}
}
