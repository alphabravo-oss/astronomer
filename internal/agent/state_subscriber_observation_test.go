package agent

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func startObservationSubscriber(t *testing.T, client *fake.Clientset) (*StateSubscriber, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	subscriber := NewStateSubscriber(client, &recordingSender{}, slog.New(slog.DiscardHandler))
	subscriber.syncTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { subscriber.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("informer shutdown did not join")
		}
	})
	return subscriber, cancel, done
}
func awaitObservation(t *testing.T, s *StateSubscriber, kind observation.Kind, state protocol.ObservationState) observation.Snapshot {
	t.Helper()
	var result observation.Snapshot
	err := wait.PollUntilContextTimeout(context.Background(), 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		result = s.ObservationSnapshot(kind)
		return result.Observation.State == state, nil
	})
	if err != nil {
		t.Fatalf("%s wanted %s got %s: %v", kind, state, result.Observation.State, err)
	}
	return result
}

func TestSharedObservationReadsIssueNoAPIRequests(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "default", ResourceVersion: "1", Labels: map[string]string{"cnpg.io/cluster": "db"}}}
	client := fake.NewClientset(pod)
	subscriber, cancel, done := startObservationSubscriber(t, client)
	for _, kind := range observation.Kinds() {
		awaitObservation(t, subscriber, kind, protocol.ObservationCurrent)
	}
	counts := map[string]int{}
	for _, action := range client.Actions() {
		if action.GetVerb() == "list" {
			counts[action.GetResource().Resource]++
		}
	}
	for _, resource := range []string{"deployments", "statefulsets", "daemonsets", "pods", "persistentvolumeclaims", "storageclasses"} {
		if counts[resource] != 1 {
			t.Fatalf("%s bootstrap LISTs=%d", resource, counts[resource])
		}
	}
	watches := map[string]int{}
	for _, action := range client.Actions() {
		if action.GetVerb() == "watch" {
			watches[action.GetResource().Resource]++
		}
	}
	for _, resource := range []string{"deployments", "statefulsets", "daemonsets", "pods", "persistentvolumeclaims", "storageclasses"} {
		if watches[resource] != 1 {
			t.Fatalf("%s bootstrap WATCHs=%d", resource, watches[resource])
		}
	}
	before := trackedActionCount(client.Actions())
	for range 100 {
		for _, kind := range observation.Kinds() {
			_ = subscriber.ObservationSnapshot(kind)
		}
	}
	if got := trackedActionCount(client.Actions()) - before; got != 0 {
		t.Fatalf("600 reads issued %d API requests", got)
	}
	t.Log("six tracked kinds: 6 bootstrap LISTs; 600 snapshot reads: 0 additional LIST/GET/WATCH")
	current := subscriber.ObservationSnapshot(observation.Pods)
	current.Pods[0].Labels["cnpg.io/cluster"] = "corrupt"
	if subscriber.ObservationSnapshot(observation.Pods).Pods[0].Labels["cnpg.io/cluster"] != "db" {
		t.Fatal("snapshot is mutable alias")
	}
	if err := client.CoreV1().Pods("default").Delete(context.Background(), "one", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait.PollUntilContextTimeout(context.Background(), 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) {
		snapshot := subscriber.ObservationSnapshot(observation.Pods)
		return snapshot.Observation.State == protocol.ObservationCurrent && len(snapshot.Pods) == 0, nil
	}); err != nil {
		t.Fatal("delete not reflected", err)
	}
	cancel()
	<-done
	awaitObservation(t, subscriber, observation.Pods, protocol.ObservationDisconnected)
}

func TestSharedObservationPartialPermissionsAndStorageSingleWatch(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("list", "persistentvolumeclaims", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "persistentvolumeclaims"}, "", nil)
	})
	subscriber := NewStateSubscriber(client, &recordingSender{}, slog.New(slog.DiscardHandler))
	subscriber.syncTimeout = 100 * time.Millisecond
	metadata := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())
	subscriber.meta = metadata
	subscriber.crdAvailable = func(metadataKind) bool { return false }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { subscriber.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("shutdown")
		}
	}()
	denied := awaitObservation(t, subscriber, observation.Claims, protocol.ObservationDenied)
	if denied.Observation.ObservedAt != nil {
		t.Fatal("never observed denied store has time")
	}
	awaitObservation(t, subscriber, observation.StorageClasses, protocol.ObservationCurrent)
	awaitObservation(t, subscriber, observation.Deployments, protocol.ObservationCurrent)
	for _, action := range metadata.Actions() {
		switch action.GetResource().Resource {
		case "persistentvolumeclaims", "storageclasses":
			t.Fatalf("duplicate metadata request: %v", action)
		}
	}
}

func trackedActionCount(actions []ktesting.Action) int {
	count := 0
	for _, action := range actions {
		switch action.GetResource().Resource {
		case "deployments", "statefulsets", "daemonsets", "pods", "persistentvolumeclaims", "storageclasses":
			count++
		}
	}
	return count
}

func TestSharedObservationRealReflectorRecoversExpiredWatch(t *testing.T) {
	client := fake.NewClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "old", ResourceVersion: "1"}})
	first := watch.NewRaceFreeFake()
	var watchCalls atomic.Int32
	client.PrependWatchReactor("pods", func(action ktesting.Action) (bool, watch.Interface, error) {
		if watchCalls.Add(1) == 1 {
			return true, first, nil
		}
		stream, err := client.Tracker().Watch(action.GetResource(), action.GetNamespace())
		return true, stream, err
	})
	subscriber, _, _ := startObservationSubscriber(t, client)
	before := awaitObservation(t, subscriber, observation.Pods, protocol.ObservationCurrent)
	if len(before.Pods) != 1 {
		t.Fatal("initial list missing")
	}
	if err := client.CoreV1().Pods("").Delete(context.Background(), "old", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	first.Error(&metav1.Status{Reason: metav1.StatusReasonExpired, Code: 410})
	if err := wait.PollUntilContextTimeout(context.Background(), 5*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		snapshot := subscriber.ObservationSnapshot(observation.Pods)
		return watchCalls.Load() >= 2 && snapshot.Observation.State == protocol.ObservationCurrent && len(snapshot.Pods) == 0, nil
	}); err != nil {
		t.Fatal("reflector failed to relist/apply deletion after410", err)
	}
	lists := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "pods" {
			lists++
		}
	}
	if lists != 2 {
		t.Fatalf("410 recovery LISTs=%d, want initial+repair", lists)
	}
}
