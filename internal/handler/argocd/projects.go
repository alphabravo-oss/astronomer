package argocd

// Phase B1: AppProject CRUD against upstream ArgoCD.
//
//   POST   /api/v1/projects              -- create
//   GET    /api/v1/projects/{name}       -- read
//   PUT    /api/v1/projects/{name}       -- replace (upstream rejects PATCH)
//   DELETE /api/v1/projects/{name}       -- delete
//
// Note: ArgoCD's project endpoint only supports PUT for updates; PATCH
// returns 405. UpdateProject reads the current project, applies the
// caller-supplied spec on top, and writes back via PUT.
//
// AppProjects are the multi-tenant guardrail in ArgoCD — they constrain
// which repos and destinations Applications inside the project may target,
// and which Kubernetes resource kinds the project may render.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/argosecurity"
)

// AppProjectSpec is the writable subset of an AppProject.
type AppProjectSpec struct {
	Description                string                   `json:"description,omitempty"`
	SourceRepos                []string                 `json:"sourceRepos,omitempty"`
	Destinations               []ApplicationDestination `json:"destinations,omitempty"`
	ClusterResourceWhitelist   []GroupKind              `json:"clusterResourceWhitelist,omitempty"`
	NamespaceResourceWhitelist []GroupKind              `json:"namespaceResourceWhitelist,omitempty"`
	ClusterResourceBlacklist   []GroupKind              `json:"clusterResourceBlacklist,omitempty"`
	NamespaceResourceBlacklist []GroupKind              `json:"namespaceResourceBlacklist,omitempty"`
	Roles                      []AppProjectRole         `json:"roles,omitempty"`
	SyncWindows                []AppProjectSyncWindow   `json:"syncWindows,omitempty"`
}

// GroupKind is the {group, kind} pair ArgoCD uses for resource whitelists.
type GroupKind struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
}

