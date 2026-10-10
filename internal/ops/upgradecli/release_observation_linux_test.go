//go:build linux

package upgradecli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// Command outputs are synthetic exact Helm metadata, not a live Helm release.
// The actual bound Commands and immutable input/fence checks are exercised.
func releaseStateFixture(t *testing.T) (*HelmStateObservation, *int) {
	t.Helper()
	r := helmReleaseFixture(t)
	// The complete upstream metadata fixture exceeds the inherited256B unit
	// diagnostic budget. Explicit1KiB remains below the parser's1MiB bound.
	r.fence.commands.options.MaximumCaptureBytes = 1 << 10
	manifest, _ := canonicalHelmManifestFixture(r.plan.Target)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.manifestFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r.manifestIdentity, r.manifestHash, err = privateBoundInput(r.manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := NewReadiness(r.fence, 1)
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewHelmStateObservation(r, ready, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls++
		expected := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--kube-context", r.plan.Identity.Context, "get", "metadata", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace, "--output", "json"}
		if strings.Join(spec.Args, "\x00") != strings.Join(expected, "\x00") {
			t.Fatal("actual latest metadata command lost context or selected another revision")
		}
		return captureFixture(t, spec, []byte(`{"name":"astronomer","chart":"astronomer","version":"1.2.2","appVersion":"v1.2.2","namespace":"owned-namespace","revision":2,"status":"deployed","deployedAt":"2026-10-10T00:00:00Z","applyMethod":"client-side","annotations":{"private":"discard"},"labels":{},"dependencies":[]}`)), nil
	}
	return o, &calls
}

func TestReleaseObservationUsesBoundActualLatestMetadataWithoutReplay(t *testing.T) {
	o, calls := releaseStateFixture(t)
	outcome, err := o.ReconcileHelm(context.Background())
	if err != nil || !outcome.Known || !outcome.Applied || outcome.Replaced || *calls != 2 {
		t.Fatal("actual repeated target assessment failed", err)
	}
	if _, err := o.Read(nil); err == nil {
		t.Fatal("nil observation context admitted")
	}
	if _, err := NewHelmStateObservation(o.release, &Readiness{}, nil); err == nil {
		t.Fatal("unrelated readiness ownership admitted")
	}
	if _, err := NewHelmStateObservation(o.release, o.readiness, &SourceDatabaseBackup{}); err == nil {
		t.Fatal("unrelated database ownership admitted")
	}
}

func TestReleaseReconciliationRetainsUnresolvedAndAutomaticRollbackOutcomes(t *testing.T) {
	for _, change := range []string{"original", "failed", "pending", "foreign", "rollback", "moving", "replacement"} {
		t.Run(change, func(t *testing.T) {
			o, _ := releaseStateFixture(t)
			calls := 0
			o.release.fence.commands.run = func(_ context.Context, spec process.Spec) (process.Result, error) {
				calls++
				raw := `{"name":"astronomer","chart":"astronomer","version":"1.2.2","appVersion":"1.2.2","namespace":"owned-namespace","revision":2,"status":"deployed"}`
				switch change {
				case "original":
					raw = strings.ReplaceAll(raw, "1.2.2", "1.2.1")
					raw = strings.ReplaceAll(raw, `"revision":2`, `"revision":1`)
				case "failed":
					raw = strings.ReplaceAll(raw, `"status":"deployed"`, `"status":"failed"`)
				case "pending":
					raw = strings.ReplaceAll(raw, `"status":"deployed"`, `"status":"pending-upgrade"`)
				case "foreign":
					raw = strings.ReplaceAll(raw, "1.2.2", "1.2.3")
				case "rollback":
					raw = strings.ReplaceAll(raw, "1.2.2", "1.2.1")
				case "moving":
					if calls == 2 {
						raw = strings.ReplaceAll(raw, `"revision":2`, `"revision":3`)
					}
				}
				return captureFixture(t, spec, []byte(raw)), nil
			}
			if change == "replacement" {
				o.release.plan.Replace = &upgrade.DisposableLease{NamespaceUID: o.release.plan.Identity.NamespaceUID, DatabasePodUID: "original-pod-uid", DatabaseName: "owned_live", RestoreDatabaseName: "owned_restored", Owner: "owned-run"}
			}
			outcome, err := o.ReconcileHelm(context.Background())
			if change == "original" {
				if err != nil || !outcome.Known || outcome.Applied || outcome.Replaced {
					t.Fatal("unchanged originally deployed state not assessed")
				}
			} else if err == nil || outcome.Known || outcome.Applied || outcome.Replaced {
				t.Fatal("uncertain mutation was acknowledged without recovery assessment")
			}
		})
	}
}

func TestReleaseObservationRejectsCanceledChangedInputsAndPostReadOwnerLoss(t *testing.T) {
	for _, change := range []string{"canceled", "post-read-canceled", "owner-loss", "manifest-loss", "child-failure"} {
		t.Run(change, func(t *testing.T) {
			o, calls := releaseStateFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			run := o.release.fence.commands.run
			if change == "canceled" {
				cancel()
			}
			o.release.fence.commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
				result, err := run(ctx, spec)
				switch change {
				case "post-read-canceled":
					cancel()
				case "owner-loss":
					o.release.fence.execute = func(context.Context, Tool, []string) ([]byte, error) { return nil, errors.New("synthetic lost owner") }
				case "manifest-loss":
					o.release.manifestHash = [32]byte{}
				case "child-failure":
					err = &process.Error{Result: process.Result{Kind: process.ChildFailed, ExitCode: 7, ChildExitCode: 7, Started: true, LeaderReaped: true}}
				}
				return result, err
			}
			if _, err := o.Read(ctx); err == nil {
				t.Fatal("failed/canceled/changed release observation admitted")
			}
			if change == "canceled" && *calls != 0 {
				t.Fatal("expired observation launched a command")
			}
		})
	}
}

