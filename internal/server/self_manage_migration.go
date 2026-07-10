package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	chartdeploy "github.com/alphabravocompany/astronomer-go/deploy"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func preflightSelfManagedApplicationCredentialMigration(ctx context.Context, k8s kubernetes.Interface, dyn dynamic.Interface) error {
	current, err := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace).Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if rolloutErr := verifySelfManagedServerRolloutComplete(ctx, k8s); rolloutErr != nil {
			return fmt.Errorf("first self-managed Application creation requires a complete server rollout: %w", rolloutErr)
		}
		if controllerErr := verifyLocalArgoApplicationControllerStopped(ctx, k8s); controllerErr != nil {
			return fmt.Errorf("first self-managed Application creation requires a quiesced Argo controller: %w", controllerErr)
		}
		return nil
	}
	if err != nil {
		return err
	}
	persistentCopyProbe := current.DeepCopy()
	if operation, exists := current.Object["operation"]; !exists || !selfManagedApplicationHasUnsafeSourceCopies(map[string]any{"operation": operation}) {
		unstructured.RemoveNestedField(persistentCopyProbe.Object, "operation")
	}
	if !selfManagedApplicationHasUnsafeSourceCopies(persistentCopyProbe.Object) {
		return nil
	}
	if err := verifySelfManagedServerRolloutComplete(ctx, k8s); err != nil {
		return fmt.Errorf("unsafe legacy Application migration requires a complete server rollout: %w", err)
	}
	statusSubresource, err := applicationStatusSubresourceEnabled(ctx, dyn)
	if err != nil {
		return fmt.Errorf("verify Application CRD status semantics before credential migration: %w", err)
	}
	if statusSubresource {
		return fmt.Errorf("refuse credential migration: installed Application CRD enables the status subresource")
	}
	if err := verifyLocalArgoApplicationControllerStopped(ctx, k8s); err != nil {
		return fmt.Errorf("unsafe legacy Application requires an operator-gated migration before any credential mutation: %w", err)
	}
	return nil
}

func verifySelfManagedAdoptionSnapshot(ctx context.Context, k8s kubernetes.Interface, snapshot *selfManagedAdoptionSnapshot) error {
	if snapshot == nil || !snapshot.RuntimeAdoption {
		return nil
	}
	if !snapshot.RequireControllerStopped {
		return fmt.Errorf("bounded adoption snapshot does not require a quiesced Argo controller")
	}
	if err := verifyLocalArgoApplicationControllerStopped(ctx, k8s); err != nil {
		return fmt.Errorf("bounded adoption evidence changed before Application restage: Argo controller is not quiesced: %w", err)
	}
	if err := verifySelfManagedServerRolloutComplete(ctx, k8s); err != nil {
		return fmt.Errorf("bounded adoption evidence changed before Application restage: server rollout is incomplete: %w", err)
	}
	selected, err := currentHelmReleaseSelection(ctx, k8s)
	if err != nil {
		return fmt.Errorf("reselect highest deployed Helm release before bounded Application restage: %w", err)
	}
	if selected.Name != snapshot.ReleaseName || selected.UID != snapshot.ReleaseUID || selected.ResourceVersion != snapshot.ReleaseResourceVersion || selected.Version != snapshot.ReleaseVersion {
		return fmt.Errorf("bounded adoption evidence changed before Application restage: highest deployed Helm release identity/version changed")
	}
	for _, evidence := range snapshot.Objects {
		var object metav1.Object
		switch evidence.Resource {
		case "statefulsets":
			statefulSet, getErr := k8s.AppsV1().StatefulSets(localAstronomerNamespace).Get(ctx, evidence.Name, metav1.GetOptions{})
			if getErr == nil {
				object = statefulSet
			} else if !apierrors.IsNotFound(getErr) {
				return fmt.Errorf("reverify bounded adoption StatefulSet %s: %w", evidence.Name, getErr)
			}
			if getErr != nil && evidence.Present {
				return fmt.Errorf("bounded adoption evidence changed before Application restage: StatefulSet %s disappeared", evidence.Name)
			}
			if getErr == nil && !evidence.Present {
				return fmt.Errorf("bounded adoption evidence changed before Application restage: StatefulSet %s appeared", evidence.Name)
			}
		case "deployments":
			deployment, getErr := k8s.AppsV1().Deployments(localAstronomerNamespace).Get(ctx, evidence.Name, metav1.GetOptions{})
			if getErr == nil {
				object = deployment
			} else if !apierrors.IsNotFound(getErr) {
				return fmt.Errorf("reverify bounded adoption Deployment %s: %w", evidence.Name, getErr)
			}
			if getErr != nil && evidence.Present {
				return fmt.Errorf("bounded adoption evidence changed before Application restage: Deployment %s disappeared", evidence.Name)
			}
			if getErr == nil && !evidence.Present {
				return fmt.Errorf("bounded adoption evidence changed before Application restage: Deployment %s appeared", evidence.Name)
			}
		case "secrets":
			secret, getErr := k8s.CoreV1().Secrets(localAstronomerNamespace).Get(ctx, evidence.Name, metav1.GetOptions{})
			if getErr != nil {
				if apierrors.IsNotFound(getErr) {
					return fmt.Errorf("runtime adoption evidence changed before Application restage: Secret %s disappeared", evidence.Name)
				}
				return fmt.Errorf("reverify runtime adoption Secret %s: %w", evidence.Name, getErr)
			}
			object = secret
		case "configmaps":
			configMap, getErr := k8s.CoreV1().ConfigMaps(localAstronomerNamespace).Get(ctx, evidence.Name, metav1.GetOptions{})
			if getErr != nil {
				if apierrors.IsNotFound(getErr) {
					return fmt.Errorf("runtime adoption evidence changed before Application restage: ConfigMap %s disappeared", evidence.Name)
				}
				return fmt.Errorf("reverify runtime adoption ConfigMap %s: %w", evidence.Name, getErr)
			}
			object = configMap
		default:
			return fmt.Errorf("runtime adoption snapshot contains unsupported evidence resource %q", evidence.Resource)
		}
		if evidence.Present && (object.GetUID() != evidence.UID || object.GetResourceVersion() != evidence.ResourceVersion) {
			return fmt.Errorf("runtime adoption evidence changed before Application restage: %s %s identity/resourceVersion changed", evidence.Resource, evidence.Name)
		}
	}
	return nil
}

