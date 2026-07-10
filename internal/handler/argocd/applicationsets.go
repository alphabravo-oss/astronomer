package argocd

// Phase B1: ApplicationSet CRUD.
//
//   POST   /api/v1/applicationsets               -- create
//   GET    /api/v1/applicationsets/{name}        -- read
//   PUT    /api/v1/applicationsets/{name}        -- replace (no PATCH upstream)
//   DELETE /api/v1/applicationsets/{name}        -- delete
//
// ApplicationSet is the fan-out primitive: a generator emits a list of
// {parameters} and a template renders one Application per row. Critical for
// "deploy this Helm release to every prod cluster" workflows.

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/argosecurity"
)

// ApplicationSetSpec is the writable subset of an ApplicationSet.
type ApplicationSetSpec struct {
	Generators []ApplicationSetGenerator `json:"generators"`
	Template   ApplicationSetTemplate    `json:"template"`
	// SyncPolicy on the *set* governs whether to delete generated apps when
	// the set is deleted ("preserveResourcesOnDeletion").
	SyncPolicy *ApplicationSetSyncPolicy `json:"syncPolicy,omitempty"`
}

// ApplicationSetGenerator is a discriminated union — exactly one field
// should be set per element. ArgoCD evaluates them in order.
type ApplicationSetGenerator struct {
	List    *ListGenerator    `json:"list,omitempty"`
	Cluster *ClusterGenerator `json:"clusters,omitempty"`
	Git     *GitGenerator     `json:"git,omitempty"`
	Matrix  *MatrixGenerator  `json:"matrix,omitempty"`
}

// ListGenerator emits one Application per element. Each element is a
// freeform key/value bag passed into the template as `.{{key}}`.
type ListGenerator struct {
	Elements []json.RawMessage `json:"elements"`
}

// ClusterGenerator emits one Application per cluster Secret in the argocd
// namespace, optionally filtered by a label selector. This is the
// integration point for our `argocd_managed_clusters` registration.
type ClusterGenerator struct {
	// Selector is a Kubernetes label selector against the cluster Secret labels.
	Selector *LabelSelector `json:"selector,omitempty"`
	// Values are extra static parameters merged into each generated row.
	Values map[string]string `json:"values,omitempty"`
}

// LabelSelector is the same shape as metav1.LabelSelector but kept local
// to avoid a kubernetes/api dependency in this small package.
type LabelSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// LabelSelectorRequirement is one expression in a selector.
type LabelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"` // In, NotIn, Exists, DoesNotExist
	Values   []string `json:"values,omitempty"`
}

// GitGenerator emits one Application per file or directory in a git repo.
// Exactly one of Files / Directories should be populated.
type GitGenerator struct {
	RepoURL     string                  `json:"repoURL"`
	Revision    string                  `json:"revision,omitempty"`
	Files       []GitGeneratorItem      `json:"files,omitempty"`
	Directories []GitGeneratorDirectory `json:"directories,omitempty"`
}

// GitGeneratorItem matches a file path inside the repo.
type GitGeneratorItem struct {
	Path string `json:"path"`
}

// GitGeneratorDirectory matches a directory path inside the repo.
type GitGeneratorDirectory struct {
	Path    string `json:"path"`
	Exclude bool   `json:"exclude,omitempty"`
}

// MatrixGenerator is a cross product of two child generators. ArgoCD
// supports nested matrix only one level deep.
type MatrixGenerator struct {
	Generators []ApplicationSetGenerator `json:"generators"`
}

// ApplicationSetTemplate is the per-row Application rendered by the set.
// Metadata.Name and Spec are templated against generator parameters
// (`{{name}}` etc).
type ApplicationSetTemplate struct {
	Metadata ApplicationMetadata `json:"metadata"`
	Spec     ApplicationSpec     `json:"spec"`
}

// ApplicationSetSyncPolicy controls deletion behavior.
type ApplicationSetSyncPolicy struct {
	PreserveResourcesOnDeletion bool `json:"preserveResourcesOnDeletion,omitempty"`
}

// ApplicationSet is the read-side projection.
type ApplicationSet struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace,omitempty"`
	} `json:"metadata"`
	Spec ApplicationSetSpec `json:"spec"`
}

// applicationSetEnvelope is the create body. ArgoCD's applicationset HTTP
// handler accepts a raw object (with TypeMeta) — no `applicationset` key.
type applicationSetEnvelope struct {
	APIVersion string              `json:"apiVersion,omitempty"`
	Kind       string              `json:"kind,omitempty"`
	Metadata   ApplicationMetadata `json:"metadata"`
	Spec       ApplicationSetSpec  `json:"spec"`
}

// CreateApplicationSet creates an ApplicationSet upstream.
func (c *Client) CreateApplicationSet(ctx context.Context, name string, spec ApplicationSetSpec) (*ApplicationSet, error) {
	if err := argosecurity.ValidateMutation(map[string]any{"spec": spec}); err != nil {
		return nil, err
	}
	body, err := json.Marshal(applicationSetEnvelope{
		APIVersion: "argoproj.io/v1alpha1",
		Kind:       "ApplicationSet",
		Metadata:   ApplicationMetadata{Name: name},
		Spec:       spec,
	})
	if err != nil {
		return nil, err
	}
	var out ApplicationSet
	if err := c.do(ctx, http.MethodPost, "/api/v1/applicationsets", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetApplicationSet reads an ApplicationSet by name.
func (c *Client) GetApplicationSet(ctx context.Context, name string) (*ApplicationSet, error) {
	var out ApplicationSet
	if err := c.do(ctx, http.MethodGet, "/api/v1/applicationsets/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteApplicationSet removes an ApplicationSet upstream. Whether ArgoCD
// also deletes the generated Applications is governed by the set's
// SyncPolicy.PreserveResourcesOnDeletion.
func (c *Client) DeleteApplicationSet(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/applicationsets/"+name, nil, nil)
}
