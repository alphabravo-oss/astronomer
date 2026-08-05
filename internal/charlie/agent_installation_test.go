package charlie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type fakeAgentBridge struct {
	calls      []string
	status     AgentBridgeStatus
	revokeFail bool
}

func (b *fakeAgentBridge) Status(context.Context) (AgentBridgeStatus, error) {
	b.calls = append(b.calls, "status")
	return b.status, nil
}
func (b *fakeAgentBridge) CentralHealth(context.Context) error {
	b.calls = append(b.calls, "central-health")
	return nil
}
func (b *fakeAgentBridge) VerifyArtifactDigests(context.Context, string, string) error {
	b.calls = append(b.calls, "artifact-digests")
	return nil
}
func (b *fakeAgentBridge) Disable(context.Context) error {
	b.calls = append(b.calls, "disable")
	return nil
}
func (b *fakeAgentBridge) StopTriggerDispatch(context.Context) error {
	b.calls = append(b.calls, "stop-triggers")
	return nil
}
func (b *fakeAgentBridge) SettleStreams(context.Context) error {
	b.calls = append(b.calls, "settle-streams")
	return nil
}
func (b *fakeAgentBridge) VerifyCredentialRevoked(context.Context, string, string) error {
	b.calls = append(b.calls, "credential-revoked")
	if b.revokeFail {
		return errors.New("still valid")
	}
	return nil
}

type fakeAgentMetadata struct {
	events []string
}

func (m *fakeAgentMetadata) MarkTemporarilyUninstalled(context.Context, uuid.UUID) error {
	m.events = append(m.events, "uninstalled")
	return nil
}
func (m *fakeAgentMetadata) MarkReconnected(context.Context, uuid.UUID) error {
	m.events = append(m.events, "reconnected")
	return nil
}
func (m *fakeAgentMetadata) MarkDisconnected(context.Context, uuid.UUID) error {
	m.events = append(m.events, "disconnected")
	return nil
}

