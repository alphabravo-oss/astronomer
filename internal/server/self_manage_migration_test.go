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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

func TestFirstSelfManagedApplicationCreationRequiresCompleteServerRollout(t *testing.T) {
	ctx := context.Background()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	kube := k8sfake.NewSimpleClientset(completeServerRolloutFixtures()...)
	deployment, _ := kube.AppsV1().Deployments(localArgoNamespace).Get(ctx, localAstronomerReleaseName+"-server", metav1.GetOptions{})
	deployment.Status.UpdatedReplicas = 0
	if _, err := kube.AppsV1().Deployments(localArgoNamespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := preflightSelfManagedApplicationCredentialMigration(ctx, kube, dyn); err == nil || !strings.Contains(err.Error(), "first self-managed Application creation") {
		t.Fatalf("first-create preflight error = %v", err)
	}
	dyn.ClearActions()
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}, safeSelfManagedValuesForTest); err == nil || !strings.Contains(err.Error(), "first self-managed Application creation") {
		t.Fatalf("first-create write-boundary error = %v", err)
	}
	for _, action := range dyn.Actions() {
		if action.GetVerb() == "create" || action.GetVerb() == "update" || action.GetVerb() == "patch" {
			t.Fatalf("partial rollout created/mutated Application: %#v", action)
		}
	}
}

func TestRestageRequiresRolloutEvenWhenForgedHashMatchesDesired(t *testing.T) {
	ctx := context.Background()
	const destination = "https://kubernetes.default.svc"
	current := activeSelfManagedApplicationForRevision(t, "0.2.1", destination)
	changedValues := safeSelfManagedValuesForTest + "\nserver:\n  replicaCount: 2\n"
	desiredSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
	_ = unstructured.SetNestedField(desiredSpec, changedValues, "source", "helm", "values")
	forgedDesiredHash, _ := selfManagedSpecHash(desiredSpec)
	annotations := current.GetAnnotations()
	annotations[selfManagedHashAnnotation] = forgedDesiredHash
	current.SetAnnotations(annotations)
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), current)
	kube := k8sfake.NewSimpleClientset(completeServerRolloutFixtures()...)
	deployment, _ := kube.AppsV1().Deployments(localArgoNamespace).Get(ctx, localAstronomerReleaseName+"-server", metav1.GetOptions{})
	deployment.Status.UpdatedReplicas = 0
	if _, err := kube.AppsV1().Deployments(localArgoNamespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, sqlc.Cluster{ApiServerUrl: destination}, changedValues)
	if err == nil || !strings.Contains(err.Error(), "restage requires a complete server rollout") {
		t.Fatalf("equal-hash different-spec restage error = %v", err)
	}
	for _, action := range dyn.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" {
			t.Fatalf("partial rollout restaged forged-hash Application: %#v", action)
		}
	}
}

