package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/hibiken/asynq"
)

// AgentManifestPayload contains parameters for generating an agent manifest.
type AgentManifestPayload struct {
	ClusterID        string `json:"cluster_id"`
	AgentToken       string `json:"agent_token"`
	ImageRepository  string `json:"image_repository,omitempty"`
	ImageTag         string `json:"image_tag,omitempty"`
	PrivilegeProfile string `json:"privilege_profile,omitempty"`
}

// NewAgentManifestTask rejects the retired queue-based renderer. Registration
// manifests contain credentials and must be returned synchronously by the API,
// never placed in Redis without a durable result sink.
func NewAgentManifestTask(payload AgentManifestPayload) (*asynq.Task, error) {
	_ = payload
	return nil, fmt.Errorf("agent:generate_manifest is retired; use the synchronous registration manifest API")
}

// HandleAgentManifest rejects legacy queue entries. The old handler rendered a
// manifest and discarded it, falsely acknowledging work while leaving a token
// in the queue payload.
func HandleAgentManifest(_ context.Context, t *asynq.Task) error {
	var p AgentManifestPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal agent manifest payload: %w", err)
	}

	if p.ClusterID == "" {
		return fmt.Errorf("cluster_id is required")
	}
	return fmt.Errorf("agent:generate_manifest is retired; use the synchronous registration manifest API: %w", asynq.SkipRetry)
}

func renderAgentManifest(ctx context.Context, clusterID, agentToken, serverURL, imageRepository, imageTag string, privilegeProfile ...string) string {
	profile := ""
	if len(privilegeProfile) > 0 {
		profile = privilegeProfile[0]
	}
	// Server-CA pin: fetch the operator-provided CA bundle from
	// platform_settings[registration.ca_bundle] (same source as the HTTP
	// renderer) and compute its checksum. Empty when no private CA is set, so
	// the agent stays on the default OS-trust path with no behavior change.
	caPEM := registrationCABundleForTask(ctx)
	return agenttemplate.RenderInstallYAML(agenttemplate.InstallTemplateData{
		ServerURL:            serverURL,
		ClusterID:            clusterID,
		RegistrationToken:    agentToken,
		CACert:               caPEM,
		CAChecksum:           agenttemplate.CAChecksumFromPEM(caPEM),
		AgentImage:           agentImageReference(imageRepository, imageTag),
		PrivilegeProfile:     profile,
		SystemArtifactURL:    runtimeDependencies(ctx).SystemArtifactURL,
		SystemArtifactDigest: runtimeDependencies(ctx).SystemArtifactDigest,
		SystemOIDCIssuer:     runtimeDependencies(ctx).SystemOIDCIssuer,
		SystemOIDCIdentity:   runtimeDependencies(ctx).SystemOIDCIdentity,
	})
}

func agentImageReference(repository, tag string) string {
	repository = strings.TrimSpace(repository)
	tag = strings.TrimSpace(tag)
	if strings.Contains(repository, "@sha256:") || tag == "" {
		return repository
	}
	return repository + ":" + tag
}

// registrationCABundleForTask reads platform_settings[registration.ca_bundle]
// via the worker runtime queries. Returns "" when queries are unwired or no CA
// is configured.
func registrationCABundleForTask(ctx context.Context) string {
	if runtimeDependencies(ctx).Queries == nil {
		return ""
	}
	row, err := runtimeDependencies(ctx).Queries.GetPlatformSetting(ctx, "registration.ca_bundle")
	if err != nil || len(row.Value) == 0 {
		return ""
	}
	var pem string
	_ = json.Unmarshal(row.Value, &pem)
	return strings.TrimSpace(pem)
}
