package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/google/uuid"
)

// AdminBridgeStatus is bounded health/configuration metadata reported by the
// fixed product-local bridge. It contains no central credentials or evidence.
type AdminBridgeStatus struct {
	CentralHealth, LogicalAgentID, InstanceID, LeaderInstanceID string
	IntegrationRevision, DisclosureDigest, EffectiveMode        string
	RouteID, ArtifactVersion                                    string
	Epoch, ReplicaCount, ReplicaOrdinal                         int64
	ProductEnabled, DeploymentEnabled, EffectiveEnabled         bool
	AutoAllowlist                                               []string
}

type adminBridgeStatusReader interface {
	AdminStatus(context.Context) (AdminBridgeStatus, error)
}

func (m *ManagedBridge) AdminStatus(ctx context.Context) (AdminBridgeStatus, error) {
	bridge, err := m.configurationRuntimeBridge(ctx)
	if err != nil {
		return AdminBridgeStatus{}, err
	}
	var response contract.BridgeStatus
	if err := bridge.runtime.DoJSON(ctx, http.MethodGet, "/status", "", nil, &response); err != nil {
		return AdminBridgeStatus{}, err
	}
	return adminBridgeStatus(response), nil
}

func (m *ManagedBridge) SetAdminMode(ctx context.Context, desired Mode) (AdminBridgeStatus, error) {
	if !validMode(desired) {
		return AdminBridgeStatus{}, fmt.Errorf("Charlie mode is invalid")
	}
	bridge, err := m.configurationRuntimeBridge(ctx)
	if err != nil {
		return AdminBridgeStatus{}, err
	}
	var status contract.BridgeStatus
	if err := bridge.runtime.DoJSON(ctx, http.MethodGet, "/status", "", nil, &status); err != nil {
		return AdminBridgeStatus{}, err
	}
	revision := string(status.IntegrationRevision)
	if desired == ModeDisabled {
		request := contract.ActivationRequest{ExpectedRevision: contract.OpaqueId(revision), ProductEnabled: false}
		if err := bridge.runtime.DoJSON(ctx, http.MethodPut, "/activation", uuid.NewString(), request, &status); err != nil {
			return AdminBridgeStatus{}, err
		}
		result := adminBridgeStatus(status)
		result.EffectiveMode = string(ModeDisabled)
		return result, nil
	}
	request := contract.ModeRequest{ExpectedRevision: contract.OpaqueId(revision), Mode: contract.Mode(desired)}
	var mode contract.ModeResponse
	if err := bridge.runtime.DoJSON(ctx, http.MethodPut, "/mode", uuid.NewString(), request, &mode); err != nil {
		return AdminBridgeStatus{}, err
	}
	if !status.ProductEnabled {
		activation := contract.ActivationRequest{ExpectedRevision: mode.IntegrationRevision, ProductEnabled: true}
		if err := bridge.runtime.DoJSON(ctx, http.MethodPut, "/activation", uuid.NewString(), activation, &status); err != nil {
			return AdminBridgeStatus{}, err
		}
	} else if err := bridge.runtime.DoJSON(ctx, http.MethodGet, "/status", "", nil, &status); err != nil {
		return AdminBridgeStatus{}, err
	}
	result := adminBridgeStatus(status)
	if result.EffectiveMode != string(desired) || !result.ProductEnabled {
		return AdminBridgeStatus{}, fmt.Errorf("Charlie bridge did not confirm requested mode")
	}
	return result, nil
}

func adminBridgeStatus(status contract.BridgeStatus) AdminBridgeStatus {
	return AdminBridgeStatus{
		CentralHealth: string(status.CentralHealth), LogicalAgentID: string(status.LogicalAgentId),
		InstanceID: string(status.InstanceId), LeaderInstanceID: string(status.LeaderInstanceId),
		IntegrationRevision: string(status.IntegrationRevision), DisclosureDigest: status.DisclosureDigest,
		EffectiveMode: string(status.EffectiveMode), RouteID: string(status.RouteId), ArtifactVersion: status.ArtifactVersion,
		Epoch: status.Epoch, ReplicaCount: int64(status.ReplicaCount), ReplicaOrdinal: int64(status.ReplicaOrdinal),
		ProductEnabled: status.ProductEnabled, DeploymentEnabled: status.DeploymentEnabled, EffectiveEnabled: status.EffectiveEnabled,
		AutoAllowlist: append([]string(nil), status.AutoAllowlist...),
	}
}

type managedModeBridge struct{ bridge *ManagedBridge }

