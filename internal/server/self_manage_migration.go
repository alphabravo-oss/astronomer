package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	chartdeploy "github.com/alphabravocompany/astronomer-go/deploy"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func ensureSelfManagedAstronomerApplication(ctx context.Context, k8s kubernetes.Interface, dyn dynamic.Interface, cluster sqlc.Cluster, valuesYAML string) error {
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
		stageSelfManagedApplication(obj, desiredHash)
		_, err = res.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	obj.SetFinalizers(approvedSelfManagedApplicationFinalizers(current.GetFinalizers()))
	if !selfManagedApplicationHasUnsafeSourceCopies(current.Object) {
		currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
		annotations := current.GetAnnotations()
		if annotations[selfManagedPhaseAnnotation] == selfManagedPhaseAwaiting && annotations[selfManagedHashAnnotation] == desiredHash {
			if approved := annotations[selfManagedApproveAnnotation]; approved == desiredHash {
				updated := current.DeepCopy()
				if err := unstructured.SetNestedMap(updated.Object, desiredSpec, "spec"); err != nil {
					return err
				}
				setSelfManagedApplicationMetadata(updated, selfManagedPhaseActive, desiredHash)
				_, err = res.Update(ctx, updated, metav1.UpdateOptions{})
				return err
			}
			stagedSpec := stagedSelfManagedSpec(desiredSpec)
			if reflect.DeepEqual(currentSpec, stagedSpec) && selfManagedApplicationMetadataClean(current, selfManagedPhaseAwaiting, desiredHash) {
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
		if err := unstructured.SetNestedMap(updated.Object, stagedSelfManagedSpec(desiredSpec), "spec"); err != nil {
			return err
		}
		setSelfManagedApplicationMetadata(updated, selfManagedPhaseAwaiting, desiredHash)
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
	if err := verifyLocalArgoApplicationControllerStopped(ctx, k8s); err != nil {
		return fmt.Errorf("unsafe legacy Application requires an operator-gated migration: %w", err)
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
			if sources, ok := typed["sources"].([]any); ok && len(sources) > 0 {
				return true
			}
			if looksLikeArgoApplicationSource(typed) {
				if _, ok := typed["helm"].(map[string]any); !ok {
					return true
				}
			}
			if helm, ok := typed["helm"].(map[string]any); ok {
				if len(helm) > 0 {
					wrapper := map[string]any{"spec": map[string]any{"source": map[string]any{"helm": helm}}}
					if validateSelfManagedApplicationSource(wrapper) != nil {
						return true
					}
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

func verifyLocalArgoApplicationControllerStopped(ctx context.Context, k8s kubernetes.Interface) error {
	if statefulSet, err := k8s.AppsV1().StatefulSets(localArgoNamespace).Get(ctx, localArgoControllerWorkload, metav1.GetOptions{}); err == nil {
		if statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas != 0 || statefulSet.Status.Replicas != 0 || statefulSet.Status.ReadyReplicas != 0 {
			return fmt.Errorf("scale StatefulSet %s to zero and wait for all controller Pods to terminate", localArgoControllerWorkload)
		}
		return verifyNoArgoControllerPods(ctx, k8s)
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	if deployment, err := k8s.AppsV1().Deployments(localArgoNamespace).Get(ctx, localArgoControllerWorkload, metav1.GetOptions{}); err == nil {
		if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 || deployment.Status.Replicas != 0 || deployment.Status.ReadyReplicas != 0 {
			return fmt.Errorf("scale Deployment %s to zero and wait for all controller Pods to terminate", localArgoControllerWorkload)
		}
		return verifyNoArgoControllerPods(ctx, k8s)
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	return fmt.Errorf("Argo application controller workload not found")
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
	return validateSelfManagedHelmValues(values)
}
