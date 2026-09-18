package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/agentcompat"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/google/uuid"
)

type clusterAgentCursorQuerier interface {
	ListClustersAfter(ctx context.Context, arg sqlc.ListClustersAfterParams) ([]sqlc.Cluster, error)
}

func (h *ClusterAgentHandler) List(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}

	limit := int32(queryLimitMax(r, 100, 500))
	offset := int32(queryOffset(r))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	cursorValues, cursorProvided := r.URL.Query()["cursor"]
	_, offsetProvided := r.URL.Query()["offset"]
	if cursorProvided && offsetProvided {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cursor and offset cannot be combined")
		return
	}
	cursorMode := !offsetProvided
	cursorBinding := paging.Binding("cluster-agents:v1", "created_at:desc,id:desc")
	var cursor paging.Cursor
	var err error
	if cursorProvided {
		if len(cursorValues) != 1 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cursor must be one opaque value")
			return
		}
		cursor, err = paging.DecodeCursor(cursorValues[0], cursorBinding)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid pagination cursor")
			return
		}
	}
	cursorQueries, cursorCapable := h.queries.(clusterAgentCursorQuerier)
	if cursorMode && !cursorCapable {
		if cursorProvided {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Cursor cluster-agent listing is not available")
			return
		}
		cursorMode = false
	}

	total, err := h.queries.CountClusters(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
		return
	}
	var clusters []sqlc.Cluster
	if cursorMode {
		clusters, err = cursorQueries.ListClustersAfter(r.Context(), sqlc.ListClustersAfterParams{
			HasCursor: cursorProvided, AfterCreatedAt: cursor.Time, AfterID: cursor.ID, QueryLimit: limit + 1,
		})
	} else {
		clusters, err = h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{Limit: limit, Offset: offset})
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
		return
	}
	nextCursor := ""
	seen := int(offset)
	if cursorMode {
		seen = cursor.Seen
		if len(clusters) > int(limit) {
			clusters = clusters[:limit]
			last := clusters[len(clusters)-1]
			nextCursor, err = paging.EncodeCursor(paging.NewCursor(last.CreatedAt, last.ID, cursorBinding, seen+len(clusters)))
			if err != nil {
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode pagination cursor")
				return
			}
		}
	}
	// Load the most recent connection per cluster for the page in a single
	// DISTINCT ON query rather than one fallback query per disconnected
	// cluster (disconnected is the steady state, so the per-row fallback used
	// to issue up to ~500 queries per page).
	latestByCluster := make(map[uuid.UUID]sqlc.AgentConnection, len(clusters))
	if len(clusters) > 0 {
		clusterIDs := make([]uuid.UUID, len(clusters))
		for i, cluster := range clusters {
			clusterIDs[i] = cluster.ID
		}
		latest, lerr := h.queries.ListLatestConnectionsByClusters(r.Context(), clusterIDs)
		if lerr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.AgentConnectionError, "Failed to list agent connections")
			return
		}
		for _, conn := range latest {
			latestByCluster[conn.ClusterID] = conn
		}
	}

	now := h.now().UTC()
	items := make([]clusterAgentItem, 0, len(clusters))
	summary := clusterAgentSummary{
		TotalClusters:                 total,
		Versions:                      map[string]int{},
		Profiles:                      map[string]int{},
		Statuses:                      map[string]int{},
		Compatibility:                 map[string]int{},
		ServerVersion:                 version.Version,
		MinimumSupportedAgentVersion:  agentcompat.MinimumSupportedVersion,
		MinimumCompatibleAgentVersion: agentcompat.MinimumCompatibleVersion,
		MaximumSupportedAgentVersion:  protocol.MaximumSupportedAgentVersionExclusive,
		GeneratedAt:                   now.Format(time.RFC3339),
	}
	for _, cluster := range clusters {
		conn := latestByCluster[cluster.ID]
		connected := conn.ID != uuid.Nil && conn.Status == "connected" && !conn.DisconnectedAt.Valid
		item := buildClusterAgentItem(cluster, conn, connected, now)
		items = append(items, item)
		summary.Statuses[item.AgentStatus]++
		switch item.AgentStatus {
		case "connected":
			summary.Connected++
		case "degraded":
			summary.Degraded++
		default:
			summary.Disconnected++
		}
		if item.AgentVersion != "" {
			summary.Versions[item.AgentVersion]++
		}
		summary.Profiles[item.PrivilegeProfile]++
		summary.Compatibility[item.CompatibilityStatus]++
	}

	metadata := paging.Exact(total, int(limit), int(offset), len(items))
	if cursorMode {
		metadata = paging.CursorPage(total, int(limit), seen, len(items), nextCursor)
	}
	RespondJSONUnwrapped(w, http.StatusOK, clusterAgentResponse{
		Response: paging.Response[clusterAgentItem]{Data: items, Pagination: metadata},
		Summary:  summary,
	})
}

