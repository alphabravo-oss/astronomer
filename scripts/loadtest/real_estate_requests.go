package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type estateRequest struct {
	Scenario   string
	Member     string
	Assignment string
	Path       string
	Validate   func([]byte) error
}
type estateRequestResult struct {
	Scenario            string                    `json:"scenario"`
	Member              string                    `json:"member,omitempty"`
	Assignment          string                    `json:"assignment_id,omitempty"`
	Scheduled           int                       `json:"scheduled"`
	Completed           int                       `json:"completed"`
	Success             int                       `json:"success"`
	Failed              int                       `json:"failed"`
	Diagnostics         map[string]int            `json:"failure_diagnostics"`
	HeaderLatency       estateLatencyDistribution `json:"header_latency"`
	FullResponseLatency estateLatencyDistribution `json:"full_response_latency"`
}
type estateRequestSamples struct {
	Result        estateRequestResult
	Headers, Full estateLatencyHistogram
}
type estateRequestRecorder struct {
	mu   sync.Mutex
	Rows []estateRequestSamples
}

func newEstateRequestRecorder(catalog []estateRequest) *estateRequestRecorder {
	r := &estateRequestRecorder{Rows: make([]estateRequestSamples, len(catalog))}
	for i, q := range catalog {
		r.Rows[i].Result = estateRequestResult{Scenario: q.Scenario, Member: q.Member, Assignment: q.Assignment, Diagnostics: map[string]int{}}
	}
	return r
}
func (r *estateRequestRecorder) scheduled(index int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rows[index].Result.Scheduled++
}
func (r *estateRequestRecorder) completed(index int, header, full time.Duration, diagnostic string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := &r.Rows[index]
	row.Result.Completed++
	if diagnostic == "" {
		row.Result.Success++
	} else {
		row.Result.Failed++
		row.Result.Diagnostics[diagnostic]++
	}
	row.Headers.observe(header)
	row.Full.observe(full)
}
func (r *estateRequestRecorder) results() []estateRequestResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]estateRequestResult, len(r.Rows))
	for i, row := range r.Rows {
		out[i] = row.Result
		out[i].HeaderLatency = row.Headers.distribution()
		out[i].FullResponseLatency = row.Full.distribution()
	}
	return out
}

// Estate requests retain header timing separately. Full timing includes bounded
// body consumption and validation; exactly one terminal outcome is recorded.
func doEstateRequest(ctx context.Context, client *http.Client, base, token string, q estateRequest, index int, rec *estateRequestRecorder) (diagnostic string) {
	rec.scheduled(index)
	start := time.Now()
	header := time.Duration(0)
	defer func() { rec.completed(index, header, time.Since(start), diagnostic) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+q.Path, nil)
	if err != nil {
		diagnostic = "request"
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	header = time.Since(start)
	if err != nil {
		diagnostic = "transport"
		return
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		diagnostic = "body_read"
		return
	}
	if len(raw) > 4<<20 {
		diagnostic = "body_limit"
		return
	}
	if resp.StatusCode != http.StatusOK {
		diagnostic = "http_status"
		return
	}
	if err = q.Validate(raw); err != nil {
		diagnostic = err.Error()
	}
	return
}

func estateRequestCatalog(m estateManifest, p estatePhaseSpec) []estateRequest {
	out := []estateRequest{}
	if p.Mode == "idle" {
		return out
	}
	if p.Mode == "search" {
		for _, kind := range m.Search.Types {
			q := url.Values{"type": {kind}, "namespace": {m.Search.Namespace}, "limit": {strconv.Itoa(m.Search.Limit)}}
			out = append(out, estateRequest{Scenario: "search_" + kind, Path: "/api/v1/resources/search?" + q.Encode(), Validate: func(raw []byte) error { return validateEstateSearchBody(raw, m.Search, kind) }})
		}
		return out
	}
	for _, member := range m.Members {
		switch p.Mode {
		case "resources":
			for _, kind := range []string{"pods", "deployments", "services"} {
				group := "/api/v1"
				if kind == "deployments" {
					group = "/apis/apps/v1"
				}
				path := "/api/v1/clusters/" + member.ClusterID + "/k8s" + group + "/namespaces/" + member.Namespace + "/" + kind + "?limit=500"
				out = append(out, estateRequest{Scenario: "namespace_" + kind, Member: member.Name, Path: path, Validate: func(raw []byte) error { return validateEstateResourceBody(raw, member.Namespace) }})
			}
		case "delivery":
			project := "project_id=" + member.ProjectID
			out = append(out, estateRequest{Scenario: "delivery_inventory", Member: member.Name, Path: "/api/v1/delivery/clusters/" + member.ClusterID + "/inventory/?" + project, Validate: func(raw []byte) error { return validateEstateDeliveryBody(raw, member, "inventory", nil) }})
			out = append(out, estateRequest{Scenario: "delivery_list", Member: member.Name, Path: "/api/v1/delivery/deployments/?" + project + "&cluster_id=" + member.ClusterID + "&limit=100&offset=0", Validate: func(raw []byte) error { return validateEstateDeliveryBody(raw, member, "list", nil) }})
			for _, assignment := range member.Assignments {
				out = append(out, estateRequest{Scenario: "delivery_detail", Member: member.Name, Assignment: assignment.ID, Path: "/api/v1/delivery/deployments/" + assignment.ID + "/?" + project, Validate: func(raw []byte) error { return validateEstateDeliveryBody(raw, member, "detail", &assignment) }})
			}
		}
	}
	return out
}
func validateEstateResourceBody(raw []byte, namespace string) error {
	var result struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Items == nil || len(result.Items) > 500 {
		return errors.New("invalid_resource_page")
	}
	for _, item := range result.Items {
		if item.Metadata.Namespace != namespace {
			return errors.New("foreign_resource")
		}
	}
	return nil
}
