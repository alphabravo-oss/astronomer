package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	chartdeploy "github.com/alphabravocompany/astronomer-go/deploy"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const safeSelfManagedValuesForTest = `
secrets:
  existingSecret: astronomer-secrets
  secretKeyKey: SECRET_KEY
  encryptionKeyKey: ASTRONOMER_ENCRYPTION_KEY
bootstrap:
  existingSecret: astronomer-bootstrap
  existingSecretKey: password
postgres:
  external:
    dsnSecretRef: {name: astronomer-database, key: dsn}
redis:
  external:
    urlSecretRef: {name: astronomer-redis, key: url}
`

func TestSelfManagedApplicationRequiresMatchingApprovalAndThenIsNoOp(t *testing.T) {
	ctx := context.Background()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	kube := k8sfake.NewSimpleClientset()
	cluster := sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}

	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatalf("create staged Application: %v", err)
	}
	resource := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace)
	staged, err := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if policy, _, _ := unstructured.NestedMap(staged.Object, "spec", "syncPolicy"); len(policy) != 0 {
		t.Fatalf("fresh takeover armed sync before approval: %#v", policy)
	}
	annotations := staged.GetAnnotations()
	hash := annotations[selfManagedHashAnnotation]
	if annotations[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting || hash == "" {
		t.Fatalf("staged metadata = %#v", annotations)
	}
	annotations[selfManagedApproveAnnotation] = "stale-" + hash
	staged.SetAnnotations(annotations)
	if _, err := resource.Update(ctx, staged, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	staged, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if policy, _, _ := unstructured.NestedMap(staged.Object, "spec", "syncPolicy"); len(policy) != 0 {
		t.Fatal("stale approval armed sync")
	}
	if _, ok := staged.GetAnnotations()[selfManagedApproveAnnotation]; ok {
		t.Fatal("stale approval was not sanitized")
	}

	annotations = staged.GetAnnotations()
	annotations[selfManagedApproveAnnotation] = hash
	staged.SetAnnotations(annotations)
	staged.Object["status"] = map[string]any{"health": map[string]any{"status": "Healthy"}}
	if _, err := resource.Update(ctx, staged, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	active, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if active.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseActive {
		t.Fatal("matching approval did not activate")
	}
	if _, found, _ := unstructured.NestedMap(active.Object, "spec", "syncPolicy", "automated"); !found {
		t.Fatal("approved Application has no automated policy")
	}
	if _, found, _ := unstructured.NestedMap(active.Object, "status"); !found {
		t.Fatal("safe status was not preserved")
	}
	dirtyAnnotations := active.GetAnnotations()
	dirtyAnnotations["kubectl.kubernetes.io/last-applied-configuration"] = `{"credential":"CANARY"}`
	active.SetAnnotations(dirtyAnnotations)
	if _, err := resource.Update(ctx, active, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	active, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if active.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseActive {
		t.Fatal("metadata sanitation restaged active Application")
	}
	if _, ok := active.GetAnnotations()["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Fatal("secret-bearing metadata survived sanitation")
	}
	if _, found, _ := unstructured.NestedMap(active.Object, "status"); !found {
		t.Fatal("metadata sanitation cleared safe status")
	}

	dyn.ClearActions()
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	for _, action := range dyn.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" || action.GetVerb() == "create" {
			t.Fatalf("fixed-point reconcile mutated Application: %#v", action)
		}
	}
	changedValues := safeSelfManagedValuesForTest + "\nserver:\n  replicaCount: 2\n"
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, changedValues); err != nil {
		t.Fatal(err)
	}
	restaged, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if restaged.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
		t.Fatal("desired change was not restaged")
	}
	if restaged.GetAnnotations()[selfManagedHashAnnotation] == hash {
		t.Fatal("desired change retained stale approval hash")
	}
	if policy, _, _ := unstructured.NestedMap(restaged.Object, "spec", "syncPolicy"); len(policy) != 0 {
		t.Fatal("desired change retained automated sync")
	}
	if _, found, _ := unstructured.NestedMap(restaged.Object, "status"); !found {
		t.Fatal("desired change cleared safe status")
	}
}

