package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	namespaceGVK          = schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}
	secretGVK             = schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	serviceAccountGVK     = schema.GroupVersionKind{Version: "v1", Kind: "ServiceAccount"}
	roleGVK               = schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"}
	roleBindingGVK        = schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"}
	clusterRoleBindingGVK = schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"}
	gitRepositoryGVK      = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "GitRepository"}
	ociRepositoryGVK      = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "OCIRepository"}
	helmRepositoryGVK     = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "HelmRepository"}
	kustomizationGVK      = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"}
	helmReleaseGVK        = schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"}
)

// Materialization is the complete, ordered object set for one assignment.
// Callers apply all objects in order and may only invoke PlanPrune after every
// apply has succeeded.
type Materialization struct {
	ControlNamespace string
	TargetNamespace  string
	Objects          []*unstructured.Unstructured
}

type ObjectIdentity struct {
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
}

func (i ObjectIdentity) String() string {
	group := i.Group
	if group == "" {
		group = "core"
	}
	return group + "/" + i.Version + ", Kind=" + i.Kind + " " + i.Namespace + "/" + i.Name
}

// BuildAssignment first revalidates the assignment against local policy, then
// constructs the full object graph without performing Kubernetes I/O.
func BuildAssignment(assignment protocol.DeliveryAssignmentV2, capabilities Capabilities, policy ValidationPolicy) (Materialization, error) {
	if err := ValidateAssignment(assignment, capabilities, policy); err != nil {
		return Materialization{}, err
	}
	names := Names(assignment.ProjectID, assignment.DeploymentID)
	targetNamespace, _ := rendererBoundary(assignment.Renderer)
	result := Materialization{ControlNamespace: names.ControlNamespace, TargetNamespace: targetNamespace}

	result.Objects = append(result.Objects, projectNamespace(assignment, names))
	authData, trustData, err := partitionCredential(assignment.Credential)
	if err != nil {
		return Materialization{}, err
	}
	if len(authData) != 0 {
		result.Objects = append(result.Objects, secretObject(assignment, names, names.AuthSecret, authData))
	}
	if len(trustData) != 0 {
		result.Objects = append(result.Objects, secretObject(assignment, names, names.TrustSecret, trustData))
	}
	result.Objects = append(result.Objects, serviceAccountObject(assignment, names))
	if assignment.Scope == protocol.DeliveryScopeNamespace {
		result.Objects = append(result.Objects, namespaceRoleObject(assignment, names, targetNamespace))
		result.Objects = append(result.Objects, namespaceRoleBindingObject(assignment, names, targetNamespace))
	} else {
		result.Objects = append(result.Objects, platformRoleBindingObject(assignment, names))
	}

	source, err := sourceObject(assignment, names)
	if err != nil {
		return Materialization{}, err
	}
	result.Objects = append(result.Objects, source)
	reconciler, err := reconcilerObject(assignment, names)
	if err != nil {
		return Materialization{}, err
	}
	result.Objects = append(result.Objects, reconciler)
	return result, nil
}

func projectNamespace(assignment protocol.DeliveryAssignmentV2, names ObjectNames) *unstructured.Unstructured {
	labels := map[string]any{
		ManagedByLabel:     ManagedByValue,
		ProjectIDHashLabel: projectHash(assignment.ProjectID),
	}
	return object(namespaceGVK, "", names.ControlNamespace, labels, nil, nil)
}

func secretObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames, name string, data map[string][]byte) *unstructured.Unstructured {
	secretData := make(map[string]any, len(data))
	for key, value := range data {
		secretData[key] = append([]byte(nil), value...)
	}
	secretType := "Opaque"
	if len(data) == 1 && data[".dockerconfigjson"] != nil {
		secretType = "kubernetes.io/dockerconfigjson"
	}
	return managedObject(assignment, secretGVK, names.ControlNamespace, name, map[string]any{
		"type": secretType,
		"data": secretData,
	})
}

func serviceAccountObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames) *unstructured.Unstructured {
	return managedObject(assignment, serviceAccountGVK, names.ControlNamespace, names.Applier, map[string]any{
		"automountServiceAccountToken": false,
	})
}

// Namespace-scoped assignments intentionally omit serviceaccounts and RBAC
// APIs so released source content cannot mint identities or escalate by binding
// pre-existing ClusterRoles. Custom-resource permissions require a future typed
// bundle permission declaration rather than an unsafe wildcard.
func namespaceRoleObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames, targetNamespace string) *unstructured.Unstructured {
	rules := []any{
		map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps", "endpoints", "events", "persistentvolumeclaims", "pods", "pods/log", "secrets", "services"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"apps"}, "resources": []any{"daemonsets", "deployments", "replicasets", "statefulsets", "controllerrevisions"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"batch"}, "resources": []any{"cronjobs", "jobs"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"autoscaling"}, "resources": []any{"horizontalpodautoscalers"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"networking.k8s.io"}, "resources": []any{"ingresses", "networkpolicies"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"policy"}, "resources": []any{"poddisruptionbudgets"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"coordination.k8s.io"}, "resources": []any{"leases"}, "verbs": workloadVerbs()},
		map[string]any{"apiGroups": []any{"discovery.k8s.io"}, "resources": []any{"endpointslices"}, "verbs": workloadVerbs()},
	}
	return managedObject(assignment, roleGVK, targetNamespace, names.Applier, map[string]any{"rules": rules})
}

func workloadVerbs() []any {
	return []any{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"}
}

func namespaceRoleBindingObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames, targetNamespace string) *unstructured.Unstructured {
	return managedObject(assignment, roleBindingGVK, targetNamespace, names.Applier, map[string]any{
		"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": names.Applier},
		"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": names.Applier, "namespace": names.ControlNamespace}},
	})
}

// Platform bindings can reference only the fixed, bootstrap-owned ClusterRole;
// assignment data can never provide a role name or rules.
func platformRoleBindingObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames) *unstructured.Unstructured {
	return managedObject(assignment, clusterRoleBindingGVK, "", names.Applier, map[string]any{
		"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": PlatformClusterRole},
		"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": names.Applier, "namespace": names.ControlNamespace}},
	})
}

func sourceObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames) (*unstructured.Unstructured, error) {
	source := assignment.Source
	spec := map[string]any{
		"interval": assignment.Policy.Interval,
		"url":      source.URL,
		"suspend":  assignment.Action == protocol.DeliveryActionSuspend,
	}
	if source.Provider != "" && source.Provider != "generic" {
		spec["provider"] = source.Provider
	}
	if source.CredentialSecret != "" {
		spec["secretRef"] = map[string]any{"name": names.AuthSecret}
	}

	gvk := gitRepositoryGVK
	switch source.Kind {
	case protocol.DeliverySourceGit:
		spec["ref"] = map[string]any{"commit": source.Revision}
		if source.Verify != nil {
			spec["verify"] = map[string]any{"mode": source.Verify.Mode, "secretRef": map[string]any{"name": names.TrustSecret}}
		}
	case protocol.DeliverySourceOCIArtifact:
		gvk = ociRepositoryGVK
		spec["ref"] = map[string]any{"digest": source.Digest}
		addOCIVerification(spec, source, names)
	case protocol.DeliverySourceHelmHTTP:
		gvk = helmRepositoryGVK
	case protocol.DeliverySourceHelmOCI:
		gvk = ociRepositoryGVK
		spec["url"] = strings.TrimSuffix(source.URL, "/") + "/" + strings.TrimPrefix(source.Chart, "/")
		spec["ref"] = map[string]any{"digest": source.Digest}
		addOCIVerification(spec, source, names)
	default:
		return nil, fmt.Errorf("unsupported source kind %q", source.Kind)
	}
	return managedObject(assignment, gvk, names.ControlNamespace, names.Source, map[string]any{"spec": spec}), nil
}

