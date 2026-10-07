package agent

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type unusedAssignmentProbe struct{}

func (unusedAssignmentProbe) Inspect(context.Context) (protocol.DeliveryControllerInventory, delivery.Capabilities, error) {
	panic("disconnected runtime inspected probe")
}

func TestObservedDeliveryRuntimeOwnsCacheAndTelemetry(t *testing.T) {
	kinds := map[schema.GroupVersionResource]string{
		{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"}:   "GitRepositoryList",
		{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}:   "OCIRepositoryList",
		{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"}:  "HelmRepositoryList",
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}: "KustomizationList",
		{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}:        "HelmReleaseList",
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds)
	store, err := delivery.NewKubernetesCheckpointStore(kubefake.NewClientset(), "agent")
	if err != nil {
		t.Fatal(err)
	}
	labels := observability.MetricValues("GitRepository", "list", "success")
	before := testutil.ToFloat64(agentObservationRequestsTotal.WithLabelValues(labels...))
	runtime, err := NewObservedDeliveryRuntime(delivery.RuntimeConfig{ClusterID: "11111111-1111-4111-8111-111111111111", Connected: func() bool { return false }}, client, store, unusedAssignmentProbe{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx, func(*protocol.Message) error { t.Error("disconnected runtime sent frame"); return nil })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runtime did not join cache")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lists, watches := 0, 0
		for _, action := range client.Actions() {
			switch action.GetVerb() {
			case "list":
				lists++
			case "watch":
				watches++
			default:
				t.Fatalf("unexpected API verb%s", action.GetVerb())
			}
		}
		if lists == 5 && watches == 5 {
			if got := testutil.ToFloat64(agentObservationRequestsTotal.WithLabelValues(labels...)); got != before+1 {
				t.Fatalf("list telemetry delta=%f", got-before)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("shared constructor did not run five scoped sources")
}