func NewManagedModeBridge(bridge *ManagedBridge) AgentModeBridge {
	if bridge == nil {
		return nil
	}
	return &managedModeBridge{bridge: bridge}
}

func (b *managedModeBridge) SetMode(ctx context.Context, mode Mode, revision int64) (ModeState, error) {
	status, err := b.bridge.SetAdminMode(ctx, mode)
	if err != nil {
		return ModeState{}, err
	}
	return modeStateFromBridge(status, revision+1), nil
}

func (b *managedModeBridge) Status(ctx context.Context) (ModeState, error) {
	status, err := b.bridge.AdminStatus(ctx)
	if err != nil {
		return ModeState{}, err
	}
	return modeStateFromBridge(status, 0), nil
}

func modeStateFromBridge(status AdminBridgeStatus, revision int64) ModeState {
	mode := Mode(status.EffectiveMode)
	if !status.EffectiveEnabled || !validMode(mode) {
		mode = ModeDisabled
	}
	// Charlie's integration revision is the authority revision signed into
	// every action envelope. It may advance for catalog/disclosure changes as
	// well as mode changes, so a product-local +1 counter is not equivalent.
	if remoteRevision, err := strconv.ParseInt(strings.TrimSpace(status.IntegrationRevision), 10, 64); err == nil && remoteRevision > 0 {
		revision = remoteRevision
	}
	return ModeState{Requested: mode, Verified: mode, Revision: revision, DisclosureDigest: status.DisclosureDigest, Active: status.EffectiveEnabled}
}

type managedAgentLifecycleBridge struct{ bridge *ManagedBridge }

// ArtifactCredentialClaim binds a durable product-owned request to the exact
// active package generation. The product agent validates these values again
// before it signs and forwards the request to Charlie Central.
type ArtifactCredentialClaim struct {
	RequestID, DeploymentID, IntegrationID, PackageID string
	CurrentGeneration                                 int64
}

// ArtifactCredentialAcknowledgement commits a replacement only after both
// product-owned Kubernetes credential consumers have been materialized and
// read back. The digest is content-derived but reveals no credential bytes.
type ArtifactCredentialAcknowledgement struct {
	RequestID, LeaseID, MaterializationDigest string
	Generation                                int64
}

type ArtifactCredentialBridge interface {
	ArtifactCredentialStatus(context.Context) (contract.ArtifactCredentialLease, error)
	ClaimArtifactCredential(context.Context, ArtifactCredentialClaim) (contract.ArtifactCredentialLease, error)
	AcknowledgeArtifactCredential(context.Context, ArtifactCredentialAcknowledgement) (contract.ArtifactCredentialLease, error)
}

type FindingChangeBridge interface {
	FindingChanges(context.Context, int64, int) (contract.FindingChangePage, error)
}

func NewManagedAgentLifecycleBridge(bridge *ManagedBridge) AgentBridgeLifecycle {
	if bridge == nil {
		return nil
	}
	return &managedAgentLifecycleBridge{bridge: bridge}
}

func (b *managedAgentLifecycleBridge) ArtifactCredentialStatus(ctx context.Context) (contract.ArtifactCredentialLease, error) {
	if b == nil || b.bridge == nil {
		return contract.ArtifactCredentialLease{}, fmt.Errorf("Charlie artifact credential bridge is unavailable")
	}
	runtimeBridge, err := b.bridge.configurationRuntimeBridge(ctx)
	if err != nil {
		return contract.ArtifactCredentialLease{}, err
	}
	var lease contract.ArtifactCredentialLease
	if err := runtimeBridge.runtime.DoJSON(ctx, http.MethodGet, "/lifecycle/artifacts/credentials", "", nil, &lease); err != nil {
		return contract.ArtifactCredentialLease{}, err
	}
	return lease, nil
}

func (b *managedAgentLifecycleBridge) ClaimArtifactCredential(ctx context.Context, claim ArtifactCredentialClaim) (contract.ArtifactCredentialLease, error) {
	if b == nil || b.bridge == nil || claim.RequestID == "" || claim.DeploymentID == "" || claim.IntegrationID == "" || claim.PackageID == "" || claim.CurrentGeneration < 1 {
		return contract.ArtifactCredentialLease{}, fmt.Errorf("Charlie artifact credential claim binding is incomplete")
	}
	runtimeBridge, err := b.bridge.configurationRuntimeBridge(ctx)
	if err != nil {
		return contract.ArtifactCredentialLease{}, err
	}
	request := contract.ArtifactCredentialClaimRequest{
		RequestId: contract.OpaqueId(claim.RequestID), CurrentGeneration: claim.CurrentGeneration,
		ExpectedDeploymentId: contract.OpaqueId(claim.DeploymentID), ExpectedIntegrationId: contract.OpaqueId(claim.IntegrationID), ExpectedPackageId: contract.OpaqueId(claim.PackageID),
	}
	var lease contract.ArtifactCredentialLease
	if err := runtimeBridge.runtime.DoJSON(ctx, http.MethodPost, "/lifecycle/artifacts/credentials", claim.RequestID, request, &lease); err != nil {
		return contract.ArtifactCredentialLease{}, err
	}
	return lease, nil
}

