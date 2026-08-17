package charlie

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	MaxContextSearchResults = 20
	maxContextSearchRows    = 500
)

type contextSearchQueries interface {
	CharlieClusterAgentList(context.Context, sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error)
}

type ContextSearchResult struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	RequiredVerb string `json:"required_verb"`
	Label        string `json:"label"`
	Summary      string `json:"summary"`
}

// ContextSearchService returns identifiers and labels only. Downstream cluster
// rows are represented exclusively as Astronomer-owned agent connection
// records; this service never reads or implies access to downstream Kubernetes.
type ContextSearchService struct {
	queries    contextSearchQueries
	authorizer SessionAccessAuthorizer
	active     func() bool
}

func NewContextSearchService(queries contextSearchQueries, authorizer SessionAccessAuthorizer, active func() bool) *ContextSearchService {
	if queries == nil || authorizer == nil || active == nil {
		return nil
	}
	return &ContextSearchService{queries: queries, authorizer: authorizer, active: active}
}

func (s *ContextSearchService) Search(ctx context.Context, actorID uuid.UUID, query string, limit int32) ([]ContextSearchResult, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if s == nil || !s.active() || actorID == uuid.Nil || len(query) > 128 || limit < 1 || limit > MaxContextSearchResults {
		return nil, fmt.Errorf("Charlie context search is unavailable")
	}
	allowed, err := s.authorizer.CanUseCharlie(ctx, actorID)
	if err != nil || !allowed {
		return nil, fmt.Errorf("Charlie context search is denied")
	}
	results := make([]ContextSearchResult, 0, limit)
	for _, candidate := range staticContextCandidates() {
		if !contextMatches(candidate, query) || !s.canRead(ctx, actorID, candidate) {
			continue
		}
		results = append(results, candidate)
	}
	// Opening the picker presents a compact, useful set of management-plane
	// scopes. Individual agent connections are searched only after the operator
	// types a query so a large cluster-agent population cannot bury the standard choices.
	if query == "" {
		sort.SliceStable(results, func(i, j int) bool { return results[i].Label < results[j].Label })
		if len(results) > int(limit) {
			results = results[:limit]
		}
		return results, nil
	}
	rows, err := s.queries.CharlieClusterAgentList(ctx, sqlc.CharlieClusterAgentListParams{
		Environment: pgtype.Text{}, Region: pgtype.Text{}, ConnectionState: pgtype.Text{},
		PageOffset: 0, PageLimit: maxContextSearchRows,
	})
	if err != nil {
		return nil, fmt.Errorf("Charlie context search is unavailable")
	}
	for _, row := range rows {
		candidate := ContextSearchResult{
			Type: "agent_connection_record", ID: row.ClusterID.String(), RequiredVerb: "read",
			Label:   strings.TrimSpace(firstNonempty(row.DisplayName, row.ClusterName, "Cluster agent connection")),
			Summary: boundedContextSummary("Astronomer-owned agent connection metadata", row.Environment, row.Region),
		}
		haystack := strings.ToLower(strings.Join([]string{candidate.ID, candidate.Label, row.AgentID, row.Environment, row.Region}, " "))
		if !strings.Contains(haystack, query) || !s.canRead(ctx, actorID, candidate) {
			continue
		}
		results = append(results, candidate)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Label < results[j].Label })
	if len(results) > int(limit) {
		results = results[:limit]
	}
	return results, nil
}

func (s *ContextSearchService) canRead(ctx context.Context, actorID uuid.UUID, candidate ContextSearchResult) bool {
	allowed, err := s.authorizer.CanReadIncidentResources(ctx, actorID, []sqlc.CharlieSessionResource{{
		ResourceType: candidate.Type, ResourceID: candidate.ID, RequiredVerb: "read",
	}})
	return err == nil && allowed
}

func staticContextCandidates() []ContextSearchResult {
	results := []ContextSearchResult{
		{Type: "installation", ID: "local", RequiredVerb: "read", Label: "Astronomer installation", Summary: "This Astronomer management-plane installation"},
		{Type: "cluster_agents", ID: "cluster-agents", RequiredVerb: "read", Label: "Cluster agents", Summary: "Astronomer-owned connection health and cross-cluster patterns"},
		{Type: "tunnel", ID: "management-plane", RequiredVerb: "read", Label: "Agent tunnel", Summary: "Astronomer management-plane tunnel and locator health"},
		{Type: "alert", ID: "active", RequiredVerb: "read", Label: "Active management alerts", Summary: "Authorized Astronomer management-plane alerts"},
		{Type: "backup", ID: "overview", RequiredVerb: "read", Label: "Management backups", Summary: "Astronomer management-plane backup health"},
		{Type: "self_management_application", ID: "astronomer", RequiredVerb: "read", Label: "Astronomer GitOps application", Summary: "Astronomer-owned self-management reconciliation"},
	}
	for _, component := range []string{"server", "worker", "frontend", "postgres", "redis", "ingress", "delivery"} {
		results = append(results, ContextSearchResult{Type: "management_component", ID: component, RequiredVerb: "read", Label: "Astronomer " + component, Summary: "Astronomer management-plane component"})
	}
	return results
}

func contextMatches(candidate ContextSearchResult, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(candidate.ID+" "+candidate.Label+" "+candidate.Summary), query)
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boundedContextSummary(prefix, environment, region string) string {
	parts := []string{prefix}
	if environment = strings.TrimSpace(environment); environment != "" {
		parts = append(parts, "environment "+environment)
	}
	if region = strings.TrimSpace(region); region != "" {
		parts = append(parts, "region "+region)
	}
	return truncateUTF8(strings.Join(parts, ", "), 160)
}
