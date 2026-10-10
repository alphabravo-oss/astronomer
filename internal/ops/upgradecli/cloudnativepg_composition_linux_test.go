//go:build linux

package upgradecli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func cnpgInitialFixture(t *testing.T) (*InitialObserver, *CloudNativePGSource, *cnpgFixtureState) {
	t.Helper()
	source, state := cloudNativePGFixture(t, 17)
	id := source.fence.options.Identity
	helm := source.fence.kubectl
	helm.Kind = Helm
	helm.Version = "v4.3.0"
	observer, err := NewInitialObserver(source.fence.commands, helm, source.fence.kubectl, LegacyEnvironment{Release: id.Release, Namespace: id.Namespace, Context: id.Context, APIServer: id.APIServer}, source.fence.options.Kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	read := source.read
	observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if tool.Kind == Helm {
			if args[5] == "values" {
				return []byte(`{"postgres":{"mode":"cloudNativePG","database":"owned_live","cloudNativePG":{"clusterName":"owned-cnpg"}}}`), nil
			}
			return []byte(fmt.Sprintf(`{"name":%q,"namespace":%q,"revision":1,"status":"deployed","chart":"astronomer","version":"1.1.0","appVersion":"1.1.0","deployedAt":"synthetic"}`, id.Release, id.Namespace)), nil
		}
		if args[4] == "auth" {
			return []byte("yes\n"), nil
		}
		if args[4] == "get" && args[5] == "clusters.postgresql.cnpg.io" && args[6] == "--namespace" {
			return []byte("owned-cnpg\t" + state.clusterUID + "\t" + id.Namespace + "\t" + id.Release + "\tpostgres\tlive\n"), nil
		}
		return read(ctx, tool, args)
	}
	return observer, source, state
}
func TestInitialObserverRoutesObservedCNPGThroughActualClusterPrimaryStorageNotExternal(t *testing.T) {
	observer, _, _ := cnpgInitialFixture(t)
	initial, err := observer.Observe(context.Background(), "v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !initial.CloudNativePG || initial.ExternalDatabase || initial.cnpg == nil || initial.cnpg.initial.ClusterUID != "cluster-original" || initial.DatabasePodUID != "pod-1" || initial.DatabaseState.Major != 17 || initial.Database != "owned_live" {
		t.Fatal("CNPG installed owner was not independently bound")
	}
	if initial.fenceOptions(observer.kubeconfig).DatabasePod != "" {
		t.Fatal("legacy Pod fence incorrectly substitutes for CNPG ownership")
	}
}
func TestInitialCNPGRefusesForeignAssertionsAndChangedApplicationSecret(t *testing.T) {
	for _, change := range []string{"external", "replacement", "secret", "cluster"} {
		t.Run(change, func(t *testing.T) {
			observer, _, state := cnpgInitialFixture(t)
			switch change {
			case "external":
				observer.selection.ExternalBackupConfirmed = true
			case "replacement":
				observer.selection.ReplaceDatabase = true
			case "secret":
				read := observer.execute
				observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
					raw, err := read(ctx, tool, args)
					if tool.Kind == Kubectl && args[4] == "exec" {
						state.secretUID = "recreated"
					}
					return raw, err
				}
			case "cluster":
				state.clusterUID = "recreated"
				read := observer.execute
				observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
					raw, err := read(ctx, tool, args)
					if tool.Kind == Kubectl && args[4] == "get" && args[5] == "clusters.postgresql.cnpg.io" && args[6] == "--namespace" {
						raw = []byte(strings.Replace(string(raw), "recreated", "cluster-original", 1))
					}
					return raw, err
				}
			}
			initial, err := observer.Observe(context.Background(), "v1.2.0")
			if err == nil || initial != (InitialInstallation{}) {
				t.Fatal("foreign assertion/changed owner admitted initial CNPG binding")
			}
		})
	}
}
func TestCNPGTargetPermitsNewMemberUIDOnlyUnderRetainedClusterAndPersistentAnchors(t *testing.T) {
	for _, change := range []string{"member", "cluster", "pvc", "pv", "service", "secret"} {
		t.Run(change, func(t *testing.T) {
			cnpg, state := cloudNativePGFixture(t, 17)
			options := DatabaseBackupOptions{Database: cnpg.options.Database, RestoreDatabase: "astronomer_upgrade_verify_fixture", DatabasePodUID: cnpg.initial.PrimaryUID, Owner: "upgrade-fixture", ExpectedMajor: 17, MinimumSchema: 66, TargetSchema: 78, ArtifactPolicy: cnpg.options.ArtifactPolicy}
			restore := CloudNativePGRestoreOptions{Pod: "astronomer-upgrade-verify-fixture", Owner: options.Owner, Database: options.RestoreDatabase, MaximumStorageBytes: 1 << 30, WaitSeconds: 5}
			source, err := newCNPGDatabaseBackup(context.Background(), cnpg, options, restore)
			if err != nil {
				t.Fatal(err)
			}
			target, err := NewTargetDatabaseObservation(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := target.Capture(context.Background()); err != nil {
				t.Fatal(err)
			}
			oldUID := cnpg.initial.PrimaryUID
			switch change {
			case "member":
				state.memberUID = "target-member"
			case "cluster":
				state.clusterUID = "foreign"
			case "pvc":
				state.pvcUID = "foreign"
			case "pv":
				state.pvUID = "foreign"
			case "service":
				state.serviceUID = "foreign"
			case "secret":
				state.secretUID = "foreign"
			}
			if _, err := source.observe(context.Background()); err == nil {
				t.Fatal("original backup fence re-admitted changed owner")
			}
			observed, err := target.Observe(context.Background())
			if change == "member" {
				if err != nil || observed.Version != 78 || cnpg.initial.PrimaryUID != oldUID {
					t.Fatal("owned target recreation changed original source anchor", err)
				}
			} else if err == nil {
				t.Fatal("foreign target persistent owner admitted")
			}
		})
	}
}

func TestInitialCNPGRefusesExternalDSNOverrideBeforeSourceSQL(t *testing.T) {
	observer, _, _ := cnpgInitialFixture(t)
	read := observer.execute
	sql := 0
	observer.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if tool.Kind == Helm && args[5] == "values" {
			return []byte(`{"postgres":{"mode":"cloudNativePG","database":"owned_live","cloudNativePG":{"clusterName":"owned-cnpg"},"external":{"dsnSecretRef":{"name":"operator-external"}}}}`), nil
		}
		if tool.Kind == Kubectl && args[4] == "exec" {
			sql++
		}
		return read(ctx, tool, args)
	}
	initial, err := observer.Observe(context.Background(), "v1.2.0")
	if err == nil || initial != (InitialInstallation{}) || sql != 0 {
		t.Fatal("external DSN precedence reached an unrelated CNPG source")
	}
}
