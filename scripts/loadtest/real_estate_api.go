package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// A dedicated transport prevents redirect credential forwarding and permits socket-free tests.
func estateHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func estateGET(ctx context.Context, c *http.Client, endpoint, token string, dst any, envelope bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("GET transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return errors.New("GET body exceeds limit or failed")
	}
	if envelope {
		var e struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil || len(e.Data) == 0 || string(e.Data) == "null" {
			return errors.New("missing API data")
		}
		raw = e.Data
	}
	if json.Unmarshal(raw, dst) != nil {
		return errors.New("invalid API response")
	}
	return nil
}

type estateVerification struct {
	Member            string         `json:"member"`
	IdentityVerified  bool           `json:"identity_verified"`
	Census            map[string]int `json:"namespace_resource_counts"`
	RenderedNamespace string         `json:"rendered_namespace_verification"`
	Reason            string         `json:"reason,omitempty"`
}
type estateDeployment struct {
	ID             string                                `json:"id"`
	ClusterID      string                                `json:"cluster_id"`
	TargetID       string                                `json:"target_id"`
	Phase          string                                `json:"phase"`
	Action         string                                `json:"action"`
	Desired        int64                                 `json:"desired_generation"`
	Observed       int64                                 `json:"observed_generation"`
	LastObserved   *time.Time                            `json:"last_observed_at"`
	DesiredDigest  string                                `json:"desired_spec_digest"`
	ObservedDigest string                                `json:"observed_spec_digest"`
	Inventory      astroclient.DeliveryResourceInventory `json:"inventory"`
}