// AppProjectRole is a named bundle of policies + groups for the project.
type AppProjectRole struct {
	Name     string   `json:"name"`
	Policies []string `json:"policies,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// AppProjectSyncWindow is ArgoCD's AppProject-level sync window model.
// Windows can be allow/deny policies over applications, namespaces, and
// clusters using cron schedules and Go-style durations such as "1h".
type AppProjectSyncWindow struct {
	Kind           string   `json:"kind"`
	Schedule       string   `json:"schedule"`
	Duration       string   `json:"duration"`
	Applications   []string `json:"applications,omitempty"`
	Namespaces     []string `json:"namespaces,omitempty"`
	Clusters       []string `json:"clusters,omitempty"`
	ManualSync     bool     `json:"manualSync,omitempty"`
	SyncOverrun    bool     `json:"syncOverrun,omitempty"`
	TimeZone       string   `json:"timeZone,omitempty"`
	UseAndOperator bool     `json:"useAndOperator,omitempty"`
	Description    string   `json:"description,omitempty"`
}

// AppProject is the projection returned by GET / created by POST.
type AppProject struct {
	Metadata struct {
		Name            string `json:"name"`
		Namespace       string `json:"namespace,omitempty"`
		ResourceVersion string `json:"resourceVersion,omitempty"`
	} `json:"metadata"`
	Spec AppProjectSpec `json:"spec"`
}

// projectCreateEnvelope is the upstream's expected POST body — a wrapper
// `{"project": {"metadata": {...}, "spec": {...}}}` with TypeMeta on the
// inner object.
type projectCreateEnvelope struct {
	Project struct {
		APIVersion string              `json:"apiVersion,omitempty"`
		Kind       string              `json:"kind,omitempty"`
		Metadata   ApplicationMetadata `json:"metadata"`
		Spec       AppProjectSpec      `json:"spec"`
	} `json:"project"`
}

// CreateProject creates an AppProject upstream.
func (c *Client) CreateProject(ctx context.Context, name string, spec AppProjectSpec) (*AppProject, error) {
	if err := validateProjectURLs(spec); err != nil {
		return nil, err
	}
	var env projectCreateEnvelope
	env.Project.APIVersion = "argoproj.io/v1alpha1"
	env.Project.Kind = "AppProject"
	env.Project.Metadata = ApplicationMetadata{Name: name}
	env.Project.Spec = spec
	body, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	var out AppProject
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProject replaces an AppProject's spec via the upstream PUT endpoint.
// ArgoCD's project API does not accept PATCH (returns 405), so callers that
// want partial updates supply a fully-formed spec; we wrap it in the upstream
// {project: {metadata, spec}} envelope.
//
// resourceVersion is required by upstream for optimistic concurrency on
// updates — pass the value from a prior GetProject.
//
// PatchProject is preserved as a deprecated alias that delegates to
// UpdateProject; existing callers don't need to change.
func (c *Client) UpdateProject(ctx context.Context, name, resourceVersion string, spec AppProjectSpec) (*AppProject, error) {
	if err := validateProjectURLs(spec); err != nil {
		return nil, err
	}
	var env projectCreateEnvelope
	env.Project.APIVersion = "argoproj.io/v1alpha1"
	env.Project.Kind = "AppProject"
	env.Project.Metadata = ApplicationMetadata{Name: name, ResourceVersion: resourceVersion}
	env.Project.Spec = spec
	body, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	var out AppProject
	if err := c.do(ctx, http.MethodPut, "/api/v1/projects/"+name, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func validateProjectURLs(spec AppProjectSpec) error {
	for i, repo := range spec.SourceRepos {
		if err := argosecurity.ValidateSourceRepoPattern(repo); err != nil {
			return fmt.Errorf("sourceRepos[%d] must be a canonical credential-free repository pattern", i)
		}
	}
	for i, destination := range spec.Destinations {
		if destination.Server != "" && destination.Server != "*" {
			if err := argosecurity.ValidateCredentialFreeURL(destination.Server); err != nil {
				return fmt.Errorf("destinations[%d].server must be a canonical credential-free URL", i)
			}
		}
	}
	return nil
}

// PatchProject is a back-compat shim around UpdateProject. The mergeBody is
// applied on top of the project's current spec by reading + merging client-
// side, then writing back via PUT (carrying the resourceVersion from the GET).
func (c *Client) PatchProject(ctx context.Context, name string, mergeBody json.RawMessage) (*AppProject, error) {
	current, err := c.GetProject(ctx, name)
	if err != nil {
		return nil, err
	}
	rv := current.Metadata.ResourceVersion
	if len(mergeBody) == 0 {
		return c.UpdateProject(ctx, name, rv, current.Spec)
	}
	specJSON, err := json.Marshal(current.Spec)
	if err != nil {
		return nil, err
	}
	merged, err := mergeJSON(specJSON, []byte(mergeBody))
	if err != nil {
		return nil, err
	}
	var spec AppProjectSpec
	if err := json.Unmarshal(merged, &spec); err != nil {
		return nil, err
	}
	return c.UpdateProject(ctx, name, rv, spec)
}

// mergeJSON applies a JSON merge patch (RFC 7396 semantics, dest fields win)
// onto base. Used by PatchProject to keep the public API stable while the
// upstream forces us to PUT a full document.
func mergeJSON(base, patch []byte) ([]byte, error) {
	var b, p map[string]any
	if err := json.Unmarshal(base, &b); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, err
	}
	mergeMap(b, p)
	return json.Marshal(b)
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		if subMap, ok := v.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				mergeMap(existing, subMap)
				continue
			}
		}
		dst[k] = v
	}
}

// DeleteProject removes an AppProject upstream.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/projects/"+name, nil, nil)
}

// GetProject reads an AppProject by name.
func (c *Client) GetProject(ctx context.Context, name string) (*AppProject, error) {
	var out AppProject
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
