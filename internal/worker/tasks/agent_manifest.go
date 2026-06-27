package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// NewAgentManifestTask creates a new agent manifest generation task.
func NewAgentManifestTask(payload AgentManifestPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal agent manifest payload: %w", err)
	}
	return asynq.NewTask("agent:generate_manifest", data), nil
}

// HandleAgentManifest generates a Kubernetes deployment manifest for the agent.
func HandleAgentManifest(ctx context.Context, t *asynq.Task) error {
	var p AgentManifestPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal agent manifest payload: %w", err)
	}

	if p.ClusterID == "" {
		return fmt.Errorf("cluster_id is required")
	}

	slog.InfoContext(ctx, "generating agent manifest",
		"cluster_id", p.ClusterID,
		"image_repository", p.ImageRepository,
		"image_tag", p.ImageTag,
		"privilege_profile", agenttemplate.NormalizePrivilegeProfile(p.PrivilegeProfile),
	)

	data := struct {
		ClusterID        string
		AgentToken       string
		ImageRepository  string
		ImageTag         string
		ServerURL        string
		PrivilegeProfile string
	}{
		ClusterID:        p.ClusterID,
		AgentToken:       p.AgentToken,
		ImageRepository:  p.ImageRepository,
		ImageTag:         p.ImageTag,
		ServerURL:        runtimeDeps.ServerURL,
		PrivilegeProfile: p.PrivilegeProfile,
	}
	if data.ImageRepository == "" {
		data.ImageRepository = runtimeDeps.AgentImageRepo
	}
	if data.ImageTag == "" {
		data.ImageTag = runtimeDeps.AgentImageTag
	}
	rendered := renderAgentManifest(ctx, data.ClusterID, data.AgentToken, data.ServerURL, data.ImageRepository, data.ImageTag, data.PrivilegeProfile)

	slog.InfoContext(ctx, "agent manifest generated", "cluster_id", p.ClusterID, "manifest_bytes", len(rendered))
	return nil
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
		ServerURL:         serverURL,
		ClusterID:         clusterID,
		RegistrationToken: agentToken,
		CACert:            caPEM,
		CAChecksum:        agenttemplate.CAChecksumFromPEM(caPEM),
		AgentImage:        imageRepository + ":" + imageTag,
		PrivilegeProfile:  profile,
	})
}

// registrationCABundleForTask reads platform_settings[registration.ca_bundle]
// via the worker runtime queries. Returns "" when queries are unwired or no CA
// is configured.
func registrationCABundleForTask(ctx context.Context) string {
	if runtimeDeps.Queries == nil {
		return ""
	}
	row, err := runtimeDeps.Queries.GetPlatformSetting(ctx, "registration.ca_bundle")
	if err != nil || len(row.Value) == 0 {
		return ""
	}
	var pem string
	_ = json.Unmarshal(row.Value, &pem)
	return strings.TrimSpace(pem)
}
