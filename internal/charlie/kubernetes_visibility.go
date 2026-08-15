package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const KubernetesVisibilitySchema = "charlie.kubernetes-visibility/v1"

type KubernetesVisibilityRediscoverer interface {
	RequestCapabilityRediscovery(context.Context) (contract.IntegrationRediscoveryReceipt, error)
}

type KubernetesVisibilityProfile string

const (
	KubernetesVisibilityDisabled           KubernetesVisibilityProfile = "disabled"
	KubernetesVisibilityProductNamespace   KubernetesVisibilityProfile = "product_namespace"
	KubernetesVisibilityClusterDiagnostics KubernetesVisibilityProfile = "cluster_diagnostics"
)

type AdminKubernetesVisibilityView struct {
	Schema                         string                        `json:"schema"`
	Profile                        KubernetesVisibilityProfile   `json:"profile"`
	Revision                       int64                         `json:"revision"`
	State                          string                        `json:"state"`
	InstanceID                     string                        `json:"instance_id"`
	Namespaces                     []string                      `json:"namespaces"`
	ProductOwnedOnly               bool                          `json:"product_owned_only"`
	ClusterScoped                  bool                          `json:"cluster_scoped"`
	PodLogs                        bool                          `json:"pod_logs"`
	DownstreamTargets              bool                          `json:"downstream_targets"`
	SecretValues                   bool                          `json:"secret_values"`
	Exec                           bool                          `json:"exec"`
	Attach                         bool                          `json:"attach"`
	PortForward                    bool                          `json:"port_forward"`
	APIProxy                       bool                          `json:"api_proxy"`
	RequiresRediscovery            bool                          `json:"requires_rediscovery"`
	RequiresCentralReview          bool                          `json:"requires_central_review"`
	RequiresProductAcknowledgement bool                          `json:"requires_product_acknowledgement"`
	CandidateDisclosureDigest      string                        `json:"candidate_disclosure_digest,omitempty"`
	AvailableProfiles              []KubernetesVisibilityProfile `json:"available_profiles"`
	ScopeSummary                   string                        `json:"scope_summary"`
}

// AdminKubernetesVisibilityInput changes observation scope only. It cannot
// alter Charlie's independent write mode or enable any interactive transport.
// openapi:request CharlieKubernetesVisibilityRequest
type AdminKubernetesVisibilityInput struct {
	Profile  KubernetesVisibilityProfile `json:"profile"`
	PodLogs  bool                        `json:"pod_logs"`
	Revision int64                       `json:"revision"`
}

func validKubernetesVisibilityProfile(profile KubernetesVisibilityProfile) bool {
	return slices.Contains([]KubernetesVisibilityProfile{
		KubernetesVisibilityDisabled, KubernetesVisibilityProductNamespace, KubernetesVisibilityClusterDiagnostics,
	}, profile)
}

func kubernetesVisibilityAuthorityPending(connection sqlc.CharlieConnection) bool {
	return connection.KubernetesVisibilityRediscoveryState == "required" ||
		(connection.KubernetesVisibilityRediscoveryState == "review_required" &&
			(connection.DisclosureDigest == "" || connection.AcknowledgedDisclosureDigest != connection.DisclosureDigest))
}

func safeKubernetesVisibility(connection sqlc.CharlieConnection) AdminKubernetesVisibilityView {
	profile := KubernetesVisibilityProfile(connection.KubernetesVisibilityProfile)
	if !validKubernetesVisibilityProfile(profile) {
		profile = KubernetesVisibilityDisabled
	}
	enabled := profile != KubernetesVisibilityDisabled
	clusterScoped := profile == KubernetesVisibilityClusterDiagnostics
	namespaces := []string{}
	if enabled {
		namespaces = []string{managementNamespace(connection)}
	}
	summary := "Kubernetes API visibility is disabled"
	if profile == KubernetesVisibilityProductNamespace {
		summary = "Product-owned resources in the Astronomer management namespace"
	} else if clusterScoped {
		summary = "Product-owned management resources plus bounded cluster diagnostics; downstream clusters excluded"
	}
	return AdminKubernetesVisibilityView{
		Schema: KubernetesVisibilitySchema, Profile: profile, Revision: connection.VerifiedModeRevision,
		State: map[bool]string{true: "enabled", false: "disabled"}[enabled], InstanceID: "astronomer-management-plane",
		Namespaces: namespaces, ProductOwnedOnly: true, ClusterScoped: clusterScoped,
		PodLogs:           enabled && connection.KubernetesVisibilityPodLogs,
		DownstreamTargets: false, SecretValues: false, Exec: false, Attach: false, PortForward: false, APIProxy: false,
		RequiresRediscovery: connection.KubernetesVisibilityRediscoveryState == "required",
		// The candidate digest records exactly what rediscovery observed. The
		// central administrator may intentionally change mode or allowlists while
		// reviewing it, which produces a different active disclosure digest. Any
		// non-empty active digest therefore proves central review completed; the
		// product must then acknowledge that authoritative digest separately.
		RequiresCentralReview: connection.KubernetesVisibilityRediscoveryState == "review_required" &&
			connection.DisclosureDigest == "",
		RequiresProductAcknowledgement: connection.KubernetesVisibilityRediscoveryState == "review_required" &&
			connection.DisclosureDigest != "" && connection.AcknowledgedDisclosureDigest != connection.DisclosureDigest,
		CandidateDisclosureDigest: connection.KubernetesVisibilityCandidateDigest,
		AvailableProfiles:         []KubernetesVisibilityProfile{KubernetesVisibilityDisabled, KubernetesVisibilityProductNamespace, KubernetesVisibilityClusterDiagnostics},
		ScopeSummary:              summary,
	}
}

