package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const deliveryFieldManager = "astronomer-agent-delivery"

var deliveryResources = map[schema.GroupVersionKind]struct {
	resource   schema.GroupVersionResource
	namespaced bool
}{
	namespaceGVK:          {resource: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	secretGVK:             {resource: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, namespaced: true},
	serviceAccountGVK:     {resource: schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, namespaced: true},
	roleGVK:               {resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, namespaced: true},
	roleBindingGVK:        {resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, namespaced: true},
	clusterRoleBindingGVK: {resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}},
	gitRepositoryGVK:      {resource: schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"}, namespaced: true},
	ociRepositoryGVK:      {resource: schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}, namespaced: true},
	helmRepositoryGVK:     {resource: schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"}, namespaced: true},
	kustomizationGVK:      {resource: schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}, namespaced: true},
	helmReleaseGVK:        {resource: schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}, namespaced: true},
}

// Executor is the complete Kubernetes side-effect boundary for workload
// delivery. It addresses only the closed resource set emitted by the
// materializer and uses server-side apply without force, so ownership conflicts
// fail closed instead of silently taking fields from another controller.
type Executor struct {
	client dynamic.Interface
}

func NewExecutor(client dynamic.Interface) (*Executor, error) {
	if client == nil {
		return nil, errors.New("delivery dynamic client is required")
	}
	return &Executor{client: client}, nil
}

// Apply applies the complete precomputed object graph in dependency order. It
// returns only after every object has been accepted by the apiserver. Callers
// must not prune when this method returns an error.
func (e *Executor) Apply(ctx context.Context, materialization Materialization) error {
	if len(materialization.Objects) == 0 {
		return errors.New("delivery materialization is empty")
	}
	for _, object := range materialization.Objects {
		if object == nil {
			return errors.New("delivery materialization contains a nil object")
		}
		if err := e.applyObject(ctx, object); err != nil {
			return fmt.Errorf("apply %s: %w", Identity(object), err)
		}
	}
	return nil
}

func (e *Executor) applyObject(ctx context.Context, object *unstructured.Unstructured) error {
	resource, namespaced, err := deliveryResource(object.GroupVersionKind())
	if err != nil {
		return err
	}
	if namespaced != (object.GetNamespace() != "") {
		return fmt.Errorf("object %s has an invalid namespace boundary", Identity(object))
	}
	payload, err := json.Marshal(object.Object)
	if err != nil {
		return fmt.Errorf("encode apply payload: %w", err)
	}
	options := metav1.PatchOptions{FieldManager: deliveryFieldManager}
	_, err = resourceInterface(e.client, resource, namespaced, object.GetNamespace()).Patch(
		ctx, object.GetName(), types.ApplyPatchType, payload, options,
	)
	return err
}

// Existing returns the bounded set of Astronomer-labeled objects that can be
// considered for pruning for one assignment. Namespace objects are shared by
// all project deployments and therefore are deliberately excluded.
func (e *Executor) Existing(ctx context.Context, assignment protocol.DeliveryAssignmentV2, materialization Materialization) ([]*unstructured.Unstructured, error) {
	selector := ManagedByLabel + "=" + ManagedByValue + "," + DeploymentIDLabel + "=" + assignment.DeploymentID
	type listTarget struct {
		gvk       schema.GroupVersionKind
		namespace string
	}
	targets := []listTarget{
		{secretGVK, materialization.ControlNamespace},
		{serviceAccountGVK, materialization.ControlNamespace},
		{roleGVK, materialization.TargetNamespace},
		{roleBindingGVK, materialization.TargetNamespace},
		{gitRepositoryGVK, materialization.ControlNamespace},
		{ociRepositoryGVK, materialization.ControlNamespace},
		{helmRepositoryGVK, materialization.ControlNamespace},
		{kustomizationGVK, materialization.ControlNamespace},
		{helmReleaseGVK, materialization.ControlNamespace},
	}
	if assignment.Scope == protocol.DeliveryScopePlatform {
		targets = append(targets, listTarget{gvk: clusterRoleBindingGVK})
	}

	result := make([]*unstructured.Unstructured, 0, len(targets))
	for _, target := range targets {
		resource, namespaced, err := deliveryResource(target.gvk)
		if err != nil {
			return nil, err
		}
		list, err := resourceInterface(e.client, resource, namespaced, target.namespace).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
			Limit:         64,
		})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s in %q: %w", resource.Resource, target.namespace, err)
		}
		if list.GetContinue() != "" {
			return nil, fmt.Errorf("refusing unbounded prune candidate set for %s in %q", resource.Resource, target.namespace)
		}
		for index := range list.Items {
			candidate := list.Items[index].DeepCopy()
			candidate.SetGroupVersionKind(target.gvk)
			result = append(result, candidate)
		}
	}
	return result, nil
}

func (e *Executor) Prune(ctx context.Context, identities []ObjectIdentity) error {
	for _, identity := range identities {
		if err := e.delete(ctx, identity, metav1.DeletePropagationBackground); err != nil {
			return fmt.Errorf("prune %s: %w", identity, err)
		}
	}
	return nil
}