func (h *ClusterAgentHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	connections, err := h.queries.ListConnectionsByCluster(r.Context(), sqlc.ListConnectionsByClusterParams{
		ClusterID: clusterID, Limit: 1, Offset: 0,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AgentConnectionError, "Failed to list agent connections")
		return
	}
	now := h.now().UTC()
	active := sqlc.AgentConnection{}
	connected := false
	if len(connections) > 0 {
		active = connections[0]
		connected = active.Status == "connected"
	}
	RespondJSON(w, http.StatusOK, buildClusterAgentItem(cluster, active, connected, now))
}

func (h *ClusterAgentHandler) Diagnostics(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	_, diagnostics, err := h.buildDiagnostics(r.Context(), clusterID)
	if err != nil {
		respondClusterAgentError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, diagnostics)
}

func (h *ClusterAgentHandler) DiagnosticsBundle(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, diagnostics, err := h.buildDiagnostics(r.Context(), clusterID)
	if err != nil {
		respondClusterAgentError(w, r, err)
		return
	}

	filename := "astronomer-agent-diagnostics-" + cluster.ID.String() + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	bundle := agentDiagnosticsBundleResponse{
		Version:     "v1",
		GeneratedAt: h.now().UTC().Format(time.RFC3339),
		ClusterID:   cluster.ID.String(),
		ClusterName: firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		Diagnostics: diagnostics,
		Notes: []string{
			"Credential material and certificate bodies are intentionally excluded.",
			"Use this bundle for support triage; it does not grant cluster access.",
		},
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(redaction.Payload(bundle))
}

func (h *ClusterAgentHandler) SelfTest(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ClusterAgentUnavailable, "Cluster agent inventory is not configured")
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, diagnostics, err := h.buildDiagnostics(r.Context(), clusterID)
	if err != nil {
		respondClusterAgentError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, buildAgentSelfTest(cluster, diagnostics, h.now().UTC()))
}

func (h *ClusterAgentHandler) buildDiagnostics(ctx context.Context, clusterID uuid.UUID) (sqlc.Cluster, agentDiagnosticsResponse, error) {
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &clusterAgentHandlerError{status: http.StatusNotFound, code: "not_found", message: "Cluster not found"}
	}
	connections, err := h.queries.ListConnectionsByCluster(ctx, sqlc.ListConnectionsByClusterParams{
		ClusterID: clusterID,
		Limit:     10,
		Offset:    0,
	})
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &clusterAgentHandlerError{status: http.StatusInternalServerError, code: "connection_error", message: "Failed to list agent connections"}
	}
	conditions, err := h.queries.ListClusterConditions(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &clusterAgentHandlerError{status: http.StatusInternalServerError, code: "condition_error", message: "Failed to list cluster conditions"}
	}
	now := h.now().UTC()
	active := sqlc.AgentConnection{}
	connected := false
	for _, conn := range connections {
		if conn.Status == "connected" {
			active = conn
			connected = true
			break
		}
	}
	if !connected && len(connections) > 0 {
		active = connections[0]
	}
	agent := buildClusterAgentItem(cluster, active, connected, now)
	recommendations := append([]string{}, agent.DegradedReasons...)
	if agent.RecommendedAction != "" {
		recommendations = append(recommendations, agent.RecommendedAction)
	}
	for _, condition := range conditions {
		if condition.Status != "True" && condition.Message != "" {
			recommendations = append(recommendations, condition.Type+": "+condition.Message)
		}
	}
	response := agentDiagnosticsResponse{
		GeneratedAt:       now.Format(time.RFC3339),
		Agent:             agent,
		RecentConnections: connectionDiagnostics(connections),
		Conditions:        conditionDiagnostics(conditions),
		Recommendations:   recommendations,
		Redactions: []string{
			"agent registration tokens are not included",
			"cluster CA certificate material is not included",
			"pod logs and Kubernetes secrets are not included in this bundle",
		},
		UpgradeRecommendation: upgradeRecommendation(agent),
	}
	if h.requester != nil && agent.AgentStatus != "disconnected" {
		live := h.collectLiveDiagnostics(ctx, cluster.ID.String(), now)
		response.Live = &live
		response.Redactions = append(response.Redactions, "live agent logs are tail-limited and sensitive-looking lines are redacted")
	}
	return cluster, response, nil
}