func TestReleaseMetadataRejectsAmbiguousMalformedOrUnboundIdentity(t *testing.T) {
	o, _ := releaseStateFixture(t)
	valid := `{"name":"astronomer","chart":"astronomer","version":"1.2.2","appVersion":"1.2.2","namespace":"owned-namespace","revision":2,"status":"deployed"}`
	if _, err := parseHelmMetadata([]byte(valid), o.release.plan.Identity); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", valid + "{}", strings.Repeat(" ", 1<<20+1), strings.Replace(valid, `"revision":2`, `"revision":null`, 1), strings.Replace(valid, `"revision":2`, `"revision":2,"revision":3`, 1), strings.Replace(valid, `"revision":2`, `"revision":-1`, 1), strings.Replace(valid, `"namespace":"owned-namespace"`, `"namespace":"other"`, 1), strings.Replace(valid, `"chart":"astronomer"`, `"chart":"other"`, 1), strings.Replace(valid, `"status":"deployed"`, `"status":"future"`, 1), strings.Replace(valid, `"status":"deployed"`, `"status":"deployed","unexpected":true`, 1)} {
		if _, err := parseHelmMetadata([]byte(raw), o.release.plan.Identity); err == nil {
			t.Fatal("ambiguous/unbound metadata admitted")
		}
	}
}

func TestTargetObservationRequiresReadyExactSchemaAndStableRevision(t *testing.T) {
	for _, change := range []string{"success", "below-target", "dirty", "moving", "canceled", "foreign-bounds"} {
		t.Run(change, func(t *testing.T) {
			o, _ := releaseStateFixture(t)
			original := o.release.fence.execute
			o.release.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
				if args[4] == "auth" {
					return []byte("yes\n"), nil
				}
				if args[4] == "get" {
					switch args[5] {
					case "nodes":
						return []byte("1\n"), nil
					case "deployments":
						return []byte("3\t3\t1\t1\t1\n"), nil
					case "poddisruptionbudgets":
						return []byte("1\n"), nil
					case "pod":
						return []byte("original-pod-uid\towned-postgres-1\towned-namespace\tastronomer\tpostgres\tlive"), nil
					}
				}
				if args[4] == "exec" {
					raw := "owned_live|18|1|75|f"
					if change == "below-target" {
						raw = "owned_live|18|1|74|f"
					}
					if change == "dirty" {
						raw = "owned_live|18|1|75|t"
					}
					return []byte(raw), nil
				}
				return original(ctx, tool, args)
			}
			source, err := NewSourceDatabaseBackup(o.release.fence, DatabaseBackupOptions{Database: "owned_live", RestoreDatabase: "astronomer_upgrade_verify_fixture", DatabasePodUID: "original-pod-uid", Owner: "owned-run", ExpectedMajor: 18, MinimumSchema: 66, TargetSchema: 75, ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1 << 20}})
			if err != nil {
				t.Fatal(err)
			}
			if change == "foreign-bounds" {
				source.options.TargetSchema = 76
				if _, err := NewHelmStateObservation(o.release, o.readiness, source); err == nil {
					t.Fatal("caller schema bounds overrode the exact target manifest")
				}
				return
			}
			o, err = NewHelmStateObservation(o.release, o.readiness, source)
			if err != nil {
				t.Fatal(err)
			}
			run := o.release.fence.commands.run
			metadataCalls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o.release.fence.commands.run = func(ctx context.Context, spec process.Spec) (process.Result, error) {
				metadataCalls++
				result, err := run(ctx, spec)
				if metadataCalls == 2 && change == "moving" {
					return captureFixture(t, spec, []byte(`{"name":"astronomer","chart":"astronomer","version":"1.2.2","appVersion":"1.2.2","namespace":"owned-namespace","revision":3,"status":"deployed"}`)), nil
				}
				if metadataCalls == 2 && change == "canceled" {
					cancel()
				}
				return result, err
			}
			err = o.ConfirmTarget(ctx)
			if change == "success" {
				if err != nil || metadataCalls != 2 {
					t.Fatal("actual stable metadata/readiness/schema observation failed", err)
				}
			} else if err == nil {
				t.Fatal("incomplete post-upgrade observation passed")
			}
		})
	}
}
