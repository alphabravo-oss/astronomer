package crd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCRDReconcilersEnvtest_StatusFinalizersAndGeneratedApplicationSets(t *testing.T) {
	requireEnvtestAssets(t)

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go AddToScheme: %v", err)
	}
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("management AddToScheme: %v", err)
	}

	testEnv := &envtest.Environment{
		Scheme: scheme,
		CRDInstallOptions: envtest.CRDInstallOptions{
			CRDs:         minimalEnvtestCRDs(),
			MaxTime:      30 * time.Second,
			PollInterval: 100 * time.Millisecond,
		},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Fatalf("stop envtest: %v", err)
		}
	})

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx := context.Background()
	for _, namespace := range []string{"astronomer-mgmt", defaultArgoNamespace} {
		if err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
			t.Fatalf("create namespace %s: %v", namespace, err)
		}
	}

	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-us-east", Namespace: "astronomer-mgmt"},
		Spec: ClusterSpec{
			Name:        "prod-us-east",
			DisplayName: "Prod US East",
		},
	}
	if err := k8sClient.Create(ctx, cluster); err != nil {
		t.Fatalf("create Cluster: %v", err)
	}
	clusterReconciler := &ClusterReconciler{Client: k8sClient, Sync: &fakeClusterSync{}, Log: slog.Default()}
	if _, err := clusterReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("reconcile Cluster: %v", err)
	}
	var clusterAfter Cluster
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "prod-us-east", Namespace: "astronomer-mgmt"}, &clusterAfter); err != nil {
		t.Fatalf("get Cluster: %v", err)
	}
	if !hasFinalizer(clusterAfter.Finalizers, FinalizerCluster) || clusterAfter.Status.Phase != "registered" {
		t.Fatalf("Cluster finalizer/status not patched: finalizers=%+v status=%+v", clusterAfter.Finalizers, clusterAfter.Status)
	}

	project := &Project{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: "astronomer-mgmt"},
		Spec: ProjectSpec{
			Name:     "platform",
			Clusters: []string{"prod-us-east"},
		},
	}
	if err := k8sClient.Create(ctx, project); err != nil {
		t.Fatalf("create Project: %v", err)
	}
	projectReconciler := &ProjectReconciler{Client: k8sClient, Sync: &fakeProjectSync{}, Log: slog.Default()}
	if _, err := projectReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("reconcile Project: %v", err)
	}
	var projectAfter Project
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "platform", Namespace: "astronomer-mgmt"}, &projectAfter); err != nil {
		t.Fatalf("get Project: %v", err)
	}
	if !hasFinalizer(projectAfter.Finalizers, FinalizerProject) || projectAfter.Status.Phase != "active" {
		t.Fatalf("Project finalizer/status not patched: finalizers=%+v status=%+v", projectAfter.Finalizers, projectAfter.Status)
	}

	bundle := &ComponentBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "ingress", Namespace: "astronomer-mgmt"},
		Spec: ComponentBundleSpec{
			Version:          "1.0.0",
			DefaultNamespace: "ingress-legacy",
			Source: ComponentBundleSourceSpec{
				Type:           "helm",
				RepoURL:        "https://kubernetes.github.io/ingress-nginx",
				Chart:          "ingress-nginx-legacy",
				TargetRevision: "4.12.0",
			},
			Versions: []ComponentBundleVersionSpec{{
				Version:          "2.0.0",
				DefaultNamespace: "ingress-v2",
				Source: ComponentBundleSourceSpec{
					Type:           "helm",
					RepoURL:        "https://kubernetes.github.io/ingress-nginx",
					Chart:          "ingress-nginx",
					TargetRevision: "4.13.0",
				},
			}},
		},
	}
	if err := k8sClient.Create(ctx, bundle); err != nil {
		t.Fatalf("create ComponentBundle: %v", err)
	}

	bundleReconciler := &ComponentBundleReconciler{Client: k8sClient, Log: slog.Default()}
	if _, err := bundleReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "ingress", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("reconcile ComponentBundle: %v", err)
	}
	var bundleAfter ComponentBundle
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "ingress", Namespace: "astronomer-mgmt"}, &bundleAfter); err != nil {
		t.Fatalf("get ComponentBundle: %v", err)
	}
	if !hasFinalizer(bundleAfter.Finalizers, FinalizerComponentBundle) {
		t.Fatalf("ComponentBundle finalizer not installed: %+v", bundleAfter.Finalizers)
	}
	if bundleAfter.Status.Phase != "Valid" {
		t.Fatalf("ComponentBundle status not patched through status subresource: %+v", bundleAfter.Status)
	}
	if got := bundleAfter.Status.AvailableVersions; len(got) != 2 || got[0] != "1.0.0" || got[1] != "2.0.0" {
		t.Fatalf("ComponentBundle availableVersions = %+v", got)
	}

	profile := &AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "viewer", Namespace: "astronomer-mgmt"},
		Spec: AgentProfileSpec{
			PrivilegeProfile: "viewer",
			NetworkEgress:    AgentProfileNetworkEgressSpec{Mode: "default"},
		},
	}
	if err := k8sClient.Create(ctx, profile); err != nil {
		t.Fatalf("create AgentProfile: %v", err)
	}
	profileReconciler := &AgentProfileReconciler{Client: k8sClient, Log: slog.Default()}
	if _, err := profileReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "viewer", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("reconcile AgentProfile: %v", err)
	}
	var profileAfter AgentProfile
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "viewer", Namespace: "astronomer-mgmt"}, &profileAfter); err != nil {
		t.Fatalf("get AgentProfile: %v", err)
	}
	if !hasFinalizer(profileAfter.Finalizers, FinalizerAgentProfile) || profileAfter.Status.Phase != "Ready" {
		t.Fatalf("AgentProfile finalizer/status not patched: finalizers=%+v status=%+v", profileAfter.Finalizers, profileAfter.Status)
	}

	baseline := &ClusterBaseline{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-baseline", Namespace: "astronomer-mgmt"},
		Spec: ClusterBaselineSpec{
			ClusterSelector: LabelSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			Bundles:         []ClusterBaselineBundleRef{{Name: "ingress", Version: "2.0.0"}},
		},
	}
	if err := k8sClient.Create(ctx, baseline); err != nil {
		t.Fatalf("create ClusterBaseline: %v", err)
	}
	baselineReconciler := &ClusterBaselineReconciler{Client: k8sClient, Log: slog.Default()}
	if _, err := baselineReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("reconcile ClusterBaseline: %v", err)
	}
	var baselineAfter ClusterBaseline
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "prod-baseline", Namespace: "astronomer-mgmt"}, &baselineAfter); err != nil {
		t.Fatalf("get ClusterBaseline: %v", err)
	}
	if !hasFinalizer(baselineAfter.Finalizers, FinalizerClusterBaseline) {
		t.Fatalf("ClusterBaseline finalizer not installed: %+v", baselineAfter.Finalizers)
	}
	if baselineAfter.Status.Phase != "Ready" || len(baselineAfter.Status.Applications) != 1 {
		t.Fatalf("ClusterBaseline status not patched: %+v", baselineAfter.Status)
	}

	var appSet unstructured.Unstructured
	appSet.SetGroupVersionKind(applicationSetGVK)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: baselineAfter.Status.Applications[0].Name, Namespace: defaultArgoNamespace}, &appSet); err != nil {
		t.Fatalf("get generated ApplicationSet: %v", err)
	}
	revision, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "targetRevision")
	if revision != "4.13.0" {
		t.Fatalf("generated targetRevision = %q", revision)
	}
	namespace, _, _ := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "destination", "namespace")
	if namespace != "ingress-v2" {
		t.Fatalf("generated namespace = %q", namespace)
	}

	target := &GitOpsTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-ingress", Namespace: "astronomer-mgmt"},
		Spec: GitOpsTargetSpec{
			Selector:  GitOpsTargetSelectorSpec{ClusterRefs: []string{"prod-us-east"}},
			BundleRef: GitOpsTargetBundleRef{Name: "ingress", Version: "2.0.0"},
		},
	}
	if err := k8sClient.Create(ctx, target); err != nil {
		t.Fatalf("create GitOpsTarget: %v", err)
	}
	targetReconciler := &GitOpsTargetReconciler{Client: k8sClient, Log: slog.Default()}
	if _, err := targetReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "prod-ingress", Namespace: "astronomer-mgmt"}}); err != nil {
		t.Fatalf("reconcile GitOpsTarget: %v", err)
	}
	var targetAfter GitOpsTarget
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "prod-ingress", Namespace: "astronomer-mgmt"}, &targetAfter); err != nil {
		t.Fatalf("get GitOpsTarget: %v", err)
	}
	if !hasFinalizer(targetAfter.Finalizers, FinalizerGitOpsTarget) || targetAfter.Status.Phase != "Ready" {
		t.Fatalf("GitOpsTarget finalizer/status not patched: finalizers=%+v status=%+v", targetAfter.Finalizers, targetAfter.Status)
	}
	var targetAppSet unstructured.Unstructured
	targetAppSet.SetGroupVersionKind(applicationSetGVK)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: targetAfter.Status.ApplicationSetName, Namespace: defaultArgoNamespace}, &targetAppSet); err != nil {
		t.Fatalf("get generated GitOpsTarget ApplicationSet: %v", err)
	}
	targetRevision, _, _ := unstructured.NestedString(targetAppSet.Object, "spec", "template", "spec", "source", "targetRevision")
	if targetRevision != "4.13.0" {
		t.Fatalf("generated GitOpsTarget targetRevision = %q", targetRevision)
	}
}

