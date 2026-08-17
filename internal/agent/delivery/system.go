package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	systemObjectName       = "astronomer-system-release"
	systemApplierName      = "astronomer-delivery-system-applier"
	systemOwnershipLabel   = "delivery.astronomer.io/system"
	systemFieldManager     = "astronomer-agent-system"
	defaultAgentNamespace  = "astronomer-system"
	defaultAgentDeployment = "astronomer-agent"
)

type SystemTrustPolicy struct {
	OIDCIdentities            []protocol.DeliveryOIDCIdentity
	KeyFingerprints           []string
	AgentImageRepositories    []string
	AllowedStorageTransitions []string
}

type SystemManagerConfig struct {
	CurrentAgentVersion string
	AgentNamespace      string
	AgentDeployment     string
	TrustPolicy         SystemTrustPolicy
}

// SystemManager owns the intentionally tiny, fixed-name system-reconciliation
// surface. It cannot apply arbitrary objects: it writes one registry Secret,
// one trust Secret, one OCIRepository, one Kustomization, and the image of the
// agent's own Deployment. The signed OCI artifact is what carries the reviewed
// Flux manifests.
type SystemManager struct {
	dynamic dynamic.Interface
	client  kubernetes.Interface
	config  SystemManagerConfig
}

func NewSystemManager(dynamicClient dynamic.Interface, client kubernetes.Interface, config SystemManagerConfig) (*SystemManager, error) {
	if dynamicClient == nil || client == nil {
		return nil, errors.New("system manager requires dynamic and typed Kubernetes clients")
	}
	if strings.TrimSpace(config.CurrentAgentVersion) == "" {
		return nil, errors.New("system manager current agent version is required")
	}
	if config.AgentNamespace == "" {
		config.AgentNamespace = defaultAgentNamespace
	}
	if config.AgentDeployment == "" {
		config.AgentDeployment = defaultAgentDeployment
	}
	return &SystemManager{dynamic: dynamicClient, client: client, config: config}, nil
}

// Reconcile returns complete only after the agent version and signed system
// Kustomization are both observed Ready. An agent-image change deliberately
// returns incomplete after patching: the new pod restarts, re-pulls the same
// authoritative snapshot, and only then upgrades Flux.
func (m *SystemManager) Reconcile(ctx context.Context, release protocol.DeliverySystemReleaseV2) (complete bool, err error) {
	defer zeroSystemCredential(&release)
	if err := m.validateRelease(release); err != nil {
		return false, err
	}
	if normalizeVersion(m.config.CurrentAgentVersion) != normalizeVersion(release.AgentVersion) {
		changed, err := m.reconcileAgentImage(ctx, release.AgentImage)
		if err != nil {
			return false, err
		}
		if changed {
			return false, nil
		}
		return false, fmt.Errorf("running agent version %q does not match desired %q", m.config.CurrentAgentVersion, release.AgentVersion)
	}

	objects := systemObjects(release)
	for _, object := range objects {
		if err := m.applyOwned(ctx, object); err != nil {
			return false, err
		}
	}
	if release.Suspend {
		return true, nil
	}
	return m.ready(ctx, release)
}

func (m *SystemManager) validateRelease(release protocol.DeliverySystemReleaseV2) error {
	if err := release.Validate(); err != nil {
		return err
	}
	minimum, err := semver.NewVersion(strings.TrimPrefix(release.MinimumKubernetes, "v"))
	if err != nil {
		return fmt.Errorf("parse minimum Kubernetes version: %w", err)
	}
	maximum, err := semver.NewVersion(strings.TrimPrefix(release.MaximumKubernetes, "v"))
	if err != nil || maximum.LessThan(minimum) {
		return errors.New("system Kubernetes compatibility range is invalid")
	}
	serverVersion, err := m.client.Discovery().ServerVersion()
	if err != nil || serverVersion == nil {
		return fmt.Errorf("discover Kubernetes version: %w", err)
	}
	current, err := semver.NewVersion(strings.TrimPrefix(serverVersion.GitVersion, "v"))
	if err != nil || current.LessThan(minimum) || current.GreaterThan(maximum) {
		return fmt.Errorf("Kubernetes %q is outside system release range %s-%s", serverVersion.GitVersion, minimum, maximum)
	}
	if !containsString(m.config.TrustPolicy.AgentImageRepositories, imageRepository(release.AgentImage)) {
		return errors.New("system release agent image repository is not allowlisted")
	}
	verification := release.Verification
	if len(verification.PublicKey) != 0 {
		fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(verification.PublicKey))
		if fingerprint != verification.KeyFingerprint || !containsString(m.config.TrustPolicy.KeyFingerprints, fingerprint) {
			return errors.New("system release public key is not trusted")
		}
	} else {
		for _, identity := range verification.OIDCIdentities {
			if !containsOIDCIdentity(m.config.TrustPolicy.OIDCIdentities, identity) {
				return errors.New("system release OIDC identity is not trusted")
			}
		}
	}
	if release.PreviousStorageVersion != "" && release.PreviousStorageVersion != release.CRDStorageVersion {
		transition := release.PreviousStorageVersion + "->" + release.CRDStorageVersion
		if !containsString(m.config.TrustPolicy.AllowedStorageTransitions, transition) {
			return fmt.Errorf("CRD storage transition %q is not qualified", transition)
		}
	}
	return nil
}

