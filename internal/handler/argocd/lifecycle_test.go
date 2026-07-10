package argocd

// Phase B1 tests: cover the new lifecycle client methods.
//
// These mirror the httptest pattern in client_test.go: spin up a fake
// upstream that asserts the request shape matches the documented ArgoCD
// API, and verify the typed responses round-trip.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newLifecycleClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-token", Options{HTTPClient: srv.Client(), Timeout: 5 * time.Second})
	return c, srv
}

func TestCreateApplication(t *testing.T) {
	var seenPath, seenAuth string
	var seenBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"xyz"},"status":{"sync":{"status":"OutOfSync"}}}`))
	})

	app, err := c.CreateApplication(context.Background(), "myapp", ApplicationSpec{
		Project: "default",
		Source: &ApplicationSource{
			RepoURL:        "https://example.com/repo",
			Path:           "manifests",
			TargetRevision: "main",
			Helm: &HelmSource{
				ReleaseName: "myapp",
			},
		},
		Destination: &ApplicationDestination{
			Server:    "https://kubernetes.default.svc",
			Namespace: "prod",
		},
	})
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if app.Metadata.Name != "myapp" {
		t.Errorf("name = %s", app.Metadata.Name)
	}
	if seenPath != "/api/v1/applications" {
		t.Errorf("path = %s", seenPath)
	}
	if seenAuth != "Bearer test-token" {
		t.Errorf("auth = %s", seenAuth)
	}
	// Verify the envelope shape: {metadata: {name}, spec: {...}}.
	if md, ok := seenBody["metadata"].(map[string]any); !ok || md["name"] != "myapp" {
		t.Errorf("metadata.name missing in body: %v", seenBody)
	}
	if sp, ok := seenBody["spec"].(map[string]any); !ok || sp["project"] != "default" {
		t.Errorf("spec.project missing: %v", seenBody)
	}
}

func TestPatchApplicationWrapsAsMergePatch(t *testing.T) {
	var seenPath, seenContentType string
	var seenBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s", r.Method)
		}
		seenPath = r.URL.Path
		seenContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp"},"status":{}}`))
	})

	merge := json.RawMessage(`{"spec":{"source":{"targetRevision":"v2"}}}`)
	if _, err := c.PatchApplication(context.Background(), "myapp", merge); err != nil {
		t.Fatalf("PatchApplication: %v", err)
	}
	if seenPath != "/api/v1/applications/myapp" {
		t.Errorf("path = %s", seenPath)
	}
	if !strings.HasPrefix(seenContentType, "application/json") {
		t.Errorf("content-type = %s", seenContentType)
	}
	if seenBody["name"] != "myapp" {
		t.Errorf("envelope.name = %v", seenBody["name"])
	}
	if seenBody["patchType"] != "merge" {
		t.Errorf("patchType = %v", seenBody["patchType"])
	}
	if !strings.Contains(seenBody["patch"].(string), "targetRevision") {
		t.Errorf("patch body lost targetRevision: %v", seenBody["patch"])
	}
}

func TestDeleteApplicationCascade(t *testing.T) {
	var seenPath, seenQuery string
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})
	if err := c.DeleteApplication(context.Background(), "myapp", true); err != nil {
		t.Fatalf("DeleteApplication: %v", err)
	}
	if seenPath != "/api/v1/applications/myapp" {
		t.Errorf("path = %s", seenPath)
	}
	if seenQuery != "cascade=true" {
		t.Errorf("query = %s", seenQuery)
	}
}

