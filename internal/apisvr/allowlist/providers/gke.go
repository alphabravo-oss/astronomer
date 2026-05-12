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

func (p *GKEProvider) Detect(ctx context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderGKE) {
		return ProviderGKE
	}
	return ""
}

// gkeMasterAuthorizedNetworksConfig is the API sub-object we read/write.
// Only the cidrBlocks list is operator-relevant in v1.
type gkeMasterAuthorizedNetworksConfig struct {
	Enabled     bool                  `json:"enabled"`
	CidrBlocks  []gkeCidrBlock        `json:"cidrBlocks"`
}

type gkeCidrBlock struct {
	DisplayName string `json:"displayName,omitempty"`
	CidrBlock   string `json:"cidrBlock"`
}

type gkeClusterResponse struct {
	MasterAuthorizedNetworksConfig gkeMasterAuthorizedNetworksConfig `json:"masterAuthorizedNetworksConfig"`
}

type gkeUpdateRequest struct {
	Update struct {
		DesiredMasterAuthorizedNetworksConfig gkeMasterAuthorizedNetworksConfig `json:"desiredMasterAuthorizedNetworksConfig"`
	} `json:"update"`
}

func (p *GKEProvider) endpoint() string {
	if p.Endpoint != "" {
		return strings.TrimSuffix(p.Endpoint, "/")
	}
	return "https://container.googleapis.com/v1"
}

func (p *GKEProvider) resourcePath(cluster Cluster) (string, error) {
	if cluster.ProjectID == "" {
		return "", fmt.Errorf("GKE project_id required")
	}
	if cluster.Region == "" {
		return "", fmt.Errorf("GKE region required")
	}
	if cluster.Name == "" {
		return "", fmt.Errorf("GKE cluster name required")
	}
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", cluster.ProjectID, cluster.Region, cluster.Name), nil
}

func (p *GKEProvider) signAndSend(ctx context.Context, req *http.Request, cluster Cluster) (*http.Response, error) {
	var creds map[string]string
	if p.Materializer != nil {
		c, err := p.Materializer.ResolveForCluster(ctx, cluster.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve cloud credential: %w", err)
		}
		creds = c
	}
	if p.SigningOverride != nil {
		if err := p.SigningOverride(req, creds); err != nil {
			return nil, err
		}
	}
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

func (p *GKEProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	path, err := p.resourcePath(cluster)
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
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GKE GetCluster %s: status %d: %s", cluster.Name, resp.StatusCode, string(rb))
	}
	var out gkeClusterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("GKE GetCluster decode: %w", err)
	}
	cidrs := make([]string, 0, len(out.MasterAuthorizedNetworksConfig.CidrBlocks))
	for _, b := range out.MasterAuthorizedNetworksConfig.CidrBlocks {
		cidrs = append(cidrs, b.CidrBlock)
	}
	return allowlist.CanonicaliseEffective(cidrs), nil
}

func (p *GKEProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	path, err := p.resourcePath(cluster)
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
	body.Update.DesiredMasterAuthorizedNetworksConfig.Enabled = true
	body.Update.DesiredMasterAuthorizedNetworksConfig.CidrBlocks = make([]gkeCidrBlock, 0, len(desired))
	for _, c := range desired {
		body.Update.DesiredMasterAuthorizedNetworksConfig.CidrBlocks = append(
			body.Update.DesiredMasterAuthorizedNetworksConfig.CidrBlocks,
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
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GKE Update %s: status %d: %s", cluster.Name, resp.StatusCode, string(rb))
	}
	return nil
}
