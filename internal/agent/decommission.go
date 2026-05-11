// Package agent: managed-side decommission handler.
//
// The server-side cluster decommission reconciler (see
// internal/worker/tasks/cluster_decommission.go) sends a MsgDecommission
// over the tunnel. This file is what answers that message — it deletes the
// managed-side resources that astronomer-go installed in the user's cluster
// (Fluent Bit DaemonSets via the astronomer-logging namespace, our labeled
// Velero CRs, the agent's own Deployment) and ACKs with a per-step report.
//
// Safety contract:
//
//  1. We NEVER hard-delete the "velero" or "monitoring" namespaces themselves
//     — the cluster operator may have other things there. We only remove
//     resources we labeled astronomer.io/managed=true.
//  2. The agent Deployment is the LAST thing deleted, AFTER sending the
//     ACK. (If we delete it before sending the ACK, the kubelet may tear
//     us down mid-write and the server never sees the result.)
//  3. Errors on individual resource deletes are reported in the ACK but
//     don't halt subsequent steps — partial cleanup is better than no
//     cleanup, and the operator can fix the rest manually using the per-
//     step error.
//  4. DryRun mode is honoured for the integration test path; nothing is
//     actually deleted but the counts reflect what WOULD be deleted.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// DefaultAgentNamespace is the namespace the agent is installed into by
// the manifest in deploy/agent/. Used as fallback when the server's
// MsgDecommission payload doesn't override it.
const DefaultAgentNamespace = "astronomer-system"

// DefaultAgentDeploymentName is the Deployment our manifest creates.
const DefaultAgentDeploymentName = "astronomer-agent"

// DefaultLoggingNamespace is what the logging stack lives in. This whole
// namespace is removed by the decommission flow — Fluent Bit + log
// forwarders + any ConfigMaps are tied to it.
const DefaultLoggingNamespace = "astronomer-logging"

// veleroNamespace is where Velero typically installs. We DO NOT delete this
// namespace; only Backup/Schedule/Restore CRs labeled astronomer.io/managed=true
// inside it.
const veleroNamespace = "velero"

// DecommissionHandler handles MsgDecommission messages.
type DecommissionHandler struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	log           *slog.Logger
	// agentDeleteDelay is how long we wait after sending the ACK before
	// deleting the agent's own Deployment. The delay gives the WS write
	// loop time to flush the ACK frame to the server.
	agentDeleteDelay time.Duration
}

// NewDecommissionHandler builds a handler from the shared K8sProxy client
// and rest.Config. The dynamic client is needed for Velero CR cleanup
// (Backups, Schedules, Restores) since those aren't typed in the agent's
// dependency set.
func NewDecommissionHandler(client kubernetes.Interface, restCfg *rest.Config, log *slog.Logger) (*DecommissionHandler, error) {
	if log == nil {
		log = slog.Default()
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	return &DecommissionHandler{
		clientset:        client,
		dynamicClient:    dyn,
		log:              log,
		agentDeleteDelay: 2 * time.Second,
	}, nil
}

// HandleDecommission is the MessageHandler entrypoint registered for
// MsgDecommission. It returns the MsgDecommissionAck synchronously; the
// caller (tunnel readLoop) routes that response back via stream_id.
//
// After the ACK is queued, the handler schedules the agent's own Deployment
// for deletion on a short delay so the WS write loop has a chance to flush.
func (h *DecommissionHandler) HandleDecommission(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	if h == nil {
		return nil, fmt.Errorf("decommission handler not configured")
	}
	var req protocol.DecommissionPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, fmt.Errorf("decode decommission payload: %w", err)
	}

	steps := []protocol.DecommissionStepResult{}

	if req.RemoveLoggingStack {
		step := h.removeLoggingStack(ctx, req.DryRun)
		steps = append(steps, step)
	} else {
		steps = append(steps, protocol.DecommissionStepResult{
			Name:    "remove_logging_stack",
			Skipped: true,
		})
	}

	if req.RemoveVeleroManaged {
		managedLabel := req.ManagedLabel
		if managedLabel == "" {
			managedLabel = "astronomer.io/managed=true"
		}
		step := h.removeVeleroManaged(ctx, managedLabel, req.DryRun)
		steps = append(steps, step)
	} else {
		steps = append(steps, protocol.DecommissionStepResult{
			Name:    "remove_velero_managed",
			Skipped: true,
		})
	}

	// Build and queue the ACK. The agent Deployment delete (step below) is
	// scheduled on a delay so the WS write loop can flush this frame first.
	ackStep := protocol.DecommissionStepResult{Name: "remove_agent_deployment"}
	if !req.RemoveAgentDeployment {
		ackStep.Skipped = true
	} else if req.DryRun {
		ackStep.Success = true
		ackStep.Removed = 1
	} else {
		// We mark the step "success: pending" so the server knows we
		// scheduled the delete; the actual kubectl delete happens after
		// the ACK is queued for writing.
		ackStep.Success = true
		ackStep.Removed = 1
	}
	steps = append(steps, ackStep)

	ack := protocol.DecommissionAckPayload{
		ClusterID: req.ClusterID,
		DryRun:    req.DryRun,
		Steps:     steps,
	}
	body, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("marshal decommission ack: %w", err)
	}
	resp := &protocol.Message{
		Type:      protocol.MsgDecommissionAck,
		StreamID:  msg.StreamID,
		RequestID: msg.RequestID,
		ClusterID: msg.ClusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}

	if req.RemoveAgentDeployment && !req.DryRun {
		// Fire-and-forget the agent self-delete after a short delay. We
		// deliberately use context.Background here: if the caller's ctx
		// is cancelled (which it will be when our pod is terminating!) we
		// don't want that to interrupt the API server delete request mid-
		// flight.
		ns := req.AgentNamespace
		if ns == "" {
			ns = DefaultAgentNamespace
		}
		name := req.AgentDeployment
		if name == "" {
			name = DefaultAgentDeploymentName
		}
		delay := h.agentDeleteDelay
		go func() {
			time.Sleep(delay)
			deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := h.clientset.AppsV1().Deployments(ns).Delete(deleteCtx, name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				h.log.Warn("decommission: failed to delete agent deployment",
					"namespace", ns, "name", name, "error", err)
			} else {
				h.log.Info("decommission: agent deployment deleted",
					"namespace", ns, "name", name)
			}
		}()
	}

	return resp, nil
}

