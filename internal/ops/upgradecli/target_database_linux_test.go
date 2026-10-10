//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type targetDatabaseFixtureState struct {
	podUID, controllerUID, claimUID, volumeUID                           string
	podPhase, podReady, podControllerKind, mount                         string
	controllerReplicas, controllerReady, currentRevision, updateRevision string
	volumeClaimUID, schema                                               string
	duplicatePods                                                        bool
	beforeRead                                                           func(string)
}

func targetDatabaseFixture(t *testing.T) (*SourceDatabaseBackup, *TargetDatabaseObservation, *targetDatabaseFixtureState) {
	t.Helper()
	source, _ := databaseBackupFixture(t)
	source.fence.options.DatabasePod = "owned-postgres-0"
	state := &targetDatabaseFixtureState{podUID: source.options.DatabasePodUID, controllerUID: "controller-uid", claimUID: "claim-uid", volumeUID: "volume-uid", podPhase: "Running", podReady: "true", podControllerKind: "StatefulSet", mount: "/var/lib/postgresql/data", controllerReplicas: "1", controllerReady: "1", currentRevision: "revision-one", updateRevision: "revision-one", schema: "owned_live|18|1|78|f\n"}
	f := source.fence
	previous := f.execute
	id := f.options.Identity
	pod := func() []byte {
		return []byte(strings.Join([]string{state.podUID, f.options.DatabasePod, id.Namespace, id.Release, "postgres", "astronomer", "astronomer", "live", state.podPhase, state.podReady, "apps/v1", state.podControllerKind, "owned-postgres", state.controllerUID, "data-" + f.options.DatabasePod, "postgres", state.mount}, "\t"))
	}
	f.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		projection := args[len(args)-1]
		if state.beforeRead != nil {
			state.beforeRead(projection)
		}
		switch projection {
		case "go-template=" + databasePodTemplate:
			return []byte(strings.Join([]string{state.podUID, f.options.DatabasePod, id.Namespace, id.Release, "postgres", "live"}, "\t")), nil
		case "go-template=" + targetDatabasePodTemplate:
			return pod(), nil
		case "go-template=" + targetDatabasePodsTemplate:
			want := "app.kubernetes.io/name=astronomer,app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/component=postgres"
			if args[len(args)-3] != want {
				t.Fatal("target database selector differs")
			}
			p := append(pod(), '\n')
			if state.duplicatePods {
				p = append(p, p...)
			}
			return p, nil
		case "go-template=" + targetDatabaseControllerTemplate:
			return []byte(strings.Join([]string{state.controllerUID, "owned-postgres", id.Namespace, id.Release, "postgres", "astronomer", "live", state.controllerReplicas, "2", "2", state.currentRevision, state.updateRevision, state.controllerReady, "astronomer", id.Release, "postgres"}, "\t")), nil
		case "go-template=" + targetDatabaseClaimTemplate:
			return []byte(strings.Join([]string{state.claimUID, "data-" + f.options.DatabasePod, id.Namespace, "live", "Bound", "owned-volume"}, "\t")), nil
		case "go-template=" + targetDatabaseVolumeTemplate:
			claim := state.volumeClaimUID
			if claim == "" {
				claim = state.claimUID
			}
			return []byte(strings.Join([]string{state.volumeUID, "owned-volume", "live", "Bound", claim, "data-" + f.options.DatabasePod, id.Namespace}, "\t")), nil
		case databaseStateScript:
			if strings.Contains(strings.Join(args, " "), "--container=postgres") {
				if args[7] != f.options.DatabasePod || args[8] != "--container=postgres" {
					t.Fatal("target database exec is not exact bound Pod/container")
				}
				return []byte(state.schema), nil
			}
		}
		return previous(ctx, tool, args)
	}
	observer, err := NewTargetDatabaseObservation(source)
	if err != nil {
		t.Fatal(err)
	}
	return source, observer, state
}

func TestTargetDatabaseObservationAllowsPodRolloutOnlyWithOriginalControllerAndStorage(t *testing.T) {
	source, observer, state := targetDatabaseFixture(t)
	if err := observer.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	state.podUID = "replacement-pod"
	if _, err := source.observe(context.Background()); err == nil {
		t.Fatal("original backup fence admitted replacement Pod")
	}
	observed, err := observer.Observe(context.Background())
	if err != nil || observed.Major != 18 || observed.Version != 78 {
		t.Fatal("same-storage target Pod rollout rejected", err)
	}
	if err := observer.Capture(context.Background()); err == nil {
		t.Fatal("initial storage re-admitted after rollout")
	}
}