func TestSelfManagedApplicationRequiresMatchingApprovalAndThenIsNoOp(t *testing.T) {
	ctx := context.Background()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	// A quiesced (zero-replica) Argo controller is part of this fixture so the
	// unsafe-operation sanitation paths below remain legal single-writer writes.
	zeroControllerReplicas := int32(0)
	fixtures := append(completeServerRolloutFixtures(), &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: localArgoControllerWorkload, Namespace: localArgoNamespace}, Spec: appsv1.StatefulSetSpec{Replicas: &zeroControllerReplicas}})
	kube := k8sfake.NewSimpleClientset(fixtures...)
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
	staged.Object["operation"] = map[string]any{"sync": map[string]any{"revision": "0.3.0", "prune": false}, "initiatedBy": map[string]any{"username": "operator@example.com"}}
	if _, err := resource.Update(ctx, staged, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	dyn.ClearActions()
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	for _, action := range dyn.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" {
			t.Fatalf("valid non-pruning manual operation was cancelled: %#v", action)
		}
	}
	staged, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	transientAnnotations := staged.GetAnnotations()
	transientAnnotations["argocd.argoproj.io/refresh"] = "normal"
	staged.SetAnnotations(transientAnnotations)
	if _, err := resource.Update(ctx, staged, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	dyn.ClearActions()
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	for _, action := range dyn.Actions() {
		if action.GetVerb() == "update" || action.GetVerb() == "patch" {
			t.Fatalf("Application was written while Argo owned an active acceptance operation: %#v", action)
		}
	}
	staged, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if _, ok := staged.Object["operation"]; !ok {
		t.Fatal("valid non-pruning manual operation was cancelled while Argo owned the Application")
	}
	if _, ok := staged.GetAnnotations()["argocd.argoproj.io/refresh"]; !ok {
		t.Fatal("transient Argo metadata was cleaned during the active operation window instead of after terminal ownership return")
	}
	staged, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	annotations[selfManagedApproveAnnotation] = "stale-" + hash
	staged.SetAnnotations(annotations)
	staged.Object["operation"] = map[string]any{"sync": map[string]any{"prune": true}}
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
	if _, ok := staged.Object["operation"]; ok {
		t.Fatal("unsafe pre-approval operation survived sanitation")
	}

	annotations = staged.GetAnnotations()
	annotations[selfManagedApproveAnnotation] = hash
	staged.SetAnnotations(annotations)
	if _, err := resource.Update(ctx, staged, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	pending, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if pending.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
		t.Fatal("approval before acceptance sync activated prune")
	}
	pending.Object["operation"] = map[string]any{"sync": map[string]any{"revision": "0.3.0", "prune": false, "syncStrategy": map[string]any{"hook": map[string]any{}}}, "initiatedBy": map[string]any{"username": "operator@example.com"}}
	if _, err := resource.Update(ctx, pending, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
		t.Fatal(err)
	}
	pending, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if pending.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
		t.Fatal("running non-pruning sync activated prune")
	}
	delete(pending.Object, "operation")
	pending.Object["status"] = successfulSelfManagedAcceptanceStatus(t, pending)
	if _, err := resource.Update(ctx, pending, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, completedOperation := range map[string]map[string]any{
		"completed prune": {"sync": map[string]any{"revision": "0.3.0", "prune": true}, "initiatedBy": map[string]any{"username": "operator@example.com"}},
		"completed force": {"sync": map[string]any{"revision": "0.3.0", "prune": false, "syncStrategy": map[string]any{"apply": map[string]any{"force": true}}}, "initiatedBy": map[string]any{"username": "operator@example.com"}},
	} {
		t.Run(name, func(t *testing.T) {
			candidate, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
			_ = unstructured.SetNestedMap(candidate.Object, completedOperation, "status", "operationState", "operation")
			if _, err := resource.Update(ctx, candidate, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
				t.Fatal(err)
			}
			candidate, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
			if candidate.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
				t.Fatal("destructive completed operation activated prune")
			}
			candidate.Object["status"] = successfulSelfManagedAcceptanceStatus(t, candidate)
			if _, err := resource.Update(ctx, candidate, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
		})
	}
	serverDeployment, _ := kube.AppsV1().Deployments(localArgoNamespace).Get(ctx, localAstronomerReleaseName+"-server", metav1.GetOptions{})
	serverDeployment.Status.UpdatedReplicas = 0
	if _, err := kube.AppsV1().Deployments(localArgoNamespace).Update(ctx, serverDeployment, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err == nil || !strings.Contains(err.Error(), "complete server rollout") {
		t.Fatalf("partial rollout approval error = %v", err)
	}
	pending, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if pending.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
		t.Fatal("partial rollout armed automated sync")
	}
	serverDeployment.Status.UpdatedReplicas = 1
	if _, err := kube.AppsV1().Deployments(localArgoNamespace).Update(ctx, serverDeployment, metav1.UpdateOptions{}); err != nil {
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
	active, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	active.Object["operation"] = map[string]any{"sync": map[string]any{"prune": true}}
	if _, err := resource.Update(ctx, active, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
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
	if _, ok := restaged.Object["operation"]; ok {
		t.Fatal("desired change retained a queued operation that bypasses approval")
	}
	restageAnnotations := restaged.GetAnnotations()
	restageAnnotations[selfManagedApproveAnnotation] = restageAnnotations[selfManagedHashAnnotation]
	restaged.SetAnnotations(restageAnnotations)
	if _, err := resource.Update(ctx, restaged, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, changedValues); err != nil {
		t.Fatal(err)
	}
	restaged, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	if restaged.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
		t.Fatal("stale Synced/Healthy status from previous values activated changed desired spec")
	}
}

// stagedSelfManagedApplicationForBarrierTest stages a fresh awaiting-approval
// Application whose live spec/hash exactly match the desired values.
func stagedSelfManagedApplicationForBarrierTest(t *testing.T, ctx context.Context, kube *k8sfake.Clientset) *dynamicfake.FakeDynamicClient {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}, safeSelfManagedValuesForTest); err != nil {
		t.Fatalf("create staged Application: %v", err)
	}
	return dyn
}

func safeSelfManagedAcceptanceOperationForTest() map[string]any {
	return map[string]any{
		"sync":        map[string]any{"revision": "0.3.0", "prune": false},
		"initiatedBy": map[string]any{"username": "operator@example.com"},
	}
}

