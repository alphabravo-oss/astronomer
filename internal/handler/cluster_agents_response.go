package handler

import (
	"encoding/json"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/agentcompat"
	"github.com/alphabravocompany/astronomer-go/internal/agentlifecycle"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func buildClusterAgentItem(cluster sqlc.Cluster, conn sqlc.AgentConnection, connected bool, now time.Time) clusterAgentItem {
	profile := agentPrivilegeProfileFromAnnotations(cluster.Annotations)
	agentVersion := firstNonEmptyAgentValue(conn.AgentVersion, cluster.AgentVersion)
	compatibility := agentcompat.Evaluate(agentVersion)
	item := clusterAgentItem{
		ClusterID:            cluster.ID.String(),
		ClusterName:          cluster.Name,
		ClusterDisplayName:   cluster.DisplayName,
		ClusterStatus:        cluster.Status,
		IsLocal:              cluster.IsLocal,
		AgentVersion:         agentVersion,
		KubernetesVersion:    cluster.KubernetesVersion,
		Distribution:         cluster.Distribution,
		NodeCount:            cluster.NodeCount,
		LastHeartbeat:        timestampPtr(conn.LastPing),
		PrivilegeProfile:     profile,
		Capabilities:         inferredAgentCapabilities(profile),
		CompatibilityStatus:  compatibility.Status,
		CompatibilityMessage: compatibility.Message,
	}
	if conn.ID != uuid.Nil {
		item.AgentID = conn.AgentID
		item.SessionID = conn.SessionID
		item.ConnectedAt = stringPtr(conn.ConnectedAt.UTC().Format(time.RFC3339))
		item.LastPing = timestampPtr(conn.LastPing)
		item.DisconnectedAt = timestampPtr(conn.DisconnectedAt)
		item.PodName = conn.PodName
		item.NodeName = conn.NodeName
		item.ChannelName = conn.ChannelName
	}

	reasons := make([]string, 0, 3)
	if !connected {
		item.AgentStatus = "disconnected"
		item.OfflineBehavior = buildAgentOfflineBehavior(cluster, conn, now)
		if cluster.Status == "awaiting_agent" {
			item.RecommendedAction = "Install or restart the Astronomer agent manifest for this cluster."
		} else {
			item.RecommendedAction = "Check the agent deployment, network egress, and registration token state."
		}
		return item
	}
	if item.LastPing != nil {
		if t, err := time.Parse(time.RFC3339, *item.LastPing); err == nil && now.Sub(t) > 2*time.Minute {
			reasons = append(reasons, "agent connection ping is stale")
		}
	}
	if item.LastHeartbeat != nil {
		if t, err := time.Parse(time.RFC3339, *item.LastHeartbeat); err == nil && now.Sub(t) > 2*time.Minute {
			reasons = append(reasons, "cluster heartbeat is stale")
		}
	}
	// Note: the full-admin privilege profile is a least-privilege *posture*
	// advisory (surfaced via PrivilegeProfile + the admin-posture endpoint),
	// not a health fault. A connected, fresh, compatible admin-profile agent
	// is fully functional, so it no longer flips the agent to "degraded".
	if compatibility.DegradedReason != "" {
		reasons = append(reasons, compatibility.DegradedReason)
	}
	if len(reasons) > 0 {
		item.AgentStatus = "degraded"
		item.DegradedReasons = reasons
		if compatibility.UpgradeRecommendation != "" {
			item.RecommendedAction = compatibility.UpgradeRecommendation
		} else {
			item.RecommendedAction = "Review the degraded reasons and rotate to the least-privilege operator profile where possible."
		}
		return item
	}
	item.AgentStatus = "connected"
	return item
}

func buildAgentOfflineBehavior(cluster sqlc.Cluster, conn sqlc.AgentConnection, now time.Time) *agentOfflineBehavior {
	lastKnown := latestAgentObservationTime(cluster, conn)
	var lastKnownAt *string
	stale := true
	if !lastKnown.IsZero() {
		stamp := lastKnown.UTC().Format(time.RFC3339)
		lastKnownAt = &stamp
		stale = now.Sub(lastKnown) > 5*time.Minute
	}
	message := "Agent tunnel is offline; live diagnostics and in-cluster operations are blocked until it reconnects."
	if lastKnownAt == nil {
		message = "Agent tunnel is offline and no last-known observation has been recorded yet."
	}
	return &agentOfflineBehavior{
		State:       "offline",
		LastKnownAt: lastKnownAt,
		Stale:       stale,
		Message:     message,
		PermittedQueuedOperations: []string{
			"cluster_metadata_updates",
			"agent_install_manifest_regeneration",
		},
		BlockedOperations: []string{
			agentlifecycle.OperationTypeUpgrade,
			"live_diagnostics",
			"kubernetes_proxy",
			"kubectl_exec",
			"pod_logs",
			"service_proxy",
			"in_cluster_mutations",
		},
	}
}

func latestAgentObservationTime(cluster sqlc.Cluster, conn sqlc.AgentConnection) time.Time {
	newest := time.Time{}
	for _, candidate := range []pgtype.Timestamptz{conn.LastPing, conn.DisconnectedAt} {
		if candidate.Valid && candidate.Time.After(newest) {
			newest = candidate.Time
		}
	}
	for _, candidate := range []time.Time{conn.ConnectedAt, cluster.UpdatedAt} {
		if !candidate.IsZero() && candidate.After(newest) {
			newest = candidate
		}
	}
	return newest
}

func agentPrivilegeProfileFromAnnotations(raw json.RawMessage) string {
	// Unspecified, malformed, and unknown annotations fail closed to viewer via
	// the canonical normalizer. Admin is available only as an explicit value.
	if len(raw) == 0 {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	return agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation])
}

