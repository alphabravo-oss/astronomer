package upgrade

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

func helmFixture(t *testing.T, historical bool) HelmInputs {
	t.Helper()
	input := HelmInputs{Tool: HelmToolBinding{Path: "/private/verified-tools/helm", Version: "v3.21.4", SHA256: "sha256:" + strings.Repeat("d", 64)}, Plan: testPlan(), ChartReference: "oci://ghcr.io/alphabravo-oss/charts/astronomer", ManifestFile: "/private/operation/release-manifest.json", RuntimeSources: map[string]string{}}
	if historical {
		input.Plan.Target, input.Plan.Previous = "v1.1.9", "v1.1.8"
	}
	input.Manifest.Release.Version = input.Plan.Target
	for _, row := range []struct{ name, repository string }{{"server", "astronomer-go-server"}, {"worker", "astronomer-go-worker"}, {"agent", "astronomer-go-agent"}, {"frontend", "astronomer-frontend"}, {"shell", "astronomer-shell"}} {
		input.Manifest.Astronomer.Images = append(input.Manifest.Astronomer.Images, releasecontract.Artifact{Name: row.name, Kind: "container_image", Reference: "ghcr.io/alphabravo-oss/" + row.repository + "@sha256:" + strings.Repeat("a", 64)})
	}
	if !historical {
		input.Manifest.Astronomer.Images = append(input.Manifest.Astronomer.Images, releasecontract.Artifact{Name: "dr", Kind: "container_image", Reference: "ghcr.io/alphabravo-oss/astronomer-dr@sha256:" + strings.Repeat("b", 64)})
	}
	for _, role := range []string{"utilities.busybox", "postgres", "redis", "dex", "managementRestoreDrill.sidecar", "managementLogging"} {
		source := "example.invalid/fixture/" + strings.ToLower(strings.ReplaceAll(role, ".", "-")) + ":qualified"
		input.RuntimeSources[role] = source
		input.Manifest.Astronomer.RuntimeImages = append(input.Manifest.Astronomer.RuntimeImages, releasecontract.RuntimeImage{SourceReference: source, Reference: strings.TrimSuffix(source, ":qualified") + "@sha256:" + strings.Repeat("c", 64)})
	}
	return input
}
func settings(args []string) map[string]string {
	result := map[string]string{}
	for i, value := range args {
		if value == "--set-string" && i+1 < len(args) {
			key, val, _ := strings.Cut(args[i+1], "=")
			result[key] = val
		}
	}
	return result
}
func TestHelmUpgradeArgumentsRetainHistoricalSixAndCandidateSevenImages(t *testing.T) {
	for _, historical := range []bool{true, false} {
		in := helmFixture(t, historical)
		dry, err := in.DryRunArguments()
		if err != nil {
			t.Fatal(err)
		}
		values := settings(dry)
		if values["image.server.digest"] != "sha256:"+strings.Repeat("a", 64) || values["config.agentImageRepository"] != "ghcr.io/alphabravo-oss/astronomer-go-agent@sha256:"+strings.Repeat("a", 64) || values["managementRestoreDrill.sidecar.image.digest"] != "sha256:"+strings.Repeat("c", 64) {
			t.Fatal("immutable image/agent/runtime roles lost")
		}
		if (values["managementBackup.image.digest"] == "") != historical {
			t.Fatal("historical six/candidate seven contract differs")
		}
		if !reflect.DeepEqual(dry[len(dry)-2:], []string{"--dry-run=server", "--hide-secret"}) {
			t.Fatal("server-side/private preview contract lost")
		}
		apply, err := in.ApplyArguments()
		if err != nil || !reflect.DeepEqual(apply[len(apply)-4:], []string{"--atomic", "--cleanup-on-fail", "--wait", "--wait-for-jobs"}) {
			t.Fatal("atomic observation contract lost")
		}
		in.Plan.Mode = Preview
		if _, err = in.ApplyArguments(); err == nil {
			t.Fatal("preview constructed mutation")
		}
	}
}
func TestHelmUpgradeArgumentsRejectUnboundImagesAndChart(t *testing.T) {
	for _, kind := range []string{"unknown_role", "duplicate", "mutable", "wrong_repository", "wrong_version", "chart_tag", "chart_auth", "missing_runtime"} {
		t.Run(kind, func(t *testing.T) {
			in := helmFixture(t, false)
			switch kind {
			case "unknown_role":
				in.Manifest.Astronomer.Images[0].Name = "unowned"
			case "duplicate":
				in.Manifest.Astronomer.Images[0] = in.Manifest.Astronomer.Images[1]
			case "mutable":
				in.Manifest.Astronomer.Images[0].Reference = "ghcr.io/alphabravo-oss/astronomer-go-server:latest"
			case "wrong_repository":
				in.Manifest.Astronomer.Images[0].Reference = "foreign.invalid/server@sha256:" + strings.Repeat("a", 64)
			case "wrong_version":
				in.Manifest.Release.Version = "v1.2.1"
			case "chart_tag":
				in.ChartReference += ":latest"
			case "chart_auth":
				in.ChartReference = "oci://user:password@example.invalid/chart"
			case "missing_runtime":
				delete(in.RuntimeSources, "postgres")
			}
			if _, err := in.arguments(); err == nil {
				t.Fatal("unbound release construction admitted")
			}
		})
	}
}

func TestHelmFourArgumentsKeepLegacyWaitAndClientSideApplyWithBoundTool(t *testing.T) {
	in := helmFixture(t, false)
	in.Tool.Version = "v4.3.0" // Primary CLI source fixture, not policy selection.
	args, err := in.ApplyArguments()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args[len(args)-4:], []string{"--rollback-on-failure", "--cleanup-on-fail", "--wait=legacy", "--wait-for-jobs"}) {
		t.Fatal("Helm4 accidentally selected watcher or deprecated atomic semantics")
	}
	found := false
	for _, arg := range args {
		if arg == "--server-side=false" {
			found = true
		}
		if arg == "--atomic" || arg == "--wait" {
			t.Fatal("implicit Helm4 semantics")
		}
	}
	if !found {
		t.Fatal("Helm4 update ownership differs from qualified v3 contract")
	}
	for _, tool := range []HelmToolBinding{{}, {Path: "helm", Version: "v4.3.0", SHA256: in.Tool.SHA256}, {Path: in.Tool.Path, Version: "v4.3.0-rc.1", SHA256: in.Tool.SHA256}, {Path: in.Tool.Path, Version: "v5.0.0", SHA256: in.Tool.SHA256}, {Path: in.Tool.Path, Version: in.Tool.Version, SHA256: "sha256:invalid"}, {Path: in.Tool.Path, Version: "v4.3.0", SHA256: "latest"}, {Path: in.Tool.Path, Version: "v4.3.0", SHA256: ""}} {
		in.Tool = tool
		if _, err := in.ApplyArguments(); err == nil {
			t.Fatal("unbound or uncharacterized executable admitted")
		}
	}
}
