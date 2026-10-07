package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestSharedDeliveryRealInformerReadsAndControllerChanges(t *testing.T) {
	images, err := fluxdistribution.ControllerImages()
	if err != nil {
		t.Fatal(err)
	}
	objects := []runtime.Object{}
	for _, name := range []string{"source-controller", "kustomize-controller", "helm-controller"} {
		image := images[name]
		args := []string{"--no-cross-namespace-refs=true", "--no-remote-bases=true"}
		objects = append(objects, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: agentdelivery.DeliverySystemNamespace, Generation: 1, ResourceVersion: "1", Labels: map[string]string{"app.kubernetes.io/version": fluxdistribution.Version()}}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: image.Reference + "@" + image.Digest, Args: args}}}}}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: 1, ReadyReplicas: 1, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}}})
	}
	client := fake.NewClientset(objects...)
	subscriber, _, _ := startObservationSubscriber(t, client)
	for _, kind := range observation.Kinds() {
		awaitObservation(t, subscriber, kind, protocol.ObservationCurrent)
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"source.toolkit.fluxcd.io/v1","resources":[]}`
		if request.URL.Path == "/version" {
			body = `{"gitVersion":"v1.35.2"}`
		}
		if request.URL.Path == "/apis" {
			body = `{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	discoveryClient, err := discovery.NewDiscoveryClientForConfigAndClient(&rest.Config{Host: "https://informer-test.invalid"}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := agentdelivery.NewClusterProbe(client, discoveryClient, true)
	if err != nil {
		t.Fatal(err)
	}
	probe.WithObservationSource(subscriber)
	before := trackedActionCount(client.Actions())
	for range 10 {
		inventory, _, err := probe.InspectObserved(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !inventory.Ready {
			t.Fatal("projected controllers not ready")
		}
		if err := inventory.Validate(); err != nil {
			t.Fatal("modern inventory protocol invalid", err)
		}
	}
	if got := trackedActionCount(client.Actions()) - before; got != 0 {
		t.Fatalf("10 real-informer probes added%d typed calls", got)
	}
	t.Log("10 delivery inspections against live fake informer stores: 0 recurring typed LIST/GET/WATCH")
	updated := objects[1].(*appsv1.Deployment).DeepCopy()
	updated.ResourceVersion = "2"
	updated.Status.AvailableReplicas = 0
	if _, err := client.AppsV1().Deployments(updated.Namespace).Update(context.Background(), updated, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(context.Background(), 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		inventory, _, err := probe.InspectObserved(context.Background())
		return err == nil && !inventory.Ready, nil
	})
	if err != nil {
		t.Fatal("controller change not observed", err)
	}
	if err := client.AppsV1().Deployments(updated.Namespace).Delete(context.Background(), updated.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(context.Background(), 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		snapshot := subscriber.ObservationSnapshot(observation.Deployments)
		return snapshot.Observation.State == protocol.ObservationCurrent && len(snapshot.Deployments) == 2, nil
	})
	if err != nil {
		t.Fatal("deleted controller retained", err)
	}
}