func requireEnvtestAssets(t *testing.T) {
	t.Helper()
	if os.Getenv("USE_EXISTING_CLUSTER") == "true" ||
		os.Getenv("KUBEBUILDER_ASSETS") != "" ||
		(os.Getenv("TEST_ASSET_KUBE_APISERVER") != "" && os.Getenv("TEST_ASSET_ETCD") != "") {
		return
	}
	if _, err := os.Stat(filepath.Join(string(os.PathSeparator), "usr", "local", "kubebuilder", "bin", "kube-apiserver")); err == nil {
		return
	}
	t.Skip("envtest assets are not configured; set KUBEBUILDER_ASSETS or TEST_ASSET_KUBE_APISERVER/TEST_ASSET_ETCD")
}

func minimalEnvtestCRDs() []*apiextensionsv1.CustomResourceDefinition {
	return []*apiextensionsv1.CustomResourceDefinition{
		minimalEnvtestCRD(GroupVersion.Group, "Cluster", "cluster", "clusters"),
		minimalEnvtestCRD(GroupVersion.Group, "Project", "project", "projects"),
		minimalEnvtestCRD(GroupVersion.Group, "ClusterBaseline", "clusterbaseline", "clusterbaselines"),
		minimalEnvtestCRD(GroupVersion.Group, "ComponentBundle", "componentbundle", "componentbundles"),
		minimalEnvtestCRD(GroupVersion.Group, "AgentProfile", "agentprofile", "agentprofiles"),
		minimalEnvtestCRD(GroupVersion.Group, "GitOpsTarget", "gitopstarget", "gitopstargets"),
		minimalEnvtestCRD(applicationSetGVK.Group, "ApplicationSet", "applicationset", "applicationsets"),
		minimalEnvtestCRD(applicationGVK.Group, "Application", "application", "applications"),
	}
}

func minimalEnvtestCRD(group, kind, singular, plural string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: singular,
				Kind:     kind,
				ListKind: kind + "List",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1alpha1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"spec": {
								Type:                   "object",
								XPreserveUnknownFields: boolPtr(true),
							},
							"status": {
								Type:                   "object",
								XPreserveUnknownFields: boolPtr(true),
							},
						},
					},
				},
				Subresources: &apiextensionsv1.CustomResourceSubresources{
					Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
				},
			}},
		},
	}
}

func boolPtr(v bool) *bool {
	return &v
}
