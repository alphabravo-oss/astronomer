package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const localFluxFieldManager = "astronomer-local-flux-bootstrap"

var localFluxResources = map[schema.GroupVersionKind]schema.GroupVersionResource{
	{Group: "", Version: "v1", Kind: "Namespace"}:                                    {Group: "", Version: "v1", Resource: "namespaces"},
	{Group: "", Version: "v1", Kind: "ServiceAccount"}:                               {Group: "", Version: "v1", Resource: "serviceaccounts"},
	{Group: "", Version: "v1", Kind: "Service"}:                                      {Group: "", Version: "v1", Resource: "services"},
	{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}: {Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}:         {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"}:  {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},
	{Group: "scheduling.k8s.io", Version: "v1", Kind: "PriorityClass"}:               {Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses"},
	{Group: "apps", Version: "v1", Kind: "Deployment"}:                               {Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "policy", Version: "v1", Kind: "PodDisruptionBudget"}:                    {Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
	{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"}:               {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
}

// EnsureLocalFlux applies the exact Flux distribution embedded in this
// release. Helm controls this bootstrap; Flux does not own the Astronomer
// release that created it.
func EnsureLocalFlux(ctx context.Context, config *rest.Config) error {
	if config == nil {
		return errors.New("local Flux bootstrap requires a Kubernetes REST config")
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create local Flux client: %w", err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(fluxdistribution.InstallYAML()), 64<<10)
	force := true
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("decode local Flux distribution: %w", err)
		}
		if object.GetKind() == "" {
			continue
		}
		resource, ok := localFluxResources[object.GroupVersionKind()]
		if !ok {
			return fmt.Errorf("local Flux distribution contains unsupported kind %s", object.GroupVersionKind().String())
		}
		payload, err := object.MarshalJSON()
		if err != nil {
			return fmt.Errorf("marshal local Flux object %s/%s: %w", object.GetKind(), object.GetName(), err)
		}
		interfaceFor := client.Resource(resource)
		var target dynamic.ResourceInterface = interfaceFor
		if namespace := object.GetNamespace(); namespace != "" {
			target = interfaceFor.Namespace(namespace)
		}
		if _, err := target.Patch(ctx, object.GetName(), types.ApplyPatchType, payload, metav1.PatchOptions{FieldManager: localFluxFieldManager, Force: &force}); err != nil {
			return fmt.Errorf("apply local Flux %s/%s: %w", object.GetKind(), object.GetName(), err)
		}
	}
}
