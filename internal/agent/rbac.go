package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// RBACSyncer applies RBAC resources from the server to the cluster.
type RBACSyncer struct {
	client *kubernetes.Clientset
	log    *slog.Logger
}

// NewRBACSyncer creates a new RBACSyncer.
func NewRBACSyncer(client *kubernetes.Clientset, log *slog.Logger) *RBACSyncer {
	return &RBACSyncer{
		client: client,
		log:    log,
	}
}

// HandleSyncRequest processes RBAC_SYNC_REQUEST messages. It applies ClusterRoles,
// ClusterRoleBindings, Roles, and RoleBindings using server-side apply and garbage
// collects removed bindings based on the managed label.
func (s *RBACSyncer) HandleSyncRequest(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error {
	var payload protocol.RBACSyncRequestPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("decode RBAC sync request: %w", err)
	}

	s.log.Info("processing RBAC sync request",
		"cluster_roles", len(payload.ClusterRoles),
		"cluster_role_bindings", len(payload.ClusterRoleBindings),
		"roles", len(payload.Roles),
		"role_bindings", len(payload.RoleBindings),
	)

	// SAFETY GUARDRAIL (H5): the RBAC-sync side channel must NOT be an unbounded,
	// control-plane-driven cluster-RBAC-write primitive (it was applying arbitrary
	// Cluster/Role bindings verbatim for every profile — under admin, an
	// unrestricted RBAC-write over the whole cluster). Mirroring reconcile.go's
	// safety contract, REFUSE — fail-closed, apply nothing — any request that
	// carries cluster-scoped RBAC or namespaced RBAC outside the astronomer-owned
	// namespaces. There is currently no server feature that drives RBAC sync; this
	// is defense-in-depth against a compromised/rogue management plane.
	if reason := rbacSyncOutOfBounds(payload); reason != "" {
		s.log.Warn("REFUSED RBAC sync request (out of bounds)", "reason", reason)
		return s.sendSyncResult(sendFn, msg.RequestID, protocol.RBACSyncResultPayload{
			Errors: []string{"refused: " + reason},
		})
	}

	result := protocol.RBACSyncResultPayload{}
	var syncErrors []string

	// Track names of applied resources for garbage collection.
	appliedClusterRoles := make(map[string]bool)
	appliedClusterRoleBindings := make(map[string]bool)
	appliedRoles := make(map[string]bool)        // "namespace/name"
	appliedRoleBindings := make(map[string]bool) // "namespace/name"

	// Apply ClusterRoles.
	for _, raw := range payload.ClusterRoles {
		var cr rbacv1.ClusterRole
		if err := json.Unmarshal(raw, &cr); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("unmarshal ClusterRole: %v", err))
			continue
		}
		if payload.ManagedLabel != "" {
			if cr.Labels == nil {
				cr.Labels = make(map[string]string)
			}
			cr.Labels[payload.ManagedLabel] = "true"
		}
		if _, err := s.client.RbacV1().ClusterRoles().Create(ctx, &cr, metav1.CreateOptions{}); err != nil {
			// Try update if create fails (already exists).
			if _, err := s.client.RbacV1().ClusterRoles().Update(ctx, &cr, metav1.UpdateOptions{}); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("apply ClusterRole %s: %v", cr.Name, err))
				continue
			}
		}
		appliedClusterRoles[cr.Name] = true
		result.Applied++
	}

	// Apply ClusterRoleBindings.
	for _, raw := range payload.ClusterRoleBindings {
		var crb rbacv1.ClusterRoleBinding
		if err := json.Unmarshal(raw, &crb); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("unmarshal ClusterRoleBinding: %v", err))
			continue
		}
		if payload.ManagedLabel != "" {
			if crb.Labels == nil {
				crb.Labels = make(map[string]string)
			}
			crb.Labels[payload.ManagedLabel] = "true"
		}
		if _, err := s.client.RbacV1().ClusterRoleBindings().Create(ctx, &crb, metav1.CreateOptions{}); err != nil {
			if _, err := s.client.RbacV1().ClusterRoleBindings().Update(ctx, &crb, metav1.UpdateOptions{}); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("apply ClusterRoleBinding %s: %v", crb.Name, err))
				continue
			}
		}
		appliedClusterRoleBindings[crb.Name] = true
		result.Applied++
	}

	// Apply Roles.
	for _, raw := range payload.Roles {
		var r rbacv1.Role
		if err := json.Unmarshal(raw, &r); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("unmarshal Role: %v", err))
			continue
		}
		if payload.ManagedLabel != "" {
			if r.Labels == nil {
				r.Labels = make(map[string]string)
			}
			r.Labels[payload.ManagedLabel] = "true"
		}
		if _, err := s.client.RbacV1().Roles(r.Namespace).Create(ctx, &r, metav1.CreateOptions{}); err != nil {
			if _, err := s.client.RbacV1().Roles(r.Namespace).Update(ctx, &r, metav1.UpdateOptions{}); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("apply Role %s/%s: %v", r.Namespace, r.Name, err))
				continue
			}
		}
		appliedRoles[r.Namespace+"/"+r.Name] = true
		result.Applied++
	}

	// Apply RoleBindings.
	for _, raw := range payload.RoleBindings {
		var rb rbacv1.RoleBinding
		if err := json.Unmarshal(raw, &rb); err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("unmarshal RoleBinding: %v", err))
			continue
		}
		if payload.ManagedLabel != "" {
			if rb.Labels == nil {
				rb.Labels = make(map[string]string)
			}
			rb.Labels[payload.ManagedLabel] = "true"
		}
		if _, err := s.client.RbacV1().RoleBindings(rb.Namespace).Create(ctx, &rb, metav1.CreateOptions{}); err != nil {
			if _, err := s.client.RbacV1().RoleBindings(rb.Namespace).Update(ctx, &rb, metav1.UpdateOptions{}); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("apply RoleBinding %s/%s: %v", rb.Namespace, rb.Name, err))
				continue
			}
		}
		appliedRoleBindings[rb.Namespace+"/"+rb.Name] = true
		result.Applied++
	}

	// Garbage collect removed resources with managed label.
	if payload.ManagedLabel != "" {
		removed, gcErrors := s.garbageCollect(ctx, payload.ManagedLabel,
			appliedClusterRoles, appliedClusterRoleBindings, appliedRoles, appliedRoleBindings)
		result.Removed = removed
		syncErrors = append(syncErrors, gcErrors...)
	}

	if len(syncErrors) > 0 {
		result.Errors = syncErrors
	}

	return s.sendSyncResult(sendFn, msg.RequestID, result)
}

