package delivery

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func observedObjects(assignment protocol.DeliveryAssignmentV2) (*unstructured.Unstructured, *unstructured.Unstructured) {
	names := Names(assignment.ProjectID, assignment.DeploymentID)
	source := managedObject(assignment, gitRepositoryGVK, names.ControlNamespace, names.Source, nil)
	source.SetGeneration(4)
	source.Object["status"] = map[string]any{
		"artifact": map[string]any{"revision": "main@sha1:0123456789abcdef", "digest": testArtifact},
		"conditions": []any{map[string]any{
			"type": "Ready", "status": "True", "reason": "Succeeded",
			"message": "fetched ssh://robot:secret@example.test/repo token=abcdef", "observedGeneration": int64(4),
			"lastTransitionTime": "2026-08-17T00:00:00Z",
		}},
	}
	reconciler := managedObject(assignment, kustomizationGVK, names.ControlNamespace, names.Base, nil)
	reconciler.SetGeneration(9)
	reconciler.Object["status"] = map[string]any{
		"inventory":  map[string]any{"entries": []any{"apps_v1_Deployment_workload_app", "_v1_Service_workload_app"}},
		"conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "ReconciliationSucceeded", "observedGeneration": int64(9)}},
	}
	return source, reconciler
}

func TestNormalizeObservationReadyAndRedacted(t *testing.T) {
	t.Parallel()
	assignment := gitAssignment()
	source, reconciler := observedObjects(assignment)
	status, err := NormalizeObservation(Observation{Assignment: assignment, Source: source, Reconciler: reconciler, ObservedAt: time.Date(2026, 8, 17, 1, 0, 0, 0, time.FixedZone("offset", 3600))})
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ready" || status.Inventory.Entries != 2 || status.Inventory.Ready != 2 || status.ObservedDigest != testArtifact ||
		status.SourceKind != "GitRepository" || status.ReconcilerKind != "Kustomization" {
		t.Fatalf("normalized status = %#v", status)
	}
	if !status.ObservedAt.Equal(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("observation not normalized to UTC: %v", status.ObservedAt)
	}
	joined := ""
	for _, condition := range status.Conditions {
		joined += condition.Message
	}
	if strings.Contains(joined, "secret") || strings.Contains(joined, "abcdef") || !strings.Contains(joined, "[redacted]") {
		t.Errorf("condition credential redaction failed: %q", joined)
	}
}

func TestNormalizeObservationPhasesAndBoundary(t *testing.T) {
	t.Parallel()
	assignment := gitAssignment()
	source, reconciler := observedObjects(assignment)
	reconciler.SetGeneration(10)
	status, err := NormalizeObservation(Observation{Assignment: assignment, Source: source, Reconciler: reconciler, ObservedAt: time.Now()})
	if err != nil || status.Phase != "applying" || len(status.WarningCodes) != 1 || status.WarningCodes[0] != "generation_lag" {
		t.Fatalf("generation lag status = %#v, %v", status, err)
	}
	reconciler.SetGeneration(9)
	reconciler.Object["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Stalled", "status": "True", "observedGeneration": int64(9)}}
	status, err = NormalizeObservation(Observation{Assignment: assignment, Source: source, Reconciler: reconciler, ObservedAt: time.Now()})
	if err != nil || status.Phase != "failed" || status.Inventory.Failed != 2 || status.ErrorCode != "reconciler_stalled" {
		t.Fatalf("stalled status = %#v, %v", status, err)
	}
	assignment.Action = protocol.DeliveryActionSuspend
	status, err = NormalizeObservation(Observation{Assignment: assignment, Source: source, Reconciler: reconciler, ObservedAt: time.Now()})
	if err != nil || status.Phase != "suspended" {
		t.Fatalf("suspended status = %#v, %v", status, err)
	}
	assignment.Action = protocol.DeliveryActionApply
	source.SetLabels(map[string]string{ManagedByLabel: ManagedByValue, DeploymentIDLabel: "44444444-4444-4444-8444-444444444444"})
	if _, err = NormalizeObservation(Observation{Assignment: assignment, Source: source, Reconciler: reconciler, ObservedAt: time.Now()}); err == nil {
		t.Fatal("cross-deployment observation was accepted")
	}
}

func TestNormalizeObservationMissingAndNoRawFields(t *testing.T) {
	t.Parallel()
	status, err := NormalizeObservation(Observation{Assignment: gitAssignment(), ObservedAt: time.Now()})
	if err != nil || status.Phase != "pending" || status.ObservedRevision != "" || len(status.Conditions) != 0 || len(status.WarningCodes) != 2 {
		t.Fatalf("missing objects status = %#v, %v", status, err)
	}
}

func TestCoalesceStatusesStableAndConflictSafe(t *testing.T) {
	t.Parallel()
	base := protocol.DeliveryDeploymentStatusV2{DeploymentID: testDeploymentID, Generation: 1, SpecDigest: testDigest, Phase: "pending", ObservedAt: time.Unix(1, 0)}
	newest := base
	newest.Generation = 2
	newest.Phase = "ready"
	newest.ObservedAt = time.Unix(2, 0)
	other := base
	other.DeploymentID = "00000000-0000-4000-8000-000000000000"
	got, err := CoalesceStatuses([]protocol.DeliveryDeploymentStatusV2{newest, other, base})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].DeploymentID != other.DeploymentID || got[1].Generation != 2 {
		t.Fatalf("coalesced status = %#v", got)
	}
	conflict := newest
	conflict.Phase = "failed"
	if _, err := CoalesceStatuses([]protocol.DeliveryDeploymentStatusV2{newest, conflict}); err == nil {
		t.Fatal("equal-time conflicting status was accepted")
	}
}

func FuzzConditionSanitization(f *testing.F) {
	f.Add("https://user:password@example.test password=secret\x00", 64)
	f.Fuzz(func(t *testing.T, value string, limit int) {
		if limit < 1 || limit > protocol.MaxDeliveryStatusMessageBytes {
			return
		}
		got := sanitizeStatusText(value, limit)
		if len(got) > limit || !utf8.ValidString(got) {
			t.Fatalf("invalid bounded sanitization")
		}
	})
}
