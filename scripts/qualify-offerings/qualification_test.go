package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func targets() []memberTarget {
	return []memberTarget{
		{Name: "primary", ClusterID: "00000000-0000-0000-0000-000000000001", ProjectID: "00000000-0000-0000-0000-000000000011", Namespace: "qualification", ExpectedPrivilegeProfile: "admin"},
		{Name: "secondary", ClusterID: "00000000-0000-0000-0000-000000000002", ProjectID: "00000000-0000-0000-0000-000000000012", Namespace: "qualification", ExpectedPrivilegeProfile: "admin"},
	}
}
func estateFixture(t *testing.T) (qualificationConfig, estateEvidence, *url.URL, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	base, _ := url.Parse("https://management.example.test")
	config := qualificationConfig{ExpectedCommit: strings.Repeat("a", 40), QualificationID: "review-20261023", MemberTargets: targets(), EvidenceFile: filepath.Join(t.TempDir(), "estate.json")}
	evidence := estateEvidence{SchemaVersion: "astronomer-estate-attestation-v1", Commit: config.ExpectedCommit, BaseOrigin: base.String(), QualificationID: config.QualificationID, ObservedAt: now, AttestedBy: "infrastructure-operator", InfrastructureSource: "https://infrastructure.example.test/inventory", Members: map[string]estateMember{}}
	for i, target := range config.MemberTargets {
		evidence.Members[target.ClusterID] = estateMember{ProjectID: target.ProjectID, Namespace: target.Namespace, Nodes: []estateNode{{Name: fmt.Sprintf("node-%d", i), SystemUUID: fmt.Sprintf("10000000-0000-0000-0000-%012d", i+1), HostID: fmt.Sprintf("provider/physical-host-%d", i)}}}
	}
	saveEstate(t, &config, evidence)
	return config, evidence, base, now
}
func saveEstate(t *testing.T, config *qualificationConfig, evidence estateEvidence) {
	t.Helper()
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.EvidenceFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	config.EvidenceSHA256 = digest(raw)
}
func TestMemberTargetsUseClusterScopedNamespacesAndRejectProtectedScopes(t *testing.T) {
	t.Parallel()
	if reasons := validateMemberTargetConfig(targets()); len(reasons) != 0 {
		t.Fatal(reasons)
	}
	for _, name := range []string{"default", "kube-system", "astronomer", "astronomer-delivery-system", "flux-system", "bad/name"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			members := targets()
			members[0].Namespace = name
			if len(validateMemberTargetConfig(members)) == 0 {
				t.Fatal("unsafe namespace accepted")
			}
		})
	}
	for _, mutation := range []func([]memberTarget){func(m []memberTarget) { m[1].ClusterID = m[0].ClusterID }, func(m []memberTarget) { m[1].ProjectID = m[0].ProjectID }, func(m []memberTarget) { m[1].Name = m[0].Name }, func(m []memberTarget) { m[1].ExpectedPrivilegeProfile = "" }} {
		m := targets()
		mutation(m)
		if len(validateMemberTargetConfig(m)) == 0 {
			t.Fatal("ambiguous target accepted")
		}
	}
}
func TestEstateEvidenceBindingFreshnessAndIndependentHosts(t *testing.T) {
	t.Parallel()
	mutations := map[string]func(*qualificationConfig, *estateEvidence){
		"valid":            func(*qualificationConfig, *estateEvidence) {},
		"stale":            func(_ *qualificationConfig, e *estateEvidence) { e.ObservedAt = e.ObservedAt.Add(-time.Hour) },
		"future":           func(_ *qualificationConfig, e *estateEvidence) { e.ObservedAt = e.ObservedAt.Add(time.Hour) },
		"wrong-commit":     func(_ *qualificationConfig, e *estateEvidence) { e.Commit = strings.Repeat("b", 40) },
		"wrong-origin":     func(_ *qualificationConfig, e *estateEvidence) { e.BaseOrigin = "https://other.example.test" },
		"wrong-task":       func(_ *qualificationConfig, e *estateEvidence) { e.QualificationID = "another-task" },
		"no-attestor":      func(_ *qualificationConfig, e *estateEvidence) { e.AttestedBy = "" },
		"untrusted-source": func(_ *qualificationConfig, e *estateEvidence) { e.InfrastructureSource = "cluster-node-names" },
		"shared-host": func(c *qualificationConfig, e *estateEvidence) {
			m := e.Members[c.MemberTargets[1].ClusterID]
			m.Nodes[0].HostID = e.Members[c.MemberTargets[0].ClusterID].Nodes[0].HostID
			e.Members[c.MemberTargets[1].ClusterID] = m
		},
		"repeated-system": func(c *qualificationConfig, e *estateEvidence) {
			m := e.Members[c.MemberTargets[1].ClusterID]
			m.Nodes[0].SystemUUID = e.Members[c.MemberTargets[0].ClusterID].Nodes[0].SystemUUID
			e.Members[c.MemberTargets[1].ClusterID] = m
		},
		"missing-member": func(c *qualificationConfig, e *estateEvidence) { delete(e.Members, c.MemberTargets[0].ClusterID) },
		"missing-node": func(c *qualificationConfig, e *estateEvidence) {
			m := e.Members[c.MemberTargets[0].ClusterID]
			m.Nodes = nil
			e.Members[c.MemberTargets[0].ClusterID] = m
		},
		"wrong-project": func(c *qualificationConfig, e *estateEvidence) {
			m := e.Members[c.MemberTargets[0].ClusterID]
			m.ProjectID = c.MemberTargets[1].ProjectID
			e.Members[c.MemberTargets[0].ClusterID] = m
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, e, b, now := estateFixture(t)
			mutate(&c, &e)
			saveEstate(t, &c, e)
			_, err := loadEstateEvidence(c, b, now)
			if (err == nil) != (name == "valid") {
				t.Fatalf("evidence acceptance: %v", err)
			}
		})
	}
	c, e, b, now := estateFixture(t)
	c.EvidenceSHA256 = strings.Repeat("0", 64)
	if _, err := loadEstateEvidence(c, b, now); err == nil {
		t.Fatal("changed bytes accepted")
	}
	saveEstate(t, &c, e)
	for _, identity := range []candidate{{Commit: c.ExpectedCommit, Dirty: true}, {Commit: strings.Repeat("b", 40)}} {
		if _, reasons := preflightInputs(c, identity, b, now); len(reasons) == 0 {
			t.Fatal("unfrozen candidate accepted")
		}
	}
	if _, reasons := preflightInputs(c, candidate{Commit: c.ExpectedCommit}, b, now); len(reasons) != 0 {
		t.Fatal(reasons)
	}
}

