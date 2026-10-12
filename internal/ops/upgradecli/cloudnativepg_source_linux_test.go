//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

type cnpgFixtureState struct {
	primary                                                                    int
	clusterUID, pvcUID, pvUID, serviceUID, secretUID, secretVersion, memberUID string
	major                                                                      uint32
	dumpCalls                                                                  int
}

func cloudNativePGFixture(t *testing.T, selectedMajor ...uint32) (*CloudNativePGSource, *cnpgFixtureState) {
	t.Helper()
	major := uint32(18)
	if len(selectedMajor) > 1 {
		t.Fatal("one fixture major required")
	}
	if len(selectedMajor) == 1 {
		major = selectedMajor[0]
	}
	fence, _ := fenceFixture(t)
	original := fence.execute
	view := &cnpgFixtureState{primary: 1, clusterUID: "cluster-original", pvcUID: "pvc-1", pvUID: "pv-1", serviceUID: "service-original", secretUID: "secret-original", secretVersion: "42", memberUID: "pod-1", major: major}
	ns, release := fence.options.Identity.Namespace, fence.options.Identity.Release
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if len(args) < 7 {
			return nil, errors.New("short fixture command")
		}
		if args[4] == "exec" {
			if args[len(args)-3] != cnpgStateScript || args[len(args)-1] != "owned_live" {
				t.Fatal("CNPG SQL must use local peer identity and fixed database positional")
			}
			return []byte(fmt.Sprintf("owned_live|%d|1|78|f\n", view.major)), nil
		}
		if args[4] != "get" {
			return original(ctx, tool, args)
		}
		row := func(fields ...string) ([]byte, error) { return []byte(strings.Join(fields, "\t")), nil }
		switch args[5] {
		case "clusters.postgresql.cnpg.io":
			return row(view.clusterUID, "owned-cnpg", ns, release, "live", "3", "3", fmt.Sprintf("owned-cnpg-%d", view.primary), fmt.Sprintf("owned-cnpg-%d", view.primary), fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%d.3", view.major))
		case "pods":
			var data strings.Builder
			for i := 1; i <= 3; i++ {
				uid := fmt.Sprintf("pod-%d", i)
				if i == 1 {
					uid = view.memberUID
				}
				fmt.Fprintf(&data, "%s\towned-cnpg-%d\t%s\towned-cnpg\t%s\tlive\tRunning\tready\tdata-%d,\n", uid, i, ns, view.clusterUID, i)
			}
			return []byte(data.String()), nil
		case "persistentvolumeclaims":
			i := strings.TrimPrefix(args[6], "data-")
			uid := "pvc-" + i
			if i == "1" {
				uid = view.pvcUID
			}
			return row(uid, args[6], ns, view.clusterUID, "live", "Bound", "volume-"+i)
		case "persistentvolumes":
			i := strings.TrimPrefix(args[6], "volume-")
			uid, pvc := "pv-"+i, "pvc-"+i
			if i == "1" {
				uid, pvc = view.pvUID, view.pvcUID
			}
			return row(uid, args[6], "live", "Bound", pvc, ns, "data-"+i)
		case "services":
			return row(view.serviceUID, "owned-cnpg-rw", ns, view.clusterUID, "live", "owned-cnpg", "primary")
		case "endpoints":
			uid := fmt.Sprintf("pod-%d", view.primary)
			if view.primary == 1 {
				uid = view.memberUID
			}
			raw, _ := row(uid, fmt.Sprintf("owned-cnpg-%d", view.primary), ns, "Pod")
			return append(raw, '\n'), nil
		case "secrets":
			if args[len(args)-1] != "go-template="+cnpgSecretTemplate {
				t.Fatal("CNPG observation may read only Secret metadata")
			}
			return row(view.secretUID, "owned-cnpg-app", ns, view.clusterUID, "live", view.secretVersion)
		default:
			return original(ctx, tool, args)
		}
	}
	source, err := NewCloudNativePGSource(context.Background(), fence, CloudNativePGOptions{Cluster: "owned-cnpg", Database: "owned_live", ApplicationSecret: "owned-cnpg-app", ExpectedMajor: major, MinimumSchema: 66, TargetSchema: 78, ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	source.capture = func(ctx context.Context, tool Tool, args []string, policy ArtifactPolicy) (*process.ArtifactCapture, process.Result, error) {
		view.dumpCalls++
		if !slices.Equal(args, source.arguments(fmt.Sprintf("owned-cnpg-%d", view.primary), cnpgDumpScript)) {
			t.Fatal("CNPG dump argv/primary differs")
		}
		result := artifactFixture(t, fence.commands.options.CaptureDirectory, "cnpg-"+strconv.Itoa(view.dumpCalls)+".stdout", []byte("synthetic archive; no real PostgreSQL credit"))
		return result.StdoutArtifact, result, nil
	}
	return source, view
}
func TestCNPGSourceAdmitsCapturedPrimaryHandoffAndRetainsBoundBackup(t *testing.T) {
	source, view := cloudNativePGFixture(t)
	view.primary = 2
	backup, result, err := source.Dump(context.Background())
	if err != nil || backup == nil || backup.primary != "owned-cnpg-2" || backup.primaryUID != "pod-2" || backup.source != source || backup.schema.Version != 78 || view.dumpCalls != 1 {
		t.Fatal("captured member handoff or bound backup failed", err)
	}
	if result.StdoutArtifact == nil || !result.StdoutArtifact.Acknowledged {
		t.Fatal("private retained dump missing")
	}
}
func TestCNPGSourceRejectsRecreatedPersistentAndOperatorIdentitiesBeforeDump(t *testing.T) {
	for _, changed := range []string{"cluster", "pvc", "pv", "service", "secret", "secret-version", "member"} {
		t.Run(changed, func(t *testing.T) {
			source, view := cloudNativePGFixture(t)
			switch changed {
			case "cluster":
				view.clusterUID = "recreated"
			case "pvc":
				view.pvcUID = "recreated"
			case "pv":
				view.pvUID = "recreated"
			case "service":
				view.serviceUID = "recreated"
			case "secret":
				view.secretUID = "recreated"
			case "secret-version":
				view.secretVersion = "43"
			case "member":
				view.memberUID = "recreated"
			}
			backup, _, err := source.Dump(context.Background())
			if err == nil || backup != nil || view.dumpCalls != 0 {
				t.Fatal("recreated CNPG owner admitted dump")
			}
		})
	}
}
func TestCNPGSourceRejectsInFlightPrimaryHandoffAndPreservesPrivateCapture(t *testing.T) {
	source, view := cloudNativePGFixture(t)
	original := source.capture
	source.capture = func(ctx context.Context, tool Tool, args []string, policy ArtifactPolicy) (*process.ArtifactCapture, process.Result, error) {
		artifact, result, err := original(ctx, tool, args, policy)
		view.primary = 2
		return artifact, result, err
	}
	backup, result, err := source.Dump(context.Background())
	if err == nil || backup != nil || view.dumpCalls != 1 || result.StdoutArtifact == nil {
		t.Fatal("in-flight primary change acknowledged")
	}
	raw, readErr := os.ReadFile(result.StdoutArtifact.Path)
	if readErr != nil || len(raw) == 0 {
		t.Fatal("original capture not retained", readErr)
	}
}
func TestCNPGSourceRejectsUnreadyOrUnboundEndpointsAndCancellation(t *testing.T) {
	for _, changed := range []string{"notready", "endpoint", "dirty", "cancel"} {
		t.Run(changed, func(t *testing.T) {
			source, view := cloudNativePGFixture(t)
			original := source.read
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source.read = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				raw, err := original(ctx, tool, args)
				if err != nil {
					return nil, err
				}
				if args[4] == "get" && args[5] == "pods" && changed == "notready" {
					return []byte(strings.Replace(string(raw), "\tready\t", "\tnotready\t", 1)), nil
				}
				if args[4] == "get" && args[5] == "endpoints" && changed == "endpoint" {
					return []byte("foreign\tforeign\tforeign\tPod\n"), nil
				}
				if args[4] == "exec" {
					if changed == "dirty" {
						return []byte("owned_live|18|1|78|t\n"), nil
					}
					if changed == "cancel" {
						cancel()
					}
				}
				return raw, nil
			}
			backup, _, err := source.Dump(ctx)
			if err == nil || backup != nil || view.dumpCalls != 0 {
				t.Fatal("invalid CNPG observation launched dump")
			}
			if changed == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost", err)
			}
		})
	}
}