func addOCIVerification(spec map[string]any, source protocol.DeliverySourceV2, names ObjectNames) {
	if source.Verify == nil {
		return
	}
	verify := map[string]any{"provider": source.Verify.Provider}
	if source.TrustSecret != "" {
		verify["secretRef"] = map[string]any{"name": names.TrustSecret}
	}
	if len(source.Verify.OIDCIdentities) != 0 {
		identities := make([]any, 0, len(source.Verify.OIDCIdentities))
		for _, identity := range source.Verify.OIDCIdentities {
			identities = append(identities, map[string]any{"issuer": identity.Issuer, "subject": identity.Subject})
		}
		verify["matchOIDCIdentity"] = identities
	}
	spec["verify"] = verify
}

func reconcilerObject(assignment protocol.DeliveryAssignmentV2, names ObjectNames) (*unstructured.Unstructured, error) {
	if assignment.Renderer.Kind == protocol.DeliveryRendererKustomize {
		config := assignment.Renderer.Kustomize
		spec := map[string]any{
			"interval":           assignment.Policy.Interval,
			"timeout":            assignment.Policy.Timeout,
			"path":               fluxPath(assignment.Source.Path),
			"prune":              config.Prune,
			"wait":               config.Wait,
			"serviceAccountName": names.Applier,
			"sourceRef":          map[string]any{"kind": sourceKind(assignment.Source.Kind), "name": names.Source},
			"suspend":            assignment.Action == protocol.DeliveryActionSuspend,
			"targetNamespace":    config.TargetNamespace,
		}
		if assignment.Policy.RetryInterval != "" {
			spec["retryInterval"] = assignment.Policy.RetryInterval
		}
		if len(config.Patches) != 0 {
			patches := make([]any, 0, len(config.Patches))
			for _, patch := range config.Patches {
				patches = append(patches, map[string]any{"patch": patch})
			}
			spec["patches"] = patches
		}
		if len(config.Substitutions) != 0 {
			substitute := make(map[string]any, len(config.Substitutions))
			for key, value := range config.Substitutions {
				substitute[key] = value
			}
			spec["postBuild"] = map[string]any{"substitute": substitute}
		}
		if len(config.HealthChecks) != 0 {
			health := make([]any, 0, len(config.HealthChecks))
			for _, check := range config.HealthChecks {
				entry := map[string]any{"apiVersion": check.APIVersion, "kind": check.Kind, "name": check.Name}
				if check.Namespace != "" {
					entry["namespace"] = check.Namespace
				}
				health = append(health, entry)
			}
			spec["healthChecks"] = health
		}
		addDependencies(spec, config.DependencyNames)
		return managedObject(assignment, kustomizationGVK, names.ControlNamespace, names.Base, map[string]any{"spec": spec}), nil
	}

	config := assignment.Renderer.Helm
	spec := map[string]any{
		"interval":           assignment.Policy.Interval,
		"timeout":            assignment.Policy.Timeout,
		"releaseName":        config.ReleaseName,
		"targetNamespace":    config.TargetNamespace,
		"serviceAccountName": names.Applier,
		"suspend":            assignment.Action == protocol.DeliveryActionSuspend,
		"install":            map[string]any{"remediation": map[string]any{"retries": int64(config.InstallRetries)}},
		"upgrade":            map[string]any{"remediation": map[string]any{"retries": int64(config.UpgradeRetries), "strategy": config.UpgradeRemediation}},
		"test":               map[string]any{"enable": config.EnableTests},
		"driftDetection":     map[string]any{"mode": config.DriftMode},
	}
	if assignment.Source.Kind == protocol.DeliverySourceHelmOCI {
		spec["chartRef"] = map[string]any{"kind": "OCIRepository", "name": names.Source}
	} else {
		spec["chart"] = map[string]any{
			"metadata": map[string]any{"labels": map[string]any{ManagedByLabel: ManagedByValue}},
			"spec": map[string]any{
				"chart":     config.Chart,
				"version":   config.Version,
				"sourceRef": map[string]any{"kind": "HelmRepository", "name": names.Source},
			},
		}
	}
	if len(config.Values) != 0 {
		var values any
		if err := json.Unmarshal(config.Values, &values); err != nil {
			return nil, fmt.Errorf("decode Helm values: %w", err)
		}
		spec["values"] = values
	}
	addDependencies(spec, config.DependencyNames)
	return managedObject(assignment, helmReleaseGVK, names.ControlNamespace, names.Base, map[string]any{"spec": spec}), nil
}