func TestSelfManagedWriteBarrierDuringActiveAcceptanceOperation(t *testing.T) {
	ctx := context.Background()
	cluster := sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}
	for _, tc := range []struct {
		name    string
		mutate  func(t *testing.T, app *unstructured.Unstructured)
		wantErr string
	}{
		{
			name: "top-level active safe operation",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["operation"] = safeSelfManagedAcceptanceOperationForTest()
			},
		},
		{
			name: "status-only running safe operation",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["status"] = map[string]any{"operationState": map[string]any{"phase": "Running", "operation": safeSelfManagedAcceptanceOperationForTest(), "startedAt": "2026-07-11T00:00:00Z"}}
			},
		},
		{
			name: "status-only terminating safe operation",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["status"] = map[string]any{"operationState": map[string]any{"phase": "Terminating", "operation": safeSelfManagedAcceptanceOperationForTest()}}
			},
		},
		{
			name: "unsafe active top-level prune",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["operation"] = map[string]any{"sync": map[string]any{"revision": "0.3.0", "prune": true}}
			},
			wantErr: "quiesce",
		},
		{
			name: "unsafe status-only running prune",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["status"] = map[string]any{"operationState": map[string]any{"phase": "Running", "operation": map[string]any{"sync": map[string]any{"revision": "0.3.0", "prune": true}}}}
			},
			wantErr: "quiesce",
		},
		{
			name: "unsafe active force apply",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["operation"] = map[string]any{"sync": map[string]any{"revision": "0.3.0", "prune": false, "syncStrategy": map[string]any{"apply": map[string]any{"force": true}}}}
			},
			wantErr: "quiesce",
		},
		{
			name: "unsafe active dry run",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["operation"] = map[string]any{"sync": map[string]any{"revision": "0.3.0", "dryRun": true}}
			},
			wantErr: "quiesce",
		},
		{
			name: "unsafe active revision mismatch",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["operation"] = map[string]any{"sync": map[string]any{"revision": "9.9.9", "prune": false}}
			},
			wantErr: "quiesce",
		},
		{
			name: "running phase without operation evidence",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["status"] = map[string]any{"operationState": map[string]any{"phase": "Running"}}
			},
			wantErr: "quiesce",
		},
		{
			name: "safe operation with drifted staged spec",
			mutate: func(t *testing.T, app *unstructured.Unstructured) {
				app.Object["operation"] = safeSelfManagedAcceptanceOperationForTest()
				_ = unstructured.SetNestedField(app.Object, "tampered-project", "spec", "project")
			},
			wantErr: "quiesce",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No quiesced controller workload exists in this fixture, so every
			// unsafe case must fail closed instead of sanitizing concurrently.
			kube := k8sfake.NewSimpleClientset(completeServerRolloutFixtures()...)
			dyn := stagedSelfManagedApplicationForBarrierTest(t, ctx, kube)
			resource := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace)
			app, err := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			transient := app.GetAnnotations()
			transient["argocd.argoproj.io/refresh"] = "normal"
			app.SetAnnotations(transient)
			tc.mutate(t, app)
			if _, err := resource.Update(ctx, app, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			before, err := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			beforeJSON, err := json.Marshal(before.Object)
			if err != nil {
				t.Fatal(err)
			}
			dyn.ClearActions()
			for pass := 0; pass < 2; pass++ {
				err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest)
				if tc.wantErr == "" && err != nil {
					t.Fatalf("reconcile %d: %v", pass, err)
				}
				if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
					t.Fatalf("reconcile %d error = %v, want substring %q", pass, err, tc.wantErr)
				}
			}
			for _, action := range dyn.Actions() {
				switch action.GetVerb() {
				case "create", "update", "patch", "delete":
					t.Fatalf("Application written during active operation window: %#v", action)
				}
			}
			after, err := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			afterJSON, err := json.Marshal(after.Object)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(beforeJSON, afterJSON) {
				t.Fatalf("Application changed during active operation window:\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
			}
		})
	}
}

