package argocd

// Phase B1: repo credential management.
//
//   POST   /api/v1/repositories                  -- create
//   GET    /api/v1/repositories                  -- list
//   GET    /api/v1/repositories/{repoUrl}        -- read by URL (URL-encoded)
//   DELETE /api/v1/repositories/{repoUrl}        -- delete
//   POST   /api/v1/repositories/{repoUrl}/test   -- connection test
//
// ArgoCD stores repo creds as Secrets in the argocd namespace. The HTTP API
// accepts a flat object with the cred fields inline; on read it scrubs them.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/alphabravocompany/astronomer-go/internal/argosecurity"
)

// Repository is the projection ArgoCD returns for a repo. Secrets such as
// password / sshPrivateKey are *not* returned on reads.
type Repository struct {
	Repo            string `json:"repo"`
	Name            string `json:"name,omitempty"`
	Type            string `json:"type,omitempty"` // "git" or "helm"
	Username        string `json:"username,omitempty"`
	Insecure        bool   `json:"insecure,omitempty"`
	EnableLFS       bool   `json:"enableLfs,omitempty"`
	Project         string `json:"project,omitempty"`
	ConnectionState struct {
		Status      string `json:"status,omitempty"`
		Message     string `json:"message,omitempty"`
		AttemptedAt string `json:"attemptedAt,omitempty"`
	} `json:"connectionState,omitempty"`
}

// RepositoryCreate is the writeable shape — union of git + helm fields.
// Empty fields are omitted from the JSON body.
type RepositoryCreate struct {
	Repo          string `json:"repo"`
	Name          string `json:"name,omitempty"`
	Type          string `json:"type,omitempty"` // "git" or "helm"; default git
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	SSHPrivateKey string `json:"sshPrivateKey,omitempty"`
	TLSClientCert string `json:"tlsClientCertData,omitempty"`
	TLSClientKey  string `json:"tlsClientCertKey,omitempty"`
	Insecure      bool   `json:"insecure,omitempty"`
	EnableLFS     bool   `json:"enableLfs,omitempty"`
	Project       string `json:"project,omitempty"`
}

// CreateRepository registers a repo (git or helm) with upstream ArgoCD.
func (c *Client) CreateRepository(ctx context.Context, repo RepositoryCreate) (*Repository, error) {
	if err := argosecurity.ValidateCredentialFreeURL(repo.Repo); err != nil {
		return nil, err
	}
	body, err := json.Marshal(repo)
	if err != nil {
		return nil, err
	}
	var out Repository
	if err := c.do(ctx, http.MethodPost, "/api/v1/repositories", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRepositories returns all repos registered with ArgoCD.
func (c *Client) ListRepositories(ctx context.Context) ([]Repository, error) {
	var resp struct {
		Items []Repository `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/repositories", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// DeleteRepository removes a repo by its URL.
func (c *Client) DeleteRepository(ctx context.Context, repoURL string) error {
	if err := argosecurity.ValidateCredentialFreeURL(repoURL); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/api/v1/repositories/"+url.PathEscape(repoURL), nil, nil)
}

// TestRepository asks upstream ArgoCD to validate the credentials by
// performing a quick connectivity check. The response carries
// ConnectionState.Status ("Successful" or "Failed").
func (c *Client) TestRepository(ctx context.Context, repo RepositoryCreate) (*Repository, error) {
	if err := argosecurity.ValidateCredentialFreeURL(repo.Repo); err != nil {
		return nil, err
	}
	body, err := json.Marshal(repo)
	if err != nil {
		return nil, err
	}
	var out Repository
	// /api/v1/repositories/{repoUrl}/validate is the modern endpoint;
	// older ArgoCD uses /repositories/test. We hit /validate.
	path := "/api/v1/repositories/" + url.PathEscape(repo.Repo) + "/validate"
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
