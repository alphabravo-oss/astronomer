package agent

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type decommissionOwnedObject struct {
	name  string
	uid   types.UID
	key   string
	value string
}

type decommissionSelfDelete struct {
	handler         *DecommissionHandler
	namespace       string
	deployment      decommissionOwnedObject
	namespaceObject decommissionOwnedObject
	role            decommissionOwnedObject
	binding         decommissionOwnedObject
}

func capturedDecommissionObject(obj metav1.Object, name, key, value string, err error) (decommissionOwnedObject, error) {
	if apierrors.IsNotFound(err) {
		return decommissionOwnedObject{}, nil
	}
	if err != nil {
		return decommissionOwnedObject{}, err
	}
	if obj == nil || !hasLabel(obj.GetLabels(), key, value) {
		return decommissionOwnedObject{}, errors.New("decommission target lacks managed ownership")
	}
	if obj.GetUID() == "" {
		return decommissionOwnedObject{}, errors.New("decommission target UID unavailable")
	}
	return decommissionOwnedObject{name: name, uid: obj.GetUID(), key: key, value: value}, nil
}

// Never infer a delete target from a payload name alone. Capture ownership
// before the other teardown steps can remove credentials or RBAC.
func (h *DecommissionHandler) prepareSelfDelete(ctx context.Context, req protocol.DecommissionPayload) (*decommissionSelfDelete, protocol.DecommissionStepResult) {
	step := protocol.DecommissionStepResult{Name: "remove_agent_deployment"}
	if !req.RemoveAgentDeployment {
		step.Skipped = true
		return nil, step
	}
	if !req.DryRun && decommissionReplyFromContext(ctx) == nil {
		step.Error = "decommission ACK writer ownership unavailable"
		return nil, step
	}
	if !req.DryRun && !h.selfDeletePending.CompareAndSwap(false, true) {
		step.Error = "decommission self-delete already pending"
		return nil, step
	}
	plan, err := h.captureSelfDelete(ctx, req)
	if err != nil {
		if !req.DryRun {
			h.selfDeletePending.Store(false)
		}
		h.log.Warn("decommission: self-delete admission failed", "error", err)
		step.Error = "decommission self-delete ownership could not be admitted"
		return nil, step
	}
	step.Success = true
	if plan.deployment.uid != "" {
		step.Removed = 1
	}
	if req.DryRun {
		return nil, step
	}
	return plan, step
}

func (h *DecommissionHandler) captureSelfDelete(ctx context.Context, req protocol.DecommissionPayload) (*decommissionSelfDelete, error) {
	ns := h.agentNamespace(req)
	name := orDefault(req.AgentDeployment, DefaultAgentDeploymentName)
	key, val := splitSelector(orDefault(req.RBACLabel, defaultRBACSelector))
	obj, err := h.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	deployment, err := capturedDecommissionObject(obj, name, key, val, err)
	if err != nil {
		return nil, err
	}
	plan := &decommissionSelfDelete{handler: h, namespace: ns, deployment: deployment}
	if !req.RemoveFullFootprint {
		return plan, nil
	}
	nsKey, nsVal := splitSelector(orDefault(req.ManagedByLabel, defaultManagedBySelector))
	nsObj, err := h.clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if apierrors.IsForbidden(err) {
		h.log.Warn("decommission: namespace capture forbidden; retaining namespace", "error", err)
	} else if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	// An operator-precreated namespace is never admitted for cascade deletion.
	if err == nil && nsObj != nil && hasLabel(nsObj.Labels, nsKey, nsVal) {
		plan.namespaceObject, err = capturedDecommissionObject(nsObj, ns, nsKey, nsVal, nil)
		if err != nil {
			return nil, err
		}
	}
	rbac := h.clientset.RbacV1()
	binding, err := rbac.ClusterRoleBindings().Get(ctx, "astronomer-agent", metav1.GetOptions{})
	if apierrors.IsForbidden(err) {
		h.log.Warn("decommission: ClusterRoleBinding capture forbidden; retaining binding", "error", err)
	} else if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil && binding != nil && hasLabel(binding.Labels, key, val) {
		plan.binding, err = capturedDecommissionObject(binding, "astronomer-agent", key, val, nil)
		if err != nil {
			return nil, err
		}
	}
	role, err := rbac.ClusterRoles().Get(ctx, "astronomer-agent", metav1.GetOptions{})
	if apierrors.IsForbidden(err) {
		h.log.Warn("decommission: ClusterRole capture forbidden; retaining role", "error", err)
	} else if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil && role != nil && hasLabel(role.Labels, key, val) {
		plan.role, err = capturedDecommissionObject(role, "astronomer-agent", key, val, nil)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// Recheck labels and original UID, then include both current UID and RV in the
// API DELETE. A replacement or ownership change during either gap fails closed.
func (o decommissionOwnedObject) delete(ctx context.Context, get func(context.Context) (metav1.Object, error), del func(context.Context, metav1.DeleteOptions) error) error {
	if o.uid == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	live, err := get(ctx)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if live == nil || live.GetUID() != o.uid || live.GetResourceVersion() == "" || !hasLabel(live.GetLabels(), o.key, o.value) {
		return errors.New("decommission captured object changed")
	}
	uid := o.uid
	preconditions := &metav1.Preconditions{UID: &uid}
	rv := live.GetResourceVersion()
	preconditions.ResourceVersion = &rv
	err = del(ctx, metav1.DeleteOptions{Preconditions: preconditions})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (p *decommissionSelfDelete) execute(ctx context.Context) error {
	h := p.handler
	deps := h.clientset.AppsV1().Deployments(p.namespace)
	if err := p.deployment.delete(ctx,
		func(ctx context.Context) (metav1.Object, error) {
			return deps.Get(ctx, p.deployment.name, metav1.GetOptions{})
		},
		func(ctx context.Context, opts metav1.DeleteOptions) error {
			return deps.Delete(ctx, p.deployment.name, opts)
		}); err != nil {
		// Never cascade a namespace after failing to delete the captured Deployment.
		return fmt.Errorf("agent deployment cleanup: %w", err)
	}
	namespaces := h.clientset.CoreV1().Namespaces()
	namespaceErr := p.namespaceObject.delete(ctx,
		func(ctx context.Context) (metav1.Object, error) {
			return namespaces.Get(ctx, p.namespaceObject.name, metav1.GetOptions{})
		},
		func(ctx context.Context, opts metav1.DeleteOptions) error {
			return namespaces.Delete(ctx, p.namespaceObject.name, opts)
		})
	if namespaceErr != nil {
		// Preserve legacy best-effort ClusterRole teardown after a namespace denial.
		h.log.Warn("decommission: captured agent namespace cleanup incomplete", "error", namespaceErr)
	}
	rbac := h.clientset.RbacV1()
	bindingErr := p.binding.delete(ctx,
		func(ctx context.Context) (metav1.Object, error) {
			return rbac.ClusterRoleBindings().Get(ctx, p.binding.name, metav1.GetOptions{})
		},
		func(ctx context.Context, opts metav1.DeleteOptions) error {
			return rbac.ClusterRoleBindings().Delete(ctx, p.binding.name, opts)
		})
	roleErr := p.role.delete(ctx,
		func(ctx context.Context) (metav1.Object, error) {
			return rbac.ClusterRoles().Get(ctx, p.role.name, metav1.GetOptions{})
		},
		func(ctx context.Context, opts metav1.DeleteOptions) error {
			return rbac.ClusterRoles().Delete(ctx, p.role.name, opts)
		})
	return errors.Join(namespaceErr, bindingErr, roleErr)
}
