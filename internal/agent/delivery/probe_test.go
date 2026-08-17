package delivery

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery/fake"
	clientfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
)

func readyController(t *testing.T, name string, remoteBases bool) *appsv1.Deployment {
	t.Helper()
	images, err := fluxdistribution.ControllerImages()
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	// source-controller does not implement --no-cross-namespace-refs.
	if name != "source-controller" {
		args = append(args, "--no-cross-namespace-refs=true")
	}
	if remoteBases {
		args = append(args, "--no-remote-bases=true")
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: DeliverySystemNamespace, Generation: 2, Labels: map[string]string{"app.kubernetes.io/version": fluxdistribution.Version()}},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: images[name].Reference + "@" + images[name].Digest, Args: args}}}}},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, AvailableReplicas: 1, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}},
	}
}

func TestClusterProbeAdvertisesOnlyObservedHardenedDistribution(t *testing.T) {
	objects := []runtime.Object{
		readyController(t, "source-controller", false),
		readyController(t, "kustomize-controller", true),
		readyController(t, "helm-controller", false),
	}
	client := clientfake.NewClientset(objects...)
	discovery := &fake.FakeDiscovery{Fake: &ktesting.Fake{}, FakedServerVersion: &version.Info{GitVersion: "v1.35.2"}}
	for _, apiVersion := range expectedFluxAPIs {
		discovery.Resources = append(discovery.Resources, &metav1.APIResourceList{GroupVersion: apiVersion})
	}
	probe, err := NewClusterProbe(client, discovery, true)
	if err != nil {
		t.Fatal(err)
	}
	inventory, capabilities, err := probe.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.Ready || inventory.FluxVersion != fluxdistribution.Version() || inventory.DistributionDigest == "" || len(inventory.Components) != 3 {
		t.Fatalf("unexpected inventory: %#v", inventory)
	}
	if !capabilities.NoCrossNamespaceRefs || !capabilities.NoRemoteKustomizeBases || !capabilities.NamespaceScope || !capabilities.PlatformScope || len(capabilities.RendererKinds) != 2 {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}

	kustomize, err := client.AppsV1().Deployments(DeliverySystemNamespace).Get(context.Background(), "kustomize-controller", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kustomize.Spec.Template.Spec.Containers[0].Args = []string{"--no-cross-namespace-refs=true"}
	if _, err := client.AppsV1().Deployments(DeliverySystemNamespace).Update(context.Background(), kustomize, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	inventory, capabilities, err = probe.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready || capabilities.NoRemoteKustomizeBases || capabilities.NoCrossNamespaceRefs {
		t.Fatalf("unhardened controller was advertised as safe: inventory=%#v capabilities=%#v", inventory, capabilities)
	}
}
