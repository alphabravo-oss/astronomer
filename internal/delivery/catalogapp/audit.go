package catalogapp

import (
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/google/uuid"
)

// The catalog operation already records its accepted request. The rollout is
// a separate durable mutation and must carry its own intent into the planner's
// transaction, retaining the initiating user and catalog-operation identity.
func catalogRolloutAuditIntent(request InstallRequest, targetID uuid.UUID) audit.Intent {
	const action = "delivery.rollout.created"
	const resourceType = "delivery_rollout"
	key := "catalog:" + request.IdempotencyKey
	return audit.Intent{
		Event: audit.Event{
			Source: "service", ActionClass: "mutation", Action: action,
			UserID: request.ActorID, ResourceType: resourceType,
			ResourceID: targetID.String(), ResourceName: request.ReleaseName,
			RequestID: key, CorrelationID: request.IdempotencyKey,
			Detail: map[string]any{
				"actor": actor(request.ActorID), "project_id": request.ProjectID.String(),
				"cluster_id": request.ClusterID.String(), "installation_id": request.InstallationID.String(),
			},
		},
		DedupeKey: audit.MutationDedupeKey(key, action, resourceType, targetID.String()),
	}
}
