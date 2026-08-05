package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAdminUnavailable         = errors.New("Charlie administration is unavailable")
	ErrAdminNotConfigured       = errors.New("Charlie is not configured")
	ErrAdminConflict            = errors.New("Charlie administration state changed")
	ErrReplacementPackageNeeded = errors.New("a new signed Charlie onboarding package is required")
)

type AdminConnectionView struct {
	Connected              bool   `json:"connected"`
	ProductID              string `json:"product_id,omitempty"`
	ProductSlug            string `json:"product_slug,omitempty"`
	DeploymentID           string `json:"deployment_id,omitempty"`
	RouteID                string `json:"route_id,omitempty"`
	CentralVersion         string `json:"central_version,omitempty"`
	SigningKeyID           string `json:"signing_key_id,omitempty"`
	SigningFingerprint     string `json:"signing_fingerprint,omitempty"`
	PackageDigest          string `json:"package_digest,omitempty"`
	DisclosureDigest       string `json:"disclosure_digest,omitempty"`
	DisclosureAcknowledged bool   `json:"disclosure_acknowledged"`
	UpdatedAt              string `json:"updated_at,omitempty"`
}

type AdminAgentView struct {
	ApplicationState string                  `json:"application_state"`
	DesiredReplicas  int32                   `json:"desired_replicas"`
	ReadyReplicas    int32                   `json:"ready_replicas"`
	LeaderReplica    string                  `json:"leader_replica,omitempty"`
	StandbyReplicas  []string                `json:"standby_replicas"`
	Replicas         []AdminAgentReplicaView `json:"replicas"`
	FencingEpoch     int64                   `json:"fencing_epoch,omitempty"`
	LastHeartbeatAt  string                  `json:"last_heartbeat_at,omitempty"`
	AgentVersion     string                  `json:"agent_version,omitempty"`
	ChartVersion     string                  `json:"chart_version,omitempty"`
	ChartDigest      string                  `json:"chart_digest,omitempty"`
	ImageDigest      string                  `json:"image_digest,omitempty"`
}

// AdminAgentReplicaView is safe product-observed status. Missing values remain
// absent rather than being inferred from StatefulSet ordinals or Secret data.
type AdminAgentReplicaView struct {
	Ordinal         int    `json:"ordinal"`
	InstanceID      string `json:"instance_id,omitempty"`
	Role            string `json:"role"`
	State           string `json:"state"`
	LastHeartbeatAt string `json:"last_heartbeat_at,omitempty"`
	Version         string `json:"version,omitempty"`
}

type AdminModeView struct {
	Requested                    Mode     `json:"requested"`
	Authoritative                Mode     `json:"authoritative"`
	Revision                     int64    `json:"revision"`
	EmergencyDisabled            bool     `json:"emergency_disabled"`
	DisablePending               bool     `json:"disable_pending,omitempty"`
	DisclosureDigest             string   `json:"disclosure_digest,omitempty"`
	AcknowledgedDisclosureDigest string   `json:"acknowledged_disclosure_digest,omitempty"`
	Effects                      []string `json:"effects"`
}

type AdminStatusView struct {
	Connection AdminConnectionView `json:"connection"`
	Agent      AdminAgentView      `json:"agent"`
	Mode       AdminModeView       `json:"mode"`
}

// openapi:request CharlieAdminTriggerRule
type AdminTriggerRule struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	SourceType            string   `json:"source_type"`
	Enabled               bool     `json:"enabled"`
	Severities            []string `json:"severities"`
	Scopes                []string `json:"scopes"`
	CooldownSeconds       int32    `json:"cooldown_seconds"`
	GracePeriodSeconds    int32    `json:"grace_period_seconds"`
	FlapWindowSeconds     int32    `json:"flap_window_seconds"`
	FlapCount             int32    `json:"flap_count"`
	FleetThresholdPercent int32    `json:"fleet_threshold_percent"`
	MinimumAgentVersion   string   `json:"minimum_agent_version,omitempty"`
	Suppressed            bool     `json:"suppressed"`
	MaximumAttempts       int32    `json:"maximum_attempts"`
	DeadLetterEnabled     bool     `json:"dead_letter_enabled"`
	ServiceIdentity       string   `json:"service_identity"`
}

type AdminAutomationView struct {
	Rules                  []AdminTriggerRule `json:"rules"`
	DefaultsRevision       int64              `json:"defaults_revision"`
	ServiceIdentityEnabled bool               `json:"service_identity_enabled"`
}

type AdminPermission struct {
	Permission string `json:"permission"`
	Scope      string `json:"scope"`
	Source     string `json:"source"`
}

type AdminAccessView struct {
	EffectivePermissions []AdminPermission `json:"effective_permissions"`
	AutomationGrants     []AdminPermission `json:"automation_grants"`
}

type AdminDiagnosticCheck struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	State     string `json:"state"`
	Summary   string `json:"summary"`
	CheckedAt string `json:"checked_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type AdminDiagnosticsView struct {
	Overall       string                 `json:"overall"`
	Checks        []AdminDiagnosticCheck `json:"checks"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
}

type AdminAgentInstaller interface {
	Status(context.Context, AgentInstallSpec) (AgentInstallationStatus, error)
	Uninstall(context.Context, AgentInstallSpec) error
	Disconnect(context.Context, AgentInstallSpec, string) error
}

type supersededAgentMaterialPruner interface {
	PruneSupersededRepositories(context.Context, AgentInstallSpec) error
	PruneSupersededSecrets(context.Context, AgentInstallSpec) error
}

type AdminService struct {
	pool      *pgxpool.Pool
	queries   *sqlc.Queries
	installer AdminAgentInstaller
	bridge    *ManagedBridge
	mode      *ModeController
	now       func() time.Time
	triggers  *TriggerAdminService
}

