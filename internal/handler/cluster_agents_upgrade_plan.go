package handler

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func upgradeRecommendation(agent clusterAgentItem) agentUpgradeRecommendation {
	if agent.AgentVersion == "" {
		return agentUpgradeRecommendation{
			Status:  "unknown",
			Message: "Agent version is not reported yet; wait for a heartbeat before planning an upgrade.",
		}
	}
	if agent.AgentStatus == "disconnected" {
		return agentUpgradeRecommendation{
			CurrentVersion: agent.AgentVersion,
			Status:         "blocked",
			Message:        "Agent must reconnect before an in-place upgrade can be coordinated.",
		}
	}
	if agent.CompatibilityStatus == "deprecated" {
		return agentUpgradeRecommendation{
			CurrentVersion: agent.AgentVersion,
			Status:         "upgrade_recommended",
			Message:        "Agent is on a deprecated compatibility track; queue an upgrade to the supported agent image.",
		}
	}
	return agentUpgradeRecommendation{
		CurrentVersion: agent.AgentVersion,
		Status:         "upgrade_trackable",
		Message:        "Queue an upgrade operation; the connected agent will patch its own Deployment image and report the result.",
	}
}

func (h *ClusterAgentHandler) buildUpgradePlan(cluster sqlc.Cluster, agent clusterAgentItem, req agentUpgradePlanRequest) (agentUpgradePlanResponse, error) {
	targetVersion := strings.TrimSpace(req.TargetVersion)
	if targetVersion == "" {
		targetVersion = h.agentImageTag
	}
	if targetVersion == "" {
		targetVersion = "latest"
	}
	targetImage := strings.TrimSpace(req.TargetImage)
	if targetImage == "" {
		targetImage = targetAgentImage(h.agentImageRepository, targetVersion)
	}
	currentImage := ""
	if agent.AgentVersion != "" {
		currentImage = targetAgentImage(h.agentImageRepository, agent.AgentVersion)
	}
	rollbackImage := strings.TrimSpace(req.RollbackImage)
	if rollbackImage == "" {
		rollbackImage = currentImage
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "agent_self_rollout"
	}
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}
	maxUnavailable := req.MaxUnavailable
	if maxUnavailable <= 0 {
		maxUnavailable = 1
	}
	canaryClusterIDs := sanitizeUpgradeCanaryIDs(req.CanaryClusterIDs)
	if len(canaryClusterIDs) == 0 && !cluster.IsLocal {
		canaryClusterIDs = []string{cluster.ID.String()}
	}
	blockers := make([]string, 0, 3)
	if agent.AgentStatus == "disconnected" {
		blockers = append(blockers, "agent is disconnected; reconnect it before rollout")
	}
	if targetImage == "" {
		blockers = append(blockers, "target image is not configured")
	}
	if cluster.IsLocal {
		blockers = append(blockers, "local management-cluster agent is upgraded with the Astronomer server release")
	}
	if maxUnavailable > batchSize {
		blockers = append(blockers, "max_unavailable cannot be greater than batch_size")
	}
	if rollbackImage == "" {
		blockers = append(blockers, "rollback image could not be inferred; provide rollback_image explicitly")
	}
	profile := agent.PrivilegeProfile
	if profile == "" {
		profile = h.agentUpgradeDefaultProfile
	}
	overrides, err := persistedAgentOverrides(cluster.AgentOverrides)
	if err != nil {
		return agentUpgradePlanResponse{}, err
	}
	configurationDigest, err := overrides.Digest()
	if err != nil {
		return agentUpgradePlanResponse{}, err
	}
	plan := agentUpgradePlanResponse{
		ClusterID:           cluster.ID.String(),
		ClusterName:         firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		CurrentVersion:      agent.AgentVersion,
		TargetVersion:       targetVersion,
		CurrentImage:        currentImage,
		TargetImage:         targetImage,
		RollbackImage:       rollbackImage,
		PrivilegeProfile:    profile,
		AgentOverrides:      overrides,
		ConfigurationDigest: configurationDigest,
		Strategy:            strategy,
		CanaryClusterIDs:    canaryClusterIDs,
		BatchSize:           batchSize,
		MaxUnavailable:      maxUnavailable,
		Ready:               len(blockers) == 0,
		Blockers:            blockers,
		PreflightChecks: []string{
			"Agent self-test returns passed or only approved warnings.",
			"Agent tunnel is connected and heartbeat/ping are fresh.",
			"Target image is configured and pullable from the adopted cluster.",
			"Rollback image is known before patching the Deployment.",
			"Canary cluster list is approved for the first rollout batch.",
			"max_unavailable is less than or equal to batch_size.",
		},
		Steps: []string{
			"Queue the upgrade operation in Astronomer.",
			"Upgrade canary clusters first and wait for post-upgrade health checks.",
			"Proceed through batches without exceeding max unavailable agents.",
			"The connected agent patches the astronomer-system/astronomer-agent Deployment to the target image.",
			"The agent reports whether the Deployment patch was accepted by the Kubernetes API.",
			"Confirm the replacement agent pod reconnects and reports the target version in Cluster Agents.",
		},
		PostUpgradeHealthChecks: []string{
			"Replacement agent pod reconnects within the rollout timeout.",
			"Cluster Agents reports the target version and supported compatibility status.",
			"Heartbeat schema version is current and heartbeat/ping freshness checks pass.",
			"Diagnostics self-test has no failed checks.",
		},
		Validation: []string{
			"Cluster Agents status returns connected for the cluster.",
			"Last heartbeat and last ping are both fresh.",
			"Privilege profile is unchanged or intentionally narrowed.",
			"Kubernetes proxy GET /version succeeds through the tunnel.",
		},
		Rollback: []string{
			"Reapply rollback_image if the new agent fails to reconnect.",
			"Keep the current registration token and CA bundle unchanged during rollback.",
			"Collect diagnostics before deleting the failed agent pod if possible.",
		},
	}
	plan.PlanDigest = agentUpgradePlanDigest(plan)
	return plan, nil
}

