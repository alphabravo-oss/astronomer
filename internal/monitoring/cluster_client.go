package monitoring

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

type ClusterRequester interface {
	Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error)
}

// NewClusterClient binds every query to the installed release in one cluster.
// No shared-backend credentials or caller-supplied network destinations are used.
func NewClusterClient(ctx context.Context, requester ClusterRequester, clusterID, namespace, release string) (*Client, error) {
	if requester == nil {
		return nil, fmt.Errorf("cluster monitoring requester is not configured")
	}
	if _, err := uuid.Parse(clusterID); err != nil {
		return nil, err
	}
	if len(validation.IsDNS1123Label(namespace)) != 0 || len(validation.IsDNS1123Subdomain(release)) != 0 {
		return nil, fmt.Errorf("invalid monitoring release target")
	}
	path := "/api/v1/namespaces/" + namespace + "/services?labelSelector=" + url.QueryEscape("app.kubernetes.io/instance="+release)
	resp, err := requester.Do(ctx, clusterID, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot discover cluster Prometheus service")
	}
	body, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Type  string `json:"type"`
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	for _, svc := range list.Items {
		if svc.Spec.Type == "ExternalName" || !strings.Contains(svc.Metadata.Name, "prometheus") || len(validation.IsDNS1035Label(svc.Metadata.Name)) != 0 {
			continue
		}
		for _, port := range svc.Spec.Ports {
			if port.Port == 9090 {
				transport := &clusterPrometheusTransport{requester: requester, clusterID: clusterID, prefix: "/api/v1/namespaces/" + namespace + "/services/http:" + svc.Metadata.Name + ":9090/proxy"}
				return &Client{baseURL: "http://cluster-prometheus", httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
			}
		}
	}
	return nil, fmt.Errorf("Prometheus service not found for release %s", release)
}

type clusterPrometheusTransport struct {
	requester         ClusterRequester
	clusterID, prefix string
}

func (t *clusterPrometheusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Host != "cluster-prometheus" {
		return nil, fmt.Errorf("invalid cluster monitoring request")
	}
	switch req.URL.Path {
	case "/api/v1/query", "/api/v1/query_range", "/-/healthy":
	default:
		return nil, fmt.Errorf("unsupported cluster monitoring path")
	}
	path := t.prefix + req.URL.Path
	if req.URL.RawQuery != "" {
		path += "?" + req.URL.RawQuery
	}
	resp, err := t.requester.Do(req.Context(), t.clusterID, http.MethodGet, path, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty cluster monitoring response")
	}
	body, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: resp.StatusCode, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}