func TestSelfManagedWriteBarrierTerminalOwnershipReturn(t *testing.T) {
	ctx := context.Background()
	cluster := sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}

	t.Run("terminal failure stays awaiting and never activates", func(t *testing.T) {
		kube := k8sfake.NewSimpleClientset(completeServerRolloutFixtures()...)
		dyn := stagedSelfManagedApplicationForBarrierTest(t, ctx, kube)
		resource := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace)
		app, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
		for _, phase := range []string{"Failed", "Error"} {
			status := successfulSelfManagedAcceptanceStatus(t, app)
			_ = unstructured.SetNestedField(status, phase, "operationState", "phase")
			app.Object["status"] = status
			annotations := app.GetAnnotations()
			annotations[selfManagedApproveAnnotation] = annotations[selfManagedHashAnnotation]
			app.SetAnnotations(annotations)
			if _, err := resource.Update(ctx, app, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
				t.Fatalf("%s reconcile: %v", phase, err)
			}
			app, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
			if app.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
				t.Fatalf("terminal %s operation activated automation", phase)
			}
			if policy, _, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy"); len(policy) != 0 {
				t.Fatalf("terminal %s operation armed sync policy: %#v", phase, policy)
			}
		}
	})

	t.Run("terminal success normalizes transient metadata to a fixed point", func(t *testing.T) {
		kube := k8sfake.NewSimpleClientset(completeServerRolloutFixtures()...)
		dyn := stagedSelfManagedApplicationForBarrierTest(t, ctx, kube)
		resource := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace)
		app, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
		app.Object["status"] = successfulSelfManagedAcceptanceStatus(t, app)
		transient := app.GetAnnotations()
		transient["argocd.argoproj.io/refresh"] = "normal"
		app.SetAnnotations(transient)
		if _, err := resource.Update(ctx, app, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
			t.Fatal(err)
		}
		app, _ = resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
		if _, ok := app.GetAnnotations()["argocd.argoproj.io/refresh"]; ok {
			t.Fatal("transient metadata survived terminal normalization")
		}
		if app.GetAnnotations()[selfManagedPhaseAnnotation] != selfManagedPhaseAwaiting {
			t.Fatal("terminal normalization changed acceptance phase without approval")
		}
		if _, found, _ := unstructured.NestedMap(app.Object, "status"); !found {
			t.Fatal("terminal normalization cleared status evidence")
		}
		dyn.ClearActions()
		if err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest); err != nil {
			t.Fatal(err)
		}
		for _, action := range dyn.Actions() {
			switch action.GetVerb() {
			case "create", "update", "patch", "delete":
				t.Fatalf("terminal metadata repair is not a fixed point: %#v", action)
			}
		}
	})
}

func TestSelfManagedOperationConflictRequiresFreshReconcileNotBlindRetry(t *testing.T) {
	ctx := context.Background()
	cluster := sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}
	kube := k8sfake.NewSimpleClientset(completeServerRolloutFixtures()...)
	dyn := stagedSelfManagedApplicationForBarrierTest(t, ctx, kube)
	resource := dyn.Resource(argocdApplicationGVR).Namespace(localArgoNamespace)
	app, _ := resource.Get(ctx, localArgoApplicationName, metav1.GetOptions{})
	transient := app.GetAnnotations()
	transient["argocd.argoproj.io/refresh"] = "normal"
	app.SetAnnotations(transient)
	if _, err := resource.Update(ctx, app, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	updates := 0
	dyn.PrependReactor("update", "applications", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, localArgoApplicationName, fmt.Errorf("argo wrote new evidence"))
	})
	err := ensureSelfManagedAstronomerApplication(ctx, kube, dyn, cluster, safeSelfManagedValuesForTest)
	if err == nil || !apierrors.IsConflict(err) {
		t.Fatalf("conflict error = %v, want returned Conflict for a fresh reconcile", err)
	}
	if updates != 1 {
		t.Fatalf("update attempts = %d, want exactly 1 (no blind retry on a stale object)", updates)
	}
}

