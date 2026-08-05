package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	DefaultCharlieAgentNamespace = "astronomer-charlie"
	defaultCharlieArgoNamespace  = "astronomer"
	charlieAgentWorkloadName     = "charlie-agent"
	charlieMCPServiceName        = "astronomer-charlie-mcp"
	charlieMCPPort               = int32(7444)
	installationOwnerLabel       = "astronomer.io/charlie-installation"
)

type AgentResourceNames struct {
	Application, Repository, ImagePull, Enrollment, BridgeTLS           string
	MCPClientTLS, CentralCA, MCPServerTLS, BridgeClientTLS, DefaultDeny string
	Bootstrap, ProductAccess, ResumeState                               string
	AgentNamespace                                                      string
}

type AgentInstallSpec struct {
	InstallationID, ConnectionID              uuid.UUID
	LogicalAgentID, DeploymentID              string
	EnvironmentID, TenantID                   string
	OnboardingPackageID                       string
	CentralURL                                string
	CentralCAPEM                              string `json:"-"`
	ChartReference, ChartVersion, ChartDigest string
	ImageReference, ImageDigest               string
	OnboardingPackage                         []byte `json:"-"`
	ReplicaCount                              int
	ArtifactCredential                        string `json:"-"`
	SecretPrefix, DisclosureDigest            string
	SecretIntegrityHMAC                       string
	ActionSigningPublicKey                    ed25519.PublicKey `json:"-"`
	ActionSigningKeyFingerprint               string
	Trust                                     GeneratedLocalTrust `json:"-"`
	CentralCIDRs                              []string
	Proxy                                     AgentProxyConfig
}

type AgentProxyConfig struct {
	ExistingSecret string
	CIDRs          []string
}

type AgentInstallReceipt struct {
	Names    AgentResourceNames
	Rollback func(context.Context) error
}

type AgentBridgeStatus struct {
	BridgeReady, CentralEnrolled, LeaderElected bool
	StandbyVisible, ProtocolCompatible          bool
	AgentProtocolVersion, BridgeProtocolVersion string
}

// CredentialRevocationTarget is the product-owned binding for Charlie's
// two-phase disconnect protocol. RequestID is deterministic so retrying an
// ambiguous POST/GET cannot create a second revocation operation.
type CredentialRevocationTarget struct {
	RequestID, DeploymentID, PackageID, IntegrationID string
}

type AgentBridgeLifecycle interface {
	Status(context.Context) (AgentBridgeStatus, error)
	CentralHealth(context.Context) error
	VerifyArtifactDigests(context.Context, string, string, string) error
	Disable(context.Context) error
	StopTriggerDispatch(context.Context) error
	SettleStreams(context.Context) error
	RevokeCredentialPackage(context.Context, CredentialRevocationTarget) error
	VerifyCredentialPackageRevoked(context.Context, CredentialRevocationTarget) error
}

type AgentMetadataLifecycle interface {
	MarkTemporarilyUninstalled(context.Context, uuid.UUID) error
	MarkReconnected(context.Context, uuid.UUID) error
	MarkDisconnected(context.Context, uuid.UUID) error
}

type PGAgentMetadataLifecycle struct{ Pool *pgxpool.Pool }

func (p PGAgentMetadataLifecycle) MarkTemporarilyUninstalled(ctx context.Context, connectionID uuid.UUID) error {
	if p.Pool == nil {
		return fmt.Errorf("Charlie metadata store is unavailable")
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET active=false, requested_mode='disabled', verified_mode='disabled', health_state='inactive', leader_instance_id='', updated_at=now() WHERE id=$1`, connectionID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("Charlie connection was not found")
	}
	return nil
}

func (p PGAgentMetadataLifecycle) MarkReconnected(ctx context.Context, connectionID uuid.UUID) error {
	if p.Pool == nil {
		return fmt.Errorf("Charlie metadata store is unavailable")
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET health_state='installing', last_error_code='', reconciliation_due_at=now(), updated_at=now() WHERE id=$1 AND health_state='inactive'`, connectionID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("Charlie connection is not reconnectable")
	}
	return nil
}