func (b *managedAgentLifecycleBridge) AcknowledgeArtifactCredential(ctx context.Context, acknowledgement ArtifactCredentialAcknowledgement) (contract.ArtifactCredentialLease, error) {
	if b == nil || b.bridge == nil || acknowledgement.RequestID == "" || acknowledgement.LeaseID == "" || acknowledgement.Generation < 1 || !exactDigest(acknowledgement.MaterializationDigest) {
		return contract.ArtifactCredentialLease{}, fmt.Errorf("Charlie artifact credential acknowledgement binding is incomplete")
	}
	runtimeBridge, err := b.bridge.configurationRuntimeBridge(ctx)
	if err != nil {
		return contract.ArtifactCredentialLease{}, err
	}
	request := contract.ArtifactCredentialAcknowledgementRequest{
		RequestId: contract.OpaqueId(acknowledgement.RequestID), Generation: acknowledgement.Generation, MaterializationDigest: acknowledgement.MaterializationDigest,
	}
	var lease contract.ArtifactCredentialLease
	path := "/lifecycle/artifacts/credentials/" + url.PathEscape(acknowledgement.LeaseID) + "/acknowledgement"
	if err := runtimeBridge.runtime.DoJSON(ctx, http.MethodPost, path, acknowledgement.RequestID, request, &lease); err != nil {
		return contract.ArtifactCredentialLease{}, err
	}
	return lease, nil
}

func (b *managedAgentLifecycleBridge) FindingChanges(ctx context.Context, cursor int64, limit int) (contract.FindingChangePage, error) {
	if b == nil || b.bridge == nil || cursor < 0 || limit < 1 || limit > 100 {
		return contract.FindingChangePage{}, fmt.Errorf("Charlie finding change cursor is invalid")
	}
	runtimeBridge, err := b.bridge.configurationRuntimeBridge(ctx)
	if err != nil {
		return contract.FindingChangePage{}, err
	}
	path := "/lifecycle/findings/changes?cursor=" + strconv.FormatInt(cursor, 10) + "&limit=" + strconv.Itoa(limit)
	var page contract.FindingChangePage
	if err := runtimeBridge.runtime.DoJSON(ctx, http.MethodGet, path, "", nil, &page); err != nil {
		return contract.FindingChangePage{}, err
	}
	return page, nil
}

func (b *managedAgentLifecycleBridge) Status(ctx context.Context) (AgentBridgeStatus, error) {
	status, err := b.bridge.AdminStatus(ctx)
	if err != nil {
		return AgentBridgeStatus{}, err
	}
	return agentBridgeStatusFromAdmin(status), nil
}

func agentBridgeStatusFromAdmin(status AdminBridgeStatus) AgentBridgeStatus {
	return AgentBridgeStatus{
		BridgeReady: true,
		// Enrollment is an authenticated connectivity fact, not execution
		// authority. A freshly provisioned integration is deliberately disabled;
		// requiring DeploymentEnabled here would deadlock installation against
		// the later explicit mode activation.
		CentralEnrolled: status.CentralHealth == "healthy" && strings.TrimSpace(status.LogicalAgentID) != "" && strings.TrimSpace(status.IntegrationRevision) != "",
		LeaderElected:   status.Epoch > 0 && strings.TrimSpace(status.LeaderInstanceID) != "",
		StandbyVisible:  status.ReplicaCount >= 2, ProtocolCompatible: true,
		AgentProtocolVersion: contract.AgentProtocolVersion, BridgeProtocolVersion: contract.BridgeProtocolVersion,
	}
}

func (b *managedAgentLifecycleBridge) CentralHealth(ctx context.Context) error {
	status, err := b.bridge.AdminStatus(ctx)
	if err != nil {
		return err
	}
	if status.CentralHealth != "healthy" {
		return fmt.Errorf("Charlie central health is not healthy")
	}
	return nil
}