func agentUpgradePlanDigest(plan agentUpgradePlanResponse) string {
	plan.PlanDigest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func sanitizeUpgradeCanaryIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		out = append(out, value)
		seen[value] = true
	}
	return out
}

func respondClusterAgentError(w http.ResponseWriter, r *http.Request, err error) {
	var handlerErr *clusterAgentHandlerError
	if errors.As(err, &handlerErr) {
		RespondRequestError(w, r, handlerErr.status, handlerErr.code, handlerErr.message)
		return
	}
	RespondRequestError(w, r, http.StatusInternalServerError, apierror.ClusterAgentError, "Cluster agent request failed")
}

func agentLifecycleOperationDTO(op sqlc.AgentLifecycleOperation) agentLifecycleOperationResponse {
	return agentLifecycleOperationResponse{
		ID:             op.ID.String(),
		ClusterID:      op.ClusterID.String(),
		OperationType:  op.OperationType,
		Status:         op.Status,
		TargetVersion:  op.TargetVersion,
		TargetImage:    op.TargetImage,
		CurrentVersion: op.CurrentVersion,
		Strategy:       op.Strategy,
		OperationSpec:  op.OperationSpec,
		RequestedBy:    uuidFromPgtype(op.RequestedBy),
		StartedAt:      timestampPtr(op.StartedAt),
		CompletedAt:    timestampPtr(op.CompletedAt),
		LastError:      op.LastError,
		CreatedAt:      op.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func uuidFromPgtype(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return stringPtr(id.String())
}

func targetAgentImage(repository, tag string) string {
	repository = strings.TrimSpace(repository)
	tag = strings.TrimSpace(tag)
	if repository == "" {
		repository = "ghcr.io/alphabravo-oss/astronomer-go-agent"
	}
	if tag == "" {
		return repository
	}
	if strings.Contains(repository, "@sha256:") {
		return repository
	}
	return repository + ":" + tag
}

func timestampPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	return stringPtr(ts.Time.UTC().Format(time.RFC3339))
}

func timePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	return stringPtr(t.UTC().Format(time.RFC3339))
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func firstNonEmptyAgentValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
