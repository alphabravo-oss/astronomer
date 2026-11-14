package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// Evidence is an operator-attested infrastructure mapping, not an inference
// from cluster IDs or Kubernetes node names. Its hash pins the reviewed bytes;
// current API reads corroborate all node identities and namespace ownership.
type estateEvidence struct {
	SchemaVersion        string                  `json:"schema_version"`
	Commit               string                  `json:"commit"`
	BaseOrigin           string                  `json:"base_origin"`
	QualificationID      string                  `json:"qualification_id"`
	ObservedAt           time.Time               `json:"observed_at"`
	AttestedBy           string                  `json:"attested_by"`
	InfrastructureSource string                  `json:"infrastructure_source"`
	Members              map[string]estateMember `json:"members"`
}
type estateMember struct {
	ProjectID string       `json:"project_id"`
	Namespace string       `json:"namespace"`
	Nodes     []estateNode `json:"nodes"`
}
type estateNode struct {
	Name       string `json:"name"`
	SystemUUID string `json:"system_uuid"`
	HostID     string `json:"infrastructure_host_id"`
}

func loadEstateEvidence(config qualificationConfig, base *url.URL, now time.Time) (estateEvidence, error) {
	var evidence estateEvidence
	if config.QualificationID == "" || len(config.QualificationID) > 63 || !dnsLabel.MatchString(config.QualificationID) {
		return evidence, errors.New("qualification_id must be a task-specific DNS label")
	}
	raw, err := readBoundedFile(config.EvidenceFile, maxBodyBytes)
	if err != nil || len(raw) > maxBodyBytes || config.EvidenceSHA256 == "" || digest(raw) != config.EvidenceSHA256 {
		return evidence, errors.New("estate evidence is unavailable, oversized or its SHA256 differs")
	}
	if err := decodeStrictJSON(raw, &evidence); err != nil {
		return evidence, errors.New("estate evidence does not match the closed schema")
	}
	if evidence.SchemaVersion != "astronomer-estate-attestation-v1" || evidence.Commit != config.ExpectedCommit || evidence.BaseOrigin != base.String() || evidence.QualificationID != config.QualificationID {
		return evidence, errors.New("estate evidence candidate, origin or task binding differs")
	}
	if evidence.ObservedAt.IsZero() || now.Sub(evidence.ObservedAt) > 30*time.Minute || evidence.ObservedAt.After(now.Add(30*time.Second)) {
		return evidence, errors.New("estate evidence is stale or future-dated")
	}
	source, err := url.Parse(evidence.InfrastructureSource)
	if strings.TrimSpace(evidence.AttestedBy) == "" || err != nil || source.Scheme != "https" || source.Host == "" || source.User != nil || source.RawQuery != "" || source.Fragment != "" {
		return evidence, errors.New("independent infrastructure source and operator attestation are required")
	}
	if len(evidence.Members) != len(config.MemberTargets) {
		return evidence, errors.New("estate evidence member inventory differs")
	}
	hosts, systems := map[string]string{}, map[string]bool{}
	for _, target := range config.MemberTargets {
		member, ok := evidence.Members[target.ClusterID]
		if !ok || member.ProjectID != target.ProjectID || member.Namespace != target.Namespace || len(member.Nodes) == 0 || len(member.Nodes) > 1000 {
			return evidence, errors.New("estate member scope or node inventory differs")
		}
		names := map[string]bool{}
		for _, node := range member.Nodes {
			system, err := uuid.Parse(node.SystemUUID)
			host := strings.TrimSpace(node.HostID)
			if !validNodeName(node.Name) || names[node.Name] || err != nil || system == uuid.Nil || systems[system.String()] || host == "" || len(host) > 512 || strings.ContainsAny(host, "\r\n\t") || host != node.HostID {
				return evidence, errors.New("estate node identity is absent or duplicated")
			}
			if owner, ok := hosts[host]; ok && owner != target.ClusterID {
				return evidence, errors.New("member clusters share an infrastructure host")
			}
			names[node.Name] = true
			systems[system.String()] = true
			hosts[host] = target.ClusterID
		}
	}
	return evidence, nil
}