func (p PGAgentMetadataLifecycle) MarkDisconnected(ctx context.Context, connectionID uuid.UUID) error {
	if p.Pool == nil {
		return fmt.Errorf("Charlie metadata store is unavailable")
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET active=false, requested_mode='disabled', verified_mode='disabled', health_state='disconnected', leader_instance_id='', local_trust_material_encrypted='', agent_secret_name='', agent_secret_hmac='', updated_at=now() WHERE id=$1`, connectionID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("Charlie connection was not found")
	}
	return nil
}

type HostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type AgentInstallerConfig struct {
	AgentNamespace, ArgoNamespace, ProductNamespace string
	ProductPodLabels                                map[string]string
	Resolver                                        HostResolver
	Bridge                                          AgentBridgeLifecycle
	Metadata                                        AgentMetadataLifecycle
	PollInterval                                    time.Duration
}

type AgentInstaller struct {
	kube                                            kubernetes.Interface
	dynamic                                         dynamic.Interface
	agentNamespace, argoNamespace, productNamespace string
	productPodLabels                                map[string]string
	resolver                                        HostResolver
	bridge                                          AgentBridgeLifecycle
	metadata                                        AgentMetadataLifecycle
	pollInterval                                    time.Duration
}

func NewAgentInstaller(kube kubernetes.Interface, dyn dynamic.Interface, cfg AgentInstallerConfig) (*AgentInstaller, error) {
	if kube == nil || dyn == nil {
		return nil, fmt.Errorf("Charlie agent installer requires Kubernetes and Argo clients")
	}
	if cfg.AgentNamespace == "" {
		cfg.AgentNamespace = DefaultCharlieAgentNamespace
	}
	if cfg.ArgoNamespace == "" {
		cfg.ArgoNamespace = defaultCharlieArgoNamespace
	}
	if cfg.ProductNamespace == "" {
		cfg.ProductNamespace = "astronomer"
	}
	for _, namespace := range []string{cfg.AgentNamespace, cfg.ArgoNamespace, cfg.ProductNamespace} {
		if !validServiceDNS("service." + namespace + ".svc") {
			return nil, fmt.Errorf("Charlie installer namespace is invalid")
		}
	}
	if len(cfg.ProductPodLabels) == 0 {
		cfg.ProductPodLabels = map[string]string{"app.kubernetes.io/name": "astronomer", "app.kubernetes.io/component": "server"}
	}
	if cfg.Resolver == nil {
		cfg.Resolver = net.DefaultResolver
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	return &AgentInstaller{
		kube: kube, dynamic: dyn, agentNamespace: cfg.AgentNamespace, argoNamespace: cfg.ArgoNamespace,
		productNamespace: cfg.ProductNamespace, productPodLabels: copyStringMap(cfg.ProductPodLabels),
		resolver: cfg.Resolver, bridge: cfg.Bridge, metadata: cfg.Metadata, pollInterval: cfg.PollInterval,
	}, nil
}

func (i *AgentInstaller) Install(ctx context.Context, spec AgentInstallSpec) (AgentInstallReceipt, error) {
	if err := validateAgentInstallSpec(spec); err != nil {
		return AgentInstallReceipt{}, err
	}
	pkg, err := contract.ParseOnboardingPackage(spec.OnboardingPackage)
	if err != nil {
		return AgentInstallReceipt{}, fmt.Errorf("Charlie signed onboarding package is invalid")
	}
	expectedMCPURL := fmt.Sprintf("https://%s.%s.svc:%d/mcp", charlieMCPServiceName, i.productNamespace, charlieMCPPort)
	if pkg.Integration.McpUrl != expectedMCPURL || strings.TrimSpace(string(pkg.Integration.IntegrationId)) == "" {
		return AgentInstallReceipt{}, fmt.Errorf("Charlie signed integration does not match the private product MCP boundary")
	}
	if len(spec.CentralCIDRs) == 0 {
		cidrs, err := i.resolveCentralCIDRs(ctx, spec.CentralURL)
		if err != nil {
			return AgentInstallReceipt{}, err
		}
		spec.CentralCIDRs = cidrs
	}
	names := agentResourceNames(spec, i.agentNamespace)
	var rollbacks []func(context.Context) error
	rollbackAll := func(rollbackCtx context.Context) error {
		var first error
		for index := len(rollbacks) - 1; index >= 0; index-- {
			if err := rollbacks[index](rollbackCtx); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	fail := func(err error) (AgentInstallReceipt, error) {
		if rollbackErr := rollbackAll(ctx); rollbackErr != nil {
			return AgentInstallReceipt{}, fmt.Errorf("%w; rollback failed", err)
		}
		return AgentInstallReceipt{}, err
	}
	appendStep := func(rollback func(context.Context) error, err error) error {
		if err == nil {
			rollbacks = append(rollbacks, rollback)
		}
		return err
	}

	if err := appendStep(i.reconcileNamespace(ctx, spec.InstallationID)); err != nil {
		return fail(err)
	}
	if err := appendStep(i.reconcileNetworkPolicy(ctx, defaultDenyPolicy(names, spec.InstallationID))); err != nil {
		return fail(err)
	}
	if err := appendStep(i.reconcileNetworkPolicy(ctx, productAccessPolicy(i, names, spec.InstallationID))); err != nil {
		return fail(err)
	}
	for _, secret := range installationSecrets(i, names, spec) {
		if err := appendStep(i.reconcileSecret(ctx, secret)); err != nil {
			return fail(err)
		}
	}
	if err := appendStep(i.reconcileService(ctx, mcpService(i, spec.InstallationID))); err != nil {
		return fail(err)
	}
	application, err := i.application(spec, names)
	if err != nil {
		return fail(err)
	}
	if err := appendStep(i.reconcileApplication(ctx, application)); err != nil {
		return fail(err)
	}
	return AgentInstallReceipt{Names: names, Rollback: rollbackAll}, nil
}

// PrepareNamespace establishes the fixed, restricted product-agent boundary
// before bootstrap material is written. It is deliberately separate from
// Install because the bootstrap Secret HMAC is an input to the immutable Argo
// desired state. Install reconciles the namespace again as an ownership check.
func (i *AgentInstaller) PrepareNamespace(ctx context.Context, installationID uuid.UUID) (func(context.Context) error, error) {
	if i == nil || installationID == uuid.Nil {
		return nil, fmt.Errorf("Charlie agent namespace preparation is unavailable")
	}
	return i.reconcileNamespace(ctx, installationID)
}

func (i *AgentInstaller) Upgrade(ctx context.Context, current, next AgentInstallSpec) (AgentInstallReceipt, error) {
	if !sameStableAgent(current, next) {
		return AgentInstallReceipt{}, fmt.Errorf("Charlie upgrade cannot change installation, logical agent, or local trust identities")
	}
	if current.ChartDigest == next.ChartDigest && current.ImageDigest == next.ImageDigest {
		return AgentInstallReceipt{}, fmt.Errorf("Charlie upgrade requires a new reviewed immutable artifact")
	}
	return i.Install(ctx, next)
}

func (i *AgentInstaller) Rollback(ctx context.Context, current, previous AgentInstallSpec) (AgentInstallReceipt, error) {
	if !sameStableAgent(current, previous) {
		return AgentInstallReceipt{}, fmt.Errorf("Charlie rollback cannot change installation, logical agent, or local trust identities")
	}
	return i.Install(ctx, previous)
}

type AgentInstallationStatus struct {
	ApplicationSynced, ApplicationHealthy            bool
	ReadyReplicas, DesiredReplicas, ExpectedReplicas int32
	BridgeReady, CentralEnrolled, LeaderElected      bool
	StandbyVisible, ProtocolCompatible               bool
	CentralHealthy, ArtifactsVerified                bool
}

func (s AgentInstallationStatus) Ready() bool {
	return s.ApplicationSynced && s.ApplicationHealthy && s.ExpectedReplicas >= 2 && s.DesiredReplicas == s.ExpectedReplicas && s.ReadyReplicas == s.DesiredReplicas &&
		s.BridgeReady && s.CentralEnrolled && s.LeaderElected && s.StandbyVisible && s.ProtocolCompatible &&
		s.CentralHealthy && s.ArtifactsVerified
}

func (i *AgentInstaller) Status(ctx context.Context, spec AgentInstallSpec) (AgentInstallationStatus, error) {
	names := agentResourceNames(spec, i.agentNamespace)
	application, err := i.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(i.argoNamespace).Get(ctx, names.Application, metav1.GetOptions{})
	if err != nil {
		return AgentInstallationStatus{}, err
	}
	statefulSet, err := i.kube.AppsV1().StatefulSets(i.agentNamespace).Get(ctx, charlieAgentWorkloadName, metav1.GetOptions{})
	if err != nil {
		return AgentInstallationStatus{}, err
	}
	syncStatus, _, _ := unstructured.NestedString(application.Object, "status", "sync", "status")
	healthStatus, _, _ := unstructured.NestedString(application.Object, "status", "health", "status")
	status := AgentInstallationStatus{
		ApplicationSynced: syncStatus == "Synced", ApplicationHealthy: healthStatus == "Healthy",
		ReadyReplicas: statefulSet.Status.ReadyReplicas, ExpectedReplicas: int32(spec.ReplicaCount),
	}
	if statefulSet.Spec.Replicas != nil {
		status.DesiredReplicas = *statefulSet.Spec.Replicas
	}
	if i.bridge == nil {
		return status, fmt.Errorf("Charlie bridge diagnostics are unavailable")
	}
	bridgeStatus, err := i.bridge.Status(ctx)
	if err != nil {
		return status, err
	}
	status.BridgeReady, status.CentralEnrolled = bridgeStatus.BridgeReady, bridgeStatus.CentralEnrolled
	status.LeaderElected, status.StandbyVisible = bridgeStatus.LeaderElected, bridgeStatus.StandbyVisible
	status.ProtocolCompatible = bridgeStatus.ProtocolCompatible && bridgeStatus.AgentProtocolVersion == contract.AgentProtocolVersion && bridgeStatus.BridgeProtocolVersion == contract.BridgeProtocolVersion
	status.CentralHealthy = i.bridge.CentralHealth(ctx) == nil
	annotations := application.GetAnnotations()
	targetRevision, _, _ := unstructured.NestedString(application.Object, "spec", "source", "targetRevision")
	runningImage := ""
	if len(statefulSet.Spec.Template.Spec.Containers) == 1 {
		runningImage = statefulSet.Spec.Template.Spec.Containers[0].Image
	}
	localArtifactsVerified := annotations["astronomer.io/charlie-chart-digest"] == spec.ChartDigest &&
		annotations["astronomer.io/charlie-image-digest"] == spec.ImageDigest &&
		targetRevision == spec.ChartDigest &&
		runningImage == strings.TrimSuffix(spec.ImageReference, "@"+spec.ImageDigest)+"@"+spec.ImageDigest
	status.ArtifactsVerified = localArtifactsVerified && i.bridge.VerifyArtifactDigests(ctx, spec.ChartVersion, spec.ChartDigest, spec.ImageDigest) == nil
	return status, nil
}

func (i *AgentInstaller) WaitReady(ctx context.Context, spec AgentInstallSpec) (AgentInstallationStatus, error) {
	ticker := time.NewTicker(i.pollInterval)
	defer ticker.Stop()
	for {
		if status, err := i.Status(ctx, spec); err == nil && status.Ready() {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return AgentInstallationStatus{}, fmt.Errorf("wait for Charlie agent readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (i *AgentInstaller) Uninstall(ctx context.Context, spec AgentInstallSpec) error {
	if err := i.uninstallResources(ctx, spec); err != nil {
		return err
	}
	return i.metadata.MarkTemporarilyUninstalled(ctx, spec.ConnectionID)
}

// PruneSupersededRepositories removes only owner-bound package repository
// credentials other than the currently reviewed package. It runs before Argo
// readiness evaluation because a revoked credential for the same OCI origin
// can otherwise make repository selection nondeterministic.
func (i *AgentInstaller) PruneSupersededRepositories(ctx context.Context, spec AgentInstallSpec) error {
	if i == nil || i.kube == nil || spec.InstallationID == uuid.Nil {
		return fmt.Errorf("Charlie repository cleanup dependencies are unavailable")
	}
	names := agentResourceNames(spec, i.agentNamespace)
	return i.prunePackageSecrets(ctx, i.argoNamespace, spec.InstallationID, map[string]bool{names.Repository: true})
}

// PruneSupersededSecrets removes the remaining owner-bound package material
// after the replacement workload is ready. Fixed product TLS names and the
// current package closure are always preserved.
func (i *AgentInstaller) PruneSupersededSecrets(ctx context.Context, spec AgentInstallSpec) error {
	if i == nil || i.kube == nil || spec.InstallationID == uuid.Nil {
		return fmt.Errorf("Charlie secret cleanup dependencies are unavailable")
	}
	names := agentResourceNames(spec, i.agentNamespace)
	preserve := map[string]bool{
		names.Bootstrap: true, names.Enrollment: true, names.BridgeTLS: true,
		names.MCPClientTLS: true, names.CentralCA: true, names.ImagePull: true,
	}
	if err := i.prunePackageSecrets(ctx, i.agentNamespace, spec.InstallationID, preserve); err != nil {
		return err
	}
	return i.prunePackageSecrets(ctx, i.argoNamespace, spec.InstallationID, map[string]bool{names.Repository: true})
}

func (i *AgentInstaller) prunePackageSecrets(ctx context.Context, namespace string, installationID uuid.UUID, preserve map[string]bool) error {
	selector := installationOwnerLabel + "=" + installationID.String()
	secrets, err := i.kube.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	for index := range secrets.Items {
		name := secrets.Items[index].Name
		if preserve[name] || !strings.HasPrefix(name, "charlie-agent-bootstrap-") {
			continue
		}
		if err := i.kube.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// Suspend removes Charlie's running workload and product network surface while
// retaining installation secrets and an owner-bound copy of the reviewed Argo
// desired state. It is the reversible feature-disable operation; it never
// deletes the agent namespace or durable product audit/history.
func (i *AgentInstaller) Suspend(ctx context.Context, spec AgentInstallSpec) error {
	if i == nil || i.bridge == nil || i.metadata == nil || spec.InstallationID == uuid.Nil || spec.ConnectionID == uuid.Nil {
		return fmt.Errorf("Charlie suspend lifecycle dependencies are unavailable")
	}
	// A central outage must not keep a local workload alive. Remote disable is
	// best effort after the product-local emergency latch; local dispatch and
	// streams, however, must settle before runtime resources are removed.
	_ = i.bridge.Disable(ctx)
	for _, step := range []func(context.Context) error{i.bridge.StopTriggerDispatch, i.bridge.SettleStreams} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	names := agentResourceNames(spec, i.agentNamespace)
	if err := i.snapshotApplication(ctx, names, spec.InstallationID); err != nil {
		return err
	}
	if err := i.deleteApplication(ctx, names.Application, spec.InstallationID); err != nil {
		return err
	}
	if err := i.deleteRuntimeResources(ctx, names, spec.InstallationID); err != nil {
		return err
	}
	return i.metadata.MarkTemporarilyUninstalled(ctx, spec.ConnectionID)
}

// Resume restores only the owner-bound runtime/network objects captured by
// Suspend. Secret material remains in Kubernetes and is never copied into the
// database or logs. The connection returns in disabled/installing state; an
// operator must explicitly clear emergency-disable and choose a mode.
func (i *AgentInstaller) Resume(ctx context.Context, spec AgentInstallSpec) error {
	if i == nil || i.metadata == nil || spec.InstallationID == uuid.Nil || spec.ConnectionID == uuid.Nil {
		return fmt.Errorf("Charlie resume lifecycle dependencies are unavailable")
	}
	names := agentResourceNames(spec, i.agentNamespace)
	application, err := i.loadApplicationSnapshot(ctx, names, spec.InstallationID)
	if err != nil {
		return err
	}
	var rollbacks []func(context.Context) error
	appendStep := func(rollback func(context.Context) error, err error) error {
		if err == nil {
			rollbacks = append(rollbacks, rollback)
		}
		return err
	}
	rollbackAll := func() {
		for index := len(rollbacks) - 1; index >= 0; index-- {
			_ = rollbacks[index](ctx)
		}
	}
	if err := appendStep(i.reconcileNetworkPolicy(ctx, productAccessPolicy(i, names, spec.InstallationID))); err != nil {
		rollbackAll()
		return err
	}
	if err := appendStep(i.reconcileService(ctx, mcpService(i, spec.InstallationID))); err != nil {
		rollbackAll()
		return err
	}
	if err := appendStep(i.reconcileApplication(ctx, application)); err != nil {
		rollbackAll()
		return err
	}
	if err := i.metadata.MarkReconnected(ctx, spec.ConnectionID); err != nil {
		rollbackAll()
		return err
	}
	return nil
}

func (i *AgentInstaller) uninstallResources(ctx context.Context, spec AgentInstallSpec) error {
	if i.bridge == nil || i.metadata == nil {
		return fmt.Errorf("Charlie uninstall lifecycle dependencies are unavailable")
	}
	_ = i.bridge.Disable(ctx)
	for _, step := range []func(context.Context) error{i.bridge.StopTriggerDispatch, i.bridge.SettleStreams} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	names := agentResourceNames(spec, i.agentNamespace)
	if err := i.deleteApplication(ctx, names.Application, spec.InstallationID); err != nil {
		return err
	}
	if err := i.deleteOwnedResources(ctx, names, spec.InstallationID); err != nil {
		return err
	}
	return nil
}

func (i *AgentInstaller) Disconnect(ctx context.Context, spec AgentInstallSpec, confirmation string) error {
	if confirmation != "disconnect:"+spec.InstallationID.String() || i.metadata == nil || i.bridge == nil || strings.TrimSpace(spec.OnboardingPackageID) == "" {
		return fmt.Errorf("Charlie disconnect requires exact destructive confirmation")
	}
	// Unlike reversible uninstall, disconnect must revoke the durable central
	// credential tree and confirm that state by an independent readback before
	// deleting any local workload, trust material, or connection metadata. An
	// ambiguous response therefore leaves local resources intact for safe retry.
	if err := i.bridge.Disable(ctx); err != nil {
		return fmt.Errorf("disable Charlie before disconnect: %w", err)
	}
	for _, step := range []func(context.Context) error{i.bridge.StopTriggerDispatch, i.bridge.SettleStreams} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	target, err := i.credentialRevocationTarget(ctx, spec)
	if err != nil {
		return err
	}
	if revokeErr := i.bridge.RevokeCredentialPackage(ctx, target); revokeErr != nil {
		// A prior attempt may already have completed central revocation and then
		// failed during local cleanup. Its caller credential can replay only the
		// exact final GET, so a POST authentication failure must fall through to
		// signed readback before deciding whether cleanup is safe to resume.
		if verifyErr := i.bridge.VerifyCredentialPackageRevoked(ctx, target); verifyErr != nil {
			return fmt.Errorf("revoke Charlie credential package: %v; verify final revocation: %w", revokeErr, verifyErr)
		}
	} else if err := i.bridge.VerifyCredentialPackageRevoked(ctx, target); err != nil {
		return fmt.Errorf("verify Charlie credential package revocation: %w", err)
	}
	names := agentResourceNames(spec, i.agentNamespace)
	if err := i.deleteApplication(ctx, names.Application, spec.InstallationID); err != nil {
		return err
	}
	if err := i.deleteOwnedResources(ctx, names, spec.InstallationID); err != nil {
		return err
	}
	return i.metadata.MarkDisconnected(ctx, spec.ConnectionID)
}

func (i *AgentInstaller) credentialRevocationTarget(ctx context.Context, spec AgentInstallSpec) (CredentialRevocationTarget, error) {
	names := agentResourceNames(spec, i.agentNamespace)
	secret, err := i.kube.CoreV1().Secrets(i.agentNamespace).Get(ctx, names.Enrollment, metav1.GetOptions{})
	if err != nil || secret.Labels[installationOwnerLabel] != spec.InstallationID.String() {
		return CredentialRevocationTarget{}, fmt.Errorf("Charlie signed onboarding identity is unavailable")
	}
	pkg, err := contract.ParseOnboardingPackage(secret.Data["onboarding-package.json"])
	if err != nil || string(pkg.PackageId) != spec.OnboardingPackageID ||
		(strings.TrimSpace(spec.DeploymentID) != "" && string(pkg.DeploymentId) != spec.DeploymentID) ||
		strings.TrimSpace(string(pkg.Integration.IntegrationId)) == "" {
		return CredentialRevocationTarget{}, fmt.Errorf("Charlie credential revocation identity does not match signed onboarding")
	}
	requestID := uuid.NewSHA1(spec.InstallationID, []byte("charlie-disconnect:"+spec.OnboardingPackageID)).String()
	return CredentialRevocationTarget{
		RequestID: requestID, DeploymentID: string(pkg.DeploymentId), PackageID: string(pkg.PackageId),
		IntegrationID: string(pkg.Integration.IntegrationId),
	}, nil
}

func (i *AgentInstaller) Reconnect(ctx context.Context, spec AgentInstallSpec) (AgentInstallReceipt, error) {
	if i.metadata == nil {
		return AgentInstallReceipt{}, fmt.Errorf("Charlie reconnect metadata dependency is unavailable")
	}
	receipt, err := i.Install(ctx, spec)
	if err != nil {
		return AgentInstallReceipt{}, err
	}
	if err := i.metadata.MarkReconnected(ctx, spec.ConnectionID); err != nil {
		_ = receipt.Rollback(ctx)
		return AgentInstallReceipt{}, err
	}
	return receipt, nil
}

func validateAgentInstallSpec(spec AgentInstallSpec) error {
	if spec.InstallationID == uuid.Nil || spec.ConnectionID == uuid.Nil || strings.TrimSpace(spec.LogicalAgentID) == "" ||
		strings.TrimSpace(spec.DeploymentID) == "" || strings.TrimSpace(spec.OnboardingPackageID) == "" ||
		len(spec.OnboardingPackage) == 0 || spec.ReplicaCount < 2 || spec.ReplicaCount > 20 || strings.TrimSpace(spec.ArtifactCredential) == "" ||
		strings.TrimSpace(spec.CentralCAPEM) == "" || strings.TrimSpace(spec.Trust.Agent.BridgeServerPrivateKey) == "" ||
		strings.TrimSpace(spec.Trust.Agent.MCPClientPrivateKey) == "" || strings.TrimSpace(spec.Trust.Astronomer.BridgeClientPrivateKey) == "" ||
		strings.TrimSpace(spec.Trust.Astronomer.MCPServerPrivateKey) == "" {
		return fmt.Errorf("Charlie agent install input is incomplete")
	}
	if !strings.HasPrefix(spec.ChartReference, "oci://") || !exactDigest(spec.ChartDigest) || !exactDigest(spec.ImageDigest) ||
		!strings.HasSuffix(spec.ImageReference, "@"+spec.ImageDigest) || strings.TrimSpace(spec.ChartVersion) == "" ||
		strings.EqualFold(spec.ChartVersion, "latest") {
		return fmt.Errorf("Charlie agent artifacts must use reviewed immutable versions and digests")
	}
	parsed, err := url.Parse(spec.CentralURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("Charlie central URL is invalid")
	}
	chartURL, chartErr := url.Parse(spec.ChartReference)
	imageName := strings.TrimSuffix(spec.ImageReference, "@"+spec.ImageDigest)
	imageURL, imageErr := url.Parse("https://" + imageName)
	// Charlie v1 serves both canonical product-agent artifacts from the same
	// origin as central. External registries may mirror those immutable digests,
	// but a product onboarding package never silently redirects installation to
	// a second artifact authority.
	if chartErr != nil || chartURL.Scheme != "oci" || chartURL.User != nil || chartURL.RawQuery != "" || chartURL.Fragment != "" || chartURL.Host != parsed.Host || chartURL.Path != "/charlie/agent-chart" ||
		imageErr != nil || imageURL.User != nil || imageURL.RawQuery != "" || imageURL.Fragment != "" || imageURL.Host != parsed.Host || imageURL.Path != "/charlie/agent" {
		return fmt.Errorf("Charlie agent artifacts must use Charlie central OCI")
	}
	if len(spec.DisclosureDigest) != 64 {
		return fmt.Errorf("Charlie disclosure acknowledgement is required")
	}
	if len(spec.SecretIntegrityHMAC) != 64 {
		return fmt.Errorf("Charlie Secret integrity receipt is required")
	}
	if len(spec.ActionSigningPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("Charlie action signing trust is required")
	}
	pkg, err := contract.ParseOnboardingPackage(spec.OnboardingPackage)
	if err != nil || pkg.ReplicaCount != spec.ReplicaCount || string(pkg.LogicalAgentId) != spec.LogicalAgentID ||
		string(pkg.DeploymentId) != spec.DeploymentID || string(pkg.PackageId) != spec.OnboardingPackageID || string(pkg.Integration.IntegrationId) == "" {
		return fmt.Errorf("Charlie signed onboarding package does not match the agent installation")
	}
	slots := make(map[int]string, spec.ReplicaCount)
	credentials := make(map[string]struct{}, spec.ReplicaCount)
	artifactCredential := ""
	for _, credential := range pkg.Credentials {
		switch credential.Purpose {
		case contract.CredentialPurposeAgentEnrollment:
			_, duplicate := credentials[credential.Credential]
			if credential.ReplicaOrdinal == nil || *credential.ReplicaOrdinal < 0 || *credential.ReplicaOrdinal >= spec.ReplicaCount || slots[*credential.ReplicaOrdinal] != "" || duplicate {
				return fmt.Errorf("Charlie signed onboarding package has invalid replica slots")
			}
			slots[*credential.ReplicaOrdinal] = credential.Credential
			credentials[credential.Credential] = struct{}{}
		case contract.CredentialPurposeArtifactPull:
			if credential.ReplicaOrdinal != nil || artifactCredential != "" {
				return fmt.Errorf("Charlie signed onboarding package has invalid artifact authority")
			}
			artifactCredential = credential.Credential
		}
	}
	if len(slots) != spec.ReplicaCount || artifactCredential != spec.ArtifactCredential {
		return fmt.Errorf("Charlie signed onboarding package credentials do not match the agent installation")
	}
	keyDigest := sha256.Sum256(spec.ActionSigningPublicKey)
	if hex.EncodeToString(keyDigest[:]) != strings.ToLower(spec.ActionSigningKeyFingerprint) {
		return fmt.Errorf("Charlie action signing trust fingerprint is invalid")
	}
	for _, cidr := range append(append([]string(nil), spec.CentralCIDRs...), spec.Proxy.CIDRs...) {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil || network.String() == "0.0.0.0/0" || network.String() == "::/0" {
			return fmt.Errorf("Charlie network destination is invalid or overbroad")
		}
	}
	if spec.Proxy.ExistingSecret != "" && len(spec.Proxy.CIDRs) == 0 {
		return fmt.Errorf("Charlie proxy requires exact network destinations")
	}
	return nil
}

func exactDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func sameStableAgent(left, right AgentInstallSpec) bool {
	return left.InstallationID == right.InstallationID && left.ConnectionID == right.ConnectionID &&
		left.LogicalAgentID == right.LogicalAgentID && left.ReplicaCount == right.ReplicaCount &&
		left.Trust.Public.BridgeClientIdentityURI == right.Trust.Public.BridgeClientIdentityURI &&
		left.Trust.Public.MCPServerIdentityURI == right.Trust.Public.MCPServerIdentityURI &&
		left.Trust.Agent.BridgeServerIdentityURI == right.Trust.Agent.BridgeServerIdentityURI &&
		left.Trust.Agent.MCPClientIdentityURI == right.Trust.Agent.MCPClientIdentityURI
}

func agentResourceNames(spec AgentInstallSpec, namespace string) AgentResourceNames {
	suffix := strings.ReplaceAll(spec.InstallationID.String(), "-", "")[:12]
	prefix := strings.TrimSuffix(strings.TrimSpace(spec.SecretPrefix), "-")
	if prefix == "" {
		prefix = "charlie-agent-" + suffix
	}
	return AgentResourceNames{
		Application: "charlie-agent-" + suffix, Repository: prefix + "-repository",
		ImagePull: prefix + "-registry-pull", Enrollment: prefix + "-enrollment",
		BridgeTLS: prefix + "-bridge-tls", MCPClientTLS: prefix + "-mcp-tls",
		CentralCA: prefix + "-central-ca", MCPServerTLS: "astronomer-charlie-mcp-tls", BridgeClientTLS: "astronomer-charlie-bridge-client-tls",
		DefaultDeny: "charlie-default-deny", Bootstrap: prefix,
		ProductAccess: "astronomer-charlie-access", ResumeState: prefix + "-resume-state", AgentNamespace: namespace,
	}
}

func managedLabels(installationID uuid.UUID) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "astronomer", "app.kubernetes.io/part-of": "charlie",
		installationOwnerLabel: installationID.String(),
	}
}

func objectMeta(name, namespace string, installationID uuid.UUID) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: managedLabels(installationID)}
}

func installationSecrets(i *AgentInstaller, names AgentResourceNames, spec AgentInstallSpec) []*corev1.Secret {
	imageRepository := strings.Split(spec.ImageReference, "@")[0]
	registry := strings.Split(imageRepository, "/")[0]
	authValue := base64.StdEncoding.EncodeToString([]byte("artifact-token:" + spec.ArtifactCredential))
	dockerJSON, _ := json.Marshal(map[string]any{"auths": map[string]any{registry: map[string]string{
		"username": "artifact-token", "password": spec.ArtifactCredential, "auth": authValue,
	}}})
	chartRepository := spec.ChartReference
	repository := &corev1.Secret{
		ObjectMeta: objectMeta(names.Repository, i.argoNamespace, spec.InstallationID), Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"type": []byte("oci"), "name": []byte("charlie-agent-chart"), "url": []byte(chartRepository),
			"username": []byte("artifact-token"), "password": []byte(spec.ArtifactCredential),
		},
	}
	repository.Labels["argocd.argoproj.io/secret-type"] = "repository"
	return []*corev1.Secret{
		{ObjectMeta: objectMeta(names.Enrollment, i.agentNamespace, spec.InstallationID), Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"onboarding-package.json": append([]byte(nil), spec.OnboardingPackage...)}},
		{ObjectMeta: objectMeta(names.BridgeTLS, i.agentNamespace, spec.InstallationID), Type: corev1.SecretTypeTLS, Data: map[string][]byte{
			"tls.crt": []byte(spec.Trust.Agent.BridgeServerCertificate), "tls.key": []byte(spec.Trust.Agent.BridgeServerPrivateKey), "ca.crt": []byte(spec.Trust.Agent.CACertificatePEM),
		}},
		{ObjectMeta: objectMeta(names.MCPClientTLS, i.agentNamespace, spec.InstallationID), Type: corev1.SecretTypeTLS, Data: map[string][]byte{
			"tls.crt": []byte(spec.Trust.Agent.MCPClientCertificate), "tls.key": []byte(spec.Trust.Agent.MCPClientPrivateKey), "ca.crt": []byte(spec.Trust.Agent.CACertificatePEM),
		}},
		{ObjectMeta: objectMeta(names.CentralCA, i.agentNamespace, spec.InstallationID), Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"ca.crt": []byte(spec.CentralCAPEM)}},
		{ObjectMeta: objectMeta(names.ImagePull, i.agentNamespace, spec.InstallationID), Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: dockerJSON}},
		repository,
		{ObjectMeta: objectMeta(names.MCPServerTLS, i.productNamespace, spec.InstallationID), Type: corev1.SecretTypeTLS, Data: map[string][]byte{
			"tls.crt": []byte(spec.Trust.Public.MCPServerCertificate), "tls.key": []byte(spec.Trust.Astronomer.MCPServerPrivateKey), "ca.crt": []byte(spec.Trust.Public.CACertificatePEM),
			"action-signing-public-key": append([]byte(nil), spec.ActionSigningPublicKey...),
		}},
		{ObjectMeta: objectMeta(names.BridgeClientTLS, i.productNamespace, spec.InstallationID), Type: corev1.SecretTypeTLS, Data: map[string][]byte{
			"tls.crt": []byte(spec.Trust.Public.BridgeClientCertificate), "tls.key": []byte(spec.Trust.Astronomer.BridgeClientPrivateKey), "ca.crt": []byte(spec.Trust.Public.CACertificatePEM),
		}},
	}
}

func defaultDenyPolicy(names AgentResourceNames, installationID uuid.UUID) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: objectMeta(names.DefaultDeny, names.AgentNamespace, installationID),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		},
	}
}

func productAccessPolicy(i *AgentInstaller, names AgentResourceNames, installationID uuid.UUID) *networkingv1.NetworkPolicy {
	port := intstr.FromInt32(charlieMCPPort)
	bridgePort := intstr.FromInt32(7443)
	protocol := corev1.ProtocolTCP
	agentNamespace := metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": i.agentNamespace}}
	agentPods := metav1.LabelSelector{MatchLabels: map[string]string{
		"app.kubernetes.io/name": charlieAgentWorkloadName, "app.kubernetes.io/component": "product-agent",
	}}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: objectMeta(names.ProductAccess, i.productNamespace, installationID),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: copyStringMap(i.productPodLabels)},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &agentNamespace, PodSelector: &agentPods}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &protocol, Port: &port}},
			}},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To:    []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &agentNamespace, PodSelector: &agentPods}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &protocol, Port: &bridgePort}},
			}},
		},
	}
}

func mcpService(i *AgentInstaller, installationID uuid.UUID) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: objectMeta(charlieMCPServiceName, i.productNamespace, installationID),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP, Selector: copyStringMap(i.productPodLabels),
			Ports: []corev1.ServicePort{{Name: "charlie-mcp", Port: charlieMCPPort, TargetPort: intstr.FromInt32(charlieMCPPort), Protocol: corev1.ProtocolTCP}},
		},
	}
}

func (i *AgentInstaller) application(spec AgentInstallSpec, names AgentResourceNames) (*unstructured.Unstructured, error) {
	checksum := func(values ...string) string {
		hash := sha256.Sum256([]byte(strings.Join(values, "|")))
		return hex.EncodeToString(hash[:])
	}
	imageRepository := strings.Split(spec.ImageReference, "@")[0]
	proxyCIDRs := spec.Proxy.CIDRs
	if proxyCIDRs == nil {
		proxyCIDRs = []string{}
	}
	values := map[string]any{
		"replicaCount": spec.ReplicaCount, "nameOverride": charlieAgentWorkloadName, "fullnameOverride": charlieAgentWorkloadName,
		"image":   map[string]any{"repository": imageRepository, "digest": spec.ImageDigest, "tag": spec.ChartVersion, "pullPolicy": "IfNotPresent"},
		"runtime": map[string]any{"enabled": true, "disclosureAcknowledgement": spec.DisclosureDigest},
		"charlie": map[string]any{"baseUrl": spec.CentralURL, "agentId": spec.LogicalAgentID, "product": "astronomer", "environment": spec.EnvironmentID, "tenant": spec.TenantID},
		"bridge": map[string]any{
			"enabled": true, "port": 7443, "serverNames": []string{"charlie-agent-bridge." + i.agentNamespace + ".svc"},
			"trustedClientIdentities":  []string{spec.Trust.Public.BridgeClientIdentityURI},
			"productNamespaceSelector": map[string]string{"kubernetes.io/metadata.name": i.productNamespace},
			"productPodSelector":       copyStringMap(i.productPodLabels),
		},
		"mcp": map[string]any{"host": charlieMCPServiceName + "." + i.productNamespace + ".svc", "port": 7444, "serverIdentity": spec.Trust.Public.MCPServerIdentityURI},
		"existingSecrets": map[string]any{
			"enrollment":   map[string]any{"name": names.Enrollment, "key": "onboarding-package.json", "replicaCount": spec.ReplicaCount, "rolloutChecksum": checksum(spec.SecretIntegrityHMAC, "enrollment")},
			"bridgeTLS":    map[string]any{"name": names.BridgeTLS, "certificateKey": "tls.crt", "privateKeyKey": "tls.key", "clientCAKey": "ca.crt", "rolloutChecksum": checksum(spec.SecretIntegrityHMAC, "bridge-tls")},
			"mcpTLS":       map[string]any{"name": names.MCPClientTLS, "certificateKey": "tls.crt", "privateKeyKey": "tls.key", "serverCAKey": "ca.crt", "rolloutChecksum": checksum(spec.SecretIntegrityHMAC, "mcp-tls")},
			"charlieCA":    map[string]any{"name": names.CentralCA, "key": "ca.crt", "rolloutChecksum": checksum(spec.SecretIntegrityHMAC, "central-ca")},
			"registryPull": map[string]any{"name": names.ImagePull, "rolloutChecksum": checksum(spec.SecretIntegrityHMAC, "registry-pull")},
		},
		"proxy": map[string]any{
			"enabled": spec.Proxy.ExistingSecret != "", "existingSecret": spec.Proxy.ExistingSecret,
			"httpsProxyKey": "https-proxy", "httpProxyKey": "http-proxy", "noProxyKey": "no-proxy",
			"rolloutChecksum": checksum(spec.Proxy.ExistingSecret),
		},
		"networkPolicy": map[string]any{
			"enabled": true,
			"dns":     map[string]any{"namespaceSelector": map[string]string{"kubernetes.io/metadata.name": "kube-system"}, "podSelector": map[string]string{"k8s-app": "kube-dns"}},
			"central": map[string]any{"port": centralPort(spec.CentralURL), "cidrs": spec.CentralCIDRs, "egressGateway": map[string]any{"namespaceSelector": map[string]string{}, "podSelector": map[string]string{}, "port": centralPort(spec.CentralURL)}},
			"mcp":     map[string]any{"cidrs": []string{}, "namespaceSelector": map[string]string{"kubernetes.io/metadata.name": i.productNamespace}, "podSelector": copyStringMap(i.productPodLabels)},
			"proxy":   map[string]any{"cidrs": proxyCIDRs, "namespaceSelector": map[string]string{}, "podSelector": map[string]string{}, "port": 8443},
		},
		"podAntiAffinity": map[string]any{"topologyKey": "kubernetes.io/hostname", "required": false},
	}
	valuesJSON, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{
			"name": names.Application, "namespace": i.argoNamespace, "labels": stringMapAny(managedLabels(spec.InstallationID)),
			"annotations": map[string]any{"astronomer.io/charlie-chart-digest": spec.ChartDigest, "astronomer.io/charlie-image-digest": spec.ImageDigest},
		},
		"spec": map[string]any{
			"project": "default",
			// Argo CD's native OCI source accepts a tag or digest in
			// targetRevision. Charlie deliberately supplies the digest: its
			// public /v2 boundary rejects tag pulls and authorizes only the
			// exact release closure recorded in the signed onboarding package.
			"source":      map[string]any{"repoURL": spec.ChartReference, "path": ".", "targetRevision": spec.ChartDigest, "helm": map[string]any{"releaseName": names.Application, "values": string(valuesJSON)}},
			"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": i.agentNamespace},
			"syncPolicy":  map[string]any{"automated": map[string]any{"prune": true, "selfHeal": true}, "syncOptions": []any{"CreateNamespace=false"}, "retry": map[string]any{"limit": int64(5)}},
		},
	}}, nil
}

func centralPort(rawURL string) int32 {
	parsed, _ := url.Parse(rawURL)
	if parsed.Port() != "" {
		var port int
		_, _ = fmt.Sscanf(parsed.Port(), "%d", &port)
		if port > 0 && port <= 65535 {
			return int32(port)
		}
	}
	return 443
}

func (i *AgentInstaller) resolveCentralCIDRs(ctx context.Context, rawURL string) ([]string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("resolve Charlie central destination: invalid URL")
	}
	addresses, err := i.resolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("resolve Charlie central destination")
	}
	var cidrs []string
	seen := map[string]bool{}
	for _, address := range addresses {
		cidr := address.IP.String() + "/128"
		if address.IP.To4() != nil {
			cidr = address.IP.String() + "/32"
		}
		if !seen[cidr] {
			seen[cidr] = true
			cidrs = append(cidrs, cidr)
		}
	}
	return cidrs, nil
}

func (i *AgentInstaller) reconcileNamespace(ctx context.Context, installationID uuid.UUID) (func(context.Context) error, error) {
	resources := i.kube.CoreV1().Namespaces()
	current, err := resources.Get(ctx, i.agentNamespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		desired := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: i.agentNamespace, Labels: map[string]string{
			"kubernetes.io/metadata.name": i.agentNamespace, "pod-security.kubernetes.io/enforce": "restricted",
			"pod-security.kubernetes.io/enforce-version": "latest", installationOwnerLabel: installationID.String(),
			"app.kubernetes.io/managed-by": "astronomer",
		}}}
		if _, err := resources.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return nil, err
		}
		// Retain the private namespace after a partial failure. Removing a
		// namespace is destructive and could race unrelated operator resources.
		return func(context.Context) error { return nil }, nil
	}
	if err != nil {
		return nil, err
	}
	previous := current.DeepCopy()
	updated := current.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = map[string]string{}
	}
	updated.Labels["pod-security.kubernetes.io/enforce"] = "restricted"
	updated.Labels["pod-security.kubernetes.io/enforce-version"] = "latest"
	if _, err := resources.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return nil, err
	}
	return func(rollbackCtx context.Context) error {
		live, err := resources.Get(rollbackCtx, previous.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		previous.ResourceVersion = live.ResourceVersion
		_, err = resources.Update(rollbackCtx, previous, metav1.UpdateOptions{})
		return err
	}, nil
}

func (i *AgentInstaller) reconcileSecret(ctx context.Context, desired *corev1.Secret) (func(context.Context) error, error) {
	resources := i.kube.CoreV1().Secrets(desired.Namespace)
	current, err := resources.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := resources.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return nil, err
		}
		return func(rollbackCtx context.Context) error {
			err := resources.Delete(rollbackCtx, desired.Name, metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if current.Labels[installationOwnerLabel] != desired.Labels[installationOwnerLabel] {
		return nil, fmt.Errorf("refuse to overwrite operator-owned Secret %s/%s", desired.Namespace, desired.Name)
	}
	previous := current.DeepCopy()
	desired.ResourceVersion = current.ResourceVersion
	if _, err := resources.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return nil, err
	}
	return func(rollbackCtx context.Context) error {
		live, err := resources.Get(rollbackCtx, previous.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		previous.ResourceVersion = live.ResourceVersion
		_, err = resources.Update(rollbackCtx, previous, metav1.UpdateOptions{})
		return err
	}, nil
}

func (i *AgentInstaller) reconcileNetworkPolicy(ctx context.Context, desired *networkingv1.NetworkPolicy) (func(context.Context) error, error) {
	resources := i.kube.NetworkingV1().NetworkPolicies(desired.Namespace)
	current, err := resources.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := resources.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return nil, err
		}
		return func(rollbackCtx context.Context) error {
			err := resources.Delete(rollbackCtx, desired.Name, metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if current.Labels[installationOwnerLabel] != desired.Labels[installationOwnerLabel] {
		return nil, fmt.Errorf("refuse to overwrite operator-owned NetworkPolicy")
	}
	previous := current.DeepCopy()
	desired.ResourceVersion = current.ResourceVersion
	if _, err := resources.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return nil, err
	}
	return func(rollbackCtx context.Context) error {
		live, err := resources.Get(rollbackCtx, previous.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		previous.ResourceVersion = live.ResourceVersion
		_, err = resources.Update(rollbackCtx, previous, metav1.UpdateOptions{})
		return err
	}, nil
}

func (i *AgentInstaller) reconcileService(ctx context.Context, desired *corev1.Service) (func(context.Context) error, error) {
	resources := i.kube.CoreV1().Services(desired.Namespace)
	current, err := resources.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := resources.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return nil, err
		}
		return func(rollbackCtx context.Context) error {
			err := resources.Delete(rollbackCtx, desired.Name, metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if current.Labels[installationOwnerLabel] != desired.Labels[installationOwnerLabel] {
		return nil, fmt.Errorf("refuse to overwrite operator-owned Service")
	}
	previous := current.DeepCopy()
	desired.ResourceVersion, desired.Spec.ClusterIP, desired.Spec.ClusterIPs = current.ResourceVersion, current.Spec.ClusterIP, current.Spec.ClusterIPs
	if _, err := resources.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return nil, err
	}
	return func(rollbackCtx context.Context) error {
		live, err := resources.Get(rollbackCtx, previous.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		previous.ResourceVersion = live.ResourceVersion
		_, err = resources.Update(rollbackCtx, previous, metav1.UpdateOptions{})
		return err
	}, nil
}

func (i *AgentInstaller) reconcileApplication(ctx context.Context, desired *unstructured.Unstructured) (func(context.Context) error, error) {
	resources := i.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(i.argoNamespace)
	current, err := resources.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := resources.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return nil, err
		}
		return func(rollbackCtx context.Context) error {
			err := resources.Delete(rollbackCtx, desired.GetName(), metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if current.GetLabels()[installationOwnerLabel] != desired.GetLabels()[installationOwnerLabel] {
		return nil, fmt.Errorf("refuse to overwrite operator-owned Argo Application")
	}
	previous := current.DeepCopy()
	desired.SetResourceVersion(current.GetResourceVersion())
	if _, err := resources.Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return nil, err
	}
	return func(rollbackCtx context.Context) error {
		live, err := resources.Get(rollbackCtx, previous.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		previous.SetResourceVersion(live.GetResourceVersion())
		_, err = resources.Update(rollbackCtx, previous, metav1.UpdateOptions{})
		return err
	}, nil
}

func (i *AgentInstaller) deleteApplication(ctx context.Context, name string, installationID uuid.UUID) error {
	resources := i.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(i.argoNamespace)
	current, err := resources.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.GetLabels()[installationOwnerLabel] != installationID.String() {
		return fmt.Errorf("refuse to delete operator-owned Argo Application")
	}
	propagation := metav1.DeletePropagationForeground
	return resources.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &propagation})
}

const applicationSnapshotKey = "application.json"

func (i *AgentInstaller) snapshotApplication(ctx context.Context, names AgentResourceNames, installationID uuid.UUID) error {
	resources := i.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(i.argoNamespace)
	application, err := resources.Get(ctx, names.Application, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, snapshotErr := i.loadApplicationSnapshot(ctx, names, installationID)
		return snapshotErr
	}
	if err != nil {
		return err
	}
	if application.GetLabels()[installationOwnerLabel] != installationID.String() {
		return fmt.Errorf("refuse to snapshot operator-owned Argo Application")
	}
	clean := application.DeepCopy()
	clean.SetResourceVersion("")
	clean.SetUID("")
	clean.SetGeneration(0)
	clean.SetCreationTimestamp(metav1.Time{})
	clean.SetManagedFields(nil)
	delete(clean.Object, "status")
	encoded, err := json.Marshal(clean.Object)
	if err != nil || len(encoded) == 0 || len(encoded) > 512<<10 {
		return fmt.Errorf("Charlie resume state is invalid or too large")
	}
	desired := &corev1.ConfigMap{
		ObjectMeta: objectMeta(names.ResumeState, i.productNamespace, installationID),
		Data:       map[string]string{applicationSnapshotKey: string(encoded)},
	}
	current, err := i.kube.CoreV1().ConfigMaps(i.productNamespace).Get(ctx, names.ResumeState, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = i.kube.CoreV1().ConfigMaps(i.productNamespace).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if current.Labels[installationOwnerLabel] != installationID.String() {
		return fmt.Errorf("refuse to overwrite operator-owned Charlie resume state")
	}
	desired.ResourceVersion = current.ResourceVersion
	_, err = i.kube.CoreV1().ConfigMaps(i.productNamespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func (i *AgentInstaller) loadApplicationSnapshot(ctx context.Context, names AgentResourceNames, installationID uuid.UUID) (*unstructured.Unstructured, error) {
	snapshot, err := i.kube.CoreV1().ConfigMaps(i.productNamespace).Get(ctx, names.ResumeState, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("load Charlie resume state: %w", err)
	}
	if snapshot.Labels[installationOwnerLabel] != installationID.String() {
		return nil, fmt.Errorf("refuse to restore operator-owned Charlie resume state")
	}
	raw := snapshot.Data[applicationSnapshotKey]
	if len(raw) == 0 || len(raw) > 512<<10 {
		return nil, fmt.Errorf("Charlie resume state is invalid")
	}
	application := &unstructured.Unstructured{}
	if json.Unmarshal([]byte(raw), &application.Object) != nil || application.GetAPIVersion() != "argoproj.io/v1alpha1" || application.GetKind() != "Application" ||
		application.GetName() != names.Application || application.GetNamespace() != i.argoNamespace || application.GetLabels()[installationOwnerLabel] != installationID.String() {
		return nil, fmt.Errorf("Charlie resume state ownership is invalid")
	}
	destination, ok, err := unstructured.NestedString(application.Object, "spec", "destination", "namespace")
	if err != nil || !ok || destination != i.agentNamespace {
		return nil, fmt.Errorf("Charlie resume destination is invalid")
	}
	return application, nil
}

func (i *AgentInstaller) deleteRuntimeResources(ctx context.Context, names AgentResourceNames, installationID uuid.UUID) error {
	service, err := i.kube.CoreV1().Services(i.productNamespace).Get(ctx, charlieMCPServiceName, metav1.GetOptions{})
	if err == nil {
		if service.Labels[installationOwnerLabel] != installationID.String() {
			return fmt.Errorf("refuse to delete operator-owned Charlie MCP Service")
		}
		err = i.kube.CoreV1().Services(i.productNamespace).Delete(ctx, charlieMCPServiceName, metav1.DeleteOptions{})
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	policy, err := i.kube.NetworkingV1().NetworkPolicies(i.productNamespace).Get(ctx, names.ProductAccess, metav1.GetOptions{})
	if err == nil {
		if policy.Labels[installationOwnerLabel] != installationID.String() {
			return fmt.Errorf("refuse to delete operator-owned Charlie access NetworkPolicy")
		}
		err = i.kube.NetworkingV1().NetworkPolicies(i.productNamespace).Delete(ctx, names.ProductAccess, metav1.DeleteOptions{})
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	statefulSet, err := i.kube.AppsV1().StatefulSets(i.agentNamespace).Get(ctx, charlieAgentWorkloadName, metav1.GetOptions{})
	if err == nil {
		instance := statefulSet.Labels["app.kubernetes.io/instance"]
		if instance != "" && instance != names.Application {
			return fmt.Errorf("refuse to delete operator-owned Charlie StatefulSet")
		}
		propagation := metav1.DeletePropagationForeground
		err = i.kube.AppsV1().StatefulSets(i.agentNamespace).Delete(ctx, charlieAgentWorkloadName, metav1.DeleteOptions{PropagationPolicy: &propagation})
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return i.waitRuntimeStopped(ctx)
}

func (i *AgentInstaller) waitRuntimeStopped(ctx context.Context) error {
	ticker := time.NewTicker(i.pollInterval)
	defer ticker.Stop()
	selector := "app.kubernetes.io/name=" + charlieAgentWorkloadName + ",app.kubernetes.io/component=product-agent"
	for {
		_, statefulSetErr := i.kube.AppsV1().StatefulSets(i.agentNamespace).Get(ctx, charlieAgentWorkloadName, metav1.GetOptions{})
		pods, podErr := i.kube.CoreV1().Pods(i.agentNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if apierrors.IsNotFound(statefulSetErr) && podErr == nil && len(pods.Items) == 0 {
			return nil
		}
		if statefulSetErr != nil && !apierrors.IsNotFound(statefulSetErr) {
			return statefulSetErr
		}
		if podErr != nil {
			return podErr
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Charlie agent runtime drain is incomplete: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (i *AgentInstaller) deleteOwnedResources(ctx context.Context, names AgentResourceNames, installationID uuid.UUID) error {
	if err := i.deleteRuntimeResources(ctx, names, installationID); err != nil {
		return err
	}
	targets := []struct{ namespace, name string }{
		{i.agentNamespace, names.Enrollment}, {i.agentNamespace, names.BridgeTLS}, {i.agentNamespace, names.MCPClientTLS},
		{i.agentNamespace, names.CentralCA}, {i.agentNamespace, names.ImagePull}, {i.argoNamespace, names.Repository},
		{i.productNamespace, names.MCPServerTLS}, {i.productNamespace, names.BridgeClientTLS}, {i.agentNamespace, names.Bootstrap},
	}
	for _, target := range targets {
		secret, err := i.kube.CoreV1().Secrets(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if secret.Labels[installationOwnerLabel] != installationID.String() {
			return fmt.Errorf("refuse to delete operator-owned Secret")
		}
		if err := i.kube.CoreV1().Secrets(target.namespace).Delete(ctx, target.name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	policy, err := i.kube.NetworkingV1().NetworkPolicies(i.agentNamespace).Get(ctx, names.DefaultDeny, metav1.GetOptions{})
	if err == nil && policy.Labels[installationOwnerLabel] == installationID.String() {
		err = i.kube.NetworkingV1().NetworkPolicies(i.agentNamespace).Delete(ctx, names.DefaultDeny, metav1.DeleteOptions{})
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	resumeState, err := i.kube.CoreV1().ConfigMaps(i.productNamespace).Get(ctx, names.ResumeState, metav1.GetOptions{})
	if err == nil {
		if resumeState.Labels[installationOwnerLabel] != installationID.String() {
			return fmt.Errorf("refuse to delete operator-owned Charlie resume state")
		}
		err = i.kube.CoreV1().ConfigMaps(i.productNamespace).Delete(ctx, names.ResumeState, metav1.DeleteOptions{})
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func copyStringMap(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func stringMapAny(source map[string]string) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