func successfulSelfManagedAcceptanceStatus(t *testing.T, application *unstructured.Unstructured) map[string]any {
	t.Helper()
	source, found, err := unstructured.NestedMap(application.Object, "spec", "source")
	if err != nil || !found {
		t.Fatalf("read staged source: %v", err)
	}
	destination, found, err := unstructured.NestedMap(application.Object, "spec", "destination")
	if err != nil || !found {
		t.Fatalf("read staged destination: %v", err)
	}
	return map[string]any{
		"sync":           map[string]any{"status": "Synced", "comparedTo": map[string]any{"source": source, "destination": destination}},
		"health":         map[string]any{"status": "Healthy"},
		"operationState": map[string]any{"phase": "Succeeded", "operation": map[string]any{"sync": map[string]any{"revision": "0.3.0", "prune": false, "syncStrategy": map[string]any{"hook": map[string]any{}}}, "initiatedBy": map[string]any{"username": "operator@example.com"}}, "syncResult": map[string]any{"source": source}},
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

func TestCurrentReferenceOnlyValuesAuthorizesOnlyStrongOlderRevisionAdoption(t *testing.T) {
	ctx := context.Background()
	const destination = "https://kubernetes.default.svc"
	active := activeSelfManagedApplicationForRevision(t, "0.2.0", destination)
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), active)
	source, err := currentReferenceOnlySelfManagedValues(ctx, dyn, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !source.AdoptLiveUpgrade || strings.TrimSpace(source.ValuesYAML) == "" {
		t.Fatalf("older strong active source was not authorized: %#v", source)
	}

	for name, mutate := range map[string]func(*unstructured.Unstructured){
		"forged hash": func(app *unstructured.Unstructured) {
			annotations := app.GetAnnotations()
			annotations[selfManagedHashAnnotation] = strings.Repeat("0", 64)
			app.SetAnnotations(annotations)
		},
		"forged destination": func(app *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(app.Object, "https://other-cluster.example", "spec", "destination", "server")
			spec, _, _ := unstructured.NestedMap(app.Object, "spec")
			hash, _ := selfManagedSpecHash(spec)
			annotations := app.GetAnnotations()
			annotations[selfManagedHashAnnotation] = hash
			app.SetAnnotations(annotations)
		},
		"forged repository": func(app *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(app.Object, "https://attacker.invalid/charts", "spec", "source", "repoURL")
			spec, _, _ := unstructured.NestedMap(app.Object, "spec")
			hash, _ := selfManagedSpecHash(spec)
			annotations := app.GetAnnotations()
			annotations[selfManagedHashAnnotation] = hash
			app.SetAnnotations(annotations)
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := active.DeepCopy()
			mutate(candidate)
			client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), candidate)
			if _, err := currentReferenceOnlySelfManagedValues(ctx, client, destination); err == nil || !strings.Contains(err.Error(), "inconsistent") {
				t.Fatalf("forged active identity error = %v", err)
			}
		})
	}
	sameRevision := activeSelfManagedApplicationForRevision(t, "0.3.0", destination)
	source, err = currentReferenceOnlySelfManagedValues(ctx, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), sameRevision), destination)
	if err != nil || source.AdoptLiveUpgrade || strings.TrimSpace(source.ValuesYAML) == "" {
		t.Fatalf("same embedded revision was not canonical: source=%#v err=%v", source, err)
	}
	awaitingOlder := active.DeepCopy()
	awaitingSpec, _, _ := unstructured.NestedMap(awaitingOlder.Object, "spec")
	awaitingHash, err := selfManagedSpecHash(awaitingSpec)
	if err != nil {
		t.Fatal(err)
	}
	stageSelfManagedApplication(awaitingOlder, awaitingHash)
	source, err = currentReferenceOnlySelfManagedValues(ctx, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), awaitingOlder), destination)
	if err != nil || !source.AdoptLiveUpgrade || strings.TrimSpace(source.ValuesYAML) == "" {
		t.Fatalf("clean awaiting older revision was not authorized for safe restage: source=%#v err=%v", source, err)
	}
	for name, mutate := range map[string]func(*unstructured.Unstructured){
		"unexpected approval": func(app *unstructured.Unstructured) {
			annotations := app.GetAnnotations()
			annotations[selfManagedApproveAnnotation] = annotations[selfManagedHashAnnotation]
			app.SetAnnotations(annotations)
		},
		"operation present": func(app *unstructured.Unstructured) {
			app.Object["operation"] = map[string]any{"sync": map[string]any{"prune": false}}
		},
		"automated policy": func(app *unstructured.Unstructured) {
			_ = unstructured.SetNestedMap(app.Object, map[string]any{"automated": map[string]any{"prune": true, "selfHeal": true}}, "spec", "syncPolicy")
		},
	} {
		t.Run("awaiting "+name, func(t *testing.T) {
			candidate := awaitingOlder.DeepCopy()
			mutate(candidate)
			if _, err := currentReferenceOnlySelfManagedValues(ctx, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), candidate), destination); err == nil || !strings.Contains(err.Error(), "awaiting") {
				t.Fatalf("unsafe awaiting identity error = %v", err)
			}
		})
	}
	newer := activeSelfManagedApplicationForRevision(t, "0.3.1", destination)
	if _, err := currentReferenceOnlySelfManagedValues(ctx, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), newer), destination); err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("newer revision error = %v", err)
	}
	nonSemantic := activeSelfManagedApplicationForRevision(t, "latest", destination)
	if _, err := currentReferenceOnlySelfManagedValues(ctx, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), nonSemantic), destination); err == nil || !strings.Contains(err.Error(), "not semantic") {
		t.Fatalf("non-semantic revision error = %v", err)
	}
	missingLabel := active.DeepCopy()
	missingLabel.SetLabels(nil)
	source, err = currentReferenceOnlySelfManagedValues(ctx, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), missingLabel), destination)
	if err != nil || source.ValuesYAML != "" || source.AdoptLiveUpgrade {
		t.Fatalf("unowned Application should fall back to takeover/scrub: source=%#v err=%v", source, err)
	}
}