func testAgentInstaller(t *testing.T) (*AgentInstaller, *kubefake.Clientset, *fakeAgentBridge, *fakeAgentMetadata) {
	t.Helper()
	kube := kubefake.NewClientset()
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		kubeutil.ArgoApplicationGVR: "ApplicationList",
	})
	bridge := &fakeAgentBridge{status: AgentBridgeStatus{
		BridgeReady: true, CentralEnrolled: true, LeaderElected: true, StandbyVisible: true, ProtocolCompatible: true,
		AgentProtocolVersion: contract.AgentProtocolVersion, BridgeProtocolVersion: contract.BridgeProtocolVersion,
	}}
	metadata := &fakeAgentMetadata{}
	installer, err := NewAgentInstaller(kube, dyn, AgentInstallerConfig{
		AgentNamespace: DefaultCharlieAgentNamespace, ArgoNamespace: "astronomer", ProductNamespace: "astronomer",
		Bridge: bridge, Metadata: metadata, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return installer, kube, bridge, metadata
}

func testAgentInstallSpec(t *testing.T) AgentInstallSpec {
	t.Helper()
	key, _ := auth.GenerateKey()
	encryptor, _ := auth.NewEncryptor(key)
	installationID := uuid.MustParse("3c608d44-848c-45d6-bd86-246be0b880af")
	trust, err := GenerateLocalTrust(encryptor, LocalTrustConfig{
		InstallationID:  installationID.String(),
		BridgeServerDNS: "charlie-agent-bridge.astronomer-charlie.svc",
		MCPServerDNS:    "astronomer-charlie-mcp.astronomer.svc",
		Now:             time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	actionSigningKey := make([]byte, 32)
	for index := range actionSigningKey {
		actionSigningKey[index] = byte(index + 1)
	}
	onboarding := newOnboardingFixture(t).signed(t)
	return AgentInstallSpec{
		InstallationID: installationID, ConnectionID: uuid.New(), LogicalAgentID: "agent-1",
		EnvironmentID: "production", TenantID: "tenant-1", CentralURL: "https://charlie.example.test",
		CentralCAPEM: "central-ca-fixture", ChartReference: "oci://charlie.example.test/charlie/agent-chart",
		ChartVersion: "1.0.0", ChartDigest: digest,
		ImageReference: "charlie.example.test/charlie/agent@" + digest, ImageDigest: digest,
		OnboardingPackage: onboarding, ReplicaCount: 2, ArtifactCredential: "artifact-secret-value-0000000000001",
		DisclosureDigest: strings.Repeat("a", 64), SecretIntegrityHMAC: strings.Repeat("b", 64),
		ActionSigningPublicKey: actionSigningKey, ActionSigningKeyFingerprint: digestBytes(actionSigningKey),
		Trust: trust, CentralCIDRs: []string{"203.0.113.10/32"},
	}
}

func TestAgentInstallerCreatesPrivateDigestPinnedAgentOnly(t *testing.T) {
	installer, kube, _, _ := testAgentInstaller(t)
	spec := testAgentInstallSpec(t)
	safeSpec, _ := json.Marshal(spec)
	for _, forbidden := range []string{"enrollment-secret-value", spec.ArtifactCredential, spec.Trust.Agent.BridgeServerPrivateKey, spec.Trust.Astronomer.MCPServerPrivateKey} {
		if strings.Contains(string(safeSpec), forbidden) {
			t.Fatal("agent install input leaked reusable secret through serialization")
		}
	}
	receipt, err := installer.Install(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := kube.CoreV1().Namespaces().Get(context.Background(), DefaultCharlieAgentNamespace, metav1.GetOptions{})
	if err != nil || namespace.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Fatalf("private namespace missing: namespace=%+v err=%v", namespace, err)
	}
	policy, err := kube.NetworkingV1().NetworkPolicies(DefaultCharlieAgentNamespace).Get(context.Background(), receipt.Names.DefaultDeny, metav1.GetOptions{})
	if err != nil || len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 {
		t.Fatalf("default deny missing: policy=%+v err=%v", policy, err)
	}
	for _, target := range []struct{ namespace, name string }{
		{DefaultCharlieAgentNamespace, receipt.Names.Enrollment}, {DefaultCharlieAgentNamespace, receipt.Names.BridgeTLS},
		{DefaultCharlieAgentNamespace, receipt.Names.MCPClientTLS}, {DefaultCharlieAgentNamespace, receipt.Names.ImagePull},
		{"astronomer", receipt.Names.Repository}, {"astronomer", receipt.Names.MCPServerTLS},
	} {
		secret, err := kube.CoreV1().Secrets(target.namespace).Get(context.Background(), target.name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		metadataJSON, _ := json.Marshal(map[string]any{"labels": secret.Labels, "annotations": secret.Annotations})
		if strings.Contains(string(metadataJSON), "enrollment-secret-value") || strings.Contains(string(metadataJSON), spec.ArtifactCredential) || strings.Contains(string(metadataJSON), "PRIVATE KEY") {
			t.Fatalf("secret content leaked into metadata: %s", metadataJSON)
		}
	}
	mcpTrust, err := kube.CoreV1().Secrets("astronomer").Get(context.Background(), receipt.Names.MCPServerTLS, metav1.GetOptions{})
	if err != nil || string(mcpTrust.Data["action-signing-public-key"]) != string(spec.ActionSigningPublicKey) {
		t.Fatalf("MCP action verification trust was not materialized: err=%v", err)
	}
	enrollment, err := kube.CoreV1().Secrets(DefaultCharlieAgentNamespace).Get(context.Background(), receipt.Names.Enrollment, metav1.GetOptions{})
	if err != nil || !bytes.Equal(enrollment.Data["onboarding-package.json"], spec.OnboardingPackage) {
		t.Fatalf("signed onboarding package was not preserved byte-for-byte: err=%v", err)
	}
	service, err := kube.CoreV1().Services("astronomer").Get(context.Background(), charlieMCPServiceName, metav1.GetOptions{})
	if err != nil || len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != 7444 {
		t.Fatalf("private MCP service missing: service=%+v err=%v", service, err)
	}
	accessPolicy, err := kube.NetworkingV1().NetworkPolicies("astronomer").Get(context.Background(), receipt.Names.ProductAccess, metav1.GetOptions{})
	if err != nil || len(accessPolicy.Spec.Ingress) != 1 || len(accessPolicy.Spec.Egress) != 1 {
		t.Fatalf("installer-owned Charlie access policy missing: policy=%+v err=%v", accessPolicy, err)
	}
	peer := accessPolicy.Spec.Ingress[0].From[0]
	if peer.NamespaceSelector == nil || peer.PodSelector == nil ||
		peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != DefaultCharlieAgentNamespace ||
		peer.PodSelector.MatchLabels["app.kubernetes.io/name"] != charlieAgentWorkloadName ||
		peer.PodSelector.MatchLabels["app.kubernetes.io/component"] != "product-agent" {
		t.Fatalf("installer-owned Charlie access policy peer is not exact: %+v", peer)
	}
	application, err := installer.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer").Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(application.Object)
	for _, forbidden := range []string{"enrollment-secret-value", spec.ArtifactCredential, "PRIVATE KEY", "RoleBinding", "ClusterRole", "charlie-central", ":latest"} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("unsafe content %q in Application: %s", forbidden, serialized)
		}
	}
	if !strings.Contains(string(serialized), spec.ImageDigest) || application.GetAnnotations()["astronomer.io/charlie-chart-digest"] != spec.ChartDigest {
		t.Fatal("Application does not preserve immutable artifact pins")
	}
	repoURL, _, _ := unstructured.NestedString(application.Object, "spec", "source", "repoURL")
	revision, _, _ := unstructured.NestedString(application.Object, "spec", "source", "targetRevision")
	path, _, _ := unstructured.NestedString(application.Object, "spec", "source", "path")
	if repoURL != spec.ChartReference || revision != spec.ChartDigest || path != "." {
		t.Fatalf("Application does not pull the Charlie chart by OCI digest: repo=%q revision=%q path=%q", repoURL, revision, path)
	}
	if _, found, _ := unstructured.NestedString(application.Object, "spec", "source", "chart"); found {
		t.Fatal("legacy Helm repository chart selector remained in native OCI source")
	}
	repository, err := kube.CoreV1().Secrets("astronomer").Get(context.Background(), receipt.Names.Repository, metav1.GetOptions{})
	if err != nil || string(repository.Data["type"]) != "oci" || string(repository.Data["url"]) != spec.ChartReference || len(repository.Data["enableOCI"]) != 0 {
		t.Fatalf("Argo OCI repository credential is not exact: type=%q url=%q err=%v", repository.Data["type"], repository.Data["url"], err)
	}
	valuesText, found, err := unstructured.NestedString(application.Object, "spec", "source", "helm", "values")
	if err != nil || !found {
		t.Fatalf("agent Application lacks reviewed values: found=%t err=%v", found, err)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(valuesText), &values); err != nil {
		t.Fatal(err)
	}
	bridge := values["bridge"].(map[string]any)
	bridgeNamespace := bridge["productNamespaceSelector"].(map[string]any)
	bridgePods := bridge["productPodSelector"].(map[string]any)
	network := values["networkPolicy"].(map[string]any)
	mcp := network["mcp"].(map[string]any)
	central := network["central"].(map[string]any)
	if bridgeNamespace["kubernetes.io/metadata.name"] != "astronomer" ||
		bridgePods["app.kubernetes.io/component"] != "server" ||
		mcp["namespaceSelector"].(map[string]any)["kubernetes.io/metadata.name"] != "astronomer" ||
		mcp["podSelector"].(map[string]any)["app.kubernetes.io/component"] != "server" {
		t.Fatalf("agent private bridge/MCP peers are not exact: bridge=%v mcp=%v", bridge, mcp)
	}
	centralCIDRs := central["cidrs"].([]any)
	if len(centralCIDRs) != 1 || centralCIDRs[0] != "203.0.113.10/32" {
		t.Fatalf("agent central egress is not exact: %v", centralCIDRs)
	}
	selfHeal, _, _ := unstructured.NestedBool(application.Object, "spec", "syncPolicy", "automated", "selfHeal")
	if !selfHeal {
		t.Fatal("Argo drift self-healing is not enabled")
	}
}

func TestAgentInstallerRejectsCentralChartMutableOrBroadArtifacts(t *testing.T) {
	installer, _, _, _ := testAgentInstaller(t)
	base := testAgentInstallSpec(t)
	tests := []AgentInstallSpec{base, base, base, base, base}
	tests[0].ChartReference = "oci://charlie.example.test/charlie/charlie-central"
	tests[1].ChartVersion = "latest"
	tests[2].CentralCIDRs = []string{"0.0.0.0/0"}
	tests[3].ChartReference = "oci://charlie.example.test/charlie/charlie-agent"
	tests[4].ImageReference = "external.example.test/charlie/agent@" + base.ImageDigest
	for index, spec := range tests {
		if _, err := installer.Install(context.Background(), spec); err == nil {
			t.Fatalf("unsafe install variant %d accepted", index)
		}
	}
}

func TestAgentInstallerRejectsOnboardingReplicaMismatchOrDuplicateSlots(t *testing.T) {
	installer, _, _, _ := testAgentInstaller(t)

	mismatched := testAgentInstallSpec(t)
	mismatched.ReplicaCount = 3
	if _, err := installer.Install(context.Background(), mismatched); err == nil {
		t.Fatal("onboarding package replica count mismatch accepted")
	}

	duplicate := testAgentInstallSpec(t)
	fixture := newOnboardingFixture(t)
	fixture.object["credentials"].([]any)[1].(map[string]any)["replica_ordinal"] = 0
	duplicate.OnboardingPackage = fixture.signed(t)
	if _, err := installer.Install(context.Background(), duplicate); err == nil {
		t.Fatal("onboarding package with duplicate replica slots accepted")
	}
}

func TestAgentInstallerPartialFailureRollsBackAndRetrySucceeds(t *testing.T) {
	installer, kube, _, _ := testAgentInstaller(t)
	spec := testAgentInstallSpec(t)
	failed := false
	kube.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create := action.(k8stesting.CreateAction)
		secret := create.GetObject().(*corev1.Secret)
		if !failed && strings.HasSuffix(secret.Name, "-mcp-tls") {
			failed = true
			return true, nil, errors.New("injected Secret failure")
		}
		return false, nil, nil
	})
	if _, err := installer.Install(context.Background(), spec); err == nil {
		t.Fatal("partial failure accepted")
	}
	secrets, _ := kube.CoreV1().Secrets(DefaultCharlieAgentNamespace).List(context.Background(), metav1.ListOptions{})
	if len(secrets.Items) != 0 {
		t.Fatalf("partial install left Secrets: %v", secrets.Items)
	}
	if _, err := installer.Install(context.Background(), spec); err != nil {
		t.Fatalf("safe retry failed: %v", err)
	}
}

