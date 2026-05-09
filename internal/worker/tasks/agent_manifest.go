package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"text/template"

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
	tpl := template.Must(template.New("agent-manifest").Parse(agentManifestTemplate))
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return err
	}

	slog.InfoContext(ctx, "agent manifest generated", "cluster_id", p.ClusterID, "manifest_bytes", rendered.Len())
	return nil
}

const agentManifestTemplate = `apiVersion: v1
kind: Namespace
metadata:
  name: astronomer-system
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent
  namespace: astronomer-system
stringData:
  AGENT_TOKEN: {{ .AgentToken | printf "%q" }}
  CLUSTER_ID: {{ .ClusterID | printf "%q" }}
  SERVER_URL: {{ .ServerURL | printf "%q" }}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: astronomer-agent
  namespace: astronomer-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: astronomer-agent
  template:
    metadata:
      labels:
        app: astronomer-agent
    spec:
      containers:
      - name: agent
        image: {{ printf "%s:%s" .ImageRepository .ImageTag | printf "%q" }}
        envFrom:
        - secretRef:
            name: astronomer-agent
`