func (m *SystemManager) reconcileAgentImage(ctx context.Context, desired string) (bool, error) {
	changed := false
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployments := m.client.AppsV1().Deployments(m.config.AgentNamespace)
		deployment, err := deployments.Get(ctx, m.config.AgentDeployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		next := deployment.DeepCopy()
		found := false
		for index := range next.Spec.Template.Spec.Containers {
			container := &next.Spec.Template.Spec.Containers[index]
			if container.Name != "agent" && container.Name != m.config.AgentDeployment {
				continue
			}
			found = true
			if container.Image == desired {
				return nil
			}
			if !containsString(m.config.TrustPolicy.AgentImageRepositories, imageRepository(container.Image)) {
				return errors.New("current agent image repository is not allowlisted")
			}
			container.Image = desired
			changed = true
			break
		}
		if !found {
			return errors.New("agent Deployment has no owned agent container")
		}
		if !changed {
			return nil
		}
		_, err = deployments.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
	return changed, err
}

func (m *SystemManager) applyOwned(ctx context.Context, object *unstructured.Unstructured) error {
	gvr, namespaced, err := systemResource(object.GroupVersionKind())
	if err != nil {
		return err
	}
	resource := resourceInterface(m.dynamic, gvr, namespaced, object.GetNamespace())
	current, err := resource.Get(ctx, object.GetName(), metav1.GetOptions{})
	if err == nil {
		if current.GetLabels()[ManagedByLabel] != ManagedByValue || current.GetLabels()[systemOwnershipLabel] != "true" {
			return fmt.Errorf("refusing to take ownership of %s %s/%s", object.GetKind(), object.GetNamespace(), object.GetName())
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	payload, err := json.Marshal(object.Object)
	if err != nil {
		return err
	}
	_, err = resource.Patch(ctx, object.GetName(), types.ApplyPatchType, payload, metav1.PatchOptions{FieldManager: systemFieldManager})
	if err != nil {
		return fmt.Errorf("apply system %s: %w", object.GetKind(), err)
	}
	return nil
}

func (m *SystemManager) ready(ctx context.Context, release protocol.DeliverySystemReleaseV2) (bool, error) {
	source, err := m.dynamic.Resource(ociRepositoryGVKToResource()).Namespace(DeliverySystemNamespace).Get(ctx, systemObjectName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	kustomization, err := m.dynamic.Resource(kustomizationGVKToResource()).Namespace(DeliverySystemNamespace).Get(ctx, systemObjectName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if !objectReady(source) || !objectReady(kustomization) {
		return false, nil
	}
	revision, _, _ := unstructured.NestedString(source.Object, "status", "artifact", "revision")
	digest, _, _ := unstructured.NestedString(source.Object, "status", "artifact", "digest")
	if !strings.Contains(revision, release.ArtifactDigest) && digest != release.ArtifactDigest {
		return false, nil
	}
	return true, nil
}

func systemObjects(release protocol.DeliverySystemReleaseV2) []*unstructured.Unstructured {
	labels := map[string]string{ManagedByLabel: ManagedByValue, systemOwnershipLabel: "true", "app.kubernetes.io/part-of": "astronomer-delivery"}
	objects := make([]*unstructured.Unstructured, 0, 4)
	if release.Credential != nil {
		objects = append(objects, systemObject(secretGVK, systemObjectName+"-auth", labels, map[string]any{
			"type": "kubernetes.io/dockerconfigjson", "data": map[string]any{".dockerconfigjson": append([]byte(nil), release.Credential.Data[".dockerconfigjson"]...)},
		}))
	}
	verification := map[string]any{"provider": "cosign"}
	if len(release.Verification.PublicKey) != 0 {
		objects = append(objects, systemObject(secretGVK, systemObjectName+"-trust", labels, map[string]any{
			"type": "Opaque", "data": map[string]any{"cosign.pub": append([]byte(nil), release.Verification.PublicKey...)},
		}))
		verification["secretRef"] = map[string]any{"name": systemObjectName + "-trust"}
	} else {
		identities := make([]any, 0, len(release.Verification.OIDCIdentities))
		for _, identity := range release.Verification.OIDCIdentities {
			identities = append(identities, map[string]any{"issuer": identity.Issuer, "subject": identity.Subject})
		}
		verification["matchOIDCIdentity"] = identities
	}
	sourceSpec := map[string]any{
		"interval": release.Interval, "url": release.ArtifactURL, "ref": map[string]any{"digest": release.ArtifactDigest},
		"verify": verification, "suspend": release.Suspend,
	}
	if release.Credential != nil {
		sourceSpec["secretRef"] = map[string]any{"name": systemObjectName + "-auth"}
	}
	objects = append(objects, systemObject(ociRepositoryGVK, systemObjectName, labels, map[string]any{"spec": sourceSpec}))
	objects = append(objects, systemObject(kustomizationGVK, systemObjectName, labels, map[string]any{"spec": map[string]any{
		"interval": release.Interval, "retryInterval": "1m", "timeout": release.Timeout, "path": "./", "prune": true, "wait": true,
		"force": false, "suspend": release.Suspend, "serviceAccountName": systemApplierName,
		"sourceRef": map[string]any{"apiVersion": "source.toolkit.fluxcd.io/v1", "kind": "OCIRepository", "name": systemObjectName},
		"postBuild": map[string]any{"substitute": map[string]any{"ASTRONOMER_SYSTEM_GENERATION": fmt.Sprintf("%d", release.Generation)}},
	}}))
	return objects
}

func systemObject(gvk schema.GroupVersionKind, name string, labels map[string]string, body map[string]any) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: body}
	object.SetGroupVersionKind(gvk)
	object.SetName(name)
	object.SetNamespace(DeliverySystemNamespace)
	object.SetLabels(labels)
	return object
}

func systemResource(gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool, error) {
	switch gvk {
	case secretGVK:
		return schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, true, nil
	case ociRepositoryGVK:
		return ociRepositoryGVKToResource(), true, nil
	case kustomizationGVK:
		return kustomizationGVKToResource(), true, nil
	default:
		return schema.GroupVersionResource{}, false, fmt.Errorf("unsupported system resource %s", gvk)
	}
}

func ociRepositoryGVKToResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}
}

func kustomizationGVKToResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}
}

func objectReady(object *unstructured.Unstructured) bool {
	observed, _, _ := unstructured.NestedInt64(object.Object, "status", "observedGeneration")
	if observed < object.GetGeneration() {
		return false
	}
	conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		if condition["type"] == "Ready" && condition["status"] == "True" {
			return true
		}
	}
	return false
}

func containsOIDCIdentity(allowed []protocol.DeliveryOIDCIdentity, candidate protocol.DeliveryOIDCIdentity) bool {
	for _, entry := range allowed {
		if entry == candidate {
			return true
		}
	}
	return false
}

func imageRepository(image string) string {
	if index := strings.Index(image, "@"); index >= 0 {
		return image[:index]
	}
	if index := strings.LastIndex(image, ":"); index > strings.LastIndex(image, "/") {
		return image[:index]
	}
	return image
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func zeroSystemCredential(release *protocol.DeliverySystemReleaseV2) {
	if release == nil || release.Credential == nil {
		return
	}
	keys := make([]string, 0, len(release.Credential.Data))
	for key := range release.Credential.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := release.Credential.Data[key]
		for index := range value {
			value[index] = 0
		}
		delete(release.Credential.Data, key)
	}
	release.Credential = nil
}