func TestCurrentEstateCorroboratesTaskOwnershipAndAllNodes(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "old-kubernetes", "missing-kubernetes", "stale-heartbeat", "management", "wrong-project", "wrong-owner", "missing-node", "extra-node", "changed-system", "not-ready"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			c, e, _, now := estateFixture(t)
			target := c.MemberTargets[0]
			member := e.Members[target.ClusterID]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("invalid request admission")
					w.WriteHeader(403)
					return
				}
				var data any
				prefix := "/api/v1/clusters/" + target.ClusterID
				switch r.URL.Path {
				case prefix + "/":
					version := "v1.35.7+k3s1"
					if scenario == "old-kubernetes" {
						version = "v1.30.4+k3s1"
					}
					if scenario == "missing-kubernetes" {
						version = ""
					}
					heartbeat := now
					if scenario == "stale-heartbeat" {
						heartbeat = now.Add(-time.Hour)
					}
					data = map[string]any{"id": target.ClusterID, "status": "active", "registration_phase": "ready", "last_heartbeat": heartbeat.Format(time.RFC3339Nano), "agent_privilege_profile": "admin", "kubernetes_version": version, "is_local": scenario == "management"}
				case "/api/v1/projects/" + target.ProjectID + "/":
					project := target.ProjectID
					if scenario == "wrong-project" {
						project = c.MemberTargets[1].ProjectID
					}
					data = map[string]any{"id": project, "cluster_id": target.ClusterID, "namespaces": []string{target.Namespace}}
				case prefix + "/namespaces/":
					task := c.QualificationID
					if scenario == "wrong-owner" {
						task = "unrelated"
					}
					data = []any{map[string]any{"name": target.Namespace, "status": "Active", "labels": map[string]string{"astronomer.io/qualification-id": task}}}
				case prefix + "/nodes/":
					data = []any{map[string]string{"name": member.Nodes[0].Name}}
					if scenario == "missing-node" {
						data = []any{}
					}
					if scenario == "extra-node" {
						data = []any{map[string]string{"name": member.Nodes[0].Name}, map[string]string{"name": "foreign"}}
					}
				case prefix + "/nodes/" + member.Nodes[0].Name + "/":
					system := member.Nodes[0].SystemUUID
					if scenario == "changed-system" {
						system = "20000000-0000-0000-0000-000000000001"
					}
					status := "Ready"
					if scenario == "not-ready" {
						status = "NotReady"
					}
					data = map[string]any{"name": member.Nodes[0].Name, "status": status, "nodeInfo": map[string]string{"systemUUID": system}}
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				envelope := map[string]any{"data": data}
				if strings.HasSuffix(r.URL.Path, "/nodes/") || strings.HasSuffix(r.URL.Path, "/namespaces/") {
					envelope["pagination"] = map[string]bool{"has_more": false}
				}
				if err := json.NewEncoder(w).Encode(envelope); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			base, _ := url.Parse(server.URL)
			result := inspectMemberTarget(t.Context(), server.Client(), base, "fixture-token", target, now)
			err := corroborateEstate(t.Context(), server.Client(), base, "fixture-token", c.QualificationID, target, member)
			passed := result.Status == "PASS" && err == nil
			if passed != (scenario == "valid") {
				t.Fatalf("scenario %s accepted=%v member=%v corroboration=%v", scenario, passed, result.Reasons, err)
			}
		})
	}
}