func ensureSelfManagedAstronomerApplication(ctx context.Context, k8s kubernetes.Interface, dyn dynamic.Interface, cluster sqlc.Cluster, valuesYAML string, adoptionSnapshots ...*selfManagedAdoptionSnapshot) error {
	var adoptionSnapshot *selfManagedAdoptionSnapshot
	if len(adoptionSnapshots) > 0 {
		adoptionSnapshot = adoptionSnapshots[0]
	}
	if err := validateSelfManagedHelmValues(valuesYAML); err != nil {
		return fmt.Errorf("refuse unsafe self-managed Helm values: %w", err)
	}
	repo, err := chartdeploy.AstronomerChartRepo()
	if err != nil {
		return fmt.Errorf("load embedded astronomer chart repo: %w", err)
	}
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]any{
				"name":      localArgoApplicationName,
				"namespace": localArgoNamespace,
				"labels": map[string]any{
					"astronomer.io/platform-owned": "true",
				},
			},
			"spec": map[string]any{
				"project":              "default",
				"revisionHistoryLimit": int64(0),
				"source": map[string]any{
					"repoURL":        localArgoRepoURL,
					"chart":          "astronomer",
					"targetRevision": repo.Version(),
					"helm": map[string]any{
						"releaseName": localAstronomerReleaseName,
						"values":      valuesYAML,
					},
				},
				"destination": map[string]any{
					"server":    cluster.ApiServerUrl,
					"namespace": localAstronomerNamespace,
				},
				"syncPolicy": map[string]any{
					"automated": map[string]any{
						"prune":    true,
						"selfHeal": true,
					},
				},
			},
		},
	}
	if err := validateSelfManagedApplicationSource(obj.Object); err != nil {
		return err
	}
	desiredSpec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	desiredHash, err := selfManagedSpecHash(desiredSpec)
	if err != nil {
		return err
	}
	res := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace)
	current, err := res.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if rolloutErr := verifySelfManagedServerRolloutComplete(ctx, k8s); rolloutErr != nil {
			return fmt.Errorf("first self-managed Application creation requires a complete server rollout: %w", rolloutErr)
		}
		if err := verifySelfManagedAdoptionSnapshot(ctx, k8s, adoptionSnapshot); err != nil {
			return err
		}
		stageSelfManagedApplication(obj, desiredHash)
		_, err = res.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	obj.SetFinalizers(approvedSelfManagedApplicationFinalizers(current.GetFinalizers()))
	persistentCopyProbe := current.DeepCopy()
	if operation, exists := current.Object["operation"]; !exists || !selfManagedApplicationHasUnsafeSourceCopies(map[string]any{"operation": operation}) {
		unstructured.RemoveNestedField(persistentCopyProbe.Object, "operation")
	}
	if !selfManagedApplicationHasUnsafeSourceCopies(persistentCopyProbe.Object) {
		currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
		annotations := current.GetAnnotations()
		if annotations[selfManagedPhaseAnnotation] == selfManagedPhaseAwaiting && annotations[selfManagedHashAnnotation] == desiredHash {
			operationSafe := selfManagedAwaitingOperationSafe(current, stagedSelfManagedSpec(desiredSpec))
			if approved := annotations[selfManagedApproveAnnotation]; approved == desiredHash {
				if !operationSafe {
					updated := current.DeepCopy()
					_ = unstructured.SetNestedMap(updated.Object, stagedSelfManagedSpec(desiredSpec), "spec")
					unstructured.RemoveNestedField(updated.Object, "operation")
					setSelfManagedApplicationMetadata(updated, selfManagedPhaseAwaiting, desiredHash)
					updated.SetFinalizers(obj.GetFinalizers())
					_, err = res.Update(ctx, updated, metav1.UpdateOptions{})
					return err
				}
				if _, operationExists := current.Object["operation"]; operationExists {
					return nil
				}
				if !selfManagedAcceptanceStatusReady(current, stagedSelfManagedSpec(desiredSpec)) {
					return nil
				}
				if err := verifySelfManagedServerRolloutComplete(ctx, k8s); err != nil {
					return fmt.Errorf("approved self-managed Application requires a complete server rollout before automated sync: %w", err)
				}
				updated := current.DeepCopy()
				if err := unstructured.SetNestedMap(updated.Object, desiredSpec, "spec"); err != nil {
					return err
				}
				setSelfManagedApplicationMetadata(updated, selfManagedPhaseActive, desiredHash)
				_, err = res.Update(ctx, updated, metav1.UpdateOptions{})
				return err
			}
			stagedSpec := stagedSelfManagedSpec(desiredSpec)
			if reflect.DeepEqual(currentSpec, stagedSpec) && operationSafe && selfManagedApplicationMetadataClean(current, selfManagedPhaseAwaiting, desiredHash) {
				return nil
			}
		}
		if reflect.DeepEqual(currentSpec, desiredSpec) && selfManagedApplicationMetadataClean(current, selfManagedPhaseActive, desiredHash) {
			return nil
		}
		if reflect.DeepEqual(currentSpec, desiredSpec) && annotations[selfManagedPhaseAnnotation] == selfManagedPhaseActive && annotations[selfManagedHashAnnotation] == desiredHash {
			updated := current.DeepCopy()
			setSelfManagedApplicationMetadata(updated, selfManagedPhaseActive, desiredHash)
			updated.SetFinalizers(obj.GetFinalizers())
			_, err = res.Update(ctx, updated, metav1.UpdateOptions{})
			return err
		}
		// Every first takeover and later desired-spec change is staged with sync
		// disabled. An operator must review the Argo diff and copy spec-hash into
		// approved-hash before prune/self-heal can be armed.
		updated := current.DeepCopy()
		if err := verifySelfManagedServerRolloutComplete(ctx, k8s); err != nil {
			return fmt.Errorf("self-managed restage requires a complete server rollout: %w", err)
		}
		if err := verifySelfManagedAdoptionSnapshot(ctx, k8s, adoptionSnapshot); err != nil {
			return err
		}
		if err := unstructured.SetNestedMap(updated.Object, stagedSelfManagedSpec(desiredSpec), "spec"); err != nil {
			return err
		}
		setSelfManagedApplicationMetadata(updated, selfManagedPhaseAwaiting, desiredHash)
		unstructured.RemoveNestedField(updated.Object, "operation")
		updated.SetFinalizers(obj.GetFinalizers())
		_, err = res.Update(ctx, updated, metav1.UpdateOptions{})
		return err
	}
	if k8s == nil {
		return fmt.Errorf("refuse unsafe self-managed Application migration without a Kubernetes client to quiesce Argo")
	}
	statusSubresource, err := applicationStatusSubresourceEnabled(ctx, dyn)
	if err != nil {
		return fmt.Errorf("verify Application CRD status semantics before credential scrub: %w", err)
	}
	if statusSubresource {
		return fmt.Errorf("refuse credential scrub: installed Application CRD enables the status subresource; upgrade the bounded status migration first")
	}
	if err := verifySelfManagedServerRolloutComplete(ctx, k8s); err != nil {
		return fmt.Errorf("unsafe legacy Application migration requires a complete server rollout: %w", err)
	}
	if err := verifyLocalArgoApplicationControllerStopped(ctx, k8s); err != nil {
		return fmt.Errorf("unsafe legacy Application requires an operator-gated migration: %w", err)
	}
	if err := verifySelfManagedAdoptionSnapshot(ctx, k8s, adoptionSnapshot); err != nil {
		return err
	}
	// The bundled Argo 9.5.21 CRD has no status subresource. A single full-object
	// replacement therefore removes legacy plaintext from spec, operation,
	// status.sync.comparedTo, status.operationState.syncResult, and every history
	// entry atomically while the controller is stopped.
	staged := obj.DeepCopy()
	stageSelfManagedApplication(staged, desiredHash)
	staged.SetResourceVersion(current.GetResourceVersion())
	if _, err = res.Update(ctx, staged, metav1.UpdateOptions{}); err != nil {
		return err
	}
	clean, err := res.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if _, hasStatus := clean.Object["status"]; hasStatus {
		return fmt.Errorf("self-managed Application status survived full-object scrub; automated sync remains disabled")
	}
	if _, hasOperation := clean.Object["operation"]; hasOperation {
		return fmt.Errorf("self-managed Application operation survived full-object scrub; automated sync remains disabled")
	}
	cleanSource, _, _ := unstructured.NestedMap(clean.Object, "spec", "source")
	desiredSource, _, _ := unstructured.NestedMap(staged.Object, "spec", "source")
	cleanPolicy, _, _ := unstructured.NestedMap(clean.Object, "spec", "syncPolicy")
	if selfManagedApplicationHasUnsafeSourceCopies(clean.Object) || !reflect.DeepEqual(cleanSource, desiredSource) || len(cleanPolicy) != 0 {
		return fmt.Errorf("self-managed Application still contains a secret-tainted source copy after scrub; automated sync remains disabled")
	}
	return nil
}