func NewAdminService(pool *pgxpool.Pool, installer AdminAgentInstaller, bridge *ManagedBridge) (*AdminService, error) {
	if pool == nil {
		return nil, ErrAdminUnavailable
	}
	queries := sqlc.New(pool)
	triggerAdmin, err := NewTriggerAdminService(queries)
	if err != nil {
		return nil, err
	}
	service := &AdminService{pool: pool, queries: queries, installer: installer, bridge: bridge, now: time.Now, triggers: triggerAdmin}
	if bridge != nil {
		controller, err := NewModeController(PGModeStore{Pool: pool}, NewManagedModeBridge(bridge))
		if err != nil {
			return nil, err
		}
		service.mode = controller
	}
	return service, nil
}

// SetWriteFence binds emergency administration to the same admission registry
// used by the private MCP runtime.
func (s *AdminService) SetWriteFence(fence *WriteFence) {
	if s != nil && s.mode != nil {
		s.mode.SetWriteFence(fence)
	}
}

// RunModeReconciler keeps the product-local authority snapshot current with
// Charlie central while preserving the local mode as an independent ceiling.
func (s *AdminService) RunModeReconciler(ctx context.Context, interval time.Duration) {
	if s != nil && s.mode != nil {
		s.mode.Run(ctx, interval)
	}
}

func (s *AdminService) connection(ctx context.Context) (sqlc.CharlieConnection, error) {
	if s == nil || s.queries == nil {
		return sqlc.CharlieConnection{}, ErrAdminUnavailable
	}
	connection, err := s.queries.GetLatestCharlieConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.CharlieConnection{}, ErrAdminNotConfigured
	}
	if err != nil {
		return sqlc.CharlieConnection{}, ErrAdminUnavailable
	}
	return connection, nil
}

func (s *AdminService) Status(ctx context.Context) (AdminStatusView, error) {
	// Status is also an immediate reconciliation boundary so an administrator
	// never has to wait for the background interval after a central policy edit.
	if s != nil && s.mode != nil {
		_, _ = s.mode.Reconcile(ctx)
	}
	connection, err := s.connection(ctx)
	if errors.Is(err, ErrAdminNotConfigured) {
		return emptyAdminStatus(), nil
	}
	if err != nil {
		return AdminStatusView{}, err
	}
	view := AdminStatusView{
		Connection: safeAdminConnection(connection),
		Agent:      safeAdminAgent(connection),
		Mode:       safeAdminMode(connection),
	}
	spec := adminInstallSpec(connection)
	if s.installer != nil {
		installation, statusErr := s.installer.Status(ctx, spec)
		view.Agent.DesiredReplicas = installation.DesiredReplicas
		view.Agent.ReadyReplicas = installation.ReadyReplicas
		view.Agent.ApplicationState = installationState(installation, statusErr)
	}
	if s.bridge != nil {
		if bridgeStatus, statusErr := s.bridge.AdminStatus(ctx); statusErr == nil {
			if connection.Active {
				if synced, syncErr := syncAgentStatus(ctx, s.queries, connection, bridgeStatus, s.now().UTC()); syncErr == nil {
					connection = synced
					view.Connection = safeAdminConnection(connection)
					view.Agent = safeAdminAgent(connection)
					view.Mode = safeAdminMode(connection)
					if s.installer != nil {
						installation, installErr := s.installer.Status(ctx, adminInstallSpec(connection))
						view.Agent.DesiredReplicas = installation.DesiredReplicas
						view.Agent.ReadyReplicas = installation.ReadyReplicas
						view.Agent.ApplicationState = installationState(installation, installErr)
					}
				}
			}
			applyBridgeStatus(&view, bridgeStatus)
		}
	}
	return view, nil
}

func emptyAdminStatus() AdminStatusView {
	return AdminStatusView{
		Connection: AdminConnectionView{},
		Agent:      AdminAgentView{ApplicationState: "not_installed", StandbyReplicas: []string{}, Replicas: []AdminAgentReplicaView{}},
		Mode:       AdminModeView{Requested: ModeDisabled, Authoritative: ModeDisabled, Effects: modeEffects(ModeDisabled)},
	}
}