// sendSyncResult marshals and sends an RBAC_SYNC_RESULT reply.
func (s *RBACSyncer) sendSyncResult(sendFn func(*protocol.Message) error, requestID string, result protocol.RBACSyncResultPayload) error {
	resultPayload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal RBAC sync result: %w", err)
	}
	return sendFn(&protocol.Message{
		Type:      protocol.MsgRBACSyncResult,
		RequestID: requestID,
		Payload:   resultPayload,
	})
}

// rbacSyncOutOfBounds returns a non-empty reason if the payload contains any
// resource the RBAC syncer is NOT allowed to touch: any cluster-scoped RBAC
// (ClusterRole/ClusterRoleBinding) or any namespaced Role/RoleBinding outside
// the astronomer-owned namespaces. Mirrors reconcile.go's owned-namespace bound.
func rbacSyncOutOfBounds(payload protocol.RBACSyncRequestPayload) string {
	if len(payload.ClusterRoles) > 0 || len(payload.ClusterRoleBindings) > 0 {
		return fmt.Sprintf("RBAC sync may not manage cluster-scoped RBAC (%d ClusterRoles, %d ClusterRoleBindings)",
			len(payload.ClusterRoles), len(payload.ClusterRoleBindings))
	}
	for _, raw := range payload.Roles {
		var r rbacv1.Role
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Sprintf("unparseable Role in payload: %v", err)
		}
		if !isAstronomerOwnedNamespace(r.Namespace) {
			return fmt.Sprintf("Role %s/%s targets a non-astronomer namespace", r.Namespace, r.Name)
		}
	}
	for _, raw := range payload.RoleBindings {
		var rb rbacv1.RoleBinding
		if err := json.Unmarshal(raw, &rb); err != nil {
			return fmt.Sprintf("unparseable RoleBinding in payload: %v", err)
		}
		if !isAstronomerOwnedNamespace(rb.Namespace) {
			return fmt.Sprintf("RoleBinding %s/%s targets a non-astronomer namespace", rb.Namespace, rb.Name)
		}
	}
	return ""
}

// isAstronomerOwnedNamespace reports whether ns is one of the astronomer-owned
// namespaces the agent is permitted to manage RBAC within.
func isAstronomerOwnedNamespace(ns string) bool {
	for _, owned := range agenttemplate.AstronomerOwnedNamespaces {
		if ns == owned {
			return true
		}
	}
	return false
}

// garbageCollect removes managed RBAC resources that are no longer in the desired state.
func (s *RBACSyncer) garbageCollect(
	ctx context.Context,
	managedLabel string,
	appliedCR, appliedCRB map[string]bool,
	appliedR, appliedRB map[string]bool,
) (int, []string) {
	removed := 0
	var errors []string
	labelSelector := managedLabel + "=true"

	// SAFETY (H5): GC is bounded to match the apply guardrail — it NEVER touches
	// cluster-scoped RBAC (the syncer no longer manages ClusterRoles/Bindings, so
	// GCing them by label would otherwise delete EVERY managed cluster RBAC the
	// moment a valid namespaced-only sync runs with an empty applied-set), and it
	// only deletes namespaced Roles/RoleBindings WITHIN the astronomer-owned
	// namespaces. appliedCR/appliedCRB are intentionally unused.
	_ = appliedCR
	_ = appliedCRB

	// Roles (owned namespaces only).
	roles, err := s.client.RbacV1().Roles("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		for _, r := range roles.Items {
			if !isAstronomerOwnedNamespace(r.Namespace) {
				continue
			}
			key := r.Namespace + "/" + r.Name
			if !appliedR[key] {
				if err := s.client.RbacV1().Roles(r.Namespace).Delete(ctx, r.Name, kubeutil.DeleteOptions()); err != nil {
					errors = append(errors, fmt.Sprintf("delete Role %s: %v", key, err))
				} else {
					removed++
					s.log.Info("garbage collected Role", "namespace", r.Namespace, "name", r.Name)
				}
			}
		}
	}

	// RoleBindings (owned namespaces only).
	rbs, err := s.client.RbacV1().RoleBindings("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		for _, rb := range rbs.Items {
			if !isAstronomerOwnedNamespace(rb.Namespace) {
				continue
			}
			key := rb.Namespace + "/" + rb.Name
			if !appliedRB[key] {
				if err := s.client.RbacV1().RoleBindings(rb.Namespace).Delete(ctx, rb.Name, kubeutil.DeleteOptions()); err != nil {
					errors = append(errors, fmt.Sprintf("delete RoleBinding %s: %v", key, err))
				} else {
					removed++
					s.log.Info("garbage collected RoleBinding", "namespace", rb.Namespace, "name", rb.Name)
				}
			}
		}
	}

	return removed, errors
}