func addDependencies(spec map[string]any, dependencyNames []string) {
	if len(dependencyNames) == 0 {
		return
	}
	dependencies := make([]any, 0, len(dependencyNames))
	for _, name := range dependencyNames {
		dependencies = append(dependencies, map[string]any{"name": name})
	}
	spec["dependsOn"] = dependencies
}

func sourceKind(kind protocol.DeliverySourceKind) string {
	switch kind {
	case protocol.DeliverySourceGit:
		return "GitRepository"
	case protocol.DeliverySourceHelmHTTP:
		return "HelmRepository"
	default:
		return "OCIRepository"
	}
}

func fluxPath(path string) string {
	path = strings.TrimPrefix(path, "./")
	if path == "" || path == "." {
		return "./"
	}
	return "./" + path
}

func managedObject(assignment protocol.DeliveryAssignmentV2, gvk schema.GroupVersionKind, namespace, name string, body map[string]any) *unstructured.Unstructured {
	labels := map[string]any{
		ManagedByLabel:     ManagedByValue,
		DeploymentIDLabel:  assignment.DeploymentID,
		ProjectIDHashLabel: projectHash(assignment.ProjectID),
	}
	annotations := map[string]any{
		SpecDigestAnnotation: assignment.SpecDigest,
		GenerationAnnotation: strconv.FormatInt(assignment.Generation, 10),
	}
	return object(gvk, namespace, name, labels, annotations, body)
}

func object(gvk schema.GroupVersionKind, namespace, name string, labels, annotations, body map[string]any) *unstructured.Unstructured {
	metadata := map[string]any{"name": name}
	if namespace != "" {
		metadata["namespace"] = namespace
	}
	if len(labels) != 0 {
		metadata["labels"] = labels
	}
	if len(annotations) != 0 {
		metadata["annotations"] = annotations
	}
	value := map[string]any{"apiVersion": gvk.GroupVersion().String(), "kind": gvk.Kind, "metadata": metadata}
	for key, entry := range body {
		value[key] = entry
	}
	return &unstructured.Unstructured{Object: value}
}