func inferredAgentCapabilities(profile string) map[string]bool {
	switch agenttemplate.NormalizePrivilegeProfile(profile) {
	case agenttemplate.PrivilegeProfileViewer, agenttemplate.PrivilegeProfileNamespaceViewer:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          false,
			"helm":          false,
			"service_proxy": false,
			"mutate":        false,
		}
	case agenttemplate.PrivilegeProfileOperator:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          true,
			"helm":          true,
			"service_proxy": true,
			"mutate":        true,
		}
	case agenttemplate.PrivilegeProfileNamespaceOperator:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          true,
			"helm":          false,
			"service_proxy": true,
			"mutate":        true,
		}
	case agenttemplate.PrivilegeProfileCustom:
		return map[string]bool{
			"watch":         false,
			"logs":          false,
			"exec":          false,
			"helm":          false,
			"service_proxy": false,
			"mutate":        false,
		}
	default:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          true,
			"helm":          true,
			"service_proxy": true,
			"mutate":        true,
		}
	}
}

func connectionDiagnostics(connections []sqlc.AgentConnection) []agentConnectionDiagnostic {
	out := make([]agentConnectionDiagnostic, 0, len(connections))
	for _, conn := range connections {
		out = append(out, agentConnectionDiagnostic{
			ID:             conn.ID.String(),
			AgentID:        conn.AgentID,
			SessionID:      conn.SessionID,
			Status:         conn.Status,
			AgentVersion:   conn.AgentVersion,
			ConnectedAt:    conn.ConnectedAt.UTC().Format(time.RFC3339),
			LastPing:       timestampPtr(conn.LastPing),
			DisconnectedAt: timestampPtr(conn.DisconnectedAt),
			PodName:        conn.PodName,
			NodeName:       conn.NodeName,
			ChannelName:    conn.ChannelName,
		})
	}
	return out
}

func conditionDiagnostics(conditions []sqlc.ClusterCondition) []clusterConditionDiagnostic {
	out := make([]clusterConditionDiagnostic, 0, len(conditions))
	for _, condition := range conditions {
		out = append(out, clusterConditionDiagnostic{
			Type:               condition.Type,
			Status:             condition.Status,
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: condition.LastTransitionTime.UTC().Format(time.RFC3339),
			LastProbeTime:      timePtr(condition.LastProbeTime),
		})
	}
	return out
}
