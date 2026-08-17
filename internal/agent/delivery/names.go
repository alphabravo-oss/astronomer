package delivery

import "github.com/alphabravocompany/astronomer-go/pkg/protocol"

const (
	ManagedByLabel          = "app.kubernetes.io/managed-by"
	ManagedByValue          = "astronomer-agent"
	DeploymentIDLabel       = "delivery.astronomer.io/deployment-id"
	ProjectIDHashLabel      = "delivery.astronomer.io/project-id-hash"
	SpecDigestAnnotation    = "delivery.astronomer.io/spec-digest"
	GenerationAnnotation    = "delivery.astronomer.io/generation"
	DeliverySystemNamespace = "astronomer-delivery-system"
	ProjectNamespacePrefix  = protocol.DeliveryProjectNamespacePrefix
	PlatformClusterRole     = "astronomer-delivery-platform-applier"
)

// ObjectNames contains every user-independent name derived from an assignment.
// The complete UUID is used for per-deployment names; this avoids the collision
// risk introduced by cosmetic short IDs while remaining below DNS limits.
type ObjectNames = protocol.DeliveryObjectNames

func Names(projectID, deploymentID string) ObjectNames {
	return protocol.ObjectNames(projectID, deploymentID)
}