func safeAdminConnection(row sqlc.CharlieConnection) AdminConnectionView {
	connected := row.OnboardingState == "consumed" || row.OnboardingState == "active"
	connected = connected && row.HealthState != "disconnected"
	return AdminConnectionView{
		Connected: connected, ProductID: row.ProductID, ProductSlug: row.ProductSlug, DeploymentID: row.DeploymentID, RouteID: row.RouteID,
		CentralVersion: row.CentralApiVersion, SigningKeyID: row.SigningKeyID,
		SigningFingerprint: row.SigningKeyFingerprint, PackageDigest: row.OnboardingPackageDigest,
		DisclosureDigest:       row.DisclosureDigest,
		DisclosureAcknowledged: row.DisclosureDigest != "" && row.AcknowledgedDisclosureDigest == row.DisclosureDigest,
		UpdatedAt:              row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func safeAdminAgent(row sqlc.CharlieConnection) AdminAgentView {
	replicas := make([]AdminAgentReplicaView, max(int(row.ReplicaCount), 0))
	for ordinal := range replicas {
		replicas[ordinal] = AdminAgentReplicaView{Ordinal: ordinal, Role: "unknown", State: "unknown"}
	}
	view := AdminAgentView{
		ApplicationState: row.HealthState, StandbyReplicas: []string{}, Replicas: replicas, FencingEpoch: row.FencingEpoch,
		LeaderReplica: row.LeaderInstanceID, AgentVersion: row.AgentProtocolVersion,
		ChartVersion: row.ChartVersion, ChartDigest: row.ChartDigest, ImageDigest: row.ImageDigest,
	}
	if row.LastConnectedAt.Valid {
		view.LastHeartbeatAt = row.LastConnectedAt.Time.UTC().Format(time.RFC3339)
	}
	return view
}

func safeAdminMode(row sqlc.CharlieConnection) AdminModeView {
	requested, verified := Mode(row.RequestedMode), Mode(row.VerifiedMode)
	if !validMode(requested) {
		requested = ModeDisabled
	}
	if !validMode(verified) {
		verified = ModeDisabled
	}
	return AdminModeView{
		Requested: requested, Authoritative: EffectiveMode(requested, verified, row.EmergencyDisabled),
		Revision: row.VerifiedModeRevision, EmergencyDisabled: row.EmergencyDisabled,
		DisablePending:   requested == ModeDisabled && verified != ModeDisabled,
		DisclosureDigest: row.DisclosureDigest, AcknowledgedDisclosureDigest: row.AcknowledgedDisclosureDigest,
		Effects: modeEffects(EffectiveMode(requested, verified, row.EmergencyDisabled)),
	}
}

func modeEffects(mode Mode) []string {
	switch mode {
	case ModeReadOnly:
		return []string{"Chat, retrieval, diagnostics, and findings are available", "No product write can execute"}
	case ModeApproval:
		return []string{"Read-only work is available", "Every product write requires exact live approval and target permission"}
	case ModeAuto:
		return []string{"Only explicitly allowlisted bounded actions may run", "Live RBAC, disclosure, epoch, and policy are rechecked for every action"}
	default:
		return []string{"New sessions, triggers, findings, approvals, actions, and MCP calls are disabled", "Configuration, diagnostics, and audit remain available"}
	}
}

func installationState(status AgentInstallationStatus, err error) string {
	if status.Ready() {
		return "ready"
	}
	if status.ApplicationSynced && status.ApplicationHealthy {
		return "degraded"
	}
	if status.DesiredReplicas > 0 || status.ReadyReplicas > 0 {
		return "installing"
	}
	if err != nil {
		return "unavailable"
	}
	return "inactive"
}

func applyBridgeStatus(view *AdminStatusView, status AdminBridgeStatus) {
	if status.ArtifactVersion != "" {
		view.Agent.AgentVersion = status.ArtifactVersion
	}
	view.Agent.LeaderReplica = status.LeaderInstanceID
	view.Agent.FencingEpoch = status.Epoch
	if status.InstanceID != "" && status.InstanceID != status.LeaderInstanceID {
		view.Agent.StandbyReplicas = []string{status.InstanceID}
	} else if status.ReplicaCount > 1 {
		view.Agent.StandbyReplicas = []string{"standby-reported"}
	}
	if status.ReplicaCount >= 2 && status.ReplicaCount <= 20 && int64(len(view.Agent.Replicas)) != status.ReplicaCount {
		view.Agent.Replicas = make([]AdminAgentReplicaView, status.ReplicaCount)
		for ordinal := range view.Agent.Replicas {
			view.Agent.Replicas[ordinal] = AdminAgentReplicaView{Ordinal: ordinal, Role: "unknown", State: "unknown"}
		}
	}
	if status.ReplicaOrdinal >= 0 && status.ReplicaOrdinal < int64(len(view.Agent.Replicas)) {
		replica := &view.Agent.Replicas[status.ReplicaOrdinal]
		replica.InstanceID = status.InstanceID
		replica.Role = "standby"
		if status.InstanceID != "" && status.InstanceID == status.LeaderInstanceID {
			replica.Role = "leader"
		}
		switch status.CentralHealth {
		case "healthy":
			replica.State = "ready"
		case "degraded":
			replica.State = "degraded"
		default:
			replica.State = "unavailable"
		}
		replica.Version = status.ArtifactVersion
	}
	if status.DisclosureDigest != "" {
		view.Mode.DisablePending = view.Mode.Requested == ModeDisabled && status.ProductEnabled
	}
}

func adminInstallSpec(row sqlc.CharlieConnection) AgentInstallSpec {
	return AgentInstallSpec{
		InstallationID: row.InstallationID, ConnectionID: row.ID, LogicalAgentID: row.LogicalAgentID,
		CentralURL: row.CentralUrl, ChartReference: row.ChartReference, ChartVersion: row.ChartVersion, ChartDigest: row.ChartDigest,
		ImageReference: row.ImageReference, ImageDigest: row.ImageDigest, SecretPrefix: row.AgentSecretName, DisclosureDigest: row.DisclosureDigest,
		SecretIntegrityHMAC: row.AgentSecretHmac, ReplicaCount: int(row.ReplicaCount),
	}
}

func (s *AdminService) Install(ctx context.Context) (AdminAgentView, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminAgentView{}, err
	}
	if connection.Active && connection.OnboardingState == "active" {
		if pruner, ok := s.installer.(supersededAgentMaterialPruner); ok {
			if err := pruner.PruneSupersededSecrets(ctx, adminInstallSpec(connection)); err != nil {
				return AdminAgentView{}, fmt.Errorf("%w: superseded Charlie material cleanup is incomplete", ErrAdminConflict)
			}
		}
		status, statusErr := s.Status(ctx)
		return status.Agent, statusErr
	}
	if connection.OnboardingState != "consumed" || s.installer == nil {
		return AdminAgentView{}, ErrAdminConflict
	}
	spec := adminInstallSpec(connection)
	if pruner, ok := s.installer.(supersededAgentMaterialPruner); ok {
		if err := pruner.PruneSupersededRepositories(ctx, spec); err != nil {
			return AdminAgentView{}, fmt.Errorf("%w: superseded Charlie repository cleanup is incomplete", ErrAdminConflict)
		}
	}
	installation, err := s.installer.Status(ctx, spec)
	if err != nil || !installation.Ready() {
		return AdminAgentView{}, fmt.Errorf("%w: Charlie agent readiness is incomplete", ErrAdminConflict)
	}
	if err := s.activateConnection(ctx, connection.ID); err != nil {
		return AdminAgentView{}, ErrAdminConflict
	}
	if pruner, ok := s.installer.(supersededAgentMaterialPruner); ok {
		if err := pruner.PruneSupersededSecrets(ctx, spec); err != nil {
			return AdminAgentView{}, fmt.Errorf("%w: superseded Charlie material cleanup is incomplete", ErrAdminConflict)
		}
	}
	status, err := s.Status(ctx)
	return status.Agent, err
}

func (s *AdminService) activateConnection(ctx context.Context, connectionID uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	locked, err := queries.LockCharlieConnectionActivation(ctx, connectionID)
	if err != nil {
		return err
	}
	found := false
	for _, id := range locked {
		if id == connectionID {
			found = true
			break
		}
	}
	if !found {
		return pgx.ErrNoRows
	}
	if err := queries.DeactivateCharlieConnectionsForReplacement(ctx, connectionID); err != nil {
		return err
	}
	if _, err := queries.ActivateCharlieConnection(ctx, sqlc.ActivateCharlieConnectionParams{ID: connectionID, HealthState: "ready"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *AdminService) ReplacementAction(context.Context, string) (AdminAgentView, error) {
	return AdminAgentView{}, ErrReplacementPackageNeeded
}

func (s *AdminService) Uninstall(ctx context.Context, actor uuid.UUID) error {
	connection, err := s.connection(ctx)
	if err != nil {
		return err
	}
	if s.installer == nil {
		return ErrAdminUnavailable
	}
	if actor == uuid.Nil {
		return ErrAdminConflict
	}
	if connection.HealthState == "inactive" {
		if err := s.installer.Uninstall(ctx, adminInstallSpec(connection)); err != nil {
			return fmt.Errorf("%w: uninstall cleanup was not confirmed", ErrAdminConflict)
		}
		return nil
	}
	if s.mode == nil {
		return ErrAdminUnavailable
	}
	if _, err := s.mode.EmergencyDisable(ctx, actor.String()); err != nil && !strings.Contains(err.Error(), "remote confirmation is pending") {
		return err
	}
	if err := s.installer.Uninstall(ctx, adminInstallSpec(connection)); err != nil {
		return fmt.Errorf("%w: uninstall prerequisites were not confirmed", ErrAdminConflict)
	}
	return nil
}

func (s *AdminService) Disconnect(ctx context.Context, actor uuid.UUID) error {
	connection, err := s.connection(ctx)
	if err != nil {
		return err
	}
	if connection.HealthState == "disconnected" {
		return nil
	}
	if s.installer == nil {
		return ErrAdminUnavailable
	}
	if actor == uuid.Nil {
		return ErrAdminConflict
	}
	confirmation := "disconnect:" + connection.InstallationID.String()
	if connection.HealthState == "inactive" {
		if err := s.installer.Disconnect(ctx, adminInstallSpec(connection), confirmation); err != nil {
			return fmt.Errorf("%w: disconnect cleanup was not confirmed", ErrAdminConflict)
		}
		return nil
	}
	if s.mode == nil {
		return ErrAdminUnavailable
	}
	if _, err := s.mode.EmergencyDisable(ctx, actor.String()); err != nil && !strings.Contains(err.Error(), "remote confirmation is pending") {
		return err
	}
	if err := s.installer.Disconnect(ctx, adminInstallSpec(connection), confirmation); err != nil {
		return fmt.Errorf("%w: disconnect prerequisites were not confirmed", ErrAdminConflict)
	}
	return nil
}

func (s *AdminService) UpdateMode(ctx context.Context, desired Mode, revision int64, emergency bool, actor uuid.UUID) (AdminModeView, error) {
	if s.mode == nil {
		return AdminModeView{}, ErrAdminUnavailable
	}
	var state ModeState
	var err error
	if emergency {
		state, err = s.mode.EmergencyDisable(ctx, actor.String())
	} else {
		prerequisites, prerequisitesErr := s.modePrerequisites(ctx)
		if prerequisitesErr != nil {
			return AdminModeView{}, prerequisitesErr
		}
		state, err = s.mode.Request(ctx, desired, revision, prerequisites)
	}
	remotePending := emergency && state.EmergencyDisabled && err != nil && strings.Contains(err.Error(), "remote confirmation is pending")
	if err != nil && !remotePending {
		return AdminModeView{}, fmt.Errorf("%w: %v", ErrAdminConflict, err)
	}
	connection, loadErr := s.connection(ctx)
	if loadErr != nil {
		return AdminModeView{}, loadErr
	}
	view := safeAdminMode(connection)
	if remotePending {
		view.DisablePending = true
	}
	return view, nil
}

func (s *AdminService) AcknowledgeDisclosure(ctx context.Context, digest string) (AdminModeView, error) {
	digest = strings.TrimSpace(digest)
	if len(digest) < 64 || len(digest) > 71 {
		return AdminModeView{}, ErrAdminConflict
	}
	result, err := s.pool.Exec(ctx, `UPDATE charlie_connections SET acknowledged_disclosure_digest=$1, updated_at=now() WHERE active=true AND disclosure_digest=$1`, digest)
	if err != nil || result.RowsAffected() != 1 {
		return AdminModeView{}, ErrAdminConflict
	}
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminModeView{}, err
	}
	return safeAdminMode(connection), nil
}

func (s *AdminService) modePrerequisites(ctx context.Context) (ModePrerequisites, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return ModePrerequisites{}, err
	}
	enabled, grants, err := s.automationState(ctx)
	if err != nil {
		return ModePrerequisites{}, err
	}
	return ModePrerequisites{
		DisclosureAcknowledged:   connection.DisclosureDigest != "" && connection.AcknowledgedDisclosureDigest == connection.DisclosureDigest,
		AutomationIdentityReady:  enabled,
		AutomationAllowlistReady: grants,
	}, nil
}

func (s *AdminService) Automation(ctx context.Context) (AdminAutomationView, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminAutomationView{}, err
	}
	rules, err := s.queries.ListCharlieTriggerRules(ctx, connection.ID)
	if err != nil {
		return AdminAutomationView{}, ErrAdminUnavailable
	}
	identityEnabled, _, err := s.automationState(ctx)
	if err != nil {
		return AdminAutomationView{}, err
	}
	items := make([]AdminTriggerRule, 0, len(rules))
	for _, rule := range rules {
		items = append(items, safeAdminTrigger(rule))
	}
	return AdminAutomationView{Rules: items, DefaultsRevision: 1, ServiceIdentityEnabled: identityEnabled}, nil
}

