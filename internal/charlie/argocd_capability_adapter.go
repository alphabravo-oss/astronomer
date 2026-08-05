package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

type ArgoCDCapabilityAdapter struct {
	dynamic     dynamic.Interface
	namespace   string
	application string
}

func NewArgoCDCapabilityAdapter(client dynamic.Interface, namespace, application string) (*ArgoCDCapabilityAdapter, error) {
	if client == nil || strings.TrimSpace(namespace) == "" || strings.TrimSpace(application) == "" {
		return nil, fmt.Errorf("Charlie Argo CD capability adapter is unavailable")
	}
	return &ArgoCDCapabilityAdapter{dynamic: client, namespace: namespace, application: application}, nil
}

func ArgoCDCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	return map[string]CapabilityExecutor{
		"astronomer.argocd.self_management_status": adapter,
		"astronomer.argocd.self_management_sync":   adapter,
	}
}

func (a *ArgoCDCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	resource := a.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(a.namespace)
	application, err := resource.Get(ctx, a.application, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !a.owned(application) {
		return nil, fmt.Errorf("Argo CD Application is not Astronomer-owned")
	}
	switch capability.Name {
	case "astronomer.argocd.self_management_status":
		return marshalBounded(sanitizedArgoStatus(application), capability.MaxResponseBytes)
	case "astronomer.argocd.self_management_sync":
		if stringArgument(arguments, "application") != a.application {
			return nil, fmt.Errorf("only the Astronomer self-management Application may be synced")
		}
		if current, found, _ := unstructured.NestedMap(application.Object, "operation"); found && len(current) > 0 {
			return nil, fmt.Errorf("an Argo CD operation is already in progress")
		}
		operationID := stringArgument(arguments, "operation_id")
		if application.GetAnnotations() == nil {
			application.SetAnnotations(map[string]string{})
		}
		annotations := application.GetAnnotations()
		annotations["astronomer.io/charlie-operation"] = operationID
		application.SetAnnotations(annotations)
		operation := map[string]any{
			"initiatedBy": map[string]any{"username": "astronomer-charlie"},
			"sync":        map[string]any{"prune": false, "syncOptions": []any{"Prune=false", "ApplyOutOfSyncOnly=true"}},
		}
		if err := unstructured.SetNestedMap(application.Object, operation, "operation"); err != nil {
			return nil, err
		}
		updated, err := resource.Update(ctx, application, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
		return marshalBounded(operationResult(arguments, "accepted", "argocd_application/"+updated.GetName()), capability.MaxResponseBytes)
	default:
		return nil, fmt.Errorf("unsupported Argo CD capability")
	}
}

func (a *ArgoCDCapabilityAdapter) Verify(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, _ json.RawMessage) (bool, error) {
	if capability.Effect == EffectRead {
		return true, nil
	}
	application, err := a.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(a.namespace).Get(ctx, a.application, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if !a.owned(application) || application.GetAnnotations()["astronomer.io/charlie-operation"] != stringArgument(arguments, "operation_id") {
		return false, nil
	}
	prune, found, err := unstructured.NestedBool(application.Object, "operation", "sync", "prune")
	return err == nil && found && !prune, err
}

func (a *ArgoCDCapabilityAdapter) owned(application *unstructured.Unstructured) bool {
	return application != nil && application.GetName() == a.application && application.GetNamespace() == a.namespace && application.GetLabels()["astronomer.io/platform-owned"] == "true"
}

func sanitizedArgoStatus(application *unstructured.Unstructured) map[string]any {
	health, _, _ := unstructured.NestedString(application.Object, "status", "health", "status")
	syncState, _, _ := unstructured.NestedString(application.Object, "status", "sync", "status")
	revision, _, _ := unstructured.NestedString(application.Object, "status", "sync", "revision")
	phase, _, _ := unstructured.NestedString(application.Object, "status", "operationState", "phase")
	return map[string]any{
		"application": application.GetName(), "health": health, "sync": syncState,
		"revision_digest": digestBytes([]byte(revision)), "operation_phase": phase,
		"observed_at": application.GetCreationTimestamp().UTC(),
	}
}
