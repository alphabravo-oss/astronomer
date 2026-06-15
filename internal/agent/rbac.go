package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

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

	resultPayload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal RBAC sync result: %w", err)
	}

	return sendFn(&protocol.Message{
		Type:      protocol.MsgRBACSyncResult,
		RequestID: msg.RequestID,
		Payload:   resultPayload,
	})
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

	// ClusterRoles.
	crs, err := s.client.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		for _, cr := range crs.Items {
			if !appliedCR[cr.Name] {
				if err := s.client.RbacV1().ClusterRoles().Delete(ctx, cr.Name, kubeutil.DeleteOptions()); err != nil {
					errors = append(errors, fmt.Sprintf("delete ClusterRole %s: %v", cr.Name, err))
				} else {
					removed++
					s.log.Info("garbage collected ClusterRole", "name", cr.Name)
				}
			}
		}
	}

	// ClusterRoleBindings.
	crbs, err := s.client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		for _, crb := range crbs.Items {
			if !appliedCRB[crb.Name] {
				if err := s.client.RbacV1().ClusterRoleBindings().Delete(ctx, crb.Name, kubeutil.DeleteOptions()); err != nil {
					errors = append(errors, fmt.Sprintf("delete ClusterRoleBinding %s: %v", crb.Name, err))
				} else {
					removed++
					s.log.Info("garbage collected ClusterRoleBinding", "name", crb.Name)
				}
			}
		}
	}

	// Roles (all namespaces).
	roles, err := s.client.RbacV1().Roles("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		for _, r := range roles.Items {
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

	// RoleBindings (all namespaces).
	rbs, err := s.client.RbacV1().RoleBindings("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err == nil {
		for _, rb := range rbs.Items {
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