func TestReadOnlyTransportRedirectsAndCLI(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/destination", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	client := inventoryHTTPClient()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if response, err := client.Do(req); err == nil {
			_ = response.Body.Close()
			t.Fatal("mutation admitted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("mutation reached server")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || calls.Load() != 1 {
		t.Fatal("redirect followed")
	}
	for _, mode := range []string{"run", "verify", "apply", "delete"} {
		if err := run(context.Background(), []string{mode}); err == nil {
			t.Fatal("unsupported mode accepted")
		}
	}
}
func TestTokenAndReportFilesFailClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("fixture-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readToken(token); err != nil || got != "fixture-token" {
		t.Fatal("private token read failed")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(token, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(link); err == nil {
		t.Fatal("symlink token admitted")
	}
	if err := os.Chmod(token, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(token); err == nil {
		t.Fatal("public token admitted")
	}
	if err := os.Chmod(token, 0600); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "first\nsecond", strings.Repeat("a", 8193)} {
		if err := os.WriteFile(token, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readToken(token); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	report := filepath.Join(dir, "report.json")
	if err := writeJSONAtomic(report, map[string]string{"status": "BLOCKED"}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(report, map[string]string{"status": "PASS"}); err == nil {
		t.Fatal("evidence overwritten")
	}
	info, err := os.Stat(report)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report permissions not private")
	}
	if err := readStrictJSON(link, &map[string]any{}); err == nil {
		t.Fatal("linked evidence admitted")
	}
	if err := decodeStrictJSON([]byte(`{"a":1} {"a":2}`), &map[string]any{}); err == nil {
		t.Fatal("trailing data admitted")
	}
}

func TestInventoryTraversesPagesAndRejectsFalseGreenResponses(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "unknown-offering", "cross-origin", "same-origin-other-route", "repeat", "bad-pagination", "false-ok", "trailing", "duplicate", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat(" ", maxBodyBytes+1)))
					return
				}
				if scenario == "false-ok" {
					_, _ = w.Write([]byte(`{"ok":false,"data":[{"slug":"one"},{"slug":"two"}]}`))
					return
				}
				if scenario == "duplicate" {
					_, _ = w.Write([]byte(`{"data":[],"data":[{"slug":"one"},{"slug":"two"}]}`))
					return
				}
				if scenario == "trailing" {
					_, _ = w.Write([]byte(`{"data":[{"slug":"one"},{"slug":"two"}]} {}`))
					return
				}
				if r.URL.Query().Get("offset") == "1" {
					slug := "two"
					if scenario == "unknown-offering" {
						slug = "unexpected"
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"slug": slug}}, "pagination": map[string]any{"has_more": false}}); err != nil {
						t.Error(err)
					}
					return
				}
				body := map[string]any{"data": []any{map[string]string{"slug": "one"}}, "pagination": map[string]any{"has_more": true, "next_offset": 1}}
				switch scenario {
				case "cross-origin":
					body["next"] = "https://foreign.example.test/api/v1/tools/"
				case "same-origin-other-route":
					body["next"] = "/api/v1/secrets/"
				case "repeat":
					body["next"] = "/api/v1/tools/"
				case "bad-pagination":
					body["pagination"] = map[string]any{"has_more": "true"}
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			base, _ := url.Parse(server.URL)
			result := inventoryRegistry(t.Context(), inventoryHTTPClient(), base, "fixture-token", registryContract{ID: "tools", Path: "/api/v1/tools/", ItemsPath: "data", IdentityField: "slug", ExpectedIdentities: []string{"one", "two"}}, nil, nil)
			if (result.Status == "PASS") != (scenario == "valid") {
				t.Fatalf("inventory acceptance mismatch: %+v", result)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "fixture-token") {
				t.Fatal("credential in evidence")
			}
		})
	}
}
func TestEstateListsRequireCompletePagination(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "missing-pagination", "repeat", "null-data"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{"data": []any{map[string]string{"name": "node"}}, "pagination": map[string]any{"has_more": false}}
				switch scenario {
				case "missing-pagination":
					delete(body, "pagination")
				case "repeat":
					body["next"] = "/api/v1/clusters/id/nodes/"
				case "null-data":
					body["data"] = nil
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			base, _ := url.Parse(server.URL)
			rows, err := getAPIList(t.Context(), server.Client(), base, "fixture-token", "/api/v1/clusters/id/nodes/")
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("list acceptance: %d rows %v", len(rows), err)
			}
		})
	}
}