func (s *AdminService) ListTriggerEvents(ctx context.Context, state string, offset, limit int32) ([]AdminTriggerEventView, error) {
	if s == nil || s.triggers == nil {
		return nil, ErrAdminUnavailable
	}
	return s.triggers.List(ctx, state, offset, limit)
}

func (s *AdminService) RetryTriggerEvent(ctx context.Context, eventID, requestID uuid.UUID) (AdminTriggerEventView, error) {
	if s == nil || s.triggers == nil {
		return AdminTriggerEventView{}, ErrAdminUnavailable
	}
	return s.triggers.Retry(ctx, eventID, requestID)
}

type triggerSelectors struct {
	Scopes              []string `json:"scopes,omitempty"`
	MinimumAgentVersion string   `json:"minimum_agent_version,omitempty"`
	Suppressed          bool     `json:"suppressed,omitempty"`
}

func safeAdminTrigger(rule sqlc.CharlieTriggerRule) AdminTriggerRule {
	var selectors triggerSelectors
	var thresholds map[string]any
	_ = json.Unmarshal(rule.Selectors, &selectors)
	_ = json.Unmarshal(rule.Thresholds, &thresholds)
	integer := func(key string, fallback int32) int32 {
		value, ok := thresholds[key].(float64)
		if !ok {
			return fallback
		}
		return int32(value)
	}
	boolean := func(key string, fallback bool) bool {
		value, ok := thresholds[key].(bool)
		if !ok {
			return fallback
		}
		return value
	}
	return AdminTriggerRule{
		ID: rule.ID.String(), Name: rule.Name, Enabled: rule.Enabled, SourceType: rule.RuleType,
		Severities: []string{rule.MinimumSeverity}, Scopes: selectors.Scopes, CooldownSeconds: rule.CooldownSeconds,
		GracePeriodSeconds: integer("grace_period_seconds", rule.WindowSeconds), FlapWindowSeconds: integer("flap_window_seconds", rule.WindowSeconds),
		FlapCount: integer("flap_count", integer("count", 1)), FleetThresholdPercent: integer("fleet_threshold_percent", 0),
		MinimumAgentVersion: selectors.MinimumAgentVersion, Suppressed: selectors.Suppressed,
		MaximumAttempts: integer("maximum_attempts", MaxTriggerDispatchAttempts), DeadLetterEnabled: boolean("dead_letter_enabled", true),
		ServiceIdentity: AutomationUsername,
	}
}

