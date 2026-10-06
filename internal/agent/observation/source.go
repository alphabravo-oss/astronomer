// Package observation defines the narrow, read-only inventory shared by agent
// consumers. It has no dependency on the agent runtime or delivery reconciler.
package observation

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type Kind string

const (
	Deployments    Kind = "Deployment"
	StatefulSets   Kind = "StatefulSet"
	DaemonSets     Kind = "DaemonSet"
	Pods           Kind = "Pod"
	Claims         Kind = "PersistentVolumeClaim"
	StorageClasses Kind = "StorageClass"
)

func Kinds() []Kind {
	return []Kind{Deployments, StatefulSets, DaemonSets, Pods, Claims, StorageClasses}
}

// Snapshot holds copies of a single kind's projected objects. Consumers may
// modify these copies without corrupting the canonical informer store. Objects
// carried with noncurrent Observation must never be used to claim health.
type Snapshot struct {
	Kind            Kind
	Observation     protocol.DeliveryObservation
	ResourceVersion string
	Deployments     []*appsv1.Deployment
	StatefulSets    []*appsv1.StatefulSet
	DaemonSets      []*appsv1.DaemonSet
	Pods            []*corev1.Pod
	Claims          []*corev1.PersistentVolumeClaim
	StorageClasses  []*storagev1.StorageClass
}

type Source interface{ ObservationSnapshot(Kind) Snapshot }