func TestCreateProject(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"metadata":{"name":"myproj"},"spec":{"description":"test"}}`))
	})

	proj, err := c.CreateProject(context.Background(), "myproj", AppProjectSpec{
		Description:  "test",
		SourceRepos:  []string{"*"},
		Destinations: []ApplicationDestination{{Server: "*", Namespace: "*"}},
		SyncWindows: []AppProjectSyncWindow{
			{
				Kind:         "deny",
				Schedule:     "0 22 * * 1-5",
				Duration:     "10h",
				Applications: []string{"*-prod"},
				ManualSync:   true,
				TimeZone:     "UTC",
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.Metadata.Name != "myproj" {
		t.Errorf("name = %s", proj.Metadata.Name)
	}
	if seenPath != "/api/v1/projects" {
		t.Errorf("path = %s", seenPath)
	}
	// AppProject create body wraps under "project".
	inner, ok := seenBody["project"].(map[string]any)
	if !ok {
		t.Fatalf("body missing 'project' key: %v", seenBody)
	}
	if md, ok := inner["metadata"].(map[string]any); !ok || md["name"] != "myproj" {
		t.Errorf("project.metadata.name missing: %v", seenBody)
	}
	if sp, ok := inner["spec"].(map[string]any); !ok || sp["description"] != "test" {
		t.Errorf("project.spec.description missing: %v", seenBody)
	} else {
		windows, ok := sp["syncWindows"].([]any)
		if !ok || len(windows) != 1 {
			t.Fatalf("project.spec.syncWindows missing: %v", seenBody)
		}
		first, _ := windows[0].(map[string]any)
		if first["kind"] != "deny" || first["schedule"] != "0 22 * * 1-5" || first["manualSync"] != true {
			t.Fatalf("project.spec.syncWindows[0] = %v", first)
		}
	}
}

func TestCreateApplicationSetWithClusterGenerator(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"metadata":{"name":"myset"},"spec":{}}`))
	})

	set, err := c.CreateApplicationSet(context.Background(), "myset", ApplicationSetSpec{
		Generators: []ApplicationSetGenerator{
			{
				Cluster: &ClusterGenerator{
					Selector: &LabelSelector{
						MatchLabels: map[string]string{
							"astronomer.io/environment": "prod",
						},
					},
				},
			},
		},
		Template: ApplicationSetTemplate{
			Metadata: ApplicationMetadata{Name: "{{name}}-myapp"},
			Spec: ApplicationSpec{
				Project: "default",
				Source: &ApplicationSource{
					RepoURL:        "https://example.com/repo",
					Path:           "manifests",
					TargetRevision: "HEAD",
				},
				Destination: &ApplicationDestination{
					Server:    "{{server}}",
					Namespace: "default",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateApplicationSet: %v", err)
	}
	if set.Metadata.Name != "myset" {
		t.Errorf("name = %s", set.Metadata.Name)
	}
	if seenPath != "/api/v1/applicationsets" {
		t.Errorf("path = %s", seenPath)
	}
	spec, ok := seenBody["spec"].(map[string]any)
	if !ok {
		t.Fatalf("missing spec: %v", seenBody)
	}
	gens, ok := spec["generators"].([]any)
	if !ok || len(gens) != 1 {
		t.Fatalf("missing generators: %v", spec)
	}
	gen0 := gens[0].(map[string]any)
	cluster, ok := gen0["clusters"].(map[string]any)
	if !ok {
		t.Fatalf("missing 'clusters' generator key: %v", gen0)
	}
	sel, ok := cluster["selector"].(map[string]any)
	if !ok {
		t.Fatalf("missing selector: %v", cluster)
	}
	if ml, ok := sel["matchLabels"].(map[string]any); !ok || ml["astronomer.io/environment"] != "prod" {
		t.Errorf("matchLabels missing label: %v", sel)
	}
}

func TestRegisterCluster(t *testing.T) {
	var seenPath string
	var seenQuery string
	var seenBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"name":"prod-cluster","server":"https://prod.example.com","labels":{"astronomer.io/environment":"prod"}}`))
	})

	out, err := c.RegisterCluster(context.Background(), ClusterRegistration{
		Name:   "prod-cluster",
		Server: "https://prod.example.com",
		Config: ClusterConfig{
			BearerToken: "k8s-sa-token",
			TLSClientConfig: &TLSClientConfig{
				CAData: []byte("---CA-BUNDLE---"),
			},
		},
		Labels: map[string]string{"astronomer.io/environment": "prod"},
		Upsert: true,
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if out.Name != "prod-cluster" {
		t.Errorf("name = %s", out.Name)
	}
	if seenPath != "/api/v1/clusters" {
		t.Errorf("path = %s", seenPath)
	}
	if seenQuery != "upsert=true" {
		t.Errorf("query = %s", seenQuery)
	}
	if seenBody["server"] != "https://prod.example.com" {
		t.Errorf("server = %v", seenBody["server"])
	}
	cfg, ok := seenBody["config"].(map[string]any)
	if !ok {
		t.Fatalf("missing config: %v", seenBody)
	}
	if cfg["bearerToken"] != "k8s-sa-token" {
		t.Errorf("bearerToken = %v", cfg["bearerToken"])
	}
	tlsCfg, ok := cfg["tlsClientConfig"].(map[string]any)
	if !ok {
		t.Fatalf("missing tlsClientConfig: %v", cfg)
	}
	// caData is base64-encoded by encoding/json for []byte.
	if tlsCfg["caData"] == nil {
		t.Errorf("caData missing: %v", tlsCfg)
	}
	if labels, ok := seenBody["labels"].(map[string]any); !ok || labels["astronomer.io/environment"] != "prod" {
		t.Errorf("labels missing: %v", seenBody)
	}
}

func TestPatchProjectUsesPUTAfterMerge(t *testing.T) {
	// PatchProject must read the current project, merge the patch, and PUT
	// the resulting full document — upstream ArgoCD rejects PATCH on
	// /api/v1/projects/{name} with 405.
	var calls []string
	var seenContentTypes []string
	var putBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		seenContentTypes = append(seenContentTypes, r.Header.Get("Content-Type"))
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"metadata":{"name":"myproj","resourceVersion":"42"},"spec":{"description":"old","sourceRepos":["*"]}}`))
		case http.MethodPut:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &putBody)
			_, _ = w.Write([]byte(`{"metadata":{"name":"myproj","resourceVersion":"43"},"spec":{"description":"new","sourceRepos":["*"]}}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	merge := json.RawMessage(`{"description":"new"}`)
	out, err := c.PatchProject(context.Background(), "myproj", merge)
	if err != nil {
		t.Fatalf("PatchProject: %v", err)
	}
	if out.Spec.Description != "new" {
		t.Errorf("description = %q; want new", out.Spec.Description)
	}
	if got := strings.Join(calls, ","); got != "GET /api/v1/projects/myproj,PUT /api/v1/projects/myproj" {
		t.Errorf("call sequence = %s", got)
	}
	for i, ct := range seenContentTypes {
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("call %d Content-Type = %q (want application/json)", i, ct)
		}
	}
	proj, ok := putBody["project"].(map[string]any)
	if !ok {
		t.Fatalf("PUT body missing project envelope: %v", putBody)
	}
	meta, ok := proj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("PUT body missing metadata: %v", proj)
	}
	if meta["resourceVersion"] != "42" {
		t.Errorf("PUT did not echo resourceVersion from GET; got %v", meta["resourceVersion"])
	}
	spec, ok := proj["spec"].(map[string]any)
	if !ok {
		t.Fatalf("PUT body missing spec: %v", proj)
	}
	if spec["description"] != "new" {
		t.Errorf("merged description = %v", spec["description"])
	}
	repos, ok := spec["sourceRepos"].([]any)
	if !ok || len(repos) != 1 || repos[0] != "*" {
		t.Errorf("merged sourceRepos = %v (must preserve original)", spec["sourceRepos"])
	}
}

func TestPatchProjectRejectsUnsafeMergedSourceReposBeforePUT(t *testing.T) {
	putCalls := 0
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"metadata":{"name":"myproj","resourceVersion":"42"},"spec":{"description":"old","sourceRepos":["https://user:pass@git.example/repo"]}}`))
		case http.MethodPut:
			putCalls++
		}
	})
	if _, err := c.PatchProject(context.Background(), "myproj", json.RawMessage(`{"description":"new"}`)); err == nil {
		t.Fatal("unsafe merged sourceRepos accepted")
	}
	if putCalls != 0 {
		t.Fatalf("unsafe merged project issued %d PUT calls", putCalls)
	}
}

