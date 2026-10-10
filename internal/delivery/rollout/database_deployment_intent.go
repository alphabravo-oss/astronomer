package rollout

import (
	"bytes"
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// UnconfiguredDeploymentIntent is DATA produced by the existing effective
// rollout resolver. It is not a rollout release, assignment or DB authority.
type UnconfiguredDeploymentIntent struct {
	Renderer            json.RawMessage
	ConfigurationDigest model.Digest
	SpecDigest          model.Digest
}

// ResolveUnconfiguredDeployment reuses the SAME resolver and hash domains as
// actual rollout planning. Its scope is an exact builtin with no configuration
// template, override set or target override. Owners must lock and prove those
// prerequisites against actual rows before comparing a deployment to this data.
func ResolveUnconfiguredDeployment(bundleDigest model.Digest, rendererJSON json.RawMessage) (UnconfiguredDeploymentIntent, error) {
	if err := bundleDigest.Validate(); err != nil {
		return UnconfiguredDeploymentIntent{}, err
	}
	renderer, configuration, effective, err := resolveEffectiveRenderer(sqlc.GetDeliveryPlanningSnapshotRow{RendererSpec: bytes.Clone(rendererJSON)}, bundleDigest, model.TargetOverrides{}, nil, nil, nil)
	if err != nil {
		return UnconfiguredDeploymentIntent{}, err
	}
	encoded, err := json.Marshal(renderer)
	if err != nil {
		return UnconfiguredDeploymentIntent{}, err
	}
	return UnconfiguredDeploymentIntent{Renderer: encoded, ConfigurationDigest: configuration, SpecDigest: effective}, nil
}