func activeSelfManagedApplicationForRevision(t *testing.T, revision, destination string) *unstructured.Unstructured {
	t.Helper()
	application := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": localArgoApplicationName, "namespace": localArgoNamespace, "labels": map[string]any{"astronomer.io/platform-owned": "true"}},
		"spec": map[string]any{
			"project": "default", "revisionHistoryLimit": int64(0),
			"source":      map[string]any{"repoURL": localArgoRepoURL, "chart": "astronomer", "targetRevision": revision, "helm": map[string]any{"releaseName": localAstronomerReleaseName, "values": safeSelfManagedValuesForTest}},
			"destination": map[string]any{"server": destination, "namespace": localArgoNamespace},
			"syncPolicy":  map[string]any{"automated": map[string]any{"prune": true, "selfHeal": true}},
		},
	}}
	spec, _, _ := unstructured.NestedMap(application.Object, "spec")
	hash, err := selfManagedSpecHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	application.SetAnnotations(map[string]string{selfManagedPhaseAnnotation: selfManagedPhaseActive, selfManagedHashAnnotation: hash})
	return application
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
	secret.Annotations["argocd.argoproj.io/sync-options"] = "Prune=true, delete = true,Validate=true,PRUNE=TRUE"
	secret.Annotations["argocd.argoproj.io/compare-options"] = "IgnoreExtraneous=false,Other=true"
	if _, err := kube.CoreV1().Secrets(localArgoNamespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfManagedCredentialSecret(ctx, kube, selfManagedDatabaseSecret, data); err != nil {
		t.Fatal(err)
	}
	secret, _ = kube.CoreV1().Secrets(localArgoNamespace).Get(ctx, selfManagedDatabaseSecret, metav1.GetOptions{})
	if got := secret.Annotations["argocd.argoproj.io/sync-options"]; got != "Validate=true,Prune=false,Delete=false" {
		t.Fatalf("ambiguous sync options survived: %q", got)
	}
	if got := secret.Annotations["argocd.argoproj.io/compare-options"]; got != "Other=true,IgnoreExtraneous" {
		t.Fatalf("ambiguous compare options survived: %q", got)
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

func TestSelfManagedRedisURLDecompositionPreservesUnsupportedURLs(t *testing.T) {
	for name, rawURL := range map[string]string{
		"query":          "redis://redis.example:6379/2?client_name=CANARY",
		"fragment":       "rediss://redis.example:6380/3#CANARY",
		"unknown scheme": "redis+sentinel://redis.example:26379/0",
	} {
		t.Run(name, func(t *testing.T) {
			server := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server", Namespace: localArgoNamespace}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "server"}}}}}}
			configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-config", Namespace: localArgoNamespace}, Data: map[string]string{"REDIS_URL": rawURL}}
			kube := k8sfake.NewSimpleClientset(configMap)
			values, err := selfManagedExternalRedisValues(context.Background(), kube, server)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := values["urlSecretRef"]; !ok {
				t.Fatalf("unsupported URL was lossy-decomposed: %#v", values)
			}
			secret, err := kube.CoreV1().Secrets(localArgoNamespace).Get(context.Background(), selfManagedRedisSecret, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := string(secret.Data["url"]); got != rawURL {
				t.Fatalf("stored URL = %q, want exact %q", got, rawURL)
			}
		})
	}
	server := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "server"}}}}}}
	kube := k8sfake.NewSimpleClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-config", Namespace: localArgoNamespace}, Data: map[string]string{"REDIS_URL": "rediss://redis.example:6380/4"}})
	values, err := selfManagedExternalRedisValues(context.Background(), kube, server)
	if err != nil {
		t.Fatal(err)
	}
	if values["address"] != "redis.example:6380" || values["database"] != 4 || values["tls"] != true {
		t.Fatalf("supported Redis URL = %#v", values)
	}
	kube = k8sfake.NewSimpleClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-config", Namespace: localArgoNamespace}, Data: map[string]string{"REDIS_URL": "redis://:$(REDIS_PASSWORD)@redis.example:6379/0"}})
	if _, err := selfManagedExternalRedisValues(context.Background(), kube, server); err == nil || !strings.Contains(err.Error(), "no matching Secret reference") {
		t.Fatalf("unresolved Redis placeholder error = %v", err)
	}
	if _, err := kube.CoreV1().Secrets(localArgoNamespace).Get(context.Background(), selfManagedRedisSecret, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("unresolved Redis placeholder was persisted as a usable URL")
	}
	passwordServer := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "server", Env: []corev1.EnvVar{{Name: "REDIS_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "redis-password"}, Key: "password"}}}}}}}}}}
	for name, aclURL := range map[string]string{
		"named ACL user":          "redis://alice:$(REDIS_PASSWORD)@redis.example:6379/0",
		"placeholder as username": "redis://$(REDIS_PASSWORD)@redis.example:6379/0",
	} {
		t.Run(name, func(t *testing.T) {
			client := k8sfake.NewSimpleClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-config", Namespace: localArgoNamespace}, Data: map[string]string{"REDIS_URL": aclURL}})
			if _, err := selfManagedExternalRedisValues(context.Background(), client, passwordServer); err == nil || !strings.Contains(err.Error(), "empty ACL username") {
				t.Fatalf("ACL placeholder error = %v", err)
			}
			if _, err := client.CoreV1().Secrets(localArgoNamespace).Get(context.Background(), selfManagedRedisSecret, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatal("unsupported ACL placeholder was persisted")
			}
		})
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
			UID: types.UID("helm-release-" + fmt.Sprint(version)), ResourceVersion: fmt.Sprint(version),
			Labels: map[string]string{"owner": "helm", "name": localAstronomerReleaseName, "status": status, "version": fmt.Sprint(version)},
		},
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(compressed.Bytes()))},
		Type: corev1.SecretType("helm.sh/release.v1"),
	}
}