func buildAgentSelfTest(cluster sqlc.Cluster, diagnostics agentDiagnosticsResponse, now time.Time) agentSelfTestResponse {
	agent := diagnostics.Agent
	checks := []agentSelfTestCheck{
		agentConnectionSelfTestCheck(agent),
		agentTimestampSelfTestCheck("heartbeat_freshness", "heartbeat", agent.LastHeartbeat, now, 2*time.Minute, 5*time.Minute),
		agentTimestampSelfTestCheck("ping_freshness", "connection ping", agent.LastPing, now, 2*time.Minute, 5*time.Minute),
		agentPrivilegeProfileSelfTestCheck(agent),
		agentCompatibilitySelfTestCheck(agent),
		agentLiveDiagnosticsSelfTestCheck(agent, diagnostics.Live),
		agentClusterConditionsSelfTestCheck(diagnostics.Conditions),
	}

	return agentSelfTestResponse{
		GeneratedAt:     now.UTC().Format(time.RFC3339),
		ClusterID:       cluster.ID.String(),
		ClusterName:     firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		Status:          agentSelfTestOverallStatus(checks),
		Checks:          checks,
		Recommendations: append([]string{}, diagnostics.Recommendations...),
	}
}

func agentConnectionSelfTestCheck(agent clusterAgentItem) agentSelfTestCheck {
	switch agent.AgentStatus {
	case "connected":
		return agentSelfTestCheck{Name: "agent_connection", Status: "passed", Message: "Agent tunnel is connected."}
	case "degraded":
		message := "Agent tunnel is connected but degraded."
		if len(agent.DegradedReasons) > 0 {
			message = message + " " + strings.Join(agent.DegradedReasons, "; ")
		}
		return agentSelfTestCheck{Name: "agent_connection", Status: "warning", Message: message}
	case "disconnected":
		return agentSelfTestCheck{Name: "agent_connection", Status: "failed", Message: "Agent tunnel is disconnected."}
	default:
		return agentSelfTestCheck{Name: "agent_connection", Status: "warning", Message: "Agent tunnel state is unknown."}
	}
}

func agentTimestampSelfTestCheck(name, label string, value *string, now time.Time, warnAfter, failAfter time.Duration) agentSelfTestCheck {
	if value == nil || strings.TrimSpace(*value) == "" {
		return agentSelfTestCheck{Name: name, Status: "failed", Message: "No " + label + " timestamp has been recorded."}
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return agentSelfTestCheck{Name: name, Status: "failed", Message: "The " + label + " timestamp is invalid."}
	}
	age := now.Sub(parsed)
	if age < 0 {
		age = 0
	}
	ageText := age.Round(time.Second).String()
	switch {
	case age > failAfter:
		return agentSelfTestCheck{Name: name, Status: "failed", Message: "Last " + label + " is stale (" + ageText + " ago)."}
	case age > warnAfter:
		return agentSelfTestCheck{Name: name, Status: "warning", Message: "Last " + label + " is aging (" + ageText + " ago)."}
	default:
		return agentSelfTestCheck{Name: name, Status: "passed", Message: "Last " + label + " is fresh (" + ageText + " ago)."}
	}
}

func agentPrivilegeProfileSelfTestCheck(agent clusterAgentItem) agentSelfTestCheck {
	effectiveProfile := agenttemplate.NormalizePrivilegeProfile(agent.PrivilegeProfile)
	switch effectiveProfile {
	case agenttemplate.PrivilegeProfileViewer, agenttemplate.PrivilegeProfileOperator,
		agenttemplate.PrivilegeProfileNamespaceViewer, agenttemplate.PrivilegeProfileNamespaceOperator:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "passed", Message: "Agent is using the effective " + effectiveProfile + " privilege profile."}
	case agenttemplate.PrivilegeProfileAdmin:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "passed", Message: "Agent is using the explicit full-management (admin) privilege profile. Apply a viewer/operator profile to scope it down if least privilege is required."}
	case agenttemplate.PrivilegeProfileCustom:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "warning", Message: "Agent is using custom RBAC; run live diagnostics to verify required permissions."}
	default:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "warning", Message: "Agent privilege profile is unknown."}
	}
}

