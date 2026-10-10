//go:build linux

package upgradecli

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

func TestLegacyEnvironmentCapturesExactEmptyDefaultsWithoutAmbientReads(t *testing.T) {
	values := map[string]string{"KUBE_CONTEXT": "owned", "EXPECTED_KUBE_API_SERVER": "https://api.example.test", "RELEASE": "", "HOME": "secret-home", "GH_TOKEN": "secret-token", "PATH": "secret-path"}
	seen := map[string]int{}
	env, err := CaptureLegacyEnvironment(func(key string) (string, bool) { seen[key]++; value, ok := values[key]; return value, ok }, "/operator/cwd")
	if err != nil {
		t.Fatal(err)
	}
	if env.Release != "astronomer" || env.Namespace != "astronomer" || env.ChartReference != "oci://ghcr.io/alphabravo-oss/charts/astronomer" || env.ReleaseRepository != "alphabravo-oss/astronomer" || env.BackupRoot != "/operator/cwd/astronomer-upgrade-backups" || env.EvidenceDirectory != "/operator/cwd/astronomer-upgrade-evidence" || env.Timeout != 15*time.Minute || env.HealthPort != 18080 || env.MinimumReadyNodes != 1 || env.MinimumBackupFreeKiB != 1048576 {
		t.Fatal("legacy empty/default contract differs")
	}
	for key, count := range seen {
		if count != 1 || key == "HOME" || key == "PATH" || key == "GH_TOKEN" || key == "KUBECONFIG" {
			t.Fatal("ambient input or repeated lookup admitted")
		}
	}
	values["RELEASE"] = "changed"
	if env.Release != "astronomer" {
		t.Fatal("captured selection changed after lookup")
	}
}

func TestLegacyEnvironmentRetainsExplicitOptionsWithoutInferringAuthority(t *testing.T) {
	values := map[string]string{"KUBE_CONTEXT": "owned", "EXPECTED_KUBE_API_SERVER": "https://api.example.test", "RELEASE": "selected", "NAMESPACE": "selected-ns", "RELEASE_REPO": "operator/attestations", "CHART_REF": "oci://registry.example.test/chart", "BACKUP_ROOT": "backups", "SANITIZED_EVIDENCE_DIR": "/public/report", "RELEASE_ARTIFACT_DIR": "staged", "TIMEOUT": "2h", "HEALTH_PORT": "18443", "MIN_READY_NODES": "3", "MIN_BACKUP_FREE_KIB": "2097152", "EXTERNAL_DB_BACKUP_CONFIRMED": "1", "EXTERNAL_DB_SCHEMA_VERSION": "78", "RC_DECRYPT_PROOF_WEBHOOK_ID": "captured-proof", "RC_REPLACE_DATABASE_FROM_BACKUP": "1"}
	env, err := CaptureLegacyEnvironment(func(k string) (string, bool) { v, ok := values[k]; return v, ok }, "/operator")
	if err != nil {
		t.Fatal(err)
	}
	if env.ReleaseRepository != values["RELEASE_REPO"] || env.ChartReference != values["CHART_REF"] || env.BackupRoot != "/operator/backups" || env.ReleaseArtifactDirectory != "/operator/staged" || env.EvidenceDirectory != "/public/report" || env.Timeout != 2*time.Hour || env.HealthPort != 18443 || env.MinimumReadyNodes != 3 || env.MinimumBackupFreeKiB != 2097152 || !env.ExternalBackupConfirmed || !env.ReplaceDatabase || env.ExternalSchemaVersion != "78" || env.DecryptProofWebhookID != "captured-proof" {
		t.Fatal("explicit input clipped or dropped")
	}
	id := upgrade.Identity{Context: env.Context, APIServer: env.APIServer, Release: env.Release, Namespace: env.Namespace, NamespaceUID: "observed-namespace", ReleaseUID: "observed-original-revision"}
	options := InvocationOptions{Plan: upgrade.Plan{Identity: id}, Fence: FenceOptions{Identity: id}}
	if err := env.MatchesObservedIdentity(options); err != nil {
		t.Fatal(err)
	}
	if options.Plan.Replace != nil || options.Fence.Revision != 0 || options.Database.DatabasePodUID != "" {
		t.Fatal("environment granted observed/replacement authority")
	}
	options.Plan.Identity.NamespaceUID = ""
	if env.MatchesObservedIdentity(options) == nil {
		t.Fatal("missing independent identity admitted")
	}
	options.Plan.Identity = id
	options.Fence.Identity.Namespace = "recreated"
	if env.MatchesObservedIdentity(options) == nil {
		t.Fatal("different fence selection admitted")
	}
}

func TestLegacyEnvironmentRejectsUnsafeSelectionWithStaticDiagnostics(t *testing.T) {
	bad := []struct{ name, value string }{{"KUBE_CONTEXT", "secret\ncontext"}, {"EXPECTED_KUBE_API_SERVER", "https://secret:credential@api.example.test"}, {"EXPECTED_KUBE_API_SERVER", "https://api.example.test?secret=credential"}, {"TIMEOUT", "secret"}, {"TIMEOUT", "-1s"}, {"TIMEOUT", "999999999999999999999999999h"}, {"HEALTH_PORT", "0655"}, {"HEALTH_PORT", "65536"}, {"MIN_READY_NODES", "4294967296"}, {"MIN_BACKUP_FREE_KIB", "18446744073709551616"}}
	for _, x := range bad {
		t.Run(x.name+"-"+strings.ReplaceAll(x.value, "/", "_"), func(t *testing.T) {
			values := map[string]string{"KUBE_CONTEXT": "owned", "EXPECTED_KUBE_API_SERVER": "https://api.example.test"}
			values[x.name] = x.value
			_, err := CaptureLegacyEnvironment(func(k string) (string, bool) { v, ok := values[k]; return v, ok }, "/operator")
			if err == nil {
				t.Fatal("unsafe/unrepresentable selection admitted")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") || strings.Contains(err.Error(), x.value) {
				t.Fatal("operator value exposed")
			}
		})
	}
	if _, err := CaptureLegacyEnvironment(nil, "/operator"); err == nil {
		t.Fatal("nil lookup admitted")
	}
	if _, err := CaptureLegacyEnvironment(func(string) (string, bool) { return "", false }, "relative"); err == nil {
		t.Fatal("unbound cwd admitted")
	}
}
