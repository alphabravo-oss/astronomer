package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GKEProvider patches a GKE cluster's master-authorized-networks-config
// field using the GKE REST API. Same shape as EKS — the REST endpoint
// is injectable so tests stub it with httptest.
type GKEProvider struct {
	HTTPClient      *http.Client
	Endpoint        string // e.g. "https://container.googleapis.com/v1"
	Materializer    CloudCredentialMaterializer
	SigningOverride func(req *http.Request, creds map[string]string) error
}

func NewGKEProvider(m CloudCredentialMaterializer) *GKEProvider {
	return &GKEProvider{
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		Materializer: m,
	}
}

func (p *GKEProvider) ID() ProviderID { return ProviderGKE }

func (p *GKEProvider) Capability() Capability {
	return Capability{Provider: ProviderGKE, CanMonitor: true, CanEnforce: true, RequiredMetadata: []string{"name", "region", "project_id", "cloud credential"}}
}

func (p *GKEProvider) Detect(ctx context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderGKE) {
		return ProviderGKE
	}
	return ""
}

// gkeMasterAuthorizedNetworksConfig is the API sub-object we read/write.
// Only the cidrBlocks list is operator-relevant in v1.
type gkeMasterAuthorizedNetworksConfig struct {
	Enabled    bool           `json:"enabled"`
	CidrBlocks []gkeCidrBlock `json:"cidrBlocks"`
}

type gkeCidrBlock struct {
	DisplayName string `json:"displayName,omitempty"`
	CidrBlock   string `json:"cidrBlock"`
}

type gkeClusterResponse struct {
	MasterAuthorizedNetworksConfig gkeMasterAuthorizedNetworksConfig `json:"masterAuthorizedNetworksConfig"`
	ControlPlaneEndpointsConfig    struct {
		IPEndpointsConfig struct {
			AuthorizedNetworksConfig gkeMasterAuthorizedNetworksConfig `json:"authorizedNetworksConfig"`
		} `json:"ipEndpointsConfig"`
	} `json:"controlPlaneEndpointsConfig"`
}

type gkeUpdateRequest struct {
	Update struct {
		DesiredControlPlaneEndpointsConfig struct {
			IPEndpointsConfig struct {
				AuthorizedNetworksConfig gkeMasterAuthorizedNetworksConfig `json:"authorizedNetworksConfig"`
			} `json:"ipEndpointsConfig"`
		} `json:"desiredControlPlaneEndpointsConfig"`
	} `json:"update"`
}

func (p *GKEProvider) endpoint() string {
	if p.Endpoint != "" {
		return strings.TrimSuffix(p.Endpoint, "/")
	}
	return "https://container.googleapis.com/v1"
}

func (p *GKEProvider) resourcePath(ctx context.Context, cluster Cluster) (string, error) {
	projectID := strings.TrimSpace(cluster.ProjectID)
	if projectID == "" && p.Materializer != nil {
		creds, err := p.Materializer.ResolveForCluster(ctx, cluster, ProviderGKE)
		if err != nil {
			return "", fmt.Errorf("resolve GKE project_id: %w", err)
		}
		projectID = strings.TrimSpace(creds["project_id"])
		if projectID == "" {
			var serviceAccount struct {
				ProjectID string `json:"project_id"`
			}
			_ = json.Unmarshal([]byte(creds["service_account_json"]), &serviceAccount)
			projectID = strings.TrimSpace(serviceAccount.ProjectID)
		}
	}
	if projectID == "" {
		return "", fmt.Errorf("GKE project_id required in cluster metadata or credential")
	}
	if cluster.Region == "" {
		return "", fmt.Errorf("GKE region required")
	}
	if cluster.Name == "" {
		return "", fmt.Errorf("GKE cluster name required")
	}
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, cluster.Region, cluster.Name), nil
}

func (p *GKEProvider) signAndSend(ctx context.Context, req *http.Request, cluster Cluster) (*http.Response, error) {
	var creds map[string]string
	if p.Materializer != nil {
		c, err := p.Materializer.ResolveForCluster(ctx, cluster, ProviderGKE)
		if err != nil {
			return nil, fmt.Errorf("resolve cloud credential: %w", err)
		}
		creds = c
	}
	if p.SigningOverride != nil {
		if err := p.SigningOverride(req, creds); err != nil {
			return nil, err
		}
	} else {
		if p.Materializer == nil {
			return nil, fmt.Errorf("GKE credential materializer is not configured")
		}
		serviceAccountJSON := strings.TrimSpace(creds["service_account_json"])
		if serviceAccountJSON == "" {
			return nil, fmt.Errorf("GKE credential requires service_account_json")
		}
		config, err := google.JWTConfigFromJSON([]byte(serviceAccountJSON), "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, fmt.Errorf("parse GKE service account credential: %w", err)
		}
		baseClient := p.HTTPClient
		if baseClient == nil {
			baseClient = httpclient.DefaultExternal()
		}
		tokenCtx := context.WithValue(ctx, oauth2.HTTPClient, baseClient)
		token, err := config.TokenSource(tokenCtx).Token()
		if err != nil {
			return nil, fmt.Errorf("acquire GKE OAuth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	}
	client := p.HTTPClient
	if client == nil {
		client = httpclient.DefaultExternal()
	}
	return client.Do(req)
}

func (p *GKEProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	path, err := p.resourcePath(ctx, cluster)
	if err != nil {
		return nil, err
	}
	url := p.endpoint() + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.signAndSend(ctx, req, cluster)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
		return nil, &HTTPError{Provider: ProviderGKE, Operation: "get_cluster", StatusCode: resp.StatusCode, Body: string(rb), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var out gkeClusterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("GKE GetCluster decode: %w", err)
	}
	config := out.ControlPlaneEndpointsConfig.IPEndpointsConfig.AuthorizedNetworksConfig
	if len(config.CidrBlocks) == 0 && len(out.MasterAuthorizedNetworksConfig.CidrBlocks) > 0 {
		config = out.MasterAuthorizedNetworksConfig
	}
	cidrs := make([]string, 0, len(config.CidrBlocks))
	for _, b := range config.CidrBlocks {
		cidrs = append(cidrs, b.CidrBlock)
	}
	return allowlist.CanonicaliseEffective(cidrs), nil
}

func (p *GKEProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	path, err := p.resourcePath(ctx, cluster)
	if err != nil {
		return err
	}
	effective, err := p.GetEffective(ctx, cluster)
	if err != nil {
		return fmt.Errorf("GetEffective before apply: %w", err)
	}
	desired := allowlist.CanonicaliseEffective(cidrs)
	if allowlist.SameSet(effective, desired) {
		return nil
	}

	body := gkeUpdateRequest{}
	config := &body.Update.DesiredControlPlaneEndpointsConfig.IPEndpointsConfig.AuthorizedNetworksConfig
	config.Enabled = true
	config.CidrBlocks = make([]gkeCidrBlock, 0, len(desired))
	for _, c := range desired {
		config.CidrBlocks = append(
			config.CidrBlocks,
			gkeCidrBlock{CidrBlock: c, DisplayName: "astronomer-managed"},
		)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := p.endpoint() + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.signAndSend(ctx, req, cluster)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
		return &HTTPError{Provider: ProviderGKE, Operation: "update_cluster", StatusCode: resp.StatusCode, Body: string(rb), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil
}
