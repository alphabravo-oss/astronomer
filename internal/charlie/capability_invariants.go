package charlie

import (
	"fmt"
	"strings"
)

const (
	v1ReadRisk           = "low"
	v1WriteRisk          = "medium"
	v1ReadImpact         = "none"
	v1WriteImpact        = "bounded_management_plane_change"
	v1ReadReversibility  = "not_applicable"
	v1WriteReversibility = "adapter_declared"
	v1ReadRollback       = "not_applicable"
	v1WriteRollback      = "stop_and_operator_reconcile"
	v1ReadTargetBounds   = "astronomer_management_plane_only"
	v1WriteTargetBounds  = "allowlisted_astronomer_management_component_only"
)

// validateV1CapabilityDescriptor is the product-owned classification boundary.
// No caller-supplied safety label can compensate for a destructive,
// irreversible, or internally inconsistent descriptor.
func validateV1CapabilityDescriptor(descriptor CapabilityDescriptor) error {
	if strings.TrimSpace(descriptor.Name) == "" || descriptor.SchemaVersion != "1" || descriptor.Destructive || descriptor.ManagedTargetAccess ||
		descriptor.MaxResponseBytes < 1 || descriptor.MaxResponseBytes > maxActionResult || descriptor.TimeoutSeconds < 1 || descriptor.TimeoutSeconds > 30 ||
		!validCapabilitySource(descriptor.Source) || strings.TrimSpace(descriptor.RBACResource) == "" || strings.TrimSpace(descriptor.RBACVerb) == "" ||
		containsSpoofableSafetyField(descriptor.AcceptedFields) {
		return fmt.Errorf("Charlie v1 capability descriptor is unsafe")
	}

	switch descriptor.Effect {
	case EffectRead:
		if descriptor.Risk != v1ReadRisk || descriptor.TargetBounds != v1ReadTargetBounds || descriptor.Impact != v1ReadImpact || descriptor.Reversibility != v1ReadReversibility || descriptor.Rollback != v1ReadRollback || descriptor.RBACVerb != "read" ||
			descriptor.AutoEligible || descriptor.Idempotent || descriptor.RequiresPrecondition || descriptor.RequiresVerification ||
			containsDescriptorField(descriptor.AcceptedFields, "operation_id") {
			return fmt.Errorf("Charlie v1 read descriptor is unsafe")
		}
	case EffectWrite:
		if descriptor.Risk != v1WriteRisk || descriptor.TargetBounds != v1WriteTargetBounds || descriptor.Impact != v1WriteImpact || descriptor.Reversibility != v1WriteReversibility || descriptor.Rollback != v1WriteRollback || descriptor.RBACVerb != "update" ||
			!descriptor.Idempotent || !descriptor.RequiresPrecondition || !descriptor.RequiresVerification ||
			!containsDescriptorField(descriptor.AcceptedFields, "operation_id") || !containsDescriptorField(descriptor.AcceptedFields, "resource_id") {
			return fmt.Errorf("Charlie v1 write descriptor is destructive or irreversible")
		}
	default:
		return fmt.Errorf("Charlie v1 capability effect is invalid")
	}
	return nil
}

func containsDescriptorField(fields []string, wanted string) bool {
	for _, field := range fields {
		if field == wanted {
			return true
		}
	}
	return false
}

func validCapabilitySource(source CapabilitySource) bool {
	switch source {
	case SourceAstronomerDatabase, SourceAstronomerServer, SourceManagementKubernetes, SourceManagementArgo, SourceManagementQueue:
		return true
	default:
		return false
	}
}

func containsSpoofableSafetyField(fields []string) bool {
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.ToLower(strings.TrimSpace(field))
		if field == "" {
			return true
		}
		if _, exists := seen[field]; exists {
			return true
		}
		seen[field] = struct{}{}
		switch field {
		case "effect", "risk", "destructive", "irreversible", "reversible", "reversibility", "rollback", "verification", "requires_verification", "idempotent", "idempotency", "auto", "auto_eligible", "approval", "approval_id":
			return true
		}
	}
	return false
}

func sameCapabilityDescriptor(left, right CapabilityDescriptor) bool {
	if left.Name != right.Name || left.Description != right.Description || left.SchemaVersion != right.SchemaVersion || left.Effect != right.Effect ||
		left.Risk != right.Risk || left.TargetBounds != right.TargetBounds || left.Impact != right.Impact || left.Reversibility != right.Reversibility ||
		left.Rollback != right.Rollback || left.Source != right.Source || left.RBACResource != right.RBACResource || left.RBACVerb != right.RBACVerb ||
		left.MaxResponseBytes != right.MaxResponseBytes || left.TimeoutSeconds != right.TimeoutSeconds || left.Destructive != right.Destructive ||
		left.AutoEligible != right.AutoEligible || left.Idempotent != right.Idempotent || left.RequiresPrecondition != right.RequiresPrecondition ||
		left.RequiresVerification != right.RequiresVerification || left.ManagedTargetAccess != right.ManagedTargetAccess || len(left.AcceptedFields) != len(right.AcceptedFields) {
		return false
	}
	for index := range left.AcceptedFields {
		if left.AcceptedFields[index] != right.AcceptedFields[index] {
			return false
		}
	}
	return true
}