func TestSelfManagedValuesShapeRejectsUnknownAndFreeFormCanaries(t *testing.T) {
	shape, err := chartdeploy.AstronomerDefaultValuesShape()
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]map[string]any{
		"unknown top level": {"futureAddon": map[string]any{"apiKey": "CANARY"}},
		"argo env":          {"argo-cd": map[string]any{"server": map[string]any{"env": []any{map[string]any{"name": "AWS_SECRET_ACCESS_KEY", "value": "CANARY"}}}}},
		"argo extra object": {"argo-cd": map[string]any{"extraObjects": []any{map[string]any{"kind": "Secret", "stringData": map[string]any{"token": "CANARY"}}}}},
		"free form args":    {"argo-cd": map[string]any{"server": map[string]any{"extraArgs": []any{"--token=CANARY"}}}},
		"URL userinfo":      {"config": map[string]any{"serverURL": "https://user:CANARY@astronomer.example"}},
		"URL query":         {"observability": map[string]any{"tracing": map[string]any{"endpoint": "https://collector.example/v1?token=CANARY"}}},
		"nil image object":  {"image": map[string]any{"migrate": nil}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSelfManagedValuesShape(values, shape, ""); err == nil {
				t.Fatal("unsafe values passed closed vocabulary")
			}
		})
	}
	allowed := map[string]any{
		"image":         map[string]any{"pullSecrets": []any{map[string]any{"name": "registry-credentials"}}},
		"gateway":       map[string]any{"hosts": []any{"astronomer.example.com"}},
		"networkPolicy": map[string]any{"externalPostgresEgressCIDRs": []any{"10.20.0.0/16"}},
	}
	if err := validateSelfManagedValuesShape(allowed, shape, ""); err != nil {
		t.Fatalf("audited operational arrays rejected: %v", err)
	}
}

func TestProductionAirGapStorageValuesSurviveSafeTakeoverVocabulary(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/chart/values-production.yaml")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	deepMergeSelfManagedValues(values, map[string]any{
		"image":    map[string]any{"registry": "registry.internal.example/platform", "pullSecrets": []any{map[string]any{"name": "private-registry"}}},
		"postgres": map[string]any{"storage": map[string]any{"size": "250Gi", "storageClassName": "encrypted-retain"}},
		"argo-cd":  map[string]any{"controller": map[string]any{"replicas": float64(2), "resources": map[string]any{"requests": map[string]any{"cpu": "500m", "memory": "1Gi"}}}},
	})
	if err := stripKnownInlineSelfManagedCredentials(values); err != nil {
		t.Fatal(err)
	}
	shape, err := chartdeploy.AstronomerDefaultValuesShape()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSelfManagedValuesShape(values, shape, ""); err != nil {
		t.Fatalf("production takeover values rejected: %v", err)
	}
	serialized, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := map[string]any{}
	if err := yaml.Unmarshal(serialized, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, roundTrip) {
		t.Fatal("production/air-gap/storage values changed during reference-only serialization")
	}
}