func TestTargetDatabaseObservationRejectsRecreatedStorageForeignOwnerAndIncompleteRollout(t *testing.T) {
	for _, change := range []string{"controller", "claim", "volume", "claim-ref", "pod-kind", "mount", "replicas", "ready", "revision", "pod-ready", "pod-phase", "duplicate-pods", "major", "dirty", "schema"} {
		t.Run(change, func(t *testing.T) {
			_, observer, state := targetDatabaseFixture(t)
			if err := observer.Capture(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "controller":
				state.controllerUID = "recreated-controller"
			case "claim":
				state.claimUID = "recreated-claim"
			case "volume":
				state.volumeUID = "recreated-volume"
			case "claim-ref":
				state.volumeClaimUID = "foreign-claim"
			case "pod-kind":
				state.podControllerKind = "Deployment"
			case "mount":
				state.mount = "/other"
			case "replicas":
				state.controllerReplicas = "2"
			case "ready":
				state.controllerReady = "0"
			case "revision":
				state.updateRevision = "revision-two"
			case "pod-ready":
				state.podReady = "false"
			case "pod-phase":
				state.podPhase = "Pending"
			case "duplicate-pods":
				state.duplicatePods = true
			case "major":
				state.schema = "owned_live|17|1|78|f"
			case "dirty":
				state.schema = "owned_live|18|1|78|t"
			case "schema":
				state.schema = "owned_live|18|1|79|f"
			}
			if _, err := observer.Observe(context.Background()); err == nil {
				t.Fatal("changed or incomplete database owner qualified")
			}
		})
	}
}

func TestTargetDatabaseObservationRejectsChangesDuringQueryAndStorageCapture(t *testing.T) {
	for _, change := range []string{"initial-pod", "initial-volume", "query-pod", "query-controller", "query-claim", "query-volume", "query-cancel"} {
		t.Run(change, func(t *testing.T) {
			_, observer, state := targetDatabaseFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if strings.HasPrefix(change, "initial") {
				reads := 0
				state.beforeRead = func(projection string) {
					if projection == "go-template="+targetDatabaseVolumeTemplate {
						reads++
						if change == "initial-pod" && reads == 1 {
							state.podUID = "recreated"
						}
						if change == "initial-volume" && reads == 2 {
							state.volumeUID = "recreated"
						}
					}
				}
				if err := observer.Capture(ctx); err == nil {
					t.Fatal("changing initial storage captured")
				}
				if observer.anchor != nil {
					t.Fatal("failed capture installed ownership")
				}
				return
			}
			if err := observer.Capture(ctx); err != nil {
				t.Fatal(err)
			}
			state.beforeRead = func(projection string) {
				if projection != databaseStateScript {
					return
				}
				switch change {
				case "query-pod":
					state.podUID = "recreated"
				case "query-controller":
					state.controllerUID = "recreated"
				case "query-claim":
					state.claimUID = "recreated"
				case "query-volume":
					state.volumeUID = "recreated"
				case "query-cancel":
					cancel()
				}
			}
			if _, err := observer.Observe(ctx); err == nil {
				t.Fatal("owner changed during SQL observation qualified")
			}
		})
	}
}

func TestTargetDatabaseObservationDoesNotAcknowledgeFailedOrUnboundReads(t *testing.T) {
	_, observer, state := targetDatabaseFixture(t)
	if _, err := observer.Observe(context.Background()); err == nil {
		t.Fatal("uncaptured storage admitted")
	}
	if observer.Capture(nil) == nil {
		t.Fatal("nil capture context admitted")
	}
	if _, err := observer.Observe(nil); err == nil {
		t.Fatal("nil observation context admitted")
	}
	previous := observer.source.fence.execute
	observer.source.fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if args[len(args)-1] == "go-template="+targetDatabaseClaimTemplate {
			return nil, errors.New("synthetic read failure")
		}
		return previous(ctx, tool, args)
	}
	if observer.Capture(context.Background()) == nil || observer.anchor != nil {
		t.Fatal("failed read granted initial storage")
	}
	observer.source.fence.execute = previous
	state.podUID = "other-initial-pod"
	if observer.Capture(context.Background()) == nil || observer.anchor != nil {
		t.Fatal("foreign initial Pod captured")
	}
}

func TestTargetDatabaseProjectionRejectsMalformedOversizedAndMultipleOwners(t *testing.T) {
	_, observer, _ := targetDatabaseFixture(t)
	for _, raw := range []string{"", strings.Repeat("x", 4097), "one\ttwo\n", "one\t<no value>", strings.Repeat("owner\t", 18)} {
		if _, err := observer.parsePod([]byte(raw)); err == nil {
			t.Fatal("malformed database projection admitted")
		}
	}
	if _, err := NewTargetDatabaseObservation(nil); err == nil {
		t.Fatal("nil source admitted")
	}
}
