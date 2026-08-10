package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
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
	Requested                    Mode               `json:"requested"`
	Authoritative                Mode               `json:"authoritative"`
	Revision                     int64              `json:"revision"`
	EmergencyDisabled            bool               `json:"emergency_disabled"`
	DisablePending               bool               `json:"disable_pending,omitempty"`
	DisclosureDigest             string             `json:"disclosure_digest,omitempty"`
	AcknowledgedDisclosureDigest string             `json:"acknowledged_disclosure_digest,omitempty"`
	Effects                      []string           `json:"effects"`
	AutoReadiness                AdminAutoReadiness `json:"auto_readiness"`
	WorkloadCeiling              Mode               `json:"workload_ceiling"`
	WorkloadCeilingReady         bool               `json:"workload_ceiling_ready"`
}

type AdminAutoReadiness struct {
	Ready    bool                        `json:"ready"`
	Blockers []AdminAutoReadinessBlocker `json:"blockers"`
}

type AdminAutoReadinessBlocker struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	NextAction string `json:"next_action"`
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
	Rules                  []AdminTriggerRule  `json:"rules"`
	ActionPolicies         []AdminActionPolicy `json:"action_policies"`
	DefaultsRevision       int64               `json:"defaults_revision"`
	ServiceIdentityEnabled bool                `json:"service_identity_enabled"`
}

// AdminActionPolicy is the safe, operator-facing intersection of Charlie's
// central capability allowlist and Astronomer's durable local safety budget.
// It deliberately omits target selectors, arguments, credentials, and
// authority references.
type AdminActionPolicy struct {
	Capability            string   `json:"capability"`
	Effect                string   `json:"effect"`
	Risk                  string   `json:"risk"`
	AutoEligible          bool     `json:"auto_eligible"`
	CentralAllowlisted    bool     `json:"central_allowlisted"`
	CentralState          string   `json:"central_state"`
	Enabled               bool     `json:"enabled"`
	Revision              int64    `json:"revision"`
	MaxActionsPerIncident int32    `json:"max_actions_per_incident"`
	MaxActionsPerWindow   int32    `json:"max_actions_per_window"`
	BudgetWindowSeconds   int32    `json:"budget_window_seconds"`
	CooldownSeconds       int32    `json:"cooldown_seconds"`
	ScopeSummary          string   `json:"scope_summary"`
	Preconditions         []string `json:"preconditions"`
	Verification          string   `json:"verification"`
	CircuitState          string   `json:"circuit_state"`
}

// AdminActionPolicyInput is intentionally limited to product-local policy.
// The central allowlist is reviewed and changed in Charlie, independently.
// openapi:request CharlieAdminActionPolicyInput
type AdminActionPolicyInput struct {
	Enabled               bool  `json:"enabled"`
	MaxActionsPerIncident int32 `json:"max_actions_per_incident"`
	MaxActionsPerWindow   int32 `json:"max_actions_per_window"`
	BudgetWindowSeconds   int32 `json:"budget_window_seconds"`
	CooldownSeconds       int32 `json:"cooldown_seconds"`
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
	ID         string `json:"id"`
	Label      string `json:"label"`
	State      string `json:"state"`
	Summary    string `json:"summary"`
	NextAction string `json:"next_action"`
	CheckedAt  string `json:"checked_at,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

type AdminDiagnosticsView struct {
	Overall       string                 `json:"overall"`
	Checks        []AdminDiagnosticCheck `json:"checks"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
}

const MaxAdminAlertDeliveryProofs = 32