func (s *AdminService) UpdateTrigger(ctx context.Context, id uuid.UUID, input AdminTriggerRule) (AdminTriggerRule, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminTriggerRule{}, err
	}
	existing, err := s.queries.GetCharlieTriggerRule(ctx, id)
	if err != nil || existing.ConnectionID != connection.ID {
		return AdminTriggerRule{}, ErrAdminConflict
	}
	if input.ID != "" && input.ID != id.String() {
		return AdminTriggerRule{}, ErrAdminConflict
	}
	if err := validateAdminTrigger(input); err != nil {
		return AdminTriggerRule{}, err
	}
	selectors, _ := json.Marshal(triggerSelectors{Scopes: normalizedScopes(input.Scopes), MinimumAgentVersion: strings.TrimSpace(input.MinimumAgentVersion), Suppressed: input.Suppressed})
	thresholds, _ := json.Marshal(map[string]any{
		"count": input.FlapCount, "grace_period_seconds": input.GracePeriodSeconds,
		"flap_window_seconds": input.FlapWindowSeconds, "flap_count": input.FlapCount,
		"fleet_threshold_percent": input.FleetThresholdPercent, "maximum_attempts": input.MaximumAttempts,
		"dead_letter_enabled": input.DeadLetterEnabled,
	})
	severity := input.Severities[0]
	updated, err := s.queries.UpdateCharlieTriggerRule(ctx, sqlc.UpdateCharlieTriggerRuleParams{
		Name: strings.TrimSpace(input.Name), RuleType: strings.TrimSpace(input.SourceType), Category: existing.Category,
		Enabled: input.Enabled && !input.Suppressed, MinimumSeverity: severity, Selectors: selectors, Thresholds: thresholds,
		WindowSeconds: max32(input.GracePeriodSeconds, 1), CooldownSeconds: input.CooldownSeconds,
		ServiceIdentityID: existing.ServiceIdentityID, ModeCeiling: existing.ModeCeiling, ID: id, ConnectionID: connection.ID,
	})
	if err != nil {
		return AdminTriggerRule{}, ErrAdminConflict
	}
	return safeAdminTrigger(updated), nil
}

func (s *AdminService) CreateTrigger(ctx context.Context, actor uuid.UUID, input AdminTriggerRule) (AdminTriggerRule, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminTriggerRule{}, err
	}
	if err := validateAdminTrigger(input); err != nil {
		return AdminTriggerRule{}, err
	}
	identity, err := s.queries.GetUserByUsername(ctx, AutomationUsername)
	if err != nil || !identity.IsActive || !identity.IsService {
		return AdminTriggerRule{}, ErrAdminUnavailable
	}
	selectors, _ := json.Marshal(triggerSelectors{Scopes: normalizedScopes(input.Scopes), MinimumAgentVersion: strings.TrimSpace(input.MinimumAgentVersion), Suppressed: input.Suppressed})
	thresholds, _ := json.Marshal(map[string]any{
		"count": input.FlapCount, "grace_period_seconds": input.GracePeriodSeconds,
		"flap_window_seconds": input.FlapWindowSeconds, "flap_count": input.FlapCount,
		"fleet_threshold_percent": input.FleetThresholdPercent, "maximum_attempts": input.MaximumAttempts,
		"dead_letter_enabled": input.DeadLetterEnabled,
	})
	created, err := s.queries.CreateCharlieTriggerRule(ctx, sqlc.CreateCharlieTriggerRuleParams{
		ConnectionID: connection.ID, Name: strings.TrimSpace(input.Name), RuleType: strings.TrimSpace(input.SourceType),
		Category: strings.TrimSpace(input.SourceType), Enabled: input.Enabled && !input.Suppressed,
		MinimumSeverity: input.Severities[0], Selectors: selectors, Thresholds: thresholds,
		WindowSeconds: max32(input.GracePeriodSeconds, 1), CooldownSeconds: input.CooldownSeconds,
		ServiceIdentityID: identity.ID, ModeCeiling: string(ModeReadOnly), CreatedByID: uuidToPG(actor),
	})
	if err != nil {
		return AdminTriggerRule{}, ErrAdminConflict
	}
	return safeAdminTrigger(created), nil
}