func TestTypedWriteClientsRejectCredentialURLsBeforeUpstream(t *testing.T) {
	calls := 0
	c, _ := newLifecycleClient(t, func(http.ResponseWriter, *http.Request) { calls++ })
	unsafe := "https://user:pass@example.test/path?sig=secret#fragment"
	if _, err := c.CreateRepository(context.Background(), RepositoryCreate{Repo: unsafe}); err == nil {
		t.Fatal("unsafe repository URL accepted")
	}
	if _, err := c.TestRepository(context.Background(), RepositoryCreate{Repo: unsafe}); err == nil {
		t.Fatal("unsafe repository test URL accepted")
	}
	if err := c.DeleteRepository(context.Background(), unsafe); err == nil {
		t.Fatal("unsafe repository delete URL accepted")
	}
	if _, err := c.RegisterCluster(context.Background(), ClusterRegistration{Server: unsafe}); err == nil {
		t.Fatal("unsafe cluster server accepted")
	}
	if _, err := c.CreateProject(context.Background(), "demo", AppProjectSpec{SourceRepos: []string{unsafe}}); err == nil {
		t.Fatal("unsafe project sourceRepos accepted")
	}
	if calls != 0 {
		t.Fatalf("unsafe writes made %d upstream calls", calls)
	}
}

