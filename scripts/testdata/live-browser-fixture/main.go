// live-browser-fixture supplies only the external cluster edges that the live
// browser tier cannot get from PostgreSQL/Redis: an authenticated agent tunnel
// and deterministic CIS/Loki Kubernetes responses. It also seeds a valid
// delivery graph through the production planner; product behavior remains the
// real server, worker, database, and UI.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/agent"
	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

const (
	fixtureProjectName = "live-browser-enterprise"
	fixtureOutputName  = "Astronomer Loki (live fixture)"
)

func main() {
	if len(os.Args) != 2 {
		fatalf("usage: live-browser-fixture agent|seed|direct-rbac")
	}
	var err error
	switch os.Args[1] {
	case "agent":
		err = runAgent()
	case "seed":
		err = seed()
	case "direct-rbac":
		err = renderDirectRBAC()
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

const directReaderManifest = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: astronomer-direct-reader
  namespace: astronomer-system
automountServiceAccountToken: false
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: astronomer-direct-reader
rules:
%s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: astronomer-direct-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: astronomer-direct-reader
subjects:
  - kind: ServiceAccount
    name: astronomer-direct-reader
    namespace: astronomer-system
`

func renderDirectRBAC() error {
	_, err := fmt.Printf(directReaderManifest, agenttemplate.RBACRulesYAML(agenttemplate.PrivilegeProfileViewer))
	return err
}

func runAgent() error {
	serverURL := requiredEnv("LIVE_FIXTURE_SERVER_URL")
	clusterID := requiredEnv("LIVE_FIXTURE_CLUSTER_ID")
	token := requiredEnv("LIVE_FIXTURE_AGENT_TOKEN")
	if serverURL == "" || clusterID == "" || token == "" {
		return errors.New("agent fixture environment is incomplete")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := &agent.AgentConfig{
		ServerURL: serverURL, ClusterID: clusterID, AgentToken: token,
		AgentID: "live-browser-fixture", CredentialSource: agent.CredentialSourceEnvironment,
		ReconnectBackoff: 1, MaxReconnect: 2, HeartbeatInterval: 5,
		PrivilegeProfile: "admin", MaxInflightRequests: 8,
	}
	client := agent.NewTunnelClient(cfg, log)
	restConfig, err := clientcmd.BuildConfigFromFlags("", requiredEnv("LIVE_FIXTURE_KUBECONFIG"))
	if err != nil {
		return fmt.Errorf("load disposable cluster kubeconfig: %w", err)
	}
	proxy, err := agent.NewK8sProxyWithConfig(restConfig, log)
	if err != nil {
		return fmt.Errorf("initialize disposable cluster proxy: %w", err)
	}
	fixture := &k8sFixture{proxy: proxy}
	client.RegisterHandler(protocol.MsgK8sRequest, fixture.handle)
	client.RegisterHandler(protocol.MsgDecommission, fixture.decommission)

	deliveryDynamic, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("initialize delivery dynamic client: %w", err)
	}
	deliveryExecutor, err := agentdelivery.NewExecutor(deliveryDynamic)
	if err != nil {
		return err
	}
	deliveryStore, err := agentdelivery.NewKubernetesCheckpointStore(proxy.Client(), agent.DefaultAgentNamespace)
	if err != nil {
		return err
	}
	deliveryProbe, err := agentdelivery.NewClusterProbe(proxy.Client(), proxy.Client().Discovery(), true)
	if err != nil {
		return err
	}
	deliveryRuntime, err := agentdelivery.NewRuntime(agentdelivery.RuntimeConfig{
		ClusterID: clusterID, AgentVersion: version.Version,
		PollInterval: time.Second, StatusInterval: time.Second,
		ValidationPolicy: agentdelivery.ValidationPolicy{AllowPlatformScope: true},
		Connected:        client.IsConnected, Logger: log,
	}, deliveryExecutor, deliveryStore, deliveryProbe)
	if err != nil {
		return err
	}
	client.RegisterHandler(protocol.MsgDeliveryStateResponse, deliveryRuntime.HandleStateResponse)
	client.RegisterHandler(protocol.MsgDeliveryReconcile, deliveryRuntime.HandleReconcile)
	mirror := agent.NewMirrorSubscriber(proxy.Client(), deliveryDynamic, client, log)
	mirror.SetConnectionWatcher(client)
	go mirror.Run(ctx)
	go func() {
		if err := deliveryRuntime.Run(ctx, client.SendFunc(ctx)); err != nil && ctx.Err() == nil {
			log.Error("live delivery runtime stopped", "error", err)
			stop()
		}
	}()
	client.SetConnectionListener(func(connected bool) {
		if connected {
			log.Info("live browser fixture ready", "cluster_id", clusterID)
		} else {
			log.Warn("live browser fixture disconnected", "cluster_id", clusterID)
		}
	})
	return client.Connect(ctx)
}

type k8sFixture struct {
	mu       sync.Mutex
	scanName string
	proxy    *agent.K8sProxy
}

func (f *k8sFixture) handle(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	var req protocol.K8sRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, fmt.Errorf("decode k8s fixture request: %w", err)
	}
	if !isSyntheticEdge(req) {
		return f.proxy.HandleRequest(ctx, msg)
	}
	status, body := f.response(req)
	wire, err := json.Marshal(protocol.K8sResponsePayload{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       base64.StdEncoding.EncodeToString([]byte(body)),
	})
	if err != nil {
		return nil, err
	}
	return &protocol.Message{Type: protocol.MsgK8sResponse, StreamID: msg.StreamID, Timestamp: time.Now().UTC(), Payload: wire}, nil
}

func isSyntheticEdge(req protocol.K8sRequestPayload) bool {
	return strings.Contains(req.Path, "cis.cattle.io") || strings.Contains(req.Path, "/proxy/loki/")
}

func (f *k8sFixture) response(req protocol.K8sRequestPayload) (int, string) {
	switch {
	case req.Method == http.MethodGet && req.Path == "/apis/cis.cattle.io/v1/clusterscanprofiles":
		return http.StatusOK, `{"items":[{"metadata":{"name":"cis-1.8"},"spec":{"benchmarkVersion":"1.8"}}]}`
	case req.Method == http.MethodPost && req.Path == "/apis/cis.cattle.io/v1/clusterscans":
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return http.StatusBadRequest, `{"message":"invalid fixture body"}`
		}
		var object struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if json.Unmarshal(decoded, &object) != nil || object.Metadata.Name == "" {
			return http.StatusBadRequest, `{"message":"missing scan name"}`
		}
		f.mu.Lock()
		f.scanName = object.Metadata.Name
		f.mu.Unlock()
		return http.StatusCreated, string(decoded)
	case req.Method == http.MethodGet && strings.HasPrefix(req.Path, "/apis/cis.cattle.io/v1/clusterscans/"):
		name := strings.TrimPrefix(req.Path, "/apis/cis.cattle.io/v1/clusterscans/")
		f.mu.Lock()
		known := f.scanName == name
		f.mu.Unlock()
		if !known {
			return http.StatusNotFound, `{"message":"scan not found"}`
		}
		return http.StatusOK, fmt.Sprintf(`{"metadata":{"name":%q},"status":{"reportName":%q}}`, name, name+"-report")
	case req.Method == http.MethodGet && strings.HasPrefix(req.Path, "/apis/cis.cattle.io/v1/clusterscanreports/"):
		// Hold the worker request briefly so the redirected detail page has a
		// truthful observable running state before the durable completion lands.
		time.Sleep(2 * time.Second)
		name := strings.TrimPrefix(req.Path, "/apis/cis.cattle.io/v1/clusterscanreports/")
		reportJSON := `{"total":2,"pass":1,"fail":1,"warn":0,"skip":0,"results":[{"section":"1","checks":[{"id":"1.1.1","description":"API server audit logging enabled","state":"pass"},{"id":"1.2.1","description":"Anonymous authentication disabled","state":"fail","severity":"high"}]}]}`
		return http.StatusOK, fmt.Sprintf(`{"metadata":{"name":%q},"spec":{"reportJSON":%q}}`, name, reportJSON)
	case req.Method == http.MethodGet && strings.Contains(req.Path, "/proxy/loki/api/v1/query_range?"):
		return http.StatusOK, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"cluster":"live-browser","namespace":"payments"},"values":[["1770000000000000000","enterprise-live-query-result"]]}]}}`
	default:
		return http.StatusNotFound, fmt.Sprintf(`{"message":"fixture has no response for %s %s"}`, req.Method, req.Path)
	}
}

