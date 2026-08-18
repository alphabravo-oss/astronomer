package charlie

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const defaultModeCeilingTimeout = 90 * time.Second

type modeCeilingActivator interface {
	SetAdminMode(context.Context, Mode) (AdminBridgeStatus, error)
}

// HelmModeCeilingRollout updates the Charlie agent CHARLIE_MODE ceiling through
// the same Helm release used at connect time, waits for both replicas, then
// confirms the product bridge. The generic agent reads its ceiling from env.
type HelmModeCeilingRollout struct {
	Queries  activeCharlieConnectionReader
	Client   kubernetes.Interface
	Helm     HelmReleaser
	Workload AgentWorkloadReader
	Bridge   modeCeilingActivator
	Timeout  time.Duration
	Sleep    func(time.Duration)
}

func (r *HelmModeCeilingRollout) Reconcile(ctx context.Context, target ModeCeilingTarget) error {
	if r == nil || r.Helm == nil || r.Workload == nil || r.Bridge == nil || !validMode(target.Desired) {
		return fmt.Errorf("Charlie mode-ceiling rollout is unavailable")
	}
	// CHARLIE_MODE is the product-owned immutable ceiling. A background
	// reconcile that intersects Charlie central (effective < requested) must
	// not rewrite the env back to disabled while a raise is in flight. Central
	// can still fail-close with ProductEnabled.
	if validMode(target.ExpectedRequested) && modeRank(target.Desired) < modeRank(target.ExpectedRequested) && !target.ExpectedEmergencyDisabled {
		target.Desired = target.ExpectedRequested
	}
	current, err := r.Workload.AgentWorkload(ctx)
	if err != nil {
		return err
	}
	if !workloadMatchesCeiling(current, target.Desired) {
		helmErr := r.applyCeiling(ctx, target.Desired)
		if patchErr := r.patchCeiling(ctx, target.Desired); patchErr != nil && helmErr != nil {
			return fmt.Errorf("%v; %v", helmErr, patchErr)
		}
		if err := r.waitWorkload(ctx, target.Desired); err != nil {
			if helmErr != nil {
				return fmt.Errorf("%v; %w", helmErr, err)
			}
			return err
		}
	}
	status, err := r.Bridge.SetAdminMode(ctx, target.Desired)
	if err != nil {
		return err
	}
	if Mode(status.ProductModeCeiling) != target.Desired {
		return fmt.Errorf("Charlie product agent did not confirm the requested mode ceiling")
	}
	if target.Desired == ModeDisabled && status.ProductEnabled {
		return fmt.Errorf("Charlie product agent remained enabled after the disabled ceiling")
	}
	return nil
}

func (r *HelmModeCeilingRollout) applyCeiling(ctx context.Context, desired Mode) error {
	if r.Queries == nil {
		return fmt.Errorf("Charlie mode-ceiling connection is unavailable")
	}
	connection, err := r.Queries.GetActiveCharlieConnection(ctx)
	if err != nil || !connection.Active || strings.TrimSpace(connection.ChartReference) == "" {
		return fmt.Errorf("Charlie mode-ceiling chart is unavailable")
	}
	password := ""
	if r.Client != nil {
		secret, secretErr := r.Client.CoreV1().Secrets(agentNamespaceName).Get(ctx, artifactPullSecret, metav1.GetOptions{})
		if secretErr != nil && !apierrors.IsNotFound(secretErr) {
			return secretErr
		}
		password = dockerconfigPassword(secret)
	}
	return r.Helm.Apply(ctx, HelmReleaseSpec{
		ChartRef: connection.ChartReference, ChartDigest: connection.ChartDigest,
		Image: connection.ImageReference, ImageDigest: connection.ImageDigest,
		PullUser: "charlie", PullSecret: password, ReuseValues: true,
		Values: map[string]any{
			"runtime": map[string]any{"modeCeiling": string(desired)},
		},
	})
}

func (r *HelmModeCeilingRollout) patchCeiling(ctx context.Context, desired Mode) error {
	if r.Client == nil {
		return fmt.Errorf("Charlie mode-ceiling Kubernetes client is unavailable")
	}
	sts, err := r.Client.AppsV1().StatefulSets(agentNamespaceName).Get(ctx, agentReleaseName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(sts.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("Charlie agent workload has no containers")
	}
	found := false
	for i := range sts.Spec.Template.Spec.Containers[0].Env {
		if sts.Spec.Template.Spec.Containers[0].Env[i].Name == "CHARLIE_MODE" {
			sts.Spec.Template.Spec.Containers[0].Env[i].Value = string(desired)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("Charlie agent CHARLIE_MODE is missing")
	}
	_, err = r.Client.AppsV1().StatefulSets(agentNamespaceName).Update(ctx, sts, metav1.UpdateOptions{})
	return err
}

func (r *HelmModeCeilingRollout) waitWorkload(ctx context.Context, desired Mode) error {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultModeCeilingTimeout
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	deadline := time.Now().Add(timeout)
	for {
		status, err := r.Workload.AgentWorkload(ctx)
		if err == nil && workloadMatchesCeiling(status, desired) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Charlie agent ceiling %s was not ready on both replicas", desired)
		}
		sleep(2 * time.Second)
	}
}

func workloadMatchesCeiling(status AgentWorkloadStatus, desired Mode) bool {
	if !status.Present || status.ModeCeiling != desired || status.Desired <= 0 || status.Ready < status.Desired {
		return false
	}
	if status.Updated > 0 && status.Updated < status.Desired {
		return false
	}
	if status.UpdateRevision != "" && status.CurrentRevision != status.UpdateRevision {
		return false
	}
	return true
}