func selfManagedAwaitingOperationSafe(application *unstructured.Unstructured, stagedSpec map[string]any) bool {
	raw, exists := application.Object["operation"]
	if !exists {
		return true
	}
	operation, ok := raw.(map[string]any)
	if !ok || len(operation) == 0 {
		return false
	}
	for key := range operation {
		if key != "sync" && key != "initiatedBy" {
			return false
		}
	}
	syncOperation, ok := operation["sync"].(map[string]any)
	if !ok || len(syncOperation) == 0 {
		return false
	}
	for key := range syncOperation {
		switch key {
		case "revision", "prune", "dryRun", "syncOptions", "syncStrategy":
		default:
			return false
		}
	}
	if prune, exists := syncOperation["prune"]; exists {
		value, ok := prune.(bool)
		if !ok || value {
			return false
		}
	}
	if dryRun, exists := syncOperation["dryRun"]; exists {
		value, ok := dryRun.(bool)
		if !ok || value {
			return false
		}
	}
	if revision, exists := syncOperation["revision"]; exists {
		value, ok := revision.(string)
		if !ok {
			return false
		}
		targetRevision, _, _ := unstructured.NestedString(stagedSpec, "source", "targetRevision")
		if strings.TrimSpace(value) != "" && value != targetRevision {
			return false
		}
	}
	if options, exists := syncOperation["syncOptions"]; exists {
		list, ok := options.([]any)
		if !ok || len(list) != 0 {
			return false
		}
	}
	if rawStrategy, exists := syncOperation["syncStrategy"]; exists {
		strategy, ok := rawStrategy.(map[string]any)
		if !ok || len(strategy) != 1 {
			return false
		}
		if rawHook, hook := strategy["hook"]; hook {
			hookConfig, ok := rawHook.(map[string]any)
			if !ok || len(hookConfig) != 0 {
				return false
			}
		} else if rawApply, apply := strategy["apply"]; apply {
			applyConfig, ok := rawApply.(map[string]any)
			if !ok {
				return false
			}
			for key, value := range applyConfig {
				if key != "force" {
					return false
				}
				force, ok := value.(bool)
				if !ok || force {
					return false
				}
			}
		} else {
			return false
		}
	}
	if initiated, exists := operation["initiatedBy"]; exists {
		initiatedBy, ok := initiated.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range initiatedBy {
			switch key {
			case "username":
				username, ok := value.(string)
				if !ok || len(username) > 253 || strings.TrimSpace(username) != username || strings.IndexFunc(username, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
					return false
				}
			case "automated":
				automated, ok := value.(bool)
				if !ok || automated {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func selfManagedAcceptanceStatusReady(application *unstructured.Unstructured, stagedSpec map[string]any) bool {
	if _, operationExists := application.Object["operation"]; operationExists {
		return false
	}
	if syncStatus, _, _ := unstructured.NestedString(application.Object, "status", "sync", "status"); syncStatus != "Synced" {
		return false
	}
	if healthStatus, _, _ := unstructured.NestedString(application.Object, "status", "health", "status"); healthStatus != "Healthy" {
		return false
	}
	if phase, _, _ := unstructured.NestedString(application.Object, "status", "operationState", "phase"); phase != "Succeeded" {
		return false
	}
	completedOperation, foundOperation, _ := unstructured.NestedMap(application.Object, "status", "operationState", "operation")
	if !foundOperation {
		return false
	}
	completed := &unstructured.Unstructured{Object: map[string]any{"operation": completedOperation}}
	if !selfManagedAwaitingOperationSafe(completed, stagedSpec) {
		return false
	}
	desiredSource, _, _ := unstructured.NestedMap(stagedSpec, "source")
	desiredDestination, _, _ := unstructured.NestedMap(stagedSpec, "destination")
	comparedSource, foundCompared, _ := unstructured.NestedMap(application.Object, "status", "sync", "comparedTo", "source")
	comparedDestination, foundDestination, _ := unstructured.NestedMap(application.Object, "status", "sync", "comparedTo", "destination")
	resultSource, foundResult, _ := unstructured.NestedMap(application.Object, "status", "operationState", "syncResult", "source")
	return foundCompared && foundDestination && foundResult && reflect.DeepEqual(comparedSource, desiredSource) && reflect.DeepEqual(comparedDestination, desiredDestination) && reflect.DeepEqual(resultSource, desiredSource)
}

func selfManagedApplicationMetadataClean(application *unstructured.Unstructured, phase, hash string) bool {
	labels := application.GetLabels()
	if len(labels) != 1 || labels["astronomer.io/platform-owned"] != "true" {
		return false
	}
	annotations := application.GetAnnotations()
	if len(annotations) != 2 || annotations[selfManagedPhaseAnnotation] != phase || annotations[selfManagedHashAnnotation] != hash {
		return false
	}
	for _, finalizer := range application.GetFinalizers() {
		if len(approvedSelfManagedApplicationFinalizers([]string{finalizer})) != 1 {
			return false
		}
	}
	return true
}

func selfManagedSpecHash(spec map[string]any) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal self-managed spec hash: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func stagedSelfManagedSpec(desired map[string]any) map[string]any {
	copy := (&unstructured.Unstructured{Object: map[string]any{"spec": desired}}).DeepCopy().Object["spec"].(map[string]any)
	copy["syncPolicy"] = map[string]any{}
	return copy
}

func stageSelfManagedApplication(application *unstructured.Unstructured, hash string) {
	desired, _, _ := unstructured.NestedMap(application.Object, "spec")
	_ = unstructured.SetNestedMap(application.Object, stagedSelfManagedSpec(desired), "spec")
	setSelfManagedApplicationMetadata(application, selfManagedPhaseAwaiting, hash)
}

func setSelfManagedApplicationMetadata(application *unstructured.Unstructured, phase, hash string) {
	application.SetLabels(map[string]string{"astronomer.io/platform-owned": "true"})
	application.SetAnnotations(map[string]string{
		selfManagedPhaseAnnotation: phase,
		selfManagedHashAnnotation:  hash,
	})
}

func approvedSelfManagedApplicationFinalizers(current []string) []string {
	result := make([]string, 0, len(current))
	for _, finalizer := range current {
		if finalizer == "resources-finalizer.argocd.argoproj.io" || finalizer == "resources-finalizer.argocd.argoproj.io/background" || finalizer == "resources-finalizer.argocd.argoproj.io/foreground" {
			result = append(result, finalizer)
		}
	}
	return result
}

func selfManagedApplicationHasUnsafeSourceCopies(object map[string]any) bool {
	var visit func(any) bool
	visit = func(node any) bool {
		switch typed := node.(type) {
		case map[string]any:
			if manifests, ok := typed["manifests"].([]any); ok && len(manifests) > 0 {
				return true
			}
			if info, ok := typed["info"].([]any); ok && len(info) > 0 {
				return true
			}
			if sources, ok := typed["sources"].([]any); ok && len(sources) > 0 {
				return true
			}
			if looksLikeArgoApplicationSource(typed) {
				wrapper := map[string]any{"spec": map[string]any{"source": typed}}
				if validateSelfManagedApplicationSource(wrapper) != nil {
					return true
				}
			} else if _, ok := typed["helm"].(map[string]any); ok {
				wrapper := map[string]any{"spec": map[string]any{"source": typed}}
				if validateSelfManagedApplicationSource(wrapper) != nil {
					return true
				}
			}
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(object)
}

func looksLikeArgoApplicationSource(value map[string]any) bool {
	for _, key := range []string{"repoURL", "chart", "path", "targetRevision", "plugin", "directory", "kustomize", "ref"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}

var customResourceDefinitionGVR = schema.GroupVersionResource{
	Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
}

func applicationStatusSubresourceEnabled(ctx context.Context, dyn dynamic.Interface) (bool, error) {
	crd, err := dyn.Resource(customResourceDefinitionGVR).Get(ctx, "applications.argoproj.io", metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	for _, raw := range versions {
		version, _ := raw.(map[string]any)
		if served, _ := version["served"].(bool); !served {
			continue
		}
		subresources, _ := version["subresources"].(map[string]any)
		if _, enabled := subresources["status"]; enabled {
			return true, nil
		}
	}
	return false, nil
}

func verifySelfManagedServerRolloutComplete(ctx context.Context, k8s kubernetes.Interface) error {
	deployment, err := k8s.AppsV1().Deployments(localAstronomerNamespace).Get(ctx, localAstronomerReleaseName+"-server", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas < 1 {
		return fmt.Errorf("server Deployment has no positive desired replica count")
	}
	desired := *deployment.Spec.Replicas
	if deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.Replicas != desired || deployment.Status.UpdatedReplicas != desired || deployment.Status.ReadyReplicas != desired || deployment.Status.AvailableReplicas != desired || deployment.Status.UnavailableReplicas != 0 {
		return fmt.Errorf("server Deployment is not fully observed/updated/ready/available (%d desired, %d updated, %d ready, %d available)", desired, deployment.Status.UpdatedReplicas, deployment.Status.ReadyReplicas, deployment.Status.AvailableReplicas)
	}
	replicaSets, err := k8s.AppsV1().ReplicaSets(localAstronomerNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	revision := deployment.Annotations["deployment.kubernetes.io/revision"]
	if revision == "" {
		return fmt.Errorf("server Deployment has no rollout revision annotation")
	}
	var currentReplicaSetName string
	var currentReplicaSetUID types.UID
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if !metav1.IsControlledBy(replicaSet, deployment) {
			continue
		}
		if replicaSet.Annotations["deployment.kubernetes.io/revision"] == revision && selfManagedReplicaSetTemplateMatchesDeployment(replicaSet.Spec.Template, deployment.Spec.Template) {
			if currentReplicaSetName != "" {
				return fmt.Errorf("multiple current server ReplicaSets match rollout revision %s", revision)
			}
			currentReplicaSetName = replicaSet.Name
			currentReplicaSetUID = replicaSet.UID
			if replicaSet.Spec.Replicas == nil || *replicaSet.Spec.Replicas != desired {
				return fmt.Errorf("current server ReplicaSet %s does not desire %d replicas", replicaSet.Name, desired)
			}
			continue
		}
		if replicaSet.Spec.Replicas != nil && *replicaSet.Spec.Replicas > 0 {
			return fmt.Errorf("old server ReplicaSet %s still has desired replicas", replicaSet.Name)
		}
	}
	if currentReplicaSetName == "" {
		return fmt.Errorf("current server ReplicaSet for revision %s was not found", revision)
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil || selector.Empty() {
		return fmt.Errorf("server Deployment has no usable Pod selector")
	}
	pods, err := k8s.CoreV1().Pods(localAstronomerNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return err
	}
	currentPods := int32(0)
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		if owner == nil || owner.Kind != "ReplicaSet" || owner.UID != currentReplicaSetUID {
			return fmt.Errorf("old or unowned server Pod %s still exists", pod.Name)
		}
		if pod.DeletionTimestamp != nil {
			return fmt.Errorf("current server Pod %s is terminating", pod.Name)
		}
		currentPods++
	}
	if currentPods != desired {
		return fmt.Errorf("current server ReplicaSet has %d Pods, want exactly %d", currentPods, desired)
	}
	return nil
}

func selfManagedReplicaSetTemplateMatchesDeployment(replicaSetTemplate, deploymentTemplate corev1.PodTemplateSpec) bool {
	normalize := func(template corev1.PodTemplateSpec) corev1.PodTemplateSpec {
		copy := *template.DeepCopy()
		delete(copy.Labels, "pod-template-hash")
		return copy
	}
	return apiequality.Semantic.DeepEqual(normalize(replicaSetTemplate), normalize(deploymentTemplate))
}

func verifyLocalArgoApplicationControllerStopped(ctx context.Context, k8s kubernetes.Interface) error {
	found := false
	if statefulSet, err := k8s.AppsV1().StatefulSets(localArgoNamespace).Get(ctx, localArgoControllerWorkload, metav1.GetOptions{}); err == nil {
		found = true
		if statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas != 0 || statefulSet.Status.Replicas != 0 || statefulSet.Status.ReadyReplicas != 0 || statefulSet.Status.CurrentReplicas != 0 || statefulSet.Status.UpdatedReplicas != 0 || statefulSet.Status.AvailableReplicas != 0 {
			return fmt.Errorf("scale StatefulSet %s to zero and wait for all controller Pods to terminate", localArgoControllerWorkload)
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	if deployment, err := k8s.AppsV1().Deployments(localArgoNamespace).Get(ctx, localArgoControllerWorkload, metav1.GetOptions{}); err == nil {
		found = true
		if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 || deployment.Status.Replicas != 0 || deployment.Status.ReadyReplicas != 0 || deployment.Status.UpdatedReplicas != 0 || deployment.Status.AvailableReplicas != 0 || deployment.Status.UnavailableReplicas != 0 {
			return fmt.Errorf("scale Deployment %s to zero and wait for all controller Pods to terminate", localArgoControllerWorkload)
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	if !found {
		return fmt.Errorf("Argo application controller workload not found")
	}
	return verifyNoArgoControllerPods(ctx, k8s)
}

func verifyNoArgoControllerPods(ctx context.Context, k8s kubernetes.Interface) error {
	pods, err := k8s.CoreV1().Pods(localArgoNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=argocd-application-controller"})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		return fmt.Errorf("controller Pod %s still exists (terminating Pods are not quiesced)", pod.Name)
	}
	return nil
}

func mergeSelfManagedValues(currentValuesYAML, bootstrapValuesYAML string) (string, error) {
	if err := validateSelfManagedHelmValues(bootstrapValuesYAML); err != nil {
		return "", fmt.Errorf("bootstrap self-managed values are unsafe: %w", err)
	}
	currentValues := map[string]any{}
	if err := yaml.Unmarshal([]byte(currentValuesYAML), &currentValues); err != nil {
		return "", fmt.Errorf("parse current self-managed values: %w", err)
	}
	bootstrapValues := map[string]any{}
	if err := yaml.Unmarshal([]byte(bootstrapValuesYAML), &bootstrapValues); err != nil {
		return "", fmt.Errorf("parse bootstrap self-managed values: %w", err)
	}
	// The self-manage Application is platform-owned. Start from the freshly
	// discovered, reference-only bootstrap tree and preserve only explicitly
	// allowlisted non-sensitive operator intent. Arbitrary current sections
	// (env, annotations, headers, unknown add-ons) are never copied because key
	// names cannot prove their values are non-secret.
	for _, component := range []string{"server", "worker", "frontend"} {
		if err := preserveSelfManagedReplicaCount(currentValues, bootstrapValues, component); err != nil {
			return "", err
		}
	}
	data, err := yaml.Marshal(bootstrapValues)
	if err != nil {
		return "", fmt.Errorf("marshal merged self-managed values: %w", err)
	}
	merged := string(data)
	if err := validateSelfManagedHelmValues(merged); err != nil {
		return "", fmt.Errorf("merged self-managed values are unsafe: %w", err)
	}
	return merged, nil
}

var selfManagedSensitiveValueKeys = map[string]struct{}{
	"password": {}, "secret": {}, "secretkey": {}, "encryptionkey": {},
	"clientsecret": {}, "githubclientsecret": {}, "googleclientsecret": {},
	"oidcclientsecret": {}, "postgrespassword": {}, "dsn": {}, "databaseurl": {},
	"redisurl": {}, "bindpw": {}, "token": {}, "accesstoken": {},
	"refreshtoken": {}, "privatekey": {}, "kubeconfig": {}, "apikey": {},
	"credentials": {}, "authorization": {}, "awssecretaccesskey": {},
}

func normalizedSensitiveKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, key)
}

func preserveSelfManagedReplicaCount(currentValues, desiredValues map[string]any, component string) error {
	current, _ := currentValues[component].(map[string]any)
	desired, _ := desiredValues[component].(map[string]any)
	if desired == nil || current == nil {
		return nil
	}
	if replicaCount, ok := current["replicaCount"]; ok {
		count, valid := replicaCount.(float64)
		if !valid || count != float64(int64(count)) || count < 1 || count > 10000 {
			return fmt.Errorf("current %s.replicaCount must be an integer between 1 and 10000", component)
		}
		desired["replicaCount"] = int64(count)
	}
	return nil
}

func validateSelfManagedHelmValues(valuesYAML string) error {
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(valuesYAML), &values); err != nil {
		return fmt.Errorf("parse values: %w", err)
	}
	return validateSelfManagedValueNode(values, "")
}

func validateSelfManagedValueNode(node any, path string) error {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if _, sensitive := selfManagedSensitiveValueKeys[normalizedSensitiveKey(key)]; sensitive {
				return fmt.Errorf("secret material field %s is forbidden; use an existingSecret/SecretRef contract", childPath)
			}
			if err := validateSelfManagedValueNode(child, childPath); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range value {
			if err := validateSelfManagedValueNode(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSelfManagedApplicationSource(obj map[string]any) error {
	if sources, found, _ := unstructured.NestedSlice(obj, "spec", "sources"); found && len(sources) > 0 {
		return fmt.Errorf("self-managed Application multi-source configuration is forbidden")
	}
	source, found, err := unstructured.NestedMap(obj, "spec", "source")
	if err != nil || !found {
		return fmt.Errorf("self-managed Application has no source")
	}
	for key := range source {
		switch key {
		case "repoURL", "chart", "targetRevision", "helm":
		default:
			return fmt.Errorf("self-managed Application source.%s is forbidden", key)
		}
	}
	helm, found, err := unstructured.NestedMap(obj, "spec", "source", "helm")
	if err != nil {
		return fmt.Errorf("read self-managed Helm source: %w", err)
	}
	if !found {
		return fmt.Errorf("self-managed Application has no Helm source")
	}
	for key := range helm {
		switch key {
		case "releaseName", "values":
		default:
			return fmt.Errorf("self-managed Application Helm %s is forbidden", key)
		}
	}
	if parameters, ok := helm["parameters"].([]any); ok && len(parameters) > 0 {
		return fmt.Errorf("self-managed Application Helm parameters are forbidden; use non-sensitive values and Secret references")
	}
	if valueFiles, ok := helm["valueFiles"].([]any); ok && len(valueFiles) > 0 {
		return fmt.Errorf("self-managed Application Helm valueFiles are forbidden; stock Argo cannot source their contents from Kubernetes Secrets")
	}
	values, _ := helm["values"].(string)
	return validateReferenceOnlySelfManagedHelmValues(values)
}

func validateReferenceOnlySelfManagedHelmValues(valuesYAML string) error {
	if err := validateSelfManagedHelmValues(valuesYAML); err != nil {
		return err
	}
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(valuesYAML), &values); err != nil {
		return fmt.Errorf("parse values: %w", err)
	}
	shape, err := chartdeploy.AstronomerDefaultValuesShape()
	if err != nil {
		return fmt.Errorf("load audited chart values vocabulary: %w", err)
	}
	return validateSelfManagedValuesShape(values, shape, "")
}
