package delivery

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// projectAssignmentObject retains only the fields consumed by accepted-status
// normalization. It never retains spec, source URLs, arbitrary annotations or
// credential material. Informer inputs belong to the reflector and stay intact.
func projectAssignmentObject(raw any) (any, error) {
	item, ok := raw.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unsupported assignment observation %T", raw)
	}
	out := &unstructured.Unstructured{Object: map[string]any{}}
	out.SetGroupVersionKind(item.GroupVersionKind())
	out.SetName(item.GetName())
	out.SetNamespace(item.GetNamespace())
	out.SetUID(item.GetUID())
	out.SetResourceVersion(item.GetResourceVersion())
	out.SetGeneration(item.GetGeneration())
	labels := item.GetLabels()
	out.SetLabels(map[string]string{ManagedByLabel: labels[ManagedByLabel], DeploymentIDLabel: labels[DeploymentIDLabel], ProjectIDHashLabel: labels[ProjectIDHashLabel]})
	specDigest := item.GetAnnotations()[SpecDigestAnnotation]
	if !digestValuePattern.MatchString(specDigest) {
		specDigest = ""
	}
	out.SetAnnotations(map[string]string{SpecDigestAnnotation: specDigest})
	conditions, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	projected := make([]any, 0, len(conditions))
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		// Keep observedGeneration even for unknown types: generation-lag detection
		// examines all conditions, while normalization filters their public types.
		entry := map[string]any{"observedGeneration": int64Value(condition["observedGeneration"])}
		// Never sanitize discriminators into validity (for example Re\x00ady).
		conditionType, status := stringValue(condition["type"]), stringValue(condition["status"])
		if allowedConditionType(conditionType) {
			entry["type"] = conditionType
		}
		if status == "True" || status == "False" || status == "Unknown" {
			entry["status"] = status
		}
		if at, err := time.Parse(time.RFC3339, stringValue(condition["lastTransitionTime"])); err == nil {
			entry["lastTransitionTime"] = at.UTC().Format(time.RFC3339Nano)
		}
		entry["reason"] = sanitizeStatusText(stringValue(condition["reason"]), 256)
		entry["message"] = sanitizeStatusText(stringValue(condition["message"]), protocol.MaxDeliveryStatusMessageBytes)
		projected = append(projected, entry)
	}
	_ = unstructured.SetNestedSlice(out.Object, projected, "status", "conditions")
	revision, digest := observedArtifact(item)
	_ = unstructured.SetNestedField(out.Object, revision, "status", "artifact", "revision")
	_ = unstructured.SetNestedField(out.Object, digest, "status", "artifact", "digest")
	entries, found, _ := unstructured.NestedSlice(item.Object, "status", "inventory", "entries")
	if found {
		clean := make([]any, len(entries))
		for i, raw := range entries {
			if _, valid := fluxResourceIdentity(raw); valid {
				entry := raw.(map[string]any)
				clean[i] = map[string]any{"id": stringValue(entry["id"]), "v": stringValue(entry["v"])}
			}
		}
		_ = unstructured.SetNestedSlice(out.Object, clean, "status", "inventory", "entries")
	}
	return out, nil
}
