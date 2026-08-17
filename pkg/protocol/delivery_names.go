package protocol

import "strings"

const DeliveryProjectNamespacePrefix = "astronomer-delivery-p-"

// DeliveryObjectNames is the one deterministic naming contract shared by the
// control plane and agent. User-controlled display names never enter a
// Kubernetes object name.
type DeliveryObjectNames struct {
	ControlNamespace string
	Base             string
	Source           string
	AuthSecret       string
	TrustSecret      string
	Applier          string
}

func ObjectNames(projectID, deploymentID string) DeliveryObjectNames {
	projectHex := strings.ReplaceAll(projectID, "-", "")
	if len(projectHex) > 12 {
		projectHex = projectHex[:12]
	}
	deploymentHex := strings.ReplaceAll(deploymentID, "-", "")
	base := "d-" + deploymentHex
	return DeliveryObjectNames{
		ControlNamespace: DeliveryProjectNamespacePrefix + projectHex,
		Base:             base,
		Source:           base + "-source",
		AuthSecret:       base + "-source-auth",
		TrustSecret:      base + "-trust",
		Applier:          base + "-applier",
	}
}
