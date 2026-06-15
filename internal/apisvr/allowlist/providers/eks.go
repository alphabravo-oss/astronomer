package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// EKSProvider patches an EKS cluster's vpcConfig.publicAccessCidrs field
// using the AWS EKS REST API.
//
// v1 implementation:
//   - HTTPClient is injectable so unit tests can stub the AWS REST
//     surface via httptest. Production wires an *http.Client whose
//     transport carries the SigV4 signer.
//   - Endpoint is configurable so the same code path drives the
//     httptest fake AND a real AWS endpoint (operators in airgapped
//     regions point us at their PrivateLink endpoint).
//   - SigningOverride is the test-only escape hatch that suppresses
//     SigV4 (httptest doesn't care about signatures); production
//     leaves it nil so the real signer wraps every request.
type EKSProvider struct {
	HTTPClient      *http.Client
	Endpoint        string // e.g. "https://eks.us-east-1.amazonaws.com"
	Materializer    CloudCredentialMaterializer
	SigningOverride func(req *http.Request, creds map[string]string) error
}

// NewEKSProvider wires the provider against the existing cloud-credentials
// materializer (sprint 053). HTTPClient defaults to a 30s-timeout client
// when nil; Endpoint must be set per-cluster (we don't pin a region
// here because the cluster row carries Region).
func NewEKSProvider(m CloudCredentialMaterializer) *EKSProvider {
	return &EKSProvider{
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		Materializer: m,
	}
}

// ID returns the registered provider id; satisfies the Registry.Lookup
// "interface{ ID() ProviderID }" probe.
func (p *EKSProvider) ID() ProviderID { return ProviderEKS }

func (p *EKSProvider) Detect(ctx context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderEKS) {
		return ProviderEKS
	}
	return ""
}

// eksDescribeClusterResponse mirrors the AWS EKS DescribeCluster shape we
// need — only the resourcesVpcConfig.publicAccessCidrs subset.
type eksDescribeClusterResponse struct {
	Cluster struct {
		ResourcesVpcConfig struct {
			PublicAccessCidrs []string `json:"publicAccessCidrs"`
		} `json:"resourcesVpcConfig"`
	} `json:"cluster"`
}

// eksUpdateClusterConfigRequest is the API body for UpdateClusterConfig.
// We only set the resourcesVpcConfig subset; AWS preserves every other
// field on the cluster.
type eksUpdateClusterConfigRequest struct {
	ResourcesVpcConfig struct {
		EndpointPublicAccess bool     `json:"endpointPublicAccess"`
		PublicAccessCidrs    []string `json:"publicAccessCidrs"`
	} `json:"resourcesVpcConfig"`
}

func (p *EKSProvider) endpoint(cluster Cluster) string {
	if p.Endpoint != "" {
		return strings.TrimSuffix(p.Endpoint, "/")
	}
	region := cluster.Region
	if region == "" {
		region = "us-east-1"
	}
	return "https://eks." + region + ".amazonaws.com"
}

func (p *EKSProvider) signAndSend(ctx context.Context, req *http.Request, cluster Cluster) (*http.Response, error) {
	var creds map[string]string
	if p.Materializer != nil && cluster.CredentialID != uuid.Nil {
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
		client = httpclient.DefaultExternal()
	}
	return client.Do(req)
}

func (p *EKSProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	if cluster.Name == "" {
		return nil, fmt.Errorf("cluster name required for EKS DescribeCluster")
	}
	url := p.endpoint(cluster) + "/clusters/" + cluster.Name
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
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("EKS DescribeCluster %s: status %d: %s", cluster.Name, resp.StatusCode, string(body))
	}
	var out eksDescribeClusterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("EKS DescribeCluster decode: %w", err)
	}
	return allowlist.CanonicaliseEffective(out.Cluster.ResourcesVpcConfig.PublicAccessCidrs), nil
}

func (p *EKSProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	if cluster.Name == "" {
		return fmt.Errorf("cluster name required for EKS UpdateClusterConfig")
	}
	// Idempotency: if the effective set already matches, skip the write.
	effective, err := p.GetEffective(ctx, cluster)
	if err != nil {
		return fmt.Errorf("GetEffective before apply: %w", err)
	}
	desired := allowlist.CanonicaliseEffective(cidrs)
	if allowlist.SameSet(effective, desired) {
		return nil
	}

	body := eksUpdateClusterConfigRequest{}
	body.ResourcesVpcConfig.EndpointPublicAccess = true
	body.ResourcesVpcConfig.PublicAccessCidrs = sortedCopy(desired)
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := p.endpoint(cluster) + "/clusters/" + cluster.Name + "/update-config"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(buf)))
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
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("EKS UpdateClusterConfig %s: status %d: %s", cluster.Name, resp.StatusCode, string(rb))
	}
	return nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
