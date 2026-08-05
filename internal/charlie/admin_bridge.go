package charlie

import (
	"context"
	"fmt"
	"net/http"
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
	if !status.ProductEnabled || !validMode(mode) {
		mode = ModeDisabled
	}
	return ModeState{Requested: mode, Verified: mode, Revision: revision, DisclosureDigest: status.DisclosureDigest, Active: status.ProductEnabled}
}

type managedAgentLifecycleBridge struct{ bridge *ManagedBridge }

func NewManagedAgentLifecycleBridge(bridge *ManagedBridge) AgentBridgeLifecycle {
	if bridge == nil {
		return nil
	}
	return &managedAgentLifecycleBridge{bridge: bridge}
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

func (*managedAgentLifecycleBridge) VerifyCredentialRevoked(context.Context, string, string) error {
	return fmt.Errorf("Charlie credential revocation confirmation is unavailable")
}
