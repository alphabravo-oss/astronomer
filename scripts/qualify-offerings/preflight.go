package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/compatibility"
)

const (
	estatePreflightSchema = "astronomer-offering-estate-preflight-v2"
	minimumMemberTargets  = 2
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)

type clusterPreflightResponse struct {
	ID                    string  `json:"id"`
	KubernetesVersion     string  `json:"kubernetes_version"`
	Status                string  `json:"status"`
	IsLocal               bool    `json:"is_local"`
	DecommissionedAt      *string `json:"decommissioned_at"`
	RegistrationPhase     string  `json:"registration_phase"`
	LastHeartbeat         *string `json:"last_heartbeat"`
	AgentPrivilegeProfile string  `json:"agent_privilege_profile"`
}

type projectPreflightResponse struct {
	ID              string   `json:"id"`
	ClusterID       string   `json:"cluster_id"`
	ClusterIDs      []string `json:"cluster_ids"`
	NamespaceScopes []struct {
		ClusterID  string   `json:"cluster_id"`
		Namespaces []string `json:"namespaces"`
	} `json:"namespace_scopes"`
	Namespaces []string `json:"namespaces"`
}

func runPreflight(ctx context.Context, args []string) error {
	flags := newFlagSet("preflight")
	configPath := flags.String("config", "", "qualification config JSON")
	outputPath := flags.String("output", "", "preflight report JSON path in an existing directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" || *outputPath == "" {
		return errors.New("--config and --output are required; positional arguments are not accepted")
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	identity, err := repositoryIdentity()
	if err != nil {
		return err
	}
	if config.ExpectedCommit != identity.Commit {
		return fmt.Errorf("frozen candidate mismatch: config=%s repository=%s", config.ExpectedCommit, identity.Commit)
	}
	base, err := validateBaseURL(config.BaseURL, config.AllowLoopbackHTTP)
	if err != nil {
		return err
	}
	report := estatePreflightReport{
		SchemaVersion: estatePreflightSchema,
		RunID:         fmt.Sprintf("preflight-%d", time.Now().UTC().UnixNano()),
		GeneratedAt:   time.Now().UTC(), Candidate: identity,
		BaseOrigin: base.Scheme + "://" + base.Host,
		Status:     "BLOCKED", RequiredMemberClusters: minimumMemberTargets,
		ConfiguredMemberTargets: len(config.MemberTargets),
		Members:                 []memberTargetResult{},
	}
	evidence, structuralReasons := preflightInputs(config, identity, base, time.Now().UTC())
	report.EvidenceSHA256 = config.EvidenceSHA256
	report.QualificationID = config.QualificationID
	if len(structuralReasons) != 0 {
		report.Reason = strings.Join(structuralReasons, "; ")
		if err := writeJSONAtomic(*outputPath, report); err != nil {
			return err
		}
		return fmt.Errorf("qualification estate is blocked: %s", report.Reason)
	}
	token, err := readToken(config.TokenFile)
	if err != nil {
		return err
	}
	client := inventoryHTTPClient()
	blocked := false
	for _, target := range config.MemberTargets {
		result := inspectMemberTarget(ctx, client, base, token, target, time.Now().UTC())
		if result.Status == "PASS" {
			if err := corroborateEstate(ctx, client, base, token, config.QualificationID, target, evidence.Members[target.ClusterID]); err != nil {
				result.Status = "BLOCKED"
				result.Reasons = []string{err.Error()}
			}
		}
		report.Members = append(report.Members, result)
		blocked = blocked || result.Status != "PASS"
	}
	finalIdentity, identityErr := repositoryIdentity()
	sourceChanged := identityErr != nil || finalIdentity != identity
	blocked = blocked || sourceChanged
	if blocked {
		report.Reason = "one or more API readiness, task ownership or infrastructure checks failed"
		if sourceChanged {
			report.Reason = "candidate source changed during preflight"
		}
	} else {
		report.Status = "PASS"
		report.Reason = "supported ready member targets match task ownership and fresh operator-attested independent host evidence; lifecycle and deployed candidate acceptance are separate"
	}
	if err := writeJSONAtomic(*outputPath, report); err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("qualification estate is blocked; inspect %s", *outputPath)
	}
	fmt.Printf("qualify-offerings: estate preflight passed; members=%d evidence=%s\n", len(report.Members), *outputPath)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

func validateMemberTargetConfig(targets []memberTarget) []string {
	reasons := []string{}
	if len(targets) > 8 {
		reasons = append(reasons, "member_targets exceeds eight-target read budget")
	}
	if len(targets) < minimumMemberTargets {
		reasons = append(reasons, fmt.Sprintf("member_targets requires at least %d explicit entries", minimumMemberTargets))
	}
	names, clusters, projects, namespaces := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, target := range targets {
		prefix := fmt.Sprintf("member_targets[%d]", index)
		if strings.TrimSpace(target.Name) == "" || names[target.Name] {
			reasons = append(reasons, prefix+".name is empty or duplicated")
		}
		names[target.Name] = true
		if _, err := uuid.Parse(target.ClusterID); err != nil || clusters[target.ClusterID] {
			reasons = append(reasons, prefix+".cluster_id is not a unique UUID")
		}
		clusters[target.ClusterID] = true
		if _, err := uuid.Parse(target.ProjectID); err != nil || projects[target.ProjectID] {
			reasons = append(reasons, prefix+".project_id is not a unique UUID")
		}
		projects[target.ProjectID] = true
		if len(target.Namespace) > 63 || !dnsLabel.MatchString(target.Namespace) || namespaces[target.ClusterID+"/"+target.Namespace] {
			reasons = append(reasons, prefix+".namespace is not a unique cluster-scoped DNS label")
		}
		namespaces[target.ClusterID+"/"+target.Namespace] = true
		if target.Namespace == "default" || strings.HasPrefix(target.Namespace, "kube-") || strings.HasPrefix(target.Namespace, "astronomer") || strings.HasPrefix(target.Namespace, "flux-") {
			reasons = append(reasons, prefix+".namespace is protected")
		}
		if strings.TrimSpace(target.ExpectedPrivilegeProfile) == "" {
			reasons = append(reasons, prefix+".expected_privilege_profile is required")
		}
	}
	return reasons
}

func inspectMemberTarget(ctx context.Context, client *http.Client, base *url.URL, token string, target memberTarget, now time.Time) memberTargetResult {
	result := memberTargetResult{
		Name: target.Name, ClusterID: target.ClusterID, ProjectID: target.ProjectID,
		Namespace: target.Namespace, ExpectedPrivilegeProfile: target.ExpectedPrivilegeProfile,
		Status: "BLOCKED", Reasons: []string{},
	}
	var cluster clusterPreflightResponse
	if err := getAPIData(ctx, client, base, token, "/api/v1/clusters/"+url.PathEscape(target.ClusterID)+"/", &cluster); err != nil {
		result.Reasons = append(result.Reasons, "cluster read failed: "+err.Error())
		return result
	}
	result.ObservedPrivilegeProfile = cluster.AgentPrivilegeProfile
	if cluster.ID != target.ClusterID {
		result.Reasons = append(result.Reasons, "cluster response ID did not match the configured target")
	}
	if !compatibility.SupportsKubernetes(cluster.KubernetesVersion) {
		result.Reasons = append(result.Reasons, "Kubernetes version is missing, malformed, prerelease or unsupported")
	}
	if cluster.IsLocal {
		result.Reasons = append(result.Reasons, "management/local cluster cannot be a mutation target")
	}
	if cluster.DecommissionedAt != nil {
		result.Reasons = append(result.Reasons, "cluster is decommissioned")
	}
	if cluster.Status != "active" {
		result.Reasons = append(result.Reasons, "cluster status is not active")
	}
	if cluster.RegistrationPhase != "ready" {
		result.Reasons = append(result.Reasons, "cluster registration phase is not ready")
	}
	if cluster.AgentPrivilegeProfile != target.ExpectedPrivilegeProfile {
		result.Reasons = append(result.Reasons, "agent privilege profile does not match the explicit contract")
	}
	if cluster.LastHeartbeat == nil {
		result.Reasons = append(result.Reasons, "cluster has no agent heartbeat")
	} else if heartbeat, err := time.Parse(time.RFC3339Nano, *cluster.LastHeartbeat); err != nil || now.Sub(heartbeat) > 2*time.Minute || heartbeat.After(now.Add(30*time.Second)) {
		result.Reasons = append(result.Reasons, "cluster agent heartbeat is invalid or stale")
	}
	var project projectPreflightResponse
	if err := getAPIData(ctx, client, base, token, "/api/v1/projects/"+url.PathEscape(target.ProjectID)+"/", &project); err != nil {
		result.Reasons = append(result.Reasons, "project read failed: "+err.Error())
		return result
	}
	if project.ID != target.ProjectID {
		result.Reasons = append(result.Reasons, "project response ID did not match the configured target")
	}
	if !projectContainsCluster(project, target.ClusterID) {
		result.Reasons = append(result.Reasons, "project is not assigned to the configured cluster")
	}
	if !projectContainsNamespace(project, target.ClusterID, target.Namespace) {
		result.Reasons = append(result.Reasons, "project does not own the configured namespace on this cluster")
	}
	if len(result.Reasons) == 0 {
		result.Status = "PASS"
		result.Reasons = []string{"cluster, agent, project, and namespace are ready for qualification"}
	}
	return result
}

func getAPIData(ctx context.Context, client *http.Client, base *url.URL, token, path string, destination any) error {
	endpoint, err := relativeAPIURL(base, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return errors.New("response body could not be read within the size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET returned HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := decodeStrictJSON(body, &envelope); err != nil {
		return errors.New("response was not a valid API data envelope")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("response omitted data")
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		return errors.New("response data did not match the preflight contract")
	}
	return nil
}

func projectContainsCluster(project projectPreflightResponse, clusterID string) bool {
	if project.ClusterID == clusterID || slices.Contains(project.ClusterIDs, clusterID) {
		return true
	}
	for _, scope := range project.NamespaceScopes {
		if scope.ClusterID == clusterID {
			return true
		}
	}
	return false
}

func projectContainsNamespace(project projectPreflightResponse, clusterID, namespace string) bool {
	for _, scope := range project.NamespaceScopes {
		if scope.ClusterID == clusterID && slices.Contains(scope.Namespaces, namespace) {
			return true
		}
	}
	return project.ClusterID == clusterID && slices.Contains(project.Namespaces, namespace)
}

func preflightInputs(config qualificationConfig, identity candidate, base *url.URL, now time.Time) (estateEvidence, []string) {
	reasons := validateMemberTargetConfig(config.MemberTargets)
	evidence, err := loadEstateEvidence(config, base, now)
	if err != nil {
		reasons = append(reasons, err.Error())
	}
	if identity.Dirty {
		reasons = append(reasons, "repository candidate is dirty")
	}
	if identity.Commit != config.ExpectedCommit {
		reasons = append(reasons, "candidate commit differs")
	}
	return evidence, reasons
}