func (f *k8sFixture) decommission(_ context.Context, msg *protocol.Message) (*protocol.Message, error) {
	var req protocol.DecommissionPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, err
	}
	steps := []protocol.DecommissionStepResult{
		{Name: "remove_velero_managed", Success: true},
		{Name: "remove_logging_stack", Success: true},
		{Name: "remove_baseline_namespaces", Success: true, Removed: 6},
		{Name: "remove_cluster_rbac", Success: true, Removed: 2},
		{Name: "remove_agent_singletons", Success: true, Removed: 3},
		{Name: "remove_agent_deployment", Success: true, Removed: 1},
	}
	payload, err := json.Marshal(protocol.DecommissionAckPayload{ClusterID: req.ClusterID, DryRun: req.DryRun, Steps: steps})
	if err != nil {
		return nil, err
	}
	return &protocol.Message{Type: protocol.MsgDecommissionAck, StreamID: msg.StreamID, RequestID: msg.RequestID, ClusterID: msg.ClusterID, Timestamp: time.Now().UTC(), Payload: payload}, nil
}

type fixtureIDs struct {
	ProjectID         string `json:"project_id"`
	RolloutID         string `json:"rollout_id"`
	RollbackRolloutID string `json:"rollback_rollout_id"`
	TrivyRolloutID    string `json:"trivy_rollout_id,omitempty"`
	TrivyTargetID     string `json:"trivy_target_id,omitempty"`
	Output            string `json:"logging_output_name"`
}