func verifyEstateMember(ctx context.Context, c *http.Client, base, token string, m estateMember, now time.Time) (estateVerification, error) {
	v := estateVerification{Member: m.Name, RenderedNamespace: "UNAVAILABLE", Census: map[string]int{}}
	var cluster struct {
		ID        string     `json:"id"`
		Status    string     `json:"status"`
		Local     bool       `json:"is_local"`
		Deleted   *time.Time `json:"decommissioned_at"`
		Phase     string     `json:"registration_phase"`
		Heartbeat *time.Time `json:"last_heartbeat"`
		Profile   string     `json:"agent_privilege_profile"`
	}
	if err := estateGET(ctx, c, base+"/api/v1/clusters/"+m.ClusterID+"/", token, &cluster, true); err != nil {
		return v, err
	}
	if cluster.ID != m.ClusterID || cluster.Local || cluster.Deleted != nil || cluster.Status != "active" || cluster.Phase != "ready" || cluster.Profile != m.PrivilegeProfile || !estateRecent(cluster.Heartbeat, now, 2*time.Minute) {
		return v, errors.New("member identity, adoption or heartbeat failed")
	}
	var project struct {
		ID         string   `json:"id"`
		ClusterID  string   `json:"cluster_id"`
		Namespaces []string `json:"namespaces"`
		Scopes     []struct {
			ClusterID  string   `json:"cluster_id"`
			Namespaces []string `json:"namespaces"`
		} `json:"namespace_scopes"`
	}
	if err := estateGET(ctx, c, base+"/api/v1/projects/"+m.ProjectID+"/", token, &project, true); err != nil {
		return v, err
	}
	allowed := project.ID == m.ProjectID && project.ClusterID == m.ClusterID && slices.Contains(project.Namespaces, m.Namespace)
	for _, s := range project.Scopes {
		allowed = allowed || (project.ID == m.ProjectID && s.ClusterID == m.ClusterID && slices.Contains(s.Namespaces, m.Namespace))
	}
	if !allowed {
		return v, errors.New("project namespace scope not verified")
	}
	if err := verifyEstateAssignmentSet(ctx, c, base, token, m); err != nil {
		return v, err
	}
	rendered := true
	for _, a := range m.Assignments {
		verified, err := verifyEstateAssignment(ctx, c, base, token, m, a, now)
		if err != nil {
			return v, err
		}
		rendered = rendered && verified
	}
	for _, k := range []string{"pods", "deployments", "services"} {
		n, err := estateResourceCount(ctx, c, base, token, m, k)
		if err != nil {
			return v, err
		}
		v.Census[k] = n
		if n != m.Resources[k] {
			return v, errors.New("namespace resource census differs from manifest")
		}
	}
	v.IdentityVerified = true
	// Legacy receipt time is not source freshness; rendered proof is separate.
	if rendered {
		v.RenderedNamespace = "VERIFIED"
	}
	if !rendered {
		v.Reason = "rendered references lack complete validated source-freshness evidence"
	}
	return v, nil
}
func verifyEstateAssignment(ctx context.Context, c *http.Client, base, token string, m estateMember, a estateAssignment, now time.Time) (bool, error) {
	var detail struct {
		Deployment estateDeployment `json:"deployment"`
	}
	if err := estateGET(ctx, c, base+"/api/v1/delivery/deployments/"+a.ID+"/?project_id="+m.ProjectID, token, &detail, true); err != nil {
		return false, err
	}
	d := detail.Deployment
	if d.ID != a.ID || d.ClusterID != m.ClusterID || d.TargetID != a.TargetID || d.Desired != a.Generation || d.Observed != a.Generation || d.Phase != "ready" || d.Action != "apply" || d.DesiredDigest != a.SpecDigest || d.ObservedDigest != a.SpecDigest || !estateRecent(d.LastObserved, now, 5*time.Minute) {
		return false, errors.New("deployment fixture identity, generation or ready status differs")
	}
	var target struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Deletion  string `json:"deletion_state"`
		Suspended bool   `json:"suspended"`
	}
	if err := estateGET(ctx, c, base+"/api/v1/delivery/targets/"+a.TargetID+"/?project_id="+m.ProjectID, token, &target, true); err != nil {
		return false, err
	}
	if target.ID != a.TargetID || target.ProjectID != m.ProjectID || target.Deletion != "active" || target.Suspended {
		return false, errors.New("deployment target scope differs")
	}
	return verifyEstateRendered(ctx, c, base, token, m, a, d.Inventory, now)
}
func estateRecent(t *time.Time, now time.Time, age time.Duration) bool {
	return t != nil && !t.After(now) && now.Sub(*t) <= age
}
func estateResourceCount(ctx context.Context, c *http.Client, base, token string, m estateMember, kind string) (int, error) {
	group := "/api/v1"
	if kind == "deployments" {
		group = "/apis/apps/v1"
	}
	path := base + "/api/v1/clusters/" + m.ClusterID + "/k8s" + group + "/namespaces/" + m.Namespace + "/" + kind
	total := 0
	continuation := ""
	seen := map[string]bool{}
	for page := 0; page < 201; page++ {
		var list struct {
			Items    []json.RawMessage `json:"items"`
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
		}
		if err := estateGET(ctx, c, path+"?limit=500&continue="+url.QueryEscape(continuation), token, &list, false); err != nil {
			return 0, err
		}
		if list.Items == nil {
			return 0, errors.New("resource list omitted items")
		}
		total += len(list.Items)
		if total > 100000 {
			return 0, errors.New("resource list exceeds bound")
		}
		continuation = list.Metadata.Continue
		if continuation == "" {
			return total, nil
		}
		if seen[continuation] || len(continuation) > 8192 {
			return 0, errors.New("invalid resource pagination")
		}
		seen[continuation] = true
	}
	return 0, errors.New("resource pagination exceeds bound")
}

func verifyEstateAssignmentSet(ctx context.Context, c *http.Client, base, token string, m estateMember) error {
	var page struct {
		Data       []estateDeployment `json:"data"`
		Pagination *struct {
			Total   *int `json:"total"`
			HasMore bool `json:"has_more"`
		} `json:"pagination"`
	}
	endpoint := base + "/api/v1/delivery/deployments/?project_id=" + m.ProjectID + "&cluster_id=" + m.ClusterID + "&limit=100&offset=0"
	if err := estateGET(ctx, c, endpoint, token, &page, false); err != nil {
		return err
	}
	if page.Pagination == nil || page.Pagination.Total == nil || page.Pagination.HasMore || *page.Pagination.Total != len(m.Assignments) || len(page.Data) != len(m.Assignments) {
		return errors.New("scoped assignment census differs from tier")
	}
	want := map[string]bool{}
	for _, a := range m.Assignments {
		want[a.ID] = true
	}
	for _, d := range page.Data {
		if !want[d.ID] || d.ClusterID != m.ClusterID {
			return errors.New("unexpected or duplicate scoped assignment")
		}
		delete(want, d.ID)
	}
	return nil
}
