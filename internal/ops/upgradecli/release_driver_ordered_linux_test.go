//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// These invoke all actual concrete Driver stages, real local crypto/private
// captures and a finite context-joined loopback readiness server. Kubernetes,
// Helm and SQL responses are explicit private command seams. They do not
// qualify official CLI execution, Kubernetes storage or PostgreSQL replacement.
type orderedDriverState struct {
	server, worker               uint32
	serverRV, workerRV           uint64
	temporary, replaced, applied bool
	failReplacement              bool
	applyCalls, sourceDrops      int
	forwardJoined                bool
}

func orderedReplacementDriverFixture(t *testing.T) (*ReleaseDriver, *orderedDriverState) {
	t.Helper()
	invocation, options, _ := invocationVerifiedRunFixture(t, "")
	old := invocation.driver
	commands := old.release.fence.commands
	fence := old.release.fence
	state := &orderedDriverState{server: 2, worker: 1, serverRV: 10, workerRV: 11}
	plan := old.plan
	plan.Mode = upgrade.Execute
	lease := upgrade.DisposableLease{NamespaceUID: plan.Identity.NamespaceUID, DatabasePodUID: options.Database.DatabasePodUID, DatabaseName: options.Database.Database, RestoreDatabaseName: options.Database.RestoreDatabase, Owner: options.Database.Owner}
	plan.Replace = &lease
	id := plan.Identity
	projected := func(fields ...string) []byte { return []byte(strings.Join(fields, "\t")) }
	oldRead := fence.execute
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		joined := strings.Join(args, "\n")
		if slices.Contains(args, replacementCatalogScript) {
			if state.replaced {
				if args[len(args)-2] == "astronomer-upgrade:"+lease.Owner {
					return []byte(lease.DatabaseName + "|700|t|t\n"), nil
				}
				return []byte(lease.DatabaseName + "|700|t|f\n"), nil
			}
			return []byte(lease.DatabaseName + "|600|t|t\n" + lease.RestoreDatabaseName + "|700|t|t\n"), nil
		}
		if slices.Contains(args, replacementExecuteScript) {
			if state.server != 0 || state.worker != 0 {
				t.Fatal("destructive gate ran before owned quiescence")
			}
			if state.failReplacement {
				return nil, errors.New("controlled failure after both owned workloads quiesced")
			}
			state.replaced = true
			state.temporary = false
			return nil, nil
		}
		if slices.Contains(args, temporaryIdentityScript) {
			if state.temporary {
				return []byte("700|t|t\n"), nil
			}
			return nil, nil
		}
		if slices.Contains(args, temporaryCreateScript) {
			state.temporary = true
			return nil, nil
		}
		if slices.Contains(args, temporaryDropScript) {
			if !slices.Contains(args, lease.RestoreDatabaseName) || !slices.Contains(args, "700") {
				state.sourceDrops++
				t.Fatal("DROP did not retain exact temporary OID/name")
			}
			state.temporary = false
			return nil, nil
		}
		if slices.Contains(args, temporarySchemaScript) {
			return []byte(lease.RestoreDatabaseName + "|17|1|78|f\n"), nil
		}
		if slices.Contains(args, databaseStateScript) {
			return []byte(lease.DatabaseName + "|17|1|78|f\n"), nil
		}
		if len(args) > 5 && args[4] == "auth" {
			return []byte("yes\n"), nil
		}
		if len(args) > 5 && args[4] == "scale" {
			count := uint32(0)
			for _, arg := range args {
				if strings.HasPrefix(arg, "--replicas=") {
					parsed, err := strconv.ParseUint(strings.TrimPrefix(arg, "--replicas="), 10, 32)
					if err != nil {
						t.Fatal(err)
					}
					count = uint32(parsed)
				}
			}
			if slices.Contains(args, "deployment/owned-server") {
				state.server = count
				state.serverRV++
			} else if slices.Contains(args, "deployment/owned-worker") {
				state.worker = count
				state.workerRV++
			} else {
				t.Fatal("foreign deployment scale")
			}
			return nil, nil
		}
		if len(args) > 5 && args[4] == "rollout" {
			return nil, nil
		}
		if len(args) > 5 && args[4] == "get" {
			template := args[len(args)-1]
			switch template {
			case "go-template=" + podTemplate:
				return projected(lease.DatabasePodUID, fence.options.DatabasePod, id.Namespace, lease.Owner, "live"), nil
			case "go-template=" + databasePodTemplate:
				return projected(lease.DatabasePodUID, fence.options.DatabasePod, id.Namespace, id.Release, "postgres", "live"), nil
			case "go-template=" + targetDatabasePodTemplate:
				return projected(lease.DatabasePodUID, fence.options.DatabasePod, id.Namespace, id.Release, "postgres", "astronomer", "astronomer", "live", "Running", "true", "apps/v1", "StatefulSet", "postgres", "controller-original", "data-"+fence.options.DatabasePod, "postgres", "/var/lib/postgresql/data"), nil
			case "go-template=" + targetDatabasePodsTemplate:
				raw, _ := fence.execute(ctx, tool, append(append([]string(nil), args[:len(args)-1]...), "go-template="+targetDatabasePodTemplate))
				return append(raw, '\n'), nil
			case "go-template=" + targetDatabaseControllerTemplate:
				return projected("controller-original", "postgres", id.Namespace, id.Release, "postgres", "astronomer", "live", "1", "2", "2", "revision", "revision", "1", "astronomer", id.Release, "postgres"), nil
			case "go-template=" + targetDatabaseClaimTemplate:
				return projected("claim-original", "data-"+fence.options.DatabasePod, id.Namespace, "live", "Bound", "volume-original"), nil
			case "go-template=" + targetDatabaseVolumeTemplate:
				return projected("pv-original", "volume-original", "live", "Bound", "claim-original", "data-"+fence.options.DatabasePod, id.Namespace), nil
			case "go-template=" + readyNodesTemplate:
				return []byte("1\n1\n1\n"), nil
			case "go-template=" + deploymentsReadyTemplate:
				return []byte(fmt.Sprintf("4\t4\t%d\t%d\t%d\n5\t5\t%d\t%d\t%d\n", state.server, state.server, state.server, state.worker, state.worker, state.worker)), nil
			case "go-template=" + replacementDeploymentsTemplate:
				return []byte(fmt.Sprintf("owned-server\tserver-original\t%s\tserver\t%d\t%d\tlive\nowned-worker\tworker-original\t%s\tworker\t%d\t%d\tlive\n", id.Namespace, state.server, state.serverRV, id.Namespace, state.worker, state.workerRV)), nil
			case "go-template=" + quiescedPDBTemplate:
				server, worker := 1, 1
				if state.server == 0 {
					server = 0
				}
				if state.worker == 0 {
					worker = 0
				}
				return []byte(fmt.Sprintf("server-pdb\tserver-pdb-original\t%s\t30\tlive\t%d\t3\t0\tastronomer\t%s\tserver\nworker-pdb\tworker-pdb-original\t%s\t31\tlive\t%d\t3\t0\tastronomer\t%s\tworker\n", id.Namespace, server, id.Release, id.Namespace, worker, id.Release)), nil
			case "go-template=" + disruptionTemplate:
				if state.server == 0 || state.worker == 0 {
					return []byte("0\n0\n"), nil
				}
				return []byte("1\n1\n"), nil
			case "go-template=" + workloadIdentityTemplate:
				return []byte("owned-server\tserver-original\t" + id.Namespace + "\nowned-worker\tworker-original\t" + id.Namespace + "\n"), nil
			case "go-template=" + readyzServiceTemplate:
				return []byte("owned-server\tservice-original\t" + id.Namespace + "\n"), nil
			}
		}
		_ = joined
		return oldRead(ctx, tool, args)
	}
	oldRun := commands.run
	commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
		if spec.Executable == options.GH.Path {
			return oldRun(ctx, spec)
		}
		if spec.Executable == options.Kubectl.Path && slices.Contains(spec.Args, "port-forward") {
			mapping := spec.Args[len(spec.Args)-1]
			port, _, ok := strings.Cut(mapping, ":")
			if !ok {
				return process.Result{}, errors.New("invalid owned tunnel mapping")
			}
			listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", port))
			if err != nil {
				return process.Result{}, err
			}
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			})}
			joined := make(chan struct{})
			go func() { defer close(joined); _ = server.Serve(listener) }()
			defer func() { _ = server.Close(); <-joined; state.forwardJoined = true }()
			path := filepath.Join(spec.CaptureDirectory, fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence))
			if err := os.WriteFile(path, []byte("Forwarding from 127.0.0.1:"+port+" -> 8000\n"), 0600); err != nil {
				return process.Result{}, err
			}
			<-ctx.Done()
			return process.Result{Kind: process.Cancelled, LeaderReaped: true}, ctx.Err()
		}
		var raw []byte
		if spec.Executable == options.Kubectl.Path {
			boundRead := func(args ...string) bool {
				base := []string{"--kubeconfig", fence.options.Kubeconfig, "--context", id.Context}
				return slices.Equal(spec.Args, append(base, args...))
			}
			secretName := "sh.helm.release.v1." + id.Release + ".v" + strconv.FormatUint(fence.options.Revision, 10)
			switch {
			case boundRead("config", "view", "--minify", "--raw", "--output", "jsonpath={.clusters[0].cluster.server}"):
				raw = []byte(id.APIServer)
			case boundRead("get", "namespace", id.Namespace, "--output", "go-template="+namespaceTemplate):
				raw = projected(id.NamespaceUID, id.Namespace, "live")
			case boundRead("get", "secret", secretName, "--namespace", id.Namespace, "--output", "go-template="+releaseTemplate):
				raw = projected(id.ReleaseUID, secretName, id.Namespace, "helm.sh/release.v1", "helm", id.Release, strconv.FormatUint(fence.options.Revision, 10), "live")
			case slices.Contains(spec.Args, databaseDumpScript):
				raw = []byte("synthetic custom archive; no actual PostgreSQL proof")
			case slices.Contains(spec.Args, archiveListScript):
				if spec.StdinFile == "" {
					t.Fatal("actual retained archive stdin missing")
				}
				raw = []byte("; synthetic readable archive listing\n")
			case slices.Contains(spec.Args, temporaryRestoreScript):
				if spec.StdinFile == "" || !state.temporary {
					t.Fatal("actual restore ownership/input missing")
				}
				raw = nil
			case slices.Contains(spec.Args, "secrets"):
				raw = []byte("apiVersion: v1\nitems: []\n")
			default:
				t.Fatal("unexpected input/capture kubectl seam", spec.Args)
			}
		} else {
			switch {
			case slices.Contains(spec.Args, "metadata"):
				version := strings.TrimPrefix(plan.Previous, "v")
				revision := 1
				if state.applied {
					version = strings.TrimPrefix(plan.Target, "v")
					revision = 2
				}
				raw = []byte(fmt.Sprintf(`{"name":%q,"chart":"astronomer","version":%q,"appVersion":%q,"namespace":%q,"revision":%d,"status":"deployed","deployedAt":"synthetic"}`, id.Release, version, version, id.Namespace, revision))
			case slices.Contains(spec.Args, "values"):
				raw = []byte("postgres:\n  bundled:\n    enabled: true\n")
			case slices.Contains(spec.Args, "history"):
				raw = []byte("[]\n")
			case slices.Contains(spec.Args, "manifest"):
				manifest, _, err := releasecontract.Load(old.release.manifestFile, plan.Target)
				if err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				for _, image := range manifest.Astronomer.Images {
					text.WriteString(image.Reference + "\n")
					if image.Name == "agent" {
						text.WriteString("AGENT_IMAGE_REPOSITORY: \"" + image.Reference + "\"\nAGENT_IMAGE_TAG: \"" + plan.Target + "\"\n")
					}
				}
				raw = []byte(text.String())
			case slices.Contains(spec.Args, "upgrade"):
				if !slices.Contains(spec.Args, "--dry-run=server") {
					if !state.replaced || state.server != 0 || state.worker != 0 {
						t.Fatal("Apply ran without freshly owned replacement/quiescence")
					}
					state.applyCalls++
					state.applied = true
					state.server, state.worker = 2, 1
				}
				raw = nil
			default:
				t.Fatal("unexpected bound Helm seam", spec.Args)
			}
		}
		result := artifactFixture(t, spec.CaptureDirectory, fmt.Sprintf("%s-%03d.stdout", spec.ID, spec.Sequence), raw)
		return result, nil
	}
	release, err := NewHelmRelease(context.Background(), fence, options.Helm, HelmReleaseOptions{Plan: plan, ManifestFile: old.release.manifestFile, ManifestSHA256: optionsHash(t, old.release.manifestFile), ChartReference: old.release.chartReference, RuntimeSources: mustRuntimeSources(t, old.verification.chart)})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSourceDatabaseBackup(fence, old.backup.source.options)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := NewReadiness(fence, 1)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewHelmStateObservation(release, readiness, source)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := NewHelmRecovery(observation, old.backup.policy)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := NewReleaseBackup(old.verification.chart, recovery, source, old.backup.policy)
	if err != nil {
		t.Fatal(err)
	}
	targetDB, err := NewTargetDatabaseObservation(source)
	if err != nil {
		t.Fatal(err)
	}
	readyz, err := NewReadyzObservation(release)
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewTargetWorkloadsWithDatabase(observation, readyz, targetDB)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := NewReleaseDriver(context.Background(), old.verification, backup, target)
	if err != nil {
		t.Fatal(err)
	}
	return driver, state
}
func TestConcreteReleaseDriverOrdersVerifiedBackupQuiescenceApplyAndObserve(t *testing.T) {
	driver, state := orderedReplacementDriverFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, stage := range []upgrade.Stage{upgrade.Verify, upgrade.Backup, upgrade.DryRun, upgrade.ReplaceDatabase, upgrade.Apply, upgrade.Observe} {
		outcome, err := driver.Step(ctx, stage, driver.plan)
		if err != nil {
			t.Fatal("concrete ordered stage failed", stage, err)
		}
		if !outcome.Known || (stage == upgrade.ReplaceDatabase && !outcome.Replaced) || (stage == upgrade.Apply && !outcome.Applied) {
			t.Fatal("actual stage acknowledgement missing", stage, outcome)
		}
	}
	if state.applyCalls != 1 || state.sourceDrops != 0 || !state.forwardJoined || !driver.backup.complete {
		t.Fatal("ordered Driver omitted real ownership/backup/health gate")
	}
	if err := driver.Cleanup(ctx, driver.plan, upgrade.CleanupPolicy{OwnedEphemeralOnly: true, PreserveBackup: true, PreserveCheckpoint: true}); err != nil {
		t.Fatal(err)
	}
	if err := driver.backup.verifyInventory(ctx); err != nil {
		t.Fatal("cleanup lost complete private recovery inventory", err)
	}
}
func TestConcreteReleaseDriverFailureAfterQuiescenceRestoresOnlyOriginalOwnedReplicas(t *testing.T) {
	driver, state := orderedReplacementDriverFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, stage := range []upgrade.Stage{upgrade.Verify, upgrade.Backup, upgrade.DryRun} {
		if _, err := driver.Step(ctx, stage, driver.plan); err != nil {
			t.Fatal(stage, err)
		}
	}
	state.failReplacement = true
	if _, err := driver.Step(ctx, upgrade.ReplaceDatabase, driver.plan); err == nil || state.server != 0 || state.worker != 0 || state.applyCalls != 0 {
		t.Fatal("controlled failure did not occur after actual quiescence")
	}
	if err := driver.Cleanup(ctx, driver.plan, upgrade.CleanupPolicy{OwnedEphemeralOnly: true, PreserveBackup: true, PreserveCheckpoint: true}); err != nil {
		t.Fatal(err)
	}
	if state.server != 2 || state.worker != 1 || state.sourceDrops != 0 {
		t.Fatal("recovery changed foreign/source scope")
	}
	if err := driver.backup.verifyInventory(ctx); err != nil {
		t.Fatal("failure recovery removed private backup", err)
	}
}

func optionsHash(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(hash[:])
}
func mustRuntimeSources(t *testing.T, chart *TargetChartContent) map[string]string {
	t.Helper()
	sources, err := chart.RuntimeSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sources
}