func TestAgentInstallerUpgradeRollbackRotationAndDrift(t *testing.T) {
	installer, kube, bridge, _ := testAgentInstaller(t)
	current := testAgentInstallSpec(t)
	receipt, err := installer.Install(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	appResource := installer.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer")
	drifted, _ := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	_ = unstructured.SetNestedField(drifted.Object, "latest", "spec", "source", "targetRevision")
	if _, err := appResource.Update(context.Background(), drifted, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	unrelated := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "operator-owned", Namespace: DefaultCharlieAgentNamespace}, Data: map[string]string{"keep": "me"}}
	if _, err := kube.CoreV1().ConfigMaps(DefaultCharlieAgentNamespace).Create(context.Background(), unrelated, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Install(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	corrected, _ := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	revision, _, _ := unstructured.NestedString(corrected.Object, "spec", "source", "targetRevision")
	if revision != current.ChartDigest {
		t.Fatalf("drift not corrected: %q", revision)
	}
	if kept, _ := kube.CoreV1().ConfigMaps(DefaultCharlieAgentNamespace).Get(context.Background(), "operator-owned", metav1.GetOptions{}); kept.Data["keep"] != "me" {
		t.Fatal("unrelated operator resource was overwritten")
	}

	next := current
	next.ChartVersion = "1.1.0"
	next.ChartDigest = "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	next.ImageDigest = "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	next.ImageReference = "charlie.example.test/charlie/agent@" + next.ImageDigest
	if _, err := installer.Upgrade(context.Background(), current, next); err != nil {
		t.Fatal(err)
	}
	upgraded, _ := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	if upgraded.GetAnnotations()["astronomer.io/charlie-image-digest"] != next.ImageDigest {
		t.Fatal("upgrade did not apply reviewed digest")
	}
	if _, err := installer.Rollback(context.Background(), next, current); err != nil {
		t.Fatal(err)
	}
	rolledBack, _ := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	if rolledBack.GetAnnotations()["astronomer.io/charlie-image-digest"] != current.ImageDigest {
		t.Fatal("rollback did not restore retained digest")
	}
	previousValues, _, _ := unstructured.NestedString(rolledBack.Object, "spec", "source", "helm", "values")
	rotated := current
	rotatedFixture := newOnboardingFixture(t)
	rotatedFixture.object["credentials"].([]any)[0].(map[string]any)["credential"] = "rotated-enrollment-secret-value-00001"
	rotatedFixture.object["credentials"].([]any)[1].(map[string]any)["credential"] = "rotated-enrollment-secret-value-00002"
	rotatedFixture.object["credentials"].([]any)[2].(map[string]any)["credential"] = "rotated-artifact-secret-value-00001"
	rotated.OnboardingPackage = rotatedFixture.signed(t)
	rotated.ArtifactCredential = "rotated-artifact-secret-value-00001"
	rotated.SecretIntegrityHMAC = strings.Repeat("c", 64)
	if _, err := installer.RotateCredentials(context.Background(), current, rotated, map[string]string{"agent_enrollment": "old-enrollment", "artifact_pull": "old-artifact"}); err != nil {
		t.Fatal(err)
	}
	if bridge.calls[len(bridge.calls)-1] != "credential-revoked" {
		t.Fatal("rotation did not verify old credential revocation")
	}
	rotatedApplication, _ := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	rotatedValues, _, _ := unstructured.NestedString(rotatedApplication.Object, "spec", "source", "helm", "values")
	if rotatedValues == previousValues {
		t.Fatal("credential rotation did not change keyed rollout checksums")
	}
	for _, rawDigest := range []string{digestBytes([]byte("rotated-enrollment-secret-value-00001")), digestBytes([]byte(rotated.ArtifactCredential))} {
		if strings.Contains(rotatedValues, rawDigest) {
			t.Fatal("rollout values exposed an offline-verifiable raw credential digest")
		}
	}
}

func TestAgentInstallerReadinessUninstallDisconnectAndReconnect(t *testing.T) {
	installer, kube, bridge, metadata := testAgentInstaller(t)
	spec := testAgentInstallSpec(t)
	receipt, err := installer.Install(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kube.CoreV1().Secrets(DefaultCharlieAgentNamespace).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: receipt.Names.Bootstrap, Namespace: DefaultCharlieAgentNamespace, Labels: managedLabels(spec.InstallationID)},
		Data:       map[string][]byte{"onboarding-package.json": spec.OnboardingPackage},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	appResource := installer.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer")
	application, _ := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{})
	application.Object["status"] = map[string]any{"sync": map[string]any{"status": "Synced"}, "health": map[string]any{"status": "Healthy"}}
	if _, err := appResource.Update(context.Background(), application, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	replicas := int32(2)
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: charlieAgentWorkloadName, Namespace: DefaultCharlieAgentNamespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas}, Status: appsv1.StatefulSetStatus{Replicas: 2, ReadyReplicas: 2},
	}
	if _, err := kube.AppsV1().StatefulSets(DefaultCharlieAgentNamespace).Create(context.Background(), statefulSet, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := installer.WaitReady(ctx, spec)
	if err != nil || !status.Ready() {
		t.Fatalf("readiness diagnostics failed: status=%+v err=%v", status, err)
	}
	if err := installer.Uninstall(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(bridge.calls[len(bridge.calls)-3:], ","); got != "disable,stop-triggers,settle-streams" {
		t.Fatalf("unsafe uninstall order: %s", got)
	}
	if len(metadata.events) != 1 || metadata.events[0] != "uninstalled" {
		t.Fatalf("temporary uninstall metadata=%v", metadata.events)
	}
	if _, err := kube.CoreV1().Secrets(DefaultCharlieAgentNamespace).Get(context.Background(), receipt.Names.Bootstrap, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("bootstrap Secret survived uninstall: %v", err)
	}
	if _, err := installer.Reconnect(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if metadata.events[len(metadata.events)-1] != "reconnected" {
		t.Fatalf("reconnect not distinct: %v", metadata.events)
	}
	if err := installer.Disconnect(context.Background(), spec, "wrong"); err == nil {
		t.Fatal("disconnect accepted without destructive confirmation")
	}
	if err := installer.Disconnect(context.Background(), spec, "disconnect:"+spec.InstallationID.String()); err != nil {
		t.Fatal(err)
	}
	if metadata.events[len(metadata.events)-1] != "disconnected" {
		t.Fatalf("disconnect not recorded distinctly: %v", metadata.events)
	}
}

func TestAgentInstallerSuspendAndResumeRemoveOnlyRuntimeSurface(t *testing.T) {
	installer, kube, _, metadata := testAgentInstaller(t)
	spec := testAgentInstallSpec(t)
	receipt, err := installer.Install(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	replicas := int32(2)
	_, err = kube.AppsV1().StatefulSets(DefaultCharlieAgentNamespace).Create(context.Background(), &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: charlieAgentWorkloadName, Namespace: DefaultCharlieAgentNamespace, Labels: map[string]string{"app.kubernetes.io/instance": receipt.Names.Application}},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.Suspend(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	appResource := installer.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace("astronomer")
	if _, err := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Argo Application survived suspend: %v", err)
	}
	if _, err := kube.CoreV1().Services("astronomer").Get(context.Background(), charlieMCPServiceName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("MCP Service survived suspend: %v", err)
	}
	if _, err := kube.NetworkingV1().NetworkPolicies("astronomer").Get(context.Background(), receipt.Names.ProductAccess, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("product access policy survived suspend: %v", err)
	}
	if _, err := kube.AppsV1().StatefulSets(DefaultCharlieAgentNamespace).Get(context.Background(), charlieAgentWorkloadName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("agent workload survived suspend: %v", err)
	}
	if _, err := kube.CoreV1().Secrets(DefaultCharlieAgentNamespace).Get(context.Background(), receipt.Names.Enrollment, metav1.GetOptions{}); err != nil {
		t.Fatalf("durable installation secret was removed: %v", err)
	}
	if _, err := kube.CoreV1().ConfigMaps("astronomer").Get(context.Background(), receipt.Names.ResumeState, metav1.GetOptions{}); err != nil {
		t.Fatalf("owner-bound resume state missing: %v", err)
	}
	if _, err := kube.CoreV1().Namespaces().Get(context.Background(), DefaultCharlieAgentNamespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("agent namespace was destructively removed: %v", err)
	}
	if err := installer.Resume(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := appResource.Get(context.Background(), receipt.Names.Application, metav1.GetOptions{}); err != nil {
		t.Fatalf("Argo Application was not restored: %v", err)
	}
	if _, err := kube.CoreV1().Services("astronomer").Get(context.Background(), charlieMCPServiceName, metav1.GetOptions{}); err != nil {
		t.Fatalf("MCP Service was not restored: %v", err)
	}
	if _, err := kube.NetworkingV1().NetworkPolicies("astronomer").Get(context.Background(), receipt.Names.ProductAccess, metav1.GetOptions{}); err != nil {
		t.Fatalf("product access policy was not restored: %v", err)
	}
	if got := strings.Join(metadata.events, ","); got != "uninstalled,reconnected" {
		t.Fatalf("lifecycle metadata=%s", got)
	}
}