func TestCurrentHelmReleaseValuesSelectsHighestDeployedRevisionAndSanitizes(t *testing.T) {
	const canary = "HELM-RELEASE-PLAINTEXT-CANARY"
	old := map[string]any{"config": map[string]any{"serverURL": "https://old.example"}}
	production := map[string]any{
		"config":    map[string]any{"env": "production", "serverURL": "https://new.example"},
		"image":     map[string]any{"registry": "registry.internal/platform", "pullSecrets": []any{map[string]any{"name": "registry-auth"}}},
		"postgres":  map[string]any{"storage": map[string]any{"size": "250Gi", "storageClassName": "encrypted-retain"}},
		"secrets":   map[string]any{"secretKey": canary, "encryptionKey": canary},
		"bootstrap": map[string]any{"password": canary},
	}
	failed := map[string]any{"config": map[string]any{"serverURL": "https://failed.example"}}
	kube := k8sfake.NewSimpleClientset(
		helmReleaseSecretFixture(t, 1, "deployed", old),
		helmReleaseSecretFixture(t, 3, "deployed", production),
		helmReleaseSecretFixture(t, 4, "failed", failed),
	)
	values, err := currentHelmReleaseValues(context.Background(), kube)
	if err != nil {
		t.Fatalf("decode Helm release: %v", err)
	}
	if got, _, _ := unstructured.NestedString(values, "config", "serverURL"); got != "https://new.example" {
		t.Fatalf("selected serverURL = %q", got)
	}
	if err := stripKnownInlineSelfManagedCredentials(values); err != nil {
		t.Fatal(err)
	}
	shape, err := chartdeploy.AstronomerDefaultValuesShape()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSelfManagedValuesShape(values, shape, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(values)
	if strings.Contains(string(raw), canary) {
		t.Fatal("Helm release plaintext survived reference-only sanitation")
	}
	if got, _, _ := unstructured.NestedString(values, "postgres", "storage", "storageClassName"); got != "encrypted-retain" {
		t.Fatalf("storage posture = %q", got)
	}
}

func TestSelfManagedCredentialSecretIsProtectedIdempotentAndRotationSafe(t *testing.T) {
	ctx := context.Background()
	kube := k8sfake.NewSimpleClientset()
	data := map[string][]byte{"password": []byte("first"), "dsn": []byte("postgres://reference")}
	if err := ensureSelfManagedCredentialSecret(ctx, kube, selfManagedDatabaseSecret, data); err != nil {
		t.Fatal(err)
	}
	secret, err := kube.CoreV1().Secrets(localAstronomerNamespace).Get(ctx, selfManagedDatabaseSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(secret.OwnerReferences) != 0 || secret.Annotations["argocd.argoproj.io/sync-options"] != "Prune=false,Delete=false" || secret.Annotations["argocd.argoproj.io/compare-options"] != "IgnoreExtraneous" {
		t.Fatalf("credential metadata = %#v", secret.ObjectMeta)
	}
	kube.ClearActions()
	if err := ensureSelfManagedCredentialSecret(ctx, kube, selfManagedDatabaseSecret, data); err != nil {
		t.Fatal(err)
	}
	for _, action := range kube.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" || action.GetVerb() == "create" {
			t.Fatalf("fixed-point credential ensure mutated Secret: %#v", action)
		}
	}
	rotated := map[string][]byte{"password": []byte("second"), "dsn": []byte("postgres://rotated-reference")}
	if err := ensureSelfManagedCredentialSecret(ctx, kube, selfManagedDatabaseSecret, rotated); err != nil {
		t.Fatal(err)
	}
	secret, _ = kube.CoreV1().Secrets(localAstronomerNamespace).Get(ctx, selfManagedDatabaseSecret, metav1.GetOptions{})
	if !reflect.DeepEqual(secret.Data, rotated) || len(secret.OwnerReferences) != 0 {
		t.Fatal("atomic rotation did not preserve the exact two-key contract")
	}

	external := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "external-owned", Namespace: localArgoNamespace}, Data: map[string][]byte{"dsn": []byte("external")}}
	if _, err := kube.CoreV1().Secrets(localArgoNamespace).Create(ctx, external, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedCredentialSecret(ctx, kube, "external-owned", map[string][]byte{"dsn": []byte("overwrite")}); err == nil {
		t.Fatal("self-manager overwrote an external Secret")
	}
	preserved, _ := kube.CoreV1().Secrets(localArgoNamespace).Get(ctx, "external-owned", metav1.GetOptions{})
	if string(preserved.Data["dsn"]) != "external" {
		t.Fatal("external Secret data changed")
	}
}

func helmReleaseSecretFixture(t *testing.T, version int, status string, config map[string]any) *corev1.Secret {
	t.Helper()
	releaseJSON, err := json.Marshal(map[string]any{"config": config})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(releaseJSON); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sh.helm.release.v1.astronomer.v" + fmt.Sprint(version), Namespace: localAstronomerNamespace,
			Labels: map[string]string{"owner": "helm", "name": localAstronomerReleaseName, "status": status, "version": fmt.Sprint(version)},
		},
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(compressed.Bytes()))},
		Type: corev1.SecretType("helm.sh/release.v1"),
	}
}

