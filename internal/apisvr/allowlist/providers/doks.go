package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// DOKSProvider reconciles DigitalOcean's control_plane_firewall object.
type DOKSProvider struct {
	HTTPClient   *http.Client
	Endpoint     string
	Materializer CloudCredentialMaterializer
	AuthOverride func(*http.Request, map[string]string) error
}

func NewDOKSProvider(m CloudCredentialMaterializer) *DOKSProvider {
	return &DOKSProvider{HTTPClient: httpclient.SafeClient(30 * time.Second), Materializer: m}
}

func (p *DOKSProvider) ID() ProviderID { return ProviderDOKS }

func (p *DOKSProvider) Capability() Capability {
	return Capability{Provider: ProviderDOKS, CanMonitor: true, CanEnforce: true, RequiredMetadata: []string{"provider_cluster_id", "cloud credential"}}
}

func (p *DOKSProvider) Detect(_ context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderDOKS) {
		return ProviderDOKS
	}
	return ""
}

func (p *DOKSProvider) endpoint() string {
	if p.Endpoint != "" {
		return strings.TrimSuffix(p.Endpoint, "/")
	}
	return "https://api.digitalocean.com/v2"
}

func (p *DOKSProvider) resourceID(cluster Cluster) (string, error) {
	id := strings.TrimSpace(cluster.ProviderResourceID)
	if id == "" {
		id = strings.TrimSpace(cluster.Annotations["digitalocean.com/cluster-id"])
	}
	if id == "" {
		return "", fmt.Errorf("DOKS provider_cluster_id is required")
	}
	return id, nil
}

func (p *DOKSProvider) authorize(ctx context.Context, req *http.Request, cluster Cluster) error {
	if p.Materializer == nil {
		return fmt.Errorf("DOKS credential materializer is not configured")
	}
	creds, err := p.Materializer.ResolveForCluster(ctx, cluster, ProviderDOKS)
	if err != nil {
		return fmt.Errorf("resolve cloud credential: %w", err)
	}
	if p.AuthOverride != nil {
		return p.AuthOverride(req, creds)
	}
	token := strings.TrimSpace(creds["token"])
	if token == "" {
		return fmt.Errorf("DOKS credential requires token")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (p *DOKSProvider) do(ctx context.Context, cluster Cluster, method string, body io.Reader) (*http.Response, error) {
	id, err := p.resourceID(cluster)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint()+"/kubernetes/clusters/"+url.PathEscape(id), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := p.authorize(ctx, req, cluster); err != nil {
		return nil, err
	}
	client := p.HTTPClient
	if client == nil {
		client = httpclient.SafeClient(30 * time.Second)
	}
	return client.Do(req)
}

func (p *DOKSProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	resp, err := p.do(ctx, cluster, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Provider: ProviderDOKS, Operation: "get_cluster", StatusCode: resp.StatusCode, Body: string(body), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var response struct {
		Cluster struct {
			ControlPlaneFirewall struct {
				Enabled          bool     `json:"enabled"`
				AllowedAddresses []string `json:"allowed_addresses"`
			} `json:"control_plane_firewall"`
		} `json:"kubernetes_cluster"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode DOKS cluster response: %w", err)
	}
	return allowlist.CanonicaliseEffective(response.Cluster.ControlPlaneFirewall.AllowedAddresses), nil
}

func (p *DOKSProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	current, err := p.GetEffective(ctx, cluster)
	if err != nil {
		return err
	}
	desired := allowlist.CanonicaliseEffective(cidrs)
	if allowlist.SameSet(current, desired) {
		return nil
	}
	payload := struct {
		ControlPlaneFirewall struct {
			Enabled          bool     `json:"enabled"`
			AllowedAddresses []string `json:"allowed_addresses"`
		} `json:"control_plane_firewall"`
	}{}
	payload.ControlPlaneFirewall.Enabled = true
	payload.ControlPlaneFirewall.AllowedAddresses = sortedCopy(desired)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := p.do(ctx, cluster, http.MethodPut, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
		return &HTTPError{Provider: ProviderDOKS, Operation: "update_cluster", StatusCode: resp.StatusCode, Body: string(errorBody), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil
}
