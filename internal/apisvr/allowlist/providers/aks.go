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

const defaultAKSAPIVersion = "2025-01-01"

// AKSProvider reconciles properties.apiServerAccessProfile.authorizedIPRanges
// through the Azure Resource Manager Managed Clusters API.
type AKSProvider struct {
	HTTPClient        *http.Client
	Endpoint          string
	AuthorityEndpoint string
	APIVersion        string
	Materializer      CloudCredentialMaterializer
	AuthOverride      func(context.Context, *http.Request, map[string]string) error
}

func NewAKSProvider(m CloudCredentialMaterializer) *AKSProvider {
	return &AKSProvider{HTTPClient: httpclient.SafeClient(30 * time.Second), Materializer: m}
}

func (p *AKSProvider) ID() ProviderID { return ProviderAKS }

func (p *AKSProvider) Capability() Capability {
	return Capability{Provider: ProviderAKS, CanMonitor: true, CanEnforce: true, RequiredMetadata: []string{"name", "resource_group", "cloud credential"}}
}

func (p *AKSProvider) Detect(_ context.Context, cluster Cluster) string {
	if matchAnnotationOrProvider(cluster, ProviderAKS) {
		return ProviderAKS
	}
	return ""
}

func (p *AKSProvider) endpoint() string {
	if p.Endpoint != "" {
		return strings.TrimSuffix(p.Endpoint, "/")
	}
	return "https://management.azure.com"
}

func (p *AKSProvider) apiVersion() string {
	if p.APIVersion != "" {
		return p.APIVersion
	}
	return defaultAKSAPIVersion
}

func (p *AKSProvider) resourceURL(cluster Cluster, subscriptionID string) (string, error) {
	if strings.TrimSpace(subscriptionID) == "" {
		return "", fmt.Errorf("AKS credential requires subscription_id")
	}
	if strings.TrimSpace(cluster.ResourceGroup) == "" {
		return "", fmt.Errorf("AKS resource_group is required")
	}
	if strings.TrimSpace(cluster.Name) == "" {
		return "", fmt.Errorf("AKS cluster name is required")
	}
	return fmt.Sprintf("%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s?api-version=%s",
		p.endpoint(), url.PathEscape(subscriptionID), url.PathEscape(cluster.ResourceGroup), url.PathEscape(cluster.Name), url.QueryEscape(p.apiVersion())), nil
}

func (p *AKSProvider) credentials(ctx context.Context, cluster Cluster) (map[string]string, error) {
	if p.Materializer == nil {
		return nil, fmt.Errorf("AKS credential materializer is not configured")
	}
	creds, err := p.Materializer.ResolveForCluster(ctx, cluster, ProviderAKS)
	if err != nil {
		return nil, fmt.Errorf("resolve cloud credential: %w", err)
	}
	return creds, nil
}

func (p *AKSProvider) authorize(ctx context.Context, req *http.Request, creds map[string]string) error {
	if p.AuthOverride != nil {
		return p.AuthOverride(ctx, req, creds)
	}
	clientID := strings.TrimSpace(creds["client_id"])
	clientSecret := strings.TrimSpace(creds["client_secret"])
	tenantID := strings.TrimSpace(creds["tenant_id"])
	if clientID == "" || clientSecret == "" || tenantID == "" {
		return fmt.Errorf("AKS credential requires client_id, client_secret, and tenant_id")
	}
	authority := strings.TrimSuffix(p.AuthorityEndpoint, "/")
	if authority == "" {
		authority = "https://login.microsoftonline.com"
	}
	tokenURL := authority + "/" + url.PathEscape(tenantID) + "/oauth2/v2.0/token"
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"client_credentials"},
		"scope":         {"https://management.azure.com/.default"},
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.httpClient().Do(tokenReq)
	if err != nil {
		return fmt.Errorf("acquire AKS OAuth token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Provider: ProviderAKS, Operation: "oauth_token", StatusCode: resp.StatusCode, Body: string(body), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return fmt.Errorf("AKS OAuth response did not contain an access_token")
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return nil
}

func (p *AKSProvider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return httpclient.SafeClient(30 * time.Second)
}

func (p *AKSProvider) getCluster(ctx context.Context, cluster Cluster) (map[string]any, string, []string, error) {
	creds, err := p.credentials(ctx, cluster)
	if err != nil {
		return nil, "", nil, err
	}
	resourceURL, err := p.resourceURL(cluster, strings.TrimSpace(creds["subscription_id"]))
	if err != nil {
		return nil, "", nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, "", nil, err
	}
	if err := p.authorize(ctx, req, creds); err != nil {
		return nil, "", nil, err
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
	if resp.StatusCode/100 != 2 {
		return nil, "", nil, &HTTPError{Provider: ProviderAKS, Operation: "get_cluster", StatusCode: resp.StatusCode, Body: string(body), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, "", nil, fmt.Errorf("decode AKS ManagedCluster response: %w", err)
	}
	properties, _ := document["properties"].(map[string]any)
	profile, _ := properties["apiServerAccessProfile"].(map[string]any)
	rawRanges, _ := profile["authorizedIPRanges"].([]any)
	ranges := make([]string, 0, len(rawRanges))
	for _, value := range rawRanges {
		if cidr, ok := value.(string); ok {
			ranges = append(ranges, cidr)
		}
	}
	return document, resp.Header.Get("ETag"), allowlist.CanonicaliseEffective(ranges), nil
}

func (p *AKSProvider) GetEffective(ctx context.Context, cluster Cluster) ([]string, error) {
	_, _, ranges, err := p.getCluster(ctx, cluster)
	return ranges, err
}

func (p *AKSProvider) Apply(ctx context.Context, cluster Cluster, cidrs []string) error {
	document, etag, current, err := p.getCluster(ctx, cluster)
	if err != nil {
		return err
	}
	desired := allowlist.CanonicaliseEffective(cidrs)
	if allowlist.SameSet(current, desired) {
		return nil
	}
	properties, ok := document["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("AKS ManagedCluster response has no properties object")
	}
	profile, _ := properties["apiServerAccessProfile"].(map[string]any)
	if profile == nil {
		profile = map[string]any{}
		properties["apiServerAccessProfile"] = profile
	}
	profile["authorizedIPRanges"] = sortedCopy(desired)
	stripAKSReadOnlyFields(document)
	creds, err := p.credentials(ctx, cluster)
	if err != nil {
		return err
	}
	resourceURL, err := p.resourceURL(cluster, strings.TrimSpace(creds["subscription_id"]))
	if err != nil {
		return err
	}
	body, err := json.Marshal(document)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, resourceURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	if err := p.authorize(ctx, req, creds); err != nil {
		return err
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderErrorBody))
		return &HTTPError{Provider: ProviderAKS, Operation: "update_cluster", StatusCode: resp.StatusCode, Body: string(errorBody), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil
}

func stripAKSReadOnlyFields(document map[string]any) {
	delete(document, "id")
	delete(document, "name")
	delete(document, "type")
	delete(document, "systemData")
	properties, _ := document["properties"].(map[string]any)
	for _, field := range []string{"provisioningState", "powerState", "resourceUID", "fqdn", "privateFQDN", "azurePortalFQDN", "currentKubernetesVersion"} {
		delete(properties, field)
	}
}