func TestTypedClientsAllowSafeGitTransportAndMultiSourceValueRepository(t *testing.T) {
	var calls []string
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "repositories"):
			_, _ = w.Write([]byte(`{"repo":"git@github.com:team/repo.git"}`))
		default:
			_, _ = w.Write([]byte(`{"metadata":{"name":"multi"}}`))
		}
	})
	if _, err := c.CreateRepository(context.Background(), RepositoryCreate{Repo: "git@github.com:team/repo.git"}); err != nil {
		t.Fatalf("safe SCP repository rejected: %v", err)
	}
	if _, err := c.CreateApplication(context.Background(), "multi", ApplicationSpec{
		Project: "default",
		Sources: []ApplicationSource{
			{RepoURL: "https://charts.example/repo", Chart: "platform", Helm: &HelmSource{ValueFiles: []string{"$values/prod.yaml", "defaults.yaml"}}},
			{RepoURL: "ssh://git@git.example/team/values.git", TargetRevision: "main", Ref: "values"},
		},
		Destination: &ApplicationDestination{Server: "https://kube.example:6443", Namespace: "prod"},
	}); err != nil {
		t.Fatalf("safe multi-source value repository rejected: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("upstream calls=%v", calls)
	}
}

func TestTypedApplicationRejectsUnsafeMultiSourceValueRepositoryBeforeUpstream(t *testing.T) {
	calls := 0
	c, _ := newLifecycleClient(t, func(http.ResponseWriter, *http.Request) { calls++ })
	if _, err := c.CreateApplication(context.Background(), "multi", ApplicationSpec{
		Project: "default",
		Sources: []ApplicationSource{
			{RepoURL: "https://charts.example/repo", Chart: "platform", Helm: &HelmSource{ValueFiles: []string{"$values/../secret.yaml"}}},
			{RepoURL: "https://git.example/values", Ref: "values"},
		},
	}); err == nil {
		t.Fatal("unsafe typed multi-source value repository accepted")
	}
	if calls != 0 {
		t.Fatalf("unsafe typed application made %d upstream calls", calls)
	}
}

func TestDeleteProjectAlwaysSendsContentType(t *testing.T) {
	// Bodyless DELETEs against ArgoCD must still carry Content-Type: application/json
	// or upstream returns 415.
	var seenCT string
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		seenCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	})
	if err := c.DeleteProject(context.Background(), "myproj"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if !strings.HasPrefix(seenCT, "application/json") {
		t.Errorf("Content-Type on DELETE = %q; want application/json", seenCT)
	}
}

func TestUnregisterClusterURLEncodesServer(t *testing.T) {
	var seenURI string
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		// r.URL.Path is decoded; r.RequestURI is the raw on-wire path.
		seenURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
	})
	if err := c.UnregisterCluster(context.Background(), "https://prod.example.com:6443"); err != nil {
		t.Fatalf("UnregisterCluster: %v", err)
	}
	if !strings.Contains(seenURI, "%2F%2F") {
		t.Errorf("request URI = %s; expected URL-encoded slashes", seenURI)
	}
	if !strings.HasPrefix(seenURI, "/api/v1/clusters/") {
		t.Errorf("request URI = %s; expected /api/v1/clusters/ prefix", seenURI)
	}
}

func TestCreateRepository(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	c, _ := newLifecycleClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"repo":"https://example.com/repo","type":"git","connectionState":{"status":"Successful"}}`))
	})

	out, err := c.CreateRepository(context.Background(), RepositoryCreate{
		Repo:     "https://example.com/repo",
		Type:     "git",
		Username: "alice",
		Password: "s3cret",
	})
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	if out.ConnectionState.Status != "Successful" {
		t.Errorf("status = %s", out.ConnectionState.Status)
	}
	if seenPath != "/api/v1/repositories" {
		t.Errorf("path = %s", seenPath)
	}
	if seenBody["password"] != "s3cret" {
		t.Errorf("password not forwarded: %v", seenBody)
	}
}