func (s *AdminService) DeleteTrigger(ctx context.Context, id uuid.UUID) error {
	connection, err := s.connection(ctx)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM charlie_trigger_rules WHERE id=$1 AND connection_id=$2`, id, connection.ID)
	if err != nil || result.RowsAffected() != 1 {
		return ErrAdminConflict
	}
	return nil
}

func validateAdminTrigger(input AdminTriggerRule) error {
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 128 || strings.TrimSpace(input.SourceType) == "" || len(input.SourceType) > 64 {
		return fmt.Errorf("%w: trigger name or source is invalid", ErrAdminConflict)
	}
	if len(input.Severities) == 0 || len(input.Severities) > 3 {
		return fmt.Errorf("%w: trigger severity is invalid", ErrAdminConflict)
	}
	for _, severity := range input.Severities {
		if severity != "info" && severity != "warning" && severity != "critical" {
			return fmt.Errorf("%w: trigger severity is invalid", ErrAdminConflict)
		}
	}
	if input.CooldownSeconds < 0 || input.CooldownSeconds > 604800 || input.GracePeriodSeconds < 1 || input.GracePeriodSeconds > 86400 ||
		input.FlapWindowSeconds < 1 || input.FlapWindowSeconds > 86400 || input.FlapCount < 1 || input.FlapCount > 100 ||
		input.FleetThresholdPercent < 0 || input.FleetThresholdPercent > 100 || input.MaximumAttempts < 1 || input.MaximumAttempts > 20 || len(input.Scopes) > 32 {
		return fmt.Errorf("%w: trigger bounds are invalid", ErrAdminConflict)
	}
	for _, scope := range input.Scopes {
		if strings.TrimSpace(scope) == "" || len(scope) > 255 {
			return fmt.Errorf("%w: trigger scope is invalid", ErrAdminConflict)
		}
	}
	if input.ServiceIdentity != "" && input.ServiceIdentity != AutomationUsername {
		return fmt.Errorf("%w: trigger service identity is invalid", ErrAdminConflict)
	}
	return nil
}

func normalizedScopes(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func max32(left, right int32) int32 {
	if left > right {
		return left
	}
	return right
}

func (s *AdminService) SetAutomationIdentity(ctx context.Context, enabled bool) (AdminAccessView, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminAccessView{}, ErrAdminUnavailable
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	user, err := queries.GetUserByUsername(ctx, AutomationUsername)
	if err != nil {
		return AdminAccessView{}, ErrAdminUnavailable
	}
	role, err := queries.GetCharlieAutomationRole(ctx)
	if err != nil || validateAutomationRole(role) != nil {
		return AdminAccessView{}, ErrAdminUnavailable
	}
	if enabled {
		if _, err := queries.EnsureCharlieAutomationBinding(ctx, sqlc.EnsureCharlieAutomationBindingParams{UserID: uuidToPG(user.ID), RoleID: role.ID}); err != nil {
			return AdminAccessView{}, ErrAdminUnavailable
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM global_role_bindings WHERE user_id=$1 AND role_id=$2`, user.ID, role.ID); err != nil {
			return AdminAccessView{}, ErrAdminUnavailable
		}
		if _, err := tx.Exec(ctx, `UPDATE charlie_trigger_rules SET enabled=false, updated_at=now() WHERE service_identity_id=$1`, user.ID); err != nil {
			return AdminAccessView{}, ErrAdminUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminAccessView{}, ErrAdminConflict
	}
	return s.Access(ctx)
}

func uuidToPG(id uuid.UUID) (value pgtype.UUID) { value.Bytes, value.Valid = id, true; return }

