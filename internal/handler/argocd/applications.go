package argocd

// Phase B1: Application CRUD against upstream ArgoCD.
//
// The upstream API for Application resources:
//   POST   /api/v1/applications              -- create
//   GET    /api/v1/applications/{name}       -- read (already covered by GetApp)
//   PUT    /api/v1/applications/{name}       -- full replace
//   PATCH  /api/v1/applications/{name}       -- partial update (merge JSON)
//   DELETE /api/v1/applications/{name}       -- delete (?cascade=true for app+resources)
//
// Argo CD models the Application as a CRD; the HTTP API wraps the spec in a
// Kubernetes-shaped envelope with metadata/spec/status. We mirror the subset
// our handler needs and accept *Spec only on writes, building the envelope
// here so callers don't have to know about TypeMeta plumbing.

import (
	"context"
	"encoding/json"
	"net/http"
)

// ApplicationSpec is the writable subset of an ArgoCD Application's spec
// section. Names match the upstream JSON shape.
type ApplicationSpec struct {
	Project     string                 `json:"project"`
	Source      *ApplicationSource     `json:"source,omitempty"`
	Sources     []ApplicationSource    `json:"sources,omitempty"`
	Destination *ApplicationDestination `json:"destination,omitempty"`
	SyncPolicy  *SyncPolicy             `json:"syncPolicy,omitempty"`
}

// ApplicationSource is one of the source definitions on an Application.
// Either Helm or Kustomize (or neither for raw manifests) may be set.
type ApplicationSource struct {
	RepoURL        string             `json:"repoURL"`
	Path           string             `json:"path,omitempty"`
	TargetRevision string             `json:"targetRevision,omitempty"`
	Chart          string             `json:"chart,omitempty"`
	Helm           *HelmSource        `json:"helm,omitempty"`
	Kustomize      *KustomizeSource   `json:"kustomize,omitempty"`
	Directory      *DirectorySource   `json:"directory,omitempty"`
}

// HelmSource carries Helm-specific source overrides.
type HelmSource struct {
	ValueFiles  []string         `json:"valueFiles,omitempty"`
	Values      string           `json:"values,omitempty"`
	Parameters  []HelmParameter  `json:"parameters,omitempty"`
	ReleaseName string           `json:"releaseName,omitempty"`
	Version     string           `json:"version,omitempty"`
}

// HelmParameter is a single --set-style override.
type HelmParameter struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	ForceString bool   `json:"forceString,omitempty"`
}

// KustomizeSource carries Kustomize-specific overrides. Kept narrow on purpose.
type KustomizeSource struct {
	NamePrefix string   `json:"namePrefix,omitempty"`
	NameSuffix string   `json:"nameSuffix,omitempty"`
	Images     []string `json:"images,omitempty"`
}

// DirectorySource is a placeholder for raw-manifest directory sources.
type DirectorySource struct {
	Recurse bool `json:"recurse,omitempty"`
}

// ApplicationDestination is where the rendered manifests get applied.
// Server is the K8s API URL of the destination cluster (must match a
// registered ArgoCD cluster) OR `Name` may be supplied instead.
type ApplicationDestination struct {
	Server    string `json:"server,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// SyncPolicy mirrors ArgoCD's syncPolicy block.
type SyncPolicy struct {
	Automated   *SyncPolicyAutomated `json:"automated,omitempty"`
	SyncOptions []string             `json:"syncOptions,omitempty"`
}

// SyncPolicyAutomated toggles auto-sync / self-heal / prune.
type SyncPolicyAutomated struct {
	Prune    bool `json:"prune,omitempty"`
	SelfHeal bool `json:"selfHeal,omitempty"`
}

// applicationEnvelope is the Kubernetes-shaped wrapper ArgoCD expects on
// create/patch/replace. Status is omitted on writes — the server fills it in.
type applicationEnvelope struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ApplicationMetadata `json:"metadata"`
	Spec       ApplicationSpec   `json:"spec"`
}

// ApplicationMetadata is the minimal metadata we expose on writes.
//
// ResourceVersion is required by ArgoCD on PUT-style updates for optimistic
// concurrency (the upstream rejects updates without it: "metadata.resourceVersion:
// must be specified for an update"). Callers performing read-modify-write
// should round-trip the value from a prior GET.
type ApplicationMetadata struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

// CreateApplication POSTs a new Application to upstream ArgoCD.
//
// Spec.Project should be one of the AppProjects on the instance ("default" if
// the caller hasn't created one). Either Spec.Source or Spec.Sources must be
// populated; the upstream rejects empty bodies.
func (c *Client) CreateApplication(ctx context.Context, name string, spec ApplicationSpec) (*Application, error) {
	body, err := json.Marshal(applicationEnvelope{
		APIVersion: "argoproj.io/v1alpha1",
		Kind:       "Application",
		Metadata:   ApplicationMetadata{Name: name},
		Spec:       spec,
	})
	if err != nil {
		return nil, err
	}
	var out Application
	if err := c.do(ctx, http.MethodPost, "/api/v1/applications", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchApplication applies a JSON merge patch to an existing Application.
// Pass only the fields you want to change inside the spec — the upstream
// merges them onto the existing Application. mergeBody is taken as-is so
// callers can express deletions via explicit nulls.
func (c *Client) PatchApplication(ctx context.Context, name string, mergeBody json.RawMessage) (*Application, error) {
	if len(mergeBody) == 0 {
		mergeBody = json.RawMessage(`{}`)
	}
	// ArgoCD's PATCH endpoint expects a JSON envelope: {"name":"...","patch":"<json-string>","patchType":"merge"}.
	// We accept the convenient shape (a real JSON object) and serialize it
	// into the string field the upstream actually parses.
	patch := struct {
		Name      string `json:"name"`
		Patch     string `json:"patch"`
		PatchType string `json:"patchType"`
	}{
		Name:      name,
		Patch:     string(mergeBody),
		PatchType: "merge",
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	var out Application
	if err := c.do(ctx, http.MethodPatch, "/api/v1/applications/"+name, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteApplication removes the Application from upstream ArgoCD. When
// cascade is true, ArgoCD will also delete the resources it deployed
// (`?cascade=true`); when false, only the Application CRD is removed.
func (c *Client) DeleteApplication(ctx context.Context, name string, cascade bool) error {
	path := "/api/v1/applications/" + name
	if cascade {
		path += "?cascade=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