func TestUnsafeSelfManagedApplicationScrubRequiresStoppedController(t *testing.T) {
	const canary = "ARGO-PLAINTEXT-CANARY"
	unsafe := selfManagedApplicationFixture(t, "server:\n  env:\n    AWS_SECRET_ACCESS_KEY: "+canary+"\n")
	unsafe.SetFinalizers([]string{"resources-finalizer.argocd.argoproj.io", "unapproved.example/finalizer"})
	hybridSource := unsafeSourceFixture(safeSelfManagedValuesForTest)
	hybridSource["plugin"] = map[string]any{"env": []any{map[string]any{"name": "AWS_SECRET_ACCESS_KEY", "value": canary}}}
	unsafe.Object["operation"] = map[string]any{"sync": map[string]any{"source": hybridSource, "manifests": []any{"apiVersion: v1\nkind: Secret\nstringData:\n  token: " + canary}}, "info": []any{map[string]any{"name": "credential", "value": canary}}}
	unsafe.Object["status"] = map[string]any{
		"sync":           map[string]any{"comparedTo": map[string]any{"source": hybridSource}},
		"operationState": map[string]any{"syncResult": map[string]any{"source": hybridSource}, "operation": map[string]any{"sync": map[string]any{"manifests": []any{canary}}}},
		"history":        []any{map[string]any{"source": hybridSource}},
	}
	crd := applicationCRDWithoutStatusFixture()
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unsafe, crd)
	if got, err := currentReferenceOnlySelfManagedValues(context.Background(), dyn, "https://kubernetes.default.svc"); err != nil || got.ValuesYAML != "" {
		t.Fatal("free-form env canary was misclassified as a canonical reference-only source")
	}
	one := int32(1)
	objects := []runtime.Object{&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: localArgoControllerWorkload, Namespace: localArgoNamespace}, Spec: appsv1.StatefulSetSpec{Replicas: &one}}}
	objects = append(objects, completeServerRolloutFixtures()...)
	kube := k8sfake.NewSimpleClientset(objects...)
	cluster := sqlc.Cluster{ApiServerUrl: "https://kubernetes.default.svc"}
	dyn.ClearActions()
	kube.ClearActions()
	if err := preflightSelfManagedApplicationCredentialMigration(context.Background(), kube, dyn); err == nil || !strings.Contains(err.Error(), "before any credential mutation") {
		t.Fatalf("outer migration preflight error = %v", err)
	}
	for _, actions := range [][]testingAction{dynamicTestingActions(dyn.Actions()), kubeTestingActions(kube.Actions())} {
		for _, action := range actions {
			if action.verb == "update" || action.verb == "patch" || action.verb == "create" || action.verb == "delete" {
				t.Fatalf("controller-on preflight mutated %s via %s", action.resource, action.verb)
			}
		}
	}
	serverRollout, _ := kube.AppsV1().Deployments(localArgoNamespace).Get(context.Background(), localAstronomerReleaseName+"-server", metav1.GetOptions{})
	serverRollout.Status.UpdatedReplicas = 0
	if _, err := kube.AppsV1().Deployments(localArgoNamespace).Update(context.Background(), serverRollout, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	dyn.ClearActions()
	kube.ClearActions()
	if err := preflightSelfManagedApplicationCredentialMigration(context.Background(), kube, dyn); err == nil || !strings.Contains(err.Error(), "complete server rollout") {
		t.Fatalf("partial rollout preflight error = %v", err)
	}
	for _, actions := range [][]testingAction{dynamicTestingActions(dyn.Actions()), kubeTestingActions(kube.Actions())} {
		for _, action := range actions {
			if action.verb == "update" || action.verb == "patch" || action.verb == "create" || action.verb == "delete" {
				t.Fatalf("partial-rollout refusal mutated %s via %s", action.resource, action.verb)
			}
		}
	}
	serverRollout.Status.UpdatedReplicas = 1
	if _, err := kube.AppsV1().Deployments(localArgoNamespace).Update(context.Background(), serverRollout, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	controller := true
	zeroRS := int32(0)
	oldLabels := map[string]string{"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/instance": "astronomer", "app.kubernetes.io/component": "server", "pod-template-hash": "oldhash"}
	oldRS := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server-oldhash", Namespace: localArgoNamespace, UID: "old-rs-uid", Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: serverRollout.Name, UID: serverRollout.UID, Controller: &controller}}}, Spec: appsv1.ReplicaSetSpec{Replicas: &zeroRS, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: oldLabels}}}}
	if _, err := kube.AppsV1().ReplicaSets(localArgoNamespace).Create(context.Background(), oldRS, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	terminatingOld := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server-oldhash-terminating", Namespace: localArgoNamespace, Labels: oldLabels, DeletionTimestamp: &metav1.Time{Time: time.Now()}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: oldRS.Name, UID: oldRS.UID, Controller: &controller}}}}
	if _, err := kube.CoreV1().Pods(localArgoNamespace).Create(context.Background(), terminatingOld, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := preflightSelfManagedApplicationCredentialMigration(context.Background(), kube, dyn); err == nil || !strings.Contains(err.Error(), "old or unowned server Pod") {
		t.Fatalf("terminating old Pod preflight error = %v", err)
	}
	if err := kube.CoreV1().Pods(localArgoNamespace).Delete(context.Background(), terminatingOld.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := kube.AppsV1().ReplicaSets(localArgoNamespace).Delete(context.Background(), oldRS.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
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

type testingAction struct{ verb, resource string }

func dynamicTestingActions(actions []k8stesting.Action) []testingAction {
	result := make([]testingAction, 0, len(actions))
	for _, action := range actions {
		result = append(result, testingAction{verb: action.GetVerb(), resource: action.GetResource().Resource})
	}
	return result
}

func kubeTestingActions(actions []k8stesting.Action) []testingAction {
	return dynamicTestingActions(actions)
}

func TestUnsafeSelfManagedApplicationRefusesStatusSubresourceCRD(t *testing.T) {
	unsafe := selfManagedApplicationFixture(t, `secrets: {secretKey: "CANARY"}`)
	crd := applicationCRDWithoutStatusFixture()
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	versions[0].(map[string]any)["subresources"] = map[string]any{"status": map[string]any{}}
	_ = unstructured.SetNestedSlice(crd.Object, versions, "spec", "versions")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unsafe, crd)
	zero := int32(0)
	objects := []runtime.Object{&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: localArgoControllerWorkload, Namespace: localArgoNamespace}, Spec: appsv1.StatefulSetSpec{Replicas: &zero}}}
	objects = append(objects, completeServerRolloutFixtures()...)
	kube := k8sfake.NewSimpleClientset(objects...)
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

func completeServerRolloutFixtures() []runtime.Object {
	one := int32(1)
	controller := true
	serverLabels := map[string]string{"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/instance": "astronomer", "app.kubernetes.io/component": "server"}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: localAstronomerReleaseName + "-server", Namespace: localArgoNamespace, UID: "server-deployment-uid", Generation: 2, Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"}},
		Spec:       appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: serverLabels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: serverLabels}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "server", Image: "server:v2"}}}}},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
	}
	replicaSetTemplate := *deployment.Spec.Template.DeepCopy()
	replicaSetTemplate.Labels["pod-template-hash"] = "newhash"
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server-newhash", Namespace: localArgoNamespace, UID: "current-rs-uid", Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: deployment.Name, UID: deployment.UID, Controller: &controller}}},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &one, Template: replicaSetTemplate},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server-newhash-abc", Namespace: localArgoNamespace, Labels: replicaSetTemplate.Labels, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: replicaSet.Name, UID: replicaSet.UID, Controller: &controller}}}}
	return []runtime.Object{deployment, replicaSet, pod}
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
