package charlie

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/google/uuid"
)

// AdminBridgeStatus is bounded health/configuration metadata reported by the
// fixed product-local bridge. It contains no central credentials or evidence.
type AdminBridgeStatus struct {
	CentralHealth, LogicalAgentID, DeploymentID, IntegrationID, InstanceID, LeaderInstanceID string
	IntegrationRevision, DisclosureDigest, CentralMode, ProductModeCeiling, EffectiveMode    string
	RouteID, ArtifactVersion                                                                 string
	Epoch, ReplicaCount, ReplicaOrdinal                                                      int64
	ProductEnabled, DeploymentEnabled, EffectiveEnabled, EmergencyDisabled                   bool
	AutoAllowlist                                                                            []string
}

func (m *ManagedBridge) RequestCapabilityRediscovery(ctx context.Context) (contract.IntegrationRediscoveryReceipt, error) {
	bridge, err := m.configurationRuntimeBridge(ctx)
	if err != nil {
		return contract.IntegrationRediscoveryReceipt{}, err
	}
	var status contract.BridgeStatus
	if err := bridge.runtime.DoJSON(ctx, http.MethodGet, "/status", "", nil, &status); err != nil {
		return contract.IntegrationRediscoveryReceipt{}, err
	}
	integrationID, revision := string(status.IntegrationId), string(status.IntegrationRevision)
	receipt, err := bridge.runtime.RequestIntegrationRediscovery(ctx, integrationID, revision, uuid.NewString())
	if err != nil {
		return contract.IntegrationRediscoveryReceipt{}, err
	}
	if receipt.IntegrationID != integrationID {
		return contract.IntegrationRediscoveryReceipt{}, fmt.Errorf("Charlie rediscovery installation changed")
	}
	m.notifyActivationChanged(ctx)
	return receipt, nil
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
		DeploymentID: string(status.DeploymentId), IntegrationID: string(status.IntegrationId),
		InstanceID: string(status.InstanceId), LeaderInstanceID: string(status.LeaderInstanceId),
		IntegrationRevision: string(status.IntegrationRevision), DisclosureDigest: status.DisclosureDigest,
		CentralMode: string(status.CentralMode), ProductModeCeiling: string(status.ProductModeCeiling),
		EffectiveMode: string(status.EffectiveMode), RouteID: string(status.RouteId), ArtifactVersion: status.ArtifactVersion,
		Epoch: status.Epoch, ReplicaCount: int64(status.ReplicaCount), ReplicaOrdinal: int64(status.ReplicaOrdinal),
		ProductEnabled: status.ProductEnabled, DeploymentEnabled: status.DeploymentEnabled,
		EffectiveEnabled: status.EffectiveEnabled, EmergencyDisabled: status.EmergencyDisabled,
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
	if current, currentErr := b.bridge.AdminStatus(ctx); currentErr == nil &&
		Mode(current.ProductModeCeiling) == mode && (mode != ModeDisabled && current.ProductEnabled || mode == ModeDisabled && !current.ProductEnabled) {
		return modeStateFromBridge(current, revision+1), nil
	}
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

// Reconcile applies and reads back the product-agent authority ceiling through
// Charlie's fixed, mutually authenticated bridge. Workload installation and
// version rollout remain ordinary Flux delivery concerns; Astronomer does not
// patch Kubernetes workloads or create a second deployment controller.
func (b *managedModeBridge) Reconcile(ctx context.Context, target ModeCeilingTarget) error {
	if b == nil || b.bridge == nil || !validMode(target.Desired) || strings.TrimSpace(target.ConnectionID) == "" {
		return fmt.Errorf("Charlie mode-ceiling target is invalid")
	}
	status, err := b.bridge.SetAdminMode(ctx, target.Desired)
	if err != nil {
		return err
	}
	if Mode(status.ProductModeCeiling) != target.Desired {
		return fmt.Errorf("Charlie product agent did not confirm the requested mode ceiling")
	}
	if target.Desired == ModeDisabled && status.ProductEnabled {
		return fmt.Errorf("Charlie product agent remained enabled after the disabled ceiling")
	}
	return nil
}

func modeStateFromBridge(status AdminBridgeStatus, revision int64) ModeState {
	// CentralMode is Charlie's reviewed authority. EffectiveMode additionally
	// intersects the immutable product-agent rollout ceiling, so using it here
	// would erase a reviewed disclosure whenever the product deliberately keeps
	// work disabled while importing and acknowledging a new catalog.
	active := status.ProductEnabled && status.DeploymentEnabled
	mode := Mode(status.CentralMode)
	disclosure := status.DisclosureDigest
	if !active || !validMode(mode) {
		mode = ModeDisabled
		disclosure = ""
	}
	// Charlie's integration revision is the authority revision signed into
	// every action envelope. It may advance for catalog/disclosure changes as
	// well as mode changes, so a product-local +1 counter is not equivalent.
	if remoteRevision, err := strconv.ParseInt(strings.TrimSpace(status.IntegrationRevision), 10, 64); err == nil && remoteRevision > 0 {
		revision = remoteRevision
	}
	return ModeState{Requested: mode, Verified: mode, Revision: revision, DisclosureDigest: disclosure, Active: active}
}

type managedFindingChangeBridge struct{ bridge *ManagedBridge }

type FindingChangeBridge interface {
	FindingChanges(context.Context, int64, int) (contract.FindingChangePage, error)
}

func NewManagedFindingChangeBridge(bridge *ManagedBridge) FindingChangeBridge {
	if bridge == nil {
		return nil
	}
	return &managedFindingChangeBridge{bridge: bridge}
}

func (b *managedFindingChangeBridge) FindingChanges(ctx context.Context, cursor int64, limit int) (contract.FindingChangePage, error) {
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