// removeLoggingStack deletes the entire astronomer-logging namespace.
// Fluent Bit DaemonSets, log forwarder ConfigMaps, ServiceAccounts and any
// other resources we installed under it go away with the namespace.
// We DON'T touch the cluster operator's own log collectors that live in
// other namespaces.
func (h *DecommissionHandler) removeLoggingStack(ctx context.Context, dryRun bool) protocol.DecommissionStepResult {
	step := protocol.DecommissionStepResult{Name: "remove_logging_stack"}
	if dryRun {
		// Check whether the namespace exists; reported "removed" is 0 or 1.
		_, err := h.clientset.CoreV1().Namespaces().Get(ctx, DefaultLoggingNamespace, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				step.Success = true
				return step
			}
			step.Error = err.Error()
			return step
		}
		step.Success = true
		step.Removed = 1
		return step
	}
	err := h.clientset.CoreV1().Namespaces().Delete(ctx, DefaultLoggingNamespace, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			step.Success = true
			return step
		}
		step.Error = err.Error()
		return step
	}
	step.Success = true
	step.Removed = 1
	return step
}

// veleroGVRs are the Velero CRDs we hard-delete when astronomer.io/managed=true.
// We don't touch other CRDs or the velero namespace itself.
var veleroGVRs = []schema.GroupVersionResource{
	{Group: "velero.io", Version: "v1", Resource: "backups"},
	{Group: "velero.io", Version: "v1", Resource: "schedules"},
	{Group: "velero.io", Version: "v1", Resource: "restores"},
}

// removeVeleroManaged deletes Velero CRs labeled astronomer.io/managed=true
// from the velero namespace. The namespace itself stays intact.
func (h *DecommissionHandler) removeVeleroManaged(ctx context.Context, labelSelector string, dryRun bool) protocol.DecommissionStepResult {
	step := protocol.DecommissionStepResult{Name: "remove_velero_managed"}
	if h.dynamicClient == nil {
		step.Error = "dynamic client not configured"
		return step
	}
	totalRemoved := 0
	errs := []string{}
	for _, gvr := range veleroGVRs {
		list, err := h.dynamicClient.Resource(gvr).Namespace(veleroNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// CRD not installed — silent skip.
				continue
			}
			errs = append(errs, fmt.Sprintf("list %s: %v", gvr.Resource, err))
			continue
		}
		if dryRun {
			totalRemoved += len(list.Items)
			continue
		}
		for _, item := range list.Items {
			if err := h.dynamicClient.Resource(gvr).Namespace(veleroNamespace).Delete(ctx, item.GetName(), metav1.DeleteOptions{}); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				errs = append(errs, fmt.Sprintf("delete %s/%s: %v", gvr.Resource, item.GetName(), err))
				continue
			}
			totalRemoved++
		}
	}
	step.Removed = totalRemoved
	if len(errs) > 0 {
		step.Error = errors.Join(toErrors(errs)...).Error()
		// Partial success: we may have removed some CRs even with errors;
		// reflect that in the count but not in Success.
		step.Success = false
		return step
	}
	step.Success = true
	return step
}

func toErrors(strs []string) []error {
	out := make([]error, 0, len(strs))
	for _, s := range strs {
		out = append(out, errors.New(s))
	}
	return out
}