type optionalTrivyTarget struct {
	id         uuid.UUID
	generation uint64
}

func seed() error {
	databaseURL := requiredEnv("DATABASE_URL")
	clusterID, err := uuid.Parse(requiredEnv("LIVE_FIXTURE_CLUSTER_ID"))
	if err != nil {
		return fmt.Errorf("parse cluster id: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	projectID, sourceID, bundleID := uuid.New(), uuid.New(), uuid.New()
	versionID, previousVersionID, targetID := uuid.New(), uuid.New(), uuid.New()
	sourceURL := requiredEnv("LIVE_FIXTURE_GIT_URL")
	revision := requiredEnv("LIVE_FIXTURE_GIT_REVISION")
	digest := requiredEnv("LIVE_FIXTURE_GIT_DIGEST")
	encryptor, err := auth.NewEncryptor(requiredEnv("ASTRONOMER_ENCRYPTION_KEY"))
	if err != nil {
		return fmt.Errorf("initialize fixture encryptor: %w", err)
	}
	encryptedCA, err := encryptor.Encrypt(requiredEnv("LIVE_FIXTURE_GIT_CA"))
	if err != nil {
		return fmt.Errorf("encrypt fixture Git CA: %w", err)
	}
	previousDigest := digest
	previousRevision := revision
	specDigest := "sha256:" + strings.Repeat("a", 64)
	previousSpecDigest := "sha256:" + strings.Repeat("c", 64)
	controllerImages, err := fluxdistribution.ControllerImages()
	if err != nil {
		return fmt.Errorf("read pinned Flux inventory: %w", err)
	}
	controllerVersions := make(map[string]string, len(controllerImages))
	for name, image := range controllerImages {
		controllerVersions[name] = image.Version
	}
	controllerVersionsJSON, err := json.Marshal(controllerVersions)
	if err != nil {
		return fmt.Errorf("encode pinned Flux inventory: %w", err)
	}
	sourceSpec, _ := json.Marshal(model.ResolvedSourceSpec{
		SourceID: sourceID, Type: model.SourceGit, URL: sourceURL, AuthMode: model.AuthNone,
		Trust:    model.TrustPolicy{AllowUnsigned: true},
		Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: revision, ArtifactDigest: model.Digest(digest)},
	})
	previousSourceSpec, _ := json.Marshal(model.ResolvedSourceSpec{
		SourceID: sourceID, Type: model.SourceGit, URL: sourceURL, AuthMode: model.AuthNone,
		Trust:    model.TrustPolicy{AllowUnsigned: true},
		Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: previousRevision, ArtifactDigest: model.Digest(previousDigest)},
	})
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO projects (id,name,display_name,cluster_id) VALUES ($1,$2,$3,$4)`, []any{projectID, fixtureProjectName, "Live Browser Enterprise", clusterID}},
		{`INSERT INTO delivery_controller_inventory (cluster_id,flux_version,components,ready,compatibility_status) VALUES ($1,$2,$3,true,'compatible') ON CONFLICT (cluster_id) DO UPDATE SET flux_version=EXCLUDED.flux_version,components=EXCLUDED.components,ready=true,compatibility_status='compatible'`, []any{clusterID, fluxdistribution.Version(), controllerVersionsJSON}},
		{`INSERT INTO delivery_sources (id,project_id,name,source_type,url,ca_bundle_encrypted,credential_epoch,trust_policy,status) VALUES ($1,$2,'live-browser-source','git',$3,$4,1,'{"allow_unsigned":true}','ready')`, []any{sourceID, projectID, sourceURL, encryptedCA}},
		{`INSERT INTO component_bundles (id,project_id,name) VALUES ($1,$2,'live-browser-bundle')`, []any{bundleID, projectID}},
		{`INSERT INTO component_bundle_versions (id,bundle_id,source_id,version,renderer,requested_revision,resolved_revision,artifact_digest,source_spec,renderer_spec,reconciliation_policy,requirements,spec_digest,verification_status,state) VALUES ($1,$2,$3,'v1','kustomize',$4,$4,$5,$6,'{"kind":"kustomize","kustomize":{"path":"./kustomize","target_namespace":"live-delivery"}}','{"interval":"5s","retry_interval":"5s","timeout":"2m0s","prune":true,"wait":true,"drift":"repair"}','[]',$7,'verified','ready')`, []any{versionID, bundleID, sourceID, revision, digest, sourceSpec, specDigest}},
		{`INSERT INTO component_bundle_versions (id,bundle_id,source_id,version,renderer,requested_revision,resolved_revision,artifact_digest,source_spec,renderer_spec,reconciliation_policy,requirements,spec_digest,verification_status,state) VALUES ($1,$2,$3,'v0','kustomize',$4,$4,$5,$6,'{"kind":"kustomize","kustomize":{"path":"./kustomize","target_namespace":"live-delivery"}}','{"interval":"5s","retry_interval":"5s","timeout":"2m0s","prune":true,"wait":true,"drift":"repair"}','[]',$7,'verified','ready')`, []any{previousVersionID, bundleID, sourceID, previousRevision, previousDigest, previousSourceSpec, previousSpecDigest}},
		{`INSERT INTO delivery_targets (id,project_id,name,bundle_version_id,placement,rollout_policy) VALUES ($1,$2,'live-browser-target',$3,'{"all_clusters":true}','{"approval_required":false}')`, []any{targetID, projectID, versionID}},
		{`INSERT INTO cluster_deployments (target_id,cluster_id,desired_bundle_version_id,desired_generation,desired_spec_digest,desired_revision,action,phase) VALUES ($1,$2,$3,1,$4,$5,'apply','ready')`, []any{targetID, clusterID, previousVersionID, previousSpecDigest, previousRevision}},
		{`INSERT INTO monitoring_backends (name,backend_type,query_url,is_default,auth_config) VALUES ('default','thanos','http://unused.invalid',true,jsonb_build_object('sharedLoki',jsonb_build_object('status','healthy','managementClusterId',$1::text,'namespace','monitoring','releaseName','astronomer-loki'))) ON CONFLICT (name) DO UPDATE SET is_default=true,auth_config=EXCLUDED.auth_config`, []any{clusterID}},
		{`INSERT INTO logging_outputs (name,output_type,configuration,cluster_id,enabled,is_system) VALUES ($1,'loki','{}',$2,true,true)`, []any{fixtureOutputName, clusterID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("seed live browser fixture: %w", err)
		}
	}
	var trivyTarget optionalTrivyTarget
	if os.Getenv("LIVE_FIXTURE_TRIVY_ENABLED") == "1" {
		trivyTarget, err = seedTrivyTarget(ctx, pool, projectID, clusterID, encryptedCA)
		if err != nil {
			return err
		}
	}
	store, err := deliveryrollout.NewPostgresPlanningStore(pool)
	if err != nil {
		return err
	}
	_, preview, err := store.Preview(ctx, targetID)
	if err != nil {
		return fmt.Errorf("preview delivery fixture: %w", err)
	}
	planner, err := deliveryrollout.NewPlanner(store, time.Now, uuid.New)
	if err != nil {
		return err
	}
	plan, err := planner.Create(ctx, deliveryrollout.CreateRequest{
		TargetID: targetID, ExpectedTargetGeneration: 1, PreviewDigest: preview.PreviewDigest,
		ConfirmAllClusters: true, Actor: "live-browser-fixture", IdempotencyKey: "live-browser-rollout",
		Strategy: model.RolloutStrategy{
			Type: model.StrategyRolling, MaxConcurrent: 1,
			MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
			ProgressDeadline: model.Duration(10 * time.Minute),
			FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, OnFailure: model.FailurePause,
		},
	})
	if err != nil {
		return fmt.Errorf("plan delivery fixture: %w", err)
	}
	rollbackPlan, err := planner.Create(ctx, deliveryrollout.CreateRequest{
		TargetID: targetID, ExpectedTargetGeneration: 1, PreviewDigest: preview.PreviewDigest,
		ConfirmAllClusters: true, Actor: "live-browser-fixture", IdempotencyKey: "live-browser-rollback",
		Strategy: model.RolloutStrategy{
			Type: model.StrategyRolling, MaxConcurrent: 1,
			MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
			ProgressDeadline: model.Duration(10 * time.Minute),
			FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, OnFailure: model.FailurePause,
		},
	})
	if err != nil {
		return fmt.Errorf("plan rollback fixture: %w", err)
	}
	var trivyRolloutID string
	if trivyTarget.id != uuid.Nil {
		_, trivyPreview, err := store.Preview(ctx, trivyTarget.id)
		if err != nil {
			return fmt.Errorf("preview Trivy delivery fixture: %w", err)
		}
		trivyPlan, err := planner.Create(ctx, deliveryrollout.CreateRequest{
			TargetID: trivyTarget.id, ExpectedTargetGeneration: trivyTarget.generation,
			PreviewDigest: trivyPreview.PreviewDigest, ConfirmAllClusters: true,
			Actor: "live-browser-fixture", IdempotencyKey: "live-browser-trivy-rollout",
			Strategy: model.RolloutStrategy{
				Type: model.StrategyRolling, MaxConcurrent: 1,
				MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
				ProgressDeadline: model.Duration(12 * time.Minute),
				FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, OnFailure: model.FailurePause,
			},
		})
		if err != nil {
			return fmt.Errorf("plan Trivy delivery fixture: %w", err)
		}
		trivyRolloutID = trivyPlan.ID.String()
	}
	// Keep the rollout in resolving until the browser explicitly pauses it.
	// The pause action creates the next real durable reconcile intent.
	if _, err := pool.Exec(ctx, `DELETE FROM task_outbox WHERE dedupe_key=$1`, "delivery-rollout-initial:"+plan.ID.String()); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM task_outbox WHERE dedupe_key=$1`, "delivery-rollout-initial:"+rollbackPlan.ID.String()); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_rollouts SET state='succeeded', ready_clusters=total_clusters, completed_at=now() WHERE id=$1`, rollbackPlan.ID); err != nil {
		return err
	}
	trivyTargetID := ""
	if trivyTarget.id != uuid.Nil {
		trivyTargetID = trivyTarget.id.String()
	}
	return json.NewEncoder(os.Stdout).Encode(fixtureIDs{
		ProjectID: projectID.String(), RolloutID: plan.ID.String(), RollbackRolloutID: rollbackPlan.ID.String(),
		TrivyRolloutID: trivyRolloutID, TrivyTargetID: trivyTargetID, Output: fixtureOutputName,
	})
}

func seedTrivyTarget(ctx context.Context, pool *pgxpool.Pool, projectID, clusterID uuid.UUID, encryptedCA string) (optionalTrivyTarget, error) {
	sourceID, bundleID, versionID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	sourceURL := requiredEnv("LIVE_FIXTURE_TRIVY_HELM_URL")
	chartVersion := requiredEnv("LIVE_FIXTURE_TRIVY_CHART_VERSION")
	chartDigest := requiredEnv("LIVE_FIXTURE_TRIVY_CHART_DIGEST")
	values, err := json.Marshal(map[string]any{
		"targetNamespaces": "live-delivery",
		"image":            map[string]any{"tag": "0.34.0@sha256:0e4f11e9632f34097f259f3a59d34bab4eea8cee9aef510d15cdfc7481d5e49c"},
		"operator": map[string]any{
			"vulnerabilityScannerEnabled": true, "sbomGenerationEnabled": false,
			"configAuditScannerEnabled": false, "rbacAssessmentScannerEnabled": false,
			"infraAssessmentScannerEnabled": false, "clusterComplianceEnabled": false,
			"scanJobsConcurrentLimit": 1, "scanJobTimeout": "5m", "scannerReportTTL": "24h",
		},
		"trivy": map[string]any{
			"image":         map[string]any{"tag": "0.74.0@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969"},
			"ignoreUnfixed": false,
		},
	})
	if err != nil {
		return optionalTrivyTarget{}, err
	}
	reconciliation := model.ReconciliationPolicy{
		Interval: model.Duration(5 * time.Second), RetryInterval: model.Duration(5 * time.Second),
		Timeout: model.Duration(8 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
	}
	requirements := []model.CapabilityRequirement{
		{Name: protocol.FeatureDeliverySourceHelmHTTP},
		{Name: protocol.FeatureDeliveryRendererHelm},
		{Name: protocol.FeatureDeliveryPlatformScope},
	}
	renderer := model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{
		Chart: "trivy-operator", ChartVersion: chartVersion, ReleaseName: "trivy-operator",
		TargetNamespace: "astronomer-trivy-system", Values: values, InstallRetries: 3, UpgradeRetries: 3,
	}}
	draft := model.BundleVersionDraft{
		SourceID: sourceID, RequestedRevision: chartVersion, Renderer: renderer,
		Scope: model.ScopePlatform, Reconciliation: reconciliation, RequiredCapabilities: requirements,
	}
	artifactDigest, err := model.ParseDigest(chartDigest)
	if err != nil {
		return optionalTrivyTarget{}, fmt.Errorf("parse Trivy chart digest: %w", err)
	}
	revision := model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: chartVersion, ArtifactDigest: artifactDigest}
	resolved, err := draft.Resolve(revision)
	if err != nil {
		return optionalTrivyTarget{}, fmt.Errorf("validate Trivy delivery fixture: %w", err)
	}
	specDigest, err := model.CanonicalDigest(struct {
		Spec         model.BundleVersionSpec `json:"spec"`
		Dependencies []uuid.UUID             `json:"dependency_bundle_ids"`
	}{resolved, []uuid.UUID{}})
	if err != nil {
		return optionalTrivyTarget{}, err
	}
	trust := model.TrustPolicy{AllowUnsigned: true}
	sourceSpec, _ := json.Marshal(model.ResolvedSourceSpec{
		SourceID: sourceID, Type: model.SourceHelmHTTP, URL: sourceURL, AuthMode: model.AuthNone,
		Trust: trust, Revision: revision,
	})
	rendererJSON, _ := json.Marshal(renderer)
	reconciliationJSON, _ := json.Marshal(reconciliation)
	requirementsJSON, _ := json.Marshal(requirements)
	placementJSON, _ := json.Marshal(model.Placement{ProjectIDs: []uuid.UUID{projectID}, ClusterIDs: []uuid.UUID{clusterID}})
	trustJSON, _ := json.Marshal(trust)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO delivery_sources (id,project_id,name,description,source_type,url,auth_mode,ca_bundle_encrypted,credential_epoch,trust_policy,status,last_resolved_at) VALUES ($1,$2,'live-browser-trivy-source','Optional live Trivy Helm repository','helm_http',$3,'none',$4,1,$5,'ready',now())`, []any{sourceID, projectID, sourceURL, encryptedCA, trustJSON}},
		{`INSERT INTO component_bundles (id,project_id,name,description) VALUES ($1,$2,'live-browser-trivy','Optional Flux-native Trivy operator proof')`, []any{bundleID, projectID}},
		{`INSERT INTO component_bundle_versions (id,bundle_id,source_id,version,renderer,scope,requested_revision,resolved_revision,artifact_digest,source_spec,renderer_spec,reconciliation_policy,health_policy,requirements,dependency_bundle_ids,spec_digest,verification_status,verification_identity,state) VALUES ($1,$2,$3,$4,'helm','platform',$5,$5,$6,$7,$8,$9,'{}',$10,'[]',$11,'verified','live-browser-pinned-chart','ready')`, []any{versionID, bundleID, sourceID, "trivy-operator-" + chartVersion, chartVersion, chartDigest, sourceSpec, rendererJSON, reconciliationJSON, requirementsJSON, specDigest.String()}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			return optionalTrivyTarget{}, fmt.Errorf("seed Trivy delivery fixture: %w", err)
		}
	}
	var generation int64
	if err := pool.QueryRow(ctx, `INSERT INTO delivery_targets (id,project_id,name,description,bundle_version_id,placement,rollout_policy,reconciliation_policy,maintenance_window_policy,suspended) VALUES ($1,$2,'live-browser-trivy-target','Optional Flux-native Trivy operator',$3,$4,'{"approval_required":false}',$5,'{}',false) RETURNING generation`, targetID, projectID, versionID, placementJSON, reconciliationJSON).Scan(&generation); err != nil {
		return optionalTrivyTarget{}, fmt.Errorf("seed Trivy delivery target: %w", err)
	}
	if generation < 1 {
		return optionalTrivyTarget{}, fmt.Errorf("seed Trivy delivery target: invalid generation %d", generation)
	}
	return optionalTrivyTarget{id: targetID, generation: uint64(generation)}, nil
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		fmt.Fprintf(os.Stderr, "live-browser-fixture: %s is required\n", name)
	}
	return value
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "live-browser-fixture: "+format+"\n", args...)
	os.Exit(1)
}
