package delivery

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestAssignmentProjectionPreservesNormalization(t *testing.T) {
	for _, assignment := range []protocol.DeliveryAssignmentV2{gitAssignment(), ociAssignment(), helmHTTPAssignment(), helmOCIAssignment()} {
		t.Run(string(assignment.Source.Kind), func(t *testing.T) {
			source, reconciler := observedObjects(assignment)
			for _, gvk := range assignmentGVKs {
				if gvk.Kind == sourceKind(assignment.Source.Kind) {
					source.SetGroupVersionKind(gvk)
				}
				if gvk.Kind == reconcilerKind(assignment.Renderer.Kind) {
					reconciler.SetGroupVersionKind(gvk)
				}
			}
			entries := []any{nil, "malformed", map[string]any{"id": "invalid", "v": "v1", "url": "never copy"}}
			for i := 0; i < 80; i++ {
				entries = append(entries, map[string]any{"id": "workload_app_apps_Deployment", "v": "v1", "extra": "never copy"})
			}
			_ = unstructured.SetNestedSlice(reconciler.Object, entries, "status", "inventory", "entries")
			for _, mode := range []string{"ready", "generation-lag", "unknown-generation-condition", "suspended", "stalled", "malformed-discriminators"} {
				t.Run(mode, func(t *testing.T) {
					assignment := assignment
					source, reconciler := source.DeepCopy(), reconciler.DeepCopy()
					switch mode {
					case "generation-lag":
						reconciler.SetGeneration(100)
					case "unknown-generation-condition":
						reconciler.SetGeneration(100)
						conditions, _, _ := unstructured.NestedSlice(reconciler.Object, "status", "conditions")
						conditions = append(conditions, map[string]any{"type": "FutureCondition", "status": "True", "observedGeneration": int64(100)})
						_ = unstructured.SetNestedSlice(reconciler.Object, conditions, "status", "conditions")
					case "malformed-discriminators":
						_ = unstructured.SetNestedSlice(reconciler.Object, []any{
							map[string]any{"type": "Re\x00ady", "status": "True", "observedGeneration": int64(9)},
							map[string]any{"type": "Ready", "status": "Tr\x00ue", "observedGeneration": int64(9)},
							map[string]any{"type": " Ready ", "status": " True ", "observedGeneration": int64(9)},
							map[string]any{"type": "Stalled", "status": "False", "lastTransitionTime": "2026-08-17T00:00:00Z\x00"},
						}, "status", "conditions")
					case "suspended":
						assignment.Action = protocol.DeliveryActionSuspend
					case "stalled":
						_ = unstructured.SetNestedSlice(reconciler.Object, []any{map[string]any{"type": "Stalled", "status": "True", "message": "token=secret"}}, "status", "conditions")
					}
					accepted := AcceptAssignment(assignment, Materialization{ControlNamespace: source.GetNamespace(), Objects: []*unstructured.Unstructured{source, reconciler}})
					now := time.Now().UTC()
					direct, err := NormalizeAcceptedObservation(AcceptedObservation{accepted, source, reconciler, now})
					if err != nil {
						t.Fatal(err)
					}
					sp, err := projectAssignmentObject(source)
					if err != nil {
						t.Fatal(err)
					}
					rp, err := projectAssignmentObject(reconciler)
					if err != nil {
						t.Fatal(err)
					}
					cached, err := NormalizeAcceptedObservation(AcceptedObservation{accepted, sp.(*unstructured.Unstructured), rp.(*unstructured.Unstructured), now})
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(direct, cached) {
						t.Fatalf("normalization changed: direct=%#v cached=%#v", direct, cached)
					}
				})
			}
		})
	}
}

func TestAssignmentCacheUnchangedReplacementPreservesOnlyPendingWork(t *testing.T) {
	f := newAssignmentCacheFixture(t, 2)
	f.cache.ConsumeDirty()
	if err := f.cache.ReplaceAssignments(f.assignments); err != nil {
		t.Fatal(err)
	}
	if len(f.cache.ConsumeDirty()) != 0 || len(f.cache.wake) != 0 {
		t.Fatal("unchanged subscriptions scheduled status work")
	}
	refs, err := assignmentObservationRefs(f.assignments[0])
	if err != nil {
		t.Fatal(err)
	}
	f.cache.markObjectDirty(refs.source)
	if err = f.cache.ReplaceAssignments(f.assignments); err != nil {
		t.Fatal(err)
	}
	if dirty := f.cache.ConsumeDirty(); len(dirty) != 1 || dirty[0] != f.assignments[0].DeploymentID {
		t.Fatalf("pending work not preserved: %v", dirty)
	}
	changed := append([]AcceptedAssignment(nil), f.assignments...)
	changed[1].Generation++
	if err = f.cache.ReplaceAssignments(changed); err != nil {
		t.Fatal(err)
	}
	if dirty := f.cache.ConsumeDirty(); len(dirty) != 1 || dirty[0] != changed[1].DeploymentID {
		t.Fatalf("changed subscription not isolated: %v", dirty)
	}
	f.cache.markObjectDirty(refs.source)
	if err = f.cache.ReplaceAssignments(changed[1:]); err != nil {
		t.Fatal(err)
	}
	if dirty := f.cache.ConsumeDirty(); len(dirty) != 0 {
		t.Fatalf("evicted pending work survived: %v", dirty)
	}
	// Wrong API versions cannot select a different cache under the same name.
	bad := changed[1]
	bad.Objects = append([]ObjectIdentity(nil), bad.Objects...)
	bad.Objects[0].Version = "v9"
	if err = f.cache.ReplaceAssignments([]AcceptedAssignment{bad}); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestAssignmentProjectionRejectsOtherTypes(t *testing.T) {
	if _, err := projectAssignmentObject(schema.GroupVersionKind{}); err == nil {
		t.Fatal("non-object projection accepted")
	}
}
