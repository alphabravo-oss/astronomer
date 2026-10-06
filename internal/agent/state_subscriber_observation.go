package agent

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type observationResource struct {
	kind   observation.Kind
	object runtime.Object
	list   func(context.Context, metav1.ListOptions) (runtime.Object, error)
	watch  func(context.Context, metav1.ListOptions) (watch.Interface, error)
}

// trackedListWatch explicitly uses the LIST-then-WATCH protocol. Initial watch
// events need a separate application barrier; do not silently opt into WatchList.
type trackedListWatch struct{ *cache.ListWatch }

func (*trackedListWatch) IsWatchListSemanticsUnSupported() bool { return true }

func observationResources(client kubernetes.Interface) []observationResource {
	apps, core, storage := client.AppsV1(), client.CoreV1(), client.StorageV1()
	return []observationResource{
		{observation.Deployments, &appsv1.Deployment{}, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return apps.Deployments("").List(ctx, o)
		}, apps.Deployments("").Watch},
		{observation.StatefulSets, &appsv1.StatefulSet{}, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return apps.StatefulSets("").List(ctx, o)
		}, apps.StatefulSets("").Watch},
		{observation.DaemonSets, &appsv1.DaemonSet{}, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return apps.DaemonSets("").List(ctx, o)
		}, apps.DaemonSets("").Watch},
		{observation.Pods, &corev1.Pod{}, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return core.Pods("").List(ctx, o)
		}, core.Pods("").Watch},
		{observation.Claims, &corev1.PersistentVolumeClaim{}, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return core.PersistentVolumeClaims("").List(ctx, o)
		}, core.PersistentVolumeClaims("").Watch},
		{observation.StorageClasses, &storagev1.StorageClass{}, func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return storage.StorageClasses().List(ctx, o)
		}, storage.StorageClasses().Watch},
	}
}

// Pre-register custom informers in the existing factory. Generated Pod/apps
// accessors reuse these exact instances, not duplicate factories or watches.
func (s *StateSubscriber) registerObservations(factory informers.SharedInformerFactory) {
	for _, resource := range observationResources(s.client) {
		tracker := observation.NewTracker(resource.kind, observation.Options{RecordRequest: recordObservationRequest, RecordWatchEvent: recordObservationWatchEvent})
		inf := factory.InformerFor(resource.object, func(_ kubernetes.Interface, resync time.Duration) cache.SharedIndexInformer {
			lw := &trackedListWatch{&cache.ListWatch{
				ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
					return tracker.List(ctx, o, resource.list)
				},
				WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
					return tracker.Watch(ctx, o, resource.watch)
				},
			}}
			informer := cache.NewSharedIndexInformer(lw, resource.object, resync, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			_ = informer.SetTransform(observation.Project)
			return informer
		})
		tracker.Bind(inf.GetStore(), inf.HasSynced)
		s.storeMu.Lock()
		if s.observations == nil {
			s.observations = make(map[observation.Kind]*observation.Tracker)
		}
		s.observations[resource.kind] = tracker
		s.storeMu.Unlock()
	}
	// These replace the old metadata-only stores. No other consumer loses its
	// existing kind, metadata invalidations or reconnect replay.
	s.attach(factory.Core().V1().PersistentVolumeClaims().Informer(), "PersistentVolumeClaim", "", "v1")
	s.attach(factory.Storage().V1().StorageClasses().Informer(), "StorageClass", "storage.k8s.io", "v1")
}

func (s *StateSubscriber) ObservationSnapshot(kind observation.Kind) observation.Snapshot {
	s.storeMu.RLock()
	tracker := s.observations[kind]
	s.storeMu.RUnlock()
	if tracker == nil {
		return observation.Snapshot{Kind: kind, Observation: protocol.DeliveryObservation{State: protocol.ObservationUnsynced}}
	}
	return tracker.Snapshot()
}

func (s *StateSubscriber) shutdownObservations(factory informers.SharedInformerFactory) {
	factory.Shutdown()
	s.storeMu.RLock()
	defer s.storeMu.RUnlock()
	for _, tracker := range s.observations {
		tracker.Disconnect()
	}
}