func (s *AdminService) KubernetesVisibility(ctx context.Context) (AdminKubernetesVisibilityView, error) {
	connection, err := s.connection(ctx)
	if errors.Is(err, ErrAdminNotConfigured) {
		return AdminKubernetesVisibilityView{
			Schema: KubernetesVisibilitySchema, Profile: KubernetesVisibilityDisabled, State: "not_configured",
			InstanceID: "astronomer-management-plane", Namespaces: []string{}, ProductOwnedOnly: true,
			AvailableProfiles: []KubernetesVisibilityProfile{KubernetesVisibilityDisabled, KubernetesVisibilityProductNamespace, KubernetesVisibilityClusterDiagnostics},
			ScopeSummary:      "Connect Charlie before enabling Kubernetes API visibility",
		}, nil
	}
	if err != nil {
		return AdminKubernetesVisibilityView{}, err
	}
	return safeKubernetesVisibility(connection), nil
}

func (s *AdminService) UpdateKubernetesVisibility(ctx context.Context, input AdminKubernetesVisibilityInput, actor uuid.UUID) (AdminKubernetesVisibilityView, error) {
	if s == nil || s.queries == nil || actor == uuid.Nil || !validKubernetesVisibilityProfile(input.Profile) || input.Revision < 0 || (input.Profile == KubernetesVisibilityDisabled && input.PodLogs) {
		return AdminKubernetesVisibilityView{}, ErrAdminConflict
	}
	connection, err := s.connection(ctx)
	if err != nil || !connection.Active || connection.EmergencyDisabled || input.Revision != connection.VerifiedModeRevision {
		return AdminKubernetesVisibilityView{}, ErrAdminConflict
	}
	unchanged := KubernetesVisibilityProfile(connection.KubernetesVisibilityProfile) == input.Profile && connection.KubernetesVisibilityPodLogs == input.PodLogs
	if unchanged && connection.KubernetesVisibilityRediscoveryState != "required" {
		return safeKubernetesVisibility(connection), nil
	}
	if err := s.requireAuthorityAudit(ctx, AuthorityMutationAudit{
		Action: "charlie.kubernetes_visibility.update", ActorID: actor, ResourceType: "charlie_connection", ResourceID: connection.ID.String(),
		Fields: map[string]any{"profile": string(input.Profile), "pod_logs": input.PodLogs, "revision": input.Revision},
	}); err != nil {
		return AdminKubernetesVisibilityView{}, err
	}
	if unchanged {
		return s.requestKubernetesVisibilityRediscovery(ctx, connection)
	}
	if s.mode != nil && s.mode.writes != nil {
		if _, err := s.mode.writes.CloseAndWait(ctx); err != nil {
			return AdminKubernetesVisibilityView{}, ErrAdminConflict
		}
	}
	updated, err := s.queries.UpdateCharlieKubernetesVisibility(ctx, sqlc.UpdateCharlieKubernetesVisibilityParams{
		ID: connection.ID, KubernetesVisibilityProfile: string(input.Profile), KubernetesVisibilityPodLogs: input.PodLogs,
		ExpectedRevision: input.Revision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminKubernetesVisibilityView{}, ErrAdminConflict
	}
	if err != nil {
		return AdminKubernetesVisibilityView{}, ErrAdminUnavailable
	}
	return s.requestKubernetesVisibilityRediscovery(ctx, updated)
}

func (s *AdminService) requestKubernetesVisibilityRediscovery(ctx context.Context, connection sqlc.CharlieConnection) (AdminKubernetesVisibilityView, error) {
	if s.visibilityRediscovery == nil {
		return AdminKubernetesVisibilityView{}, ErrAdminUnavailable
	}
	receipt, err := s.visibilityRediscovery.RequestCapabilityRediscovery(ctx)
	if err != nil {
		return AdminKubernetesVisibilityView{}, ErrAdminUnavailable
	}
	remoteRevision, err := strconv.ParseInt(receipt.IntegrationRevision, 10, 64)
	if err != nil || remoteRevision < connection.VerifiedModeRevision || receipt.State != "disabled" {
		return AdminKubernetesVisibilityView{}, ErrAdminConflict
	}
	confirmed, err := s.queries.ConfirmCharlieKubernetesVisibilityRediscovery(ctx, sqlc.ConfirmCharlieKubernetesVisibilityRediscoveryParams{
		ID: connection.ID, CandidateDigest: receipt.DisclosureDigest, RemoteRevision: remoteRevision,
		ExpectedLocalRevision: connection.VerifiedModeRevision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminKubernetesVisibilityView{}, ErrAdminConflict
	}
	if err != nil {
		return AdminKubernetesVisibilityView{}, ErrAdminUnavailable
	}
	return safeKubernetesVisibility(confirmed), nil
}

// ConnectorCapabilityMetadata is content-free provenance included in the MCP
// safety disclosure. Charlie central incorporates it into the capability digest.
type ConnectorCapabilityMetadata struct {
	Schema            string   `json:"schema"`
	Kind              string   `json:"kind"`
	Profile           string   `json:"profile"`
	Boundary          string   `json:"boundary"`
	InstanceID        string   `json:"instance_id"`
	ScopeSummary      string   `json:"scope_summary"`
	ClusterScoped     bool     `json:"cluster_scoped"`
	ContentAccess     []string `json:"content_access"`
	DownstreamTargets bool     `json:"downstream_targets"`
}

type ConnectorMetadataProvider interface {
	ConnectorMetadata(context.Context, string) (ConnectorCapabilityMetadata, bool)
}

type activeCharlieConnectionReader interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
}

// KubernetesVisibilityExecutor rechecks the persisted profile for discovery
// and again at execution. It wraps only product-owned adapters and never owns a
// Kubernetes credential itself.
type KubernetesVisibilityExecutor struct {
	next   CapabilityExecutor
	reader activeCharlieConnectionReader
}

func NewKubernetesVisibilityExecutor(next CapabilityExecutor, reader activeCharlieConnectionReader) (*KubernetesVisibilityExecutor, error) {
	if next == nil || reader == nil {
		return nil, fmt.Errorf("Charlie Kubernetes visibility dependencies are unavailable")
	}
	return &KubernetesVisibilityExecutor{next: next, reader: reader}, nil
}

func (e *KubernetesVisibilityExecutor) visibility(ctx context.Context) (AdminKubernetesVisibilityView, error) {
	connection, err := e.reader.GetActiveCharlieConnection(ctx)
	if err != nil {
		return AdminKubernetesVisibilityView{}, err
	}
	return safeKubernetesVisibility(connection), nil
}

func (e *KubernetesVisibilityExecutor) allowed(ctx context.Context, name string) bool {
	descriptor, ok := capabilityByName(name)
	if !ok {
		return false
	}
	if availability, ok := e.next.(CapabilityAvailability); ok && !availability.SupportsCapability(ctx, name) {
		return false
	}
	if descriptor.Source != SourceManagementKubernetes {
		return true
	}
	view, err := e.visibility(ctx)
	if err != nil || view.Profile == KubernetesVisibilityDisabled {
		return false
	}
	if name == "astronomer.management.pod_logs" && !view.PodLogs {
		return false
	}
	if name == "astronomer.management.nodes" && !view.ClusterScoped {
		return false
	}
	return true
}

func (e *KubernetesVisibilityExecutor) SupportsCapability(ctx context.Context, name string) bool {
	return e != nil && e.allowed(ctx, name)
}

func (e *KubernetesVisibilityExecutor) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	if e == nil || !e.allowed(ctx, capability.Name) {
		return nil, fmt.Errorf("Charlie capability adapter is unavailable")
	}
	return e.next.Execute(ctx, capability, arguments)
}