func projectHash(projectID string) string {
	value := strings.ReplaceAll(projectID, "-", "")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func Identity(object *unstructured.Unstructured) ObjectIdentity {
	gvk := object.GroupVersionKind()
	return ObjectIdentity{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Namespace: object.GetNamespace(), Name: object.GetName()}
}

// PlanPrune returns only stale, correctly labeled objects inside the exact
// assignment boundary. It refuses to plan after a partial apply and fails
// closed if a managed candidate has an unexpected kind, namespace, or name.
func PlanPrune(assignment protocol.DeliveryAssignmentV2, materialization Materialization, existing []*unstructured.Unstructured, applySucceeded bool) ([]ObjectIdentity, error) {
	if !applySucceeded {
		return nil, errors.New("prune is forbidden until the complete expected object set applies successfully")
	}
	expected := make(map[ObjectIdentity]struct{}, len(materialization.Objects))
	for _, candidate := range materialization.Objects {
		expected[Identity(candidate)] = struct{}{}
	}
	stale := make([]ObjectIdentity, 0)
	for _, candidate := range existing {
		if candidate == nil {
			return nil, errors.New("prune candidate is nil")
		}
		labels := candidate.GetLabels()
		if labels[ManagedByLabel] != ManagedByValue || labels[DeploymentIDLabel] != assignment.DeploymentID {
			continue
		}
		if labels[ProjectIDHashLabel] != projectHash(assignment.ProjectID) {
			return nil, fmt.Errorf("managed prune candidate %s has a mismatched project boundary", Identity(candidate))
		}
		identity := Identity(candidate)
		if _, keep := expected[identity]; keep {
			continue
		}
		if err := validatePruneIdentity(assignment, materialization, identity); err != nil {
			return nil, err
		}
		stale = append(stale, identity)
	}
	sortDeleteOrder(stale)
	return stale, nil
}

// PlanDeletion verifies that the tombstone exactly fences the accepted
// assignment. Orphan tombstones deliberately return no deletion operations.
func PlanDeletion(assignment protocol.DeliveryAssignmentV2, tombstone protocol.DeliveryDeletionV2, materialization Materialization) ([]ObjectIdentity, error) {
	if tombstone.DeploymentID != assignment.DeploymentID || tombstone.Generation != assignment.Generation || tombstone.SpecDigest != assignment.SpecDigest {
		return nil, errors.New("deletion tombstone does not match the accepted assignment generation and digest")
	}
	if tombstone.Orphan {
		return nil, nil
	}
	identities := make([]ObjectIdentity, 0, len(materialization.Objects)-1)
	for _, candidate := range materialization.Objects {
		identity := Identity(candidate)
		if identity.Kind == "Namespace" {
			continue
		}
		if err := validatePruneIdentity(assignment, materialization, identity); err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	sortDeleteOrder(identities)
	return identities, nil
}

func validatePruneIdentity(assignment protocol.DeliveryAssignmentV2, materialization Materialization, identity ObjectIdentity) error {
	allowed := map[schema.GroupVersionKind]bool{
		secretGVK: true, serviceAccountGVK: true, roleGVK: true, roleBindingGVK: true,
		gitRepositoryGVK: true, ociRepositoryGVK: true, helmRepositoryGVK: true,
		kustomizationGVK: true, helmReleaseGVK: true,
	}
	if assignment.Scope == protocol.DeliveryScopePlatform {
		allowed[clusterRoleBindingGVK] = true
	}
	gvk := schema.GroupVersionKind{Group: identity.Group, Version: identity.Version, Kind: identity.Kind}
	if !allowed[gvk] {
		return fmt.Errorf("refusing to prune unexpected managed kind %s", identity)
	}
	names := Names(assignment.ProjectID, assignment.DeploymentID)
	if identity.Name != names.Applier && identity.Name != names.AuthSecret && identity.Name != names.TrustSecret && identity.Name != names.Source && identity.Name != names.Base {
		return fmt.Errorf("refusing to prune unexpected managed name %s", identity)
	}
	if identity.Namespace == "" {
		if assignment.Scope != protocol.DeliveryScopePlatform || gvk != clusterRoleBindingGVK || identity.Name != names.Applier {
			return fmt.Errorf("refusing to prune unexpected cluster-scoped object %s", identity)
		}
		return nil
	}
	if identity.Namespace != materialization.ControlNamespace && identity.Namespace != materialization.TargetNamespace {
		return fmt.Errorf("refusing to prune object outside assignment namespaces: %s", identity)
	}
	if identity.Namespace == materialization.TargetNamespace && gvk != roleGVK && gvk != roleBindingGVK {
		return fmt.Errorf("refusing to prune non-RBAC object from workload namespace: %s", identity)
	}
	return nil
}

func sortDeleteOrder(identities []ObjectIdentity) {
	sort.Slice(identities, func(i, j int) bool {
		left, right := deleteRank(identities[i].Kind), deleteRank(identities[j].Kind)
		if left != right {
			return left < right
		}
		return identities[i].String() < identities[j].String()
	})
}

func deleteRank(kind string) int {
	switch kind {
	case "Kustomization", "HelmRelease":
		return 0
	case "GitRepository", "OCIRepository", "HelmRepository":
		return 1
	case "RoleBinding", "ClusterRoleBinding":
		return 2
	case "Role":
		return 3
	case "Secret":
		return 4
	case "ServiceAccount":
		return 5
	default:
		return 100
	}
}
