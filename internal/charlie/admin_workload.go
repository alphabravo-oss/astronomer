package charlie

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AgentWorkloadStatus is the local Kubernetes replica projection for the
// generic Charlie agent. It is independent of Charlie heartbeats.
type AgentWorkloadStatus struct {
	Present         bool
	Desired         int32
	Ready           int32
	Updated         int32
	CurrentRevision string
	UpdateRevision  string
	ModeCeiling     Mode
}

type AgentWorkloadReader interface {
	AgentWorkload(context.Context) (AgentWorkloadStatus, error)
}

type KubernetesAgentWorkload struct {
	client    kubernetes.Interface
	namespace string
	name      string
}

func NewKubernetesAgentWorkload(client kubernetes.Interface) *KubernetesAgentWorkload {
	if client == nil {
		return nil
	}
	return &KubernetesAgentWorkload{client: client, namespace: agentNamespaceName, name: agentReleaseName}
}

func (k *KubernetesAgentWorkload) AgentWorkload(ctx context.Context) (AgentWorkloadStatus, error) {
	if k == nil || k.client == nil {
		return AgentWorkloadStatus{}, nil
	}
	sts, err := k.client.AppsV1().StatefulSets(k.namespace).Get(ctx, k.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return AgentWorkloadStatus{}, nil
	}
	if err != nil {
		return AgentWorkloadStatus{}, err
	}
	desired := int32(0)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	status := AgentWorkloadStatus{
		Present: true, Desired: desired, Ready: sts.Status.ReadyReplicas,
		Updated: sts.Status.UpdatedReplicas, CurrentRevision: sts.Status.CurrentRevision,
		UpdateRevision: sts.Status.UpdateRevision,
	}
	if containers := sts.Spec.Template.Spec.Containers; len(containers) > 0 {
		for _, env := range containers[0].Env {
			if env.Name == "CHARLIE_MODE" {
				if mode := Mode(env.Value); validMode(mode) {
					status.ModeCeiling = mode
				}
				break
			}
		}
	}
	return status, nil
}

func applyAgentWorkload(view *AdminStatusView, workload AgentWorkloadStatus) {
	if view == nil || !workload.Present {
		return
	}
	view.Agent.DesiredReplicas = workload.Desired
	view.Agent.ReadyReplicas = workload.Ready
	replicasReady := workload.Desired > 0 && workload.Ready >= workload.Desired
	if replicasReady && view.Agent.ApplicationState == "installing" {
		view.Agent.ApplicationState = "ready"
	}
	if validMode(workload.ModeCeiling) {
		view.Mode.WorkloadCeiling = workload.ModeCeiling
		want := view.Mode.Requested
		if !validMode(want) {
			want = view.Mode.Authoritative
		}
		// CHARLIE_MODE is the product-requested ceiling. During a raise it
		// matches Requested before Charlie central has caught up.
		view.Mode.WorkloadCeilingReady = replicasReady && workload.ModeCeiling == want
	} else if replicasReady && view.Mode.Authoritative == ModeDisabled && view.Mode.WorkloadCeiling == ModeDisabled {
		view.Mode.WorkloadCeilingReady = true
	}
}