// AdminAlertDeliveryProof is deliberately metadata-only. Subject, body,
// destination, channel identity, dedupe bucket, and provider error text never
// cross the administrator API boundary.
type AdminAlertDeliveryProof struct {
	DeliveryID       string     `json:"delivery_id"`
	FindingID        string     `json:"finding_id"`
	DeliveryKind     string     `json:"delivery_kind"`
	Status           string     `json:"status"`
	TemplateIdentity string     `json:"template_identity"`
	DeepLinkValid    bool       `json:"deep_link_valid"`
	ContentFree      bool       `json:"content_free"`
	AttemptCount     int32      `json:"attempt_count"`
	MaximumAttempts  int32      `json:"maximum_attempts"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
}

type AdminAlertDeliveryProofView struct {
	FindingID            string                    `json:"finding_id"`
	FindingBlockCode     string                    `json:"finding_block_code"`
	FindingWorkflowState string                    `json:"finding_workflow_state"`
	DeliveryCount        int                       `json:"delivery_count"`
	DedupeValid          bool                      `json:"dedupe_valid"`
	Deliveries           []AdminAlertDeliveryProof `json:"deliveries"`
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
	auditor   AuthorityMutationAuditor
}

func NewAdminService(pool *pgxpool.Pool, installer AdminAgentInstaller, bridge *ManagedBridge) (*AdminService, error) {
	if pool == nil {
		return nil, ErrAdminUnavailable
	}
	queries := sqlc.New(pool)
	auditor := NewDBLifecycleAuditor(queries)
	triggerAdmin, err := NewTriggerAdminService(queries, auditor)
	if err != nil {
		return nil, err
	}
	service := &AdminService{pool: pool, queries: queries, installer: installer, bridge: bridge, now: time.Now, triggers: triggerAdmin, auditor: auditor}
	if bridge != nil {
		controller, err := NewModeController(PGModeStore{Pool: pool}, NewManagedModeBridge(bridge), auditor)
		if err != nil {
			return nil, err
		}
		if ceilingInstaller, ok := installer.(ModeCeilingInstaller); ok {
			controller.SetModeCeilingRollout(modeCeilingRollout{load: queries.GetLatestCharlieConnection, installer: ceilingInstaller})
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

func (s *AdminService) AlertDeliveryProofs(ctx context.Context, findingID uuid.UUID) (AdminAlertDeliveryProofView, error) {
	if findingID == uuid.Nil || s == nil || s.queries == nil {
		return AdminAlertDeliveryProofView{}, ErrAdminUnavailable
	}
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminAlertDeliveryProofView{}, err
	}
	matches, err := s.queries.CharlieFindingMatchesConnectionIdentity(ctx, sqlc.CharlieFindingMatchesConnectionIdentityParams{ConnectionID: connection.ID, FindingID: findingID})
	if err != nil || !matches {
		return AdminAlertDeliveryProofView{}, ErrAdminNotConfigured
	}
	finding, err := s.queries.GetCharlieFinding(ctx, findingID)
	if err != nil {
		return AdminAlertDeliveryProofView{}, ErrAdminNotConfigured
	}
	rows, err := s.queries.ListCharlieAlertDeliveriesForFinding(ctx, sqlc.ListCharlieAlertDeliveriesForFindingParams{ConnectionID: connection.ID, FindingID: findingID, PageLimit: MaxAdminAlertDeliveryProofs + 1})
	if err != nil || len(rows) > MaxAdminAlertDeliveryProofs {
		return AdminAlertDeliveryProofView{}, ErrAdminUnavailable
	}
	view := AdminAlertDeliveryProofView{
		FindingID: findingID.String(), FindingBlockCode: finding.ExecutionBlockCode,
		FindingWorkflowState: finding.WorkflowState, DeliveryCount: len(rows), DedupeValid: true,
		Deliveries: make([]AdminAlertDeliveryProof, 0, len(rows)),
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		proof := safeAdminAlertDeliveryProof(row)
		view.Deliveries = append(view.Deliveries, proof)
		channel := "none"
		if row.NotificationChannelID.Valid {
			channel = uuid.UUID(row.NotificationChannelID.Bytes).String()
		}
		key := channel + ":" + row.DeliveryKind + ":" + fmt.Sprint(row.DedupeBucket)
		if _, duplicate := seen[key]; duplicate {
			view.DedupeValid = false
		}
		seen[key] = struct{}{}
	}
	return view, nil
}

func safeAdminAlertDeliveryProof(row sqlc.CharlieAlertDelivery) AdminAlertDeliveryProof {
	wantLink := "/dashboard/charlie?tab=findings&finding=" + row.FindingID.String()
	identity, subject := "unrecognized", ""
	switch row.DeliveryKind {
	case "initial":
		identity, subject = "charlie.finding.initial/v1", "Charlie finding requires attention"
	case "escalation":
		identity, subject = "charlie.finding.escalation/v1", "Charlie finding remains unresolved"
	}
	wantBody := "Review the durable finding in Astronomer. No approval or product action is implied by this notification. " + wantLink
	deepLinkValid := row.DeepLink == wantLink
	contentFree := identity != "unrecognized" && deepLinkValid && row.Subject == subject && row.Body == wantBody
	proof := AdminAlertDeliveryProof{
		DeliveryID: row.ID.String(), FindingID: row.FindingID.String(), DeliveryKind: row.DeliveryKind,
		Status: row.Status, TemplateIdentity: identity, DeepLinkValid: deepLinkValid, ContentFree: contentFree,
		AttemptCount: row.AttemptCount, MaximumAttempts: row.MaximumAttempts,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.DeliveredAt.Valid {
		delivered := row.DeliveredAt.Time.UTC()
		proof.DeliveredAt = &delivered
	}
	return proof
}

// Status returns a pure projection of durable connection state, live bridge
// heartbeats, and installer readiness. It does not run mode reconciliation —
// that remains on RunModeReconciler and post-mutation mode paths so admin GET
// stays free of authority side-effects.
func (s *AdminService) Status(ctx context.Context) (AdminStatusView, error) {
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
	// An inactive staged replacement and an emergency stop are transport
	// isolation boundaries, not just action-authorization bits. Do not probe the
	// product agent, central Charlie, or artifact registry in either state. Apart
	// from making the admin page responsive during an outage or upgrade, this
	// prevents a read-only status request from opening network activity before
	// activation or re-opening activity that the operator explicitly stopped.
	if connectionTransportQuiesced(connection) {
		return quiescedAdminStatus(connection), nil
	}
	if s.bridge != nil {
		if bridgeStatus, statusErr := s.bridge.AdminStatus(ctx); statusErr == nil {
			if connection.Active {
				if synced, syncErr := syncAgentStatus(ctx, s.queries, connection, bridgeStatus, s.now().UTC()); syncErr == nil {
					connection = synced
					view.Connection = safeAdminConnection(connection)
					view.Agent = safeAdminAgent(connection)
					view.Mode = safeAdminMode(connection)
				}
			}
			applyBridgeStatus(&view, bridgeStatus)
		}
	}
	s.applyInstallerStatus(ctx, &view, connection)
	view.Mode = s.enrichMode(ctx, view.Mode)
	return view, nil
}

func (s *AdminService) applyInstallerStatus(ctx context.Context, view *AdminStatusView, connection sqlc.CharlieConnection) {
	if s == nil || s.installer == nil || view == nil {
		return
	}
	installation, statusErr := s.installer.Status(ctx, adminInstallSpec(connection))
	view.Agent.DesiredReplicas = installation.DesiredReplicas
	view.Agent.ReadyReplicas = installation.ReadyReplicas
	view.Agent.ApplicationState = installationState(installation, statusErr)
	view.Mode.WorkloadCeiling = installation.ModeCeiling
	view.Mode.WorkloadCeilingReady = statusErr == nil && installation.ModeCeilingReady
}

// LocalStatus returns only the durable product-owned projection. It never
// reconciles central authority, contacts the product agent, or asks the
// Kubernetes installer for live state. The feature-disabled administration
// surface uses this method so a page refresh cannot reactivate transport.
func (s *AdminService) LocalStatus(ctx context.Context) (AdminStatusView, error) {
	connection, err := s.connection(ctx)
	if errors.Is(err, ErrAdminNotConfigured) {
		return emptyAdminStatus(), nil
	}
	if err != nil {
		return AdminStatusView{}, err
	}
	return quiescedAdminStatus(connection), nil
}

func quiescedAdminStatus(connection sqlc.CharlieConnection) AdminStatusView {
	view := AdminStatusView{
		Connection: safeAdminConnection(connection),
		Agent:      safeAdminAgent(connection),
		Mode:       safeAdminMode(connection),
	}
	// Persisted runtime state is historical while the feature is off. Report it
	// as quiesced rather than implying that the agent was polled or is running.
	view.Agent.ApplicationState = "inactive"
	view.Agent.ReadyReplicas = 0
	view.Agent.LastHeartbeatAt = ""
	view.Agent.LeaderReplica = ""
	view.Agent.StandbyReplicas = []string{}
	for index := range view.Agent.Replicas {
		view.Agent.Replicas[index].Role = "unknown"
		view.Agent.Replicas[index].State = "unknown"
		view.Agent.Replicas[index].LastHeartbeatAt = ""
	}
	view.Mode.Requested = ModeDisabled
	view.Mode.Authoritative = ModeDisabled
	view.Mode.WorkloadCeiling = ModeDisabled
	view.Mode.WorkloadCeilingReady = false
	view.Mode.Effects = modeEffects(ModeDisabled)
	return view
}

func connectionTransportQuiesced(connection sqlc.CharlieConnection) bool {
	return !connection.Active || connection.EmergencyDisabled
}

func emptyAdminStatus() AdminStatusView {
	return AdminStatusView{
		Connection: AdminConnectionView{},
		Agent:      AdminAgentView{ApplicationState: "not_installed", StandbyReplicas: []string{}, Replicas: []AdminAgentReplicaView{}},
		Mode: AdminModeView{Requested: ModeDisabled, Authoritative: ModeDisabled, WorkloadCeiling: ModeDisabled, Effects: modeEffects(ModeDisabled), AutoReadiness: AdminAutoReadiness{Blockers: []AdminAutoReadinessBlocker{{
			Code: "allowlist_unreviewed", Message: "Charlie is not connected or active.", NextAction: "Connect Charlie and review its MCP disclosure before configuring automation.",
		}}}},
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
		Effects: modeEffects(EffectiveMode(requested, verified, row.EmergencyDisabled)), AutoReadiness: AdminAutoReadiness{Blockers: []AdminAutoReadinessBlocker{}},
		WorkloadCeiling: requested,
	}
}

func (s *AdminService) enrichMode(ctx context.Context, view AdminModeView) AdminModeView {
	prerequisites, err := s.modePrerequisites(ctx)
	view.AutoReadiness = adminAutoReadiness(prerequisites, err)
	return view
}

func adminAutoReadiness(prerequisites ModePrerequisites, err error) AdminAutoReadiness {
	result := AdminAutoReadiness{Blockers: []AdminAutoReadinessBlocker{}}
	add := func(code, message, next string) {
		result.Blockers = append(result.Blockers, AdminAutoReadinessBlocker{Code: code, Message: message, NextAction: next})
	}
	if err != nil {
		add("allowlist_unreviewed", "Automation readiness could not be verified.", "Keep Charlie in read-only or approval mode and rerun diagnostics.")
		return result
	}
	if !prerequisites.DisclosureAcknowledged {
		add("disclosure_unacknowledged", "The current MCP capability disclosure is not acknowledged.", "Rediscover the MCP catalog and acknowledge its exact disclosure digest.")
	}
	if !prerequisites.AutomationIdentityReady {
		add("automation_identity_unconfigured", "The dedicated Charlie automation service identity is disabled.", "Enable the service identity only after reviewing its exact product permissions.")
	}
	if !prerequisites.AutomationAllowlistReady {
		add("allowlist_unreviewed", "Charlie has no centrally reviewed auto-eligible capability for this integration revision.", "Review and allowlist an exact capability in Charlie; an empty allowlist can never enable automation.")
	}
	if !prerequisites.AutomationTargetReady {
		add("target_grants_missing", "The automation identity has no exact global target permission from the auto-eligible catalog.", "Grant one reviewed resource and verb; wildcard and scoped grants do not qualify.")
	}
	result.Ready = len(result.Blockers) == 0
	return result
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
		InstallationID: row.InstallationID, ConnectionID: row.ID, LogicalAgentID: row.LogicalAgentID, DeploymentID: row.DeploymentID,
		OnboardingPackageID: row.OnboardingPackageID,
		CentralURL:          row.CentralUrl, ChartReference: row.ChartReference, ChartVersion: row.ChartVersion, ChartDigest: row.ChartDigest,
		ImageReference: row.ImageReference, ImageDigest: row.ImageDigest, SecretPrefix: row.AgentSecretName, DisclosureDigest: row.DisclosureDigest,
		SecretIntegrityHMAC: row.AgentSecretHmac, ReplicaCount: int(row.ReplicaCount),
		ModeCeiling: normalizedModeCeiling(Mode(row.RequestedMode)),
	}
}

func (s *AdminService) Install(ctx context.Context) (AdminAgentView, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminAgentView{}, err
	}
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.agent.install", ResourceType: "charlie_connection", ResourceID: connection.ID.String()}); err != nil {
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

func (s *AdminService) ReplacementAction(ctx context.Context, action string) (AdminAgentView, error) {
	if s == nil || s.pool == nil || s.queries == nil || s.installer == nil {
		return AdminAgentView{}, ErrAdminUnavailable
	}
	current, err := s.queries.GetActiveCharlieConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminAgentView{}, ErrReplacementPackageNeeded
	}
	if err != nil {
		return AdminAgentView{}, ErrAdminUnavailable
	}
	next, err := s.queries.GetLatestCharlieConnection(ctx)
	if err != nil {
		return AdminAgentView{}, ErrAdminUnavailable
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	if err := validateReplacementAction(current, next, action, now); err != nil {
		return AdminAgentView{}, err
	}
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.agent." + action, ResourceType: "charlie_connection", ResourceID: next.ID.String()}); err != nil {
		return AdminAgentView{}, err
	}
	nextSpec := adminInstallSpec(next)
	if pruner, ok := s.installer.(supersededAgentMaterialPruner); ok {
		if err := pruner.PruneSupersededRepositories(ctx, nextSpec); err != nil {
			return AdminAgentView{}, fmt.Errorf("%w: superseded Charlie repository cleanup is incomplete", ErrAdminConflict)
		}
	}
	installation, err := s.installer.Status(ctx, nextSpec)
	if err != nil || !installation.Ready() {
		return AdminAgentView{}, fmt.Errorf("%w: replacement Charlie agent readiness is incomplete", ErrAdminConflict)
	}
	if err := s.activateReplacement(ctx, current.ID, next.ID); err != nil {
		return AdminAgentView{}, fmt.Errorf("%w: replacement activation raced or changed", ErrAdminConflict)
	}
	if pruner, ok := s.installer.(supersededAgentMaterialPruner); ok {
		if err := pruner.PruneSupersededSecrets(ctx, nextSpec); err != nil {
			return AdminAgentView{}, fmt.Errorf("%w: superseded Charlie material cleanup is incomplete", ErrAdminConflict)
		}
	}
	status, err := s.Status(ctx)
	return status.Agent, err
}

func validateReplacementAction(current, next sqlc.CharlieConnection, action string, now time.Time) error {
	if !current.Active || current.OnboardingState != "active" || next.Active || next.OnboardingState != "consumed" ||
		current.ID == next.ID || !next.CreatedAt.After(current.CreatedAt) ||
		current.InstallationID != next.InstallationID || current.ProductID != next.ProductID ||
		current.ProductSlug != next.ProductSlug || current.DeploymentID != next.DeploymentID ||
		current.RouteID != next.RouteID || current.CentralUrl != next.CentralUrl ||
		current.CentralCaFingerprint != next.CentralCaFingerprint ||
		current.SigningKeyID != next.SigningKeyID || current.SigningKeyFingerprint != next.SigningKeyFingerprint ||
		current.LogicalAgentID != next.LogicalAgentID || current.ReplicaCount != next.ReplicaCount ||
		current.BridgeServiceName != next.BridgeServiceName || current.McpServiceName != next.McpServiceName ||
		current.OnboardingPackageID == next.OnboardingPackageID ||
		current.OnboardingPackageDigest == next.OnboardingPackageDigest || strings.TrimSpace(next.AgentSecretHmac) == "" ||
		!next.OnboardingPackageExpiresAt.After(now) || !next.EnrollmentCredentialsExpiresAt.After(now) ||
		!next.ArtifactCredentialExpiresAt.After(now) || !next.CertificateExpiresAt.After(now) {
		return ErrReplacementPackageNeeded
	}
	currentVersion, currentErr := semver.NewVersion(current.ChartVersion)
	nextVersion, nextErr := semver.NewVersion(next.ChartVersion)
	if currentErr != nil || nextErr != nil {
		return ErrReplacementPackageNeeded
	}
	sameArtifacts := current.ChartDigest == next.ChartDigest && current.ImageDigest == next.ImageDigest
	switch action {
	case "upgrade":
		if sameArtifacts || !nextVersion.GreaterThan(currentVersion) {
			return ErrReplacementPackageNeeded
		}
	case "rollback":
		if sameArtifacts || !nextVersion.LessThan(currentVersion) {
			return ErrReplacementPackageNeeded
		}
	case "rotate":
		if !sameArtifacts || !nextVersion.Equal(currentVersion) {
			return ErrReplacementPackageNeeded
		}
	default:
		return ErrAdminConflict
	}
	return nil
}

func (s *AdminService) activateReplacement(ctx context.Context, currentID, nextID uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	locked, err := queries.LockCharlieConnectionActivation(ctx, nextID)
	if err != nil {
		return err
	}
	lockedIDs := make(map[uuid.UUID]struct{}, len(locked))
	for _, id := range locked {
		lockedIDs[id] = struct{}{}
	}
	if _, ok := lockedIDs[currentID]; !ok {
		return pgx.ErrNoRows
	}
	if _, ok := lockedIDs[nextID]; !ok {
		return pgx.ErrNoRows
	}
	current, err := queries.GetActiveCharlieConnection(ctx)
	if err != nil || current.ID != currentID || current.OnboardingState != "active" {
		return pgx.ErrNoRows
	}
	latest, err := queries.GetLatestCharlieConnection(ctx)
	if err != nil || latest.ID != nextID || latest.OnboardingState != "consumed" || latest.Active {
		return pgx.ErrNoRows
	}
	if err := queries.DeactivateCharlieConnectionsForReplacement(ctx, nextID); err != nil {
		return err
	}
	if _, err := queries.ActivateCharlieConnection(ctx, sqlc.ActivateCharlieConnectionParams{ID: nextID, HealthState: "ready"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.agent.uninstall", ResourceType: "charlie_connection", ResourceID: connection.ID.String(), ActorID: actor}); err != nil {
		return err
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
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.disconnect", ResourceType: "charlie_connection", ResourceID: connection.ID.String(), ActorID: actor}); err != nil {
		return err
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
		connection, connectionErr := s.connection(ctx)
		if connectionErr != nil {
			return AdminModeView{}, connectionErr
		}
		if connection.EmergencyDisabled {
			if desired != ModeDisabled || revision != connection.VerifiedModeRevision {
				return AdminModeView{}, fmt.Errorf("%w: clear emergency disable with the exact disabled revision before requesting authority", ErrAdminConflict)
			}
			state, err = s.mode.ClearEmergencyDisable(ctx, actor.String())
		} else {
			prerequisites, prerequisitesErr := s.modePrerequisites(ctx)
			if prerequisitesErr != nil {
				return AdminModeView{}, prerequisitesErr
			}
			state, err = s.mode.Request(ctx, desired, revision, prerequisites)
		}
	}
	remotePending := emergency && state.EmergencyDisabled && err != nil && strings.Contains(err.Error(), "remote confirmation is pending")
	ceilingPending := emergency && state.EmergencyDisabled && err != nil && strings.Contains(err.Error(), "agent ceiling confirmation is pending")
	confirmationPending := remotePending || ceilingPending
	if err != nil && !confirmationPending {
		return AdminModeView{}, fmt.Errorf("%w: %v", ErrAdminConflict, err)
	}
	connection, loadErr := s.connection(ctx)
	if loadErr != nil {
		return AdminModeView{}, loadErr
	}
	view := safeAdminMode(connection)
	view = s.enrichMode(ctx, view)
	// A successful transition, including one awaiting only central confirmation,
	// has already proven the exact ceiling on both product-agent replicas.
	view.WorkloadCeilingReady = err == nil || remotePending
	if confirmationPending {
		view.DisablePending = true
	}
	return view, nil
}

func (s *AdminService) AcknowledgeDisclosure(ctx context.Context, digest string) (AdminModeView, error) {
	digest = strings.TrimSpace(digest)
	if len(digest) < 64 || len(digest) > 71 {
		return AdminModeView{}, ErrAdminConflict
	}
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminModeView{}, err
	}
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.disclosure.acknowledge", ResourceType: "charlie_connection", ResourceID: connection.ID.String()}); err != nil {
		return AdminModeView{}, err
	}
	result, err := s.pool.Exec(ctx, `UPDATE charlie_connections SET acknowledged_disclosure_digest=$1, updated_at=now() WHERE active=true AND disclosure_digest=$1`, digest)
	if err != nil || result.RowsAffected() != 1 {
		return AdminModeView{}, ErrAdminConflict
	}
	connection, err = s.connection(ctx)
	if err != nil {
		return AdminModeView{}, err
	}
	return s.enrichMode(ctx, safeAdminMode(connection)), nil
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
	prerequisites := ModePrerequisites{
		DisclosureAcknowledged:  connection.DisclosureDigest != "" && connection.AcknowledgedDisclosureDigest == connection.DisclosureDigest,
		AutomationIdentityReady: enabled,
		AutomationTargetReady:   grants,
	}
	if s.bridge == nil {
		return prerequisites, ErrAdminUnavailable
	}
	status, err := s.bridge.AdminStatus(ctx)
	if err != nil {
		return prerequisites, err
	}
	for _, capability := range status.AutoAllowlist {
		for _, descriptor := range WriteCapabilityCatalog() {
			if capability == descriptor.Name && descriptor.AutoEligible {
				prerequisites.AutomationAllowlistReady = true
				return prerequisites, nil
			}
		}
	}
	return prerequisites, nil
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
	policies, err := s.actionPolicies(ctx, connection.ID)
	if err != nil {
		return AdminAutomationView{}, err
	}
	return AdminAutomationView{Rules: items, ActionPolicies: policies, DefaultsRevision: 1, ServiceIdentityEnabled: identityEnabled}, nil
}

func (s *AdminService) actionPolicies(ctx context.Context, connectionID uuid.UUID) ([]AdminActionPolicy, error) {
	rows, err := s.queries.ListCharlieAutomationPolicies(ctx, connectionID)
	if err != nil {
		return nil, ErrAdminUnavailable
	}
	configured := make(map[string]sqlc.CharlieAutomationPolicy, len(rows))
	for _, row := range rows {
		configured[row.Capability] = row
	}
	central, centralState := map[string]struct{}{}, "unavailable"
	if s.bridge != nil {
		status, statusErr := s.bridge.AdminStatus(ctx)
		if statusErr == nil {
			centralState = "verified"
			for _, capability := range status.AutoAllowlist {
				central[capability] = struct{}{}
			}
		}
	}
	result := []AdminActionPolicy{}
	for _, descriptor := range WriteCapabilityCatalog() {
		if !descriptor.AutoEligible {
			continue
		}
		policy := AdminActionPolicy{
			Capability: descriptor.Name, Effect: string(descriptor.Effect), Risk: descriptor.Risk,
			AutoEligible: true, CentralState: centralState, MaxActionsPerIncident: 1, MaxActionsPerWindow: 1,
			BudgetWindowSeconds: 1800, CooldownSeconds: 1800,
			ScopeSummary:  "live RBAC plus the exact session resource ID",
			Preconditions: []string{"current disclosure and integration revision", "live product RBAC", "exact resource scope", "healthy safety budget and cooldown"},
			Verification:  "the product adapter must verify the bounded result before success is recorded",
			CircuitState:  "unknown",
		}
		_, policy.CentralAllowlisted = central[descriptor.Name]
		if row, ok := configured[descriptor.Name]; ok {
			policy.Enabled, policy.Revision = row.Enabled, row.Revision
			policy.MaxActionsPerIncident, policy.MaxActionsPerWindow = row.MaxActionsPerIncident, row.MaxActionsPerWindow
			policy.BudgetWindowSeconds, policy.CooldownSeconds = row.BudgetWindowSeconds, row.CooldownSeconds
		}
		result = append(result, policy)
	}
	return result, nil
}

func (s *AdminService) UpdateActionPolicy(ctx context.Context, capability string, input AdminActionPolicyInput) (AdminActionPolicy, error) {
	capability = strings.TrimSpace(capability)
	var descriptor *CapabilityDescriptor
	for _, candidate := range WriteCapabilityCatalog() {
		if candidate.Name == capability && candidate.AutoEligible {
			copy := candidate
			descriptor = &copy
			break
		}
	}
	if descriptor == nil || input.MaxActionsPerIncident < 1 || input.MaxActionsPerIncident > 100 ||
		input.MaxActionsPerWindow < 1 || input.MaxActionsPerWindow > 100 ||
		input.BudgetWindowSeconds < 60 || input.BudgetWindowSeconds > 86400 ||
		input.CooldownSeconds < 30 || input.CooldownSeconds > 604800 {
		return AdminActionPolicy{}, ErrAdminConflict
	}
	connection, err := s.connection(ctx)
	if err != nil {
		return AdminActionPolicy{}, err
	}
	if input.Enabled {
		if s.bridge == nil {
			return AdminActionPolicy{}, ErrAdminConflict
		}
		status, statusErr := s.bridge.AdminStatus(ctx)
		if statusErr != nil {
			return AdminActionPolicy{}, ErrAdminUnavailable
		}
		centrallyAllowed := false
		for _, allowed := range status.AutoAllowlist {
			if allowed == capability {
				centrallyAllowed = true
				break
			}
		}
		if !centrallyAllowed {
			return AdminActionPolicy{}, ErrAdminConflict
		}
	}
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.action_policy.update", ResourceType: "charlie_action_policy", ResourceID: digestBytes([]byte(capability))}); err != nil {
		return AdminActionPolicy{}, err
	}
	if _, err = s.queries.UpsertCharlieAutomationPolicy(ctx, sqlc.UpsertCharlieAutomationPolicyParams{
		ConnectionID: connection.ID, Capability: capability, Enabled: input.Enabled,
		MaxActionsPerIncident: input.MaxActionsPerIncident, MaxActionsPerWindow: input.MaxActionsPerWindow,
		BudgetWindowSeconds: input.BudgetWindowSeconds, CooldownSeconds: input.CooldownSeconds,
	}); err != nil {
		return AdminActionPolicy{}, ErrAdminUnavailable
	}
	policies, err := s.actionPolicies(ctx, connection.ID)
	if err != nil {
		return AdminActionPolicy{}, err
	}
	for _, policy := range policies {
		if policy.Capability == capability {
			return policy, nil
		}
	}
	return AdminActionPolicy{}, ErrAdminConflict
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
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{
		Action: "admin.charlie.trigger.update", ResourceType: "charlie_trigger_rule", ResourceID: id.String(),
		Fields: map[string]any{"enabled": input.Enabled, "suppressed": input.Suppressed},
	}); err != nil {
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
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{
		Action: "admin.charlie.trigger.create", ResourceType: "charlie_trigger_rule", ResourceID: digestBytes([]byte(strings.TrimSpace(input.Name))), ActorID: actor,
		Fields: map[string]any{"enabled": input.Enabled, "suppressed": input.Suppressed},
	}); err != nil {
		return AdminTriggerRule{}, err
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
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{Action: "admin.charlie.trigger.delete", ResourceType: "charlie_trigger_rule", ResourceID: id.String()}); err != nil {
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
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{
		Action: "admin.charlie.access.update", ResourceType: "charlie_automation_identity", ResourceID: "automation_identity", Fields: map[string]any{"enabled": enabled},
	}); err != nil {
		return AdminAccessView{}, err
	}
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

func (s *AdminService) requireAuthorityAudit(ctx context.Context, event AuthorityMutationAudit) error {
	if s == nil || requireAuthorityMutationAudit(ctx, s.auditor, event) != nil {
		return ErrAdminUnavailable
	}
	return nil
}

func (s *AdminService) automationState(ctx context.Context) (enabled, hasTargetGrants bool, err error) {
	row := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users u JOIN global_roles r ON r.name='Charlie Automation' AND r.is_builtin=true
			JOIN global_role_bindings b ON b.user_id=u.id AND b.role_id=r.id
			WHERE u.username=$1 AND u.is_active=true AND u.is_service=true
		)`, AutomationUsername)
	if scanErr := row.Scan(&enabled); scanErr != nil {
		return false, false, ErrAdminUnavailable
	}
	if !enabled {
		return false, false, nil
	}
	access, accessErr := s.Access(ctx)
	if accessErr != nil {
		return false, false, accessErr
	}
	return true, hasAutomationWriteGrant(access.AutomationGrants), nil
}

// hasAutomationWriteGrant accepts only an exact global resource/verb pair from
// Astronomer's published auto-eligible management-plane catalog. Wildcards and
// cluster/project grants do not satisfy auto-mode readiness in v1.
func hasAutomationWriteGrant(grants []AdminPermission) bool {
	required := make(map[string]struct{})
	for _, capability := range WriteCapabilityCatalog() {
		if capability.AutoEligible {
			required[capability.RBACResource+":"+capability.RBACVerb] = struct{}{}
		}
	}
	for _, grant := range grants {
		if grant.Scope != "global" {
			continue
		}
		if _, ok := required[grant.Permission]; ok {
			return true
		}
	}
	return false
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