func (s *AdminService) automationState(ctx context.Context) (enabled, hasTargetGrants bool, err error) {
	row := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users u JOIN global_roles r ON r.name='Charlie Automation' AND r.is_builtin=true
			JOIN global_role_bindings b ON b.user_id=u.id AND b.role_id=r.id
			WHERE u.username=$1 AND u.is_active=true AND u.is_service=true
		), EXISTS(
			SELECT 1 FROM users u
			LEFT JOIN global_role_bindings gb ON gb.user_id=u.id
			LEFT JOIN global_roles gr ON gr.id=gb.role_id
			LEFT JOIN cluster_role_bindings cb ON cb.user_id=u.id
			LEFT JOIN cluster_roles cr ON cr.id=cb.role_id
			LEFT JOIN project_role_bindings pb ON pb.user_id=u.id
			LEFT JOIN project_roles pr ON pr.id=pb.role_id
			WHERE u.username=$1 AND (
				COALESCE(gr.rules,'[]'::jsonb) @> '[{"resource":"management"}]'::jsonb OR
				COALESCE(cr.rules,'[]'::jsonb) <> '[]'::jsonb OR COALESCE(pr.rules,'[]'::jsonb) <> '[]'::jsonb)
		)`, AutomationUsername)
	if scanErr := row.Scan(&enabled, &hasTargetGrants); scanErr != nil {
		return false, false, ErrAdminUnavailable
	}
	return enabled, hasTargetGrants, nil
}

func (s *AdminService) Access(ctx context.Context) (AdminAccessView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role_name, rules, scope, source FROM (
			SELECT gr.name role_name, gr.rules, 'global'::text scope, 'global_role'::text source
			FROM users u JOIN global_role_bindings b ON b.user_id=u.id JOIN global_roles gr ON gr.id=b.role_id WHERE u.username=$1
			UNION ALL
			SELECT cr.name, cr.rules, 'cluster:'||b.cluster_id::text, 'cluster_role' FROM users u JOIN cluster_role_bindings b ON b.user_id=u.id JOIN cluster_roles cr ON cr.id=b.role_id WHERE u.username=$1
			UNION ALL
			SELECT pr.name, pr.rules, 'project:'||b.project_id::text, 'project_role' FROM users u JOIN project_role_bindings b ON b.user_id=u.id JOIN project_roles pr ON pr.id=b.role_id WHERE u.username=$1
		) grants ORDER BY scope, role_name`, AutomationUsername)
	if err != nil {
		return AdminAccessView{}, ErrAdminUnavailable
	}
	defer rows.Close()
	grants := []AdminPermission{}
	for rows.Next() {
		var role, scope, source string
		var raw []byte
		if err := rows.Scan(&role, &raw, &scope, &source); err != nil {
			return AdminAccessView{}, ErrAdminUnavailable
		}
		var rules []struct {
			Resource string   `json:"resource"`
			Verbs    []string `json:"verbs"`
		}
		if json.Unmarshal(raw, &rules) != nil {
			continue
		}
		for _, rule := range rules {
			for _, verb := range rule.Verbs {
				grants = append(grants, AdminPermission{Permission: rule.Resource + ":" + verb, Scope: scope, Source: source + ":" + role})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return AdminAccessView{}, ErrAdminUnavailable
	}
	effective := append([]AdminPermission(nil), grants...)
	return AdminAccessView{EffectivePermissions: effective, AutomationGrants: grants}, nil
}

func (s *AdminService) Diagnostics(ctx context.Context, correlationID string) (AdminDiagnosticsView, error) {
	now := s.now().UTC()
	checked := now.Format(time.RFC3339)
	checks := []AdminDiagnosticCheck{}
	add := func(id, label, state, summary string) {
		checks = append(checks, AdminDiagnosticCheck{ID: id, Label: label, State: state, Summary: summary, CheckedAt: checked})
	}
	connection, connectionErr := s.connection(ctx)
	if connectionErr != nil {
		add("local_config", "Local database and configuration", "unavailable", "Charlie configuration is not available.")
		for _, item := range [][2]string{{"product_bridge_mtls", "Product Bridge mTLS"}, {"agent_primary", "Agent primary replica"}, {"agent_standby", "Agent standby replica"}, {"central_via_agent", "Central through agent"}, {"leader_epoch", "Leader and fencing epoch"}, {"route_rag", "Route and RAG readiness"}, {"mcp_tls_discovery", "MCP TLS and discovery digest"}, {"oci_artifacts", "OCI chart and image"}, {"credential_expiry", "Certificate and credential expiry"}} {
			add(item[0], item[1], "unknown", "Check was not run because Charlie is not configured.")
		}
		return AdminDiagnosticsView{Overall: "unavailable", Checks: checks, CorrelationID: correlationID}, nil
	}
	add("local_config", "Local database and configuration", "healthy", "Local Charlie metadata is readable and contains no runtime credentials in this response.")
	bridgeStatus, bridgeErr := AdminBridgeStatus{}, ErrAdminUnavailable
	if s.bridge != nil {
		bridgeStatus, bridgeErr = s.bridge.AdminStatus(ctx)
	}
	if bridgeErr != nil {
		add("product_bridge_mtls", "Product Bridge mTLS", "unavailable", "The fixed local mTLS bridge could not be reached.")
	} else {
		add("product_bridge_mtls", "Product Bridge mTLS", "healthy", "The product-local bridge completed mutual TLS and returned bounded status.")
	}
	installation := AgentInstallationStatus{}
	installErr := ErrAdminUnavailable
	if s.installer != nil {
		installation, installErr = s.installer.Status(ctx, adminInstallSpec(connection))
	}
	primaryState := "healthy"
	if installation.ReadyReplicas < 1 {
		primaryState = "unavailable"
	}
	add("agent_primary", "Agent primary replica", primaryState, fmt.Sprintf("%d of %d replicas are ready.", installation.ReadyReplicas, installation.DesiredReplicas))
	standbyState := "healthy"
	if installation.ReadyReplicas < 2 {
		standbyState = "degraded"
	}
	add("agent_standby", "Agent standby replica", standbyState, fmt.Sprintf("%d ready replicas are visible locally.", installation.ReadyReplicas))
	centralState := "unavailable"
	if bridgeErr == nil && bridgeStatus.CentralHealth == "healthy" {
		centralState = "healthy"
	} else if bridgeErr == nil {
		centralState = "degraded"
	}
	add("central_via_agent", "Central through agent", centralState, "Central health is reported only through the product-local agent.")
	leaderState := "degraded"
	if bridgeErr == nil && bridgeStatus.Epoch > 0 && bridgeStatus.LeaderInstanceID != "" {
		leaderState = "healthy"
	}
	add("leader_epoch", "Leader and fencing epoch", leaderState, "Leader identity and epoch are reported by the local bridge.")
	routeState := "unknown"
	routeSummary := "The current bridge contract does not independently attest RAG readiness."
	if bridgeErr == nil && bridgeStatus.RouteID == connection.RouteID && bridgeStatus.CentralHealth == "healthy" {
		routeState = "healthy"
		routeSummary = "The configured route matches the agent report; central route health is available."
	}
	add("route_rag", "Route and RAG readiness", routeState, routeSummary)
	digestState := "degraded"
	if bridgeErr == nil && normalizeDigest(bridgeStatus.DisclosureDigest) == normalizeDigest(connection.DisclosureDigest) {
		digestState = "healthy"
	}
	add("mcp_tls_discovery", "MCP TLS and discovery digest", digestState, "The agent disclosure digest is compared with Astronomer's current MCP catalog.")
	artifactState := "unavailable"
	if installErr == nil && installation.ArtifactsVerified {
		artifactState = "healthy"
	} else if installation.ApplicationSynced {
		artifactState = "degraded"
	}
	add("oci_artifacts", "OCI chart and image", artifactState, "Argo application and immutable artifact status are checked locally and through the agent.")
	credentialState := "unknown"
	credentialSummary := "Credential expiry is not exposed by the current agent status contract."
	if connection.LastRotatedAt.Valid {
		expiry := connection.LastRotatedAt.Time.Add(90 * 24 * time.Hour)
		credentialSummary = "Last local rotation implies expiry no later than " + expiry.UTC().Format(time.RFC3339) + "."
		if expiry.After(now.Add(7 * 24 * time.Hour)) {
			credentialState = "healthy"
		} else {
			credentialState = "degraded"
		}
	}
	add("credential_expiry", "Certificate and credential expiry", credentialState, credentialSummary)
	overall := "healthy"
	for _, check := range checks {
		if check.State == "unavailable" {
			overall = "unavailable"
			break
		}
		if check.State == "degraded" || check.State == "unknown" {
			overall = "degraded"
		}
	}
	return AdminDiagnosticsView{Overall: overall, Checks: checks, CorrelationID: correlationID}, nil
}

func normalizeDigest(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
}

// PGModeStore uses revision-checked updates and makes emergency disable atomic
// with cancellation/fencing of all outstanding Charlie work.
type PGModeStore struct{ Pool *pgxpool.Pool }

func (p PGModeStore) LoadModeState(ctx context.Context) (ModeState, error) {
	row, err := sqlc.New(p.Pool).GetActiveCharlieConnection(ctx)
	if err != nil {
		return ModeState{}, err
	}
	return dbModeState(row), nil
}
func (p PGModeStore) SetRequestedMode(ctx context.Context, connectionID string, mode Mode, expected int64) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET requested_mode=$1, verified_mode_revision=verified_mode_revision+1, updated_at=now() WHERE id=$2 AND active=true AND emergency_disabled=false AND verified_mode_revision=$3`, string(mode), id, expected)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	row, err := sqlc.New(p.Pool).GetCharlieConnection(ctx, id)
	return dbModeState(row), err
}
func (p PGModeStore) SetVerifiedMode(ctx context.Context, connectionID string, mode Mode, expected, next int64, digest string) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET verified_mode=$1, verified_mode_revision=$2, disclosure_digest=$3::VARCHAR(128), acknowledged_disclosure_digest=CASE WHEN disclosure_digest=$3::VARCHAR(128) THEN acknowledged_disclosure_digest ELSE '' END, last_verified_at=now(), updated_at=now() WHERE id=$4 AND active=true AND emergency_disabled=false AND verified_mode_revision=$5`, string(mode), next, digest, id, expected)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	row, err := sqlc.New(p.Pool).GetCharlieConnection(ctx, id)
	return dbModeState(row), err
}
func (p PGModeStore) SetEmergencyDisabled(ctx context.Context, connectionID, actorID string) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return ModeState{}, err
	}
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ModeState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE charlie_connections SET emergency_disabled=true, emergency_disabled_by_id=$1, emergency_disabled_at=now(), requested_mode='disabled', verified_mode='disabled', verified_mode_revision=verified_mode_revision+1, updated_at=now() WHERE id=$2 AND active=true`, actor, id)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	for _, statement := range []string{
		`UPDATE charlie_sessions SET state='aborted', completed_at=now(), updated_at=now() WHERE connection_id=$1 AND state IN ('creating','active','waiting_approval')`,
		`UPDATE charlie_action_approvals SET state='rejected', updated_at=now() WHERE connection_id=$1 AND state='approved'`,
		`UPDATE charlie_action_receipts SET state='fenced', updated_at=now() WHERE connection_id=$1 AND state IN ('claimed','waiting_approval','dispatched','ambiguous','verifying')`,
		`UPDATE charlie_trigger_events SET state='suppressed', updated_at=now() WHERE state IN ('pending','retry','dispatching') AND rule_id IN (SELECT id FROM charlie_trigger_rules WHERE connection_id=$1)`,
		`UPDATE charlie_delegations SET revoked_at=now() WHERE revoked_at IS NULL AND session_id IN (SELECT id FROM charlie_sessions WHERE connection_id=$1)`,
	} {
		if _, err := tx.Exec(ctx, statement, id); err != nil {
			return ModeState{}, err
		}
	}
	row, err := sqlc.New(tx).GetCharlieConnection(ctx, id)
	if err != nil {
		return ModeState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModeState{}, err
	}
	return dbModeState(row), nil
}
func (p PGModeStore) ClearEmergencyDisabled(ctx context.Context, connectionID, actorID string) (ModeState, error) {
	id, err := uuid.Parse(connectionID)
	if err != nil {
		return ModeState{}, err
	}
	result, err := p.Pool.Exec(ctx, `UPDATE charlie_connections SET emergency_disabled=false, emergency_disabled_by_id=NULL, emergency_disabled_at=NULL, requested_mode='disabled', verified_mode='disabled', verified_mode_revision=verified_mode_revision+1, updated_at=now() WHERE id=$1 AND active=true AND emergency_disabled=true AND verified_mode='disabled'`, id)
	if err != nil || result.RowsAffected() != 1 {
		return ModeState{}, ErrAdminConflict
	}
	row, err := sqlc.New(p.Pool).GetCharlieConnection(ctx, id)
	return dbModeState(row), err
}
func dbModeState(row sqlc.CharlieConnection) ModeState {
	return ModeState{ConnectionID: row.ID.String(), Active: row.Active, EmergencyDisabled: row.EmergencyDisabled, Requested: Mode(row.RequestedMode), Verified: Mode(row.VerifiedMode), Revision: row.VerifiedModeRevision, DisclosureDigest: row.DisclosureDigest, UpdatedAt: row.UpdatedAt}
}
