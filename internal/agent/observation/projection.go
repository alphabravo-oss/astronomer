package observation

import (
	"fmt"
	"maps"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Project preserves metadata used by live invalidation/replay and the exact
// workload/storage fields consumed by health and delivery inventory. No env,
// mounts, storage parameters, arbitrary annotations, or credential body is
// retained. The original informer input is never mutated.
func Project(object any) (any, error) {
	switch item := object.(type) {
	case *appsv1.Deployment:
		out := &appsv1.Deployment{ObjectMeta: objectMetadata(item.ObjectMeta), Status: *item.Status.DeepCopy()}
		out.Spec.Replicas = copyInt32(item.Spec.Replicas)
		out.Spec.Template.Spec.Containers = containers(item.Spec.Template.Spec.Containers)
		return out, nil
	case *appsv1.StatefulSet:
		out := &appsv1.StatefulSet{ObjectMeta: objectMetadata(item.ObjectMeta), Status: *item.Status.DeepCopy()}
		out.Spec.Replicas = copyInt32(item.Spec.Replicas)
		out.Spec.Template.Spec.Containers = containers(item.Spec.Template.Spec.Containers)
		return out, nil
	case *appsv1.DaemonSet:
		out := &appsv1.DaemonSet{ObjectMeta: objectMetadata(item.ObjectMeta), Status: *item.Status.DeepCopy()}
		out.Spec.Template.Spec.Containers = containers(item.Spec.Template.Spec.Containers)
		return out, nil
	case *corev1.Pod:
		out := &corev1.Pod{ObjectMeta: objectMetadata(item.ObjectMeta)}
		out.Spec.Containers = containers(item.Spec.Containers)
		out.Status.Conditions = slices.Clone(item.Status.Conditions)
		return out, nil
	case *corev1.PersistentVolumeClaim:
		out := &corev1.PersistentVolumeClaim{ObjectMeta: objectMetadata(item.ObjectMeta)}
		out.Spec.Resources.Requests = item.Spec.Resources.Requests.DeepCopy()
		if item.Spec.StorageClassName != nil {
			name := *item.Spec.StorageClassName
			out.Spec.StorageClassName = &name
		}
		out.Spec.VolumeName = item.Spec.VolumeName
		if item.Spec.VolumeMode != nil {
			mode := *item.Spec.VolumeMode
			out.Spec.VolumeMode = &mode
		}
		out.Spec.AccessModes = slices.Clone(item.Spec.AccessModes)
		out.Status.Phase = item.Status.Phase
		out.Status.Capacity = item.Status.Capacity.DeepCopy()
		out.Status.AccessModes = slices.Clone(item.Status.AccessModes)
		return out, nil
	case *storagev1.StorageClass:
		out := &storagev1.StorageClass{ObjectMeta: objectMetadata(item.ObjectMeta), Provisioner: item.Provisioner}
		out.Annotations = map[string]string{}
		for _, key := range []string{"storageclass.kubernetes.io/is-default-class", "storageclass.beta.kubernetes.io/is-default-class"} {
			if value, ok := item.Annotations[key]; ok {
				out.Annotations[key] = value
			}
		}
		if item.AllowVolumeExpansion != nil {
			allowed := *item.AllowVolumeExpansion
			out.AllowVolumeExpansion = &allowed
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported observation projection %T", object)
	}
}

func objectMetadata(meta metav1.ObjectMeta) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: meta.Name, Namespace: meta.Namespace, UID: meta.UID, ResourceVersion: meta.ResourceVersion, Generation: meta.Generation, CreationTimestamp: meta.CreationTimestamp, Labels: maps.Clone(meta.Labels)}
}
func copyInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
func containers(items []corev1.Container) []corev1.Container {
	result := make([]corev1.Container, len(items))
	for n, item := range items {
		result[n] = corev1.Container{Name: item.Name, Image: item.Image, Resources: *item.Resources.DeepCopy()}
		// Delivery verifies only these fixed, non-sensitive hardening switches.
		// Retaining arbitrary workload command arguments could retain credentials.
		for _, arg := range item.Args {
			if arg == "--no-cross-namespace-refs=true" || arg == "--no-remote-bases=true" {
				result[n].Args = append(result[n].Args, arg)
			}
		}
	}
	return result
}

func (s *Snapshot) copyObjects(objects []any) {
	for _, raw := range objects {
		switch item := raw.(type) {
		case *appsv1.Deployment:
			s.Deployments = append(s.Deployments, item.DeepCopy())
		case *appsv1.StatefulSet:
			s.StatefulSets = append(s.StatefulSets, item.DeepCopy())
		case *appsv1.DaemonSet:
			s.DaemonSets = append(s.DaemonSets, item.DeepCopy())
		case *corev1.Pod:
			s.Pods = append(s.Pods, item.DeepCopy())
		case *corev1.PersistentVolumeClaim:
			s.Claims = append(s.Claims, item.DeepCopy())
		case *storagev1.StorageClass:
			s.StorageClasses = append(s.StorageClasses, item.DeepCopy())
		}
	}
}