// BeginDeletion advances a tombstone without racing Flux finalizers. The
// reconciler is suspended and foreground-deleted first; sources, credentials,
// and RBAC are removed only after it is actually gone. The returned boolean is
// true only when every recorded object is absent.
func (e *Executor) BeginDeletion(ctx context.Context, assignment protocol.DeliveryAssignmentV2, tombstone protocol.DeliveryDeletionV2, materialization Materialization, recorded []ObjectIdentity) (bool, error) {
	if tombstone.DeploymentID != assignment.DeploymentID || tombstone.Generation != assignment.Generation || tombstone.SpecDigest != assignment.SpecDigest {
		return false, errors.New("deletion tombstone does not match the accepted assignment generation and digest")
	}
	if tombstone.Orphan {
		for _, identity := range recorded {
			if identity.Kind == "Kustomization" || identity.Kind == "HelmRelease" || identity.Kind == "GitRepository" || identity.Kind == "OCIRepository" || identity.Kind == "HelmRepository" {
				if err := e.suspend(ctx, assignment, materialization, identity); err != nil {
					return false, fmt.Errorf("suspend orphaned %s: %w", identity, err)
				}
			}
		}
		return true, nil
	}

	identities := append([]ObjectIdentity(nil), recorded...)
	sortDeleteOrder(identities)
	for _, identity := range identities {
		if identity.Kind == "Namespace" {
			continue
		}
		if err := validatePruneIdentity(assignment, materialization, identity); err != nil {
			return false, err
		}
	}

	reconcilerPresent := false
	for _, identity := range identities {
		if identity.Kind != "Kustomization" && identity.Kind != "HelmRelease" {
			continue
		}
		present, err := e.existsWithFence(ctx, identity, tombstone)
		if err != nil {
			return false, err
		}
		if !present {
			continue
		}
		reconcilerPresent = true
		if err := e.suspend(ctx, assignment, materialization, identity); err != nil {
			return false, fmt.Errorf("suspend %s: %w", identity, err)
		}
		if err := e.delete(ctx, identity, metav1.DeletePropagationForeground); err != nil {
			return false, fmt.Errorf("delete reconciler %s: %w", identity, err)
		}
	}
	if reconcilerPresent {
		return false, nil
	}

	remaining := false
	for _, identity := range identities {
		if identity.Kind == "Namespace" || identity.Kind == "Kustomization" || identity.Kind == "HelmRelease" {
			continue
		}
		present, err := e.existsWithFence(ctx, identity, tombstone)
		if err != nil {
			return false, err
		}
		if !present {
			continue
		}
		remaining = true
		if err := e.delete(ctx, identity, metav1.DeletePropagationBackground); err != nil {
			return false, fmt.Errorf("delete %s: %w", identity, err)
		}
	}
	return !remaining, nil
}

func (e *Executor) suspend(ctx context.Context, assignment protocol.DeliveryAssignmentV2, materialization Materialization, identity ObjectIdentity) error {
	if err := validatePruneIdentity(assignment, materialization, identity); err != nil {
		return err
	}
	resource, namespaced, err := resourceForIdentity(identity)
	if err != nil {
		return err
	}
	patch := []byte(`{"spec":{"suspend":true}}`)
	_, err = resourceInterface(e.client, resource, namespaced, identity.Namespace).Patch(
		ctx, identity.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: deliveryFieldManager},
	)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (e *Executor) existsWithFence(ctx context.Context, identity ObjectIdentity, tombstone protocol.DeliveryDeletionV2) (bool, error) {
	resource, namespaced, err := resourceForIdentity(identity)
	if err != nil {
		return false, err
	}
	object, err := resourceInterface(e.client, resource, namespaced, identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read deletion candidate %s: %w", identity, err)
	}
	labels, annotations := object.GetLabels(), object.GetAnnotations()
	if labels[ManagedByLabel] != ManagedByValue || labels[DeploymentIDLabel] != tombstone.DeploymentID ||
		annotations[SpecDigestAnnotation] != tombstone.SpecDigest || annotations[GenerationAnnotation] != strconv.FormatInt(tombstone.Generation, 10) {
		return false, fmt.Errorf("refusing deletion because object fence changed: %s", identity)
	}
	return true, nil
}

func (e *Executor) delete(ctx context.Context, identity ObjectIdentity, propagation metav1.DeletionPropagation) error {
	resource, namespaced, err := resourceForIdentity(identity)
	if err != nil {
		return err
	}
	err = resourceInterface(e.client, resource, namespaced, identity.Namespace).Delete(ctx, identity.Name, metav1.DeleteOptions{PropagationPolicy: &propagation})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (e *Executor) Get(ctx context.Context, identity ObjectIdentity) (*unstructured.Unstructured, error) {
	resource, namespaced, err := resourceForIdentity(identity)
	if err != nil {
		return nil, err
	}
	object, err := resourceInterface(e.client, resource, namespaced, identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	object.SetGroupVersionKind(schema.GroupVersionKind{Group: identity.Group, Version: identity.Version, Kind: identity.Kind})
	return object, nil
}

func deliveryResource(gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool, error) {
	mapping, found := deliveryResources[gvk]
	if !found {
		return schema.GroupVersionResource{}, false, fmt.Errorf("resource kind %s is outside the delivery allowlist", gvk.String())
	}
	return mapping.resource, mapping.namespaced, nil
}

func resourceForIdentity(identity ObjectIdentity) (schema.GroupVersionResource, bool, error) {
	return deliveryResource(schema.GroupVersionKind{Group: identity.Group, Version: identity.Version, Kind: identity.Kind})
}

func resourceInterface(client dynamic.Interface, resource schema.GroupVersionResource, namespaced bool, namespace string) dynamic.ResourceInterface {
	if namespaced {
		return client.Resource(resource).Namespace(namespace)
	}
	return client.Resource(resource)
}

func materializationIdentities(materialization Materialization) []ObjectIdentity {
	result := make([]ObjectIdentity, 0, len(materialization.Objects))
	seen := make(map[ObjectIdentity]struct{}, len(materialization.Objects))
	for _, object := range materialization.Objects {
		identity := Identity(object)
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, identity)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
