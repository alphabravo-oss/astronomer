package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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

	// TODO: Generate Kubernetes YAML manifest with agent deployment, RBAC, etc.

	slog.InfoContext(ctx, "agent manifest generated", "cluster_id", p.ClusterID)
	return nil
}