func corroborateEstate(ctx context.Context, client *http.Client, base *url.URL, token, task string, target memberTarget, member estateMember) error {
	prefix := "/api/v1/clusters/" + url.PathEscape(target.ClusterID)
	namespaces, err := getAPIList(ctx, client, base, token, prefix+"/namespaces/?limit=100&offset=0")
	if err != nil {
		return fmt.Errorf("namespace ownership read failed: %w", err)
	}
	owned := false
	for _, raw := range namespaces {
		var namespace struct {
			Name   string            `json:"name"`
			Status string            `json:"status"`
			Labels map[string]string `json:"labels"`
		}
		if json.Unmarshal(raw, &namespace) != nil {
			return errors.New("namespace inventory is malformed")
		}
		if namespace.Name == target.Namespace {
			if owned || namespace.Status != "Active" || namespace.Labels["astronomer.io/qualification-id"] != task {
				return errors.New("qualification namespace ownership or readiness differs")
			}
			owned = true
		}
	}
	if !owned {
		return errors.New("task-owned qualification namespace is absent")
	}
	nodes, err := getAPIList(ctx, client, base, token, prefix+"/nodes/?limit=100&offset=0")
	if err != nil || len(nodes) != len(member.Nodes) {
		return errors.New("current node inventory differs from infrastructure attestation")
	}
	expected := map[string]string{}
	for _, node := range member.Nodes {
		expected[node.Name] = node.SystemUUID
	}
	seen := map[string]bool{}
	for _, raw := range nodes {
		var node struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &node) != nil || expected[node.Name] == "" || seen[node.Name] {
			return errors.New("current node identity differs from infrastructure attestation")
		}
		seen[node.Name] = true
		var detail struct {
			Name     string `json:"name"`
			Status   string `json:"status"`
			NodeInfo struct {
				SystemUUID string `json:"systemUUID"`
			} `json:"nodeInfo"`
		}
		if err := getAPIData(ctx, client, base, token, prefix+"/nodes/"+url.PathEscape(node.Name)+"/", &detail); err != nil {
			return errors.New("node identity read failed")
		}
		if detail.Name != node.Name || detail.Status != "Ready" || !strings.EqualFold(detail.NodeInfo.SystemUUID, expected[node.Name]) {
			return errors.New("node readiness or system UUID differs from infrastructure attestation")
		}
	}
	return nil
}

type getOnlyTransport struct{ base http.RoundTripper }

func (t getOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		return nil, errors.New("qualification client permits GET only")
	}
	return t.base.RoundTrip(request)
}
func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
func decodeStrictJSON(raw []byte, destination any) error {
	return jsoncontract.Decode(raw, destination)
}
func getAPIList(ctx context.Context, client *http.Client, base *url.URL, token, path string) ([]json.RawMessage, error) {
	next, err := relativeAPIURL(base, path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	rows := []json.RawMessage{}
	for page := 0; next != nil && page < maxPages; page++ {
		if seen[next.String()] {
			return nil, errors.New("inventory pagination repeated")
		}
		seen[next.String()] = true
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return nil, errors.New("inventory request failed")
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
		_ = response.Body.Close()
		if readErr != nil || len(raw) > maxBodyBytes || response.StatusCode != http.StatusOK {
			return nil, errors.New("inventory read failed or exceeded limit")
		}
		var envelope map[string]json.RawMessage
		if decodeStrictJSON(raw, &envelope) != nil {
			return nil, errors.New("inventory response is malformed")
		}
		var pagination struct {
			HasMore *bool `json:"has_more"`
		}
		if json.Unmarshal(envelope["pagination"], &pagination) != nil || pagination.HasMore == nil {
			return nil, errors.New("inventory response omitted explicit pagination completeness")
		}
		var data []json.RawMessage
		if len(envelope["ok"]) > 0 && string(envelope["ok"]) != "true" || len(envelope["error"]) > 0 && string(envelope["error"]) != "null" || string(envelope["data"]) == "null" || json.Unmarshal(envelope["data"], &data) != nil {
			return nil, errors.New("inventory response omitted valid data")
		}
		rows = append(rows, data...)
		if len(rows) > 10000 {
			return nil, errors.New("inventory row budget exceeded")
		}
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		next, err = nextPageURL(base, next, decoded)
		if err != nil {
			return nil, err
		}
	}
	if next != nil {
		return nil, errors.New("inventory page budget exceeded")
	}
	return rows, nil
}

func readBoundedFile(path string, maximum int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("input must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() {
		return nil, errors.New("input file identity changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errors.New("input exceeded read budget")
	}
	return raw, nil
}

func validNodeName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if len(part) > 63 || !dnsLabel.MatchString(part) {
			return false
		}
	}
	return true
}