func agentCompatibilitySelfTestCheck(agent clusterAgentItem) agentSelfTestCheck {
	message := agent.CompatibilityMessage
	if message == "" {
		message = "Agent compatibility status is " + agent.CompatibilityStatus + "."
	}
	switch agent.CompatibilityStatus {
	case "supported":
		return agentSelfTestCheck{Name: "compatibility", Status: "passed", Message: message}
	case "blocked":
		return agentSelfTestCheck{Name: "compatibility", Status: "failed", Message: message}
	default:
		return agentSelfTestCheck{Name: "compatibility", Status: "warning", Message: message}
	}
}

func agentLiveDiagnosticsSelfTestCheck(agent clusterAgentItem, live *agentLiveDiagnostics) agentSelfTestCheck {
	if agent.AgentStatus == "disconnected" {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "failed", Message: "Live diagnostics cannot run while the agent is disconnected."}
	}
	if live == nil {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "warning", Message: "Live diagnostics requester is not configured for this server."}
	}
	if len(live.Errors) > 0 {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "warning", Message: "Live diagnostics completed with errors: " + strings.Join(live.Errors, "; ")}
	}
	warnings := make([]string, 0)
	for _, check := range live.Checks {
		if check.Status == "failed" {
			return agentSelfTestCheck{Name: "live_diagnostics", Status: "failed", Message: "Live diagnostic check failed: " + check.Name + ". " + check.Message}
		}
		if check.Status == "warning" {
			warnings = append(warnings, check.Name)
		}
	}
	if len(warnings) > 0 {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "warning", Message: "Live diagnostics completed with warnings: " + strings.Join(warnings, ", ")}
	}
	return agentSelfTestCheck{Name: "live_diagnostics", Status: "passed", Message: "Live diagnostics completed through the agent tunnel."}
}

func agentClusterConditionsSelfTestCheck(conditions []clusterConditionDiagnostic) agentSelfTestCheck {
	if len(conditions) == 0 {
		return agentSelfTestCheck{Name: "cluster_conditions", Status: "warning", Message: "No cluster conditions have been recorded yet."}
	}
	// optionalCapabilityConditions are cluster features that are legitimately
	// absent on many clusters (e.g. no Gateway API CRDs installed). A False here
	// means "this optional capability isn't present", not "the cluster is
	// unhealthy", so it must NOT fail the self-test — surface it as a warning.
	optionalCapabilityConditions := map[string]bool{
		"GatewayAPISupported": true,
	}
	falseConditions := make([]string, 0)
	optionalFalse := make([]string, 0)
	unknownConditions := make([]string, 0)
	for _, condition := range conditions {
		// MetricsAvailable (C3 / M13) is observability only — a cluster with no
		// metrics-server legitimately reports it False (NoMetricsServer) and that
		// must NOT flip the agent self-test to failed. Skip it here; the distinct
		// reason is still surfaced in the raw conditions list for the UI pill.
		if condition.Type == "MetricsAvailable" {
			continue
		}
		switch condition.Status {
		case "True":
			continue
		case "False":
			if optionalCapabilityConditions[condition.Type] {
				optionalFalse = append(optionalFalse, condition.Type)
			} else {
				falseConditions = append(falseConditions, condition.Type)
			}
		default:
			unknownConditions = append(unknownConditions, condition.Type)
		}
	}
	if len(falseConditions) > 0 {
		return agentSelfTestCheck{Name: "cluster_conditions", Status: "failed", Message: "False cluster conditions: " + strings.Join(falseConditions, ", ")}
	}
	if len(optionalFalse) > 0 || len(unknownConditions) > 0 {
		notes := append(append([]string{}, optionalFalse...), unknownConditions...)
		return agentSelfTestCheck{Name: "cluster_conditions", Status: "warning", Message: "Optional or unknown cluster conditions not satisfied: " + strings.Join(notes, ", ")}
	}
	return agentSelfTestCheck{Name: "cluster_conditions", Status: "passed", Message: "Recorded cluster conditions are healthy."}
}

func agentSelfTestOverallStatus(checks []agentSelfTestCheck) string {
	status := "passed"
	for _, check := range checks {
		switch check.Status {
		case "failed":
			return "failed"
		case "warning":
			status = "warning"
		}
	}
	return status
}