func (b *managedAgentLifecycleBridge) VerifyArtifactDigests(ctx context.Context, chartVersion, chartDigest, imageDigest string) error {
	if normalizeDigest(chartDigest) == "" || normalizeDigest(imageDigest) == "" || strings.TrimSpace(chartVersion) == "" {
		return fmt.Errorf("Charlie artifact metadata is incomplete")
	}
	status, err := b.bridge.AdminStatus(ctx)
	if err != nil || status.ArtifactVersion != chartVersion {
		return fmt.Errorf("Charlie agent artifact version is unverified")
	}
	return nil
}

func (b *managedAgentLifecycleBridge) Disable(ctx context.Context) error {
	_, err := b.bridge.SetAdminMode(ctx, ModeDisabled)
	return err
}

func (b *managedAgentLifecycleBridge) StopTriggerDispatch(ctx context.Context) error {
	status, err := b.bridge.AdminStatus(ctx)
	if err != nil {
		return err
	}
	if status.ProductEnabled || status.EffectiveEnabled {
		return fmt.Errorf("Charlie trigger dispatch is not disabled")
	}
	return nil
}

func (b *managedAgentLifecycleBridge) SettleStreams(ctx context.Context) error {
	return b.StopTriggerDispatch(ctx)
}

func (b *managedAgentLifecycleBridge) RevokeCredentialPackage(ctx context.Context, target CredentialRevocationTarget) error {
	runtime, publicKey, signingKeyID, replicas, err := b.revocationContext(ctx, target)
	if err != nil {
		return err
	}
	request := contract.CredentialRevocationRequest{
		RequestId: contract.OpaqueId(target.RequestID), Reason: contract.CredentialRevocationProductDisconnect,
		ExpectedDeploymentId: contract.OpaqueId(target.DeploymentID), ExpectedPackageId: contract.OpaqueId(target.PackageID),
		ExpectedIntegrationId: contract.OpaqueId(target.IntegrationID),
	}
	var raw json.RawMessage
	if err := runtime.DoJSON(ctx, http.MethodPost, "/lifecycle/credentials/revocation", target.RequestID, request, &raw); err != nil {
		return err
	}
	_, err = contract.VerifyCredentialRevocationReceipt(raw, publicKey, target.RequestID, target.DeploymentID, target.PackageID, target.IntegrationID, signingKeyID, replicas, false)
	return err
}

func (b *managedAgentLifecycleBridge) VerifyCredentialPackageRevoked(ctx context.Context, target CredentialRevocationTarget) error {
	runtime, publicKey, signingKeyID, replicas, err := b.revocationContext(ctx, target)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	path := "/lifecycle/credentials/revocation?request_id=" + url.QueryEscape(target.RequestID)
	if err := runtime.DoJSON(ctx, http.MethodGet, path, "", nil, &raw); err != nil {
		return err
	}
	_, err = contract.VerifyCredentialRevocationReceipt(raw, publicKey, target.RequestID, target.DeploymentID, target.PackageID, target.IntegrationID, signingKeyID, replicas, true)
	return err
}

func (b *managedAgentLifecycleBridge) revocationContext(ctx context.Context, target CredentialRevocationTarget) (*contract.Runtime, ed25519.PublicKey, string, int, error) {
	if b == nil || b.bridge == nil || target.RequestID == "" || target.DeploymentID == "" || target.PackageID == "" || target.IntegrationID == "" {
		return nil, nil, "", 0, fmt.Errorf("Charlie credential revocation binding is incomplete")
	}
	connection, configured := b.bridge.configurationConnection(ctx)
	if !configured || connection.DeploymentID != target.DeploymentID || connection.OnboardingPackageID != target.PackageID || connection.SigningKeyID == "" {
		return nil, nil, "", 0, fmt.Errorf("Charlie credential revocation binding does not match onboarding")
	}
	publicKey, err := os.ReadFile(b.bridge.config.SigningKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, nil, "", 0, fmt.Errorf("Charlie credential revocation signing trust is unavailable")
	}
	digest := sha256.Sum256(publicKey)
	if hex.EncodeToString(digest[:]) != strings.ToLower(connection.SigningKeyFingerprint) {
		return nil, nil, "", 0, fmt.Errorf("Charlie credential revocation signing trust does not match onboarding")
	}
	runtimeBridge, err := b.bridge.configurationRuntimeBridge(ctx)
	if err != nil {
		return nil, nil, "", 0, err
	}
	return runtimeBridge.runtime, ed25519.PublicKey(publicKey), connection.SigningKeyID, int(connection.ReplicaCount), nil
}
