package argocd

// Phase B1: register clusters into upstream ArgoCD.
//
//   POST   /api/v1/clusters             -- register
//   GET    /api/v1/clusters             -- list
//   GET    /api/v1/clusters/{server}    -- read by server URL (URL-encoded)
//   DELETE /api/v1/clusters/{server}    -- unregister
//
// Internally ArgoCD stores cluster credentials as a Secret labelled
// `argocd.argoproj.io/secret-type: cluster` in the argocd namespace. The HTTP
// API hides that and accepts a flat object with the credentials inline.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// ClusterConfig carries the credentials embedded in an ArgoCD Cluster.
type ClusterConfig struct {
	BearerToken     string           `json:"bearerToken,omitempty"`
	Username        string           `json:"username,omitempty"`
	Password        string           `json:"password,omitempty"`
	TLSClientConfig *TLSClientConfig `json:"tlsClientConfig,omitempty"`
	// AWSAuthConfig / ExecProviderConfig omitted — out of scope for B1.
}

// TLSClientConfig is the TLS material for verifying / authenticating to the
// destination Kubernetes API server.
type TLSClientConfig struct {
	Insecure   bool   `json:"insecure,omitempty"`
	ServerName string `json:"serverName,omitempty"`
	CertData   []byte `json:"certData,omitempty"`
	KeyData    []byte `json:"keyData,omitempty"`
	CAData     []byte `json:"caData,omitempty"`
}

// ClusterRegistration is the request body for POST /api/v1/clusters.
type ClusterRegistration struct {
	// Server is the K8s API URL ArgoCD will dial to talk to the cluster.
	Server string `json:"server"`
	// Name is a human-friendly identifier; ArgoCD destinations may target
	// either Server or Name.
	Name string `json:"name,omitempty"`
	// Config carries the credentials.
	Config ClusterConfig `json:"config"`
	// Namespaces, when non-empty, restricts which namespaces ArgoCD will
	// manage on this cluster (default: all).
	Namespaces []string `json:"namespaces,omitempty"`
	// Labels are stamped onto the upstream Cluster Secret. The
	// ApplicationSet `cluster` generator's selector matches against these.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations stamped onto the upstream Cluster Secret.
	Annotations map[string]string `json:"annotations,omitempty"`
	// ProjectName scopes the cluster to a single AppProject (optional).
	Project string `json:"project,omitempty"`
	// Upsert tells ArgoCD to update an existing registration when the cluster
	// already exists with different credentials or labels.
	Upsert bool `json:"-"`
}

// Cluster is the projection ArgoCD returns for a registered cluster.
// Sensitive credentials are *not* returned (config.bearerToken is "" on
// reads); we re-send them on every update.
type Cluster struct {
	Name        string            `json:"name"`
	Server      string            `json:"server"`
	Namespaces  []string          `json:"namespaces,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Project     string            `json:"project,omitempty"`
	ConnectionState struct {
		Status     string `json:"status,omitempty"`
		Message    string `json:"message,omitempty"`
		AttemptedAt string `json:"attemptedAt,omitempty"`
	} `json:"connectionState,omitempty"`
}

// RegisterCluster registers a Kubernetes cluster into upstream ArgoCD.
// The upstream returns the registered Cluster shape on success.
func (c *Client) RegisterCluster(ctx context.Context, reg ClusterRegistration) (*Cluster, error) {
	body, err := json.Marshal(reg)
	if err != nil {
		return nil, err
	}
	path := "/api/v1/clusters"
	if reg.Upsert {
		path += "?upsert=true"
	}
	var out Cluster
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCluster reads a registered cluster by its server URL. The server URL
// must be URL-encoded into the path.
func (c *Client) GetCluster(ctx context.Context, server string) (*Cluster, error) {
	var out Cluster
	if err := c.do(ctx, http.MethodGet, "/api/v1/clusters/"+url.PathEscape(server), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnregisterCluster removes a registered cluster from upstream ArgoCD by
// its server URL.
func (c *Client) UnregisterCluster(ctx context.Context, server string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/clusters/"+url.PathEscape(server), nil, nil)
}

// ListClusters returns all clusters registered with ArgoCD.
func (c *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	var resp struct {
		Items []Cluster `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/clusters", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}