func (e *KubernetesVisibilityExecutor) Verify(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, result json.RawMessage) (bool, error) {
	if e == nil || !e.allowed(ctx, capability.Name) {
		return false, fmt.Errorf("Charlie capability adapter is unavailable")
	}
	return e.next.Verify(ctx, capability, arguments, result)
}

func (e *KubernetesVisibilityExecutor) ConnectorMetadata(ctx context.Context, name string) (ConnectorCapabilityMetadata, bool) {
	descriptor, ok := capabilityByName(name)
	if !ok || descriptor.Source != SourceManagementKubernetes || !e.allowed(ctx, name) {
		return ConnectorCapabilityMetadata{}, false
	}
	view, err := e.visibility(ctx)
	if err != nil {
		return ConnectorCapabilityMetadata{}, false
	}
	content := []string{}
	if view.PodLogs {
		content = append(content, "pod_logs")
	}
	return ConnectorCapabilityMetadata{
		Schema: "charlie.connector-capability/v1", Kind: "kubernetes", Profile: string(view.Profile),
		Boundary: "product_runtime", InstanceID: view.InstanceID, ScopeSummary: view.ScopeSummary,
		ClusterScoped: view.ClusterScoped, ContentAccess: content, DownstreamTargets: false,
	}, true
}

func managementNamespace(connection sqlc.CharlieConnection) string {
	parts := strings.Split(strings.TrimSpace(connection.McpServiceName), ".")
	if len(parts) >= 3 && parts[1] != "" {
		return parts[1]
	}
	return "astronomer"
}
