//go:build linux

package upgradecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// This fixture executes the actual chart/metadata signature and OCI-content
// path, private artifact reader and restore ownership machine. SQL/Kubernetes
// results are deliberately synthetic; it grants no live restore qualification.
type cnpgRestoreFixture struct {
	restore                                *CloudNativePGRestore
	source                                 *CloudNativePGSource
	state                                  *cnpgFixtureState
	present, createdDB, restoredDB         bool
	podUID                                 string
	mutation                               string
	createCalls, restoreCalls, deleteCalls int
}

func newCNPGRestoreFixture(t *testing.T) *cnpgRestoreFixture {
	t.Helper()
	chart := newLocalChartCryptoFixture(t, false, "17-cnpg")
	// Add the CNPG field to an independently rebuilt/signed chart using the
	// existing genuine fixture constructor's explicit CNPG variant.
	if _, _, err := chart.chart.CNPGImage(context.Background()); err != nil {
		t.Fatal(err)
	}
	source, state := cloudNativePGFixture(t, 17)
	backup, _, err := source.Dump(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := &cnpgRestoreFixture{source: source, state: state, podUID: "verify-original"}
	original := source.fence.execute
	source.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if len(args) > 6 && args[4] == "get" && args[5] == "pod" && args[6] == "astronomer-upgrade-verify-fixture" {
			if !f.present {
				return nil, nil
			}
			fields := []string{f.podUID, "astronomer-upgrade-verify-fixture", source.fence.options.Identity.Namespace, "upgrade-fixture", "live", "0", "1", f.restore.image, "Running", "verify=emptydir=1Gi", "false", "0", "0", "/bin/sh|-ec|;" + cnpgVerifierStartScript + "|", "POSTGRES_DB=postgres|POSTGRES_USER=postgres|PGHOST=/var/lib/postgresql/upgrade-verifier|PGDATA=/var/lib/postgresql/upgrade-verifier/data|TMPDIR=/var/lib/postgresql/upgrade-verifier|", "26", "26", "true", "1Gi", "1Gi", "verify=/var/lib/postgresql=readwrite|", "Never", "26", "RuntimeDefault", "false", "ALL|", "true"}
			switch f.mutation {
			case "volume":
				fields[9] = "verify=foreign"
			case "image":
				fields[7] = "foreign/image@sha256:" + strings.Repeat("a", 64)
			case "owner":
				fields[3] = "foreign"
			case "token":
				fields[10] = "<no value>"
			case "mount":
				fields[20] = "verify=/foreign=readwrite|"
			case "restart":
				fields[21] = "Always"
			case "group":
				fields[22] = "0"
			case "seccomp":
				fields[23] = "Unconfined"
			case "escalation":
				fields[24] = "true"
			case "capabilities":
				fields[25] = "NET_ADMIN|"
			case "rootfs":
				fields[26] = "false"
			case "root":
				fields[15] = "0"
			case "limit":
				fields[19] = "2Gi"
			}
			return []byte(strings.Join(fields, "\t")), nil
		}
		if len(args) > 4 && args[4] == "wait" {
			return nil, ctx.Err()
		}
		if len(args) > 4 && args[4] == "exec" && slices.Contains(args, "astronomer-upgrade-verify-fixture") {
			if slices.Contains(args, temporaryDropScript) {
				t.Fatal("isolated verifier cleanup must not execute SQL DROP")
			}
			if slices.Contains(args, temporaryIdentityScript) {
				if f.createdDB {
					return []byte("700|t|t\n"), nil
				}
				return nil, nil
			}
			if slices.Contains(args, temporaryCreateScript) {
				f.createdDB = true
				return nil, nil
			}
			if slices.Contains(args, temporarySchemaScript) {
				if !f.restoredDB {
					return nil, errors.New("restore never consumed archive")
				}
				return []byte("astronomer_upgrade_verify_fixture|17|1|78|f\n"), nil
			}
			t.Fatal("unexpected verifier SQL command", args)
		}
		if slices.Contains(args, temporaryCreateScript) || slices.Contains(args, temporaryRestoreScript) || slices.Contains(args, temporaryDropScript) {
			t.Fatal("mutation attempted on a source CNPG member")
		}
		return original(ctx, tool, args)
	}
	r, err := NewCloudNativePGRestore(context.Background(), chart.chart, backup, CloudNativePGRestoreOptions{Pod: "astronomer-upgrade-verify-fixture", Owner: "upgrade-fixture", Database: "astronomer_upgrade_verify_fixture", MaximumStorageBytes: 1 << 30, WaitSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	f.restore = r
	r.input = func(ctx context.Context, tool Tool, args []string, input *process.ArtifactCapture, maximum int64) ([]byte, process.Result, error) {
		if err := verifyArtifactInput(ctx, source.fence.commands.options.CaptureDirectory, input, maximum); err != nil {
			return nil, process.Result{}, err
		}
		raw, err := os.ReadFile(input.Path)
		if err != nil {
			return nil, process.Result{}, err
		}
		result := process.Result{}
		if slices.Contains(args, "create") {
			var pod corev1.Pod
			if json.Unmarshal(raw, &pod) != nil || pod.Spec.Volumes[0].EmptyDir == nil || pod.Spec.Volumes[0].PersistentVolumeClaim != nil || pod.Spec.SecurityContext.RunAsUser == nil || *pod.Spec.SecurityContext.RunAsUser != 26 || pod.Spec.SecurityContext.RunAsGroup == nil || *pod.Spec.SecurityContext.RunAsGroup != 26 || pod.Spec.SecurityContext.SeccompProfile == nil || pod.Spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault || pod.Spec.Containers[0].SecurityContext == nil || pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation == nil || *pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation || pod.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem == nil || !*pod.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem || pod.Spec.Containers[0].SecurityContext.Capabilities == nil || !slices.Equal(pod.Spec.Containers[0].SecurityContext.Capabilities.Drop, []corev1.Capability{"ALL"}) || !slices.Equal(pod.Spec.Containers[0].Command, []string{"/bin/sh", "-ec"}) || pod.Spec.Containers[0].Image != r.image || pod.Name != r.options.Pod {
				t.Fatal("actual private Pod manifest differs")
			}
			f.createCalls++
			f.present = true
			return nil, result, nil
		}
		if slices.Contains(args, "delete") {
			var options metav1.DeleteOptions
			if json.Unmarshal(raw, &options) != nil || options.Preconditions == nil || options.Preconditions.UID == nil || string(*options.Preconditions.UID) != f.podUID {
				t.Fatal("delete lacks captured UID precondition")
			}
			f.deleteCalls++
			f.present = false
			return nil, result, nil
		}
		if !slices.Contains(args, r.options.Pod) {
			t.Fatal("archive stdin sent to source CNPG member")
		}
		if slices.Contains(args, archiveListScript) {
			return []byte("; synthetic readable archive listing\n"), result, nil
		}
		if slices.Contains(args, temporaryRestoreScript) {
			f.restoreCalls++
			if f.mutation == "restore-failure" {
				return nil, result, errors.New("synthetic failed restore")
			}
			f.restoredDB = true
			return nil, result, nil
		}
		t.Fatal("unexpected private input command", args)
		return nil, result, nil
	}
	return f
}
func TestCNPGOwnedRestoreUsesAuthenticatedSameMajorIndependentPodAndUIDCleanup(t *testing.T) {
	f := newCNPGRestoreFixture(t)
	if err := f.restore.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.createCalls != 1 || f.restoreCalls != 1 || !f.restore.temporary.restored || !f.present {
		t.Fatal("actual owned restore machine did not complete")
	}
	// Source rollover after Apply does not authorize SQL DROP or invalidate
	// independent Pod retirement. Original source backup binding stays unchanged.
	originalUID := f.restore.backup.primaryUID
	f.state.memberUID = "target-recreated-member"
	if err := f.restore.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.deleteCalls != 1 || f.present || f.restore.backup.primaryUID != originalUID {
		t.Fatal("owned Pod cleanup or original source anchor changed")
	}
	if raw, err := os.ReadFile(f.restore.backup.artifact.Path); err != nil || len(raw) == 0 {
		t.Fatal("private original dump lost", err)
	}
}
func TestCNPGOwnedRestoreRejectsForeignPodSpecAndRecreatedUID(t *testing.T) {
	for _, changed := range []string{"volume", "image", "owner", "token", "mount", "restart", "group", "seccomp", "escalation", "capabilities", "rootfs", "root", "limit", "uid"} {
		t.Run(changed, func(t *testing.T) {
			f := newCNPGRestoreFixture(t)
			if err := f.restore.Verify(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.mutation = changed
			if changed == "uid" {
				f.podUID = "recreated"
			}
			if err := f.restore.Cleanup(context.Background()); err == nil || f.deleteCalls != 0 {
				t.Fatal("foreign verifier deletion admitted")
			}
		})
	}
}
func TestCNPGOwnedRestoreFailureAndCancellationRetainDumpAndAttemptedPod(t *testing.T) {
	for _, changed := range []string{"restore-failure", "canceled-input"} {
		t.Run(changed, func(t *testing.T) {
			f := newCNPGRestoreFixture(t)
			f.mutation = changed
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := f.restore.input
			if changed == "canceled-input" {
				f.restore.input = func(ctx context.Context, tool Tool, args []string, input *process.ArtifactCapture, maximum int64) ([]byte, process.Result, error) {
					if slices.Contains(args, temporaryRestoreScript) {
						cancel()
						return nil, process.Result{}, ctx.Err()
					}
					return original(ctx, tool, args, input, maximum)
				}
			}
			err := f.restore.Verify(ctx)
			if err == nil || f.deleteCalls != 0 || !f.present || !f.restore.creationAttempted {
				t.Fatal("failed restore discarded ownership/recovery")
			}
			if changed == "canceled-input" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost", err)
			}
			if _, err := os.Stat(f.restore.backup.artifact.Path); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCNPGOwnedRestoreRefusesExistingPodAndMismatchedMajorBeforeCreation(t *testing.T) {
	f := newCNPGRestoreFixture(t)
	f.present = true
	if err := f.restore.Verify(context.Background()); err == nil || f.createCalls != 0 {
		t.Fatal("preexisting verifier admitted")
	}
	copy := *f.restore.backup
	copy.schema.Major = 18
	if _, err := NewCloudNativePGRestore(context.Background(), f.restore.chart, &copy, f.restore.options); err == nil {
		t.Fatal("different major admitted")
	}
	if f.state.dumpCalls != 1 {
		t.Fatal(fmt.Sprintf("unexpected source dumps: %d", f.state.dumpCalls))
	}
}