func TestUnsafeSelfManagedApplicationScrubRequiresStoppedController(t *testing.T) {
	const canary = "ARGO-PLAINTEXT-CANARY"
	unsafe := selfManagedApplicationFixture(t, `bootstrap: {password: "`+canary+`"}`)
	unsafe.SetFinalizers([]string{"resources-finalizer.argocd.argoproj.io", "unapproved.example/finalizer"})
	unsafe.Object["operation"] = map[string]any{"sync": map[string]any{"source": unsafeSourceFixture(canary)}}
	unsafe.Object["status"] = map[string]any{
		"sync":           map[string]any{"comparedTo": map[string]any{"source": unsafeSourceFixture(canary)}},
		"operationState": map[string]any{"syncResult": map[string]any{"source": unsafeSourceFixture(canary)}},
		"history":        []any{map[string]any{"source": unsafeSourceFixture(canary)}},
	}
	crd := applicationCRDWithoutStatusFixture()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unsafe, crd)
	one := int32(1)
	kube := k8sfake.NewSimpleClientset(&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: localArgoControllerWorkload, Namespace: localArgoNamespace}, Spec: appsv1.StatefulSetSpec{Replicas: &one}})
	cluster := sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}
	dyn.ClearActions()
	if err := ensureSelfManagedAstronomerApplication(context.Background(), kube, dyn, cluster, safeSelfManagedValuesForTest); err == nil || !strings.Contains(err.Error(), "scale StatefulSet") {
		t.Fatalf("active controller migration error = %v", err)
	}
	for _, action := range dyn.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" || action.GetVerb() == "create" {
			t.Fatalf("active-controller refusal mutated Application: %#v", action)
		}
	}
	zero := int32(0)
	sts, _ := kube.AppsV1().StatefulSets(localArgoNamespace).Get(context.Background(), localArgoControllerWorkload, metav1.GetOptions{})
	sts.Spec.Replicas = &zero
	if _, err := kube.AppsV1().StatefulSets(localArgoNamespace).Update(context.Background(), sts, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	now := metav1.Now()
	terminating := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "terminating-controller", Namespace: localArgoNamespace, DeletionTimestamp: &now, Labels: map[string]string{"app.kubernetes.io/name": "argocd-application-controller"}}}
	if _, err := kube.CoreV1().Pods(localArgoNamespace).Create(context.Background(), terminating, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(context.Background(), kube, dyn, cluster, safeSelfManagedValuesForTest); err == nil || !strings.Contains(err.Error(), "still exists") {
		t.Fatalf("terminating controller error = %v", err)
	}
	if err := kube.CoreV1().Pods(localArgoNamespace).Delete(context.Background(), terminating.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(context.Background(), kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatalf("scrub: %v", err)
	}
	clean, _ := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace).Get(context.Background(), localArgoApplicationName, metav1.GetOptions{})
	raw, _ := json.Marshal(clean.Object)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("canary survived scrub: %s", raw)
	}
	if _, ok := clean.Object["status"]; ok {
		t.Fatal("status survived scrub")
	}
	if _, ok := clean.Object["operation"]; ok {
		t.Fatal("operation survived scrub")
	}
	if clean.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
		t.Fatal("scrub did not remain staged")
	}
	if got := clean.GetFinalizers(); len(got) != 1 || got[0] != "resources-finalizer.argocd.argoproj.io" {
		t.Fatalf("finalizers = %v", got)
	}
}

func TestUnsafeSelfManagedApplicationRefusesStatusSubresourceCRD(t *testing.T) {
	unsafe := selfManagedApplicationFixture(t, `secrets: {secretKey: "CANARY"}`)
	crd := applicationCRDWithoutStatusFixture()
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	versions[0].(map[string]any)["subresources"] = map[string]any{"status": map[string]any{}}
	_ = unstructured.SetNestedSlice(crd.Object, versions, "spec", "versions")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unsafe, crd)
	zero := int32(0)
	kube := k8sfake.NewSimpleClientset(&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: localArgoControllerWorkload, Namespace: localArgoNamespace}, Spec: appsv1.StatefulSetSpec{Replicas: &zero}})
	err := ensureSelfManagedAstronomerApplication(context.Background(), kube, dyn, sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}, safeSelfManagedValuesForTest)
	if err == nil || !strings.Contains(err.Error(), "enables the status subresource") {
		t.Fatalf("status-subresource error = %v", err)
	}
	current, _ := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace).Get(context.Background(), localArgoApplicationName, metav1.GetOptions{})
	raw, _ := json.Marshal(current.Object)
	if !strings.Contains(string(raw), "CANARY") {
		t.Fatal("refusal unexpectedly mutated unsafe Application")
	}
}

func selfManagedApplicationFixture(t *testing.T, values string) *unstructured.Unstructured {
	t.Helper()
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": localArgoApplicationName, "namespace": localArgoNamespace},
		"spec":     map[string]any{"source": unsafeSourceFixture(values)},
	}}
}

func unsafeSourceFixture(values string) map[string]any {
	return map[string]any{"repoURL": localArgoRepoURL, "chart": "astronomer", "targetRevision": "0.2.0", "helm": map[string]any{"releaseName": localAstronomerReleaseName, "values": values}}
}

func applicationCRDWithoutStatusFixture() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": "applications.argoproj.io"},
		"spec":     map[string]any{"versions": []any{map[string]any{"name": "v1alpha1", "served": true, "subresources": map[string]any{}}}},
	}}
}
