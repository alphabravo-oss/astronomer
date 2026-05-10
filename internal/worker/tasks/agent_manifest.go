package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/hibiken/asynq"
)

// AgentManifestPayload contains parameters for generating an agent manifest.
type AgentManifestPayload struct {
	ClusterID       string `json:"cluster_id"`
	AgentToken      string `json:"agent_token"`
	ImageRepository string `json:"image_repository,omitempty"`
	ImageTag        string `json:"image_tag,omitempty"`
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
	)

	data := struct {
		ClusterID       string
		AgentToken      string
		ImageRepository string
		ImageTag        string
		ServerURL       string
	}{
		ClusterID:       p.ClusterID,
		AgentToken:      p.AgentToken,
		ImageRepository: p.ImageRepository,
		ImageTag:        p.ImageTag,
		ServerURL:       runtimeDeps.ServerURL,
	}
	if data.ImageRepository == "" {
		data.ImageRepository = runtimeDeps.AgentImageRepo
	}
	if data.ImageTag == "" {
		data.ImageTag = runtimeDeps.AgentImageTag
	}
	rendered := renderAgentManifest(data.ClusterID, data.AgentToken, data.ServerURL, data.ImageRepository, data.ImageTag)

	slog.InfoContext(ctx, "agent manifest generated", "cluster_id", p.ClusterID, "manifest_bytes", len(rendered))
	return nil
}

func renderAgentManifest(clusterID, agentToken, serverURL, imageRepository, imageTag string) string {
	return agenttemplate.RenderInstallYAML(agenttemplate.InstallTemplateData{
		ServerURL:         serverURL,
		ClusterID:         clusterID,
		RegistrationToken: agentToken,
		CACert:            "",
		AgentImage:        imageRepository + ":" + imageTag,
	})
}
